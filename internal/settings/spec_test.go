package settings

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaults_AllKeysPresent(t *testing.T) {
	d := Defaults()
	require.Contains(t, d, "trace_max_body_size")
	require.Contains(t, d, "fallback_sleep_ms")
	require.Equal(t, "65536", d["trace_max_body_size"])
	require.Equal(t, "1000", d["fallback_sleep_ms"])
}

func TestApply_KnownKey(t *testing.T) {
	var s AgentSettings
	require.NoError(t, Apply(&s, "fallback_sleep_ms", "2500"))
	require.Equal(t, 2500, s.FallbackSleepMs)
}

func TestApply_UnknownKey_Ignored(t *testing.T) {
	var s AgentSettings
	require.NoError(t, Apply(&s, "no_such_key", "x"))
	require.Equal(t, 0, s.FallbackSleepMs)
}

func TestApply_ParseError(t *testing.T) {
	var s AgentSettings
	err := Apply(&s, "fallback_sleep_ms", "not_an_int")
	require.Error(t, err)
	var pe *ParseError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "fallback_sleep_ms", pe.Key)
}

func TestApply_RangeError_Below(t *testing.T) {
	var s AgentSettings
	err := Apply(&s, "fallback_sleep_ms", "-1")
	require.Error(t, err)
	var re *RangeError
	require.True(t, errors.As(err, &re))
	require.Equal(t, "fallback_sleep_ms", re.Key)
}

func TestApply_RangeError_Above(t *testing.T) {
	var s AgentSettings
	err := Apply(&s, "fallback_sleep_ms", "60001")
	require.Error(t, err)
	var re *RangeError
	require.True(t, errors.As(err, &re))
	require.Equal(t, "fallback_sleep_ms", re.Key)
}

func TestValidate_NoMutation(t *testing.T) {
	err := Validate("trace_max_body_size", "131072")
	require.NoError(t, err)
	d := Defaults()
	require.Equal(t, "65536", d["trace_max_body_size"], "Defaults 应不受 Validate 影响")
}

func TestKeys_DeclarationOrder(t *testing.T) {
	keys := Keys()
	require.Equal(t, []string{"trace_max_body_size", "fallback_sleep_ms"}, keys,
		"Keys 应按 struct 字段声明顺序")
}
