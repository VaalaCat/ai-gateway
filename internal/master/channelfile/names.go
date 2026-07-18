package channelfile

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

type NameAllocator struct {
	used map[string]struct{}
}

func NewNameAllocator(existing []string) *NameAllocator {
	used := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		used[name] = struct{}{}
	}
	return &NameAllocator{used: used}
}

func (a *NameAllocator) Allocate(source string) (string, error) {
	if source == "" {
		return "", errorsNameRequired()
	}
	if len(source) > MaxNameBytes {
		return "", fmt.Errorf("name exceeds %d bytes", MaxNameBytes)
	}
	if _, exists := a.used[source]; !exists {
		a.used[source] = struct{}{}
		return source, nil
	}

	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		base := truncateUTF8(source, MaxNameBytes-len(suffix))
		candidate := base + suffix
		if _, exists := a.used[candidate]; exists {
			continue
		}
		a.used[candidate] = struct{}{}
		return candidate, nil
	}
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func errorsNameRequired() error {
	return fmt.Errorf("name is required")
}
