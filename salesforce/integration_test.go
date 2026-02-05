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

	// Load .env from repo root
	_ = godotenv.Load("../.env")

	url := os.Getenv("SALESFORCE_URL")
	username := os.Getenv("SALESFORCE_USERNAME")
	password := os.Getenv("SALESFORCE_PASSWORD")
	token := os.Getenv("SALESFORCE_TOKEN")
	clientID := os.Getenv("SALESFORCE_CLIENT_ID")

	if url == "" || username == "" || password == "" {
		t.Skip("SALESFORCE_URL, SALESFORCE_USERNAME, SALESFORCE_PASSWORD must be set")
	}

	if clientID == "" {
		clientID = "steampipe"
	}

	client := simpleforce.NewClient(url, clientID, simpleforce.DefaultAPIVersion)
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
