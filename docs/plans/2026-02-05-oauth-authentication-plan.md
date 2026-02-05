# OAuth Authentication Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add JWT Bearer and pre-obtained token authentication to the Salesforce plugin alongside existing username/password auth.

**Architecture:** New auth flows are implemented entirely in `utils.go` using `simpleforce.SetSidLoc()` to inject externally-obtained tokens. A precedence chain (token → JWT → password) in `connectRaw()` selects the auth method based on which config fields are populated. JWT signing uses `golang-jwt/jwt/v5`.

**Tech Stack:** Go 1.24, `github.com/golang-jwt/jwt/v5`, `simpleforce.SetSidLoc()`

---

### Task 1: Add new config fields

**Files:**
- Modify: `salesforce/connection_config.go:14-23`

**Step 1: Write the failing test**

Add to `salesforce/utils_test.go`:

```go
func TestGetConfig_NewFields(t *testing.T) {
	// Verify the struct has the new fields by setting them
	cfg := salesforceConfig{
		AccessToken:    strPtr("tok_123"),
		PrivateKey:     strPtr("-----BEGIN RSA PRIVATE KEY-----"),
		PrivateKeyFile: strPtr("/path/to/key.pem"),
	}
	if *cfg.AccessToken != "tok_123" {
		t.Errorf("AccessToken = %q, want %q", *cfg.AccessToken, "tok_123")
	}
	if *cfg.PrivateKey != "-----BEGIN RSA PRIVATE KEY-----" {
		t.Errorf("PrivateKey not set correctly")
	}
	if *cfg.PrivateKeyFile != "/path/to/key.pem" {
		t.Errorf("PrivateKeyFile not set correctly")
	}
}

func strPtr(s string) *string { return &s }
```

**Step 2: Run test to verify it fails**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestGetConfig_NewFields -v`
Expected: FAIL — `salesforceConfig` has no field `AccessToken`

**Step 3: Write minimal implementation**

In `salesforce/connection_config.go`, add three fields to the `salesforceConfig` struct after the `Token` field:

```go
type salesforceConfig struct {
	URL              *string               `hcl:"url"`
	Username         *string               `hcl:"username"`
	Password         *string               `hcl:"password"`
	Token            *string               `hcl:"token"`
	AccessToken      *string               `hcl:"access_token"`
	PrivateKey       *string               `hcl:"private_key"`
	PrivateKeyFile   *string               `hcl:"private_key_file"`
	ClientId         *string               `hcl:"client_id"`
	APIVersion       *string               `hcl:"api_version"`
	Objects          *[]string             `hcl:"objects"`
	NamingConvention *NamingConventionEnum `hcl:"naming_convention"`
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestGetConfig_NewFields -v`
Expected: PASS

**Step 5: Commit**

```bash
git add salesforce/connection_config.go salesforce/utils_test.go
git commit -m "feat: add access_token, private_key, private_key_file config fields"
```

---

### Task 2: Add `golang-jwt/jwt/v5` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

**Step 1: Add the dependency**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go get github.com/golang-jwt/jwt/v5`

**Step 2: Verify it resolves**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go mod tidy`
Expected: no errors, `go.mod` now contains `github.com/golang-jwt/jwt/v5`

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add golang-jwt/jwt/v5 dependency"
```

---

### Task 3: Implement `loginJWT()` function

**Files:**
- Modify: `salesforce/utils.go` (add new function)
- Test: `salesforce/utils_test.go` (add unit tests)

**Step 1: Write the failing test**

Add to `salesforce/utils_test.go`. We test `loginJWT` with a self-generated RSA key pair against a mock HTTP server that mimics Salesforce's `/services/oauth2/token` endpoint.

```go
import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

func generateTestRSAKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, string(pemBytes)
}

func TestLoginJWT_Success(t *testing.T) {
	_, pemStr := generateTestRSAKey(t)

	// Mock Salesforce token endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		params, _ := url.ParseQuery(string(body))
		if params.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("unexpected grant_type: %s", params.Get("grant_type"))
		}
		if params.Get("assertion") == "" {
			t.Error("assertion is empty")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"mock_token_123","instance_url":"https://na99.salesforce.com"}`))
	}))
	defer server.Close()

	accessToken, instanceURL, err := loginJWT(server.URL, "test_client_id", "user@example.com", pemStr)
	if err != nil {
		t.Fatalf("loginJWT failed: %v", err)
	}
	if accessToken != "mock_token_123" {
		t.Errorf("access_token = %q, want %q", accessToken, "mock_token_123")
	}
	if instanceURL != "https://na99.salesforce.com" {
		t.Errorf("instance_url = %q, want %q", instanceURL, "https://na99.salesforce.com")
	}
}

