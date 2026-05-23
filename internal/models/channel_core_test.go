package models

import (
	"reflect"
	"testing"
)

func TestChannelCore_FieldsPresent(t *testing.T) {
	expected := []string{
		"ID", "Name", "Type", "Status", "BaseURL", "Weight", "Priority",
		"SupportedAPITypes", "Endpoints", "PassthroughEnabled", "UseLegacyAdaptor",
		"Organization", "ApiVersion", "SystemPrompt", "SystemPromptInInput", "RoleMapping",
		"ParamOverride", "Setting", "Tag", "Remark", "TestModel", "AutoBan",
		"StatusCodeMapping", "OtherSettings", "CreatedAt", "UpdatedAt",
	}
	typ := reflect.TypeOf(ChannelCore{})
	for _, name := range expected {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("ChannelCore missing field %q", name)
		}
	}
}

func TestChannelEmbedsCore(t *testing.T) {
	typ := reflect.TypeOf(Channel{})
	if _, ok := typ.FieldByName("ChannelCore"); !ok {
		t.Fatal("Channel must embed ChannelCore")
	}
	if _, ok := typ.FieldByName("BaseURL"); !ok {
		t.Fatal("Channel.BaseURL not promoted")
	}
	// admin-specific fields stay on the outer struct
	for _, name := range []string{"Key", "Models", "ModelMapping", "ProxyURL", "HeaderOverride"} {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("Channel missing admin-only field %q", name)
		}
		if f.Anonymous {
			t.Fatalf("Channel field %q should not be anonymous", name)
		}
	}
}

func TestPrivateChannelEmbedsCore(t *testing.T) {
	typ := reflect.TypeOf(PrivateChannel{})
	if _, ok := typ.FieldByName("ChannelCore"); !ok {
		t.Fatal("PrivateChannel must embed ChannelCore")
	}
	if _, ok := typ.FieldByName("BaseURL"); !ok {
		t.Fatal("PrivateChannel.BaseURL not promoted")
	}
}

func TestPrivateChannelOwnerFields(t *testing.T) {
	typ := reflect.TypeOf(PrivateChannel{})
	for _, name := range []string{"OwnerID", "KeyCipher", "KeyLast4", "Models", "ModelMapping"} {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("PrivateChannel missing %q", name)
		}
	}
}

func TestSchemaIntact_ChannelTable(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, col := range []string{
		"id", "name", "type", "base_url", "weight", "priority", "status",
		"supported_api_types", "endpoints", "passthrough_enabled", "use_legacy_adaptor",
		"organization", "api_version", "system_prompt", "system_prompt_in_input", "role_mapping",
		"param_override", "setting", "tag", "remark", "test_model", "auto_ban",
		"status_code_mapping", "other_settings", "created_at", "updated_at",
		// admin-specific
		"key", "models", "model_mapping", "proxy_url", "header_override",
	} {
		if !db.Migrator().HasColumn(&Channel{}, col) {
			t.Errorf("channels.%s column missing after embed", col)
		}
	}

	// Tag index is declared on the outer Channel struct as an embed-tag
	// override (admin lookups filter by tag; BYOK does not). If the override
	// regresses the embedded tag would win and the index would disappear.
	if !db.Migrator().HasIndex(&Channel{}, "idx_channels_tag") {
		t.Fatal("Channel.Tag index missing (embed override broken)")
	}
}

func TestSchemaIntact_PrivateChannelTable(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, col := range []string{
		"id", "name", "type", "base_url", "weight", "priority", "status",
		"supported_api_types", "endpoints", "passthrough_enabled", "use_legacy_adaptor",
		"organization", "api_version", "system_prompt", "system_prompt_in_input", "role_mapping",
		"param_override", "setting", "tag", "remark", "test_model", "auto_ban",
		"status_code_mapping", "other_settings", "created_at", "updated_at",
		// BYOK-specific
		"owner_id", "key_cipher", "key_last4", "models", "model_mapping",
	} {
		if !db.Migrator().HasColumn(&PrivateChannel{}, col) {
			t.Errorf("private_channels.%s column missing after embed", col)
		}
	}

	// Composite indexes are declared via ChannelCore embed-tag overrides on
	// PrivateChannel.Name / .Status. If the override regresses, the embedded
	// tags would win and we'd silently lose the (owner_id, ...) indexes that
	// scope BYOK queries.
	if !db.Migrator().HasIndex(&PrivateChannel{}, "idx_pchan_owner_status") {
		t.Fatal("PrivateChannel composite index idx_pchan_owner_status missing")
	}
	if !db.Migrator().HasIndex(&PrivateChannel{}, "uidx_pchan_owner_name") {
		t.Fatal("PrivateChannel unique composite index uidx_pchan_owner_name missing")
	}
}
