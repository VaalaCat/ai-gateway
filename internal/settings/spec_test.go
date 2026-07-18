package settings

import (
	"errors"
	"reflect"
	"strconv"
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
		"breaker_threshold", "breaker_cooldown_ms", "breaker_enabled", "retry_max_channels",
		"byok_billing_mode", "min_quota_reserve",
		"rate_limiter_enabled", "sse_keepalive_ms", "queue_time_ms",
		"health_error_rate_yellow_pct", "health_error_rate_red_pct",
		"health_saturation_yellow_pct", "health_saturation_red_pct",
		"health_offline_seconds", "health_window_seconds",
		"cache_load_timeout_ms", "cache_refresh_after_ms", "cache_refresh_timeout_ms",
		"cache_refresh_max_retries", "cache_refresh_backoff_base_ms", "cache_refresh_backoff_max_ms",
		"usage_upload_backoff_max_sec", "usage_upload_concurrency", "usage_slim_body_after_attempts",
		"usage_strip_trace_after_attempts", "usage_billing_only_after_attempts", "heartbeat_call_timeout_sec",
		"heartbeat_reconnect_failures", "agent.control_heartbeat_degraded_seconds",
		"agent.control_health_recovery_samples",
		"image_inline_fetch_timeout_sec", "image_inline_max_bytes", "image_inline_concurrency",
		"image_inline_ssrf_guard", "image_inline_host_allowlist",
		"agent.relay_default_uri", "agent.relay_fallback_enabled",
		"agent.body_memory_threshold_bytes", "agent.body_hard_limit_bytes",
		"agent.tunnel_max_metadata_bytes", "agent.tunnel_max_data_bytes",
		"agent.tunnel_initial_window_bytes", "agent.tunnel_max_session_queue_bytes",
		"agent.tunnel_max_streams", "agent.tunnel_open_to_commit_timeout_ms",
		"agent.tunnel_window_stall_timeout_ms", "agent.tunnel_drain_timeout_seconds",
	}, keys, "Keys 应按 struct 字段声明顺序")
}

func TestAgentRelayAndTunnelSettingsSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		kind  reflect.Kind
		key   string
		def   string
		min   string
		max   string
		tag   string
	}{
		{"RelayDefaultURI", reflect.String, "agent.relay_default_uri", "", "", "", "agent.relay_default_uri,"},
		{"RelayFallbackEnabled", reflect.Int, "agent.relay_fallback_enabled", "0", "0", "1", "agent.relay_fallback_enabled,0,0,1"},
		{"BodyMemoryThresholdBytes", reflect.Int64, "agent.body_memory_threshold_bytes", "1048576", "65536", "16777216", "agent.body_memory_threshold_bytes,1048576,65536,16777216"},
		{"BodyHardLimitBytes", reflect.Int64, "agent.body_hard_limit_bytes", "67108864", "1048576", "268435456", "agent.body_hard_limit_bytes,67108864,1048576,268435456"},
		{"TunnelMaxMetadataBytes", reflect.Int64, "agent.tunnel_max_metadata_bytes", "65536", "4096", "262144", "agent.tunnel_max_metadata_bytes,65536,4096,262144"},
		{"TunnelMaxDataBytes", reflect.Int64, "agent.tunnel_max_data_bytes", "65536", "4096", "262144", "agent.tunnel_max_data_bytes,65536,4096,262144"},
		{"TunnelInitialWindowBytes", reflect.Int64, "agent.tunnel_initial_window_bytes", "524288", "65536", "8388608", "agent.tunnel_initial_window_bytes,524288,65536,8388608"},
		{"TunnelMaxSessionQueueBytes", reflect.Int64, "agent.tunnel_max_session_queue_bytes", "8388608", "524288", "67108864", "agent.tunnel_max_session_queue_bytes,8388608,524288,67108864"},
		{"TunnelMaxStreams", reflect.Int, "agent.tunnel_max_streams", "256", "1", "4096", "agent.tunnel_max_streams,256,1,4096"},
		{"TunnelOpenToCommitTimeoutMS", reflect.Int, "agent.tunnel_open_to_commit_timeout_ms", "30000", "1000", "120000", "agent.tunnel_open_to_commit_timeout_ms,30000,1000,120000"},
		{"TunnelWindowStallTimeoutMS", reflect.Int, "agent.tunnel_window_stall_timeout_ms", "60000", "1000", "300000", "agent.tunnel_window_stall_timeout_ms,60000,1000,300000"},
		{"TunnelDrainTimeoutSec", reflect.Int, "agent.tunnel_drain_timeout_seconds", "300", "1", "1800", "agent.tunnel_drain_timeout_seconds,300,1,1800"},
	}

	typ := reflect.TypeFor[AgentSettings]()
	defaults := Defaults()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.field, func(t *testing.T) {
			field, ok := typ.FieldByName(tt.field)
			require.True(t, ok, "missing AgentSettings field")
			require.Equal(t, tt.kind, field.Type.Kind())
			require.Equal(t, tt.tag, field.Tag.Get("setting"))
			require.Equal(t, tt.def, defaults[tt.key])

			if tt.kind == reflect.String {
				require.NoError(t, Validate(tt.key, "wss://relay.example/tunnel"))
				return
			}

			require.NoError(t, Validate(tt.key, tt.min))
			require.NoError(t, Validate(tt.key, tt.max))
			min, err := strconv.ParseInt(tt.min, 10, 64)
			require.NoError(t, err)
			max, err := strconv.ParseInt(tt.max, 10, 64)
			require.NoError(t, err)
			require.Error(t, Validate(tt.key, strconv.FormatInt(min-1, 10)))
			require.Error(t, Validate(tt.key, strconv.FormatInt(max+1, 10)))
		})
	}
}

