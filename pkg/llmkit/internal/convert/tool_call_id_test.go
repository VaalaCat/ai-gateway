package convert

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

// decodeJSONNumber 用 UseNumber 解码，保证大整数不经过 float64。
func decodeJSONNumber(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// sampleProviderData 返回一份带大整数和未知命名空间的 ProviderData。
func sampleProviderData() *ir.ToolCallProviderData {
	return &ir.ToolCallProviderData{
		Protocol: ir.ProtocolOpenAIChat,
		Models:   []string{"gemini-3-pro-preview", "gemini-3-pro"},
		Fields: map[string]json.RawMessage{
			"extra_content": json.RawMessage(`{"google":{"thought_signature":"sig"},"x":{"n":9007199254740993}}`),
			"signature":     json.RawMessage(`"call-sig"`),
		},
	}
}

func brokenHashID(original string) string {
	sum := sha256.Sum256([]byte(original))
	return "call_" + hex.EncodeToString(sum[:])[:24]
}

func packedFromJSON(payload string) string {
	return toolCallIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestIsPackedToolCallID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"current version package", PackToolCallID("call_1", sampleProviderData()), true},
		{"other version package", "call_llmkit_v2_abc", true},
		{"broken package", "call_llmkit_garbage", true},
		{"plain call id", "call_abc", false},
		{"empty id", "", false},
		{"prefix in the middle", "x_call_llmkit_v1_abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsPackedToolCallID(tt.id))
		})
	}
}

func TestToolCallIDPackUnpack(t *testing.T) {
	urlSafe := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

	t.Run("round trip keeps id models and fields", func(t *testing.T) {
		data := sampleProviderData()
		packed := PackToolCallID("call_1", data)

		if !strings.HasPrefix(packed, "call_llmkit_v1_") {
			t.Fatalf("packed = %q, want call_llmkit_v1_ prefix", packed)
		}
		if !urlSafe.MatchString(packed) {
			t.Fatalf("packed = %q, want only [A-Za-z0-9_-]", packed)
		}

		rawID, got := UnpackToolCallID(packed)
		if rawID != "call_1" {
			t.Fatalf("rawID = %q, want call_1", rawID)
		}
		if got == nil {
			t.Fatal("ProviderData = nil, want value")
		}
		if got.Protocol != ir.ProtocolOpenAIChat {
			t.Errorf("Protocol = %q, want %q", got.Protocol, ir.ProtocolOpenAIChat)
		}
		if !reflect.DeepEqual(got.Models, data.Models) {
			t.Errorf("Models = %v, want %v", got.Models, data.Models)
		}
		if len(got.Fields) != len(data.Fields) {
			t.Fatalf("Fields keys = %d, want %d", len(got.Fields), len(data.Fields))
		}
		for key, want := range data.Fields {
			gotField, ok := got.Fields[key]
			if !ok {
				t.Fatalf("Fields[%q] missing", key)
			}
			if !reflect.DeepEqual(decodeJSONNumber(t, gotField), decodeJSONNumber(t, want)) {
				t.Errorf("Fields[%q] = %s, want JSON-equal to %s", key, gotField, want)
			}
		}
		if !strings.Contains(string(got.Fields["extra_content"]), "9007199254740993") {
			t.Errorf("unpacked extra_content = %s, want 9007199254740993 kept verbatim", got.Fields["extra_content"])
		}

		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(packed, toolCallIDPrefix))
		if err != nil {
			t.Fatalf("decode packed payload: %v", err)
		}
		if !strings.Contains(string(payload), "9007199254740993") {
			t.Errorf("packed JSON = %s, want 9007199254740993 kept verbatim", payload)
		}
	})

	tests := []struct {
		name       string
		input      string
		wantID     string
		wantNilDat bool
	}{
		{name: "plain id passes through", input: "call_plain", wantID: "call_plain", wantNilDat: true},
		{name: "empty id passes through", input: "", wantID: "", wantNilDat: true},
		{name: "legacy format without protocol returns its id", input: packedFromJSON(`{"id":"call_x","signature":"s"}`), wantID: "call_x", wantNilDat: true},
		{name: "legacy format with message signature returns its id", input: packedFromJSON(`{"id":"call_y","signature":"s","message_signature":"m"}`), wantID: "call_y", wantNilDat: true},
		{name: "invalid base64 becomes hash id", input: "call_llmkit_v1_!!!", wantID: brokenHashID("call_llmkit_v1_!!!"), wantNilDat: true},
		{name: "empty payload becomes hash id", input: "call_llmkit_v1_", wantID: brokenHashID("call_llmkit_v1_"), wantNilDat: true},
		{name: "non json payload becomes hash id", input: packedFromJSON("not json"), wantID: brokenHashID(packedFromJSON("not json")), wantNilDat: true},
		{name: "json without id becomes hash id", input: packedFromJSON(`{"protocol":"openai_chat","fields":{}}`), wantID: brokenHashID(packedFromJSON(`{"protocol":"openai_chat","fields":{}}`)), wantNilDat: true},
		{name: "other call_llmkit_ version becomes hash id", input: "call_llmkit_meta_abc", wantID: brokenHashID("call_llmkit_meta_abc"), wantNilDat: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotData := UnpackToolCallID(tt.input)
			if gotID != tt.wantID {
				t.Errorf("rawID = %q, want %q", gotID, tt.wantID)
			}
			if tt.wantNilDat && gotData != nil {
				t.Errorf("ProviderData = %+v, want nil", gotData)
			}

			againID, againData := UnpackToolCallID(tt.input)
			if againID != gotID || (againData == nil) != (gotData == nil) {
				t.Errorf("second unpack = (%q, %v), want stable (%q, %v)", againID, againData, gotID, gotData)
			}
		})
	}

	t.Run("hash id has call_ prefix and 24 hex chars", func(t *testing.T) {
		gotID, _ := UnpackToolCallID("call_llmkit_v1_!!!")
		if !regexp.MustCompile(`^call_[0-9a-f]{24}$`).MatchString(gotID) {
			t.Errorf("rawID = %q, want call_ + 24 hex chars", gotID)
		}
	})

	t.Run("nil provider data keeps original id", func(t *testing.T) {
		if got := PackToolCallID("call_1", nil); got != "call_1" {
			t.Errorf("PackToolCallID(call_1, nil) = %q, want call_1", got)
		}
	})
}

