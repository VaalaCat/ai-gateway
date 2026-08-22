package convert

import "github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"

func DefaultRequestFieldPermissions() RequestFieldPermissions {
	return RequestFieldPermissions{AllowStore: true}
}

func FilterOptionalRequestFields(request *ir.Request, permissions RequestFieldPermissions) {
	if request == nil {
		return
	}
	if !permissions.AllowServiceTier {
		request.ServiceTier = ""
		delete(request.Extras, "service_tier")
	}
	if !permissions.AllowInferenceGeo {
		request.InferenceGeo = ""
		delete(request.Extras, "inference_geo")
	}
	if !permissions.AllowStore {
		request.Store = nil
		delete(request.Extras, "store")
	}
	if !permissions.AllowSafetyIdentifier {
		request.SafetyIdentifier = ""
		delete(request.Extras, "safety_identifier")
	}
	if !permissions.AllowIncludeObfuscation {
		request.StreamOptions = filterIncludeObfuscation(request.StreamOptions)
		if options, ok := request.Extras["stream_options"].(map[string]any); ok {
			options = filterIncludeObfuscation(options)
			if len(options) == 0 {
				delete(request.Extras, "stream_options")
			} else {
				request.Extras["stream_options"] = options
			}
		}
	}
	if len(request.Extras) == 0 {
		request.Extras = nil
	}
}

func filterIncludeObfuscation(options map[string]any) map[string]any {
	delete(options, "include_obfuscation")
	if len(options) == 0 {
		return nil
	}
	return options
}
