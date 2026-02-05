# OAuth Authentication Support

## Summary

Add two new authentication methods to the Salesforce plugin alongside the existing username/password flow:

1. **Pre-obtained access token** — user provides `access_token` + `url`
2. **OAuth 2.0 JWT Bearer Flow** — plugin signs a JWT with a private key and exchanges it for an access token

## Precedence Chain

When multiple credential sets are configured, the plugin tries them in this order (first complete set wins):

1. **Token flow** — `access_token` is non-empty → call `client.SetSidLoc(accessToken, url)`. Requires `url`.
2. **JWT Bearer flow** — `private_key` or `private_key_file` is non-empty → build JWT, POST to Salesforce token endpoint, call `client.SetSidLoc()`. Requires `client_id`, `username`, `url`.
3. **Password flow** — `username` + `password` are non-empty → existing `client.LoginPassword()`. Unchanged.
4. **None matched** — error: "no valid authentication credentials configured".

Each branch validates its required companion fields and returns a clear error if any are missing.

## New Config Fields

3 new fields in `salesforceConfig` (`connection_config.go`):

```go
AccessToken    *string `hcl:"access_token"`
PrivateKey     *string `hcl:"private_key"`
PrivateKeyFile *string `hcl:"private_key_file"`
```

Existing fields reused:
- `url` — Salesforce instance URL (used by all three flows)
- `client_id` — connected app consumer key (used by JWT and password flows)
- `username` — Salesforce user (used by JWT and password flows)

## JWT Bearer Flow Implementation

New function `loginJWT()` in `utils.go` (~40 lines).

### JWT Construction (using `github.com/golang-jwt/jwt/v5`)

| Claim | Value |
|-------|-------|
| `iss` | `client_id` (connected app consumer key) |
| `sub` | `username` (Salesforce user to impersonate) |
| `aud` | Salesforce login URL (e.g., `https://login.salesforce.com` or `https://test.salesforce.com` for sandboxes) |
| `exp` | current time + 3 minutes (Salesforce max) |

Signing method: **RS256** (RSA-SHA256, required by Salesforce).

### Key Loading

- If `private_key` is set → parse PEM from string directly (inline takes precedence)
- Else read `private_key_file` from disk → parse PEM
- Use `jwt.ParseRSAPrivateKeyFromPEM()` to get `*rsa.PrivateKey`
- Clear error on parse failure

### Token Exchange

- HTTP POST to `{loginURL}/services/oauth2/token`
- Content-Type: `application/x-www-form-urlencoded`
- Body: `grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion={signedJWT}`
- Parse JSON response for `access_token` and `instance_url`
- On error, surface Salesforce's `error` + `error_description` fields
- Use `instance_url` from token response (Salesforce may redirect to a different instance)

### Result

Call `client.SetSidLoc(accessToken, instanceURL)`.

## Token Refresh

No automatic refresh. If a token expires mid-session, the query fails and the user reconnects. This is consistent with the current password flow behavior (SOAP session IDs also expire).

## Files Changed

| File | Change |
|------|--------|
| `salesforce/connection_config.go` | Add 3 new fields |
| `salesforce/utils.go` | Add precedence chain in `connectRaw()`, add `loginJWT()` function |
| `salesforce/integration_test.go` | Update `loadEnvAndCreateClient()` with precedence chain, add `TestIntegration_LoginJWT`, `TestIntegration_LoginToken` |
| `docs/index.md` | Document new auth methods, precedence, config examples |
| `config/salesforce.spc` | Add new fields with comments |
| `go.mod` / `go.sum` | Add `github.com/golang-jwt/jwt/v5` |

No changes to table definitions, query logic, column mapping, or any other part of the plugin.

## New Dependency

- `github.com/golang-jwt/jwt/v5` — JWT construction and RSA signing

## Config Examples

### Token flow
```hcl
connection "salesforce" {
  plugin = "salesforce"
  url           = "https://na01.salesforce.com/"
  access_token  = "00D..."
}
```

### JWT Bearer flow
```hcl
connection "salesforce" {
  plugin = "salesforce"
  url              = "https://na01.salesforce.com/"
  client_id        = "3MVG9..."
  username         = "user@example.com"
  private_key_file = "/path/to/server.key"
}
```

### Password flow (existing, unchanged)
```hcl
connection "salesforce" {
  plugin = "salesforce"
  url       = "https://na01.salesforce.com/"
  username  = "user@example.com"
  password  = "password123"
  token     = "securityToken"
}
```
