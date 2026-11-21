package gemini

import "strings"

// The zero value preserves the caller's generation parameters for unlisted models.
type generationCapabilities struct {
	minimumThinkingLevel string // Level-only thinking; used for disabled or minimal effort.
	xhighLevel           string // Optional translation of XHIGH.
	omitPenalties        bool
	omitSingleCandidate  bool
}

var modelGenerationConfigs = func() map[string]generationCapabilities {
	gemini4 := generationCapabilities{
		minimumThinkingLevel: "LOW",
		omitPenalties:        true,
		omitSingleCandidate:  true,
	}
	cappedAtHigh := gemini4
	cappedAtHigh.xhighLevel = "HIGH"
	return map[string]generationCapabilities{
		"gemini-4":        gemini4,
		"gemini-4-argon":  cappedAtHigh,
		"gemini-4-carbon": cappedAtHigh,
	}
}()

func generationCapabilitiesFor(model string) generationCapabilities {
	id := model[strings.LastIndexByte(model, '/')+1:]
	if config, ok := lookupGenerationConfig(id); ok {
		return config
	}
	// A minor release can have its own entries. Otherwise it inherits the
	// same variant (and ultimately the defaults) from the major release.
	name, isGemini := strings.CutPrefix(id, "gemini-")
	version, _, _ := strings.Cut(name, "-")
	if major, _, hasMinor := strings.Cut(version, "."); isGemini && hasMinor {
		config, _ := lookupGenerationConfig("gemini-" + major + name[len(version):])
		return config
	}
	return generationCapabilities{}
}

// Try the full ID first, then remove one dash-delimited suffix at a time.
func lookupGenerationConfig(id string) (generationCapabilities, bool) {
	for {
		if config, ok := modelGenerationConfigs[id]; ok {
			return config, true
		}
		end := strings.LastIndexByte(id, '-')
		if end < 0 {
			return generationCapabilities{}, false
		}
		id = id[:end]
	}
}
