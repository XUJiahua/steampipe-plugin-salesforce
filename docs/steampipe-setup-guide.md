# Steampipe + Salesforce Plugin 安装与配置指南

本文档记录从零开始安装 Steampipe、配置 Salesforce 插件并验证查询的完整过程。

## 前提条件

- 已编译安装 Salesforce 插件（`make install`），二进制位于：
  `~/.steampipe/plugins/hub.steampipe.io/plugins/turbot/salesforce@latest/steampipe-plugin-salesforce.plugin`
- 已在 Salesforce 中配置好 Connected App 并获取认证凭据（参考 `.env.example`）

## 1. 安装 Steampipe

```bash
sudo /bin/sh -c "$(curl -fsSL https://steampipe.io/install/steampipe.sh)"
```

验证安装：

```bash
steampipe --version
# Steampipe v2.3.5
```

## 2. 创建 Salesforce 连接配置

创建配置文件 `~/.steampipe/config/salesforce.spc`：

```bash
mkdir -p ~/.steampipe/config
```

### JWT Bearer Flow 认证（推荐）

```hcl
connection "salesforce" {
  plugin = "salesforce"

  url              = "https://<your-instance>.my.salesforce.com"
  client_id        = "<Connected App Consumer Key>"
  username         = "<salesforce-username>"
  private_key_file = "/path/to/server.key"
}
```

JWT 认证需要事先完成以下准备（详见 `.env.example`）：

1. 生成 RSA 密钥对：
   ```bash
   openssl genrsa -out server.key 2048
   openssl req -new -x509 -key server.key -out server.crt -days 365
   ```
2. 在 Salesforce 中创建 Connected App，上传 `server.crt`
3. 授权 Connected App（Admin approved users are pre-authorized）
4. 获取 Consumer Key 作为 `client_id`

### 其他认证方式

**Access Token**（适合快速测试）：

```hcl
connection "salesforce" {
  plugin = "salesforce"

  url          = "https://<your-instance>.my.salesforce.com"
  access_token = "<oauth-access-token>"
}
```

**Username/Password**（已过时，不推荐）：

```hcl
connection "salesforce" {
  plugin = "salesforce"

  url      = "https://login.salesforce.com"
  username = "<username>"
  password = "<password>"
  token    = "<security-token>"
}
```

认证优先级：Access Token > JWT Bearer Flow > Username/Password。

## 3. 启动 Steampipe 服务

```bash
steampipe service start
```

输出示例：

```
Steampipe service is running:

Database:
  Host(s):            127.0.0.1, ::1
  Port:               9193
  Database:           steampipe
  User:               steampipe
  Connection string:  postgres://steampipe@127.0.0.1:9193/steampipe
```

常用服务管理命令：

```bash
steampipe service status            # 查看状态
steampipe service status --show-password  # 查看数据库密码
steampipe service restart           # 重启
steampipe service stop              # 停止
```

## 4. 验证查询

### 方式 A：使用 steampipe query

```bash
steampipe query "SELECT id, name, industry FROM salesforce_account LIMIT 5"
```

输出示例：

```
+--------------------+-------------------------------------+--------------+
| id                 | name                                | industry     |
+--------------------+-------------------------------------+--------------+
| 001g5000008QZuLAAW | Edge Communications                 | Electronics  |
| 001g5000008QZuPAAW | Grand Hotels & Resorts Ltd          | Hospitality  |
| 001g5000008QZuMAAW | Burlington Textiles Corp of America | Apparel      |
| 001g5000008QZuOAAW | Dickenson plc                       | Consulting   |
| 001g5000008QZuNAAW | Pyramid Construction Inc.           | Construction |
+--------------------+-------------------------------------+--------------+
```

### 方式 B：使用 psql 或其他 PostgreSQL 客户端

```bash
# 获取密码
steampipe service status --show-password

# 连接
psql -h 127.0.0.1 -p 9193 -U steampipe -d steampipe
```

连接后即可执行标准 SQL：

```sql
SELECT id, name, industry FROM salesforce_account LIMIT 5;
```

## 文件结构概览

```
~/.steampipe/
├── config/
│   └── salesforce.spc          # 连接配置
├── plugins/
│   └── hub.steampipe.io/plugins/turbot/salesforce@latest/
│       └── steampipe-plugin-salesforce.plugin   # 插件二进制
└── db/                         # Steampipe 内嵌 PostgreSQL 数据
```
