package salesforce

import (
	"context"
	"testing"

	"github.com/turbot/steampipe-plugin-sdk/v5/plugin/transform"
)

func TestGetFieldFromSObjectMap(t *testing.T) {
	ctx := context.Background()

	t.Run("field exists", func(t *testing.T) {
		data := &transform.TransformData{
			Param:       "AccountId",
			HydrateItem: map[string]interface{}{"AccountId": "001xx000003DGbY", "Name": "Test"},
		}
		got, err := getFieldFromSObjectMap(ctx, data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "001xx000003DGbY" {
			t.Errorf("got %v, want %v", got, "001xx000003DGbY")
		}
	})

	t.Run("field missing returns nil", func(t *testing.T) {
		data := &transform.TransformData{
			Param:       "MissingField",
			HydrateItem: map[string]interface{}{"Name": "Test"},
		}
		got, err := getFieldFromSObjectMap(ctx, data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestGetFieldFromSObjectMapByColumnName(t *testing.T) {
	ctx := context.Background()

	t.Run("snake_case column resolves to CamelCase", func(t *testing.T) {
		data := &transform.TransformData{
			ColumnName:  "account_id",
			HydrateItem: map[string]interface{}{"AccountId": "001xx000003DGbY"},
		}
		got, err := getFieldFromSObjectMapByColumnName(ctx, data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "001xx000003DGbY" {
			t.Errorf("got %v, want %v", got, "001xx000003DGbY")
		}
	})

	t.Run("custom __c field unchanged", func(t *testing.T) {
		data := &transform.TransformData{
			ColumnName:  "my_field__c",
			HydrateItem: map[string]interface{}{"my_field__c": "custom_value"},
		}
		got, err := getFieldFromSObjectMapByColumnName(ctx, data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "custom_value" {
			t.Errorf("got %v, want %v", got, "custom_value")
		}
	})

	t.Run("missing field returns nil", func(t *testing.T) {
		data := &transform.TransformData{
			ColumnName:  "nonexistent",
			HydrateItem: map[string]interface{}{"Name": "Test"},
		}
		got, err := getFieldFromSObjectMapByColumnName(ctx, data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}
