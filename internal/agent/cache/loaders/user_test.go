package loaders

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestUserLoader_Found(t *testing.T) {
	su := protocol.SyncedUser{ID: 42, GroupID: 3}
	suB, _ := json.Marshal(su)
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: true, Data: suB}
		return json.Marshal(resp)
	}}
	l := &UserLoader{Client: cli}
	got, err := l.Load(context.Background(), 42)
	if err != nil || got == nil || got.ID != 42 || got.GroupID != 3 {
		t.Fatalf("Load: %+v err=%v", got, err)
	}
	if cli.lastMethod != consts.RPCSyncFetchEntity {
		t.Fatalf("method = %q", cli.lastMethod)
	}
	req, _ := cli.lastParams.(protocol.FetchEntityRequest)
	if req.Entity != "user" || req.Key != "42" {
		t.Fatalf("req = %+v", req)
	}
}

func TestUserLoader_NotFound(t *testing.T) {
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: false}
		return json.Marshal(resp)
	}}
	l := &UserLoader{Client: cli}
	_, err := l.Load(context.Background(), 99)
	if !errors.Is(err, entitycache.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestUserLoader_RPCError(t *testing.T) {
	want := errors.New("net")
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		return nil, want
	}}
	l := &UserLoader{Client: cli}
	_, err := l.Load(context.Background(), 1)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
