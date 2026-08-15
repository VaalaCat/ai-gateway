package api_upstream

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type UpstreamPublisher interface {
	PublishUpstream(context.Context, string, models.APIUpstream) error
}

type Creator struct {
	Cipher *byokcrypto.Cipher
}

type Handler struct {
	App       app.Application
	Publisher UpstreamPublisher
	Creator   Creator
}

type ListRequest struct {
	api.PaginationQuery
	APIServiceID *uint  `form:"api_service_id"`
	BackendID    *uint  `form:"backend_id"`
	Search       string `form:"search"`
	Status       *int   `form:"status"`
}

type CreateInput struct {
	Name           string                     `json:"name" binding:"required,max=64"`
	BaseURL        string                     `json:"base_url" binding:"required"`
	Weight         int                        `json:"weight"`
	Priority       int                        `json:"priority"`
	AuthType       models.APIUpstreamAuthType `json:"auth_type"`
	Credential     *APIUpstreamCredential     `json:"credential"`
	ProxyURL       *string                    `json:"proxy_url"`
	HeaderOverride map[string]string          `json:"header_override"`
	Status         *int                       `json:"status"`
}

type CreateRequest struct {
	BackendID uint `json:"backend_id" binding:"required"`
	CreateInput
}

type IDRequest struct {
	ID string `uri:"id" binding:"required"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(v map[string]any) { r.Fields = v }

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[APIUpstreamManagementResponse], error) {
	if _, err := h.listServiceID(c, req); err != nil {
		return api.PaginatedResponse[APIUpstreamManagementResponse]{}, err
	}
	p, s := api.NormalizePagination(req.Page, req.PageSize)
	rows, total, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIUpstream().List(dao.ListOptions{Page: p, PageSize: s}, dao.APIUpstreamFilter{APIServiceID: req.APIServiceID, BackendID: req.BackendID, Search: req.Search, Status: req.Status})
	if err != nil {
		return api.PaginatedResponse[APIUpstreamManagementResponse]{}, api.InternalError("list API upstreams failed", err)
	}
	data := make([]APIUpstreamManagementResponse, 0, len(rows))
	for _, row := range rows {
		data = append(data, NewAPIUpstreamManagementResponse(row, true))
	}
	return api.PaginatedResponse[APIUpstreamManagementResponse]{Data: data, Total: total, Page: p, PageSize: s}, nil
}

func (h *Handler) listServiceID(c *app.Context, req ListRequest) (uint, error) {
	if req.APIServiceID == nil && req.BackendID == nil {
		return 0, api.BadRequestError("api_service_id or backend_id is required", nil)
	}
	if req.APIServiceID != nil && *req.APIServiceID == 0 {
		return 0, api.BadRequestError("api_service_id must be greater than zero", nil)
	}
	if req.BackendID == nil {
		return *req.APIServiceID, nil
	}
	if *req.BackendID == 0 {
		return 0, api.BadRequestError("backend_id must be greater than zero", nil)
	}
	backend, err := h.backend(c, *req.BackendID)
	if err != nil {
		return 0, err
	}
	if req.APIServiceID != nil && backend.APIServiceID != *req.APIServiceID {
		return 0, api.BadRequestError("backend does not belong to api_service_id", nil)
	}
	return backend.APIServiceID, nil
}

func (h *Handler) Get(c *app.Context, req IDRequest) (APIUpstreamManagementResponse, error) {
	row, err := h.upstream(c, req.ID)
	if err != nil {
		return APIUpstreamManagementResponse{}, err
	}
	return NewAPIUpstreamManagementResponse(*row, true), nil
}

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[APIUpstreamManagementResponse], error) {
	if _, err := h.backend(c, req.BackendID); err != nil {
		return api.Created[APIUpstreamManagementResponse]{}, err
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	var row models.APIUpstream
	err := dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		var createErr error
		row, createErr = h.Creator.CreateInTx(tx, req.BackendID, req.CreateInput)
		return createErr
	})
	if err != nil {
		return api.Created[APIUpstreamManagementResponse]{}, createUpstreamError(err)
	}
	if err = h.publish(c, "create", row); err != nil {
		return api.Created[APIUpstreamManagementResponse]{}, err
	}
	return api.Created[APIUpstreamManagementResponse]{Value: NewAPIUpstreamManagementResponse(row, true)}, nil
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (api.StatusResponse, error) {
	row, err := h.upstream(c, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	if _, ok := req.Fields["backend_id"]; ok {
		return api.StatusResponse{}, api.ErrorWithCode(400, "backend_id_immutable", "backend_id cannot be changed", nil)
	}
	credential, proxy, err := decodeSecretPatch(req.Fields)
	if err != nil {
		return api.StatusResponse{}, err
	}
	if err = validateAPIUpstreamProxyURL(proxy); err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid upstream proxy_url", err)
	}
	if err = normalizePatch(req.Fields); err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	err = dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		current, err := dao.NewAdminQuery(tx).APIUpstream().GetByID(row.ID)
		if err != nil {
			return err
		}
		finalAuth := current.AuthType
		if value, ok := req.Fields["auth_type"].(models.APIUpstreamAuthType); ok {
			finalAuth = value
		}
		if err := validateCredentialTransition(current.AuthType, finalAuth, credential, false); err != nil {
			return err
		}
		if err := dao.NewAdminMutation(tx).APIUpstream().Update(row.ID, req.Fields); err != nil {
			return err
		}
		updated, err := dao.NewAdminQuery(tx).APIUpstream().GetByID(row.ID)
		if err != nil {
			return err
		}
		if err := h.Creator.storeSecrets(tx, updated, credential, proxy); err != nil {
			return err
		}
		row = updated
		return nil
	})
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("update API upstream failed", err)
	}
	if err = h.publish(c, "update", *row); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func (h *Handler) Delete(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	row, err := h.upstream(c, req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	if err = dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).APIUpstream().Delete(row.ID); err != nil {
		return api.StatusResponse{}, api.InternalError("delete API upstream failed", err)
	}
	if err = h.publish(c, "delete", *row); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func (h *Handler) upstream(c *app.Context, raw string) (*models.APIUpstream, error) {
	id, err := upstreamID(raw)
	if err != nil {
		return nil, err
	}
	row, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIUpstream().GetByID(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, api.NotFoundError(consts.ErrNotFound)
	}
	if err != nil {
		return nil, api.InternalError("load API upstream failed", err)
	}
	return row, nil
}

func (h *Handler) backend(c *app.Context, backendID uint) (*models.APIBackend, error) {
	backend, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).APIBackend().GetByID(backendID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, api.NotFoundError(consts.ErrNotFound)
	}
	if err != nil {
		return nil, api.InternalError("load API backend failed", err)
	}
	return backend, nil
}

func (c Creator) CreateInTx(ctx dao.Context, backendID uint, input CreateInput) (models.APIUpstream, error) {
	row, err := BuildAPIUpstreamForCreate(backendID, input)
	if err != nil {
		return models.APIUpstream{}, err
	}
	if err := dao.NewAdminMutation(ctx).APIUpstream().Create(&row); err != nil {
		return models.APIUpstream{}, err
	}
	if err := c.storeSecrets(ctx, &row, input.Credential, input.ProxyURL); err != nil {
		return models.APIUpstream{}, err
	}
	return row, nil
}

func (c Creator) storeSecrets(ctx dao.Context, row *models.APIUpstream, credential *APIUpstreamCredential, proxy *string) error {
	updates := map[string]any{}
	if credential != nil {
		value, err := EncryptAPIUpstreamCredential(c.Cipher, row.ID, row.AuthType, *credential)
		if err != nil {
			return api.BadRequestError("invalid upstream credential", err)
		}
		updates["credential_ciphertext"] = value
		row.CredentialCiphertext = value
	}
	if proxy != nil {
		value, err := encryptOptional(c.Cipher, row.ID, *proxy)
		if err != nil {
			return api.BadRequestError("invalid upstream proxy_url", err)
		}
		updates["proxy_url_ciphertext"] = value
		row.ProxyURLCiphertext = value
	}
	if len(updates) == 0 {
		return nil
	}
	if err := dao.NewAdminMutation(ctx).APIUpstream().Update(row.ID, updates); err != nil {
		return api.InternalError("store API upstream secrets failed", err)
	}
	return nil
}

func validateCredentialTransition(previous, next models.APIUpstreamAuthType, credential *APIUpstreamCredential, creating bool) error {
	if next == models.APIUpstreamAuthNone {
		if creating && credential == nil {
			return nil
		}
		if previous != next && credential == nil {
			return api.BadRequestError("credential is required when changing auth_type", nil)
		}
		if credential != nil && validateAPIUpstreamCredential(next, *credential) != nil {
			return api.BadRequestError("invalid upstream credential", nil)
		}
		return nil
	}
	if credential == nil && (creating || previous != next) {
		return api.BadRequestError("credential is required for auth_type", nil)
	}
	if credential != nil && validateAPIUpstreamCredential(next, *credential) != nil {
		return api.BadRequestError("invalid upstream credential", nil)
	}
	return nil
}

func (h *Handler) publish(c *app.Context, action string, row models.APIUpstream) error {
	if h.Publisher == nil {
		return nil
	}
	if err := h.Publisher.PublishUpstream(context.Background(), action, row); err != nil {
		return api.InternalError("publish API upstream failed", err)
	}
	return nil
}

func createUpstreamError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return api.NotFoundError(consts.ErrNotFound)
	}
	return api.BadRequestError("create API upstream failed", err)
}

func upstreamID(raw string) (uint, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, api.BadRequestError("invalid id", err)
	}
	return uint(n), nil
}

func encryptOptional(cipher *byokcrypto.Cipher, id uint, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if cipher == nil {
		return "", errInvalidAPIUpstreamCredential
	}
	sealed, err := cipher.Seal(value, id)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decodeSecretPatch(fields map[string]any) (*APIUpstreamCredential, *string, error) {
	var credential *APIUpstreamCredential
	if raw, ok := fields["credential"]; ok {
		delete(fields, "credential")
		encoded, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, api.BadRequestError("invalid upstream credential", nil)
		}
		value := APIUpstreamCredential{}
		for key, item := range encoded {
			stringValue, ok := item.(string)
			if !ok {
				continue
			}
			switch key {
			case "bearer_token":
				value.BearerToken = stringValue
			case "header_name":
				value.HeaderName = stringValue
			case "header_value":
				value.HeaderValue = stringValue
			case "query_name":
				value.QueryName = stringValue
			case "query_value":
				value.QueryValue = stringValue
			case "basic_username":
				value.BasicUsername = stringValue
			case "basic_password":
				value.BasicPassword = stringValue
			}
		}
		credential = &value
	}
	var proxy *string
	if raw, ok := fields["proxy_url"]; ok {
		delete(fields, "proxy_url")
		value, ok := raw.(string)
		if !ok {
			return nil, nil, api.BadRequestError("invalid upstream proxy_url", nil)
		}
		proxy = &value
	}
	return credential, proxy, nil
}

func normalizePatch(fields map[string]any) error {
	for _, key := range []string{"weight", "priority", "status"} {
		if value, ok := fields[key].(float64); ok && value == float64(int(value)) {
			fields[key] = int(value)
		}
	}
	if value, ok := fields["auth_type"].(string); ok {
		fields["auth_type"] = models.APIUpstreamAuthType(value)
	}
	if value, ok := fields["header_override"]; ok {
		values, ok := value.(map[string]any)
		if !ok {
			return api.BadRequestError("header_override must be an object", nil)
		}
		headerOverride := make(map[string]string, len(values))
		for key, item := range values {
			stringValue, ok := item.(string)
			if !ok {
				return api.BadRequestError("header_override values must be strings", nil)
			}
			headerOverride[key] = stringValue
		}
		normalized, err := normalizeAPIUpstreamHeaderOverrides(headerOverride)
		if err != nil {
			return api.BadRequestError("invalid upstream header_override", err)
		}
		fields["header_override"] = datatypes.NewJSONType(normalized)
	}
	return nil
}
