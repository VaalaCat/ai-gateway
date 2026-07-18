package upstream

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func imgServer(t *testing.T, contentType string, body []byte, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
}

func TestFetchInlineImage_Success(t *testing.T) {
	srv := imgServer(t, "image/png", []byte("PNGDATA"), 200)
	defer srv.Close()
	b64, mime, err := FetchInlineImage(context.Background(), srv.URL,
		FetchConfig{TimeoutSec: 5, MaxBytes: 1024, SSRFGuard: false})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if mime != "image/png" || b64 == "" {
		t.Errorf("mime=%q b64empty=%v", mime, b64 == "")
	}
}

func TestFetchInlineImage_SSRFBlocksLoopback(t *testing.T) {
	srv := imgServer(t, "image/png", []byte("PNGDATA"), 200) // httptest 监听 127.0.0.1
	defer srv.Close()
	if _, _, err := FetchInlineImage(context.Background(), srv.URL,
		FetchConfig{TimeoutSec: 5, MaxBytes: 1024, SSRFGuard: true}); err == nil {
		t.Errorf("SSRF guard should block loopback")
	}
}

func TestFetchInlineImage_SchemeRejected(t *testing.T) {
	if _, _, err := FetchInlineImage(context.Background(), "file:///etc/passwd",
		FetchConfig{TimeoutSec: 5, MaxBytes: 1024}); err == nil {
		t.Errorf("file:// should be rejected")
	}
}

func TestFetchInlineImage_SizeLimit(t *testing.T) {
	srv := imgServer(t, "image/png", []byte("this-body-is-way-too-large"), 200)
	defer srv.Close()
	if _, _, err := FetchInlineImage(context.Background(), srv.URL,
		FetchConfig{TimeoutSec: 5, MaxBytes: 4, SSRFGuard: false}); err == nil {
		t.Errorf("body over max_bytes should error")
	}
}

func TestFetchInlineImage_HostAllowlistMiss(t *testing.T) {
	srv := imgServer(t, "image/png", []byte("PNGDATA"), 200)
	defer srv.Close()
	if _, _, err := FetchInlineImage(context.Background(), srv.URL,
		FetchConfig{TimeoutSec: 5, MaxBytes: 1024, HostAllowlist: []string{"only-this.example.com"}}); err == nil {
		t.Errorf("host not in allowlist should error")
	}
}

func TestFetchInlineImage_NonImageRejected(t *testing.T) {
	srv := imgServer(t, "text/html", []byte("<html>"), 200)
	defer srv.Close()
	if _, _, err := FetchInlineImage(context.Background(), srv.URL,
		FetchConfig{TimeoutSec: 5, MaxBytes: 1024}); err == nil {
		t.Errorf("non-image content-type should error")
	}
}

func TestIsBlockedIP(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "::1",
		"::127.0.0.1", "::169.254.169.254",
	} {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1"} {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should NOT be blocked", s)
		}
	}
}

func TestCheckDialAddr(t *testing.T) {
	cases := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"zoned link-local ipv6", "[fe80::1%docker0]:80", true},
		{"unspecified ipv6", "[::]:80", true},
		{"loopback ipv4", "127.0.0.1:80", true},
		{"private ipv4", "10.0.0.1:80", true},
		{"unparseable address", "garbage", true},
		{"link-local ipv6 no zone", "[fe80::1]:80", true},
		{"public ipv4 allowed", "8.8.8.8:80", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkDialAddr(c.address)
			if c.wantErr && err == nil {
				t.Errorf("checkDialAddr(%q): expected error, got nil", c.address)
			}
			if !c.wantErr && err != nil {
				t.Errorf("checkDialAddr(%q): expected nil, got %v", c.address, err)
			}
		})
	}
}

func hostOf(rawURL string) string {
	u, _ := url.Parse(rawURL)
	return u.Hostname()
}

func TestFetchInlineImage_RedirectToDisallowedHostBlocked(t *testing.T) {
	dst := imgServer(t, "image/png", []byte("PNGDATA"), 200)
	defer dst.Close()

	// httptest binds both servers to 127.0.0.1, differing only by port, and hostAllowed
	// (like the pre-existing allowlist check) compares hostname only, ignoring port. So
	// redirecting to dst.URL verbatim wouldn't exercise the allowlist mismatch this test
	// is meant to catch. Redirect via "localhost" instead — same loopback listener, but a
	// hostname string that genuinely differs from src's "127.0.0.1" allowlist entry.
	dstURL, err := url.Parse(dst.URL)
	if err != nil {
		t.Fatalf("parse dst url: %v", err)
	}
	dstViaLocalhost := *dstURL
	dstViaLocalhost.Host = "localhost:" + dstURL.Port()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dstViaLocalhost.String(), http.StatusFound)
	}))
	defer src.Close()

	_, _, err = FetchInlineImage(context.Background(), src.URL, FetchConfig{
		SSRFGuard:     false,
		MaxBytes:      1024,
		TimeoutSec:    5,
		HostAllowlist: []string{hostOf(src.URL)},
	})
	if err == nil {
		t.Errorf("redirect to disallowed host should be blocked")
	}
}
