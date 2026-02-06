---
organization: Turbot
category: ["saas"]
icon_url: "/images/plugins/turbot/salesforce.svg"
brand_color: "#00A1E0"
display_name: "Salesforce"
short_name: "salesforce"
description: "Steampipe plugin to query accounts, opportunities, users and more from your Salesforce instance."
og_description: "Query Salesforce with SQL! Open source CLI. No DB required."
og_image: "/images/plugins/turbot/salesforce-social-graphic.png"
engines: ["steampipe", "sqlite", "postgres", "export"]
---

# Salesforce + Steampipe

[Salesforce](https://www.salesforce.com/) is a customer relationship management (CRM) platform.

[Steampipe](https://steampipe.io) is an open-source zero-ETL engine to instantly query cloud APIs using SQL.

List won opportunities:

```sql
select
  name,
  amount,
  close_date
from
  salesforce_opportunity
where
  is_won;
```

```
+-------------------------------------+--------+---------------------------+
| name                                | amount | close_date                |
+-------------------------------------+--------+---------------------------+
| GenePoint Standby Generator         | 85000  | 2021-10-23T05:30:00+05:30 |
| GenePoint SLA                       | 30000  | 2021-12-16T05:30:00+05:30 |
| Express Logistics Standby Generator | 220000 | 2021-09-15T05:30:00+05:30 |
+-------------------------------------+------------------------------------+
```

## Documentation

- **[Table definitions & examples →](/plugins/turbot/salesforce/tables)**

## Get started

### Install

Download and install the latest salesforce plugin:

```bash
steampipe plugin install salesforce
```

### Configuration

Installing the latest salesforce plugin will create a config file (`~/.steampipe/config/salesforce.spc`) with a single connection named `salesforce`:

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
  # client_id = "3MVG99E3Ry5mh4z_FakeID"
  # username = "user@example.com"
  # private_key_file = "/path/to/server.key"
  # private_key = "-----BEGIN RSA PRIVATE KEY-----\n..."

  # Option 3: Username/Password flow
  # username = "user@example.com"
  # password = "Dummy@~Password"
  # token = "ABO5C3PNqOP0BHsPFakeToken"

  # The Salesforce security token is only required If the client's IP address is not added to the organization's list of trusted IPs
  # https://help.salesforce.com/s/articleView?id=sf.security_networkaccess.htm&type=5

  # List of Salesforce object names to generate additional tables for
  # This argument only accepts exact Salesforce standard and custom object names, e.g., AccountBrand, OpportunityStage, CustomApp__c
  # For a full list of standard object names, please see https://developer.salesforce.com/docs/atlas.en-us.api.meta/api/sforce_api_objects_list.htm
  # All custom object names should end in "__c", following Salesforce object naming standards
  # objects = ["AccountBrand", "OpportunityStage", "CustomApp__c"]

  # Salesforce API version to connect to
  # api_version = "43.0"

  # The naming_convention allows users to control the naming format for tables and columns in the plugin. Below are the supported values:
  # api_native - If set to this value, the plugin will use the native format for table names, meaning there will be no "salesforce_" prefix, and the table and column names will remain as they are in Salesforce.
  # snake_case (default) - If the user does not specify any value, the plugin will use snake case for table and column names and table names will have a "salesforce_" prefix.
  # naming_convention = "snake_case"
}
```

### Credentials

The plugin supports three authentication methods. Choose the one that fits your use case:

| Method | Best For | Setup Complexity |
|--------|----------|------------------|
| [Pre-obtained Access Token](#obtaining-an-access-token) | Quick testing, existing OAuth flows | Low |
| [JWT Bearer Flow](#setting-up-jwt-bearer-flow) | Production, automation, CI/CD | Medium |
| [Username/Password](#username-password-setup) | Simple setups, development | Low |

#### Obtaining an Access Token

The easiest way to get an access token is using the [Salesforce CLI](https://developer.salesforce.com/tools/salesforcecli):

**Install Salesforce CLI**

```bash
# macOS (Homebrew)
brew install sf

# Or via npm (all platforms)
npm install -g @salesforce/cli

# Verify installation
sf --version
```

**Login and Get Token**

```bash
# Login to your Salesforce org (opens browser)
sf org login web --alias my-org

# Display credentials including access token
sf org display --target-org my-org --json
```

The output includes `accessToken` and `instanceUrl`:

```json
{
  "result": {
    "accessToken": "00D...",
    "instanceUrl": "https://mycompany.develop.my.salesforce.com"
  }
}
```

Use these values in your configuration:

```hcl
connection "salesforce" {
  plugin       = "salesforce"
  url          = "https://mycompany.develop.my.salesforce.com/"
  access_token = "00D..."
}
```

**Alternative: Device Flow (No Browser)**

If you prefer not to open a browser (e.g., remote servers), use the device flow:

```bash
sf org login device --alias my-org
```

Follow the prompts: visit the provided URL, enter the code, and authenticate in your browser.

**Notes:**
- Access tokens typically expire after 2 hours. For long-running or automated use cases, consider using the JWT Bearer Flow instead.
- The CLI handles all Salesforce domain types automatically (`login.salesforce.com`, `*.my.salesforce.com`, `*.develop.my.salesforce.com`, etc.).

#### Setting Up JWT Bearer Flow

JWT Bearer Flow is recommended for production and automation scenarios. It requires a one-time setup of a Connected App with a certificate.

**Step 1: Generate RSA Key Pair**

```bash
# Generate private key
openssl genrsa -out server.key 2048

# Generate self-signed certificate (valid for 1 year)
openssl req -new -x509 -key server.key -out server.crt -days 365 -subj "/CN=steampipe"
```

**Step 2: Create Connected App in Salesforce**

1. Log in to Salesforce Setup
2. Search for **App Manager** → Click **New Connected App**
3. Fill in the basic information:
   - **Connected App Name:** `Steampipe`
   - **API Name:** `Steampipe`
   - **Contact Email:** Your email address
4. Enable OAuth Settings:
   - **Enable OAuth Settings:** ✓
   - **Callback URL:** `https://localhost` (required but not used for JWT)
   - **Use digital signatures:** ✓ → Upload `server.crt`
   - **Selected OAuth Scopes:** Add these two scopes:
     - `Access and manage your data (api)`
     - `Perform requests on your behalf at any time (refresh_token, offline_access)`
5. Click **Save**
6. **Wait 2-10 minutes** for the Connected App to activate
7. After activation, note the **Consumer Key** — this is your `client_id`

**Step 3: Pre-authorize Users**

1. In Setup, search for **Manage Connected Apps**
2. Find your app and click **Manage**
3. Click **Edit Policies**
4. Set **Permitted Users** to `Admin approved users are pre-authorized`
5. Click **Save**
6. Scroll down to **Profiles** or **Permission Sets** and add the profiles/permission sets for users who will authenticate

**Step 4: Configure the Plugin**

```hcl
connection "salesforce" {
  plugin           = "salesforce"
  url              = "https://na01.salesforce.com/"
  client_id        = "3MVG9..."              # Consumer Key from Step 2
  username         = "user@example.com"
  private_key_file = "/path/to/server.key"
}
```

For CI/CD environments, you can use an inline private key via environment variable:

```hcl
connection "salesforce" {
  plugin      = "salesforce"
  url         = "https://na01.salesforce.com/"
  client_id   = "3MVG9..."
  username    = "user@example.com"
  private_key = <<-EOT
-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA...
-----END RSA PRIVATE KEY-----
EOT
}
```

#### Username Password Setup

For username/password authentication:

1. [Reset your security token](https://help.salesforce.com/articleView?id=user_security_token.htm&type=5) — Salesforce emails you a new token
2. The security token is required unless your IP is in the [organization's trusted IP list](https://help.salesforce.com/s/articleView?id=sf.security_networkaccess.htm&type=5)

```hcl
connection "salesforce" {
  plugin   = "salesforce"
  url      = "https://na01.salesforce.com/"
  username = "user@example.com"
  password = "MyPassword"
  token    = "MySecurityToken"  # Omit if IP is trusted
}
```

#### Troubleshooting

| Error | Cause | Solution |
|-------|-------|----------|
| `user hasn't approved this consumer` | User not pre-authorized for JWT | Complete Step 3 of JWT setup |
| `invalid_grant` | JWT auth failed | Check `username`, certificate upload, and private key path |
| `INVALID_LOGIN` | Wrong credentials | Verify username, password, and security token |
| `token response missing instance_url` | Malformed OAuth response | Check Salesforce org status and Connected App configuration |

### Authentication

The plugin supports three authentication methods. The method is auto-detected based on which credentials are provided, with the following precedence: **access_token** > **JWT** > **username/password**.

#### Pre-obtained Access Token

Use a pre-obtained OAuth access token. This is useful when you have an existing OAuth flow or are using a refresh token externally.

```hcl
connection "salesforce" {
  plugin       = "salesforce"
  url          = "https://na01.salesforce.com/"
  access_token = "00D..."
}
```

#### JWT Bearer Flow

Use the [OAuth 2.0 JWT Bearer Flow](https://help.salesforce.com/s/articleView?id=sf.remoteaccess_oauth_jwt_flow.htm&type=5) for server-to-server authentication. Requires a connected app with a certificate.

```hcl
connection "salesforce" {
  plugin           = "salesforce"
  url              = "https://na01.salesforce.com/"
  client_id        = "3MVG99E3Ry5mh4z..."
  username         = "user@example.com"
  private_key_file = "/path/to/server.key"
}
```

You can also provide the private key inline using `private_key` instead of `private_key_file`:

```hcl
connection "salesforce" {
  plugin      = "salesforce"
  url         = "https://na01.salesforce.com/"
  client_id   = "3MVG99E3Ry5mh4z..."
  username    = "user@example.com"
  private_key = "-----BEGIN RSA PRIVATE KEY-----\nMIIE..."
}
```

#### Username/Password Flow

The traditional username/password authentication. Requires the security token if connecting from an IP outside your trusted range.

```hcl
connection "salesforce" {
  plugin   = "salesforce"
  url      = "https://na01.salesforce.com/"
  username = "user@example.com"
  password = "MyPassword"
  token    = "MySecurityToken"
}
```

## Custom Fields

Salesforce supports the addition of [custom fields](https://help.salesforce.com/s/articleView?id=sf.adding_fields.htm&type=5) to standard objects.

If you have set up Salesforce credentials correctly in the Steampipe configuration, Steampipe will generate the tables schema with all the custom fields along with standard object fields dynamically.

For instance, if the `Account` object in my Salesforce account has a custom field with the label `Priority` and the API name `Priority__c`, the table schema will be generated as:

```sh
.inspect salesforce_account
+-----------------------+--------------------------+-------------------------------------------------------------+
| column         | type | description                                                                            |
+-----------------------+---------------------------+------------------------------------------------------------+
| account_number | text | The Account Number.                                                                    |
| account_source | text | The source of the account record. For example, Advertisement, Data.com, or Trade Show. |
| priority__c    | text | The account's priority.                                                                |
+----------------+------+----------------------------------------------------------------------------------------+
```

The custom field `priority__c` column can then be queried like other columns:

```sql
select
  account_number,
  priority__c
from
  salesforce_account;
```

**Note:** Salesforce custom field names are always suffixed with `__c`, which is reflected in the column names as well.

## Custom Objects

Salesforce also supports creating [custom objects](https://help.salesforce.com/s/articleView?id=sf.dev_objectcreate_task_lex.htm&type=5) to track and store data that's unique to your organization.

Steampipe will create table schemas for all custom objects set in the `objects` argument.

For instance, if my connection configuration is:

```hcl
connection "salesforce" {
  plugin    = "salesforce"
  url       = "https://my-dev-env.my.salesforce.com"
  username  = "user@example.com"
  password  = "MyPassword"
  token     = "MyToken"
  client_id = "MyClientID"
  objects   = ["CustomApp__c", "OtherCustomApp__c"]
}
```

Steampile will automatically create two tables, `salesforce_custom_app__c` and `salesforce_other_custom_app__c`, which can then be inspected and queried like other tables:

```sh
.inspect salesforce
+---------------------------------+---------------------------------------------------------+
| table                           | description                                             |
+---------------------------------+---------------------------------------------------------+
| salesforce_account_contact_role | Represents the role that a Contact plays on an Account. |
| salesforce_custom_app__c        | Represents Salesforce object CustomApp__c.              |
| salesforce_other_custom_app__c  | Represents Salesforce object OtherCustomApp__c.         |
+---------------------------------+---------------------------------------------------------+
```

To get details of a specific custom object table, inspect it by name:

```sh
.inspect salesforce_custom_app__c
+---------------------+--------------------------+-------------------------+
| column              | type                     | description             |
+---------------------+--------------------------+-------------------------+
| created_by_id       | text                     | ID of app creator.      |
| created_date        | timestamp with time zone | Created date.           |
| id                  | text                     | App record ID.          |
| is_deleted          | boolean                  | True if app is deleted. |
| last_modified_by_id | text                     | ID of last modifier.    |
| last_modified_date  | timestamp with time zone | Last modified date.     |
| name                | text                     | App name.               |
| owner_id            | text                     | Owner ID.               |
| system_modstamp     | timestamp with time zone | System Modstamp.        |
+---------------------+--------------------------+-------------------------+
```

This table can also be queried like other tables:

```sql
select
  *
from
  salesforce_custom_app__c;
```

**Note:** Salesforce custom object names are always suffixed with `__c`, which is reflected in the table names as well.

## Naming Convention

The `naming_convention` configuration argument allows you to control the naming format for tables and columns in the plugin.

### Snake Case

If you do not specify a value for `naming_convention` or set it to `snake_case`, the plugin will use snake case for table and column names, and table names will have a `salesforce_` prefix.

For example:

```sql
select
  id,
  who_count,
  what_count,
  subject,
  is_all_day_event
from
  salesforce_event;
```

```
+---------------------+-----------+------------+---------+------------------+
| id                  | who_count | what_count | subject | is_all_day_event |
+----------------------------------------------+----------------------------+
| 00U2t0000000Mw3dEAD | 0         |  0         | test    | false            |
+---------------------+-----------+------------+---------+------------------+
```

### API Native

If `naming_convention` is set to `api_native`, the plugin will use Salesforce naming conventions. Table and column names will have mixed case and table names will not start with `salesforce_`.

For example:

```sql
select
  "Id",
  "WhoCount",
  "WhatCount",
  "Subject",
  "IsAllDayEvent"
from
  "Event";
```

```
+---------------------+----------+-------------+---------+---------------+
| ID                  | WhoCount |  WhatCount  | Subject | IsAllDayEvent |
+----------------------------------------------+-------------------------+
| 00U2t0000000Mw3dEAD | 0        |  0          | test    | false         |
+---------------------+----------+-----------------------+---------------+
```


