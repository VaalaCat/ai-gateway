package gemini

import (
	"regexp"
	"strings"
)

// The zero value uses the generic Gemini generation settings.
type generationCapabilities struct {
	minimumThinkingLevel string
	xhighLevel           string
	omitPenalties        bool
	omitSingleCandidate  bool
}

func gemini4Config(xhighLevel string) generationCapabilities {
	return generationCapabilities{
		minimumThinkingLevel: "LOW",
		xhighLevel:           xhighLevel,
		omitPenalties:        true,
		omitSingleCandidate:  true,
	}
}

// First match wins. Put model-specific rules before their generation default.
var modelGenerationRules = []struct {
	pattern *regexp.Regexp
	config  generationCapabilities
}{
	{regexp.MustCompile(`^gemini-4(\.[0-9]+)*-(argon|carbon)(-|$)`), gemini4Config("HIGH")},
	{regexp.MustCompile(`^gemini-4(\.[0-9]+)*(-|$)`), gemini4Config("")},
}

func generationCapabilitiesFor(model string) generationCapabilities {
	id := model[strings.LastIndexByte(model, '/')+1:]
	for _, rule := range modelGenerationRules {
		if rule.pattern.MatchString(id) {
			return rule.config
		}
	}
	return generationCapabilities{}
}
