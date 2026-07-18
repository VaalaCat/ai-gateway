package state

import "testing"

// TestPhaseValues：Phase 是 4 个递增的常量，PhaseNone=0 是隐含零值，
// 后续阶段必须严格递增（重要：if rctx.State.FailPhase >= PhasePlan 这种比较依赖这个不变量）。
func TestPhaseValues(t *testing.T) {
	if PhaseNone != 0 {
		t.Fatalf("PhaseNone should be 0, got %d", PhaseNone)
	}
	if PhaseCtxBuild <= PhaseNone || PhasePlan <= PhaseCtxBuild || PhaseExecute <= PhasePlan {
		t.Fatalf("phases not strictly increasing: None=%d Ctx=%d Plan=%d Exec=%d",
			PhaseNone, PhaseCtxBuild, PhasePlan, PhaseExecute)
	}
}

// TestRelayContextZeroValueOK：RelayInput 是值类型，零值必须可读不 panic。
func TestRelayContextZeroValueOK(t *testing.T) {
	rctx := &RelayContext{State: &RelayState{}}
	if rctx.Input.Model != "" {
		t.Fatalf("zero Input.Model should be empty, got %q", rctx.Input.Model)
	}
	if rctx.State.FailPhase != PhaseNone {
		t.Fatalf("zero State.FailPhase should be PhaseNone, got %d", rctx.State.FailPhase)
	}
	if rctx.State.Err != nil {
		t.Fatal("zero State.Err should be nil")
	}
}

// TestRelayContextNilStateUnsafe：边界 — State 是 *指针*，
// 不主动初始化时为 nil（不能误以为零值 RelayState 已被填）。
func TestRelayContextNilStateUnsafe(t *testing.T) {
	rctx := &RelayContext{}
	if rctx.State != nil {
		t.Fatalf("State should be nil-zero by default, got %#v", rctx.State)
	}
}

// TestRelayInputFieldsAssign：所有字段可赋值并读回。
func TestRelayInputFieldsAssign(t *testing.T) {
	in := RelayInput{
		RequestID:       "req-123",
		Model:           "gpt-4o",
		IsStream:        true,
		ForcedChannelID: 42,
	}
	if in.RequestID != "req-123" || in.Model != "gpt-4o" || !in.IsStream || in.ForcedChannelID != 42 {
		t.Fatalf("field round-trip failed: %#v", in)
	}
}
