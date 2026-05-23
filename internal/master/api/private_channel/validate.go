package private_channel

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// ValidatorCtx carries everything a BYOK validator needs. Create-path callers
// pass Dirty=nil (all fields are considered dirty); Update-path callers pass a
// map keyed by JSON field name with true for every field present in the patch.
// Each validator that cares about a specific field MUST self-check via IsDirty
// before doing work, so the same registry runs for both Create and Update.
type ValidatorCtx struct {
	Query   dao.AdminQuery
	Req     map[string]any
	Dirty   map[string]bool
	OwnerID uint
	GroupID uint
}

// IsDirty reports whether `field` is present in the patch. Create-path callers
// pass Dirty=nil and every field is treated as dirty.
func (c ValidatorCtx) IsDirty(field string) bool {
	if c.Dirty == nil {
		return true
	}
	return c.Dirty[field]
}

// IsCreate reports whether this is a Create-path invocation (Dirty=nil).
// Used by validators that only apply on Create (e.g. per-user channel limit).
func (c ValidatorCtx) IsCreate() bool { return c.Dirty == nil }

type validatorFn func(c ValidatorCtx) error

// byokValidators is the ordered registry shared by Create and Update.
// Each entry self-checks IsDirty / IsCreate before doing real work.
var byokValidators = []validatorFn{
	validateBYOKEnabledForUserCtx,
	validateUserUnderChannelLimitCtx,
	validateFieldLengthsCtx,
	validateNameUniquenessCtx,
	validateBaseURLAllowlistCtx,
	validateModelsNonEmptyCtx,
	validateModelsSubsetOfModelConfigsCtx,
	validateModelMappingTargetsCtx,
}

// fieldLengthLimits enumerates the per-field max byte-length caps enforced by
// validateFieldLengthsCtx. Limits mirror the gorm `size:N` tags on
// ChannelCore (see internal/models/channel_core.go) so a PATCH cannot smuggle
// in an oversize value that the DB driver would later reject with a 500.
// Create-path requests are also covered by `binding:"max=N"` tags on
// CreateRequest, which fail earlier with a structured 400 from gin.
var fieldLengthLimits = map[string]int{
	"name":         64,
	"base_url":     256,
	"tag":          64,
	"remark":       255,
	"organization": 128,
	"api_version":  32,
	"test_model":   128,
}

// RunValidators executes the BYOK validator registry against c, returning the
// first error. Pass Dirty=nil for Create; pass a map keyed by patch field name
// for Update.
func RunValidators(c ValidatorCtx) error {
	for _, v := range byokValidators {
		if err := v(c); err != nil {
			return err
		}
	}
	return nil
}

// --- ValidatorCtx-based validator implementations ---

func validateBYOKEnabledForUserCtx(c ValidatorCtx) error {
	if c.OwnerID == 0 {
		return api.UnauthorizedError("not authenticated")
	}
	if !c.Query.Setting().LookupBool(consts.SettingKeyBYOKEnabled, consts.BYOKDefaultEnabledBool) {
		return api.ForbiddenError("byok globally disabled")
	}
	g, err := c.Query.UserGroup().GetByID(c.GroupID)
	if err == nil && g != nil && g.BYOKEnabled != nil && !*g.BYOKEnabled {
		return api.ForbiddenError("byok disabled for your user group")
	}
	return nil
}

