package byokcrypto

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"go.uber.org/zap"
)

// errDecryptFailure 是 decrypt 失败时对外暴露的唯一固定文案。
// 不带任何 cipher 内部细节（AAD 长度、tag mismatch、ciphertext too short），
// 防止攻击者通过错误文案区分失败原因构造 oracle。
var errDecryptFailure = errors.New("decrypt failure")

// SanitizeDecryptErr 把 cipher 内部 decrypt 错误转为对外固定 error。
//   - nil 透传 nil（不把成功转成失败）
//   - 非 nil：原 err 通过 zap.L().Error 落 server 日志；
//     metrics.BYOKDecryptFailureTotal +1；
//     返回固定的 errDecryptFailure（无内部细节）
//
// 调用点：所有 cipher.Open() 失败路径必须经此 sanitize 后再返回上层。
// 见 §5.1 — 防御 AAD/length oracle 信息泄露。
func SanitizeDecryptErr(err error) error {
	if err == nil {
		return nil
	}
	if l := zap.L(); l != nil {
		l.Error("byok decrypt failed", zap.Error(err))
	}
	metrics.BYOKDecryptFailureTotal.Inc()
	return errDecryptFailure
}
