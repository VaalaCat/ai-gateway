package llmkit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

func TestCodexResponsesRequestToGemini(t *testing.T) {
	codec := NewCodec()
	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: []byte(`{
		"model":"gemini-4-argon",
		"input":"Read the file.",
		"stream":true,
		"tool_choice":"auto",
		"client_metadata":{"x-codex-turn-metadata":"codex"},
		"generationConfig":{"maxOutputTokens":100},
		"tools":[
			{"type":"custom","name":"apply_patch","description":"Apply a patch","format":{"type":"text"}},
			{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}
		]
	}`)})
	require.NoError(t, err)
	var dropped []DroppedTool
	encoded, err := codec.EncodeRequest(EncodeRequestInput{
		Request: decoded.Request,
		Target:  Target{Protocol: ProtocolGemini, Model: "gemini-4-argon"},
		Options: ConversionOptions{OnDroppedTools: func(tools []DroppedTool) { dropped = tools }},
	})
	require.NoError(t, err)
	require.Equal(t, "/v1beta/models/gemini-4-argon:streamGenerateContent?alt=sse", encoded.Path)
	require.JSONEq(t, `{
		"contents":[{"role":"user","parts":[{"text":"Read the file."}]}],
		"generationConfig":{"maxOutputTokens":100},
		"tools":[{"functionDeclarations":[{"name":"read_file","description":"Read a file","parametersJsonSchema":{"type":"object","properties":{"path":{"type":"string"}}}}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}
	}`, string(encoded.Body))
	require.Equal(t, []DroppedTool{{Type: "custom", Name: "apply_patch", Reason: convert.DroppedToolReasonCrossProtocolIncompatible}}, dropped)
}

func TestGeminiClientToolSignatureRoundTrip(t *testing.T) {
	var upstreamBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1beta/models/gemini-tools:streamGenerateContent", r.URL.Path)
		require.Equal(t, "sse", r.URL.Query().Get("alt"))
		require.Equal(t, "upstream-key", r.Header.Get("x-goog-api-key"))
		require.Empty(t, r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: "+`{"candidates":[{"content":{"parts":[{"text":"working"},{"functionCall":{"name":"read","args":{"path":"a"}}}]}}]}`+"\n\n")
		io.WriteString(w, "data: "+`{"candidates":[{"content":{"parts":[{"thoughtSignature":"late-signature"}]},"finishReason":"STOP"}]}`+"\n\n")
		io.WriteString(w, "data: "+`{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":80,"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":115}}`+"\n\n")
	}))
	defer server.Close()
	codec := NewCodec()
	req, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: []byte(`{"model":"alias","stream":true,"messages":[{"role":"user","content":"read"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`)})
	require.NoError(t, err)
	events, err := NewClient(ClientOptions{Codec: codec}).Call(context.Background(), req.Request, Target{Protocol: ProtocolGemini, BaseURL: server.URL, Model: "gemini-tools", APIKey: "upstream-key"}, CallOptions{})
	require.NoError(t, err)
	chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: ProtocolOpenAIChat, Stream: true, Events: events})
	require.NoError(t, err)
	var output strings.Builder
	for c := range chunks {
		require.NoError(t, c.Err)
		output.Write(c.Data)
	}
	var id string
	for _, line := range strings.Split(output.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						ID string `json:"id"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) == nil && len(frame.Choices) > 0 && len(frame.Choices[0].Delta.ToolCalls) > 0 && frame.Choices[0].Delta.ToolCalls[0].ID != "" {
			id = frame.Choices[0].Delta.ToolCalls[0].ID
		}
	}
	require.Contains(t, id, "call_llmkit_v1_")
	next := map[string]any{"model": "alias", "messages": []any{map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": id, "type": "function", "function": map[string]any{"name": "read", "arguments": `{"path":"a"}`}}}}, map[string]any{"role": "tool", "tool_call_id": id, "content": "done"}}}
	wire, err := json.Marshal(next)
	require.NoError(t, err)
	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: wire})
	require.NoError(t, err)
	encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: "gemini-tools"}})
	require.NoError(t, err)
	var body struct {
		Contents []struct {
			Parts []struct {
				ThoughtSignature string         `json:"thoughtSignature"`
				FunctionResponse map[string]any `json:"functionResponse"`
			} `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(encoded.Body, &body))
	require.Equal(t, "late-signature", body.Contents[0].Parts[0].ThoughtSignature)
	require.Equal(t, "read", body.Contents[1].Parts[0].FunctionResponse["name"])
	require.NotContains(t, string(encoded.Body), "call_llmkit_")
}

