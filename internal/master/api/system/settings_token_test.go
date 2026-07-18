package system

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
)

func TestSettingDefs_TokenModelWhitelistSelfService(t *testing.T) {
	def, ok := settingDefs[consts.SettingKeyTokenModelWhitelistSelfService]
	if !ok {
		t.Fatal("token model whitelist self-service setting is not registered")
	}
	if def.Default != "false" {
		t.Fatalf("default = %q, want false", def.Default)
	}
	if !def.Validate("true") || !def.Validate("false") || def.Validate("1") || def.Validate("") {
		t.Fatal("setting must accept only true or false")
	}
}
