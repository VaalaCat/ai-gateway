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

func TestUserRoutingsLoader_Found(t *testing.T) {
	routings := []*protocol.SyncedRouting{
		{ID: 1, Name: "fast", Scope: "user", UserID: 42, Enabled: true},
		{ID: 2, Name: "cheap", Scope: "user", UserID: 42, Enabled: true},
	}
	payload, _ := json.Marshal(map[string]any{"routings": routings})

	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: true, Data: payload}
		return json.Marshal(resp)
	}}

	l := &UserRoutingsLoader{Client: cli}
	got, err := l.Load(context.Background(), 42)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got == nil {
		t.Fatal("got nil UserRoutingMap")
	}
	if len(got.Routings) != 2 {
		t.Fatalf("len(Routings) = %d, want 2", len(got.Routings))
	}
	if got.Routings["fast"] == nil || got.Routings["fast"].ID != 1 {
		t.Fatalf("Routings[fast] = %+v", got.Routings["fast"])
	}
	if cli.lastMethod != consts.RPCSyncFetchEntity {
		t.Fatalf("method = %q, want %q", cli.lastMethod, consts.RPCSyncFetchEntity)
	}
	req, _ := cli.lastParams.(protocol.FetchEntityRequest)
	if req.Entity != "user_routings" || req.Key != "42" {
		t.Fatalf("params = %+v", req)
	}
}

func TestUserRoutingsLoader_NotFound(t *testing.T) {
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: false}
		return json.Marshal(resp)
	}}
	l := &UserRoutingsLoader{Client: cli}
	_, err := l.Load(context.Background(), 99)
	if !errors.Is(err, entitycache.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUserRoutingsLoader_NilClient(t *testing.T) {
	l := &UserRoutingsLoader{Client: nil}
	_, err := l.Load(context.Background(), 1)
	if !errors.Is(err, entitycache.ErrNotFound) {
		t.Fatalf("nil client: err = %v, want ErrNotFound", err)
	}
}

func TestUserRoutingsLoader_RPCError(t *testing.T) {
	want := errors.New("rpc boom")
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		return nil, want
	}}
	l := &UserRoutingsLoader{Client: cli}
	_, err := l.Load(context.Background(), 1)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