func TestGeminiResponsesToolSignaturesAcrossEndpointModels(t *testing.T) {
	const (
		endpointOne  = "ep-first"
		endpointTwo  = "ep-second"
		signatureOne = "signature-from-first-endpoint"
		signatureTwo = "signature-from-second-endpoint"
	)

	packCallID := func(id, model, signature string) string {
		return convert.PackToolCallID(id, &ir.ToolCallProviderData{
			Protocol: ir.ProtocolGemini,
			Models:   []string{model},
			Fields:   map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"` + signature + `"`)},
		})
	}
	firstCallID := packCallID("call-first", endpointOne, signatureOne)
	secondCallID := packCallID("call-second", endpointTwo, signatureTwo)
	input := `{"model":"gemini-4-argon","input":[` +
		`{"type":"function_call","call_id":"` + firstCallID + `","name":"read_first","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"` + firstCallID + `","output":"first-result"},` +
		`{"type":"function_call","call_id":"` + secondCallID + `","name":"read_second","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"` + secondCallID + `","output":"second-result"}]}`

	for _, targetModel := range []string{endpointOne, endpointTwo} {
		t.Run(targetModel, func(t *testing.T) {
			codec := NewCodec()
			decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: []byte(input)})
			require.NoError(t, err)
			before, err := json.Marshal(decoded.Request)
			require.NoError(t, err)

			encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: targetModel}})
			require.NoError(t, err)
			after, err := json.Marshal(decoded.Request)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "encoding must not mutate the decoded request")

			var body struct {
				Contents []struct {
					Role  string `json:"role"`
					Parts []struct {
						Signature        string                     `json:"thoughtSignature"`
						FunctionCall     *struct{ ID, Name string } `json:"functionCall"`
						FunctionResponse *struct{ ID, Name string } `json:"functionResponse"`
					} `json:"parts"`
				} `json:"contents"`
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &body))
			callSignatures := map[string]string{}
			var callIDs, resultIDs []string
			for _, content := range body.Contents {
				for _, part := range content.Parts {
					if part.FunctionCall != nil {
						callIDs = append(callIDs, part.FunctionCall.ID)
						callSignatures[part.FunctionCall.ID] = part.Signature
					}
					if part.FunctionResponse != nil {
						resultIDs = append(resultIDs, part.FunctionResponse.ID)
					}
				}
			}
			require.ElementsMatch(t, []string{"call-first", "call-second"}, callIDs)
			require.ElementsMatch(t, []string{"call-first", "call-second"}, resultIDs)
			require.Equal(t, signatureOne, callSignatures["call-first"])
			require.Equal(t, signatureTwo, callSignatures["call-second"])
		})
	}
}

func TestGeminiClientImageEventsAndNativeInboundUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1beta/models/image:generateContent", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"image:"},{"inlineData":{"mimeType":"image/jpeg","data":"AA=="},"thoughtSignature":"sig"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`)
	}))
	defer server.Close()
	client := NewClient(ClientOptions{})
	events, err := client.Call(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "draw"}}}}}, Target{Protocol: ProtocolGemini, Model: "image", BaseURL: server.URL}, CallOptions{})
	require.NoError(t, err)
	var image *ContentBlock
	for event := range events {
		require.NotEqual(t, EventError, event.Type)
		if event.Type == EventContentBlock {
			image = event.ContentBlock
		}
	}
	require.NotNil(t, image)
	require.Equal(t, "image/jpeg", image.MimeType)
	require.Equal(t, []string{"image"}, image.ProviderData.Models)
	_, err = NewCodec().DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1beta/models/image:generateContent", Body: []byte(`{}`)})
	require.ErrorIs(t, err, ErrUnsupportedEndpoint)
}