func TestCloneToolCallProviderData(t *testing.T) {
	tests := []struct {
		name  string
		input *ir.ToolCallProviderData
	}{
		{name: "nil stays nil", input: nil},
		{name: "empty stays empty", input: &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat}},
		{name: "full value", input: sampleProviderData()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CloneToolCallProviderData(tt.input)
			if tt.input == nil {
				if got != nil {
					t.Fatalf("clone = %+v, want nil", got)
				}
				return
			}
			if got == tt.input {
				t.Fatal("clone shares the original pointer")
			}
			if !reflect.DeepEqual(got, tt.input) {
				t.Fatalf("clone = %+v, want equal to %+v", got, tt.input)
			}
		})
	}

	t.Run("mutating the clone leaves the original intact", func(t *testing.T) {
		original := sampleProviderData()
		clone := CloneToolCallProviderData(original)

		clone.Models[0] = "changed"
		clone.Models = append(clone.Models, "extra")
		clone.Fields["signature"][1] = 'X'
		clone.Fields["added"] = json.RawMessage(`1`)
		delete(clone.Fields, "extra_content")

		want := sampleProviderData()
		if !reflect.DeepEqual(original, want) {
			t.Errorf("original = %+v, want unchanged %+v", original, want)
		}
	})
}

func TestUnpackRequestToolCallIDs(t *testing.T) {
	native := &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Fields: map[string]json.RawMessage{"signature": json.RawMessage(`"native"`)}}
	packed := PackToolCallID("call_1", sampleProviderData())
	tests := []struct {
		name       string
		call       ir.ToolCall
		resultID   string
		wantCall   ir.ToolCall
		wantResult string
	}{
		{
			name:       "package wins over native fields",
			call:       ir.ToolCall{ID: packed, Name: "read", ProviderData: native},
			resultID:   packed,
			wantCall:   ir.ToolCall{ID: "call_1", Name: "read", ProviderData: sampleProviderData()},
			wantResult: "call_1",
		},
		{
			name:       "plain id keeps native fields",
			call:       ir.ToolCall{ID: "call_1", Name: "read", ProviderData: native},
			resultID:   "call_1",
			wantCall:   ir.ToolCall{ID: "call_1", Name: "read", ProviderData: native},
			wantResult: "call_1",
		},
		{
			name:       "broken package becomes hash id for call and result",
			call:       ir.ToolCall{ID: "call_llmkit_v1_!!!", Name: "read"},
			resultID:   "call_llmkit_v1_!!!",
			wantCall:   ir.ToolCall{ID: brokenHashID("call_llmkit_v1_!!!"), Name: "read"},
			wantResult: brokenHashID("call_llmkit_v1_!!!"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &ir.Request{Messages: []ir.Message{
				{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{tt.call}},
				{Role: ir.RoleTool, ToolCallID: tt.resultID},
			}}
			UnpackRequestToolCallIDs(request)
			require.Equal(t, tt.wantCall, request.Messages[0].ToolCalls[0])
			require.Equal(t, tt.wantResult, request.Messages[1].ToolCallID)
		})
	}
}

