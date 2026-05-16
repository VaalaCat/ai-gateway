package oauth

import "testing"

func TestNewAllowlist_Valid(t *testing.T) {
	a, err := NewAllowlist([]string{
		"https://foo.example.com",
		"http://192.168.1.10:8140",
		"HTTP://Bar.example.com:80/", // 规范化后 http://bar.example.com，应被接受
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if a.empty {
		t.Fatal("expected empty=false")
	}
	if _, ok := a.Match("https://foo.example.com"); !ok {
		t.Fatal("expected match")
	}
	if _, ok := a.Match("http://bar.example.com"); !ok {
		t.Fatal("expected match (normalized)")
	}
}

func TestNewAllowlist_RejectsInvalid(t *testing.T) {
	cases := [][]string{
		{"foo.example.com"},                                   // 缺 scheme
		{"ftp://foo.example.com"},                             // 非 http/https
		{"http://"},                                           // 缺 host
		{"https://foo.example.com", "https://FOO.example.com"}, // 规范化后撞车
	}
	for _, in := range cases {
		if _, err := NewAllowlist(in); err == nil {
			t.Fatalf("expected error for %v", in)
		}
	}
}

func TestNewAllowlist_Empty(t *testing.T) {
	a, err := NewAllowlist(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.empty {
		t.Fatal("expected empty=true for nil input")
	}
	a2, err := NewAllowlist([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !a2.empty {
		t.Fatal("expected empty=true for [] input")
	}
}

func TestAllowlist_Match(t *testing.T) {
	a, _ := NewAllowlist([]string{"https://foo.example.com"})
	if m, ok := a.Match("https://foo.example.com"); !ok || m != "https://foo.example.com" {
		t.Fatalf("hit: m=%q ok=%v", m, ok)
	}
	if m, ok := a.Match("https://evil.com"); ok || m != "" {
		t.Fatalf("miss: m=%q ok=%v", m, ok)
	}
}

func TestAllowlist_EmptyMatch(t *testing.T) {
	a, _ := NewAllowlist(nil)
	if m, ok := a.Match("https://anything.example.com"); !ok || m != "https://anything.example.com" {
		t.Fatalf("dev mode: m=%q ok=%v", m, ok)
	}
}
