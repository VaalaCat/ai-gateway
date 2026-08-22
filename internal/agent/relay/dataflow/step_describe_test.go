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
}
