// Package modelview 计算"当前 caller 在 /v1/models 中应见的模型列表"。
// 该域不依赖 chat completion 的 state.RelayContext；只需要 caller 的 UserInfo
// 与一个最小数据接口 ModelStore（*cache.Store 天然满足）。
package modelview

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// ListedModel 是 modelview 给 handler 的 DTO。
// Created 时间戳由 handler 在转 OpenAI 响应时统一打，不进核心层（便于测试稳定）。
type ListedModel struct {
	Name    string
	OwnedBy string
}

const (
	OwnedByAdmin   = "ai-gateway"
	OwnedByBYOK    = "ai-gateway-byok"
	OwnedByRouting = "ai-gateway-routing"
)

// ModelStore 是 modelview 需要的最小数据访问接口。
// *cache.Store 天然满足；测试可注入 stub。
type ModelStore interface {
	GetAllModelNames() []string
	GetChannelsForModel(name string) []*models.Channel
	ListGlobalRoutingNames() []string
	ListUserRoutingNames(userID uint) []string
	ListVisibleBYOKModelNamesForUser(userID uint) []string
}

// ListVisibleModels 返回 caller 在 /v1/models 中应见的模型列表。
//
// 顺序契约：
//  1. admin 段：来自 store.GetAllModelNames()，应用 4 层 AND 白名单，让位 routing
//  2. BYOK 段：来自 store.ListVisibleBYOKModelNamesForUser，应用 group/token model 白名单，让位 routing；不应用 channel 白名单
//  3. routing 段：global routing → user routing（去重保序）
//
// admin 与 BYOK 同名时合并去重，BYOK 覆盖 OwnedBy 为 OwnedByBYOK，保留 admin 段原位。
// BYOK 独有项追加在 admin 段末尾、routing 段之前。
//
// ui == nil 或 ui.UserID == 0 时按"未认证"语义处理：admin 段无白名单过滤、BYOK 段空、
// routing 段仅 global routing。
func ListVisibleModels(store ModelStore, ui *app.UserInfo) []ListedModel {
	ctx := newListingContext(store, ui)
	admin := collectAdmin(ctx)
	byok := collectBYOK(ctx)
	routing := collectRouting(ctx)
	return merge(admin, byok, routing)
}

type listingContext struct {
	store ModelStore
	ui    *app.UserInfo
	// routingNames 是 global→user 顺序的有序、未去重切片，供 collectRouting 保序输出。
	// routingSet 是同一组名字的 set，供 admin / BYOK 段做 contains 检查（map 遍历无序，
	// 不能替代 routingNames 生成输出顺序）。两者由 ListVisibleModels 一次构建后共享。
	routingNames []string
	routingSet   map[string]struct{}
}

func newListingContext(store ModelStore, ui *app.UserInfo) *listingContext {
	var names []string
	names = append(names, store.ListGlobalRoutingNames()...)
	if ui != nil && ui.UserID > 0 {
		names = append(names, store.ListUserRoutingNames(ui.UserID)...)
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return &listingContext{store: store, ui: ui, routingNames: names, routingSet: set}
}

func collectAdmin(ctx *listingContext) []ListedModel {
	allModels := ctx.store.GetAllModelNames()
	var (
		tokenModels, groupModels                  []string
		allowedChannelIDs, groupAllowedChannelIDs []uint
	)
	if ctx.ui != nil {
		tokenModels = ctx.ui.TokenModels
		groupModels = ctx.ui.GroupModels
		allowedChannelIDs = ctx.ui.AllowedChannelIDs
		groupAllowedChannelIDs = ctx.ui.GroupAllowedChannelIDs
	}
	out := make([]ListedModel, 0, len(allModels))
	for _, name := range allModels {
		if _, isRouting := ctx.routingSet[name]; isRouting {
			continue
		}
		if len(groupAllowedChannelIDs) > 0 {
			if len(plan.FilterByAllowedChannels(ctx.store.GetChannelsForModel(name), groupAllowedChannelIDs)) == 0 {
				continue
			}
		}
		if len(allowedChannelIDs) > 0 {
			if len(plan.FilterByAllowedChannels(ctx.store.GetChannelsForModel(name), allowedChannelIDs)) == 0 {
				continue
			}
		}
		if len(groupModels) > 0 && !utils.ModelMatches(name, groupModels) {
			continue
		}
		if len(tokenModels) > 0 && !utils.ModelMatches(name, tokenModels) {
			continue
		}
		out = append(out, ListedModel{Name: name, OwnedBy: OwnedByAdmin})
	}
	return out
}

// collectBYOK 列出 caller 在 /v1/models 中应见的 BYOK 段模型。
//
// 过滤规则（与 spec §6.4 对齐）：
//  1. 未认证（ui == nil 或 ui.UserID == 0）→ 返回 nil
//  2. 与 routing 段同名 → 跳过，让位 routing
//  3. GroupModels / TokenModels 非空时按 utils.ModelMatches 过滤
//  4. 不应用 channel 白名单（GroupAllowedChannelIDs / AllowedChannelIDs）—— BYOK
//     绕过 channel 白名单，与 chat completion pool.go 中 SourcePrivate 的行为一致。
func collectBYOK(ctx *listingContext) []ListedModel {
	if ctx.ui == nil || ctx.ui.UserID == 0 {
		return nil
	}
	names := ctx.store.ListVisibleBYOKModelNamesForUser(ctx.ui.UserID)
	if len(names) == 0 {
		return nil
	}
	tokenModels := ctx.ui.TokenModels
	groupModels := ctx.ui.GroupModels
	out := make([]ListedModel, 0, len(names))
	for _, name := range names {
		if _, isRouting := ctx.routingSet[name]; isRouting {
			continue
		}
		if len(groupModels) > 0 && !utils.ModelMatches(name, groupModels) {
			continue
		}
		if len(tokenModels) > 0 && !utils.ModelMatches(name, tokenModels) {
			continue
		}
		out = append(out, ListedModel{Name: name, OwnedBy: OwnedByBYOK})
	}
	return out
}

func collectRouting(ctx *listingContext) []ListedModel {
	out := make([]ListedModel, 0, len(ctx.routingNames))
	seen := make(map[string]struct{}, len(ctx.routingNames))
	for _, name := range ctx.routingNames {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, ListedModel{Name: name, OwnedBy: OwnedByRouting})
	}
	return out
}

func merge(admin, byok, routing []ListedModel) []ListedModel {
	result := make([]ListedModel, 0, len(admin)+len(byok)+len(routing))
	idx := make(map[string]int, len(admin)+len(byok))
	for _, m := range admin {
		idx[m.Name] = len(result)
		result = append(result, m)
	}
	for _, m := range byok {
		if i, dup := idx[m.Name]; dup {
			result[i].OwnedBy = OwnedByBYOK
			continue
		}
		idx[m.Name] = len(result)
		result = append(result, m)
	}
	result = append(result, routing...)
	return result
}
