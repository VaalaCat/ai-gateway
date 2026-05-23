package private_channel

import (
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// stubApp satisfies app.Application by embedding the interface and overriding only GetDB.
// Calls to any other method will panic — validators only use GetDB so those panics never fire.
type stubApp struct {
	app.Application
	db *gorm.DB
}

func (s *stubApp) GetDB() *gorm.DB { return s.db }

func newValidatorTestCtx(t *testing.T, userID, groupID uint) *app.Context {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed default group: %v", err)
	}
	// Seed user so group membership exists
	db.Create(&models.User{ID: userID, GroupID: groupID, Username: "testuser"})

	return &app.Context{
		App:      &stubApp{db: db},
		UserInfo: &app.UserInfo{UserID: userID, GroupID: groupID},
	}
}

// newCreateCtx returns a ValidatorCtx wired as Create-path (Dirty=nil) for the given owner/group.
func newCreateCtx(appCtx *app.Context, ownerID, groupID uint, req map[string]any) ValidatorCtx {
	return ValidatorCtx{
		Query:   dao.NewAdminQuery(dao.NewContext(appCtx.App)),
		Req:     req,
		Dirty:   nil,
		OwnerID: ownerID,
		GroupID: groupID,
	}
}

func TestValidateBaseURLAllowlist_SystemMatch(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": "https://api.openai.com/v1"})
	if err := validateBaseURLAllowlistCtx(c); err != nil {
		t.Fatalf("system prefix should match: %v", err)
	}
}

func TestValidateBaseURLAllowlist_AdminMatch(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_base_url_allowlist", Value: `["https://my.corp.com/llm"]`})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": "https://my.corp.com/llm/v1"})
	if err := validateBaseURLAllowlistCtx(c); err != nil {
		t.Fatalf("admin allowlist should match: %v", err)
	}
}

func TestValidateBaseURLAllowlist_Reject(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": "http://10.0.0.1:9000/v1"})
	if err := validateBaseURLAllowlistCtx(c); err == nil {
		t.Fatal("private IP should reject")
	}
}

func TestValidateBaseURLAllowlist_EmptyPass(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": ""})
	if err := validateBaseURLAllowlistCtx(c); err != nil {
		t.Fatalf("empty BaseURL should pass: %v", err)
	}
}

func TestValidateBaseURLAllowlist_CorruptSettingFallsBackToSystem(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_base_url_allowlist", Value: "not-json"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": "https://api.openai.com/v1"})
	if err := validateBaseURLAllowlistCtx(c); err != nil {
		t.Fatalf("system prefix still works when admin allowlist is corrupt: %v", err)
	}
}

// === §1.1 SSRF: strings.HasPrefix → url.Parse host/scheme/path-segment check ===

func TestValidateBaseURLAllowlist_SSRFAttackRejected(t *testing.T) {
	// System allowlist already contains https://api.openai.com.
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid OpenAI v1", "https://api.openai.com/v1/chat/completions", false},
		{"valid root", "https://api.openai.com", false},
		{"valid root with slash", "https://api.openai.com/", false},
		{"SSRF host suffix", "https://api.openai.com.attacker.com/v1", true},
		{"SSRF host prefix in path", "https://attacker.com/api.openai.com", true},
		{"scheme mismatch http", "http://api.openai.com/v1", true},
		{"empty on create", "", false},
		{"malformed url", "://bad", true},
		{"no scheme", "api.openai.com/v1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			appCtx := newValidatorTestCtx(t, 1, 1)
			ctx := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": c.url})
			err := validateBaseURLAllowlistCtx(ctx)
			if (err != nil) != c.wantErr {
				t.Fatalf("base_url=%q: got err=%v, want err=%v", c.url, err, c.wantErr)
			}
		})
	}
}

func TestValidateBaseURLAllowlist_PathSegmentPrefix(t *testing.T) {
	// Admin allowlist with /v1 path — child must match on segment boundary.
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"under /v1", "https://api.example.com/v1/chat", false},
		{"exact /v1", "https://api.example.com/v1", false},
		{"exact /v1 with trailing slash", "https://api.example.com/v1/", false},
		{"/v1 with query", "https://api.example.com/v1?x=1", false},
		{"/v1 with fragment", "https://api.example.com/v1#frag", false},
		{"v1beta crosses segment", "https://api.example.com/v1beta/chat", true},
		{"v2 different segment", "https://api.example.com/v2/chat", true},
		{"root rejected when prefix is /v1", "https://api.example.com/", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			appCtx := newValidatorTestCtx(t, 1, 1)
			appCtx.App.GetDB().Create(&models.Setting{Key: "byok_base_url_allowlist", Value: `["https://api.example.com/v1"]`})
			ctx := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": c.url})
			err := validateBaseURLAllowlistCtx(ctx)
			if (err != nil) != c.wantErr {
				t.Fatalf("base_url=%q: got err=%v, want err=%v", c.url, err, c.wantErr)
			}
		})
	}
}

