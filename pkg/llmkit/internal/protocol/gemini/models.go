package gemini

import (
	"regexp"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
)

type generationCapabilities convert.GeminiGenerationConfig

func gemini4Config(xhighLevel string) generationCapabilities {
	return generationCapabilities{
		MinimumThinkingLevel: "LOW",
		XHighLevel:           xhighLevel,
		OmitPenalties:        true,
		OmitSingleCandidate:  true,
	}
}

// More specific patterns precede the family default, as in channel rules.
var defaultGenerationRules = []struct {
	pattern *regexp.Regexp
	config  generationCapabilities
}{
	{regexp.MustCompile(`^gemini-4(\.[0-9]+)*-(argon|carbon)(-|$)`), gemini4Config("HIGH")},
	{regexp.MustCompile(`^gemini-4(\.[0-9]+)*(-|$)`), gemini4Config("")},
}

func generationCapabilitiesFor(model string, configured *convert.GeminiGenerationConfig) generationCapabilities {
	if configured != nil {
		return generationCapabilities(*configured)
	}
	id := model[strings.LastIndexByte(model, '/')+1:]
	for _, rule := range defaultGenerationRules {
		if rule.pattern.MatchString(id) {
			return rule.config
		}
	}
	return generationCapabilities{}
}
