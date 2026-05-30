package pricing

import (
	"sort"
	"strings"
)

// MatchAll 在 source 中匹配 modelName，返回命中名下的全部 provider 候选。
// 先精确、再模糊（normalize 后取最短、字母序最早的源端名）。
func MatchAll(modelName string, source SourceData) (matchType, matchedName string, prices []SourceModelPrice, ok bool) {
	if p, found := source[modelName]; found {
		return "exact", modelName, p, true
	}
	normalizedTarget := normalize(modelName)
	var names []string
	for name := range source {
		if normalize(name) == normalizedTarget {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", "", nil, false
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) < len(names[j])
		}
		return names[i] < names[j]
	})
	best := names[0]
	return "fuzzy", best, source[best], true
}

func normalize(name string) string {
	if idx := strings.Index(name, "--"); idx >= 0 {
		name = name[idx+2:]
	} else if idx := strings.Index(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.ReplaceAll(name, "--", "-")
	return strings.ToLower(name)
}
