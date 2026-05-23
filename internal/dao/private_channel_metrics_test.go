package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestPrivateChannel_Create_SetsOwnerGauge 验证 Create 后 owner 的 GaugeVec
// 反映 CountByOwner 的真实值（不是简单 +1，避免并发漂移）。
func TestPrivateChannel_Create_SetsOwnerGauge(t *testing.T) {
	a, _ := setupTestApp(t)
	metrics.BYOKPrivateChannelCount.Reset()

	m := NewAdminMutation(NewContext(a))
	pc1 := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 7, Name: "a", Status: 1}
	if err := m.PrivateChannel().Create(pc1); err != nil {
		t.Fatal(err)
	}
	if got := readPrivateChannelGauge(t, "7"); got != 1 {
		t.Fatalf("after Create #1, owner=7 gauge = %v, want 1", got)
	}
	pc2 := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 7, Name: "b", Status: 1}
	if err := m.PrivateChannel().Create(pc2); err != nil {
		t.Fatal(err)
	}
	if got := readPrivateChannelGauge(t, "7"); got != 2 {
		t.Fatalf("after Create #2, owner=7 gauge = %v, want 2", got)
	}
}

// TestPrivateChannel_Delete_DecrementsOwnerGauge 验证 Delete 后 owner gauge 重算。
func TestPrivateChannel_Delete_DecrementsOwnerGauge(t *testing.T) {
	a, _ := setupTestApp(t)
	metrics.BYOKPrivateChannelCount.Reset()

	m := NewAdminMutation(NewContext(a))
	pc := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 11, Name: "to-del", Status: 1}
	if err := m.PrivateChannel().Create(pc); err != nil {
		t.Fatal(err)
	}
	if got := readPrivateChannelGauge(t, "11"); got != 1 {
		t.Fatalf("after Create, gauge = %v, want 1", got)
	}
	if err := m.PrivateChannel().Delete(pc.ID, 11); err != nil {
		t.Fatal(err)
	}
	if got := readPrivateChannelGauge(t, "11"); got != 0 {
		t.Fatalf("after Delete, gauge = %v, want 0", got)
	}
}

// TestPrivateChannel_DeleteByOwner_ZeroesOwnerGauge 验证整 owner 全删后 gauge=0。
func TestPrivateChannel_DeleteByOwner_ZeroesOwnerGauge(t *testing.T) {
	a, _ := setupTestApp(t)
	metrics.BYOKPrivateChannelCount.Reset()

	m := NewAdminMutation(NewContext(a))
	if err := m.PrivateChannel().Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 21, Name: "x", Status: 1}); err != nil {
		t.Fatal(err)
	}
	if err := m.PrivateChannel().Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 21, Name: "y", Status: 1}); err != nil {
		t.Fatal(err)
	}
	if got := readPrivateChannelGauge(t, "21"); got != 2 {
		t.Fatalf("setup: gauge = %v, want 2", got)
	}
	if err := m.PrivateChannel().DeleteByOwner(21); err != nil {
		t.Fatal(err)
	}
	if got := readPrivateChannelGauge(t, "21"); got != 0 {
		t.Fatalf("after DeleteByOwner, gauge = %v, want 0", got)
	}
}

func readPrivateChannelGauge(t *testing.T, ownerLabel string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := metrics.BYOKPrivateChannelCount.WithLabelValues(ownerLabel).(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetGauge().GetValue()
}
