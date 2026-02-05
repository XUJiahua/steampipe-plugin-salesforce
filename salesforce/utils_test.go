package salesforce

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"

	"github.com/turbot/steampipe-plugin-sdk/v5/grpc/proto"
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin"
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin/quals"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetSalesforceColumnName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple id", "id", "Id"},
		{"compound name", "account_id", "AccountId"},
		{"multi-word", "created_by_id", "CreatedById"},
		{"custom field unchanged", "my_field__c", "my_field__c"},
		{"custom field with caps unchanged", "My_Custom__c", "My_Custom__c"},
		{"single word", "name", "Name"},
		{"already camel", "Name", "Name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSalesforceColumnName(tt.input)
			if got != tt.expected {
				t.Errorf("getSalesforceColumnName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGenerateQuery(t *testing.T) {
	tests := []struct {
		name      string
		columns   []*plugin.Column
		tableName string
		expected  string
	}{
		{
			name: "basic columns",
			columns: []*plugin.Column{
				{Name: "id", Type: proto.ColumnType_STRING},
				{Name: "name", Type: proto.ColumnType_STRING},
			},
			tableName: "Account",
			expected:  "SELECT Id, Name FROM Account",
		},
		{
			name: "skips organization_id snake_case",
			columns: []*plugin.Column{
				{Name: "id", Type: proto.ColumnType_STRING},
				{Name: "organization_id", Type: proto.ColumnType_STRING},
				{Name: "name", Type: proto.ColumnType_STRING},
			},
			tableName: "Account",
			expected:  "SELECT Id, Name FROM Account",
		},
		{
			name: "skips OrganizationId api_native",
			columns: []*plugin.Column{
				{Name: "Id", Type: proto.ColumnType_STRING},
				{Name: "OrganizationId", Type: proto.ColumnType_STRING},
				{Name: "Name", Type: proto.ColumnType_STRING},
			},
			tableName: "Account",
			expected:  "SELECT Id, Name FROM Account",
		},
		{
			name: "custom fields preserved",
			columns: []*plugin.Column{
				{Name: "id", Type: proto.ColumnType_STRING},
				{Name: "my_field__c", Type: proto.ColumnType_STRING},
			},
			tableName: "CustomObj__c",
			expected:  "SELECT Id, my_field__c FROM CustomObj__c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateQuery(tt.columns, tt.tableName)
			if got != tt.expected {
				t.Errorf("generateQuery() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIsColumnAvailable(t *testing.T) {
	columns := []*plugin.Column{
		{Name: "id"},
		{Name: "name"},
		{Name: "email"},
	}

	tests := []struct {
		name     string
		colName  string
		expected bool
	}{
		{"found", "id", true},
		{"found middle", "name", true},
		{"not found", "phone", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isColumnAvailable(tt.colName, columns)
			if got != tt.expected {
				t.Errorf("isColumnAvailable(%q) = %v, want %v", tt.colName, got, tt.expected)
			}
		})
	}

	// empty slice
	t.Run("empty slice", func(t *testing.T) {
		if isColumnAvailable("id", []*plugin.Column{}) {
			t.Error("isColumnAvailable with empty slice should return false")
		}
	})
}

func TestDecodeQueryResult(t *testing.T) {
	ctx := context.Background()

	t.Run("valid roundtrip", func(t *testing.T) {
		input := map[string]interface{}{
			"Id":   "001xx000003DGbYAAW",
			"Name": "Test Account",
		}
		output := map[string]interface{}{}
		err := decodeQueryResult(ctx, input, &output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if output["Id"] != "001xx000003DGbYAAW" {
			t.Errorf("Id = %v, want %v", output["Id"], "001xx000003DGbYAAW")
		}
		if output["Name"] != "Test Account" {
			t.Errorf("Name = %v, want %v", output["Name"], "Test Account")
		}
	})

	t.Run("slice roundtrip", func(t *testing.T) {
		input := []map[string]interface{}{
			{"Id": "001", "Name": "A"},
			{"Id": "002", "Name": "B"},
		}
		output := []map[string]interface{}{}
		err := decodeQueryResult(ctx, input, &output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(output) != 2 {
			t.Errorf("len = %d, want 2", len(output))
		}
	})

	t.Run("invalid input returns error", func(t *testing.T) {
		// channels can't be marshaled to JSON
		input := make(chan int)
		output := map[string]interface{}{}
		err := decodeQueryResult(ctx, input, &output)
		if err == nil {
			t.Error("expected error for non-marshalable input, got nil")
		}
	})
}

// helper to make a KeyColumnQualMap entry
func makeQualMap(columnName string, operator string, value *proto.QualValue) plugin.KeyColumnQualMap {
	return plugin.KeyColumnQualMap{
		columnName: &plugin.KeyColumnQuals{
			Name: columnName,
			Quals: quals.QualSlice{
				&quals.Qual{
					Column:   columnName,
					Operator: operator,
					Value:    value,
				},
			},
		},
	}
}

func TestBuildQueryFromQuals(t *testing.T) {
	t.Run("string equals", func(t *testing.T) {
		qualMap := makeQualMap("name", "=", &proto.QualValue{
			Value: &proto.QualValue_StringValue{StringValue: "Acme"},
		})
		cols := []*plugin.Column{{Name: "name", Type: proto.ColumnType_STRING}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "Name = 'Acme'"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("string not equals", func(t *testing.T) {
		qualMap := makeQualMap("name", "<>", &proto.QualValue{
			Value: &proto.QualValue_StringValue{StringValue: "Acme"},
		})
		cols := []*plugin.Column{{Name: "name", Type: proto.ColumnType_STRING}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "Name != 'Acme'"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("string IN list", func(t *testing.T) {
		qualMap := plugin.KeyColumnQualMap{
			"name": &plugin.KeyColumnQuals{
				Name: "name",
				Quals: quals.QualSlice{
					&quals.Qual{
						Column:   "name",
						Operator: "=",
						Value: &proto.QualValue{
							Value: &proto.QualValue_ListValue{
								ListValue: &proto.QualValueList{
									Values: []*proto.QualValue{
										{Value: &proto.QualValue_StringValue{StringValue: "Acme"}},
										{Value: &proto.QualValue_StringValue{StringValue: "Globex"}},
									},
								},
							},
						},
					},
				},
			},
		}
		cols := []*plugin.Column{{Name: "name", Type: proto.ColumnType_STRING}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "Name IN ('Acme','Globex')"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("string NOT IN list", func(t *testing.T) {
		qualMap := plugin.KeyColumnQualMap{
			"name": &plugin.KeyColumnQuals{
				Name: "name",
				Quals: quals.QualSlice{
					&quals.Qual{
						Column:   "name",
						Operator: "<>",
						Value: &proto.QualValue{
							Value: &proto.QualValue_ListValue{
								ListValue: &proto.QualValueList{
									Values: []*proto.QualValue{
										{Value: &proto.QualValue_StringValue{StringValue: "Acme"}},
										{Value: &proto.QualValue_StringValue{StringValue: "Globex"}},
									},
								},
							},
						},
					},
				},
			},
		}
		cols := []*plugin.Column{{Name: "name", Type: proto.ColumnType_STRING}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "Name NOT IN ('Acme','Globex')"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("bool equals true", func(t *testing.T) {
		qualMap := makeQualMap("is_active", "=", &proto.QualValue{
			Value: &proto.QualValue_BoolValue{BoolValue: true},
		})
		cols := []*plugin.Column{{Name: "is_active", Type: proto.ColumnType_BOOL}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "IsActive = TRUE"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("bool not equals", func(t *testing.T) {
		qualMap := makeQualMap("is_active", "<>", &proto.QualValue{
			Value: &proto.QualValue_BoolValue{BoolValue: false},
		})
		cols := []*plugin.Column{{Name: "is_active", Type: proto.ColumnType_BOOL}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "IsActive = FALSE"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("int equals", func(t *testing.T) {
		qualMap := makeQualMap("number_of_employees", "=", &proto.QualValue{
			Value: &proto.QualValue_Int64Value{Int64Value: 100},
		})
		cols := []*plugin.Column{{Name: "number_of_employees", Type: proto.ColumnType_INT}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "NumberOfEmployees = 100"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("int not equals", func(t *testing.T) {
		qualMap := makeQualMap("number_of_employees", "<>", &proto.QualValue{
			Value: &proto.QualValue_Int64Value{Int64Value: 0},
		})
		cols := []*plugin.Column{{Name: "number_of_employees", Type: proto.ColumnType_INT}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "NumberOfEmployees != 0"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("int greater than", func(t *testing.T) {
		qualMap := makeQualMap("number_of_employees", ">", &proto.QualValue{
			Value: &proto.QualValue_Int64Value{Int64Value: 50},
		})
		cols := []*plugin.Column{{Name: "number_of_employees", Type: proto.ColumnType_INT}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "NumberOfEmployees > 50"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("double equals", func(t *testing.T) {
		qualMap := makeQualMap("amount", "=", &proto.QualValue{
			Value: &proto.QualValue_DoubleValue{DoubleValue: 99.5},
		})
		cols := []*plugin.Column{{Name: "amount", Type: proto.ColumnType_DOUBLE}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "Amount = 99.500000"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("double not equals", func(t *testing.T) {
		qualMap := makeQualMap("amount", "<>", &proto.QualValue{
			Value: &proto.QualValue_DoubleValue{DoubleValue: 0.0},
		})
		cols := []*plugin.Column{{Name: "amount", Type: proto.ColumnType_DOUBLE}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		expected := "Amount != 0.000000"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("timestamp dateTime", func(t *testing.T) {
		ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		qualMap := makeQualMap("created_date", ">=", &proto.QualValue{
			Value: &proto.QualValue_TimestampValue{TimestampValue: timestamppb.New(ts)},
		})
		cols := []*plugin.Column{{Name: "created_date", Type: proto.ColumnType_TIMESTAMP}}
		sfCols := map[string]string{"created_date": "dateTime"}
		got := buildQueryFromQuals(qualMap, cols, sfCols)
		expected := "CreatedDate >= 2024-01-15T10:30:00Z"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("timestamp date type", func(t *testing.T) {
		ts := time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)
		qualMap := makeQualMap("birth_date", "=", &proto.QualValue{
			Value: &proto.QualValue_TimestampValue{TimestampValue: timestamppb.New(ts)},
		})
		cols := []*plugin.Column{{Name: "birth_date", Type: proto.ColumnType_TIMESTAMP}}
		sfCols := map[string]string{"birth_date": "date"}
		got := buildQueryFromQuals(qualMap, cols, sfCols)
		expected := "BirthDate = 2024-06-20"
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("multiple filters with AND", func(t *testing.T) {
		qualMap := plugin.KeyColumnQualMap{
			"name": &plugin.KeyColumnQuals{
				Name: "name",
				Quals: quals.QualSlice{
					&quals.Qual{
						Column:   "name",
						Operator: "=",
						Value:    &proto.QualValue{Value: &proto.QualValue_StringValue{StringValue: "Acme"}},
					},
				},
			},
			"is_active": &plugin.KeyColumnQuals{
				Name: "is_active",
				Quals: quals.QualSlice{
					&quals.Qual{
						Column:   "is_active",
						Operator: "=",
						Value:    &proto.QualValue{Value: &proto.QualValue_BoolValue{BoolValue: true}},
					},
				},
			},
		}
		cols := []*plugin.Column{
			{Name: "name", Type: proto.ColumnType_STRING},
			{Name: "is_active", Type: proto.ColumnType_BOOL},
		}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		// Both filters should be present joined by AND
		if got != "Name = 'Acme' AND IsActive = TRUE" && got != "IsActive = TRUE AND Name = 'Acme'" {
			// The order depends on map iteration, so check both contain parts
			if !contains(got, "Name = 'Acme'") || !contains(got, "IsActive = TRUE") || !contains(got, " AND ") {
				t.Errorf("got %q, want both filters joined by AND", got)
			}
		}
	})

	t.Run("empty quals returns empty string", func(t *testing.T) {
		qualMap := plugin.KeyColumnQualMap{}
		cols := []*plugin.Column{{Name: "name", Type: proto.ColumnType_STRING}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("qual not in column list is ignored", func(t *testing.T) {
		qualMap := makeQualMap("phone", "=", &proto.QualValue{
			Value: &proto.QualValue_StringValue{StringValue: "555"},
		})
		// Column list doesn't include "phone"
		cols := []*plugin.Column{{Name: "name", Type: proto.ColumnType_STRING}}
		got := buildQueryFromQuals(qualMap, cols, map[string]string{})
		if got != "" {
			t.Errorf("got %q, want empty string for non-matching column", got)
		}
	})
}

func stringPtr(s string) *string { return &s }

func TestGetConfig_NewFields(t *testing.T) {
	// Verify the struct has the new fields by setting them
	cfg := salesforceConfig{
		AccessToken:    stringPtr("tok_123"),
		PrivateKey:     stringPtr("-----BEGIN RSA PRIVATE KEY-----"),
		PrivateKeyFile: stringPtr("/path/to/key.pem"),
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

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
