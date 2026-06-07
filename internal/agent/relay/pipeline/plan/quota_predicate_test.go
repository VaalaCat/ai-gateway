package plan

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// pricedMC 返回一个含价格的 ModelConfig（InputPrice>0），用于"已定价"分支。
func pricedMC() *models.ModelConfig {
	return &models.ModelConfig{InputPrice: 0.001}
}

// zeroMC 返回四桶价格全 0 的 ModelConfig，用于"未定价"分支。
func zeroMC() *models.ModelConfig {
	return &models.ModelConfig{}
}

// freeChannel / paidChannel 工厂，仅区分 Free 标记。
func freeChannel() *models.Channel {
	return &models.Channel{Free: true}
}

func paidChannel() *models.Channel {
	return &models.Channel{Free: false}
}

// TestChannelConsumesQuota 表驱动覆盖免费判定全部分支。
func TestChannelConsumesQuota(t *testing.T) {
	cases := []struct {
		name      string
		ch        *models.Channel
		source    state.ChannelSource
		byokMode  string
		mc        *models.ModelConfig
		wantSpend bool
	}{
		{
			name:      "free channel never consumes",
			ch:        freeChannel(),
			source:    state.SourceAdmin,
			byokMode:  consts.BYOKBillingModeServiceFee,
			mc:        pricedMC(),
			wantSpend: false,
		},
		{
			name:      "paid admin priced model consumes",
			ch:        paidChannel(),
			source:    state.SourceAdmin,
			byokMode:  consts.BYOKBillingModeServiceFee,
			mc:        pricedMC(),
			wantSpend: true,
		},
		{
			name:      "unpriced model (all-zero) never consumes",
			ch:        paidChannel(),
			source:    state.SourceAdmin,
			byokMode:  consts.BYOKBillingModeServiceFee,
			mc:        zeroMC(),
			wantSpend: false,
		},
		{
			name:      "nil model config never consumes",
			ch:        paidChannel(),
			source:    state.SourceAdmin,
			byokMode:  consts.BYOKBillingModeServiceFee,
			mc:        nil,
			wantSpend: false,
		},
		{
			name:      "byok private free-mode never consumes",
			ch:        paidChannel(),
			source:    state.SourcePrivate,
			byokMode:  "free",
			mc:        pricedMC(),
			wantSpend: false,
		},
		{
			name:      "byok private service_fee consumes",
			ch:        paidChannel(),
			source:    state.SourcePrivate,
			byokMode:  consts.BYOKBillingModeServiceFee,
			mc:        pricedMC(),
			wantSpend: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ChannelConsumesQuota(tc.ch, tc.source, tc.byokMode, tc.mc)
			if got != tc.wantSpend {
				t.Errorf("ChannelConsumesQuota = %v, want %v", got, tc.wantSpend)
			}
		})
	}
}

// TestModelIsPriced 单独钉住每一桶价格都能触发"已定价"。
func TestModelIsPriced(t *testing.T) {
	cases := []struct {
		name string
		mc   *models.ModelConfig
		want bool
	}{
		{"nil", nil, false},
		{"all-zero", &models.ModelConfig{}, false},
		{"input only", &models.ModelConfig{InputPrice: 0.1}, true},
		{"output only", &models.ModelConfig{OutputPrice: 0.1}, true},
		{"cache read only", &models.ModelConfig{CacheReadPrice: 0.1}, true},
		{"cache write only", &models.ModelConfig{CacheWritePrice: 0.1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelIsPriced(tc.mc); got != tc.want {
				t.Errorf("modelIsPriced = %v, want %v", got, tc.want)
			}
		})
	}
}
