package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestBYOKMetrics_RequestTotalIncrements 验证 BYOKRequestTotal 是真实 prometheus
// CounterVec，按 (owner_type, model) label 分桶累加。
func TestBYOKMetrics_RequestTotalIncrements(t *testing.T) {
	BYOKRequestTotal.Reset()
	BYOKRequestTotal.WithLabelValues("private", "gpt-4o").Inc()
	BYOKRequestTotal.WithLabelValues("private", "gpt-4o").Inc()

	if got := readCounterVec(t, BYOKRequestTotal, "private", "gpt-4o"); got != 2 {
		t.Fatalf("want 2, got %v", got)
	}
}

// TestBYOKMetrics_PrivateChannelCountByOwner 验证 GaugeVec 按 owner_id 分桶 set，
// Set 必须覆盖（不是累加）。
func TestBYOKMetrics_PrivateChannelCountByOwner(t *testing.T) {
	BYOKPrivateChannelCount.Reset()
	BYOKPrivateChannelCount.WithLabelValues("7").Set(5)
	BYOKPrivateChannelCount.WithLabelValues("7").Set(8)
	BYOKPrivateChannelCount.WithLabelValues("9").Set(2)

	m := &dto.Metric{}
	if err := BYOKPrivateChannelCount.WithLabelValues("7").(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := m.GetGauge().GetValue(); got != 8 {
		t.Fatalf("owner=7 want 8, got %v", got)
	}
	m2 := &dto.Metric{}
	_ = BYOKPrivateChannelCount.WithLabelValues("9").(prometheus.Metric).Write(m2)
	if got := m2.GetGauge().GetValue(); got != 2 {
		t.Fatalf("owner=9 want 2, got %v", got)
	}
}

// TestBYOKMetrics_DecryptFailureIsRealCounter 替换 Task 8 stub：sanitize 路径
// .Inc() 调用必须命中真实 prometheus Counter（不是 noop），从 DefaultGatherer
// 能 scrape 出 byok_decrypt_failure_total 数值。
func TestBYOKMetrics_DecryptFailureIsRealCounter(t *testing.T) {
	before := readCounter(t, BYOKDecryptFailureTotal)
	BYOKDecryptFailureTotal.Inc()
	BYOKDecryptFailureTotal.Inc()
	if got := readCounter(t, BYOKDecryptFailureTotal) - before; got != 2 {
		t.Fatalf("delta want 2, got %v", got)
	}
}

// TestBYOKMetrics_AllRegistered 验证 3 个保留 metric 都注册到 DefaultGatherer，
// 防止有人加 metric 忘了 MustRegister。
func TestBYOKMetrics_AllRegistered(t *testing.T) {
	expected := map[string]bool{
		"byok_private_channel_count": false,
		"byok_request_total":         false,
		"byok_decrypt_failure_total": false,
	}
	// 触发样本，否则 Gather 可能跳过未观测的 label-less Counter。
	BYOKPrivateChannelCount.WithLabelValues("0").Set(0)
	BYOKRequestTotal.WithLabelValues("init", "init").Add(0)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if _, ok := expected[f.GetName()]; ok {
			expected[f.GetName()] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Fatalf("metric %s not registered with prometheus.DefaultGatherer", name)
		}
	}
}

// --- helpers ---

func readCounter(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

func readCounterVec(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := cv.WithLabelValues(labels...).(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetCounter().GetValue()
}
