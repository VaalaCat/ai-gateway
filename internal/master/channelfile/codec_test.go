package channelfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"main","status":1,"type":1,"key":"secret","base_url":"","models":[],"model_mapping":{},"weight":1,"priority":0,"proxy_url":"","header_override":"","supported_api_types":"","endpoints":"","passthrough_enabled":false,"use_legacy_adaptor":false,"organization":"","api_version":"","system_prompt":"","system_prompt_in_input":false,"role_mapping":"","param_override":"","setting":"","tag":"","remark":"","test_model":"","auto_ban":0,"status_code_mapping":"","other_settings":"","disable_keepalive":false,"resilience":{},"price_ratio":1,"free":false,"limit":{},"affinity":{}}]}`
		envelope, err := Decode[AdminChannel](strings.NewReader(body), KindAdminChannels)
		if err != nil {
			t.Fatal(err)
		}
		if len(envelope.Channels) != 1 || envelope.Channels[0].Key != "secret" {
			t.Fatalf("unexpected envelope: %#v", envelope)
		}
	})

	t.Run("failure rejects unknown and trailing data", func(t *testing.T) {
		cases := []string{
			`{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[],"owner_id":1}`,
			`{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]} {}`,
		}
		for _, body := range cases {
			if _, err := Decode[AdminChannel](strings.NewReader(body), KindAdminChannels); err == nil {
				t.Fatalf("Decode(%q) succeeded", body)
			}
		}
	})

	t.Run("boundary rejects nil channels and too many rows", func(t *testing.T) {
		_, err := Decode[AdminChannel](strings.NewReader(`{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":null}`), KindAdminChannels)
		if err == nil {
			t.Fatal("nil channels accepted")
		}
		var fileErr *Error
		if !errors.As(err, &fileErr) || fileErr.Code != "invalid_channel_file" {
			t.Fatalf("err = %v", err)
		}

		rows := strings.Repeat(`{},`, MaxChannels) + `{}`
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[` + rows + `]}`
		_, err = Decode[AdminChannel](strings.NewReader(body), KindAdminChannels)
		if !errors.As(err, &fileErr) || fileErr.Code != "too_many_channels" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDecodeAdminChannelImport(t *testing.T) {
	t.Run("projects BYOK fields into admin channel", func(t *testing.T) {
		body := `{"schema_version":1,"kind":"byok_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"mine","status":1,"type":1,"key":"secret","base_url":"https://example.com","models":["qwen"],"model_mapping":{"qwen":"upstream"},"weight":2,"priority":3,"affinity":{"enabled":true}}]}`
		envelope, err := DecodeAdminChannelImport(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Kind != KindAdminChannels || envelope.ExportedAt.IsZero() || len(envelope.Channels) != 1 {
			t.Fatalf("unexpected envelope: %#v", envelope)
		}
		channel := envelope.Channels[0]
		if channel.Name != "mine" || channel.Key != "secret" || len(channel.Models) != 1 ||
			channel.ModelMapping["qwen"] != "upstream" || channel.Affinity.Enabled == nil || !*channel.Affinity.Enabled {
			t.Fatalf("unexpected projected channel: %#v", channel)
		}
	})

	t.Run("rejects fields unknown to declared BYOK source", func(t *testing.T) {
		body := `{"schema_version":1,"kind":"byok_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"mine","unexpected":true}]}`
		assertChannelFileErrorCode(t, DecodeAdminChannelImport, body, "invalid_channel_file")
	})

	t.Run("preserves native admin behavior and non-nil empty channels", func(t *testing.T) {
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]}`
		envelope, err := DecodeAdminChannelImport(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Kind != KindAdminChannels || envelope.Channels == nil || len(envelope.Channels) != 0 {
			t.Fatalf("unexpected envelope: %#v", envelope)
		}
	})
}

func TestDecodeBYOKChannelImport(t *testing.T) {
	t.Run("projects admin fields and drops admin-only fields", func(t *testing.T) {
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"shared","status":1,"type":1,"key":"secret","base_url":"https://example.com","models":["qwen"],"model_mapping":{"qwen":"upstream"},"weight":2,"priority":3,"proxy_url":"http://proxy","header_override":"{\"x\":\"y\"}","disable_keepalive":true,"resilience":{"max_retries":2},"price_ratio":2,"free":true,"limit":{"disable_at":1,"rules":[]},"affinity":{"enabled":true}}]}`
		envelope, err := DecodeBYOKChannelImport(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Kind != KindBYOKChannels || envelope.ExportedAt.IsZero() || len(envelope.Channels) != 1 {
			t.Fatalf("unexpected envelope: %#v", envelope)
		}
		channel := envelope.Channels[0]
		if channel.Name != "shared" || channel.Key != "secret" || len(channel.Models) != 1 ||
			channel.ModelMapping["qwen"] != "upstream" || channel.Affinity.Enabled == nil || !*channel.Affinity.Enabled {
			t.Fatalf("unexpected projected channel: %#v", channel)
		}
		encoded, err := json.Marshal(channel)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"proxy_url", "header_override", "disable_keepalive", "resilience", "price_ratio", "free", "limit"} {
			if strings.Contains(string(encoded), `"`+field+`"`) {
				t.Fatalf("admin-only field %q survived projection: %s", field, encoded)
			}
		}
	})

	t.Run("rejects malformed envelope boundaries", func(t *testing.T) {
		tooMany := strings.Repeat(`{},`, MaxChannels) + `{}`
		cases := []struct {
			name string
			body string
			code string
		}{
			{name: "unknown kind", body: `{"schema_version":1,"kind":"unknown","exported_at":"2026-07-16T00:00:00Z","channels":[]}`, code: "channel_file_kind_mismatch"},
			{name: "unsupported schema", body: `{"schema_version":2,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]}`, code: "unsupported_schema_version"},
			{name: "unsupported schema before unknown kind", body: `{"schema_version":2,"kind":"future_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]}`, code: "unsupported_schema_version"},
			{name: "missing schema before missing kind", body: `{}`, code: "unsupported_schema_version"},
			{name: "trailing JSON", body: `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]} {}`, code: "invalid_channel_file"},
			{name: "null channels", body: `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":null}`, code: "invalid_channel_file"},
			{name: "too many channels", body: fmt.Sprintf(`{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[%s]}`, tooMany), code: "too_many_channels"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assertChannelFileErrorCode(t, DecodeBYOKChannelImport, tc.body, tc.code)
			})
		}
	})

	t.Run("preserves native BYOK behavior", func(t *testing.T) {
		body := `{"schema_version":1,"kind":"byok_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"native","key":"secret","models":[],"model_mapping":{},"affinity":{}}]}`
		envelope, err := DecodeBYOKChannelImport(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Kind != KindBYOKChannels || envelope.Channels[0].Name != "native" {
			t.Fatalf("unexpected envelope: %#v", envelope)
		}
	})
}

func assertChannelFileErrorCode[T any](
	t *testing.T,
	decode func(io.Reader) (T, error),
	body string,
	want string,
) {
	t.Helper()
	_, err := decode(strings.NewReader(body))
	var fileErr *Error
	if !errors.As(err, &fileErr) || fileErr.Code != want {
		t.Fatalf("err = %v, want code %q", err, want)
	}
}
