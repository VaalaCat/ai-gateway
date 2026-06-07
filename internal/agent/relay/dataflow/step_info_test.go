package dataflow

import "testing"

func TestAllStepInfos_OrderMatchesDefaultStepOrder(t *testing.T) {
	got := AllStepInfos()
	if len(got) != len(defaultStepOrder) {
		t.Fatalf("AllStepInfos len = %d, want %d", len(got), len(defaultStepOrder))
	}
	for i, key := range defaultStepOrder {
		if got[i].Key != key {
			t.Fatalf("AllStepInfos[%d].Key = %q, want %q", i, got[i].Key, key)
		}
		if got[i].Title == "" {
			t.Fatalf("AllStepInfos[%d] (%s) has empty Title", i, key)
		}
	}
}

func TestBaseStepInfos_CoversEveryDefaultStep(t *testing.T) {
	// 自动工序无配置页,ConfigRef 合法为空。
	nonConfigurable := map[string]bool{"forward_client_headers": true}
	for _, key := range defaultStepOrder {
		info, ok := baseStepInfos[key]
		if !ok {
			t.Fatalf("baseStepInfos missing key %q", key)
		}
		if info.Title == "" {
			t.Errorf("baseStepInfos[%q].Title is empty", key)
		}
		if info.ConfigRef == "" && !nonConfigurable[key] {
			t.Errorf("baseStepInfos[%q].ConfigRef is empty", key)
		}
	}
}