func TestLoginJWT_ServerError(t *testing.T) {
	_, pemStr := generateTestRSAKey(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"user hasn't approved this consumer"}`))
	}))
	defer server.Close()

	_, _, err := loginJWT(server.URL, "test_client_id", "user@example.com", pemStr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error should contain 'invalid_grant', got: %v", err)
	}
}

func TestLoginJWT_BadKey(t *testing.T) {
	_, _, err := loginJWT("https://login.salesforce.com", "cid", "user@example.com", "not-a-pem-key")
	if err == nil {
		t.Fatal("expected error for bad PEM key, got nil")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestLoginJWT -v`
Expected: FAIL — `loginJWT` undefined

**Step 3: Write minimal implementation**

Add to `salesforce/utils.go`:

```go
import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// loginJWT performs the OAuth 2.0 JWT Bearer flow.
// loginURL is the Salesforce token endpoint base (e.g. "https://login.salesforce.com").
// Returns the access_token and instance_url from the token response.
func loginJWT(loginURL, clientID, username, privateKeyPEM string) (string, string, error) {
	// Parse the RSA private key
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", "", fmt.Errorf("failed to decode PEM block from private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 as fallback
		keyIface, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return "", "", fmt.Errorf("failed to parse private key: %v (PKCS1: %v)", err2, err)
		}
		var ok bool
		key, ok = keyIface.(*rsa.PrivateKey)
		if !ok {
			return "", "", fmt.Errorf("PKCS8 key is not RSA")
		}
	}

	// Build JWT claims
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    clientID,
		Subject:   username,
		Audience:  jwt.ClaimStrings{loginURL},
		ExpiresAt: jwt.NewNumericDate(now.Add(3 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedJWT, err := token.SignedString(key)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign JWT: %v", err)
	}

	// POST to token endpoint
	tokenURL := loginURL + "/services/oauth2/token"
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signedJWT},
	}

	resp, err := http.PostForm(tokenURL, form)
	if err != nil {
		return "", "", fmt.Errorf("token request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read token response: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("failed to parse token response: %v", err)
	}

	if errMsg, ok := result["error"]; ok {
		desc, _ := result["error_description"].(string)
		return "", "", fmt.Errorf("salesforce OAuth error: %s: %s", errMsg, desc)
	}

	accessToken, _ := result["access_token"].(string)
	instanceURL, _ := result["instance_url"].(string)
	if accessToken == "" {
		return "", "", fmt.Errorf("token response missing access_token")
	}

	return accessToken, instanceURL, nil
}
```

Note: add `"crypto/rsa"`, `"crypto/x509"`, `"encoding/pem"`, `"io"`, `"net/http"`, `"net/url"`, `"time"`, and `"github.com/golang-jwt/jwt/v5"` to the imports in `utils.go`.

**Step 4: Run tests to verify they pass**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestLoginJWT -v`
Expected: PASS (all 3 tests)

**Step 5: Commit**

```bash
git add salesforce/utils.go salesforce/utils_test.go
git commit -m "feat: implement loginJWT for OAuth 2.0 JWT Bearer flow"
```

---

### Task 4: Implement `loadPrivateKey()` helper

**Files:**
- Modify: `salesforce/utils.go`
- Test: `salesforce/utils_test.go`

**Step 1: Write the failing test**

Add to `salesforce/utils_test.go`:

```go
import "os"

func TestLoadPrivateKey_InlineString(t *testing.T) {
	_, pemStr := generateTestRSAKey(t)
	got, err := loadPrivateKey(&pemStr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty PEM string")
	}
}

func TestLoadPrivateKey_FromFile(t *testing.T) {
	_, pemStr := generateTestRSAKey(t)
	tmpFile, err := os.CreateTemp("", "test-key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(pemStr)
	tmpFile.Close()

	filePath := tmpFile.Name()
	got, err := loadPrivateKey(nil, &filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty PEM string")
	}
}

func TestLoadPrivateKey_InlineTakesPrecedence(t *testing.T) {
	_, pemStr := generateTestRSAKey(t)
	bogusFile := "/nonexistent/path.pem"
	got, err := loadPrivateKey(&pemStr, &bogusFile)
	if err != nil {
		t.Fatalf("inline should take precedence, got error: %v", err)
	}
	if got != pemStr {
		t.Error("expected inline key to be returned")
	}
}

func TestLoadPrivateKey_BothNil(t *testing.T) {
	_, err := loadPrivateKey(nil, nil)
	if err == nil {
		t.Error("expected error when both are nil")
	}
}

func TestLoadPrivateKey_FileNotFound(t *testing.T) {
	path := "/nonexistent/key.pem"
	_, err := loadPrivateKey(nil, &path)
	if err == nil {
		t.Error("expected error for missing file")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestLoadPrivateKey -v`
Expected: FAIL — `loadPrivateKey` undefined

**Step 3: Write minimal implementation**

Add to `salesforce/utils.go`:

```go
import "os"

// loadPrivateKey returns the PEM string from either inline config or file.
// Inline takes precedence over file.
func loadPrivateKey(privateKey *string, privateKeyFile *string) (string, error) {
	if privateKey != nil && *privateKey != "" {
		return *privateKey, nil
	}
	if privateKeyFile != nil && *privateKeyFile != "" {
		data, err := os.ReadFile(*privateKeyFile)
		if err != nil {
			return "", fmt.Errorf("failed to read private key file %q: %v", *privateKeyFile, err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("either private_key or private_key_file must be set")
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestLoadPrivateKey -v`
Expected: PASS (all 5 tests)

**Step 5: Commit**

```bash
git add salesforce/utils.go salesforce/utils_test.go
git commit -m "feat: add loadPrivateKey helper for inline/file PEM loading"
```

---

### Task 5: Implement `loginURL()` helper

**Files:**
- Modify: `salesforce/utils.go`
- Test: `salesforce/utils_test.go`

**Step 1: Write the failing test**

Add to `salesforce/utils_test.go`:

```go
func TestLoginURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"production", "https://na01.salesforce.com/", "https://login.salesforce.com"},
		{"production no slash", "https://na01.salesforce.com", "https://login.salesforce.com"},
		{"sandbox", "https://mycompany--dev.sandbox.my.salesforce.com/", "https://test.salesforce.com"},
		{"sandbox cs", "https://cs42.salesforce.com/", "https://test.salesforce.com"},
		{"test keyword", "https://test.salesforce.com/", "https://test.salesforce.com"},
		{"my.salesforce.com prod", "https://mycompany.my.salesforce.com/", "https://login.salesforce.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loginURL(tt.input)
			if got != tt.expected {
				t.Errorf("loginURL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestLoginURL -v`
Expected: FAIL — `loginURL` undefined

**Step 3: Write minimal implementation**

Add to `salesforce/utils.go`:

```go
// loginURL determines the Salesforce login endpoint based on the instance URL.
// Sandbox instances use test.salesforce.com, production uses login.salesforce.com.
func loginURL(instanceURL string) string {
	lower := strings.ToLower(instanceURL)
	if strings.Contains(lower, "sandbox") || strings.Contains(lower, "test.salesforce.com") || strings.Contains(lower, "cs") && strings.Contains(lower, ".salesforce.com") {
		return "https://test.salesforce.com"
	}
	return "https://login.salesforce.com"
}
```

Wait — the `cs` heuristic is too broad (could match "success.salesforce.com"). Let me refine:

```go
import "regexp"

var sandboxPattern = regexp.MustCompile(`(?i)(sandbox|\.cs\d+\.|test\.salesforce\.com)`)

// loginURL determines the Salesforce login endpoint based on the instance URL.
// Sandbox instances use test.salesforce.com, production uses login.salesforce.com.
func loginURL(instanceURL string) string {
	if sandboxPattern.MatchString(instanceURL) {
		return "https://test.salesforce.com"
	}
	return "https://login.salesforce.com"
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -run TestLoginURL -v`
Expected: PASS (all 6 tests)

**Step 5: Commit**

```bash
git add salesforce/utils.go salesforce/utils_test.go
git commit -m "feat: add loginURL helper for sandbox detection"
```

---

### Task 6: Rewrite `connectRaw()` with precedence chain

**Files:**
- Modify: `salesforce/utils.go:22-87`

**Step 1: Write the failing test**

We can't easily unit-test `connectRaw()` directly (it needs `plugin.Connection` and cache), but we can verify the build compiles and existing tests still pass. The real validation is in integration tests (Task 8). For now, verify the refactored code compiles.

**Step 2: Rewrite `connectRaw()`**

Replace the body of `connectRaw()` in `salesforce/utils.go` (lines 22-87) with:

```go
func connectRaw(ctx context.Context, cc *connection.ConnectionCache, c *plugin.Connection) (*simpleforce.Client, error) {
	// Load connection from cache
	cacheKey := "simpleforce"
	if cc != nil {
		if cachedData, ok := cc.Get(ctx, cacheKey); ok {
			return cachedData.(*simpleforce.Client), nil
		}
	}

	config := GetConfig(c)
	apiVersion := simpleforce.DefaultAPIVersion
	clientID := "steampipe"

	if config.ClientId != nil {
		clientID = *config.ClientId
	}
	if config.APIVersion != nil {
		apiVersion = *config.APIVersion
	}

	// Precedence 1: Pre-obtained access token
	if config.AccessToken != nil && *config.AccessToken != "" {
		if config.URL == nil || *config.URL == "" {
			return nil, fmt.Errorf("access_token auth requires 'url' to be set")
		}
		client := simpleforce.NewClient(*config.URL, clientID, apiVersion)
		if client == nil {
			return nil, fmt.Errorf("failed to create salesforce client")
		}
		client.SetSidLoc(*config.AccessToken, *config.URL)

		if cc != nil {
			if err := cc.Set(ctx, cacheKey, client); err != nil {
				plugin.Logger(ctx).Error("connectRaw", "cache-set", err)
			}
		}
		return client, nil
	}

	// Precedence 2: JWT Bearer flow
	if config.PrivateKey != nil && *config.PrivateKey != "" || config.PrivateKeyFile != nil && *config.PrivateKeyFile != "" {
		if config.URL == nil || *config.URL == "" {
			return nil, fmt.Errorf("jwt auth requires 'url' to be set")
		}
		if config.Username == nil || *config.Username == "" {
			return nil, fmt.Errorf("jwt auth requires 'username' to be set")
		}
		if clientID == "steampipe" && config.ClientId == nil {
			return nil, fmt.Errorf("jwt auth requires 'client_id' to be set")
		}

		pemKey, err := loadPrivateKey(config.PrivateKey, config.PrivateKeyFile)
		if err != nil {
			return nil, err
		}

		loginBase := loginURL(*config.URL)
		accessToken, instanceURL, err := loginJWT(loginBase, clientID, *config.Username, pemKey)
		if err != nil {
			return nil, fmt.Errorf("jwt login failed: %v", err)
		}

		client := simpleforce.NewClient(instanceURL, clientID, apiVersion)
		if client == nil {
			return nil, fmt.Errorf("failed to create salesforce client")
		}
		client.SetSidLoc(accessToken, instanceURL)

		if cc != nil {
			if err := cc.Set(ctx, cacheKey, client); err != nil {
				plugin.Logger(ctx).Error("connectRaw", "cache-set", err)
			}
		}
		return client, nil
	}

	// Precedence 3: Username/Password flow (existing)
	if config.Username != nil && *config.Username != "" && config.Password != nil && *config.Password != "" {
		if config.URL == nil || *config.URL == "" {
			return nil, fmt.Errorf("password auth requires 'url' to be set")
		}
		securityToken := ""
		if config.Token != nil {
			securityToken = *config.Token
		}

		client := simpleforce.NewClient(*config.URL, clientID, apiVersion)
		if client == nil {
			return nil, fmt.Errorf("failed to create salesforce client")
		}

		err := client.LoginPassword(*config.Username, *config.Password, securityToken)
		if err != nil {
			return nil, fmt.Errorf("password login failed: %v", err)
		}

		if cc != nil {
			if err := cc.Set(ctx, cacheKey, client); err != nil {
				plugin.Logger(ctx).Error("connectRaw", "cache-set", err)
			}
		}
		return client, nil
	}

	return nil, fmt.Errorf("no valid authentication credentials configured; provide access_token, private_key/private_key_file, or username/password")
}
```

**Step 3: Verify it compiles and existing tests pass**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go build ./... && go test ./salesforce/ -v -count=1`
Expected: BUILD OK, all existing unit tests PASS

**Step 4: Commit**

```bash
git add salesforce/utils.go
git commit -m "feat: rewrite connectRaw with token/JWT/password precedence chain"
```

---

### Task 7: Update integration tests

**Files:**
- Modify: `salesforce/integration_test.go`

**Step 1: Update `loadEnvAndCreateClient()` with precedence chain**

Replace the function and add new test functions:

```go
func loadEnvAndCreateClient(t *testing.T) *simpleforce.Client {
	t.Helper()

	_ = godotenv.Load("../.env")

	url := os.Getenv("SALESFORCE_URL")
	accessToken := os.Getenv("SALESFORCE_ACCESS_TOKEN")
	privateKey := os.Getenv("SALESFORCE_PRIVATE_KEY")
	privateKeyFile := os.Getenv("SALESFORCE_PRIVATE_KEY_FILE")
	username := os.Getenv("SALESFORCE_USERNAME")
	password := os.Getenv("SALESFORCE_PASSWORD")
	token := os.Getenv("SALESFORCE_TOKEN")
	clientID := os.Getenv("SALESFORCE_CLIENT_ID")

	if clientID == "" {
		clientID = "steampipe"
	}

	apiVersion := simpleforce.DefaultAPIVersion

	// Precedence 1: Pre-obtained access token
	if accessToken != "" {
		if url == "" {
			t.Fatal("SALESFORCE_ACCESS_TOKEN requires SALESFORCE_URL")
		}
		client := simpleforce.NewClient(url, clientID, apiVersion)
		if client == nil {
			t.Fatal("failed to create simpleforce client")
		}
		client.SetSidLoc(accessToken, url)
		return client
	}

	// Precedence 2: JWT Bearer flow
	if privateKey != "" || privateKeyFile != "" {
		if url == "" || username == "" {
			t.Fatal("JWT auth requires SALESFORCE_URL and SALESFORCE_USERNAME")
		}
		pemKey, err := loadPrivateKey(&privateKey, &privateKeyFile)
		if err != nil {
			t.Fatalf("failed to load private key: %v", err)
		}
		loginBase := loginURL(url)
		at, instanceURL, err := loginJWT(loginBase, clientID, username, pemKey)
		if err != nil {
			t.Fatalf("JWT login failed: %v", err)
		}
		client := simpleforce.NewClient(instanceURL, clientID, apiVersion)
		if client == nil {
			t.Fatal("failed to create simpleforce client")
		}
		client.SetSidLoc(at, instanceURL)
		return client
	}

	// Precedence 3: Username/Password
	if url == "" || username == "" || password == "" {
		t.Skip("no valid auth credentials set (need SALESFORCE_ACCESS_TOKEN, SALESFORCE_PRIVATE_KEY/_FILE, or SALESFORCE_USERNAME+PASSWORD)")
	}

	client := simpleforce.NewClient(url, clientID, apiVersion)
	if client == nil {
		t.Fatal("failed to create simpleforce client")
	}

	err := client.LoginPassword(username, password, token)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	return client
}

func TestIntegration_LoginToken(t *testing.T) {
	_ = godotenv.Load("../.env")
	if os.Getenv("SALESFORCE_ACCESS_TOKEN") == "" {
		t.Skip("SALESFORCE_ACCESS_TOKEN not set")
	}
	client := loadEnvAndCreateClient(t)
	if client == nil {
		t.Fatal("client is nil after token auth")
	}
	// Verify the token works by running a simple query
	result, err := client.Query("SELECT Id FROM Organization")
	if err != nil {
		t.Fatalf("query failed with token auth: %v", err)
	}
	t.Logf("token auth: Organization Id = %s", result.Records[0].ID())
}

func TestIntegration_LoginJWT(t *testing.T) {
	_ = godotenv.Load("../.env")
	if os.Getenv("SALESFORCE_PRIVATE_KEY") == "" && os.Getenv("SALESFORCE_PRIVATE_KEY_FILE") == "" {
		t.Skip("SALESFORCE_PRIVATE_KEY/_FILE not set")
	}
	client := loadEnvAndCreateClient(t)
	if client == nil {
		t.Fatal("client is nil after JWT auth")
	}
	result, err := client.Query("SELECT Id FROM Organization")
	if err != nil {
		t.Fatalf("query failed with JWT auth: %v", err)
	}
	t.Logf("JWT auth: Organization Id = %s", result.Records[0].ID())
}
```

**Step 2: Verify it compiles**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go build ./... && go vet ./...`
Expected: no errors

**Step 3: Commit**

```bash
git add salesforce/integration_test.go
git commit -m "feat: update integration tests with OAuth auth precedence chain"
```

---

### Task 8: Update config documentation

**Files:**
- Modify: `config/salesforce.spc`
- Modify: `docs/index.md`

**Step 1: Update `config/salesforce.spc`**

Add the new fields with comments:

```hcl
connection "salesforce" {
  plugin = "salesforce"

  # Salesforce instance URL, e.g., "https://na01.salesforce.com/"
  # url = "https://na01.salesforce.com/"

  # Authentication method is auto-detected based on which credentials are provided.
  # Precedence: access_token > private_key/private_key_file (JWT) > username/password

  # Option 1: Pre-obtained OAuth access token
  # access_token = "00D..."

  # Option 2: JWT Bearer flow - requires client_id, username, and private key
  # private_key_file = "/path/to/server.key"
  # private_key = "-----BEGIN RSA PRIVATE KEY-----\n..."

  # Option 3: Username/Password flow
  # username = "user@example.com"
  # password = "Dummy@~Password"

  # The Salesforce security token is only required If the client's IP address is not added to the organization's list of trusted IPs
  # https://help.salesforce.com/s/articleView?id=sf.security_networkaccess.htm&type=5
  # token = "ABO5C3PNqOP0BHsPFakeToken"

  # Salesforce client ID of the connected app (used by JWT and password flows)
  # client_id = "3MVG99E3Ry5mh4z_FakeID"

  # List of Salesforce object names to generate additional tables for
  # objects = ["AccountBrand", "OpportunityStage", "CustomApp__c"]

  # Salesforce API version to connect to
  # api_version = "43.0"

  # naming_convention = "snake_case"
}
```

**Step 2: Update `docs/index.md` Credentials section**

After the existing "### Credentials" section, add a new "### Authentication" section documenting all three methods with config examples (use the examples from the design doc). Keep it concise.

**Step 3: Commit**

```bash
git add config/salesforce.spc docs/index.md
git commit -m "docs: document OAuth authentication methods and config examples"
```

---

### Task 9: Run full test suite and verify build

**Step 1: Run all unit tests**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go test ./salesforce/ -v -count=1`
Expected: all unit tests PASS

**Step 2: Run full build**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && make install`
Expected: builds successfully, binary installed

**Step 3: Run go vet**

Run: `cd /home/jiahua/workspace/postgresql_fdw/refs/steampipe-plugin-salesforce && go vet ./...`
Expected: no issues