func TestGeminiImagesAcrossOpenAIProtocols(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol Protocol
		path     string
	}{
		{"chat", ProtocolOpenAIChat, "/v1/chat/completions"},
		{"responses", ProtocolOpenAIResponses, "/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codec := NewCodec()
			request, err := codec.EncodeRequest(EncodeRequestInput{Request: Request{}, Target: Target{Protocol: ProtocolGemini, Model: "image"}})
			require.NoError(t, err)
			events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolGemini, State: request.State, Body: strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"你"},{"inlineData":{"mimeType":"image/png","data":"AA=="},"thoughtSignature":"one"},{"text":"好"},{"inlineData":{"mimeType":"image/webp","data":"BB=="},"thoughtSignature":"two"}]},"finishReason":"STOP"}]}`)})
			require.NoError(t, err)
			chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: tc.protocol, Events: events})
			require.NoError(t, err)
			var wire strings.Builder
			for chunk := range chunks {
				require.NoError(t, chunk.Err)
				wire.Write(chunk.Data)
			}
			var response map[string]any
			require.NoError(t, json.Unmarshal([]byte(wire.String()), &response))
			next := map[string]any{"model": "alias"}
			if tc.protocol == ProtocolOpenAIChat {
				message := response["choices"].([]any)[0].(map[string]any)["message"]
				next["messages"] = []any{message}
			} else {
				next["input"] = response["output"]
			}
			raw, err := json.Marshal(next)
			require.NoError(t, err)
			decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: tc.path, Body: raw})
			require.NoError(t, err)
			encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: "image"}})
			require.NoError(t, err)
			var gemini struct {
				Contents []struct {
					Parts []struct {
						Text       string         `json:"text"`
						InlineData map[string]any `json:"inlineData"`
						Signature  string         `json:"thoughtSignature"`
					} `json:"parts"`
				} `json:"contents"`
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &gemini))
			var sequence []string
			for _, message := range gemini.Contents {
				for _, part := range message.Parts {
					if part.InlineData != nil {
						sequence = append(sequence, part.Signature)
					} else {
						sequence = append(sequence, part.Text)
					}
				}
			}
			require.Equal(t, []string{"你", "one", "好", "two"}, sequence)
		})
	}
}

func TestGeminiThinkingClientProtocolMatrix(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolClaude} {
		for _, stream := range []bool{false, true} {
			t.Run(string(protocol)+"/"+map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
				codec := NewCodec()
				raw := `{"candidates":[{"content":{"parts":[{"text":"consider ","thought":true},{"text":"carefully","thought":true},{"text":"answer"}]},"finishReason":"STOP"}]}`
				input := raw
				if stream {
					input = "data: " + raw + "\n\n"
				}
				events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolGemini, Body: strings.NewReader(input), Stream: stream})
				require.NoError(t, err)
				output, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: protocol, Events: events, Stream: stream})
				require.NoError(t, err)
				var wire strings.Builder
				for chunk := range output {
					require.NoError(t, chunk.Err)
					wire.Write(chunk.Data)
				}
				var thought, text string
				if !stream {
					var object map[string]any
					require.NoError(t, json.Unmarshal([]byte(wire.String()), &object))
					switch protocol {
					case ProtocolOpenAIChat:
						message := object["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
						thought, _ = message["reasoning_content"].(string)
						text, _ = message["content"].(string)
					case ProtocolClaude:
						for _, value := range object["content"].([]any) {
							block := value.(map[string]any)
							if block["type"] == "thinking" {
								thought += block["thinking"].(string)
							} else if block["type"] == "text" {
								text += block["text"].(string)
							}
						}
					case ProtocolOpenAIResponses:
						for _, value := range object["output"].([]any) {
							item := value.(map[string]any)
							if item["type"] == "reasoning" {
								require.Empty(t, item["content"])
								for _, v := range item["summary"].([]any) {
									thought += v.(map[string]any)["text"].(string)
								}
							} else if item["type"] == "message" {
								for _, v := range item["content"].([]any) {
									text += v.(map[string]any)["text"].(string)
								}
							}
						}
					}
				} else {
					for _, line := range strings.Split(wire.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
							continue
						}
						var object map[string]any
						require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &object))
						switch protocol {
						case ProtocolOpenAIChat:
							choices, _ := object["choices"].([]any)
							if len(choices) > 0 {
								delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
								v, _ := delta["reasoning_content"].(string)
								thought += v
								v, _ = delta["content"].(string)
								text += v
							}
						case ProtocolClaude:
							if object["type"] == "content_block_delta" {
								delta := object["delta"].(map[string]any)
								v, _ := delta["thinking"].(string)
								thought += v
								v, _ = delta["text"].(string)
								text += v
							}
						case ProtocolOpenAIResponses:
							require.NotEqual(t, "response.reasoning_text.delta", object["type"])
							if object["type"] == "response.reasoning_summary_text.delta" {
								thought += object["delta"].(string)
							} else if object["type"] == "response.output_text.delta" {
								text += object["delta"].(string)
							}
						}
					}
				}
				require.Equal(t, "consider carefully", thought)
				require.Equal(t, "answer", text)
			})
		}
	}
}

func TestGeminiNamespacedFunctionsRestoreNames(t *testing.T) {
	codec := NewCodec()
	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: []byte(`{"model":"alias","input":"read","tools":[{"type":"namespace","name":"files","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]},{"type":"namespace","name":"network","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}]}`)})
	require.NoError(t, err)
	encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: "m"}})
	require.NoError(t, err)
	var body struct {
		Tools []struct {
			Declarations []struct {
				Name string `json:"name"`
			} `json:"functionDeclarations"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(encoded.Body, &body))
	require.Len(t, body.Tools[0].Declarations, 2)
	a, b := body.Tools[0].Declarations[0].Name, body.Tools[0].Declarations[1].Name
	require.NotEqual(t, a, b)
	raw, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": a, "args": map[string]any{}}}, map[string]any{"functionCall": map[string]any{"name": b, "args": map[string]any{}}}}}, "finishReason": "STOP"}}})
	events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolGemini, Body: strings.NewReader(string(raw)), State: encoded.State})
	require.NoError(t, err)
	var namespaces []string
	for event := range events {
		if event.Type == EventToolCallStart {
			require.Equal(t, "read", event.ToolCall.Name)
			namespaces = append(namespaces, event.ToolCall.Namespace)
		}
	}
	require.Equal(t, []string{"files", "network"}, namespaces)
}

