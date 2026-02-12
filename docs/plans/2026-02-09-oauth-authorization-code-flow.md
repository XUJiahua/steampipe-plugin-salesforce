# OAuth Authorization Code Flow (Web Admin Authorization)

> **Status:** Future — 记录方案 B 设计，日后需要支持时参考。
>
> **前置条件:** 方案 A（JWT Bearer Flow + access_token）已实现，见 `2026-02-05-oauth-authentication-design.md`。

## 背景

当前插件支持三种认证方式（优先级: access_token > JWT > password），均为 Server-to-Server 模式，凭证写在配置文件中。方案 B 的目标是让管理员在网页上点击"连接 Salesforce"按钮，通过浏览器交互完成 OAuth 授权，无需手动填写 token 或私钥。

## 适用场景

- 多租户 SaaS 平台，每个客户连接自己的 Salesforce org
- 需要终端用户身份（而非服务账号）执行操作
- 不希望管理员接触私钥或 API 凭证

## OAuth Authorization Code Flow 概述

```
管理员浏览器                Web 后端                    Salesforce
     |                        |                           |
     |-- 点击"连接SF" ------->|                           |
     |                        |-- 302 重定向 ------------->|
     |<-- Salesforce 登录页 --|                           |
     |-- 用户登录并授权 ----->|                           |
     |                        |<-- callback?code=xxx -----|
     |                        |-- POST /token (code) ---->|
     |                        |<-- access_token + --------|
     |                        |    refresh_token           |
     |<-- 授权成功 ----------|                           |
```

### Token 生命周期

| Token | 有效期 | 过期处理 |
|-------|--------|----------|
| `access_token` | ~2 小时（由 Salesforce Session Settings 控制） | 用 `refresh_token` 换取新的 `access_token` |
| `refresh_token` | **默认永不过期** | 见下方「refresh_token 失效场景」 |
| `authorization_code` | 15 分钟 | 一次性使用，过期需重新授权 |

### refresh_token 失效场景

refresh_token 默认不过期，但以下情况会失效：

1. **Connected App 设置了 Refresh Token Policy**（如 expire after 90 days）
2. **用户手动撤销授权**（Setup > Connected Apps OAuth Usage > Revoke）
3. **管理员撤销所有 tokens**（Connected App 管理页面）
4. **用户修改密码**（取决于 org 的 policy）
5. **Connected App 的 client_secret 被轮换**

**失效时的处理：无法自动恢复，必须用户重新走一次浏览器授权流程。** 前端应检测 refresh 失败并提示管理员重新授权。

## 插件层改动

### 新增 config 字段

在 `salesforce/connection_config.go` 的 `salesforceConfig` 中新增：

```go
RefreshToken *string `hcl:"refresh_token"`
ClientSecret *string `hcl:"client_secret"`
```

### 修改 `connectRaw()` 优先级链

新增 Precedence 1.5（在 access_token 之后、JWT 之前）：

```
access_token > refresh_token > private_key/JWT > username/password
```

```go
// Precedence 1.5: Refresh Token flow
if config.RefreshToken != nil && *config.RefreshToken != "" {
    if config.URL == nil || *config.URL == "" {
        return nil, fmt.Errorf("refresh_token auth requires 'url'")
    }
    if config.ClientId == nil || *config.ClientId == "" {
        return nil, fmt.Errorf("refresh_token auth requires 'client_id'")
    }
    // client_secret is required for web server flow refresh
    if config.ClientSecret == nil || *config.ClientSecret == "" {
        return nil, fmt.Errorf("refresh_token auth requires 'client_secret'")
    }

    accessToken, instanceURL, err := refreshAccessToken(
        loginURL(*config.URL),
        *config.ClientId,
        *config.ClientSecret,
        *config.RefreshToken,
    )
    if err != nil {
        return nil, fmt.Errorf("refresh_token login failed: %v", err)
    }

    client := simpleforce.NewClient(instanceURL, clientID, apiVersion)
    client.SetSidLoc(accessToken, instanceURL)
    // cache...
    return client, nil
}
```

### 新增 `refreshAccessToken()` 函数

```go
// refreshAccessToken exchanges a refresh_token for a new access_token.
func refreshAccessToken(loginEndpoint, clientID, clientSecret, refreshToken string) (string, string, error) {
    tokenURL := loginEndpoint + "/services/oauth2/token"
    form := url.Values{
        "grant_type":    {"refresh_token"},
        "client_id":     {clientID},
        "client_secret": {clientSecret},
        "refresh_token": {refreshToken},
    }

    resp, err := http.PostForm(tokenURL, form)
    if err != nil {
        return "", "", fmt.Errorf("refresh request failed: %v", err)
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    var result map[string]interface{}
    json.Unmarshal(body, &result)

    if errMsg, ok := result["error"]; ok {
        desc, _ := result["error_description"].(string)
        return "", "", fmt.Errorf("OAuth error: %s: %s", errMsg, desc)
    }

    accessToken, _ := result["access_token"].(string)
    instanceURL, _ := result["instance_url"].(string)
    if accessToken == "" {
        return "", "", fmt.Errorf("response missing access_token")
    }
    if instanceURL == "" {
        return "", "", fmt.Errorf("response missing instance_url")
    }

    return accessToken, instanceURL, nil
}
```

