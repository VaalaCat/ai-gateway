package agentproxy

import "testing"

func TestResolveAddress_ByTag(t *testing.T) {
	addrs := []Address{
		{URL: "http://10.0.1.1:8139", Tag: "internal"},
		{URL: "http://1.2.3.4:8139", Tag: "public"},
	}
	url, err := ResolveAddress(addrs, "public", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://1.2.3.4:8139" {
		t.Errorf("expected public addr, got %s", url)
	}
}

func TestResolveAddress_ByPreferredTag(t *testing.T) {
	addrs := []Address{
		{URL: "http://10.0.1.1:8139", Tag: "internal"},
		{URL: "http://1.2.3.4:8139", Tag: "public"},
	}
	url, err := ResolveAddress(addrs, "", "internal", "")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://10.0.1.1:8139" {
		t.Errorf("expected internal addr, got %s", url)
	}
}

func TestResolveAddress_Empty(t *testing.T) {
	_, err := ResolveAddress(nil, "", "", "")
	if err == nil {
		t.Error("expected error for empty addresses")
	}
}

func TestParseAddresses(t *testing.T) {
	raw := `[{"url":"http://10.0.1.1:8139","tag":"internal"}]`
	addrs := ParseAddresses(raw)
	if len(addrs) != 1 || addrs[0].Tag != "internal" {
		t.Errorf("unexpected parse result: %+v", addrs)
	}
}

func TestResolveProxyURL(t *testing.T) {
	if p := ResolveProxyURL("http://agent-proxy", "http://global"); p != "http://agent-proxy" {
		t.Errorf("expected agent proxy, got %s", p)
	}
	if p := ResolveProxyURL("", "http://global"); p != "http://global" {
		t.Errorf("expected global proxy, got %s", p)
	}
}