func TestGeminiMixedPartsRoundTrip(t *testing.T) {
	for _, target := range []Protocol{ProtocolOpenAIChat, ProtocolOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(string(target)+"/"+map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
				codec := NewCodec()
				request, err := codec.EncodeRequest(EncodeRequestInput{Request: Request{}, Target: Target{Protocol: ProtocolGemini, Model: "m"}})
				require.NoError(t, err)
				raw := `{"candidates":[{"content":{"parts":[{"text":"summary","thought":true},{"text":"before"},{"functionCall":{"name":"read","args":{}},"thoughtSignature":"tool"},{"text":"middle"},{"inlineData":{"mimeType":"image/png","data":"AA=="},"thoughtSignature":"image"},{"text":"after"}]},"finishReason":"STOP"}]}`
				input := raw
				if stream {
					input = "data: " + raw + "\n\n"
				}
				events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolGemini, Body: strings.NewReader(input), Stream: stream, State: request.State})
				require.NoError(t, err)
				output, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: target, Events: events, Stream: stream})
				require.NoError(t, err)
				var wire strings.Builder
				for chunk := range output {
					require.NoError(t, chunk.Err)
					wire.Write(chunk.Data)
				}
				next := map[string]any{"model": "m"}
				path := "/v1/chat/completions"
				if !stream {
					var object map[string]any
					require.NoError(t, json.Unmarshal([]byte(wire.String()), &object))
					if target == ProtocolOpenAIChat {
						next["messages"] = []any{object["choices"].([]any)[0].(map[string]any)["message"]}
					} else {
						next["input"] = object["output"]
					}
				} else if target == ProtocolOpenAIChat {
					message := map[string]any{"role": "assistant"}
					var text, reason string
					var images []any
					call := map[string]any{"type": "function"}
					function := map[string]any{}
					args := ""
					for _, line := range strings.Split(wire.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
							continue
						}
						var object map[string]any
						require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &object))
						choices, _ := object["choices"].([]any)
						if len(choices) == 0 {
							continue
						}
						delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
						v, _ := delta["content"].(string)
						text += v
						v, _ = delta["reasoning_content"].(string)
						reason += v
						if values, ok := delta["images"].([]any); ok {
							images = append(images, values...)
						}
						if values, ok := delta["tool_calls"].([]any); ok {
							for _, value := range values {
								c := value.(map[string]any)
								if id, ok := c["id"]; ok {
									call["id"] = id
								}
								if f, ok := c["function"].(map[string]any); ok {
									if name, ok := f["name"]; ok {
										function["name"] = name
									}
									v, _ := f["arguments"].(string)
									args += v
								}
							}
						}
					}
					message["content"] = text
					message["reasoning_content"] = reason
					message["images"] = images
					function["arguments"] = args
					call["function"] = function
					message["tool_calls"] = []any{call}
					next["messages"] = []any{message}
				} else {
					for _, line := range strings.Split(wire.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var object map[string]any
						if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &object) == nil && object["type"] == "response.completed" {
							next["input"] = object["response"].(map[string]any)["output"]
						}
					}
				}
				if target == ProtocolOpenAIResponses {
					path = "/v1/responses"
				}
				rawNext, err := json.Marshal(next)
				require.NoError(t, err)
				decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: path, Body: rawNext})
				require.NoError(t, err)
				encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: "m"}})
				require.NoError(t, err)
				var body struct {
					Contents []struct {
						Parts []struct {
							Text      string         `json:"text"`
							Thought   bool           `json:"thought"`
							Call      map[string]any `json:"functionCall"`
							Image     map[string]any `json:"inlineData"`
							Signature string         `json:"thoughtSignature"`
						} `json:"parts"`
					} `json:"contents"`
				}
				require.NoError(t, json.Unmarshal(encoded.Body, &body))
				var sequence []string
				for _, m := range body.Contents {
					for _, p := range m.Parts {
						switch {
						case p.Thought:
							sequence = append(sequence, "thought:"+p.Text)
						case p.Call != nil:
							sequence = append(sequence, "call:"+p.Signature)
						case p.Image != nil:
							sequence = append(sequence, "image:"+p.Signature)
						default:
							sequence = append(sequence, p.Text)
						}
					}
				}
				require.Equal(t, []string{"thought:summary", "before", "call:tool", "middle", "image:image", "after"}, sequence, string(rawNext))
			})
		}
	}
}

