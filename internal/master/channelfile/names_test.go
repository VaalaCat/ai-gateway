package channelfile

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNameAllocator(t *testing.T) {
	t.Run("success keeps unused name", func(t *testing.T) {
		a := NewNameAllocator(nil)
		got, err := a.Allocate("openai")
		if err != nil || got != "openai" {
			t.Fatalf("got %q, err %v", got, err)
		}
	})

	t.Run("collision includes database and batch names", func(t *testing.T) {
		a := NewNameAllocator([]string{"openai"})
		first, _ := a.Allocate("openai")
		second, _ := a.Allocate("openai")
		if first != "openai-2" || second != "openai-3" {
			t.Fatalf("got %q, %q", first, second)
		}
	})

	t.Run("boundary truncates at utf8 boundary for suffix", func(t *testing.T) {
		name := strings.Repeat("界", 20) + "abcd"
		if len(name) != MaxNameBytes {
			t.Fatalf("test name bytes = %d", len(name))
		}
		a := NewNameAllocator([]string{name})
		got, err := a.Allocate(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) > MaxNameBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, "-2") {
			t.Fatalf("invalid allocated name %q (%d bytes)", got, len(got))
		}
	})

	t.Run("failure rejects empty and oversized source", func(t *testing.T) {
		a := NewNameAllocator(nil)
		if _, err := a.Allocate(""); err == nil {
			t.Fatal("empty name accepted")
		}
		if _, err := a.Allocate(strings.Repeat("a", MaxNameBytes+1)); err == nil {
			t.Fatal("oversized name accepted")
		}
	})
}