func validateUserUnderChannelLimitCtx(c ValidatorCtx) error {
	// Patch path never adds rows, so the limit is irrelevant there.
	if !c.IsCreate() {
		return nil
	}
	if c.OwnerID == 0 {
		return api.UnauthorizedError("not authenticated")
	}
	// §5.4 BYOKMaxChannels semantics:
	//   - group override nil → fall back to global setting byok_max_channels_per_user
	//   - effective limit <= 0 → quota disabled by admin (ForbiddenError)
	//   - count >= limit       → quota reached (ConflictError)
	// Negative values are rejected at write time (user_group create/update); reaching
	// the validator with limit<0 means stale data — treat as disabled rather than
	// the previous buggy `count >= -1` which is always true and locks all users out.
	limit := c.Query.Setting().LookupInt(consts.SettingKeyBYOKMaxChannelsPerUser, consts.BYOKDefaultMaxChannelsPerUserInt)
	if g, _ := c.Query.UserGroup().GetByID(c.GroupID); g != nil && g.BYOKMaxChannels != nil {
		limit = *g.BYOKMaxChannels
	}
	if limit <= 0 {
		return api.ForbiddenError("byok channel quota disabled by admin")
	}
	count, err := c.Query.PrivateChannel().CountByOwner(c.OwnerID)
	if err != nil {
		return api.InternalError("count private channels", err)
	}
	if int(count) >= limit {
		return api.ConflictError(fmt.Sprintf("byok channel quota %d reached", limit), nil)
	}
	return nil
}

func validateNameUniquenessCtx(c ValidatorCtx) error {
	if !c.IsDirty("name") {
		return nil
	}
	name, _ := c.Req["name"].(string)
	if name == "" {
		return nil
	}
	if c.OwnerID == 0 {
		return api.UnauthorizedError("not authenticated")
	}
	existing, _, err := c.Query.PrivateChannel().ListOwnedBy(
		c.OwnerID,
		dao.ListOptions{Page: 1, PageSize: 1000},
		dao.PrivateChannelFilter{Search: name},
	)
	if err != nil {
		return api.InternalError("list private channels", err)
	}
	for _, e := range existing {
		if e.Name == name {
			return api.ConflictError("name already used: "+name, nil)
		}
	}
	return nil
}

func validateBaseURLAllowlistCtx(c ValidatorCtx) error {
	if !c.IsDirty("base_url") {
		return nil
	}
	baseURL, _ := c.Req["base_url"].(string)

	// §1.6: explicit PATCH base_url="" is meaningless — refuse so callers can't
	// blank out the URL on update (Create path keeps Dirty=nil and falls through).
	if c.Dirty != nil && c.Dirty["base_url"] && baseURL == "" {
		return api.BadRequestError("base_url cannot be empty on update", nil)
	}
	if baseURL == "" {
		return nil // Create path: empty → use type built-in URL
	}

	// §1.1 SSRF fix: parse and compare scheme/host/path explicitly instead of
	// strings.HasPrefix, which lets "https://api.openai.com.attacker.com/v1"
	// match "https://api.openai.com" and leak keys upstream.
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return api.BadRequestError("base_url malformed", nil)
	}

	extra := readBaseURLAllowlistExtraFromQuery(c.Query)
	all := make([]string, 0, len(consts.SystemBYOKBaseURLs)+len(extra))
	all = append(all, consts.SystemBYOKBaseURLs...)
	all = append(all, extra...)
	for _, prefix := range all {
		p, err := url.Parse(prefix)
		if err != nil || p.Scheme == "" || p.Host == "" {
			continue
		}
		if u.Scheme != p.Scheme {
			continue
		}
		if u.Host != p.Host {
			continue
		}
		if !hasPathSegmentPrefix(u.Path, p.Path) {
			continue
		}
		return nil
	}
	return api.ForbiddenError("base_url not in allowlist")
}

// hasPathSegmentPrefix reports whether child has parent as a path-segment prefix.
// "/v1" matches "/v1", "/v1/", "/v1/foo" but NOT "/v1beta". Parent "" or "/" is
// treated as "any path".
func hasPathSegmentPrefix(child, parent string) bool {
	parent = strings.TrimRight(parent, "/")
	if parent == "" {
		return true
	}
	if !strings.HasPrefix(child, parent) {
		return false
	}
	rest := child[len(parent):]
	return rest == "" || rest[0] == '/'
}

func validateModelsSubsetOfModelConfigsCtx(c ValidatorCtx) error {
	if !c.IsDirty("models") {
		return nil
	}
	ms := stringSliceFromAny(c.Req["models"])
	if len(ms) == 0 {
		return nil
	}
	cfgs, err := c.Query.ModelConfig().ListAll()
	if err != nil {
		return api.InternalError("load model configs", err)
	}
	set := modelNameSet(cfgs)
	for _, m := range ms {
		if _, ok := set[m]; !ok {
			return api.BadRequestError("model not registered: "+m, nil)
		}
	}
	return nil
}

