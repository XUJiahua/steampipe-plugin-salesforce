//go:build integration

package salesforce

import (
	"os"
	"testing"

	"github.com/joho/godotenv"
	"github.com/simpleforce/simpleforce"
	"github.com/turbot/steampipe-plugin-sdk/v5/grpc/proto"
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin"
)

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

	// Precedence 2: Refresh Token flow
	refreshToken := os.Getenv("SALESFORCE_REFRESH_TOKEN")
	clientSecret := os.Getenv("SALESFORCE_CLIENT_SECRET")
	if refreshToken != "" {
		if url == "" {
			t.Fatal("SALESFORCE_REFRESH_TOKEN requires SALESFORCE_URL")
		}
		if clientID == "steampipe" {
			t.Fatal("SALESFORCE_REFRESH_TOKEN requires SALESFORCE_CLIENT_ID")
		}
		if clientSecret == "" {
			t.Fatal("SALESFORCE_REFRESH_TOKEN requires SALESFORCE_CLIENT_SECRET")
		}
		loginBase := loginURL(url)
		at, instanceURL, err := refreshAccessToken(loginBase, clientID, clientSecret, refreshToken)
		if err != nil {
			t.Fatalf("refresh_token login failed: %v", err)
		}
		client := simpleforce.NewClient(instanceURL, clientID, apiVersion)
		if client == nil {
			t.Fatal("failed to create simpleforce client")
		}
		client.SetSidLoc(at, instanceURL)
		return client
	}

	// Precedence 3: JWT Bearer flow
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

	// Precedence 4: Username/Password
	if url == "" || username == "" || password == "" {
		t.Skip("no valid auth credentials set (need SALESFORCE_ACCESS_TOKEN, SALESFORCE_REFRESH_TOKEN, SALESFORCE_PRIVATE_KEY/_FILE, or SALESFORCE_USERNAME+PASSWORD)")
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

func TestIntegration_Login(t *testing.T) {
	client := loadEnvAndCreateClient(t)
	if client == nil {
		t.Fatal("client is nil after login")
	}
}

func TestIntegration_QueryAccounts(t *testing.T) {
	client := loadEnvAndCreateClient(t)
	result, err := client.Query("SELECT Id, Name FROM Account LIMIT 5")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("returned %d Account records", len(result.Records))
}

func TestIntegration_QueryContacts(t *testing.T) {
	client := loadEnvAndCreateClient(t)
	result, err := client.Query("SELECT Id, Name, Email FROM Contact LIMIT 5")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("returned %d Contact records", len(result.Records))
}

func TestIntegration_QueryOpportunities(t *testing.T) {
	client := loadEnvAndCreateClient(t)
	result, err := client.Query("SELECT Id, Name, StageName, Amount FROM Opportunity LIMIT 5")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("returned %d Opportunity records", len(result.Records))
}

func TestIntegration_DescribeAccount(t *testing.T) {
	client := loadEnvAndCreateClient(t)
	meta := client.SObject("Account").Describe()
	if meta == nil {
		t.Fatal("Describe() returned nil for Account")
	}
	fields, ok := (*meta)["fields"]
	if !ok || fields == nil {
		t.Fatal("Account metadata missing 'fields'")
	}
	t.Logf("Account describe returned fields metadata")
}

func TestIntegration_GetAccountByID(t *testing.T) {
	client := loadEnvAndCreateClient(t)

	// First query an ID
	result, err := client.Query("SELECT Id FROM Account LIMIT 1")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(result.Records) == 0 {
		t.Skip("no Account records found")
	}

	id := result.Records[0].ID()
	if id == "" {
		t.Fatal("first Account record has empty ID")
	}

	obj := client.SObject("Account").Get(id)
	if obj == nil {
		t.Fatalf("Get(%s) returned nil", id)
	}
	t.Logf("retrieved Account %s", id)
}

func TestIntegration_QueryOrganization(t *testing.T) {
	client := loadEnvAndCreateClient(t)
	result, err := client.Query("SELECT Id, Name, InstanceName, IsSandbox FROM Organization")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(result.Records) == 0 {
		t.Fatal("Organization query returned 0 records")
	}
	t.Logf("Organization Id: %s", result.Records[0].ID())
}

func TestIntegration_GenerateQueryEndToEnd(t *testing.T) {
	client := loadEnvAndCreateClient(t)

	columns := []*plugin.Column{
		{Name: "id", Type: proto.ColumnType_STRING},
		{Name: "name", Type: proto.ColumnType_STRING},
		{Name: "organization_id", Type: proto.ColumnType_STRING}, // should be skipped
	}

	query := generateQuery(columns, "Account") + " LIMIT 5"
	t.Logf("generated SOQL: %s", query)

	result, err := client.Query(query)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("returned %d records from generated query", len(result.Records))
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

func TestIntegration_LoginRefreshToken(t *testing.T) {
	_ = godotenv.Load("../.env")
	if os.Getenv("SALESFORCE_REFRESH_TOKEN") == "" {
		t.Skip("SALESFORCE_REFRESH_TOKEN not set")
	}
	client := loadEnvAndCreateClient(t)
	if client == nil {
		t.Fatal("client is nil after refresh_token auth")
	}
	result, err := client.Query("SELECT Id FROM Organization")
	if err != nil {
		t.Fatalf("query failed with refresh_token auth: %v", err)
	}
	t.Logf("refresh_token auth: Organization Id = %s", result.Records[0].ID())
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
