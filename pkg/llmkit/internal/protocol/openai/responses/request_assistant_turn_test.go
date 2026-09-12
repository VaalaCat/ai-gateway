package responses

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

func decodeResponsesRequest(t *testing.T, body string) *ir.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	decoded, err := (&handler{}).decodeHTTPRequest(request)
	require.NoError(t, err)
	return decoded
}

func TestResponsesDecodeRequestAggregatesCompleteAssistantTurn(t *testing.T) {
	decoded := decodeResponsesRequest(t, `{
      "model":"deepseek-reasoner",
      "input":[
        {"type":"message","role":"user","content":"Inspect files"},
        {"type":"reasoning","summary":[{"type":"summary_text","text":"Need inspect files."}],"content":[{"type":"reasoning_text","text":"Need inspect files."}],"encrypted_content":"enc"},
        {"type":"message","role":"assistant","content":[{"type":"output_text","text":"I will inspect."}]},
        {"type":"function_call","call_id":"call_a","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
        {"type":"function_call","call_id":"call_b","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},
        {"type":"function_call_output","call_id":"call_a","output":"file.txt"}
      ]
    }`)

	require.Len(t, decoded.Messages, 3)
	assistant := decoded.Messages[1]
	require.Equal(t, ir.RoleAssistant, assistant.Role)
	require.Len(t, assistant.Content, 2)
	require.Equal(t, ir.ContentTypeThinking, assistant.Content[0].Type)
	require.Equal(t, "Need inspect files.", assistant.Content[0].Text)
	require.Equal(t, "I will inspect.", assistant.Content[1].Text)
	require.Equal(t, []string{"call_a", "call_b"}, []string{assistant.ToolCalls[0].ID, assistant.ToolCalls[1].ID})
	require.Equal(t, ir.RoleTool, decoded.Messages[2].Role)
}

func TestResponsesDecodeRequestFunctionOutputEndsAssistantTurn(t *testing.T) {
	decoded := decodeResponsesRequest(t, `{
      "input":[
        {"type":"reasoning","content":[{"type":"reasoning_text","text":"first"}]},
        {"type":"function_call","call_id":"call_a","name":"lookup","arguments":"{}"},
        {"type":"function_call_output","call_id":"call_a","output":"ok"},
        {"type":"reasoning","content":[{"type":"reasoning_text","text":"second"}]},
        {"type":"message","role":"assistant","content":"done"}
      ]
    }`)
	require.Len(t, decoded.Messages, 3)
	require.Equal(t, "first", decoded.Messages[0].Content[0].Text)
	require.Equal(t, ir.RoleTool, decoded.Messages[1].Role)
	require.Equal(t, "second", decoded.Messages[2].Content[0].Text)
	require.Equal(t, "done", decoded.Messages[2].Content[1].Text)
}

func TestResponsesDecodeRequestSecondReasoningStartsNewTurn(t *testing.T) {
	decoded := decodeResponsesRequest(t, `{
      "input":[
        {"type":"reasoning","content":[{"type":"reasoning_text","text":"first"}]},
        {"type":"reasoning","content":[{"type":"reasoning_text","text":"second"}]}
      ]
    }`)
	require.Len(t, decoded.Messages, 2)
	require.Equal(t, "first", decoded.Messages[0].Content[0].Text)
	require.Equal(t, "second", decoded.Messages[1].Content[0].Text)
}

func TestResponsesDecodeRequestSecondAssistantMessageStartsNewTurn(t *testing.T) {
	decoded := decodeResponsesRequest(t, `{
      "input":[
        {"type":"message","role":"assistant","content":"first"},
        {"type":"message","role":"assistant","content":"second"}
      ]
    }`)
	require.Len(t, decoded.Messages, 2)
	require.Equal(t, "first", decoded.Messages[0].Content[0].Text)
	require.Equal(t, "second", decoded.Messages[1].Content[0].Text)
}

func TestResponsesDecodeRequestUnknownItemFlushesPendingTurn(t *testing.T) {
	decoded := decodeResponsesRequest(t, `{
      "input":[
        {"type":"function_call","call_id":"call_a","name":"lookup","arguments":"{}"},
        {"type":"future_item","value":1},
        {"type":"message","role":"assistant","content":"after"}
      ]
    }`)
	require.Len(t, decoded.Messages, 3)
	require.Equal(t, "call_a", decoded.Messages[0].ToolCalls[0].ID)
	require.Empty(t, decoded.Messages[1].Role)
	require.JSONEq(t, `{"type":"future_item","value":1}`, string(decoded.Messages[1].RawJSON))
	require.Equal(t, "after", decoded.Messages[2].Content[0].Text)
}
