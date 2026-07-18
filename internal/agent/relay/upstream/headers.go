package upstream

import (
	"net/http"
	"net/textproto"
	"strings"
)

// alwaysStrippedHeaders 是网关永远自己掌管、绝不从客户端转发的请求头(小写)。
// 四类理由:鉴权改写 / 框架重建 / 压缩解压机器 / 安全泄漏。
var alwaysStrippedHeaders = map[string]bool{
	// hop-by-hop (RFC 7230 §6.1)
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true, "expect": true,
	// 框架 / 网关按新 body 重建
	"host": true, "content-length": true, "content-type": true, "content-encoding": true,
	// 压缩例外:删掉 accept-encoding 让 Go Transport 注入 gzip 并透明解压,
	// usage 抽取依赖明文 body。这是为保住解压机器的刻意保留,不是诚实透传。
	"accept-encoding": true,
	// 全协议凭证:显式删除,避免源协议凭证跨协议泄漏到目标上游。
	"authorization": true, "x-api-key": true, "api-key": true,
	"x-goog-api-key": true, "openai-organization": true,
	// 客户端身份:与 API 鉴权无关,部分上游因 cookie 行为异常。
	"cookie": true,
	// 转发/CDN 泄漏(精确名;前缀见 alwaysStrippedPrefixes)
	"forwarded": true, "x-real-ip": true, "cdn-loop": true,
}

// alwaysStrippedPrefixes 永远剥离的前缀(小写)。
var alwaysStrippedPrefixes = []string{
	"x-forwarded-", // 任意转发变体
	"cf-",          // Cloudflare 注入头 (CF-Ray 等); 短前缀有误杀风险, 已知可接受
	"eo-",          // 腾讯 EdgeOne 边缘注入头 (EO-Client-IP/EO-Log-UUID 等); 同 cf- 短前缀风险, 已知可接受
	"x-vaala-",     // 网关内部 header
}

// crossProtocolStrippedHeaders 仅入站协议 != 出站协议时剥离:源协议的 feature/beta
// 开关,对不同目标协议无意义甚至报错。
var crossProtocolStrippedHeaders = map[string]bool{
	// 协议版本/特性开关:同协议应透传(passthrough 无 codec 重编码,依赖客户端原值;
	// native 同协议由 codec 先 set,dst 已有则不覆盖);跨协议对目标协议无意义甚至报错,才剥离。
	"anthropic-version": true,
	"anthropic-beta":    true,
	"openai-beta":       true,
}

// crossProtocolStrippedPrefixes 仅跨协议剥离的前缀(小写)。
var crossProtocolStrippedPrefixes = []string{
	"x-stainless-", // OpenAI/Anthropic SDK 指纹
}

// ForwardClientHeaders 把客户端入站 header 诚实地叠到 dst(上游请求头)上:
// 默认转发一切,只跳过受管 header。dst 里已 set 的目标协议头(鉴权/content-type/
// 版本)不会被覆盖。crossProtocol=true 时额外剥掉源协议 feature/SDK 头。
// User-Agent:客户端发了就转发;没发就显式置空,阻止 Go 注入 Go-http-client/N。
func ForwardClientHeaders(dst, inbound http.Header, crossProtocol bool) {
	for key, vals := range inbound {
		if isStrippedHeader(key, crossProtocol) {
			continue
		}
		if _, exists := dst[textproto.CanonicalMIMEHeaderKey(key)]; exists {
			continue // codec/path 已 set 的目标头优先
		}
		for _, v := range vals {
			dst.Add(key, v)
		}
	}
	if _, ok := dst[textproto.CanonicalMIMEHeaderKey("User-Agent")]; !ok {
		dst.Set("User-Agent", "") // 空串:Go 既不注入也不发 UA
	}
}

// isStrippedHeader 判断一个 header 名是否落在受管剥离集合内。
func isStrippedHeader(key string, crossProtocol bool) bool {
	lower := strings.ToLower(key)
	if alwaysStrippedHeaders[lower] {
		return true
	}
	if hasAnyPrefix(lower, alwaysStrippedPrefixes) {
		return true
	}
	if crossProtocol {
		if crossProtocolStrippedHeaders[lower] {
			return true
		}
		if hasAnyPrefix(lower, crossProtocolStrippedPrefixes) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
