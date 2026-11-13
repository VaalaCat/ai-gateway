package legacy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/xunfei"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLegacyTransferDistinguishesDummyResponseFromEmptyHTTPBody(t *testing.T) {
	for _, test := range []struct {
		name    string
		adaptor relaychannel.Adaptor
		unknown bool
	}{
		{name: "Xunfei dummy response", adaptor: &xunfei.Adaptor{}, unknown: true},
		{name: "HTTP empty response", adaptor: &emptyHTTPBodyAdaptor{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := inflight.NewRegistry(nil, 0)
			entry := registry.Track(inflight.Meta{})
			entry.SetCurrentAttempt(&inflight.AttemptInProgress{Seq: 1})
			collector := newTraceCollector(true)
			collector.progress = entry
			ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "invalid-local-test-key"}}
			result := relayPreparedRequest(nil, ginContext, test.adaptor, info, strings.NewReader("{}"), nil, "test", collector)
			// 1. 真实 Xunfei adaptor 在读取本地认证配置时退出，不建立外部连接。
			require.ErrorContains(t, result.Err, "invalid auth")
			progress := registry.Snapshot()[0]
			if test.unknown {
				require.Nil(t, progress.Progress.UpstreamBodyBytes)
				require.Nil(t, progress.AttemptProgress.UpstreamBodyBytes)
			} else {
				require.EqualValues(t, 0, *progress.Progress.UpstreamBodyBytes)
				require.EqualValues(t, 0, *progress.AttemptProgress.UpstreamBodyBytes)
			}
			require.Nil(t, progress.Progress.UpstreamIdleMs)
		})
	}
}

type emptyHTTPBodyAdaptor struct{ xunfei.Adaptor }

func (adaptor *emptyHTTPBodyAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (any, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}
