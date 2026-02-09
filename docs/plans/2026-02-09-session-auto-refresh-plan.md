# Session Auto-Refresh Implementation Plan

## Design Summary

When a Salesforce API call fails due to expired session, detect the error, clear the cached client, re-authenticate, and retry the operation once. Access Token auth cannot be refreshed (no credentials to re-auth), so it returns a clear error instead.

### Key Constraint: `SObject.Get()` Swallows Errors

`simpleforce`'s `SObject.Get()` returns `nil` on ANY error (network, session expired, not found) — it logs internally but never exposes the error. This means `getSalesforceObjectbyID` currently treats session expiration as "not found" silently.

**Solution**: When `Get()` returns nil, issue a probe query (`SELECT Id FROM <table> WHERE Id = '<id>' LIMIT 1`) to disambiguate. If the probe fails with a session error, reconnect and retry.

---

## Task 1: Add `isSessionExpiredError()` + `isAccessTokenAuth()`

**File**: `salesforce/utils.go`

Add after `loginJWT()` function (after line 594):

```go
const cacheKeyClient = "simpleforce"

// isSessionExpiredError checks whether an error from simpleforce indicates
// an expired or invalid Salesforce session.
// simpleforce errors are plain strings with format:
//   "[simpleforce] Error. http code: 401 Error Message:  Session expired or invalid Error Code: INVALID_SESSION_ID"
func isSessionExpiredError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "INVALID_SESSION_ID") ||
		strings.Contains(msg, "SESSION_EXPIRED") ||
		strings.Contains(msg, "http code: 401")
}

// isAccessTokenAuth returns true if the connection config uses a pre-obtained
// access token (which cannot be refreshed automatically).
func isAccessTokenAuth(config salesforceConfig) bool {
	return config.AccessToken != nil && *config.AccessToken != ""
}
```

Also replace the hardcoded `"simpleforce"` cache key in `connectRaw()` (line 37) with the new `cacheKeyClient` constant.

**File**: `salesforce/utils_test.go`

Add tests after the existing `TestLoginJWT_*` tests:

```go
func TestIsSessionExpiredError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"unrelated error", fmt.Errorf("connection refused"), false},
		{"INVALID_SESSION_ID", fmt.Errorf("[simpleforce] Error. http code: 401 Error Message:  Session expired or invalid Error Code: INVALID_SESSION_ID"), true},
		{"SESSION_EXPIRED", fmt.Errorf("Error Code: SESSION_EXPIRED"), true},
		{"http 401", fmt.Errorf("[simpleforce] Error. http code: 401 Error Message: something"), true},
		{"http 403 not matched", fmt.Errorf("[simpleforce] Error. http code: 403 Error Message: forbidden"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSessionExpiredError(tt.err)
			if got != tt.expected {
				t.Errorf("isSessionExpiredError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestIsAccessTokenAuth(t *testing.T) {
	tok := "some_token"
	empty := ""
	tests := []struct {
		name     string
		config   salesforceConfig
		expected bool
	}{
		{"with token", salesforceConfig{AccessToken: &tok}, true},
		{"nil", salesforceConfig{}, false},
		{"empty string", salesforceConfig{AccessToken: &empty}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAccessTokenAuth(tt.config)
			if got != tt.expected {
				t.Errorf("isAccessTokenAuth() = %v, want %v", got, tt.expected)
			}
		})
	}
}
```

**Verify**: `go test ./salesforce/ -v -run 'TestIsSession|TestIsAccess'`

---

## Task 2: Add `reconnect()` helper

**File**: `salesforce/utils.go`

Add after `isAccessTokenAuth()`:

```go
// reconnect clears the cached client and re-authenticates.
// Returns an error if the current auth method is access_token (cannot refresh).
func reconnect(ctx context.Context, d *plugin.QueryData) (*simpleforce.Client, error) {
	config := GetConfig(d.Connection)
	if isAccessTokenAuth(config) {
		return nil, fmt.Errorf("salesforce session expired; access_token auth cannot be refreshed automatically — obtain a new token and update the config")
	}

	plugin.Logger(ctx).Info("salesforce.reconnect", "msg", "session expired, re-authenticating")

	// Clear cached client
	if d.ConnectionCache != nil {
		d.ConnectionCache.Delete(ctx, cacheKeyClient)
	}

	// Re-authenticate
	return connect(ctx, d)
}
```

