package salesforce

import (
	"context"
	"testing"

	"github.com/turbot/steampipe-plugin-sdk/v5/grpc/proto"
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin"
)

func strPtr(s string) *NamingConventionEnum {
	v := NamingConventionEnum(s)
	return &v
}

func TestCheckNameScheme(t *testing.T) {
	tests := []struct {
		name           string
		config         salesforceConfig
		dynamicColumns []*plugin.Column
		expected       string
	}{
		{
			name:           "nil naming convention returns id",
			config:         salesforceConfig{NamingConvention: nil},
			dynamicColumns: nil,
			expected:       "id",
		},
		{
			name:           "snake_case returns id",
			config:         salesforceConfig{NamingConvention: strPtr("snake_case")},
			dynamicColumns: []*plugin.Column{{Name: "id"}},
			expected:       "id",
		},
		{
			name:   "api_native with columns returns Id",
			config: salesforceConfig{NamingConvention: strPtr("api_native")},
			dynamicColumns: []*plugin.Column{
				{Name: "Id"},
				{Name: "Name"},
			},
			expected: "Id",
		},
		{
			name:           "api_native with empty columns returns id",
			config:         salesforceConfig{NamingConvention: strPtr("api_native")},
			dynamicColumns: []*plugin.Column{},
			expected:       "id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkNameScheme(tt.config, tt.dynamicColumns)
			if got != tt.expected {
				t.Errorf("checkNameScheme() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMergeTableColumns(t *testing.T) {
	ctx := context.Background()

	t.Run("default merges static and dynamic with dedup", func(t *testing.T) {
		config := salesforceConfig{NamingConvention: nil}
		static := []*plugin.Column{
			{Name: "id", Type: proto.ColumnType_STRING},
			{Name: "name", Type: proto.ColumnType_STRING},
		}
		dynamic := []*plugin.Column{
			{Name: "id", Type: proto.ColumnType_STRING},   // duplicate
			{Name: "email", Type: proto.ColumnType_STRING}, // new
		}
		got := mergeTableColumns(ctx, config, dynamic, static)

		// Should have id, name (from static) + email (from dynamic)
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		names := map[string]bool{}
		for _, c := range got {
			names[c.Name] = true
		}
		for _, expected := range []string{"id", "name", "email"} {
			if !names[expected] {
				t.Errorf("missing column %q", expected)
			}
		}
	})

	t.Run("api_native returns only dynamic", func(t *testing.T) {
		config := salesforceConfig{NamingConvention: strPtr("api_native")}
		static := []*plugin.Column{
			{Name: "id", Type: proto.ColumnType_STRING},
		}
		dynamic := []*plugin.Column{
			{Name: "Id", Type: proto.ColumnType_STRING},
			{Name: "Name", Type: proto.ColumnType_STRING},
		}
		got := mergeTableColumns(ctx, config, dynamic, static)

		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].Name != "Id" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "Id")
		}
	})

	t.Run("api_native with empty dynamic returns static", func(t *testing.T) {
		config := salesforceConfig{NamingConvention: strPtr("api_native")}
		static := []*plugin.Column{
			{Name: "id", Type: proto.ColumnType_STRING},
			{Name: "name", Type: proto.ColumnType_STRING},
		}
		dynamic := []*plugin.Column{}
		got := mergeTableColumns(ctx, config, dynamic, static)

		// api_native with empty dynamic falls through to default path
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].Name != "id" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "id")
		}
	})

	t.Run("empty dynamic returns static only", func(t *testing.T) {
		config := salesforceConfig{}
		static := []*plugin.Column{
			{Name: "id", Type: proto.ColumnType_STRING},
		}
		got := mergeTableColumns(ctx, config, []*plugin.Column{}, static)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Name != "id" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "id")
		}
	})
}
