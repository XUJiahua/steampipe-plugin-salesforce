# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
make install           # Build and install plugin to ~/.steampipe/plugins/...
make test              # Run unit tests
make test-integration  # Run integration tests (requires .env with Salesforce credentials)
```

The build uses `-tags=netgo` to force pure Go network stack, avoiding cgo. Release builds (`.goreleaser.yml`) additionally set `CGO_ENABLED=0` for fully static binaries.

The binary is placed at `~/.steampipe/plugins/hub.steampipe.io/plugins/turbot/salesforce@latest/steampipe-plugin-salesforce.plugin`.

### Running a Single Test

```bash
go test ./salesforce/ -v -run TestFunctionName
go test -tags integration ./salesforce/ -v -run TestIntegration_LoginJWT -timeout 120s
```

Integration tests use `//go:build integration` and load credentials from `salesforce/.env` (see `salesforce/.env.example`).

CI runs `golangci-lint` on push to main and PRs (via `.github/workflows/golangci-lint.yml`).

## Architecture

This is a [Steampipe](https://steampipe.io) plugin that exposes Salesforce objects as SQL tables. It uses the `steampipe-plugin-sdk/v5` framework and `simpleforce` as the Salesforce API client.

### Dynamic Schema

The plugin uses **dynamic schema mode** (`plugin.SchemaModeDynamic`). Tables are built at runtime in `pluginTableDefinitions()` (`salesforce/plugin.go`):

1. **Static tables** (16 built-in): Account, Contact, Opportunity, Lead, User, etc. These have hand-defined columns in `table_salesforce_*.go` files, merged with dynamically-fetched columns from Salesforce metadata.
2. **Dynamic tables**: Created from custom Salesforce objects listed in the `objects` config parameter. Columns are entirely generated from Salesforce `SObject.Describe()` metadata.

Column metadata is fetched concurrently using goroutines with a sync.Mutex-protected map.

### Authentication

`connectRaw()` in `utils.go` supports three methods with precedence: **access_token → JWT Bearer Flow → username/password**.

- **Access Token**: Sets session ID directly via `SetSidLoc()`. Requires `url`.
- **JWT Bearer Flow**: Signs a JWT with RSA private key, exchanges it at `/services/oauth2/token`. Uses custom `salesforceJWTClaims` type to serialize `aud` as string (Salesforce rejects array format). Requires `client_id`, `username`, `private_key`/`private_key_file`, and `url`.
- **Username/Password**: Uses `simpleforce.LoginPassword()`. Requires `username`, `password`, `url`, and optionally `token` (security token).

The client is cached in the connection cache after authentication.

### Session Retry

`queryWithRetry()` and `getWithRetry()` in `utils.go` detect expired sessions (`INVALID_SESSION_ID`, `SESSION_EXPIRED`, HTTP 401) via `isSessionExpiredError()`, clear the connection cache, re-authenticate, and retry once. Access token auth cannot auto-refresh (returns a clear error directing the user to obtain a new token).

### Naming Conventions

The `naming_convention` config controls table/column naming:
- **`snake_case`** (default): Tables prefixed with `salesforce_`, columns in snake_case. Custom fields (`__c` suffix) are lowercased but not converted.
- **`api_native`**: Tables use Salesforce-native names (e.g., `Account`), columns keep API names. When active, static column definitions are skipped in favor of dynamic columns only.

### Key Code Flow

- **Entry**: `main.go` → `salesforce.Plugin()` → `pluginTableDefinitions()`
- **Static table pattern**: Each `table_salesforce_*.go` exports a function like `SalesforceAccount(ctx, dynamicMap, config)` returning `*plugin.Table` with hand-defined columns merged via `mergeTableColumns()`.
- **Dynamic table generation**: `generateDynamicTables()` in `plugin.go` maps Salesforce SOAP types to Steampipe column types (string/ID/time→STRING, date/dateTime→TIMESTAMP, boolean→BOOL, double→DOUBLE, int→INT, default→JSON).
- **Query execution**: `listSalesforceObjectsByTable()` in `table_salesforce_object.go` builds SOQL from table columns via `generateQuery()`, adds WHERE clauses from SQL qualifiers via `buildQueryFromQuals()`, and handles pagination.
- **Column name mapping**: `getSalesforceColumnName()` converts snake_case back to CamelCase for SOQL, but leaves custom fields (`__c` suffix) unchanged.

### Adding a New Static Table

Follow the pattern in any `table_salesforce_*.go` file:
1. Create `table_salesforce_<name>.go` with a function `SalesforceXxx(ctx, dynamicMap, config) *plugin.Table`
2. Define static columns, using `mergeTableColumns()` to combine with dynamic columns
3. Use `listSalesforceObjectsByTable(salesforceName, dm.salesforceColumns)` for List hydrate
4. Use `getSalesforceObjectbyID(salesforceName)` for Get hydrate
5. Register in both naming convention branches in `pluginTableDefinitions()` in `plugin.go`
