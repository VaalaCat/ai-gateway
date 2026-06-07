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
	require.Equal(t, []string{
		"trace_max_body_size", "fallback_sleep_ms", "affinity_enabled", "affinity_ttl_sec",
		"max_retries_per_channel", "retry_backoff_base_ms", "retry_backoff_max_ms",
		"breaker_threshold", "breaker_cooldown_ms", "retry_max_channels",
		"byok_billing_mode", "min_quota_reserve",
		"rate_limiter_enabled", "sse_keepalive_ms", "queue_time_ms",
		"health_error_rate_yellow_pct", "health_error_rate_red_pct",
		"health_saturation_yellow_pct", "health_saturation_red_pct",
		"health_offline_seconds", "health_window_seconds",
		"cache_load_timeout_ms", "cache_refresh_after_ms", "cache_refresh_timeout_ms",
		"cache_refresh_max_retries", "cache_refresh_backoff_base_ms", "cache_refresh_backoff_max_ms",
	}, keys, "Keys 应按 struct 字段声明顺序")
}

func TestAgentSettings_QuotaReserveDefault(t *testing.T) {
	d := Defaults()
	if d["min_quota_reserve"] != "0" {
		t.Errorf("min_quota_reserve default = %q, want 0", d["min_quota_reserve"])
	}
}

func TestApply_QuotaReserve(t *testing.T) {
	var s AgentSettings
	if err := Apply(&s, "min_quota_reserve", "500"); err != nil {
		t.Fatal(err)
	}
	if s.MinQuotaReserve != 500 {
		t.Errorf("MinQuotaReserve = %d, want 500", s.MinQuotaReserve)
	}
}

func TestApply_StringField_BYOKMode(t *testing.T) {
	var s AgentSettings
	if err := Apply(&s, "byok_billing_mode", "service_fee"); err != nil {
		t.Fatalf("apply string setting: %v", err)
	}
	if s.BYOKBillingMode != "service_fee" {
		t.Errorf("BYOKBillingMode = %q, want service_fee", s.BYOKBillingMode)
	}
}

func TestApply_IntField_StillWorks(t *testing.T) { // regression: int path unchanged
	var s AgentSettings
	if err := Apply(&s, "retry_max_channels", "7"); err != nil {
		t.Fatal(err)
	}
	if s.RetryMaxChannels != 7 {
		t.Errorf("RetryMaxChannels = %d, want 7", s.RetryMaxChannels)
	}
}

func TestRetryMaxChannelsDefault(t *testing.T) {
	if got := Defaults()["retry_max_channels"]; got != "5" {
		t.Errorf("retry_max_channels default = %q, want 5", got)
	}
}

func TestAffinityDefaults(t *testing.T) {
	defs := Defaults()
	require.Equal(t, "1", defs["affinity_enabled"])
	require.Equal(t, "300", defs["affinity_ttl_sec"])
	var s AgentSettings
	for k, v := range Defaults() {
		require.NoError(t, Apply(&s, k, v))
	}
	require.Equal(t, 1, s.AffinityEnabled)
	require.Equal(t, 300, s.AffinityTTLSec)
	require.Error(t, Validate("affinity_enabled", "2"), "affinity_enabled=2 should be rejected")
}
