package private_channel

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/gin-gonic/gin"
)

type TransferFilter struct {
	Search string `json:"search"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type TransferExportRequest = channelfile.ExportRequest[TransferFilter]

func (h *Handler) ExportHTTP(adapter *api.Adapter) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var req TransferExportRequest
		if err := ginCtx.ShouldBindJSON(&req); err != nil {
			api.WriteMappedError(adapter, ginCtx, api.BadRequestError(err.Error(), err))
			return
		}
		ctx := adapter.ContextFactory.Build(ginCtx)
		envelope, err := h.exportPrivateChannels(ctx, req.Selection)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, err)
			return
		}
		var body bytes.Buffer
		if err := channelfile.Encode(&body, envelope); err != nil {
			api.WriteMappedError(adapter, ginCtx, api.InternalError("encode channel file", err))
			return
		}
		api.SetAttachmentHeaders(ginCtx, "ai-gateway-byok-channels-"+time.Now().UTC().Format("20060102T150405Z")+".json")
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
		envelope, err := channelfile.Decode[channelfile.BYOKChannel](
			ginCtx.Request.Body, channelfile.KindBYOKChannels,
		)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, api.MapChannelFileError(err))
			return
		}
		ctx := adapter.ContextFactory.Build(ginCtx)
		if ctx.UserInfo == nil {
			api.WriteMappedError(adapter, ginCtx, api.UnauthorizedError("not authenticated"))
			return
		}
		owner := PrivateChannelOwner{UserID: ctx.UserInfo.UserID, GroupID: ctx.UserInfo.GroupID}
		preview, inputs, err := h.previewPrivateChannelImport(ctx, owner, envelope.Channels)
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
		rows, err := createPrivateChannels(ctx, h.Provider, owner, inputs)
		if err != nil {
			api.WriteMappedError(adapter, ginCtx, err)
			return
		}
		result := channelfile.ImportResult{
			Kind: channelfile.KindBYOKChannels, Created: len(rows),
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

func (h *Handler) exportPrivateChannels(
	c *app.Context,
	selection channelfile.Selection[TransferFilter],
) (channelfile.Envelope[channelfile.BYOKChannel], error) {
	if c.UserInfo == nil || c.UserInfo.UserID == 0 {
		return channelfile.Envelope[channelfile.BYOKChannel]{}, api.UnauthorizedError("not authenticated")
	}
	cipher := h.Provider.GetCipher()
	if cipher == nil {
		return channelfile.Envelope[channelfile.BYOKChannel]{}, api.InternalError("byok cipher not configured", nil)
	}
	rows, err := selectPrivateChannels(c, c.UserInfo.UserID, selection)
	if err != nil {
		return channelfile.Envelope[channelfile.BYOKChannel]{}, err
	}
	files := make([]channelfile.BYOKChannel, 0, len(rows))
	for i := range rows {
		file, err := privateChannelToFile(rows[i], cipher)
		if err != nil {
			return channelfile.Envelope[channelfile.BYOKChannel]{}, api.InternalError(
				fmt.Sprintf("decrypt private channel %d", rows[i].ID), err,
			)
		}
		files = append(files, file)
	}
	return channelfile.NewEnvelope(channelfile.KindBYOKChannels, time.Now(), files), nil
}

func selectPrivateChannels(
	c *app.Context,
	ownerID uint,
	selection channelfile.Selection[TransferFilter],
) ([]models.PrivateChannel, error) {
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
		var rows []models.PrivateChannel
		if err := daoCtx.GetDB().Where("owner_id = ? AND id IN ?", ownerID, selection.IDs).
			Order("id ASC").Find(&rows).Error; err != nil {
			return nil, api.InternalError("list private channels", err)
		}
		if len(rows) != len(privateUniqueIDs(selection.IDs)) {
			return nil, api.NotFoundError("one or more private channels not found")
		}
		return rows, nil
	case channelfile.SelectionFilter:
		filter, err := privateTransferFilter(selection.Filter)
		if err != nil {
			return nil, err
		}
		rows, total, err := q.PrivateChannel().ListOwnedBy(
			ownerID, dao.ListOptions{Page: 1, PageSize: channelfile.MaxChannels + 1}, filter,
		)
		if err != nil {
			return nil, api.InternalError("list private channels", err)
		}
		if total > channelfile.MaxChannels {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "too_many_channels", "too many channels", nil)
		}
		return rows, nil
	default:
		return nil, api.BadRequestError("selection.mode must be ids or filter", nil)
	}
}

func privateTransferFilter(input TransferFilter) (dao.PrivateChannelFilter, error) {
	filter := dao.PrivateChannelFilter{Search: input.Search}
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

func privateChannelToFile(channel models.PrivateChannel, cipher *byokcrypto.Cipher) (channelfile.BYOKChannel, error) {
	key, err := cipher.Open(channel.KeyCipher, channel.OwnerID)
	if err != nil {
		return channelfile.BYOKChannel{}, err
	}
	return channelfile.BYOKChannel{
		Name: channel.Name, Status: channel.Status, Type: channel.Type, Key: key,
		BaseURL: channel.BaseURL, Models: append([]string(nil), channel.Models...),
		ModelMapping: cloneMapping(channel.ModelMapping.Data()), Weight: channel.Weight,
		Priority: channel.Priority, SupportedAPITypes: channel.SupportedAPITypes,
		Endpoints: channel.Endpoints, PassthroughEnabled: channel.PassthroughEnabled,
		UseLegacyAdaptor: channel.UseLegacyAdaptor, Organization: channel.Organization,
		APIVersion: channel.ApiVersion, SystemPrompt: channel.SystemPrompt,
		SystemPromptInInput: channel.SystemPromptInInput, RoleMapping: channel.RoleMapping,
		ParamOverride: channel.ParamOverride, Setting: channel.Setting, Tag: channel.Tag,
		Remark: channel.Remark, TestModel: channel.TestModel, AutoBan: channel.AutoBan,
		StatusCodeMapping: channel.StatusCodeMapping, OtherSettings: channel.OtherSettings,
		Affinity: channel.Affinity.Data(),
	}, nil
}

func (h *Handler) previewPrivateChannelImport(
	c *app.Context,
	owner PrivateChannelOwner,
	files []channelfile.BYOKChannel,
) (channelfile.Preview, []PrivateChannelCreateInput, error) {
	preview := channelfile.Preview{
		Kind: channelfile.KindBYOKChannels, Total: len(files),
		Items: make([]channelfile.PreviewItem, 0, len(files)),
	}
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	var existing []string
	if err := daoCtx.GetDB().Model(&models.PrivateChannel{}).Where("owner_id = ?", owner.UserID).
		Pluck("name", &existing).Error; err != nil {
		return preview, nil, api.InternalError("list private channel names", err)
	}
	allocator := channelfile.NewNameAllocator(existing)
	inputs := make([]PrivateChannelCreateInput, 0, len(files))
	var quotaErr error
	if len(files) > 0 {
		quotaErr = validateUserUnderChannelLimitCtx(ValidatorCtx{
			Query: q, Dirty: nil, OwnerID: owner.UserID, GroupID: owner.GroupID, CreateCount: len(files),
		})
	}
	for i := range files {
		item := channelfile.PreviewItem{
			Index: i, SourceName: files[i].Name, Warnings: []channelfile.ItemIssue{},
		}
		finalName, nameErr := allocator.Allocate(files[i].Name)
		if nameErr == nil {
			item.FinalName = finalName
			if finalName != files[i].Name {
				item.Warnings = append(item.Warnings, channelfile.ItemIssue{
					Code: "renamed", Field: "name", Message: "name already exists; a suffix will be added",
				})
			}
		}
		input := privateFileToCreateInput(files[i], finalName)
		var itemErr error
		switch {
		case nameErr != nil:
			itemErr = nameErr
		case quotaErr != nil:
			itemErr = quotaErr
		default:
			itemErr = validatePrivateChannelInput(q, owner, input, len(files), true)
		}
		if itemErr != nil {
			item.Error = &channelfile.ItemIssue{Code: "validation_failed", Message: itemErr.Error()}
			preview.Failed++
		} else {
			preview.Ready++
		}
		inputs = append(inputs, input)
		preview.Items = append(preview.Items, item)
	}
	return preview, inputs, nil
}

func privateFileToCreateInput(file channelfile.BYOKChannel, name string) PrivateChannelCreateInput {
	return PrivateChannelCreateInput{
		Name: name, Status: file.Status, Type: file.Type, Key: file.Key, BaseURL: file.BaseURL,
		Models: append([]string(nil), file.Models...), ModelMapping: cloneMapping(file.ModelMapping),
		Weight: file.Weight, Priority: file.Priority, SupportedAPITypes: file.SupportedAPITypes,
		Endpoints: file.Endpoints, PassthroughEnabled: file.PassthroughEnabled,
		UseLegacyAdaptor: file.UseLegacyAdaptor, Organization: file.Organization,
		APIVersion: file.APIVersion, SystemPrompt: file.SystemPrompt,
		SystemPromptInInput: file.SystemPromptInInput, RoleMapping: file.RoleMapping,
		ParamOverride: file.ParamOverride, Setting: file.Setting, Tag: file.Tag,
		Remark: file.Remark, TestModel: file.TestModel, AutoBan: file.AutoBan,
		StatusCodeMapping: file.StatusCodeMapping, OtherSettings: file.OtherSettings,
		Affinity: file.Affinity,
	}
}

func privateUniqueIDs(ids []uint) map[uint]struct{} {
	result := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result
}
