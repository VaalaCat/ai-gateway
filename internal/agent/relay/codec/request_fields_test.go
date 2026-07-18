package codec

import "testing"

func TestFilterOptionalRequestFields_DefaultPermissions(t *testing.T) {
	store := true
	req := &Request{
		ServiceTier:      "priority",
		InferenceGeo:     "us",
		SafetyIdentifier: "user-123",
		Store:            &store,
		StreamOptions: map[string]any{
			"include_obfuscation": false,
			"include_usage":       true,
		},
		Extras: map[string]any{
			"inference_geo":     "legacy-copy",
			"safety_identifier": "legacy-copy",
			"stream_options": map[string]any{
				"include_obfuscation": false,
				"custom":              "keep",
			},
		},
	}

	FilterOptionalRequestFields(req, DefaultRequestFieldPermissions())

	if req.ServiceTier != "" || req.InferenceGeo != "" || req.SafetyIdentifier != "" {
		t.Fatalf("default permissions must filter optional identity/billing fields: %#v", req)
	}
	if req.Store == nil || !*req.Store {
		t.Fatal("store must remain allowed by default")
	}
	if _, ok := req.StreamOptions["include_obfuscation"]; ok {
		t.Fatal("include_obfuscation must be filtered by default")
	}
	if req.StreamOptions["include_usage"] != true {
		t.Fatalf("unrelated stream option was removed: %#v", req.StreamOptions)
	}
	extraStream, ok := req.Extras["stream_options"].(map[string]any)
	if !ok || extraStream["custom"] != "keep" {
		t.Fatalf("nested extras stream_options were not preserved: %#v", req.Extras)
	}
	if _, ok := extraStream["include_obfuscation"]; ok {
		t.Fatal("nested include_obfuscation must be filtered by default")
	}
}

func TestFilterOptionalRequestFields_AllAllowed(t *testing.T) {
	store := true
	req := &Request{
		ServiceTier:      "priority",
		InferenceGeo:     "eu",
		SafetyIdentifier: "user-123",
		Store:            &store,
		StreamOptions:    map[string]any{"include_obfuscation": false},
	}
	permissions := RequestFieldPermissions{
		AllowServiceTier:        true,
		AllowInferenceGeo:       true,
		AllowStore:              true,
		AllowSafetyIdentifier:   true,
		AllowIncludeObfuscation: true,
	}

	FilterOptionalRequestFields(req, permissions)

	if req.ServiceTier != "priority" || req.InferenceGeo != "eu" || req.SafetyIdentifier != "user-123" {
		t.Fatalf("allowed fields changed: %#v", req)
	}
	if req.Store == nil || req.StreamOptions["include_obfuscation"] != false {
		t.Fatalf("allowed store/stream options changed: %#v", req)
	}
}

func TestFilterOptionalRequestFields_DisabledStoreAndEmptyContainers(t *testing.T) {
	store := false
	req := &Request{
		Store:         &store,
		StreamOptions: map[string]any{"include_obfuscation": true},
		Extras: map[string]any{
			"store":             true,
			"stream_options":    map[string]any{"include_obfuscation": true},
			"safety_identifier": "user-123",
		},
	}
	permissions := DefaultRequestFieldPermissions()
	permissions.AllowStore = false

	FilterOptionalRequestFields(req, permissions)

	if req.Store != nil || req.StreamOptions != nil || req.Extras != nil {
		t.Fatalf("disabled/empty fields were not removed: %#v", req)
	}
	FilterOptionalRequestFields(nil, permissions)
}