func TestUnpackToolCallEvents(t *testing.T) {
	native := func() *ir.ToolCallProviderData {
		return &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Fields: map[string]json.RawMessage{"signature": json.RawMessage(`"s"`)}}
	}
	withModels := func(models ...string) *ir.ToolCallProviderData {
		data := native()
		data.Models = models
		return data
	}
	packed := PackToolCallID("call_1", withModels("m3"))
	streaming := func(eventType ir.EventType, id string, data *ir.ToolCallProviderData) ir.Event {
		return ir.Event{Type: eventType, ToolCall: &ir.StreamingToolCall{CallID: id, Name: "read", ProviderData: data}}
	}
	delta := func(id string, data *ir.ToolCallProviderData) ir.Event {
		return ir.Event{Type: ir.EventToolCallDelta, Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{ID: id, Name: "read", ProviderData: data}}}
	}
	tests := []struct {
		name        string
		targetModel string
		events      []ir.Event
		want        []ir.Event
	}{
		{
			name:        "native fields get this layer's model",
			targetModel: "m2",
			events:      []ir.Event{streaming(ir.EventToolCallStart, "call_1", native()), streaming(ir.EventToolCallEnd, "call_1", native()), delta("call_2", native())},
			want:        []ir.Event{streaming(ir.EventToolCallStart, "call_1", withModels("m2")), streaming(ir.EventToolCallEnd, "call_1", withModels("m2")), delta("call_2", withModels("m2"))},
		},
		{
			name:   "no target model keeps models empty",
			events: []ir.Event{streaming(ir.EventToolCallEnd, "call_1", native())},
			want:   []ir.Event{streaming(ir.EventToolCallEnd, "call_1", native())},
		},
		{
			name:        "package from the next layer",
			targetModel: "m2",
			events:      []ir.Event{streaming(ir.EventToolCallStart, packed, nil), streaming(ir.EventToolCallArgumentsDelta, packed, nil), streaming(ir.EventToolCallEnd, packed, nil), delta(packed, nil)},
			want: []ir.Event{
				streaming(ir.EventToolCallStart, "call_1", withModels("m3", "m2")), streaming(ir.EventToolCallArgumentsDelta, "call_1", nil),
				streaming(ir.EventToolCallEnd, "call_1", withModels("m3", "m2")), delta("call_1", withModels("m3", "m2")),
			},
		},
		{
			name:        "no provider data and other events unchanged",
			targetModel: "m2",
			events:      []ir.Event{{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "hi"}}, streaming(ir.EventToolCallStart, "call_1", nil), {Type: ir.EventDone}},
			want:        []ir.Event{{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "hi"}}, streaming(ir.EventToolCallStart, "call_1", nil), {Type: ir.EventDone}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneEvents(tt.events)
			got := collectEvents(UnpackToolCallEvents(context.Background(), eventChannel(tt.events...), tt.targetModel))
			require.Equal(t, tt.want, got)
			require.Equal(t, before, tt.events, "input events must not be mutated")
		})
	}
}

