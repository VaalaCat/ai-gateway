package publish

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// newByokRctx 构造一个 projectExecution 测试用的最小 RelayContext。
func newByokRctx(attempt state.Attempt) *state.RelayContext {
	rctx := newPublishTestRctx()
	rctx.State.Recorder = trace.NewRecorder(false, 0)
	rctx.State.Execution = state.ExecutionResult{
		Used:    attempt,
		Outcome: state.AttemptResult{},
	}
	return rctx
}

// TestFillExecution_AdminSource 验证 Source=SourceAdmin 时 ChannelID=SourceID, OwnerType="admin"。
func TestFillExecution_AdminSource(t *testing.T) {
	attempt := state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 17, Name: "admin-ch", Type: 1}},
		Source:    state.SourceAdmin,
		SourceID:  17,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if e.ChannelID != 17 {
		t.Errorf("admin: ChannelID = %d, want 17", e.ChannelID)
	}
	if e.PrivateChannelID != 0 {
		t.Errorf("admin: PrivateChannelID = %d, want 0", e.PrivateChannelID)
	}
	if e.OwnerType != "admin" {
		t.Errorf("admin: OwnerType = %q, want \"admin\"", e.OwnerType)
	}
}

// TestFillExecution_PrivateSource 验证 Source=SourcePrivate 时 PrivateChannelID=SourceID,
// ChannelID=0, OwnerType="private"。
func TestFillExecution_PrivateSource(t *testing.T) {
	attempt := state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 99, Name: "byok-ch", Type: 1}},
		Source:    state.SourcePrivate,
		SourceID:  99,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if e.PrivateChannelID != 99 {
		t.Errorf("private: PrivateChannelID = %d, want 99", e.PrivateChannelID)
	}
	if e.ChannelID != 0 {
		t.Errorf("private: ChannelID = %d, want 0", e.ChannelID)
	}
	if e.OwnerType != "private" {
		t.Errorf("private: OwnerType = %q, want \"private\"", e.OwnerType)
	}
}

// TestFillExecution_FallbackAdminWhenSourceEmpty 验证 Source="" (零值，pre-Task-12 兼容路径)
// 时退化为 admin 路径，ChannelID 取 Channel.ID。
func TestFillExecution_FallbackAdminWhenSourceEmpty(t *testing.T) {
	attempt := state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 5, Name: "legacy-ch", Type: 1}},
		Source:    "", // zero value — pre-Task-12 callsite
		SourceID:  0,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if e.ChannelID != 5 {
		t.Errorf("fallback: ChannelID = %d, want 5", e.ChannelID)
	}
	if e.PrivateChannelID != 0 {
		t.Errorf("fallback: PrivateChannelID = %d, want 0", e.PrivateChannelID)
	}
	if e.OwnerType != "admin" {
		t.Errorf("fallback: OwnerType = %q, want \"admin\"", e.OwnerType)
	}
}

// TestFillExecution_AdminSource_SnapshotsPriceRatio 验证公共 channel 的 price_ratio
// 在 relay 选中时被快照进 entry。
func TestFillExecution_AdminSource_SnapshotsPriceRatio(t *testing.T) {
	attempt := state.Attempt{
		Channel: &models.Channel{
			ChannelCore: models.ChannelCore{ID: 17, Name: "admin-ch", Type: 1},
			PriceRatio:  0.5,
		},
		Source:    state.SourceAdmin,
		SourceID:  17,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if e.PriceRatio != 0.5 {
		t.Errorf("admin: PriceRatio = %v, want 0.5", e.PriceRatio)
	}
}

// TestFillExecution_PrivateSource_NoPriceRatio 验证 private 行不快照倍率(保持零值 0)。
func TestFillExecution_PrivateSource_NoPriceRatio(t *testing.T) {
	attempt := state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 9, Name: "byok-ch"}},
		Source:    state.SourcePrivate,
		SourceID:  9,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if e.PriceRatio != 0 {
		t.Errorf("private: PriceRatio = %v, want 0 (unset)", e.PriceRatio)
	}
}

// TestFillExecution_AdminSource_SnapshotsFree 验证公共 channel 的 free 标记被快照进 entry。
func TestFillExecution_AdminSource_SnapshotsFree(t *testing.T) {
	attempt := state.Attempt{
		Channel: &models.Channel{
			ChannelCore: models.ChannelCore{ID: 21, Name: "free-ch", Type: 1},
			Free:        true,
		},
		Source:    state.SourceAdmin,
		SourceID:  21,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if !e.Free {
		t.Errorf("admin free channel: e.Free = false, want true")
	}
}

// TestFillExecution_PrivateSource_FreeStaysFalse 验证 private 行不快照 free(保持 false)。
func TestFillExecution_PrivateSource_FreeStaysFalse(t *testing.T) {
	attempt := state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 9, Name: "byok-ch"}, Free: true},
		Source:    state.SourcePrivate,
		SourceID:  9,
		RealModel: "gpt-4o",
		Mode:      state.ModeNative,
	}
	rctx := newByokRctx(attempt)

	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)

	if e.Free {
		t.Errorf("private row: e.Free = true, want false (not snapshotted)")
	}
}
