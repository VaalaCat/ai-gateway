package oauth

import (
	"errors"
	"fmt"
	"strings"
)

type UserinfoPayload struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Picture           string `json:"picture"`
}

const (
	usernameMinLen   = 3
	usernameMaxLen   = 32
	usernameMaxTries = 50
)

// ResolveUsername 选一个唯一可用的本地 username。
// exists(name) 应返回 (是否已被占用, 查询错误)。
func ResolveUsername(info UserinfoPayload, exists func(string) (bool, error)) (string, error) {
	base := pickBase(info)
	if base == "" {
		return "", errors.New("no candidate username (sub/email/preferred_username all empty)")
	}
	base = sanitize(base)
	base = padShort(base)
	base = truncate(base)

	for i := 1; i <= usernameMaxTries; i++ {
		candidate := base
		if i > 1 {
			suffix := fmt.Sprintf("_%d", i)
			// Trim the base so that base+suffix fits in usernameMaxLen.
			// Otherwise truncate(base+suffix) collapses back to `base`, and
			// all conflict candidates would be the same taken name.
			maxBase := usernameMaxLen - len(suffix)
			trimmed := base
			if len(trimmed) > maxBase {
				trimmed = trimmed[:maxBase]
			}
			candidate = trimmed + suffix
		}
		taken, err := exists(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", errors.New("username candidates exhausted")
}

func pickBase(info UserinfoPayload) string {
	if info.PreferredUsername != "" {
		return info.PreferredUsername
	}
	if info.Email != "" {
		if at := strings.Index(info.Email, "@"); at > 0 {
			return info.Email[:at]
		}
	}
	return info.Sub
}

func sanitize(in string) string {
	out := make([]rune, 0, len(in))
	for _, r := range in {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			out = append(out, r)
			continue
		}
		out = append(out, '_')
	}
	return string(out)
}

func padShort(in string) string {
	if len(in) < usernameMinLen {
		return "oauth_" + in
	}
	return in
}

func truncate(in string) string {
	if len(in) > usernameMaxLen {
		return in[:usernameMaxLen]
	}
	return in
}