func TestGeminiResponsesToolResultKeepsAssistantTurnTogether(t *testing.T) {
	codec := NewCodec()
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: Request{}, Target: Target{Protocol: ProtocolGemini, Model: "m"}})
			require.NoError(t, err)
			raw := `{"candidates":[{"content":{"parts":[{"text":"before"},{"functionCall":{"id":"read-1","name":"read","args":{"path":"file"}},"thoughtSignature":"sig"},{"text":"after"}]},"finishReason":"STOP"}]}`
			if stream {
				raw = "data: " + raw + "\n\n"
			}
			events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolGemini, Body: strings.NewReader(raw), Stream: stream, State: encoded.State})
			require.NoError(t, err)
			chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: ProtocolOpenAIResponses, Events: events, Stream: stream})
			require.NoError(t, err)
			var wire strings.Builder
			for c := range chunks {
				require.NoError(t, c.Err)
				wire.Write(c.Data)
			}
			var response map[string]any
			if stream {
				for _, line := range strings.Split(wire.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var frame map[string]any
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) == nil && frame["type"] == "response.completed" {
						response = frame["response"].(map[string]any)
					}
				}
			} else {
				require.NoError(t, json.Unmarshal([]byte(wire.String()), &response))
			}
			input := response["output"].([]any)
			var callID string
			for _, item := range input {
				m := item.(map[string]any)
				if m["type"] == "function_call" {
					callID = m["call_id"].(string)
				}
			}
			require.NotEmpty(t, callID)
			input = append(input, map[string]any{"type": "function_call_output", "call_id": callID, "output": "file body"})
			next, _ := json.Marshal(map[string]any{"model": "m", "input": input})
			decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: next})
			require.NoError(t, err)
			upstream, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: "m"}})
			require.NoError(t, err)
			var body struct {
				Contents []struct {
					Role  string           `json:"role"`
					Parts []map[string]any `json:"parts"`
				} `json:"contents"`
			}
			require.NoError(t, json.Unmarshal(upstream.Body, &body))
			require.Len(t, body.Contents, 2, string(upstream.Body))
			require.Equal(t, "model", body.Contents[0].Role)
			require.Len(t, body.Contents[0].Parts, 3)
			require.Equal(t, "before", body.Contents[0].Parts[0]["text"])
			require.Equal(t, "read", body.Contents[0].Parts[1]["functionCall"].(map[string]any)["name"])
			require.Equal(t, "sig", body.Contents[0].Parts[1]["thoughtSignature"])
			require.Equal(t, "after", body.Contents[0].Parts[2]["text"])
			require.Equal(t, "user", body.Contents[1].Role)
			require.Len(t, body.Contents[1].Parts, 1)
			require.Equal(t, "read", body.Contents[1].Parts[0]["functionResponse"].(map[string]any)["name"])
		})
	}
}

