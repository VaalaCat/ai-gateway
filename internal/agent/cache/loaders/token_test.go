package loaders

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// stubWSClient 仅实现 Call；其他方法 panic（loader 不会用到）。
type stubWSClient struct {
	lastMethod string
	lastParams any
	respond    func(method string, params any) (json.RawMessage, error)
}

func (c *stubWSClient) OnNotification(_ string, _ app.NotificationHandler) {
}
func (c *stubWSClient) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.lastMethod = method
	c.lastParams = params
	return c.respond(method, params)
}
func (c *stubWSClient) Notify(_ string, _ any) error { return nil }
func (c *stubWSClient) Close() error                 { return nil }
func (c *stubWSClient) ReadLoop()                    {}

func TestTokenLoader_Found_WarmsUserSide(t *testing.T) {
	users := entitycache.NewFullCache[uint, *protocol.SyncedUser]()

	tok := &models.Token{ID: 11, Key: "sk-x", UserID: 22, Status: 1}
	side := protocol.SyncedUser{ID: 22, GroupID: 7}
	tokB, _ := json.Marshal(tok)
	sideB, _ := json.Marshal(side)

	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: true, Data: tokB, Side: sideB}
		return json.Marshal(resp)
	}}

	l := &TokenLoader{Client: cli, Users: users}
	got, err := l.Load(context.Background(), "sk-x")
	if err != nil || got == nil || got.ID != 11 || got.Key != "sk-x" {
		t.Fatalf("Load result: %+v err=%v", got, err)
	}
	if cli.lastMethod != consts.RPCSyncFetchEntity {
		t.Fatalf("method = %q, want %q", cli.lastMethod, consts.RPCSyncFetchEntity)
	}
	req, _ := cli.lastParams.(protocol.FetchEntityRequest)
	if req.Entity != "token" || req.Key != "sk-x" {
		t.Fatalf("params = %+v", req)
	}

	// 验证 user 被 warm
	cached, ok := users.Peek(22)
	if !ok || cached.GroupID != 7 {
		t.Fatalf("user warm failed: %+v ok=%v", cached, ok)
	}
}

func TestTokenLoader_NotFoundReturnsErrNotFound(t *testing.T) {
	users := entitycache.NewFullCache[uint, *protocol.SyncedUser]()
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: false}
		return json.Marshal(resp)
	}}
	l := &TokenLoader{Client: cli, Users: users}
	_, err := l.Load(context.Background(), "sk-missing")
	if !errors.Is(err, entitycache.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTokenLoader_RPCErrorPropagates(t *testing.T) {
	users := entitycache.NewFullCache[uint, *protocol.SyncedUser]()
	want := errors.New("boom")
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		return nil, want
	}}
	l := &TokenLoader{Client: cli, Users: users}
	_, err := l.Load(context.Background(), "sk-x")
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestTokenLoader_FoundWithoutSide_NoWarm(t *testing.T) {
	users := entitycache.NewFullCache[uint, *protocol.SyncedUser]()
	tok := &models.Token{ID: 1, Key: "sk-x", UserID: 22, Status: 1}
	tokB, _ := json.Marshal(tok)
	cli := &stubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: true, Data: tokB}
		return json.Marshal(resp)
	}}
	l := &TokenLoader{Client: cli, Users: users}
	if _, err := l.Load(context.Background(), "sk-x"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, ok := users.Peek(22); ok {
		t.Fatal("user cache should not be warmed when Side is empty")
	}
}