func TestValidateBaseURLAllowlist_HostMustMatchIncludingPort(t *testing.T) {
	// Allowlist with explicit port — channel must match it.
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_base_url_allowlist", Value: `["https://corp.example.com:8443/llm"]`})

	// Exact host:port matches.
	ok := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": "https://corp.example.com:8443/llm/v1"})
	if err := validateBaseURLAllowlistCtx(ok); err != nil {
		t.Fatalf("exact host:port should match: %v", err)
	}

	// Different port rejected.
	bad := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": "https://corp.example.com:9000/llm/v1"})
	if err := validateBaseURLAllowlistCtx(bad); err == nil {
		t.Fatal("different port must be rejected")
	}
}

// === §1.6: PATCH base_url="" must be rejected on update ===

func TestValidateBaseURLAllowlist_EmptyRejectedOnUpdate(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	ctx := ValidatorCtx{
		Query:   dao.NewAdminQuery(dao.NewContext(appCtx.App)),
		Req:     map[string]any{"base_url": ""},
		Dirty:   map[string]bool{"base_url": true}, // explicit patch
		OwnerID: 1,
		GroupID: 1,
	}
	if err := validateBaseURLAllowlistCtx(ctx); err == nil {
		t.Fatal("PATCH base_url='' must be rejected")
	}
}

func TestValidateBaseURLAllowlist_EmptyAllowedOnCreate(t *testing.T) {
	// Create path (Dirty=nil) with empty base_url is allowed — falls back to type built-in URL.
	appCtx := newValidatorTestCtx(t, 1, 1)
	ctx := newCreateCtx(appCtx, 1, 1, map[string]any{"base_url": ""})
	if err := validateBaseURLAllowlistCtx(ctx); err != nil {
		t.Fatalf("Create-path empty base_url should pass: %v", err)
	}
}

func TestValidateModelsSubsetOfModelConfigs_AllPresent(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.ModelConfig{ModelName: "gpt-4o"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"models": []string{"gpt-4o"}})
	if err := validateModelsSubsetOfModelConfigsCtx(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidateModelsSubsetOfModelConfigs_Rejects(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.ModelConfig{ModelName: "gpt-4o"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"models": []string{"gpt-99"}})
	if err := validateModelsSubsetOfModelConfigsCtx(c); err == nil {
		t.Fatal("unknown model should reject")
	}
}

func TestValidateUserUnderChannelLimit_Overflow(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_max_channels_per_user", Value: "2"})
	db := appCtx.App.GetDB()
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "b", Status: 1})

	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	if err := validateUserUnderChannelLimitCtx(c); err == nil {
		t.Fatal("at-limit should reject")
	}
}

func TestValidateNameUniquePerOwner_Conflict(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "dup", Status: 1})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"name": "dup"})
	if err := validateNameUniquenessCtx(c); err == nil {
		t.Fatal("duplicate name should conflict")
	}
}

func TestValidateBYOKEnabledForUser_GlobalOff(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_enabled", Value: "false"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	if err := validateBYOKEnabledForUserCtx(c); err == nil {
		t.Fatal("global off should reject")
	}
}

func TestValidateBYOKEnabledForUser_GroupOverrideOff(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	f := false
	db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_enabled", &f)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	if err := validateBYOKEnabledForUserCtx(c); err == nil {
		t.Fatal("group override off should reject")
	}
}

// === validateModelMappingTargets coverage ===

func TestValidateModelMappingTargets_Empty(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"model_mapping": map[string]string(nil)})
	if err := validateModelMappingTargetsCtx(c); err != nil {
		t.Fatalf("empty mapping should pass: %v", err)
	}
}

func TestValidateModelMappingTargets_KeyMissing(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.ModelConfig{ModelName: "gpt-4o"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"model_mapping": map[string]string{"unknown": "gpt-4o"}})
	if err := validateModelMappingTargetsCtx(c); err == nil {
		t.Fatal("mapping key not in ModelConfig should reject")
	}
}

