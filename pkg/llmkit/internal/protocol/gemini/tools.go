package gemini

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type functionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parametersJsonSchema,omitempty"`
}

func encodeTools(req *ir.Request, policy convert.BuiltinToolFallbackPolicy, body, generation map[string]any) error {
	var declarations []functionDeclaration
	var dropped []convert.DroppedTool
	source := req.InboundProtocol
	if source == "" {
		source = ir.ProtocolGemini // Native tools supplied directly through the IR.
	}
	emit := convert.TargetEmitFuncs{Function: func(t ir.Tool) any {
		return functionDeclaration{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}
	}}
	var tools []any
	for _, tool := range req.Tools {
		switch tool.Type {
		case "image_generation":
			generation["responseModalities"] = []string{"TEXT", "IMAGE"}
			raw := object(tool.RawConfig)
			imageConfig := map[string]any{}
			if ratio, ok := raw["aspect_ratio"]; ok {
				imageConfig["aspectRatio"] = ratio
			}
			if size, ok := raw["image_size"]; ok {
				imageConfig["imageSize"] = size
			}
			if config, ok := raw["imageConfig"]; ok {
				for key, value := range object(config) {
					imageConfig[key] = value
				}
			}
			if len(imageConfig) > 0 {
				generation["imageConfig"] = imageConfig
			}
		case "web_search", "web_search_preview":
			tools = append(tools, map[string]any{"googleSearch": map[string]any{}})
		default:
			resolved, err := convert.ResolveTool(tool, source, ir.ProtocolGemini, policy, emit)
			if err != nil {
				return fmt.Errorf("%w: %s", err, tool.Type)
			}
			if resolved.Dropped != nil {
				dropped = append(dropped, *resolved.Dropped)
			} else if declaration, ok := resolved.Emit.(functionDeclaration); ok {
				declarations = append(declarations, declaration)
			} else if resolved.Emit != nil {
				tools = append(tools, resolved.Emit)
			}
		}
	}
	if len(declarations) > 0 {
		tools = append(tools, map[string]any{"functionDeclarations": declarations})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	convert.RecordDroppedTools(req, dropped)
	choice := req.ToolChoice
	if choice == nil {
		return nil
	}
	mode := "AUTO"
	config := map[string]any{}
	switch choice.Type {
	case "required":
		mode = "ANY"
	case "function", "custom":
		mode = "ANY"
		for _, tool := range dropped {
			if tool.Name == choice.Name && tool.Type == choice.Type {
				return fmt.Errorf("%w: selected tool %q was dropped", convert.ErrBuiltinToolUnsupported, choice.Name)
			}
		}
		config["allowedFunctionNames"] = []string{choice.Name}
	case "none":
		mode = "NONE"
	}
	if len(tools) == 0 {
		if mode == "ANY" {
			return fmt.Errorf("%w: tool_choice %q requires an available tool", convert.ErrBuiltinToolUnsupported, choice.Type)
		}
		return nil
	}
	config["mode"] = mode
	body["toolConfig"] = map[string]any{"functionCallingConfig": config}
	return nil
}