func validateModelMappingTargetsCtx(c ValidatorCtx) error {
	// model_mapping legality depends on the model registry; we re-check whenever
	// either model_mapping or models is dirty.
	if !c.IsDirty("model_mapping") && !c.IsDirty("models") {
		return nil
	}
	mapping := mappingFromAny(c.Req["model_mapping"])
	if len(mapping) == 0 {
		return nil
	}
	cfgs, _ := c.Query.ModelConfig().ListAll()
	set := modelNameSet(cfgs)
	for k, v := range mapping {
		if _, ok := set[k]; !ok {
			return api.BadRequestError("model mapping key not registered: "+k, nil)
		}
		if _, ok := set[v]; !ok {
			return api.BadRequestError("model mapping value not registered: "+v, nil)
		}
	}
	return nil
}

// validateFieldLengthsCtx enforces per-field byte-length caps on PATCH bodies
// (and re-checks Create-path inputs, even though binding tags on CreateRequest
// already cover Create). Without this guard, oversize string values would
// propagate to the DB driver and surface as opaque 500s; here we reject with a
// structured 400 instead. Non-string values are ignored — the per-field
// validators that care about type already coerce/validate them.
func validateFieldLengthsCtx(c ValidatorCtx) error {
	for field, max := range fieldLengthLimits {
		if !c.IsDirty(field) {
			continue
		}
		v, ok := c.Req[field].(string)
		if !ok {
			continue
		}
		if len(v) > max {
			return api.BadRequestError(
				fmt.Sprintf("%s exceeds max length %d", field, max), nil)
		}
	}
	return nil
}

// --- helpers ---

func modelNameSet(cfgs []models.ModelConfig) map[string]struct{} {
	set := make(map[string]struct{}, len(cfgs))
	for _, mc := range cfgs {
		set[mc.ModelName] = struct{}{}
	}
	return set
}

func readBaseURLAllowlistExtraFromQuery(q dao.AdminQuery) []string {
	raw := q.Setting().LookupString(consts.SettingKeyBYOKBaseURLAllowlist, consts.BYOKDefaultBaseURLAllowlistStr)
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// stringSliceFromAny accepts both []string (Create path) and []any (Update path,
// where map[string]any decoding produces []any).
func stringSliceFromAny(v any) []string {
	switch x := v.(type) {
	case []string:
		out := make([]string, 0, len(x))
		out = append(out, x...)
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// mappingFromAny accepts both map[string]string (Create) and map[string]any (Update).
func mappingFromAny(v any) map[string]string {
	switch x := v.(type) {
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			out[k] = val
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(x))
		for k, val := range x {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}

// createRequestToMap projects a CreateRequest into the field map that the
// shared validator registry consumes.
func createRequestToMap(req *CreateRequest) map[string]any {
	return map[string]any{
		"name":          req.Name,
		"base_url":      req.BaseURL,
		"models":        req.Models,
		"model_mapping": req.ModelMapping,
		"weight":        req.Weight,
		"priority":      req.Priority,
	}
}

// dirtyFromFields returns a Dirty map for the Update path. Every key in fields
// is marked dirty; reserved keys filtered by callers already.
func dirtyFromFields(fields map[string]any) map[string]bool {
	out := make(map[string]bool, len(fields))
	for k := range fields {
		out[k] = true
	}
	return out
}

// validateModelsNonEmptyCtx rejects requests that try to set models to an
// empty slice. Create path is already covered by CreateRequest's
// binding:"required,min=1" tag, so the function short-circuits via
// IsCreate() and only does real work on the Update path.
func validateModelsNonEmptyCtx(c ValidatorCtx) error {
	if c.IsCreate() {
		return nil
	}
	if !c.IsDirty("models") {
		return nil
	}
	if len(stringSliceFromAny(c.Req["models"])) == 0 {
		return api.BadRequestError("models must not be empty", nil)
	}
	return nil
}
