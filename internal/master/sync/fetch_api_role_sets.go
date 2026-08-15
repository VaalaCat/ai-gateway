package sync

import (
	"context"
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
)

type APIRoleSetFetcher struct{}

func (APIRoleSetFetcher) FetchUser(
	ctx context.Context,
	q dao.AdminQuery,
	principalID uint,
) (protocol.APIRoleSetFetchResult, error) {
	result, err := apirbac.NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUser(ctx, principalID)
	return roleSetFetchResult(principalID, result), err
}

type apiRoleSetFetchFunc func(context.Context, dao.AdminQuery, uint) (protocol.APIRoleSetFetchResult, error)

func (h *Hub) handleAPIRoleSetFetch(
	ctx context.Context,
	conn *ws.Conn,
	req *jsonrpc.Request,
	fetch apiRoleSetFetchFunc,
) {
	if req.ID == nil {
		return
	}
	var params protocol.APIRoleSetFetchRequest
	if err := json.Unmarshal(req.Params, &params); err != nil || params.PrincipalID == 0 {
		message := "principal_id must not be zero"
		if err != nil {
			message = err.Error()
		}
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, message))
		return
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, ctx))
	result, err := fetch(ctx, q, params.PrincipalID)
	if err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
		return
	}
	response, _ := jsonrpc.NewResponse(req.ID, result)
	conn.SendResponse(response)
}

func (APIRoleSetFetcher) FetchToken(
	ctx context.Context,
	q dao.AdminQuery,
	principalID uint,
) (protocol.APIRoleSetFetchResult, error) {
	result, err := apirbac.NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindToken(ctx, principalID)
	return roleSetFetchResult(principalID, result), err
}

func roleSetFetchResult(principalID uint, result apirbac.RoleSetResult) protocol.APIRoleSetFetchResult {
	roleIDs := append([]uint{}, result.RoleIDs...)
	return protocol.APIRoleSetFetchResult{
		PrincipalID: principalID,
		Exists:      result.Exists,
		RoleSet:     protocol.APIRoleSet{RoleIDs: roleIDs},
	}
}
