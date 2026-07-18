package legacy

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
)

func init() {
	service.InitHttpClient()
	bindLegacyClientTransport(service.GetHttpClient())
	// Initialize new-api constants required for streaming and file handling.
	// These are normally set by common.InitEnv() which reads env vars,
	// but we set sensible defaults directly to avoid depending on env config.
	constant.StreamingTimeout = 300
	constant.StreamScannerMaxBufferMB = 64
	constant.MaxFileDownloadMB = 64
	constant.MaxRequestBodyMB = 128
	constant.ForceStreamOption = true
}
