package oauth

import "fmt"

// Allowlist 持有规范化后的对外 origin 集合。
// empty=true 时进入开发模式：所有 origin 都视作合法，原样回传。
type Allowlist struct {
	set   map[string]struct{}
	empty bool
}

// NewAllowlist 解析输入列表，规范化 + 查重。空切片 / nil 合法。
func NewAllowlist(rawURLs []string) (*Allowlist, error) {
	if len(rawURLs) == 0 {
		return &Allowlist{set: map[string]struct{}{}, empty: true}, nil
	}
	set := make(map[string]struct{}, len(rawURLs))
	for i, raw := range rawURLs {
		o, err := parseOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("public_base_urls[%d]: %w", i, err)
		}
		if _, dup := set[o]; dup {
			return nil, fmt.Errorf("public_base_urls: duplicate origin %q", o)
		}
		set[o] = struct{}{}
	}
	return &Allowlist{set: set, empty: false}, nil
}

// Match 判定 origin 是否被允许，返回最终用于拼 redirect_uri 的字符串。
//   - 列表为空：开发模式，直接放行
//   - 列表非空：严格匹配，命中返回 (origin, true)，否则 ("", false)
func (a *Allowlist) Match(origin string) (string, bool) {
	if a.empty {
		return origin, true
	}
	if _, ok := a.set[origin]; ok {
		return origin, true
	}
	return "", false
}
