package responses

import (
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// decodeResponsesNamespaceFunctions expands an OpenAI Responses namespace
// group into explicit IR functions before any target codec can apply a builtin
// tool fallback policy.
func decodeResponsesNamespaceFunctions(raw json.RawMessage, seen map[string]map[string]struct{}, groupID string) ([]ir.Tool, error) {
	var group respNamespaceTool
	if err := json.Unmarshal(raw, &group); err != nil {
		return nil, fmt.Errorf("unmarshal namespace tool: %w", err)
	}
	if group.Name == "" {
		return nil, fmt.Errorf("namespace tool has empty name")
	}
	if len(group.Tools) == 0 {
		return nil, fmt.Errorf("namespace %q has no tools", group.Name)
	}
	if seen[group.Name] == nil {
		seen[group.Name] = make(map[string]struct{})
	}

	tools := make([]ir.Tool, 0, len(group.Tools))
	for index, child := range group.Tools {
		if child.Type != "function" {
			return nil, fmt.Errorf("namespace %q child %d has type %q, want function", group.Name, index, child.Type)
		}
		if child.Name == "" {
			return nil, fmt.Errorf("namespace %q child %d has empty name", group.Name, index)
		}
		if _, exists := seen[group.Name][child.Name]; exists {
			return nil, fmt.Errorf("namespace %q has duplicate function %q", group.Name, child.Name)
		}
		seen[group.Name][child.Name] = struct{}{}
		var inputSchema any
		if len(child.Parameters) > 0 {
			inputSchema = child.Parameters
		}
		tools = append(tools, ir.Tool{
			Type:             "function",
			Namespace:        group.Name,
			NamespaceGroupID: groupID,
			Name:             child.Name,
			Description:      child.Description,
			InputSchema:      inputSchema,
			Strict:           child.Strict,
		})
	}
	return tools, nil
}

type responsesNamespaceOutputGroup struct {
	id       string
	name     string
	children []any
}

type responsesEncodedTools struct {
	tools             []any
	dropped           []convert.DroppedTool
	functionFallbacks []convert.FunctionFallbackTool
}

// encodeResponsesTools keeps the existing ResolveTool behavior for ordinary
// tools and restores only explicit namespace function children into a Responses
// namespace container. A namespace child must be a function before ResolveTool
// runs, so builtin fallback cannot drop it.
func encodeResponsesTools(
	tools []ir.Tool,
	source ir.Protocol,
	policy convert.BuiltinToolFallbackPolicy,
	emit convert.TargetEmitFuncs,
) (responsesEncodedTools, error) {
	var encoded responsesEncodedTools
	seenChildren := make(map[string]map[string]struct{})
	var previousNamespaceGroup *responsesNamespaceOutputGroup

	for _, tool := range tools {
		if tool.Namespace != "" {
			if tool.Type != "" && tool.Type != "function" {
				return responsesEncodedTools{}, fmt.Errorf("%w: namespace %q tool %q has type %q", convert.ErrNamespacedFunctionNotFunction, tool.Namespace, tool.Name, tool.Type)
			}
			if seenChildren[tool.Namespace] == nil {
				seenChildren[tool.Namespace] = make(map[string]struct{})
			}
			if _, exists := seenChildren[tool.Namespace][tool.Name]; exists {
				return responsesEncodedTools{}, fmt.Errorf("namespace %q has duplicate function %q", tool.Namespace, tool.Name)
			}
			seenChildren[tool.Namespace][tool.Name] = struct{}{}
		}

		resolved, err := convert.ResolveTool(tool, source, ir.ProtocolOpenAIResponses, policy, emit)
		if err != nil {
			return responsesEncodedTools{}, err
		}
		if resolved.Dropped != nil {
			encoded.dropped = append(encoded.dropped, *resolved.Dropped)
			previousNamespaceGroup = nil
			continue
		}
		if resolved.FunctionFallback != nil {
			encoded.functionFallbacks = append(encoded.functionFallbacks, *resolved.FunctionFallback)
		}
		if tool.Namespace == "" {
			encoded.tools = append(encoded.tools, resolved.Emit)
			previousNamespaceGroup = nil
			continue
		}

		group := previousNamespaceGroup
		if !canAppendToResponsesNamespaceGroup(group, tool) {
			group = &responsesNamespaceOutputGroup{id: tool.NamespaceGroupID, name: tool.Namespace}
			encoded.tools = append(encoded.tools, group)
		}
		group.children = append(group.children, resolved.Emit)
		previousNamespaceGroup = group
	}

	for index, entry := range encoded.tools {
		group, ok := entry.(*responsesNamespaceOutputGroup)
		if !ok {
			continue
		}
		encoded.tools[index] = map[string]any{
			"type":  "namespace",
			"name":  group.name,
			"tools": group.children,
		}
	}
	return encoded, nil
}

func canAppendToResponsesNamespaceGroup(group *responsesNamespaceOutputGroup, tool ir.Tool) bool {
	return group != nil && group.name == tool.Namespace && group.id == tool.NamespaceGroupID
}