func TestPackToolCallEvents(t *testing.T) {
	partial := &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Models: []string{"m"}, Fields: map[string]json.RawMessage{"signature": json.RawMessage(`"early"`)}}
	full := &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Models: []string{"m"}, Fields: map[string]json.RawMessage{"signature": json.RawMessage(`"early"`), "message_signature": json.RawMessage(`"late"`)}}
	streaming := func(eventType ir.EventType, id string, data *ir.ToolCallProviderData) ir.Event {
		return ir.Event{Type: eventType, ToolCall: &ir.StreamingToolCall{CallID: id, Name: "read", ProviderData: data}}
	}
	delta := func(id string, data *ir.ToolCallProviderData) ir.Event {
		return ir.Event{Type: ir.EventToolCallDelta, Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{ID: id, Name: "read", ProviderData: data}}}
	}
	early := PackToolCallID("call_1", partial)
	tests := []struct {
		name   string
		events []ir.Event
		want   []ir.Event
	}{
		{
			name:   "package fixed at start",
			events: []ir.Event{streaming(ir.EventToolCallStart, "call_1", partial), streaming(ir.EventToolCallArgumentsDelta, "call_1", nil), streaming(ir.EventToolCallEnd, "call_1", full)},
			want:   []ir.Event{streaming(ir.EventToolCallStart, early, nil), streaming(ir.EventToolCallArgumentsDelta, early, nil), streaming(ir.EventToolCallEnd, early, nil)},
		},
		{
			name:   "non-stream delta packed directly",
			events: []ir.Event{delta("call_2", full)},
			want:   []ir.Event{delta(PackToolCallID("call_2", full), nil)},
		},
		{
			name:   "no provider data keeps raw id",
			events: []ir.Event{streaming(ir.EventToolCallStart, "call_3", nil), streaming(ir.EventToolCallEnd, "call_3", nil), delta("call_4", nil), {Type: ir.EventDone}},
			want:   []ir.Event{streaming(ir.EventToolCallStart, "call_3", nil), streaming(ir.EventToolCallEnd, "call_3", nil), delta("call_4", nil), {Type: ir.EventDone}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneEvents(tt.events)
			got := collectEvents(PackToolCallEvents(context.Background(), eventChannel(tt.events...)))
			require.Equal(t, tt.want, got)
			require.Equal(t, before, tt.events, "input events must not be mutated")
		})
	}
}

func TestToolCallEventsCancellation(t *testing.T) {
	adapters := map[string]func(context.Context, <-chan ir.Event) <-chan ir.Event{
		"unpack": func(ctx context.Context, events <-chan ir.Event) <-chan ir.Event {
			return UnpackToolCallEvents(ctx, events, "m")
		},
		"pack": PackToolCallEvents,
	}
	for name, adapt := range adapters {
		t.Run(name+"/nil input", func(t *testing.T) {
			require.Nil(t, adapt(context.Background(), nil))
		})
		t.Run(name+"/events after cancel still reach a reading consumer", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			source := make(chan ir.Event)
			out := adapt(ctx, source)
			cancel()
			go func() {
				source <- ir.Event{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningInterrupted}
				close(source)
			}()
			got := collectEvents(out)
			require.Equal(t, []ir.Event{{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningInterrupted}}, got)
		})
		t.Run(name+"/stalled consumer after cancel closes output", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			source := make(chan ir.Event)
			out := adapt(ctx, source)
			go func() {
				defer close(source)
				for range 200 {
					source <- ir.Event{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "x"}}
				}
			}()
			require.Eventually(t, func() bool { return len(out) == cap(out) }, time.Second, time.Millisecond)
			cancel()
			received := 0
			timeout := time.After(time.Second)
			for {
				select {
				case _, ok := <-out:
					if !ok {
						require.Less(t, received, 200)
						return
					}
					received++
				case <-timeout:
					t.Fatal("output did not close after cancellation")
				}
			}
		})
	}
}

// cloneEvents 深拷贝事件里的工具调用部分，用来确认输入没被改动。
func cloneEvents(events []ir.Event) []ir.Event {
	cloned := make([]ir.Event, len(events))
	for index, event := range events {
		cloned[index] = event
		if event.ToolCall != nil {
			call := *event.ToolCall
			call.ProviderData = CloneToolCallProviderData(call.ProviderData)
			cloned[index].ToolCall = &call
		}
		if event.Delta != nil && event.Delta.ToolCall != nil {
			delta := *event.Delta
			call := *delta.ToolCall
			call.ProviderData = CloneToolCallProviderData(call.ProviderData)
			delta.ToolCall = &call
			cloned[index].Delta = &delta
		}
	}
	return cloned
}
