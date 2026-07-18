package upstream

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// FetchConfig 是抓取一张远程图片的运行期参数(调用方从 cache.Settings() 组装)。
type FetchConfig struct {
	TimeoutSec    int
	MaxBytes      int
	SSRFGuard     bool
	HostAllowlist []string // 空 = 不限(仍受 SSRFGuard 约束)
}

// FetchInlineImage 抓取远程图片 URL,返回 (base64, MIME, err)。err != nil 时调用方降级
// (保留原 URL)。带 scheme/allowlist/SSRF 闸门 + 超时 + 大小上限。
func FetchInlineImage(ctx context.Context, rawURL string, cfg FetchConfig) (b64, mime string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("scheme %q not allowed", u.Scheme)
	}
	if len(cfg.HostAllowlist) > 0 && !hostAllowed(u.Hostname(), cfg.HostAllowlist) {
		return "", "", fmt.Errorf("host %q not in allowlist", u.Hostname())
	}

	client := &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second}
	if cfg.SSRFGuard {
		// Control 在实际拨号(DNS 解析之后)对目标 IP 复检 → 防 DNS rebinding。
		dialer := &net.Dialer{
			Control: func(_, address string, _ syscall.RawConn) error {
				return checkDialAddr(address)
			},
		}
		client.Transport = &http.Transport{DialContext: dialer.DialContext}
	}
	// CheckRedirect 恒设置(不受 SSRFGuard 开关约束):allowlist 本身语义就是"恒生效"。
	// IP 级 SSRF 复检由 Transport/Control 在重定向后的拨号上复用,这里只需补 scheme + allowlist。
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect scheme %q not allowed", req.URL.Scheme)
		}
		if len(cfg.HostAllowlist) > 0 && !hostAllowed(req.URL.Hostname(), cfg.HostAllowlist) {
			return fmt.Errorf("redirect host %q not in allowlist", req.URL.Hostname())
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(cfg.MaxBytes)+1))
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}
	if len(data) > cfg.MaxBytes {
		return "", "", fmt.Errorf("image exceeds max_bytes %d", cfg.MaxBytes)
	}

	mime = imageMIME(resp.Header.Get("Content-Type"), data)
	if mime == "" {
		return "", "", fmt.Errorf("not an image")
	}
	return base64.StdEncoding.EncodeToString(data), mime, nil
}

// checkDialAddr 判定即将拨号的地址是否命中 SSRF 闸门。fail-closed:无法解析即拦。
func checkDialAddr(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("blocked: unparseable dial address %q (ssrf guard)", address)
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // 剥 IPv6 zone id: fe80::1%eth0
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil || isBlockedIP(ip) {
		return fmt.Errorf("blocked host %q (ssrf guard)", host)
	}
	return nil
}

// isBlockedIP 命中私网/环回/link-local(含云元数据 169.254.169.254)/未指定/多播。
func isBlockedIP(ip net.IP) bool {
	// 折叠 IPv4-compatible IPv6(::a.b.c.d,已废弃)到内嵌 v4,让下面的谓词能识别。
	// 排除 :: 与 ::1(交给 IsUnspecified/IsLoopback)。
	if v16 := ip.To16(); v16 != nil && ip.To4() == nil {
		allZero := true
		for _, b := range v16[:12] {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero && !(v16[12] == 0 && v16[13] == 0 && v16[14] == 0 && (v16[15] == 0 || v16[15] == 1)) {
			ip = net.IPv4(v16[12], v16[13], v16[14], v16[15])
		}
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func hostAllowed(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, a := range allow {
		if strings.ToLower(strings.TrimSpace(a)) == host {
			return true
		}
	}
	return false
}

// imageMIME 优先响应 Content-Type 的 image/* 主类型,兜底 magic-bytes 嗅探;非 image 返回 ""。
func imageMIME(contentType string, data []byte) string {
	if ct := strings.TrimSpace(strings.Split(contentType, ";")[0]); strings.HasPrefix(ct, "image/") {
		return ct
	}
	if sniff := http.DetectContentType(data); strings.HasPrefix(sniff, "image/") {
		return sniff
	}
	return ""
}