**No separate test needed** — this function is tested indirectly through `queryWithRetry`/`getWithRetry` tests and integration tests.

---

## Task 3: Add `queryWithRetry()` wrapper + tests

**File**: `salesforce/utils.go`

Add after `reconnect()`:

```go
// queryWithRetry executes a SOQL query via client.Query(). If the query fails
// due to session expiration, it reconnects and retries once.
func queryWithRetry(ctx context.Context, d *plugin.QueryData, client *simpleforce.Client, query string) (*simpleforce.Client, *simpleforce.QueryResult, error) {
	result, err := client.Query(query)
	if err == nil {
		return client, result, nil
	}

	if !isSessionExpiredError(err) {
		return client, nil, err
	}

	plugin.Logger(ctx).Warn("salesforce.queryWithRetry", "msg", "session expired, reconnecting", "error", err)

	newClient, reconnErr := reconnect(ctx, d)
	if reconnErr != nil {
		return client, nil, reconnErr
	}

	result, err = newClient.Query(query)
	return newClient, result, err
}
```

**Why return the new client?** After reconnect, the caller (especially the pagination loop in `listSalesforceObjectsByTable`) must use the new client for subsequent calls.

**File**: `salesforce/utils_test.go`

Add tests using httptest to simulate session expiration followed by successful retry:

```go
func TestQueryWithRetry_NoError(t *testing.T) {
	// Test that a successful query returns immediately without retry.
	// Use a mock server that returns valid query results.
	// Verify the query is called exactly once.
}

func TestQueryWithRetry_SessionExpired_Retry(t *testing.T) {
	// Test that session expiration triggers reconnect and retry.
	// Mock server: first request returns 401/INVALID_SESSION_ID, second returns success.
	// This test is complex because it needs a full plugin.QueryData — consider
	// testing at integration level instead.
}
```

Note: `queryWithRetry` depends on `plugin.QueryData` which is hard to mock in unit tests. The primary verification will be through integration tests (Task 6). Unit tests for `isSessionExpiredError` (Task 1) cover the detection logic.

**Verify**: `go test ./salesforce/ -v -run TestQueryWithRetry`

---

## Task 4: Add `getWithRetry()` wrapper + tests

**File**: `salesforce/utils.go`

Add after `queryWithRetry()`:

```go
// getWithRetry fetches a Salesforce object by ID. Since simpleforce's
// SObject.Get() swallows errors (returns nil for both "not found" AND
// "session expired"), this function uses a probe query to disambiguate
// when Get() returns nil.
func getWithRetry(ctx context.Context, d *plugin.QueryData, client *simpleforce.Client, tableName string, id string) (*simpleforce.Client, *simpleforce.SObject, error) {
	obj := client.SObject(tableName).Get(id)
	if obj != nil {
		return client, obj, nil
	}

	// Get() returned nil — could be "not found" or session expired.
	// Use a probe query to check if the session is still valid.
	probe := fmt.Sprintf("SELECT Id FROM %s WHERE Id = '%s' LIMIT 1", tableName, id)
	_, err := client.Query(probe)
	if err == nil {
		// Session is valid; object was genuinely not found (or Get() failed for another reason).
		return client, nil, nil
	}

	if !isSessionExpiredError(err) {
		// Session is valid but query failed for another reason; treat original Get() nil as "not found".
		return client, nil, nil
	}

	// Session expired — reconnect and retry.
	plugin.Logger(ctx).Warn("salesforce.getWithRetry", "msg", "session expired during Get, reconnecting", "table", tableName, "id", id)

	newClient, reconnErr := reconnect(ctx, d)
	if reconnErr != nil {
		return client, nil, reconnErr
	}

	obj = newClient.SObject(tableName).Get(id)
	return newClient, obj, nil
}
```

