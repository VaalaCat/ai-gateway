package models

import (
	"errors"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// AuthType 指定上游认证头及其原始值模板。
type AuthType struct {
	Header string `json:"header"`
	Value  string `json:"value"`
}

// ChannelAuth 保存整体默认认证和出站协议覆盖。
type ChannelAuth struct {
	Default   *AuthType           `json:"default,omitempty"`
	Protocols map[string]AuthType `json:"protocols,omitempty"`
}

var (
	ErrUpstreamAuthHeader   = errors.New("upstream auth header is invalid")
	ErrUpstreamAuthValue    = errors.New("upstream auth value is invalid")
	ErrUpstreamAuthProtocol = errors.New("upstream auth protocol is unsupported")
)

// ValidateChannelAuth 校验整体和协议认证的头名、值与协议键。
func ValidateChannelAuth(auth ChannelAuth) error {
	// 1. 校验整体默认认证。
	if auth.Default != nil {
		if err := validateAuthType(*auth.Default); err != nil {
			return err
		}
	}
	// 2. 校验各出站协议的完整认证设置。
	for protocol, authType := range auth.Protocols {
		switch protocol {
		case "chat_completions", "responses", "messages", "generate_content":
		default:
			return ErrUpstreamAuthProtocol
		}
		if err := validateAuthType(authType); err != nil {
			return err
		}
	}
	return nil
}

// validateAuthType 校验单组认证头和未展开的值模板。
func validateAuthType(authType AuthType) error {
	// 1. 校验头名，并拒绝传输头与内部头。
	if !httpguts.ValidHeaderFieldName(authType.Header) {
		return ErrUpstreamAuthHeader
	}
	header := strings.ToLower(authType.Header)
	if strings.HasPrefix(header, "x-vaala-") {
		return ErrUpstreamAuthHeader
	}
	switch header {
	case "host", "content-length", "content-type", "content-encoding", "accept-encoding",
		"connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return ErrUpstreamAuthHeader
	}
	// 2. 校验值中的控制字符和变量名。
	if authType.Value == "" || !httpguts.ValidHeaderFieldValue(authType.Value) ||
		strings.Contains(strings.ReplaceAll(authType.Value, "${CHANNEL_KEY}", ""), "${") {
		return ErrUpstreamAuthValue
	}
	return nil
}

// Clone 复制认证指针和协议 map。
func (a ChannelAuth) Clone() ChannelAuth {
	// 1. 复制整体默认认证。
	cloned := a
	if a.Default != nil {
		defaultAuth := *a.Default
		cloned.Default = &defaultAuth
	}
	// 2. 复制协议覆盖。
	if a.Protocols != nil {
		cloned.Protocols = make(map[string]AuthType, len(a.Protocols))
		for protocol, authType := range a.Protocols {
			cloned.Protocols[protocol] = authType
		}
	}
	return cloned
}
