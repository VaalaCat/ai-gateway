package native

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

const (
	namespaceFixturePath = "testdata/regressions/codex_namespace_spawn_agent_responses_to_chat.json"
	namespaceChatName    = "multi_agent_v1__spawn_agent"
	namespaceCallID      = "call_spawn_agent"
	namespaceArguments   = `{"task":"inspect the gateway"}`
)

// behavior change: this fixture is the smallest shape from req-496f5fe7-58da-427c-8d2a-f606a63251a9.
// A Responses namespace function must reach a Chat-only upstream and then be
// reconstructed before the client sees either response form.
func TestNative_CodexNamespaceSpawnAgentResponsesToChat(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[stream], func(t *testing.T) {
			var captured map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read upstream request: %v", err)
				}
				if err := json.Unmarshal(raw, &captured); err != nil {
					t.Fatalf("decode upstream request: %v", err)
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(namespaceChatStreamResponse()))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(namespaceChatNonStreamResponse()))
			}))
			defer upstream.Close()

			channel := &models.Channel{
				ChannelCore: models.ChannelCore{
					ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1,
					SupportedAPITypes: `["chat-completion"]`,
				},
				Key: "key", Models: "gpt-5",
			}
			rctx, recorder := newNativeTestCtx(t, namespaceFixtureBody(t, stream), llmkit.ProtocolOpenAIResponses, stream)
			result := (&Backend{}).Relay(rctx, state.Attempt{Channel: channel, RealModel: "gpt-5"})
			if result.Err != nil {
				t.Fatalf("Relay: %v", result.Err)
			}
			assertNamespaceChatUpstreamRequest(t, captured)
			if stream {
				assertNamespaceResponsesStream(t, recorder.Body.String())
			} else {
				assertNamespaceResponsesBody(t, recorder.Body.Bytes())
			}
		})
	}
}

func TestNative_ResponsesNamespaceParallelStreamRestoresByCallID(t *testing.T) {
	var captured map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(namespaceParallelChatStreamResponse()))
	}))
	defer upstream.Close()

	channel := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1,
			SupportedAPITypes: `["chat-completion"]`,
		},
		Key: "key", Models: "gpt-5",
	}
	rctx, recorder := newNativeTestCtx(t, namespaceParallelFixtureBody(), llmkit.ProtocolOpenAIResponses, true)
	result := (&Backend{}).Relay(rctx, state.Attempt{Channel: channel, RealModel: "gpt-5"})
	if result.Err != nil {
		t.Fatalf("Relay: %v", result.Err)
	}
	assertParallelNamespaceChatUpstreamRequest(t, captured)
	assertParallelNamespaceResponsesStream(t, recorder.Body.String())
}

func namespaceFixtureBody(t *testing.T, stream bool) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(namespaceFixturePath))
	if err != nil {
		t.Fatalf("read namespace fixture: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode namespace fixture: %v", err)
	}
	request["stream"] = stream
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode namespace request: %v", err)
	}
	return encoded
}

func namespaceParallelFixtureBody() []byte {
	return []byte(`{
		"model":"gpt-5",
		"stream":true,
		"input":"delegate two tasks",
		"parallel_tool_calls":true,
		"tools":[
			{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent","description":"Delegate an agent","parameters":{"type":"object"}}]},
			{"type":"namespace","name":"shell_v1","tools":[{"type":"function","name":"spawn_agent","description":"Run a shell task","parameters":{"type":"object"}}]}
		]
	}`)
}

func assertNamespaceChatUpstreamRequest(t *testing.T, request map[string]any) {
	t.Helper()
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("Chat upstream tools = %#v, want one flattened spawn_agent", request["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("Chat upstream tool = %#v, want object", tools[0])
	}
	function, ok := tool["function"].(map[string]any)
	if !ok || function["name"] != namespaceChatName {
		t.Fatalf("Chat upstream function = %#v, want %q", function, namespaceChatName)
	}
	if strict, ok := function["strict"].(bool); !ok || strict {
		t.Errorf("Chat upstream strict = %#v, want false", function["strict"])
	}
	choice, ok := request["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("Chat upstream tool_choice = %#v, want named function", request["tool_choice"])
	}
	choiceFunction, ok := choice["function"].(map[string]any)
	if !ok || choiceFunction["name"] != namespaceChatName {
		t.Errorf("Chat upstream tool_choice = %#v, want %q", choice, namespaceChatName)
	}
}

func assertParallelNamespaceChatUpstreamRequest(t *testing.T, request map[string]any) {
	t.Helper()
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("Chat upstream tools = %#v, want two flattened namespace functions", request["tools"])
	}
	seen := make(map[string]bool, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("Chat upstream tool = %#v, want object", raw)
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("Chat upstream function = %#v, want object", tool["function"])
		}
		name, _ := function["name"].(string)
		seen[name] = true
	}
	for _, want := range []string{"multi_agent_v1__spawn_agent", "shell_v1__spawn_agent"} {
		if !seen[want] {
			t.Errorf("Chat upstream functions = %v, missing %q", seen, want)
		}
	}
}

