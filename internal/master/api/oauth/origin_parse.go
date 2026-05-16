package oauth

import (
	"fmt"
	"net/url"
	"strings"
)

// parseOrigin 把任意 origin 字符串规范化为 "<scheme>://<host>[:<port>]"，
// 同时验证：scheme ∈ {http, https}、有 host、不带 path/query/fragment。
// 默认端口（http:80 / https:443）会被剥离，其他端口保留。
func parseOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty origin")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("host is empty")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin must not contain path, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if host == "" {
		return "", fmt.Errorf("host is empty")
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, nil
	}
	return scheme + "://" + host, nil
}
