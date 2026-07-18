package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"gorm.io/gorm"
)

type tokenRoutingsFetchHandler struct{}

func (tokenRoutingsFetchHandler) Fetch(_ context.Context, q dao.AdminQuery, key string) (
	data, side json.RawMessage, found bool, err error,
) {
	tokenID64, parseErr := strconv.ParseUint(key, 10, 64)
	if parseErr != nil || tokenID64 == 0 {
		return nil, nil, false, nil
	}
	tokenID := uint(tokenID64)
	if _, tokenErr := q.Token().GetByID(tokenID); errors.Is(tokenErr, gorm.ErrRecordNotFound) {
		return nil, nil, false, nil
	} else if tokenErr != nil {
		return nil, nil, false, tokenErr
	}
	routings, listErr := q.ModelRouting().ListByToken(tokenID)
	if listErr != nil {
		return nil, nil, false, listErr
	}
	payload, marshalErr := json.Marshal(routingMap(routings))
	if marshalErr != nil {
		return nil, nil, false, marshalErr
	}
	return payload, nil, true, nil
}