func assertNamespaceResponsesBody(t *testing.T, raw []byte) {
	t.Helper()
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode client Responses body: %v\n%s", err, raw)
	}
	if len(response.Output) != 1 {
		t.Fatalf("client Responses output = %#v, want one function call", response.Output)
	}
	assertNamespaceResponseItem(t, response.Output[0].Type, response.Output[0].CallID, response.Output[0].Name, response.Output[0].Namespace, response.Output[0].Arguments, namespaceArguments)
}

func assertNamespaceResponsesStream(t *testing.T, raw string) {
	t.Helper()
	items := map[string]struct {
		Type      string
		CallID    string
		Name      string
		Namespace string
		Arguments string
	}{}
	var event string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") || (event != "response.output_item.added" && event != "response.output_item.done") {
			continue
		}
		var payload struct {
			Item struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Arguments string `json:"arguments"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatalf("decode %s: %v", event, err)
		}
		if payload.Item.Type == "function_call" {
			items[event] = struct {
				Type      string
				CallID    string
				Name      string
				Namespace string
				Arguments string
			}{payload.Item.Type, payload.Item.CallID, payload.Item.Name, payload.Item.Namespace, payload.Item.Arguments}
		}
	}
	for _, eventName := range []string{"response.output_item.added", "response.output_item.done"} {
		item, ok := items[eventName]
		if !ok {
			t.Fatalf("missing %s function_call in client stream:\n%s", eventName, raw)
		}
		wantArguments := ""
		if eventName == "response.output_item.done" {
			wantArguments = namespaceArguments
		}
		assertNamespaceResponseItem(t, item.Type, item.CallID, item.Name, item.Namespace, item.Arguments, wantArguments)
	}
}

func assertParallelNamespaceResponsesStream(t *testing.T, raw string) {
	t.Helper()
	type item struct {
		Type      string
		Name      string
		Namespace string
		Arguments string
	}
	items := map[string]map[string]item{}
	var event string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") || (event != "response.output_item.added" && event != "response.output_item.done") {
			continue
		}
		var payload struct {
			Item struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Arguments string `json:"arguments"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatalf("decode %s: %v", event, err)
		}
		if payload.Item.Type != "function_call" {
			continue
		}
		if items[payload.Item.CallID] == nil {
			items[payload.Item.CallID] = make(map[string]item)
		}
		items[payload.Item.CallID][event] = item{
			Type: payload.Item.Type, Name: payload.Item.Name, Namespace: payload.Item.Namespace, Arguments: payload.Item.Arguments,
		}
	}

	for callID, want := range map[string]item{
		"call_agent": {Type: "function_call", Name: "spawn_agent", Namespace: "multi_agent_v1", Arguments: `{"task":"inspect"}`},
		"call_shell": {Type: "function_call", Name: "spawn_agent", Namespace: "shell_v1", Arguments: `{"cmd":"pwd"}`},
	} {
		for _, eventName := range []string{"response.output_item.added", "response.output_item.done"} {
			got, ok := items[callID][eventName]
			if !ok {
				t.Fatalf("missing %s for %s in client stream:\n%s", eventName, callID, raw)
			}
			if got.Type != want.Type || got.Name != want.Name || got.Namespace != want.Namespace {
				t.Errorf("%s %s = %#v, want %#v", eventName, callID, got, want)
			}
			wantArguments := ""
			if eventName == "response.output_item.done" {
				wantArguments = want.Arguments
			}
			if got.Arguments != wantArguments {
				t.Errorf("%s %s arguments = %q, want %q", eventName, callID, got.Arguments, wantArguments)
			}
		}
	}
}

func assertNamespaceResponseItem(t *testing.T, typ, callID, name, namespace, arguments, wantArguments string) {
	t.Helper()
	if typ != "function_call" || callID != namespaceCallID || name != "spawn_agent" || namespace != "multi_agent_v1" || arguments != wantArguments {
		t.Errorf("Responses function call = type=%q call_id=%q name=%q namespace=%q arguments=%q", typ, callID, name, namespace, arguments)
	}
}

func namespaceChatNonStreamResponse() string {
	return `{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"` + namespaceCallID + `","type":"function","function":{"name":"` + namespaceChatName + `","arguments":` + marshalJSONString(namespaceArguments) + `}}]},"finish_reason":"tool_calls"}]}`
}

func namespaceChatStreamResponse() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"` + namespaceCallID + `","type":"function","function":{"name":"` + namespaceChatName + `","arguments":` + marshalJSONString(namespaceArguments) + `}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
}

func namespaceParallelChatStreamResponse() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl_parallel","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[` +
			`{"index":1,"id":"call_shell","type":"function","function":{"name":"shell_v1__spawn_agent","arguments":` + marshalJSONString(`{"cmd":"`) + `}},` +
			`{"index":0,"id":"call_agent","type":"function","function":{"name":"multi_agent_v1__spawn_agent","arguments":` + marshalJSONString(`{"task":"`) + `}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_parallel","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[` +
			`{"index":0,"function":{"arguments":` + marshalJSONString(`inspect"}`) + `}},` +
			`{"index":1,"function":{"arguments":` + marshalJSONString(`pwd"}`) + `}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_parallel","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
}

func marshalJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
