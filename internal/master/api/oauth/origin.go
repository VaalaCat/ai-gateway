package oauth

import (
	"net/http"
	"strings"
)

// resolveRequestOrigin 把请求解析为规范化 origin。
// 优先级：X-Forwarded-Host + X-Forwarded-Proto > r.Host + (r.TLS != nil ? https : http)。
// 多代理链时取首段；规范化与 parseOrigin 完全一致。
// 返回值始终非空（至少 r.Host 存在），异常输入用最佳努力构造。
func resolveRequestOrigin(r *http.Request) string {
	host := firstSegment(r.Header.Get("X-Forwarded-Host"))
	scheme := strings.ToLower(strings.TrimSpace(firstSegment(r.Header.Get("X-Forwarded-Proto"))))
	if host == "" {
		host = r.Host
	}
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	normalized, err := parseOrigin(scheme + "://" + host)
	if err != nil {
		return scheme + "://" + strings.ToLower(host)
	}
	return normalized
}

func firstSegment(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, ','); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}
