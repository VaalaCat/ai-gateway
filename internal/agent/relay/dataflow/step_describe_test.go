package dataflow

import "testing"

func TestDescribe_DetailSummaries(t *testing.T) {
	mm := (&StepModelMapping{mapping: map[string]string{"a": "x", "b": "y"}}).Describe()
	if mm.Key != "model_mapping" || mm.Title != "模型映射" {
		t.Fatalf("model_mapping base wrong: %+v", mm)
	}
	if mm.Detail != "2" {
		t.Fatalf("model_mapping Detail = %q, want \"2\"", mm.Detail)
	}

	po := (&StepParamOverride{params: map[string]any{"top_p": 1, "temperature": 0.5}}).Describe()
	if po.Detail != "temperature,top_p" {
		t.Fatalf("param_override Detail = %q, want \"temperature,top_p\"", po.Detail)
	}

	ho := (&StepHeaderOverride{headers: map[string]any{"X-B": "1", "X-A": "2"}}).Describe()
	if ho.Detail != "X-A,X-B" {
		t.Fatalf("header_override Detail = %q, want \"X-A,X-B\"", ho.Detail)
	}

	en := (&StepEncode{proto: "openai_chat"}).Describe()
	if en.Detail != "openai_chat" {
		t.Fatalf("encode Detail = %q, want \"openai_chat\"", en.Detail)
	}

	sp := (&StepInjectSystemPrompt{prompt: "hi"}).Describe()
	if sp.Detail != "" {
		t.Fatalf("inject_system_prompt Detail = %q, want empty", sp.Detail)
	}
}

func TestDescribe_DetailBoundaries(t *testing.T) {
	// nil mapping → "0"(len(nil)==0)
	if d := (&StepModelMapping{mapping: nil}).Describe().Detail; d != "0" {
		t.Errorf("model_mapping nil Detail = %q, want \"0\"", d)
	}
	// nil rules → Detail 空(nil-guard 生效)
	if d := (&StepRoleMapping{rules: nil}).Describe().Detail; d != "" {
		t.Errorf("role_mapping nil Detail = %q, want empty", d)
	}
	// 空 params → joinSortedKeys 提前返回 ""
	if d := (&StepParamOverride{params: nil}).Describe().Detail; d != "" {
		t.Errorf("param_override empty Detail = %q, want empty", d)
	}
	// 空 headers → ""
	if d := (&StepHeaderOverride{headers: map[string]any{}}).Describe().Detail; d != "" {
		t.Errorf("header_override empty Detail = %q, want empty", d)
	}
}