### 修改 `reconnect()` 支持 refresh_token 自动续期

当前 `reconnect()` 只拒绝 `access_token` 模式（无法刷新）。`refresh_token` 模式下可以自动续期：

```go
func reconnect(ctx context.Context, d *plugin.QueryData) (*simpleforce.Client, error) {
    config := GetConfig(d.Connection)

    // access_token 模式无法自动刷新
    if isAccessTokenAuth(config) {
        return nil, fmt.Errorf("session expired; access_token cannot be refreshed")
    }

    // refresh_token 和 JWT 和 password 模式都可以重新认证
    // 清除缓存后重新走 connectRaw 即可
    if d.ConnectionCache != nil {
        d.ConnectionCache.Delete(ctx, cacheKeyClient)
    }
    return connect(ctx, d)
}
```

**注意：** refresh_token 刷新只返回新的 access_token，**不返回新的 refresh_token**（Salesforce 行为）。所以原始 refresh_token 一直有效直到上述失效场景发生。

## Web 后端改动

### API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `GET /auth/salesforce` | GET | 生成授权 URL，302 重定向到 Salesforce |
| `GET /auth/salesforce/callback` | GET | 接收 callback，用 code 换 token |
| `GET /api/connections` | GET | 查询当前连接状态 |
| `DELETE /api/connections/:id` | DELETE | 断开连接，清除 token |
| `POST /api/connections/:id/test` | POST | 测试连接是否可用 |

### 授权入口 (`GET /auth/salesforce`)

```
重定向到:
https://login.salesforce.com/services/oauth2/authorize
  ?response_type=code
  &client_id={CONNECTED_APP_CLIENT_ID}
  &redirect_uri={YOUR_CALLBACK_URL}
  &scope=api refresh_token
  &state={CSRF_TOKEN}
```

### 回调处理 (`GET /auth/salesforce/callback`)

1. 验证 `state` 参数（CSRF 保护）
2. 用 `code` 换 token：

```
POST https://login.salesforce.com/services/oauth2/token
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code={AUTHORIZATION_CODE}
&client_id={CLIENT_ID}
&client_secret={CLIENT_SECRET}
&redirect_uri={CALLBACK_URL}
```

3. 收到响应：

```json
{
  "access_token": "00D...",
  "refresh_token": "5Aep...",
  "instance_url": "https://na01.salesforce.com",
  "token_type": "Bearer",
  "scope": "api refresh_token"
}
```

4. 加密存储 `refresh_token` 和 `access_token` 到数据库
5. 动态创建/更新 PostgreSQL Foreign Server（见下方）

### Token 加密存储

```sql
CREATE TABLE salesforce_connections (
    id            SERIAL PRIMARY KEY,
    org_id        TEXT UNIQUE NOT NULL,       -- Salesforce Organization ID
    instance_url  TEXT NOT NULL,
    access_token  TEXT NOT NULL,              -- AES-256 加密
    refresh_token TEXT NOT NULL,              -- AES-256 加密
    client_id     TEXT NOT NULL,
    client_secret TEXT NOT NULL,              -- AES-256 加密
    username      TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW(),
    status        TEXT DEFAULT 'active'       -- active / expired / revoked
);
```

加密密钥通过环境变量注入（`ENCRYPTION_KEY`），不存数据库。

### 动态 FDW 管理

授权完成后，后端连接 PostgreSQL 执行：

```sql
-- 1. 删除旧的（如果存在）
DROP SERVER IF EXISTS steampipe_salesforce CASCADE;

-- 2. 用 refresh_token 创建新的
CREATE SERVER steampipe_salesforce
  FOREIGN DATA WRAPPER steampipe_postgres_salesforce
  OPTIONS (
    config 'url = "https://na01.salesforce.com"
refresh_token = "5Aep..."
client_id = "3MVG9..."
client_secret = "..."'
  );

-- 3. 重新导入 schema
CREATE SCHEMA IF NOT EXISTS salesforce;
IMPORT FOREIGN SCHEMA salesforce
  FROM SERVER steampipe_salesforce
  INTO salesforce;
```

**注意事项：**
- `DROP SERVER CASCADE` 会删除所有 foreign table，应用层需要重新查询
- 如果有活跃的数据库连接在查询 foreign table，DROP 可能会阻塞
- 考虑在低峰期执行，或使用 `pg_terminate_backend()` 清理活跃连接