func TestValidateModelMappingTargets_ValueMissing(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.ModelConfig{ModelName: "gpt-4o"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"model_mapping": map[string]string{"gpt-4o": "unknown"}})
	if err := validateModelMappingTargetsCtx(c); err == nil {
		t.Fatal("mapping value not in ModelConfig should reject")
	}
}

func TestValidateModelMappingTargets_BothPresent(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	db.Create(&models.ModelConfig{ModelName: "gpt-4o"})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o-mini"})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"model_mapping": map[string]string{"gpt-4o-mini": "gpt-4o"}})
	if err := validateModelMappingTargetsCtx(c); err != nil {
		t.Fatalf("both key and value registered should pass: %v", err)
	}
}

// === Happy-path positive tests for other validators ===

func TestValidateUserUnderChannelLimit_BelowLimit(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	// global default is 20; 0 channels < 20 limit should pass
	if err := validateUserUnderChannelLimitCtx(c); err != nil {
		t.Fatalf("0 channels < 20 limit should pass: %v", err)
	}
}

func TestValidateNameUniquePerOwner_UniqueName(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "existing", Status: 1})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"name": "fresh-name"})
	if err := validateNameUniquenessCtx(c); err != nil {
		t.Fatalf("unique name should pass: %v", err)
	}
}

func TestValidateNameUniquePerOwner_DifferentOwner(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1) // current user is owner 1
	appCtx.App.GetDB().Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 99, Name: "shared-name", Status: 1})
	c := newCreateCtx(appCtx, 1, 1, map[string]any{"name": "shared-name"})
	// Different owner has the name — current owner can reuse
	if err := validateNameUniquenessCtx(c); err != nil {
		t.Fatalf("cross-owner same name should pass: %v", err)
	}
}

func TestValidateBYOKEnabledForUser_DefaultPass(t *testing.T) {
	// Neither global setting nor group override set BYOK to false → default true → pass
	appCtx := newValidatorTestCtx(t, 1, 1)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	if err := validateBYOKEnabledForUserCtx(c); err != nil {
		t.Fatalf("default-on should pass: %v", err)
	}
}

func TestValidateBYOKEnabledForUser_GroupOverrideOn(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	tr := true
	db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_enabled", &tr)
	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	if err := validateBYOKEnabledForUserCtx(c); err != nil {
		t.Fatalf("group override on should pass: %v", err)
	}
}

// === RunValidators (Task 2): unified Create/Update runner with dirty-field self-check ===

func TestRunValidators_CreatePath_RunsAll(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	db.Create(&models.ModelConfig{ModelName: "gpt-4o"})

	q := dao.NewAdminQuery(dao.NewContext(appCtx.App))
	c := ValidatorCtx{
		Query:   q,
		OwnerID: 1,
		GroupID: 1,
		Dirty:   nil, // Create = all fields dirty
		Req: map[string]any{
			"name":          "fresh",
			"base_url":      "https://api.openai.com/v1",
			"models":        []string{"gpt-4o"},
			"model_mapping": map[string]string{"gpt-4o": "gpt-4o"},
		},
	}
	if err := RunValidators(c); err != nil {
		t.Fatalf("create path with valid inputs should pass: %v", err)
	}

	// Now break base_url → Create-path should reject (proving base_url validator ran).
	c.Req["base_url"] = "http://10.0.0.1:9000/v1"
	if err := RunValidators(c); err == nil {
		t.Fatal("create path should reject disallowed base_url")
	}
}

func TestRunValidators_PatchPath_SkipsCleanFields(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	db.Create(&models.ModelConfig{ModelName: "gpt-4o"})

	q := dao.NewAdminQuery(dao.NewContext(appCtx.App))
	// Patch only `weight` — even though base_url in Req is disallowed, base_url validator
	// must NOT run (it is clean). model validators must NOT run (clean).
	c := ValidatorCtx{
		Query:   q,
		OwnerID: 1,
		GroupID: 1,
		Dirty:   map[string]bool{"weight": true},
		Req: map[string]any{
			"weight":   uint(5),
			"base_url": "http://10.0.0.1:9000/v1", // would fail if validator ran
			"models":   []any{"unknown-model"},    // would fail if validator ran
		},
	}
	if err := RunValidators(c); err != nil {
		t.Fatalf("patch path should skip clean fields: %v", err)
	}
}

