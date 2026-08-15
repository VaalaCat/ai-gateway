package tunnel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebSocketCloseCodecRFCValidity(t *testing.T) {
	_, err := EncodeWebSocketClose(0, "")
	require.NoError(t, err, "empty Close has no status on wire")
	decoded, err := DecodeWebSocketClose(nil)
	require.NoError(t, err)
	require.Equal(t, WebSocketCloseEvent, decoded.Kind)
	require.Zero(t, decoded.Code)

	for _, code := range []int{1005, 1006, 1014, 1015, 2000} {
		_, err = EncodeWebSocketClose(code, "invalid")
		require.Error(t, err, "code %d must not be sent", code)
	}
	_, err = EncodeWebSocketClose(1000, string([]byte{0xff}))
	require.Error(t, err)
	_, err = DecodeWebSocketClose([]byte{0x03, 0xe8, 0xff})
	require.Error(t, err)

	payload, err := EncodeWebSocketClose(1000, strings.Repeat("a", 123))
	require.NoError(t, err)
	require.Len(t, payload, MaxWebSocketControlPayloadBytes)
	_, err = EncodeWebSocketClose(1000, strings.Repeat("a", 124))
	require.Error(t, err)
}
