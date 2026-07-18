package channelfile

import (
	"errors"
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
