package convert

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestFilterOptionalRequestFields(t *testing.T) {
	t.Run("default filters optional fields and keeps store", func(t *testing.T) {
		store := true
		req := &ir.Request{
			ServiceTier: "priority", InferenceGeo: "us", SafetyIdentifier: "user", Store: &store,
			StreamOptions: map[string]any{"include_obfuscation": false, "include_usage": true},
			Extras: map[string]any{
				"inference_geo":     "legacy",
				"safety_identifier": "legacy",
				"stream_options": map[string]any{
					"include_obfuscation": false,
					"custom":              "keep",
				},
			},
		}
		FilterOptionalRequestFields(req, DefaultRequestFieldPermissions())
		if req.ServiceTier != "" || req.InferenceGeo != "" || req.SafetyIdentifier != "" || req.Store == nil || req.StreamOptions["include_usage"] != true {
			t.Fatalf("filtered request = %#v", req)
		}
		if _, ok := req.StreamOptions["include_obfuscation"]; ok {
			t.Fatal("include_obfuscation was retained")
		}
		nested, ok := req.Extras["stream_options"].(map[string]any)
		if !ok || nested["custom"] != "keep" {
			t.Fatalf("nested stream_options = %#v", req.Extras["stream_options"])
		}
		if _, ok := nested["include_obfuscation"]; ok {
			t.Fatal("nested include_obfuscation was retained")
		}
	})
	t.Run("all allowed are retained", func(t *testing.T) {
		store := false
		req := &ir.Request{ServiceTier: "priority", InferenceGeo: "eu", SafetyIdentifier: "user", Store: &store, StreamOptions: map[string]any{"include_obfuscation": true}}
		FilterOptionalRequestFields(req, RequestFieldPermissions{AllowServiceTier: true, AllowInferenceGeo: true, AllowStore: true, AllowSafetyIdentifier: true, AllowIncludeObfuscation: true})
		if req.ServiceTier == "" || req.InferenceGeo == "" || req.SafetyIdentifier == "" || req.Store == nil || req.StreamOptions == nil {
			t.Fatalf("allowed fields changed: %#v", req)
		}
	})
	t.Run("nil request", func(t *testing.T) {
		FilterOptionalRequestFields(nil, RequestFieldPermissions{})
	})
	t.Run("disabled fields remove empty containers", func(t *testing.T) {
		store := false
		request := &ir.Request{
			Store:         &store,
			StreamOptions: map[string]any{"include_obfuscation": true},
			Extras: map[string]any{
				"store":             true,
				"safety_identifier": "user",
				"stream_options":    map[string]any{"include_obfuscation": true},
			},
		}
		permissions := DefaultRequestFieldPermissions()
		permissions.AllowStore = false
		FilterOptionalRequestFields(request, permissions)
		if request.Store != nil || request.StreamOptions != nil || request.Extras != nil {
			t.Fatalf("request = %#v", request)
		}
	})
}