func TestControlHealthSettings_DefaultsAndRanges(t *testing.T) {
	defs := Defaults()
	require.Equal(t, "90", defs["agent.control_heartbeat_degraded_seconds"])
	require.Equal(t, "2", defs["agent.control_health_recovery_samples"])

	var s AgentSettings
	require.NoError(t, Apply(&s, "agent.control_heartbeat_degraded_seconds", "10"))
	require.Equal(t, 10, s.ControlHeartbeatDegradedSec)
	require.NoError(t, Apply(&s, "agent.control_heartbeat_degraded_seconds", "3600"))
	require.Error(t, Apply(&s, "agent.control_heartbeat_degraded_seconds", "9"))
	require.Error(t, Apply(&s, "agent.control_heartbeat_degraded_seconds", "3601"))

	require.NoError(t, Apply(&s, "agent.control_health_recovery_samples", "1"))
	require.Equal(t, 1, s.ControlHealthRecoverySamples)
	require.NoError(t, Apply(&s, "agent.control_health_recovery_samples", "10"))
	require.Error(t, Apply(&s, "agent.control_health_recovery_samples", "0"))
	require.Error(t, Apply(&s, "agent.control_health_recovery_samples", "11"))
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

func TestBreakerEnabledSetting(t *testing.T) {
	defs := Defaults()
	require.Equal(t, "1", defs["breaker_enabled"])

	var s AgentSettings
	require.NoError(t, Apply(&s, "breaker_enabled", "0"))
	require.Equal(t, 0, s.BreakerEnabled)
	require.NoError(t, Apply(&s, "breaker_enabled", "1"))
	require.Equal(t, 1, s.BreakerEnabled)
	require.Error(t, Validate("breaker_enabled", "2"), "breaker_enabled=2 should be rejected")
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

func TestImageInlineSettings_DefaultsAndParse(t *testing.T) {
	defs := Defaults()
	require.Equal(t, "10", defs["image_inline_fetch_timeout_sec"])
	require.Equal(t, "10485760", defs["image_inline_max_bytes"])
	require.Equal(t, "4", defs["image_inline_concurrency"])
	require.Equal(t, "1", defs["image_inline_ssrf_guard"])
	require.Equal(t, "", defs["image_inline_host_allowlist"])

	var s AgentSettings
	for k, v := range Defaults() {
		require.NoError(t, Apply(&s, k, v))
	}
	require.Equal(t, 10, s.ImageInlineFetchTimeoutSec)
	require.Equal(t, 10485760, s.ImageInlineMaxBytes)
	require.Equal(t, 4, s.ImageInlineConcurrency)
	require.Equal(t, 1, s.ImageInlineSSRFGuard)
	require.Equal(t, "", s.ImageInlineHostAllowlist)

	// int key 走范围校验
	require.NoError(t, Apply(&s, "image_inline_concurrency", "8"))
	require.Equal(t, 8, s.ImageInlineConcurrency)

	require.Error(t, Apply(&s, "image_inline_concurrency", "999"))
	require.Error(t, Apply(&s, "image_inline_concurrency", "0"))

	// string key 直存
	require.NoError(t, Apply(&s, "image_inline_host_allowlist", "a.com,b.com"))
	require.Equal(t, "a.com,b.com", s.ImageInlineHostAllowlist)
}