func TestRunValidators_PatchPath_RunsDirtyFields(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	db.Create(&models.ModelConfig{ModelName: "gpt-4o"})

	q := dao.NewAdminQuery(dao.NewContext(appCtx.App))
	// Patch model_mapping with an unregistered target — should reject.
	c := ValidatorCtx{
		Query:   q,
		OwnerID: 1,
		GroupID: 1,
		Dirty:   map[string]bool{"model_mapping": true},
		Req: map[string]any{
			"model_mapping": map[string]string{"gpt-4o": "unknown-target"},
		},
	}
	if err := RunValidators(c); err == nil {
		t.Fatal("patch path should reject illegal model_mapping")
	}
}

func TestRunValidators_AlwaysRunsBYOKEnabledCheck(t *testing.T) {
	appCtx := newValidatorTestCtx(t, 1, 1)
	// Globally disable BYOK.
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_enabled", Value: "false"})

	q := dao.NewAdminQuery(dao.NewContext(appCtx.App))
	// Patch a field unrelated to BYOK toggle — runner must STILL reject.
	c := ValidatorCtx{
		Query:   q,
		OwnerID: 1,
		GroupID: 1,
		Dirty:   map[string]bool{"weight": true},
		Req:     map[string]any{"weight": uint(3)},
	}
	if err := RunValidators(c); err == nil {
		t.Fatal("BYOK globally off should reject every patch path")
	}
}

// === §5.4: BYOKMaxChannels semantics — 0 = disabled, <0 invalid (rejected on write),
// positive = effective cap, nil = inherit global default ===

// asAPIErr extracts *api.APIError for HTTP-status assertions.
func asAPIErr(t *testing.T, err error) *apiErrShape {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// internal/master/api.APIError is the only Status carrier in this codebase.
	type statusErr interface{ Error() string }
	_ = statusErr(err)
	// Use a tiny shim via type assertion to avoid importing api here when not needed.
	return &apiErrShape{err: err}
}

type apiErrShape struct{ err error }

func (s *apiErrShape) message() string { return s.err.Error() }

func TestValidateByokQuota_Disabled_GroupOverrideZero(t *testing.T) {
	// Group overrides BYOKMaxChannels = 0 → quota disabled.
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	zero := 0
	db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_max_channels", &zero)

	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	err := validateUserUnderChannelLimitCtx(c)
	if err == nil {
		t.Fatal("BYOKMaxChannels=0 (group override) should disable quota")
	}
	if msg := asAPIErr(t, err).message(); !contains(msg, "disabled") {
		t.Fatalf("expected 'disabled' in error message, got %q", msg)
	}
}

func TestValidateByokQuota_Disabled_SystemSettingZero(t *testing.T) {
	// No group override; global setting byok_max_channels_per_user = 0 → disabled.
	appCtx := newValidatorTestCtx(t, 1, 1)
	appCtx.App.GetDB().Create(&models.Setting{Key: "byok_max_channels_per_user", Value: "0"})

	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	err := validateUserUnderChannelLimitCtx(c)
	if err == nil {
		t.Fatal("system byok_max_channels_per_user=0 should disable quota")
	}
	if msg := asAPIErr(t, err).message(); !contains(msg, "disabled") {
		t.Fatalf("expected 'disabled' in error message, got %q", msg)
	}
}

func TestValidateByokQuota_Reached(t *testing.T) {
	// Group BYOKMaxChannels = 2; already 2 channels → quota reached.
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	two := 2
	db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_max_channels", &two)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "b", Status: 1})

	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	err := validateUserUnderChannelLimitCtx(c)
	if err == nil {
		t.Fatal("count >= limit should reject")
	}
	if msg := asAPIErr(t, err).message(); !contains(msg, "reached") {
		t.Fatalf("expected 'reached' in error message, got %q", msg)
	}
}

func TestValidateByokQuota_AvailableSlot(t *testing.T) {
	// Group BYOKMaxChannels = 2; 1 existing → 1 slot left → pass.
	appCtx := newValidatorTestCtx(t, 1, 1)
	db := appCtx.App.GetDB()
	two := 2
	db.Model(&models.UserGroup{}).Where("id = 1").Update("byok_max_channels", &two)
	db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 1, Name: "a", Status: 1})

	c := newCreateCtx(appCtx, 1, 1, map[string]any{})
	if err := validateUserUnderChannelLimitCtx(c); err != nil {
		t.Fatalf("1 < 2 should pass: %v", err)
	}
}

// contains is a tiny helper to avoid pulling in strings just for assertions.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// === Task 13 §5.5: PATCH-path field length bounds ===

