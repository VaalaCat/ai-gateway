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

func TestTokenRoutingsLoader_FoundEmptyAndPopulated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		routings map[string]*protocol.SyncedRouting
	}{
		{name: "empty", routings: map[string]*protocol.SyncedRouting{}},
		{name: "populated", routings: map[string]*protocol.SyncedRouting{
			"smart": {ID: 3, Name: "smart", Scope: "token", TokenID: 9, Enabled: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(protocol.TokenRoutingMap{Routings: tc.routings})
			cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
				return json.Marshal(protocol.FetchEntityResponse{Found: true, Data: payload})
			}}
			got, err := (&TokenRoutingsLoader{Client: cli}).Load(context.Background(), 9)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Routings) != len(tc.routings) {
				t.Fatalf("routings = %+v", got.Routings)
			}
			req, _ := cli.lastParams.(protocol.FetchEntityRequest)
			if req.Entity != "token_routings" || req.Key != "9" {
				t.Fatalf("params = %+v", req)
			}
		})
	}
}

func TestTokenRoutingsLoader_NotFoundAndRPCError(t *testing.T) {
	notFound := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		return json.Marshal(protocol.FetchEntityResponse{Found: false})
	}}
	if _, err := (&TokenRoutingsLoader{Client: notFound}).Load(context.Background(), 1); !errors.Is(err, entitycache.ErrNotFound) {
		t.Fatalf("not found err = %v", err)
	}
	want := errors.New("control closed")
	failed := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) { return nil, want }}
	if _, err := (&TokenRoutingsLoader{Client: failed}).Load(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("rpc err = %v", err)
	}
}