func TestGeminiReplaysGoogleChatToolSignature(t *testing.T) {
	codec := NewCodec()
	body := []byte(`{"model":"m","messages":[
		{"role":"assistant","tool_calls":[{"id":"native-id","type":"function","function":{"name":"read_powershell","arguments":"{}"},"extra_content":{"google":{"thought_signature":"opaque"},"other":{"value":true}}}]},
		{"role":"tool","tool_call_id":"native-id","content":"done"}]}`)
	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: http.MethodPost, Path: "/v1/chat/completions", Body: body})
	require.NoError(t, err)
	encoded, err := codec.EncodeRequest(EncodeRequestInput{
		Request: decoded.Request,
		Target:  Target{Protocol: ProtocolGemini, Model: "m"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"contents":[
		{"role":"model","parts":[{"functionCall":{"id":"native-id","name":"read_powershell","args":{}},"thoughtSignature":"opaque"}]},
		{"role":"user","parts":[{"functionResponse":{"id":"native-id","name":"read_powershell","response":{"result":"done"}}}]}]}`, string(encoded.Body))
}

func TestGeminiChatParallelToolRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "json"
		if stream {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			codec := NewCodec()
			target := Target{Protocol: ProtocolGemini, Model: "gemini-4-argon"}
			initial, err := codec.EncodeRequest(EncodeRequestInput{Target: target})
			require.NoError(t, err)
			reply := `{"candidates":[{"content":{"parts":[
				{"functionCall":{"name":"lookup","id":"vertex-id","args":{"city":"Paris"}},"thoughtSignature":"opaque"},
				{"functionCall":{"name":"lookup","args":{"city":"London"}}}
			]},"finishReason":"STOP"}]}`
			if stream {
				reply = "data: " + strings.ReplaceAll(reply, "\n", "") + "\n\n"
			}
			events, err := codec.DecodeResponse(ctx, DecodeResponseInput{
				Protocol: ProtocolGemini, State: initial.State, Body: strings.NewReader(reply), Stream: stream,
			})
			require.NoError(t, err)
			chunks, err := codec.EncodeResponse(ctx, EncodeResponseInput{Protocol: ProtocolOpenAIChat, Stream: stream, Events: events})
			require.NoError(t, err)

			var ids []string
			var finish string
			for chunk := range chunks {
				require.NoError(t, chunk.Err)
				payload := strings.TrimSpace(string(chunk.Data))
				if stream {
					payload = strings.TrimPrefix(payload, "data: ")
					if payload == "[DONE]" || payload == "" {
						continue
					}
				}
				type toolCall struct {
					ID    string `json:"id"`
					Extra struct {
						Google struct {
							Signature string `json:"thought_signature"`
						} `json:"google"`
					} `json:"extra_content"`
				}
				type message struct {
					Calls []toolCall `json:"tool_calls"`
				}
				var frame struct {
					Choices []struct {
						Message message `json:"message"`
						Delta   message `json:"delta"`
						Finish  string  `json:"finish_reason"`
					} `json:"choices"`
				}
				require.NoError(t, json.Unmarshal([]byte(payload), &frame))
				for _, choice := range frame.Choices {
					if choice.Finish != "" {
						finish = choice.Finish
					}
					for _, call := range append(choice.Message.Calls, choice.Delta.Calls...) {
						if call.ID == "" {
							continue
						}
						if len(ids) == 0 {
							require.Equal(t, "opaque", call.Extra.Google.Signature)
						} else {
							require.Empty(t, call.Extra.Google.Signature)
						}
						ids = append(ids, call.ID)
					}
				}
			}
			require.Equal(t, "tool_calls", finish)
			require.Len(t, ids, 2)
			require.NotEqual(t, ids[0], ids[1])

			// Replay only standard Chat fields with the tool results in completion order.
			request := Request{Messages: []Message{
				{Role: RoleAssistant, ToolCalls: []ToolCall{
					{ID: ids[0], Name: "lookup", Arguments: `{"city":"Paris"}`},
					{ID: ids[1], Name: "lookup", Arguments: `{"city":"London"}`},
				}},
				{Role: RoleTool, ToolCallID: ids[1], Content: []ContentBlock{{Type: ContentTypeText, Text: `["London",9007199254740993]`}}},
				{Role: RoleTool, ToolCallID: ids[0], Content: []ContentBlock{{Type: ContentTypeText, Text: `{"city":"Paris","id":9007199254740993}`}}},
			}}
			next, err := codec.EncodeRequest(EncodeRequestInput{Request: request, Target: target})
			require.NoError(t, err)
			require.JSONEq(t, `{"contents":[
				{"role":"model","parts":[
					{"functionCall":{"name":"lookup","id":"vertex-id","args":{"city":"Paris"}},"thoughtSignature":"opaque"},
					{"functionCall":{"name":"lookup","args":{"city":"London"}}}]},
				{"role":"user","parts":[
					{"functionResponse":{"name":"lookup","id":"vertex-id","response":{"city":"Paris","id":9007199254740993}}},
					{"functionResponse":{"name":"lookup","response":{"result":["London",9007199254740993]}}}]}]}`, string(next.Body))
			require.Equal(t, 2, strings.Count(string(next.Body), "9007199254740993"))
		})
	}
}

func TestGeminiChatGenerationExtensions(t *testing.T) {
	codec := NewCodec()
	for _, tc := range []struct {
		name, model, fields, generation string
		wantError                       bool
	}{
		{"Neon disabled", "models/gemini-4-neon", `"reasoning_effort":"none"`, `{"thinkingConfig":{"thinkingLevel":"LOW"}}`, false},
		{"new variant", "gemini-4-future-preview", `"reasoning_effort":"xhigh","n":1,"presence_penalty":0`, `{"thinkingConfig":{"thinkingLevel":"XHIGH"}}`, false},
		{"earlier model", "gemini-2.5-flash", `"reasoning_effort":"none","n":1`, `{"thinkingConfig":{"thinkingBudget":0},"candidateCount":1}`, false},
		{"multiple native candidates", "gemini-2.5-flash", `"n":1,"generationConfig":{"candidateCount":2}`, "", true},
		{"protobuf field names", "gemini-4-neon", `"generation_config":{"candidate_count":2}`, "", true},
		{"effective single candidate", "gemini-2.5-flash", `"n":2,"generationConfig":{"candidateCount":1}`, `{"candidateCount":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"messages":[{"role":"user","content":"hi"}],"modelArmorConfig":{"promptTemplateName":"templates/test"},"future_extension":{"value":17},` + tc.fields + `}`
			decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: []byte(body)})
			require.NoError(t, err)
			encoded, err := codec.EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: tc.model}})
			if tc.wantError {
				require.ErrorContains(t, err, "one candidate")
				return
			}
			require.NoError(t, err)
			var wire map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded.Body, &wire))
			require.JSONEq(t, tc.generation, string(wire["generationConfig"]))
			require.JSONEq(t, `{"promptTemplateName":"templates/test"}`, string(wire["modelArmorConfig"]))
			require.JSONEq(t, `{"value":17}`, string(wire["future_extension"]))
		})
	}
}

func TestGeminiToolFallbackAndChoice(t *testing.T) {
	const fileSearch = `{"type":"file_search","vector_store_ids":["vs_test"]}`
	const function = `{"type":"function","name":"lookup"}`
	const custom = `{"type":"custom","name":"edit"}`
	codec := NewCodec()
	for _, tc := range []struct {
		name                                           string
		policy                                         BuiltinToolFallbackPolicy
		tools, choice, wantTools, wantMode, dropReason string
		wantError                                      bool
	}{
		{"drop optional", BuiltinToolFallbackDrop, fileSearch, `"auto"`, "", "", DroppedToolReasonCrossProtocolIncompatible, false},
		{"drop required", BuiltinToolFallbackDrop, fileSearch, `"required"`, "", "", "", true},
		{"another tool satisfies required", BuiltinToolFallbackDrop, fileSearch + "," + function, `"required"`, `[{"functionDeclarations":[{"name":"lookup","description":""}]}]`, "ANY", DroppedToolReasonCrossProtocolIncompatible, false},
		{"dropped selected tool", BuiltinToolFallbackDrop, custom + "," + function, `{"type":"custom","name":"edit"}`, "", "", "", true},
		{"preserve raw tool", BuiltinToolFallbackPassthrough, fileSearch, `"auto"`, "[" + fileSearch + "]", "AUTO", "", false},
		{"error policy", BuiltinToolFallbackError, fileSearch, `"auto"`, "", "", "", true},
		{"function fallback unsupported", BuiltinToolFallbackFunction, fileSearch, `"none"`, "", "", DroppedToolReasonFunctionFallbackUnsupported, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := `{"input":"hi","tools":[` + tc.tools + `],"tool_choice":` + tc.choice + `}`
			decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: []byte(request)})
			require.NoError(t, err)
			var dropped []DroppedTool
			encoded, err := codec.EncodeRequest(EncodeRequestInput{
				Request: decoded.Request, Target: Target{Protocol: ProtocolGemini, Model: "m"},
				Options: ConversionOptions{BuiltinToolFallback: tc.policy, OnDroppedTools: func(tools []DroppedTool) { dropped = tools }},
			})
			if tc.wantError {
				require.ErrorIs(t, err, convert.ErrBuiltinToolUnsupported)
				return
			}
			require.NoError(t, err)
			var wire struct {
				Tools      json.RawMessage
				ToolConfig struct{ FunctionCallingConfig struct{ Mode string } }
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &wire))
			if tc.wantTools == "" {
				require.Empty(t, wire.Tools)
			} else {
				require.JSONEq(t, tc.wantTools, string(wire.Tools))
			}
			require.Equal(t, tc.wantMode, wire.ToolConfig.FunctionCallingConfig.Mode)
			if tc.dropReason != "" {
				require.Equal(t, []DroppedTool{{Type: "file_search", Reason: tc.dropReason}}, dropped)
			} else {
				require.Empty(t, dropped)
			}
		})
	}
}
