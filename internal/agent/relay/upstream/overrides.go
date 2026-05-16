package upstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

// ApplyOverrides applies param and header overrides to an outbound HTTP request.
// Param overrides are shallow-merged into the JSON request body (top-level keys only).
// Header overrides are set on the request headers.
// If body is not valid JSON and paramOverride is non-empty, param override is skipped
// and an error is returned; header overrides are always applied.
func ApplyOverrides(req *http.Request, body []byte, paramOverride, headerOverride map[string]any) ([]byte, error) {
	var retErr error

	// Apply param override to body
	if len(paramOverride) > 0 && len(body) > 0 {
		var bodyMap map[string]any
		if err := json.Unmarshal(body, &bodyMap); err != nil {
			retErr = fmt.Errorf("unmarshal body for param override: %w", err)
		} else {
			for k, v := range paramOverride {
				if v == nil {
					delete(bodyMap, k)
				} else {
					bodyMap[k] = v
				}
			}
			if newBody, err := json.Marshal(bodyMap); err != nil {
				retErr = fmt.Errorf("marshal body after param override: %w", err)
			} else {
				body = newBody
				req.Body = io.NopCloser(bytes.NewReader(body))
				req.ContentLength = int64(len(body))
			}
		}
	}

	// Apply header override
	for k, v := range headerOverride {
		if v == nil {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}

	return body, retErr
}

func ApplyHeaderFilter(header http.Header) {
	for key := range header {
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "forwarded",
			"x-forwarded-for",
			"x-forwarded-host",
			"x-forwarded-port",
			"x-forwarded-proto",
			"x-forwarded-server",
			"x-real-ip",
			"cdn-loop":
			header.Del(key)
		default:
			if strings.HasPrefix(lowerKey, "cf-") {
				header.Del(key)
			}
		}
	}
}

// parseProtocolOverride converts the raw map[string]any decoded from
// Channel.OtherSettings["protocol_override"] into a map[Protocol]Protocol.
// Invalid keys, invalid values, "auto", and empty strings are dropped.
func parseProtocolOverride(raw map[string]any) map[codec.Protocol]codec.Protocol {
	if len(raw) == 0 {
		return nil
	}
	result := make(map[codec.Protocol]codec.Protocol, len(raw))
	for k, v := range raw {
		valStr, ok := v.(string)
		if !ok {
			continue
		}
		if valStr == "" || valStr == "auto" {
			continue
		}
		inbound := codec.Protocol(k)
		outbound := codec.Protocol(valStr)
		if !isValidOverrideProtocolKey(inbound) || !isValidOverrideProtocolValue(outbound) {
			continue
		}
		result[inbound] = outbound
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// isValidOverrideProtocolKey accepts the codec-supported inbound protocols.
func isValidOverrideProtocolKey(p codec.Protocol) bool {
	switch p {
	case codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, codec.ProtocolClaude:
		return true
	}
	return false
}

// isValidOverrideProtocolValue accepts the codec-supported outbound protocols.
// Same set as keys for now; kept as a separate function so future expansion
// (e.g., outbound-only protocols) does not affect inbound validation.
func isValidOverrideProtocolValue(p codec.Protocol) bool {
	return isValidOverrideProtocolKey(p)
}

// modelOverrideRule represents a single per-model protocol override rule
// after parse and regex compile.
type modelOverrideRule struct {
	Pattern    *regexp.Regexp
	PatternRaw string
	IsExact    bool
	Overrides  map[codec.Protocol]codec.Protocol // includes ProtocolWildcard sentinel for "*"
}

// ProtocolWildcard is a sentinel used as a key in modelOverrideRule.Overrides
// to mean "all inbound protocols not explicitly listed". It is NEVER passed
// to codec.NegotiateOutboundProtocol — ResolveOverride expands it before the
// codec call.
const ProtocolWildcard codec.Protocol = "*"

// parseModelProtocolOverride converts the raw decoded JSON list into compiled
// rules. Invalid regex / invalid protocol values cause the offending entry
// to be dropped with a warn log; the rest of the list survives.
//
// channelID is used purely for log tagging.
func parseModelProtocolOverride(raw []any, channelID uint) []modelOverrideRule {
	if len(raw) == 0 {
		return nil
	}
	rules := make([]modelOverrideRule, 0, len(raw))
	for i, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			zap.L().Warn("model_protocol_override: entry is not an object",
				zap.Uint("channel_id", channelID), zap.Int("index", i))
			continue
		}
		modelStr, _ := entry["model"].(string)
		if modelStr == "" {
			zap.L().Warn("model_protocol_override: empty model pattern",
				zap.Uint("channel_id", channelID), zap.Int("index", i))
			continue
		}
		re, err := regexp.Compile("^" + modelStr + "$")
		if err != nil {
			zap.L().Warn("model_protocol_override: invalid regex",
				zap.Uint("channel_id", channelID),
				zap.Int("index", i),
				zap.String("pattern", modelStr),
				zap.Error(err))
			continue
		}
		ovRaw, _ := entry["overrides"].(map[string]any)
		ov := parseModelOverridesMap(ovRaw)
		if len(ov) == 0 {
			continue
		}
		rules = append(rules, modelOverrideRule{
			Pattern:    re,
			PatternRaw: modelStr,
			IsExact:    !regexHasMetaChar(modelStr),
			Overrides:  ov,
		})
	}
	if len(rules) == 0 {
		return nil
	}
	return rules
}

