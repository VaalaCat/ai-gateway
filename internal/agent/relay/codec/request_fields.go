package codec

// RequestFieldPermissions controls which optional client request fields may be
// forwarded to an upstream provider. Store is the only field allowed by
// default, matching the legacy adaptor's privacy and billing policy.
type RequestFieldPermissions struct {
	AllowServiceTier        bool `json:"allow_service_tier,omitempty"`
	AllowInferenceGeo       bool `json:"allow_inference_geo,omitempty"`
	AllowStore              bool `json:"allow_store"`
	AllowSafetyIdentifier   bool `json:"allow_safety_identifier,omitempty"`
	AllowIncludeObfuscation bool `json:"allow_include_obfuscation,omitempty"`
}

func DefaultRequestFieldPermissions() RequestFieldPermissions {
	return RequestFieldPermissions{AllowStore: true}
}

// FilterOptionalRequestFields applies the channel request-field policy before
// an outbound codec serializes the IR. It mutates the working request owned by
// the dataflow pass; Pass.Original remains unchanged.
func FilterOptionalRequestFields(req *Request, permissions RequestFieldPermissions) {
	if req == nil {
		return
	}

	if !permissions.AllowServiceTier {
		req.ServiceTier = ""
		delete(req.Extras, "service_tier")
	}
	if !permissions.AllowInferenceGeo {
		req.InferenceGeo = ""
		delete(req.Extras, "inference_geo")
	}
	if !permissions.AllowStore {
		req.Store = nil
		delete(req.Extras, "store")
	}
	if !permissions.AllowSafetyIdentifier {
		req.SafetyIdentifier = ""
		delete(req.Extras, "safety_identifier")
	}
	if !permissions.AllowIncludeObfuscation {
		req.StreamOptions = filterIncludeObfuscation(req.StreamOptions)
		if streamOptions, ok := req.Extras["stream_options"].(map[string]any); ok {
			streamOptions = filterIncludeObfuscation(streamOptions)
			if len(streamOptions) == 0 {
				delete(req.Extras, "stream_options")
			} else {
				req.Extras["stream_options"] = streamOptions
			}
		}
	}
	if len(req.Extras) == 0 {
		req.Extras = nil
	}
}

func filterIncludeObfuscation(streamOptions map[string]any) map[string]any {
	delete(streamOptions, "include_obfuscation")
	if len(streamOptions) == 0 {
		return nil
	}
	return streamOptions
}
