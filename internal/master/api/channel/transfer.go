package channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

type ExportFilter struct {
	Search string `json:"search"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type ExportRequest = channelfile.ExportRequest[ExportFilter]

func (h *Handler) ExportHTTP(adapter *api.Adapter) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var req ExportRequest
		if err := ginCtx.ShouldBindJSON(&req); err != nil {
			api.WriteMappedError(adapter, ginCtx, api.BadRequestError(err.Error(), err))
			return
		}
		ctx := adapter.ContextFactory.Build(ginCtx)
		envelope, err := exportAdminChannels(ctx, req.Selection)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, err)
			return
		}
		var body bytes.Buffer
		if err := channelfile.Encode(&body, envelope); err != nil {
			api.WriteMappedError(adapter, ginCtx, api.InternalError("encode channel file", err))
			return
		}
		api.SetAttachmentHeaders(ginCtx, "ai-gateway-admin-channels-"+time.Now().UTC().Format("20060102T150405Z")+".json")
		ginCtx.Data(http.StatusOK, "application/json; charset=utf-8", body.Bytes())
	}
}

func (h *Handler) ImportHTTP(adapter *api.Adapter) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		dryRun, err := api.ParseChannelImportDryRun(ginCtx.Query("dry_run"))
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, err)
			return
		}
		envelope, err := channelfile.Decode[channelfile.AdminChannel](
			ginCtx.Request.Body, channelfile.KindAdminChannels,
		)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, api.MapChannelFileError(err))
			return
		}
		ctx := adapter.ContextFactory.Build(ginCtx)
		preview, inputs, err := previewAdminChannelImport(ctx, envelope.Channels)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, err)
			return
		}
		if dryRun {
			adapter.Writer.WriteJSON(ginCtx, http.StatusOK, preview)
			return
		}
		if len(inputs) == 0 {
			api.WriteMappedError(adapter, ginCtx, api.ErrorWithCode(
				http.StatusBadRequest, "channels_empty", "channels cannot be empty", nil,
			))
			return
		}
		if preview.Failed > 0 {
			api.WriteMappedError(adapter, ginCtx, api.ErrorWithCode(
				http.StatusBadRequest, "channel_import_invalid", "channel import validation failed", nil,
			))
			return
		}
		rows, err := createAdminChannels(ctx, inputs)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, err)
			return
		}
		result := channelfile.ImportResult{
			Kind: channelfile.KindAdminChannels, Created: len(rows),
			Items: make([]channelfile.ImportResultItem, 0, len(rows)),
		}
		for i := range rows {
			result.Items = append(result.Items, channelfile.ImportResultItem{
				ID: rows[i].ID, SourceName: preview.Items[i].SourceName, FinalName: rows[i].Name,
			})
		}
		adapter.Writer.WriteJSON(ginCtx, http.StatusCreated, result)
	}
}

func exportAdminChannels(
	c *app.Context,
	selection channelfile.Selection[ExportFilter],
) (channelfile.Envelope[channelfile.AdminChannel], error) {
	rows, err := selectAdminChannels(c, selection)
	if err != nil {
		return channelfile.Envelope[channelfile.AdminChannel]{}, err
	}
	files := make([]channelfile.AdminChannel, 0, len(rows))
	for i := range rows {
		file, err := adminChannelToFile(rows[i])
		if err != nil {
			return channelfile.Envelope[channelfile.AdminChannel]{}, api.InternalError(
				fmt.Sprintf("encode stored channel %d", rows[i].ID), err,
			)
		}
		files = append(files, file)
	}
	return channelfile.NewEnvelope(channelfile.KindAdminChannels, time.Now(), files), nil
}

func selectAdminChannels(
	c *app.Context,
	selection channelfile.Selection[ExportFilter],
) ([]models.Channel, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	switch selection.Mode {
	case channelfile.SelectionIDs:
		if len(selection.IDs) == 0 {
			return nil, api.BadRequestError("ids cannot be empty", nil)
		}
		if len(selection.IDs) > channelfile.MaxChannels {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "too_many_channels", "too many channels", nil)
		}
		var rows []models.Channel
		if err := daoCtx.GetDB().Where("id IN ?", selection.IDs).Order("id ASC").Find(&rows).Error; err != nil {
			return nil, api.InternalError("list channels", err)
		}
		if len(rows) != len(uniqueIDs(selection.IDs)) {
			return nil, api.NotFoundError("one or more channels not found")
		}
		return rows, nil
	case channelfile.SelectionFilter:
		filter, err := adminExportFilter(selection.Filter)
		if err != nil {
			return nil, err
		}
		rows, total, err := q.Channel().List(
			dao.ListOptions{Page: 1, PageSize: channelfile.MaxChannels + 1}, filter,
		)
		if err != nil {
			return nil, api.InternalError("list channels", err)
		}
		if total > channelfile.MaxChannels {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "too_many_channels", "too many channels", nil)
		}
		return rows, nil
	default:
		return nil, api.BadRequestError("selection.mode must be ids or filter", nil)
	}
}

func adminExportFilter(input ExportFilter) (dao.ChannelListFilter, error) {
	filter := dao.ChannelListFilter{Search: input.Search}
	if input.Type != "" {
		value, err := strconv.Atoi(input.Type)
		if err != nil {
			return filter, api.BadRequestError("invalid type filter", err)
		}
		filter.Type = &value
	}
	if input.Status != "" {
		value, err := strconv.Atoi(input.Status)
		if err != nil || (value != 0 && value != 1) {
			return filter, api.BadRequestError("invalid status filter", err)
		}
		filter.Status = &value
	}
	return filter, nil
}

func adminChannelToFile(channel models.Channel) (channelfile.AdminChannel, error) {
	mapping := map[string]string{}
	if channel.ModelMapping != "" {
		if err := json.Unmarshal([]byte(channel.ModelMapping), &mapping); err != nil {
			return channelfile.AdminChannel{}, fmt.Errorf("invalid model_mapping: %w", err)
		}
	}
	return channelfile.AdminChannel{
		Name: channel.Name, Status: channel.Status, Type: channel.Type, Key: channel.Key,
		BaseURL: channel.BaseURL, Models: splitModels(channel.Models), ModelMapping: mapping,
		Weight: channel.Weight, Priority: channel.Priority, ProxyURL: channel.ProxyURL,
		HeaderOverride: channel.HeaderOverride, SupportedAPITypes: channel.SupportedAPITypes,
		Endpoints: channel.Endpoints, PassthroughEnabled: channel.PassthroughEnabled,
		UseLegacyAdaptor: channel.UseLegacyAdaptor, Organization: channel.Organization,
		APIVersion: channel.ApiVersion, SystemPrompt: channel.SystemPrompt,
		SystemPromptInInput: channel.SystemPromptInInput, RoleMapping: channel.RoleMapping,
		ParamOverride: channel.ParamOverride, Setting: channel.Setting, Tag: channel.Tag,
		Remark: channel.Remark, TestModel: channel.TestModel, AutoBan: channel.AutoBan,
		StatusCodeMapping: channel.StatusCodeMapping, OtherSettings: channel.OtherSettings,
		DisableKeepalive: channel.DisableKeepalive, Resilience: channel.Resilience.Data(),
		PriceRatio: channel.PriceRatio, Free: channel.Free, Limit: channel.Limit.Data(),
		Affinity: channel.Affinity.Data(),
	}, nil
}

func previewAdminChannelImport(
	c *app.Context,
	files []channelfile.AdminChannel,
) (channelfile.Preview, []AdminChannelCreateInput, error) {
	preview := channelfile.Preview{
		Kind: channelfile.KindAdminChannels, Total: len(files),
		Items: make([]channelfile.PreviewItem, 0, len(files)),
	}
	var existing []string
	if err := c.App.GetDB().WithContext(c.RequestContext()).Model(&models.Channel{}).Pluck("name", &existing).Error; err != nil {
		return preview, nil, api.InternalError("list channel names", err)
	}
	allocator := channelfile.NewNameAllocator(existing)
	inputs := make([]AdminChannelCreateInput, 0, len(files))
	for i := range files {
		item := channelfile.PreviewItem{
			Index: i, SourceName: files[i].Name, Warnings: []channelfile.ItemIssue{},
		}
		finalName, err := allocator.Allocate(files[i].Name)
		if err != nil {
			item.Error = issueFromError("invalid_name", "name", err)
			preview.Failed++
			preview.Items = append(preview.Items, item)
			inputs = append(inputs, adminFileToCreateInput(files[i], files[i].Name))
			continue
		}
		item.FinalName = finalName
		if finalName != files[i].Name {
			item.Warnings = append(item.Warnings, channelfile.ItemIssue{
				Code: "renamed", Field: "name", Message: "name already exists; a suffix will be added",
			})
		}
		input := adminFileToCreateInput(files[i], finalName)
		if _, err := buildAdminChannel(input); err != nil {
			item.Error = issueFromError("validation_failed", "", err)
			preview.Failed++
		} else {
			preview.Ready++
		}
		inputs = append(inputs, input)
		preview.Items = append(preview.Items, item)
	}
	return preview, inputs, nil
}

func adminFileToCreateInput(file channelfile.AdminChannel, name string) AdminChannelCreateInput {
	return AdminChannelCreateInput{
		Name: name, Status: file.Status, Type: file.Type, Key: file.Key, BaseURL: file.BaseURL,
		Models: append([]string(nil), file.Models...), ModelMapping: nonNilMap(file.ModelMapping),
		Weight: file.Weight, Priority: file.Priority, ProxyURL: file.ProxyURL,
		HeaderOverride: file.HeaderOverride, SupportedAPITypes: file.SupportedAPITypes,
		Endpoints: file.Endpoints, PassthroughEnabled: file.PassthroughEnabled,
		UseLegacyAdaptor: file.UseLegacyAdaptor, Organization: file.Organization,
		APIVersion: file.APIVersion, SystemPrompt: file.SystemPrompt,
		SystemPromptInInput: file.SystemPromptInInput, RoleMapping: file.RoleMapping,
		ParamOverride: file.ParamOverride, Setting: file.Setting, Tag: file.Tag,
		Remark: file.Remark, TestModel: file.TestModel, AutoBan: file.AutoBan,
		StatusCodeMapping: file.StatusCodeMapping, OtherSettings: file.OtherSettings,
		DisableKeepalive: file.DisableKeepalive, Resilience: file.Resilience,
		PriceRatio: file.PriceRatio, Free: file.Free, Limit: file.Limit, Affinity: file.Affinity,
	}
}

func issueFromError(code, field string, err error) *channelfile.ItemIssue {
	return &channelfile.ItemIssue{Code: code, Field: field, Message: err.Error()}
}

func uniqueIDs(ids []uint) map[uint]struct{} {
	result := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result
}
