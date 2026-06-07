package plan

import "testing"

// stubFilter 是测试用可控 filter:Apply 直接返回预设结果，并记录是否被调用。
type stubFilter struct {
	name   string
	out    []ScoredCandidate
	code   DropCode
	called *bool
}

func (f stubFilter) Name() string { return f.name }

func (f stubFilter) Apply(_ *FilterContext, _ []ScoredCandidate) ([]ScoredCandidate, DropCode) {
	if f.called != nil {
		*f.called = true
	}
	return f.out, f.code
}

// TestRunFilters_EmptyWithCodeStopsPipeline:
// 第一个 filter 把候选收空且带 DropInsufficientQuota → 管道中断，
// 后续 filter 不被调用，runFilters 返回 (empty, DropInsufficientQuota)。
func TestRunFilters_EmptyWithCodeStopsPipeline(t *testing.T) {
	secondCalled := false
	emptying := stubFilter{name: "emptying", out: nil, code: DropInsufficientQuota}
	following := stubFilter{name: "following", out: []ScoredCandidate{{}}, code: DropNone, called: &secondCalled}

	in := []ScoredCandidate{{}, {}}
	got, code := runFilters(&FilterContext{}, in, []CandidateFilter{emptying, following})

	if len(got) != 0 {
		t.Errorf("kept len = %d, want 0", len(got))
	}
	if code != DropInsufficientQuota {
		t.Errorf("code = %v, want DropInsufficientQuota", code)
	}
	if secondCalled {
		t.Error("following filter must NOT be called after pipeline stops")
	}
}

// TestRunFilters_AllPassThrough: 没有 filter 收空时全程跑完，返回最后结果 + DropNone。
func TestRunFilters_AllPassThrough(t *testing.T) {
	firstCalled, secondCalled := false, false
	f1 := stubFilter{name: "f1", out: []ScoredCandidate{{}, {}}, code: DropNone, called: &firstCalled}
	f2 := stubFilter{name: "f2", out: []ScoredCandidate{{}}, code: DropNone, called: &secondCalled}

	in := []ScoredCandidate{{}, {}}
	got, code := runFilters(&FilterContext{}, in, []CandidateFilter{f1, f2})

	if !firstCalled || !secondCalled {
		t.Errorf("both filters must run: f1=%v f2=%v", firstCalled, secondCalled)
	}
	if len(got) != 1 {
		t.Errorf("kept len = %d, want 1 (last filter output)", len(got))
	}
	if code != DropNone {
		t.Errorf("code = %v, want DropNone", code)
	}
}

// TestRunFilters_EmptyWithoutCodeDoesNotStop:
// 边界:filter 把候选收空但 code==DropNone（无原因），管道不报错继续，
// 但因 len(cands)==0 后续 filter 被 short-circuit 跳过，最终返回 (empty, DropNone)。
func TestRunFilters_EmptyWithoutCodeDoesNotStop(t *testing.T) {
	secondCalled := false
	emptying := stubFilter{name: "emptying", out: nil, code: DropNone}
	following := stubFilter{name: "following", out: []ScoredCandidate{{}}, code: DropNone, called: &secondCalled}

	in := []ScoredCandidate{{}, {}}
	got, code := runFilters(&FilterContext{}, in, []CandidateFilter{emptying, following})

	if len(got) != 0 {
		t.Errorf("kept len = %d, want 0", len(got))
	}
	if code != DropNone {
		t.Errorf("code = %v, want DropNone (empty without reason → no error)", code)
	}
	if secondCalled {
		t.Error("following filter skipped once candidates emptied")
	}
}
