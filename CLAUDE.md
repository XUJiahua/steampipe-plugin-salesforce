# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Install

```bash
make install    # Builds and installs plugin to ~/.steampipe/plugins/...
```

This compiles all Go files in the root directory with `netgo` build tag using Go 1.24. The binary is placed at `~/.steampipe/plugins/hub.steampipe.io/plugins/turbot/salesforce@latest/steampipe-plugin-salesforce.plugin`.

`netgo` tag 强制使用纯 Go 网络栈（DNS 解析等），避免通过 cgo 依赖系统 C 库。发布构建（`.goreleaser.yml`）同时设置了 `CGO_ENABLED=0` + `-tags=netgo` 来生成完全静态的二进制，确保跨 Linux/macOS 分发时不受 glibc/musl 版本差异影响。Makefile 本地构建没有设 `CGO_ENABLED=0`，因此 `netgo` tag 是本地开发时避免网络代码走 cgo 路径的唯一保障。

There are no automated tests in this repository.

## Architecture

This is a [Steampipe](https://steampipe.io) plugin that exposes Salesforce objects as SQL tables. It uses the `steampipe-plugin-sdk/v5` framework and `simpleforce` as the Salesforce API client.

### Dynamic Schema

The plugin uses **dynamic schema mode** (`plugin.SchemaModeDynamic`). Tables are built at runtime in `pluginTableDefinitions()` (`salesforce/plugin.go`):

1. **Static tables** (15 built-in): Account, Contact, Opportunity, Lead, etc. These have hand-defined columns in `table_salesforce_*.go` files, merged with dynamically-fetched columns from Salesforce metadata.
2. **Dynamic tables**: Created from custom Salesforce objects listed in the `objects` config parameter. Columns are entirely generated from Salesforce `SObject.Describe()` metadata.

Column metadata is fetched concurrently using goroutines with a sync.Mutex-protected map.

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
- **Connection**: `connectRaw()` in `utils.go` authenticates via username/password/token, caches the client in the connection cache.

### Adding a New Static Table

Follow the pattern in any `table_salesforce_*.go` file:
1. Create `table_salesforce_<name>.go` with a function `SalesforceXxx(ctx, dynamicMap, config) *plugin.Table`
2. Define static columns, using `mergeTableColumns()` to combine with dynamic columns
3. Use `listSalesforceObjectsByTable(salesforceName, dm.salesforceColumns)` for List hydrate
4. Use `getSalesforceObjectbyID(salesforceName)` for Get hydrate
5. Register in both naming convention branches in `pluginTableDefinitions()` in `plugin.go`
