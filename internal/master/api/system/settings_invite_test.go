package system

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
)

func TestSettingDefs_InviteKeysPresentWithDefaults(t *testing.T) {
	cases := map[string]string{
		consts.SettingKeyInviteEnabled:      "false",
		consts.SettingKeyInviteUserMaxCodes: "5",
		consts.SettingKeyInviteUserMaxUses:  "1",
	}
	for key, want := range cases {
		def, ok := settingDefs[key]
		if !ok {
			t.Fatalf("settingDefs missing key %q", key)
		}
		if def.Default != want {
			t.Errorf("%s default = %q, want %q", key, def.Default, want)
		}
	}
}

func TestSettingDefs_InviteValidation(t *testing.T) {
	enabled := settingDefs[consts.SettingKeyInviteEnabled]
	if !enabled.Validate("true") || !enabled.Validate("false") || enabled.Validate("maybe") {
		t.Error("invite_enabled validator wrong")
	}

	maxCodes := settingDefs[consts.SettingKeyInviteUserMaxCodes]
	if !maxCodes.Validate("0") || maxCodes.Validate("-1") {
		t.Error("invite_user_max_codes should allow 0, reject -1")
	}
	if !maxCodes.Validate("10000") || maxCodes.Validate("10001") || maxCodes.Validate("abc") {
		t.Error("invite_user_max_codes should allow 10000, reject 10001 and non-numeric")
	}

	maxUses := settingDefs[consts.SettingKeyInviteUserMaxUses]
	if maxUses.Validate("0") || !maxUses.Validate("1") {
		t.Error("invite_user_max_uses should reject 0, allow 1")
	}
	if !maxUses.Validate("10000") || maxUses.Validate("10001") || maxUses.Validate("abc") {
		t.Error("invite_user_max_uses should allow 10000, reject 10001 and non-numeric")
	}
}