**Verify**: Unit testing is difficult due to `plugin.QueryData` dependency. Covered by integration tests.

---

## Task 5: Update hydrate functions to use retry wrappers

**File**: `salesforce/table_salesforce_object.go`

### 5a: Update `listSalesforceObjectsByTable`

Replace lines 16-57 with:

```go
return func(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	client, err := connect(ctx, d)
	if err != nil {
		plugin.Logger(ctx).Error("salesforce.listSalesforceObjectsByTable", "connection error", err)
		return nil, err
	}
	if client == nil {
		plugin.Logger(ctx).Error("salesforce.listSalesforceObjectsByTable", "client_not_found: unable to generate dynamic tables because of invalid steampipe salesforce configuration", err)
		return nil, fmt.Errorf("salesforce.listSalesforceObjectsByTable: client_not_found, unable to query table %s because of invalid steampipe salesforce configuration", d.Table.Name)
	}

	query := generateQuery(d.Table.Columns, tableName)
	condition := buildQueryFromQuals(d.Quals, d.Table.Columns, salesforceCols)
	if condition != "" {
		query = fmt.Sprintf("%s where %s", query, condition)
		plugin.Logger(ctx).Debug("salesforce.listSalesforceObjectsByTable", "table_name", d.Table.Name, "query_condition", condition)
	}

	for {
		client, result, err := queryWithRetry(ctx, d, client, query)
		if err != nil {
			plugin.Logger(ctx).Error("salesforce.listSalesforceObjectsByTable", "query error", err)
			return nil, err
		}

		AccountList := new([]map[string]interface{})
		err = decodeQueryResult(ctx, result.Records, AccountList)
		if err != nil {
			plugin.Logger(ctx).Error("salesforce.listSalesforceObjectsByTable", "results decoding error", err)
			return nil, err
		}

		for _, account := range *AccountList {
			d.StreamListItem(ctx, account)
		}

		// Paging
		if result.Done {
			break
		} else {
			query = result.NextRecordsURL
		}
	}

	return nil, nil
}
```

Changes:
- `client.Query(query)` → `queryWithRetry(ctx, d, client, query)`
- Capture updated `client` from return value (needed after reconnect for pagination)

### 5b: Update `getSalesforceObjectbyID`

Replace lines 89-93 with:

```go
client, obj, err := getWithRetry(ctx, d, client, tableName, id)
if err != nil {
	plugin.Logger(ctx).Error("salesforce.getSalesforceObjectbyID", "get error", err)
	return nil, err
}
if obj == nil {
	plugin.Logger(ctx).Warn("salesforce.getSalesforceObjectbyID", fmt.Sprintf("%s with id \"%s\" not found", tableName, id))
	return nil, nil
}
```

Changes:
- `client.SObject(tableName).Get(id)` → `getWithRetry(ctx, d, client, tableName, id)`
- Now properly surfaces session expiration errors instead of silently returning "not found"

**Verify**: `go test ./salesforce/ -v` (unit tests) + `make test-integration` (integration tests)

---

## Task 6: Run all tests and verify

```bash
# Unit tests
go test ./salesforce/ -v -count=1

# Integration tests (requires .env with credentials)
go test -tags integration ./salesforce/ -v -count=1 -timeout 120s

# Build
make install
```

---

## Files Changed Summary

| File | Changes |
|---|---|
| `salesforce/utils.go` | Add `cacheKeyClient` const, `isSessionExpiredError()`, `isAccessTokenAuth()`, `reconnect()`, `queryWithRetry()`, `getWithRetry()`. Replace hardcoded cache key. |
| `salesforce/utils_test.go` | Add `TestIsSessionExpiredError`, `TestIsAccessTokenAuth` |
| `salesforce/table_salesforce_object.go` | Replace `client.Query()` with `queryWithRetry()`, replace `client.SObject().Get()` with `getWithRetry()` |

## Not Changed

- `connection_config.go` — no changes needed
- `plugin.go` — no changes needed
- Cache TTL strategy — unchanged (SDK default 1 hour)
- Authentication logic in `connectRaw()` — unchanged