### Token 刷新策略

两种方式，建议同时实现：

**被动刷新：** 查询失败时触发（已在插件层的 `queryWithRetry` 中实现）

**主动刷新（可选）：** 后端定时任务，在 access_token 过期前主动刷新：

```python
# 伪代码
@scheduled(every="90 minutes")  # access_token 默认 2h，提前刷新
def refresh_tokens():
    for conn in db.query(salesforce_connections).filter(status='active'):
        try:
            new_access_token = salesforce_refresh(conn.refresh_token)
            conn.access_token = encrypt(new_access_token)
            update_fdw_server(conn)  # ALTER SERVER ... OPTIONS (SET config ...)
        except RefreshFailed:
            conn.status = 'expired'
            notify_admin("Salesforce 连接已失效，请重新授权")
```

## Salesforce Connected App 配置差异

| 配置项 | 方案 A (JWT) | 方案 B (Authorization Code) |
|--------|-------------|---------------------------|
| Callback URL | `https://localhost`（不使用） | **实际回调 URL**（如 `https://yourapp.com/auth/salesforce/callback`） |
| Use digital signatures | 需要上传证书 | 不需要 |
| Client Secret | 不需要 | **需要**（用于 code 换 token 和刷新） |
| OAuth Scopes | `api`, `refresh_token offline_access` | `api`, `refresh_token offline_access` |
| Permitted Users | Admin approved (pre-authorize) | All users may self-authorize 或 Admin approved |

## 前端页面

### 连接管理页面

```
┌────────────────────────────────────────────────────┐
│  Salesforce 连接管理                                │
├────────────────────────────────────────────────────┤
│                                                    │
│  状态: ● 已连接                                     │
│  组织: Acme Corp (00D...)                          │
│  实例: https://acme.my.salesforce.com              │
│  用户: admin@acme.com                              │
│  上次刷新: 2026-02-09 14:30:00                      │
│                                                    │
│  [测试连接]  [断开连接]  [重新授权]                    │
│                                                    │
├────────────────────────────────────────────────────┤
│                                                    │
│  状态: ○ 未连接                                     │
│                                                    │
│  [连接 Salesforce]  ← 触发 OAuth Authorization Code │
│                                                    │
└────────────────────────────────────────────────────┘
```

## Docker 架构

```yaml
# docker-compose.yml 新增
services:
  postgres:
    # ... 现有 PostgreSQL + FDW 容器
    # init 脚本只创建 extension，不创建 server

  web:
    build: ./web
    ports:
      - "3000:3000"
    environment:
      DATABASE_URL: postgres://postgres:postgres@postgres:5432/postgres
      ENCRYPTION_KEY: ${ENCRYPTION_KEY}
      SALESFORCE_CLIENT_ID: ${SALESFORCE_CLIENT_ID}
      SALESFORCE_CLIENT_SECRET: ${SALESFORCE_CLIENT_SECRET}
      OAUTH_CALLBACK_URL: ${OAUTH_CALLBACK_URL}
    depends_on:
      postgres:
        condition: service_healthy
```

init 脚本拆分：

```
init/
  01-create-extension.sql    # 保留：CREATE EXTENSION
  02-create-foreign-server.sh # 删除：不再在启动时创建 server
  03-import-foreign-schema.sql # 删除：由 web 后端在授权后触发
```

## 与方案 A (JWT) 的对比

| 维度 | 方案 A (JWT) | 方案 B (Authorization Code) |
|------|-------------|---------------------------|
| 管理员体验 | 填表单（client_id + username + 私钥） | 点击按钮 → 浏览器授权 |
| 插件改动 | **已完成** | 需新增 refresh_token 字段 + 刷新函数 |
| Token 过期恢复 | 自动（私钥重新签 JWT） | 自动（用 refresh_token 刷新） |
| refresh_token 失效 | **不存在此问题** | 需用户重新浏览器授权 |
| 安全存储 | 私钥 | refresh_token + client_secret |
| 网络要求 | 后端可直接访问 SF API | 后端需要用户浏览器可达的回调 URL |

## 实施顺序

1. **插件层：** 新增 `refresh_token` + `client_secret` 配置字段，实现 `refreshAccessToken()` 函数
2. **插件层：** 修改 `reconnect()` 支持 refresh_token 自动续期
3. **插件层：** 编译并测试
4. **后端：** 实现 OAuth 回调端点 + token 存储
5. **后端：** 实现动态 FDW 管理（DROP/CREATE SERVER）
6. **后端：** 实现 token 主动刷新定时任务
7. **前端：** 连接管理页面
8. **Docker：** 拆分 init 脚本，新增 web 容器