func TestValidateFieldLengths_BaseURLTooLong(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", 300)
	c := ValidatorCtx{
		Req:   map[string]any{"base_url": long},
		Dirty: map[string]bool{"base_url": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize base_url must reject")
	}
}

func TestValidateFieldLengths_NameTooLong(t *testing.T) {
	long := strings.Repeat("n", 100)
	c := ValidatorCtx{
		Req:   map[string]any{"name": long},
		Dirty: map[string]bool{"name": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize name must reject")
	}
}

func TestValidateFieldLengths_TagTooLong(t *testing.T) {
	long := strings.Repeat("t", 100)
	c := ValidatorCtx{
		Req:   map[string]any{"tag": long},
		Dirty: map[string]bool{"tag": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize tag must reject")
	}
}

func TestValidateFieldLengths_RemarkTooLong(t *testing.T) {
	long := strings.Repeat("r", 300)
	c := ValidatorCtx{
		Req:   map[string]any{"remark": long},
		Dirty: map[string]bool{"remark": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize remark must reject")
	}
}

func TestValidateFieldLengths_OrganizationTooLong(t *testing.T) {
	long := strings.Repeat("o", 200)
	c := ValidatorCtx{
		Req:   map[string]any{"organization": long},
		Dirty: map[string]bool{"organization": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize organization must reject")
	}
}

func TestValidateFieldLengths_ApiVersionTooLong(t *testing.T) {
	long := strings.Repeat("v", 64)
	c := ValidatorCtx{
		Req:   map[string]any{"api_version": long},
		Dirty: map[string]bool{"api_version": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize api_version must reject")
	}
}

func TestValidateFieldLengths_TestModelTooLong(t *testing.T) {
	long := strings.Repeat("m", 200)
	c := ValidatorCtx{
		Req:   map[string]any{"test_model": long},
		Dirty: map[string]bool{"test_model": true},
	}
	if err := validateFieldLengthsCtx(c); err == nil {
		t.Fatal("oversize test_model must reject")
	}
}

func TestValidateFieldLengths_WithinLimitPasses(t *testing.T) {
	c := ValidatorCtx{
		Req: map[string]any{
			"base_url":     "https://api.openai.com/v1",
			"name":         "my-channel",
			"tag":          "prod",
			"remark":       "production OpenAI channel",
			"organization": "org-1234567890",
			"api_version":  "2024-02-15",
			"test_model":   "gpt-4o",
		},
		Dirty: map[string]bool{
			"base_url": true, "name": true, "tag": true, "remark": true,
			"organization": true, "api_version": true, "test_model": true,
		},
	}
	if err := validateFieldLengthsCtx(c); err != nil {
		t.Fatalf("within-limit values must pass, got %v", err)
	}
}

func TestValidateFieldLengths_SkipCleanField(t *testing.T) {
	// base_url value is oversize but is NOT dirty — must not trigger.
	long := strings.Repeat("a", 1000)
	c := ValidatorCtx{
		Req:   map[string]any{"base_url": long},
		Dirty: map[string]bool{"weight": true},
	}
	if err := validateFieldLengthsCtx(c); err != nil {
		t.Fatalf("not-dirty field should not be checked, got %v", err)
	}
}

func TestValidateFieldLengths_EmptyStringPasses(t *testing.T) {
	// Empty string is within any positive length cap; should pass.
	c := ValidatorCtx{
		Req:   map[string]any{"remark": ""},
		Dirty: map[string]bool{"remark": true},
	}
	if err := validateFieldLengthsCtx(c); err != nil {
		t.Fatalf("empty string within limit should pass: %v", err)
	}
}

func TestValidateFieldLengths_NonStringValueIgnored(t *testing.T) {
	// patch may carry non-string values for these keys (caller-supplied JSON);
	// the validator should not panic — it just skips non-string values.
	c := ValidatorCtx{
		Req:   map[string]any{"name": 123},
		Dirty: map[string]bool{"name": true},
	}
	if err := validateFieldLengthsCtx(c); err != nil {
		t.Fatalf("non-string value should be ignored, got %v", err)
	}
}

func TestRunValidators_PatchPath_RejectsOversizeBaseURL(t *testing.T) {
	// Integration: oversize base_url through RunValidators on patch path.
	appCtx := newValidatorTestCtx(t, 1, 1)
	q := dao.NewAdminQuery(dao.NewContext(appCtx.App))
	long := "https://example.com/" + strings.Repeat("a", 300)
	c := ValidatorCtx{
		Query:   q,
		OwnerID: 1,
		GroupID: 1,
		Dirty:   map[string]bool{"base_url": true},
		Req:     map[string]any{"base_url": long},
	}
	if err := RunValidators(c); err == nil {
		t.Fatal("oversize base_url via RunValidators must reject")
	}
}