// parseModelOverridesMap mirrors parseProtocolOverride but additionally
// accepts "*" as an inbound key (represented internally as ProtocolWildcard).
func parseModelOverridesMap(raw map[string]any) map[codec.Protocol]codec.Protocol {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[codec.Protocol]codec.Protocol, len(raw))
	for k, v := range raw {
		valStr, ok := v.(string)
		if !ok || valStr == "" || valStr == "auto" {
			continue
		}
		var inbound codec.Protocol
		if k == "*" {
			inbound = ProtocolWildcard
		} else {
			inbound = codec.Protocol(k)
			if !isValidOverrideProtocolKey(inbound) {
				continue
			}
		}
		outbound := codec.Protocol(valStr)
		if !isValidOverrideProtocolValue(outbound) {
			continue
		}
		out[inbound] = outbound
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// regexHasMetaChar returns true if the string contains any Go regex metachar.
// Used to detect "exact-string" patterns for specificity ranking.
func regexHasMetaChar(s string) bool {
	for _, r := range s {
		switch r {
		case '[', ']', '(', ')', '.', '*', '+', '?', '{', '}', '|', '^', '$', '\\':
			return true
		}
	}
	return false
}

// ChannelOverrideRules combines channel-level and model-level overrides
// parsed from a single Channel.OtherSettings blob.
type ChannelOverrideRules struct {
	ChannelLevel map[codec.Protocol]codec.Protocol
	ModelLevel   []modelOverrideRule
}

// ChannelOverrideRulesFor parses Channel.OtherSettings into structured rules.
// Returns nil when the channel has no overrides at all (both fields empty)
// or when the JSON cannot be parsed.
func ChannelOverrideRulesFor(ch *models.Channel) *ChannelOverrideRules {
	if ch == nil || ch.OtherSettings == "" {
		return nil
	}
	var other map[string]any
	if err := json.Unmarshal([]byte(ch.OtherSettings), &other); err != nil {
		return nil
	}
	rules := &ChannelOverrideRules{}
	if rawCh, ok := other["protocol_override"].(map[string]any); ok {
		rules.ChannelLevel = parseProtocolOverride(rawCh)
	}
	if rawML, ok := other["model_protocol_override"].([]any); ok {
		rules.ModelLevel = parseModelProtocolOverride(rawML, ch.ID)
	}
	if rules.ChannelLevel == nil && rules.ModelLevel == nil {
		return nil
	}
	return rules
}

// ResolveOverride picks the highest-specificity per-model rule that matches
// modelName, expands its wildcard inbound (if any) into the set of valid
// inbound protocols, and returns a flat map[Protocol]Protocol.
//
// Falls back to rules.ChannelLevel when no per-model rule matches.
func ResolveOverride(rules *ChannelOverrideRules, modelName string) map[codec.Protocol]codec.Protocol {
	if rules == nil {
		return nil
	}
	if len(rules.ModelLevel) == 0 {
		return rules.ChannelLevel
	}

	type scored struct {
		rule       modelOverrideRule
		configIdx  int
		patternLen int
		score      int // higher = more specific (exact-model > regex)
	}
	var matches []scored
	for i, r := range rules.ModelLevel {
		if !r.Pattern.MatchString(modelName) {
			continue
		}
		base := 0
		if r.IsExact {
			base = 2
		}
		matches = append(matches, scored{rule: r, configIdx: i, patternLen: len(r.PatternRaw), score: base})
	}
	if len(matches) == 0 {
		return rules.ChannelLevel
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].patternLen != matches[j].patternLen {
			return matches[i].patternLen > matches[j].patternLen
		}
		return matches[i].configIdx < matches[j].configIdx
	})

	winning := matches[0].rule
	return expandRuleOverrides(winning)
}

// validInboundProtocols enumerates all codec-supported inbound protocols.
// Used to expand a per-rule '*' wildcard inbound key. Inbound protocol is
// determined by the client's request URL on the gateway, NOT by the channel's
// upstream endpoints — so wildcard expansion uses the fixed protocol set,
// matching the channel-level protocol_override key set.
var validInboundProtocols = []codec.Protocol{
	codec.ProtocolOpenAIChat,
	codec.ProtocolOpenAIResponses,
	codec.ProtocolClaude,
}

// expandRuleOverrides materializes rule.Overrides into a flat
// map[Protocol]Protocol. ProtocolWildcard inbound expands into entries for
// each valid inbound protocol that isn't already explicitly listed.
// Reachability of the resulting outbound is verified later inside
// codec.NegotiateOutboundProtocol.
func expandRuleOverrides(rule modelOverrideRule) map[codec.Protocol]codec.Protocol {
	out := make(map[codec.Protocol]codec.Protocol, len(rule.Overrides))
	wildcardTarget, hasWildcard := rule.Overrides[ProtocolWildcard]
	for k, v := range rule.Overrides {
		if k == ProtocolWildcard {
			continue
		}
		out[k] = v
	}
	if !hasWildcard {
		return out
	}
	for _, p := range validInboundProtocols {
		if _, set := out[p]; set {
			continue // explicit beats wildcard
		}
		out[p] = wildcardTarget
	}
	return out
}
