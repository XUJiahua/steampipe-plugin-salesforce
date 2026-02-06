package salesforce

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/iancoleman/strcase"
	"github.com/simpleforce/simpleforce"
	"github.com/turbot/steampipe-plugin-sdk/v5/connection"
	"github.com/turbot/steampipe-plugin-sdk/v5/grpc/proto"
	"github.com/turbot/steampipe-plugin-sdk/v5/memoize"
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin"
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin/transform"
)

func connect(ctx context.Context, d *plugin.QueryData) (*simpleforce.Client, error) {
	return connectRaw(ctx, d.ConnectionCache, d.Connection)
}

// connectRaw returns a Salesforce client after authentication.
// Authentication method is selected based on which credentials are configured.
// Precedence: access_token > private_key/private_key_file (JWT) > username/password
func connectRaw(ctx context.Context, cc *connection.ConnectionCache, c *plugin.Connection) (*simpleforce.Client, error) {
	// Load connection from cache, which preserves throttling protection etc
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
	if (config.PrivateKey != nil && *config.PrivateKey != "") || (config.PrivateKeyFile != nil && *config.PrivateKeyFile != "") {
		if config.URL == nil || *config.URL == "" {
			return nil, fmt.Errorf("jwt auth requires 'url' to be set")
		}
		if config.Username == nil || *config.Username == "" {
			return nil, fmt.Errorf("jwt auth requires 'username' to be set")
		}
		if config.ClientId == nil || *config.ClientId == "" {
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

	// Precedence 3: Username/Password flow
	if config.Username != nil && *config.Username != "" && config.Password != nil && *config.Password != "" {
		if config.URL == nil || *config.URL == "" {
			return nil, fmt.Errorf("password auth requires 'url' to be set")
		}
		securityToken := ""
		// The Salesforce security token is only required If the client's IP address is not added to the organization's list of trusted IPs
		// https://help.salesforce.com/s/articleView?id=sf.security_networkaccess.htm&type=5
		// https://migration.trujay.com/help/how-to-add-an-ip-address-to-whitelist-on-salesforce/
		if config.Token != nil {
			securityToken = *config.Token
		}

		client := simpleforce.NewClient(*config.URL, clientID, apiVersion)
		if client == nil {
			return nil, fmt.Errorf("failed to create salesforce client")
		}

		// LoginPassword signs into salesforce using password. token is optional if trusted IP is configured.
		// Ref: https://developer.salesforce.com/docs/atlas.en-us.214.0.api_rest.meta/api_rest/intro_understanding_username_password_oauth_flow.htm
		// Ref: https://developer.salesforce.com/docs/atlas.en-us.214.0.api.meta/api/sforce_api_calls_login.htm
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

// generateQuery:: returns sql query based on the column names, table name passed
func generateQuery(columns []*plugin.Column, tableName string) string {
	var queryColumns []string
	for _, column := range columns {
		if column.Name != "OrganizationId" && column.Name != "organization_id" {
			queryColumns = append(queryColumns, getSalesforceColumnName(column.Name))
		}
	}

	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(queryColumns, ", "), tableName)
}

// decodeQueryResult(ctx, apiResponse, responseStruct):: converts raw apiResponse to required output struct
func decodeQueryResult(ctx context.Context, response interface{}, respObject interface{}) error {
	resp, err := json.Marshal(response)
	if err != nil {
		return err
	}

	// For debugging purpose - commenting out to avoid unnecessary logs
	// plugin.Logger(ctx).Info("decodeQueryResult", "Items", string(resp))
	err = json.Unmarshal(resp, respObject)
	if err != nil {
		return err
	}

	return nil
}

// buildQueryFromQuals :: generate api_native based on the contions specified in sql query
// refrences
// - https://developer.salesforce.com/docs/atlas.en-us.234.0.soql_sosl.meta/soql_sosl/sforce_api_calls_soql_select_comparisonoperators.htm
func buildQueryFromQuals(equalQuals plugin.KeyColumnQualMap, tableColumns []*plugin.Column, salesforceCols map[string]string) string {
	filters := []string{}

	for _, filterQualItem := range tableColumns {
		filterQual := equalQuals[filterQualItem.Name]
		if filterQual == nil {
			continue
		}

		// Check only if filter qual map matches with optional column name
		if filterQual.Name == filterQualItem.Name {
			if filterQual.Quals == nil {
				continue
			}

			for _, qual := range filterQual.Quals {
				if qual.Value != nil {
					value := qual.Value
					switch filterQualItem.Type {
					case proto.ColumnType_STRING:
						// In case of IN caluse
						if value.GetListValue() != nil {
							// continue
							switch qual.Operator {
							case "=":
								stringValueSlice := []string{}
								for _, q := range value.GetListValue().Values {
									stringValueSlice = append(stringValueSlice, q.GetStringValue())
								}
								if len(stringValueSlice) > 0 {
									filters = append(filters, fmt.Sprintf("%s IN ('%s')", getSalesforceColumnName(filterQualItem.Name), strings.Join(stringValueSlice, "','")))
								}
							case "<>":
								stringValueSlice := []string{}
								for _, q := range value.GetListValue().Values {
									stringValueSlice = append(stringValueSlice, q.GetStringValue())
								}
								if len(stringValueSlice) > 0 {
									filters = append(filters, fmt.Sprintf("%s NOT IN ('%s')", getSalesforceColumnName(filterQualItem.Name), strings.Join(stringValueSlice, "','")))
								}
							}
						} else {
							switch qual.Operator {
							case "=":
								filters = append(filters, fmt.Sprintf("%s = '%s'", getSalesforceColumnName(filterQualItem.Name), value.GetStringValue()))
							case "<>":
								filters = append(filters, fmt.Sprintf("%s != '%s'", getSalesforceColumnName(filterQualItem.Name), value.GetStringValue()))
							}
						}
					case proto.ColumnType_BOOL:
						switch qual.Operator {
						case "<>":
							filters = append(filters, fmt.Sprintf("%s = %s", getSalesforceColumnName(filterQualItem.Name), "FALSE"))
						case "=":
							filters = append(filters, fmt.Sprintf("%s = %s", getSalesforceColumnName(filterQualItem.Name), "TRUE"))
						}
					case proto.ColumnType_INT:
						switch qual.Operator {
						case "<>":
							filters = append(filters, fmt.Sprintf("%s != %d", getSalesforceColumnName(filterQualItem.Name), value.GetInt64Value()))
						default:
							filters = append(filters, fmt.Sprintf("%s %s %d", getSalesforceColumnName(filterQualItem.Name), qual.Operator, value.GetInt64Value()))
						}
					case proto.ColumnType_DOUBLE:
						switch qual.Operator {
						case "<>":
							filters = append(filters, fmt.Sprintf("%s != %f", getSalesforceColumnName(filterQualItem.Name), value.GetDoubleValue()))
						default:
							filters = append(filters, fmt.Sprintf("%s %s %f", getSalesforceColumnName(filterQualItem.Name), qual.Operator, value.GetDoubleValue()))
						}
					// Need a way to distinguish b/w date and dateTime fields
					case proto.ColumnType_TIMESTAMP:
						// https://developer.salesforce.com/docs/atlas.en-us.234.0.soql_sosl.meta/soql_sosl/sforce_api_calls_soql_select_dateformats.htm
						if salesforceCols[filterQual.Name] == "date" {
							switch qual.Operator {
							case "=", ">=", ">", "<=", "<":
								filters = append(filters, fmt.Sprintf("%s %s %s", getSalesforceColumnName(filterQualItem.Name), qual.Operator, value.GetTimestampValue().AsTime().Format("2006-01-02")))
							}
						} else {
							switch qual.Operator {
							case "=", ">=", ">", "<=", "<":
								filters = append(filters, fmt.Sprintf("%s %s %s", getSalesforceColumnName(filterQualItem.Name), qual.Operator, value.GetTimestampValue().AsTime().Format("2006-01-02T15:04:05Z")))
							}
						}
					}
				}
			}

		}
	}

	if len(filters) > 0 {
		return strings.Join(filters, " AND ")
	}

	return ""
}

func getSalesforceColumnName(name string) string {
	var columnName string
	// Salesforce custom fields are suffixed with '__c' and are not converted to
	// snake case in the table schema, so use the column name as is
	if strings.HasSuffix(name, "__c") {
		columnName = name
	} else {
		columnName = strcase.ToCamel(name)
	}
	return columnName
}

func mergeTableColumns(_ context.Context, config salesforceConfig, dynamicColumns []*plugin.Column, staticColumns []*plugin.Column) []*plugin.Column {
	var columns []*plugin.Column

	// when NamingConvention is set to api_native, do not add the static columns
	if config.NamingConvention != nil && *config.NamingConvention == "api_native" && len(dynamicColumns) > 0 {
		columns = append(columns, dynamicColumns...)
		return columns
	}

	columns = append(columns, staticColumns...)
	for _, col := range dynamicColumns {
		if isColumnAvailable(col.Name, staticColumns) {
			continue
		}
		columns = append(columns, col)
	}

	return columns
}

// dynamicColumns:: Returns list coulms for a salesforce object
func dynamicColumns(ctx context.Context, client *simpleforce.Client, salesforceTableName string, config salesforceConfig) ([]*plugin.Column, plugin.KeyColumnSlice, map[string]string) {
	sObjectMeta := client.SObject(salesforceTableName).Describe()
	if sObjectMeta == nil {
		plugin.Logger(ctx).Error("salesforce.dynamicColumns", fmt.Sprintf("Table %s not present in salesforce", salesforceTableName))
		return []*plugin.Column{}, plugin.KeyColumnSlice{}, map[string]string{}
	}

	// Top columns
	cols := []*plugin.Column{
		{Name: "organization_id", Type: proto.ColumnType_STRING, Description: "Unique identifier of the organization in Salesforce.", Hydrate: getOrganizationId, Transform: transform.FromValue()},
	}
	salesforceCols := map[string]string{}
	// Key columns
	keyColumns := plugin.KeyColumnSlice{}

	salesforceObjectMetadata := *sObjectMeta
	salesforceObjectMetadataAsByte, err := json.Marshal(salesforceObjectMetadata["fields"])
	if err != nil {
		plugin.Logger(ctx).Error("salesforce.dynamicColumns", "json marshal error %v", err)
	}

	salesforceObjectFields := []map[string]interface{}{}
	// var queryColumns []string
	err = json.Unmarshal(salesforceObjectMetadataAsByte, &salesforceObjectFields)
	if err != nil {
		plugin.Logger(ctx).Error("salesforce.dynamicColumns", "json unmarshal error %v", err)
	}
	for _, fields := range salesforceObjectFields {
		if fields["name"] == nil {
			continue
		}
		fieldName := fields["name"].(string)
		compoundFieldName := fields["compoundFieldName"]
		if compoundFieldName != nil && compoundFieldName.(string) != fieldName {
			continue
		}

		if fields["soapType"] == nil {
			continue
		}
		soapType := strings.Split((fields["soapType"]).(string), ":")
		fieldType := soapType[len(soapType)-1]

		// Column dynamic generation
		// Don't convert to snake case since field names can have underscores in
		// them, so it's impossible to convert from snake case back to camel case
		// to match the original field name. Also, if we convert to snake case,
		// custom fields like "TestField" and "Test_Field" will result in duplicates
		// check if DynamicTableAndPropertyNames is true
		var columnFieldName string

		// keep the field name as it is if NamingConvention is set to api_native
		if config.NamingConvention != nil && *config.NamingConvention == "api_native" {
			columnFieldName = fieldName
		} else if strings.HasSuffix(fieldName, "__c") {
			columnFieldName = strings.ToLower(fieldName)
		} else {
			columnFieldName = strcase.ToSnake(fieldName)
		}

		column := plugin.Column{
			Name:        columnFieldName,
			Description: fmt.Sprintf("%s.", fields["label"].(string)),
			Transform:   transform.FromP(getFieldFromSObjectMap, fieldName),
		}
		salesforceCols[columnFieldName] = fieldType

		// Set column type based on the `soapType` from salesforce schema
		switch fieldType {
		case "string", "ID", "time":
			column.Type = proto.ColumnType_STRING
			keyColumns = append(keyColumns, &plugin.KeyColumn{Name: columnFieldName, Require: plugin.Optional, Operators: []string{"=", "<>"}})
		case "date", "dateTime":
			column.Type = proto.ColumnType_TIMESTAMP
			keyColumns = append(keyColumns, &plugin.KeyColumn{Name: columnFieldName, Require: plugin.Optional, Operators: []string{"=", ">", ">=", "<=", "<"}})
		case "boolean":
			column.Type = proto.ColumnType_BOOL
			keyColumns = append(keyColumns, &plugin.KeyColumn{Name: columnFieldName, Require: plugin.Optional, Operators: []string{"=", "<>"}})
		case "double":
			column.Type = proto.ColumnType_DOUBLE
			keyColumns = append(keyColumns, &plugin.KeyColumn{Name: columnFieldName, Require: plugin.Optional, Operators: []string{"=", ">", ">=", "<=", "<"}})
		case "int":
			column.Type = proto.ColumnType_INT
			keyColumns = append(keyColumns, &plugin.KeyColumn{Name: columnFieldName, Require: plugin.Optional, Operators: []string{"=", ">", ">=", "<=", "<"}})
		default:
			column.Type = proto.ColumnType_JSON
		}
		cols = append(cols, &column)
	}
	return cols, keyColumns, salesforceCols
}

var getOrganizationIdMemoize = plugin.HydrateFunc(getOrganizationIdUncached).Memoize(memoize.WithCacheKeyFunction(getOrganizationIdCacheKey))

func getOrganizationIdCacheKey(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	cacheKey := "getOrganizationId"
	return cacheKey, nil
}

func getOrganizationId(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {

	config, err := getOrganizationIdMemoize(ctx, d, h)
	if err != nil {
		return nil, err
	}

	c := config.(string)

	return c, nil
}

func getOrganizationIdUncached(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {

	var orgId string

	client, err := connect(ctx, d)
	if err != nil {
		plugin.Logger(ctx).Error("salesforce.getOrganizationIdUncached", "connection error", err)
		return nil, err
	}

	// SOQL Query to retrieve organization details
	query := "SELECT Id, Name, InstanceName, IsSandbox FROM Organization"

	result, err := client.Query(query)
	if err != nil {
		plugin.Logger(ctx).Error("salesforce.getOrganizationIdUncached", "api error", err)
		return nil, err
	}

	if len(result.Records) > 0 {
		orgId = result.Records[0].ID()
	}


	return orgId, nil
}

// isColumnAvailable:: Checks if the column is not present in the existing columns slice
func isColumnAvailable(columnName string, columns []*plugin.Column) bool {
	for _, col := range columns {
		if col.Name == columnName {
			return true
		}
	}
	return false
}

var sandboxPattern = regexp.MustCompile(`(?i)(sandbox|[/.]cs\d+\.|test\.salesforce\.com)`)

// loginURL determines the Salesforce login endpoint based on the instance URL.
// Sandbox instances use test.salesforce.com, production uses login.salesforce.com.
func loginURL(instanceURL string) string {
	if sandboxPattern.MatchString(instanceURL) {
		return "https://test.salesforce.com"
	}
	return "https://login.salesforce.com"
}

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

// loginJWT performs the OAuth 2.0 JWT Bearer flow.
// loginURL is the Salesforce token endpoint base (e.g. "https://login.salesforce.com").
// Returns the access_token and instance_url from the token response.
func loginJWT(loginEndpoint, clientID, username, privateKeyPEM string) (string, string, error) {
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
		Audience:  jwt.ClaimStrings{loginEndpoint},
		ExpiresAt: jwt.NewNumericDate(now.Add(3 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedJWT, err := token.SignedString(key)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign JWT: %v", err)
	}

	// POST to token endpoint
	tokenURL := loginEndpoint + "/services/oauth2/token"
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
	if instanceURL == "" {
		return "", "", fmt.Errorf("token response missing instance_url")
	}

	return accessToken, instanceURL, nil
}
