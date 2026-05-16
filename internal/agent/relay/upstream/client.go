package upstream

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// BuildHTTPClient creates an HTTP client for upstream requests.
// The transport is obtained from the pool to enable connection reuse and apply
// per-host idle connection limits configured in relay settings.
//
// 改成包级函数 + 显式 pool 参数，让 backend 不再依赖 *Handler。
// pool 为 nil 时 fallback 到 default Transport，避免 panic 让单元测试装配更松。
func BuildHTTPClient(pool app.TransportPool, ch *models.Channel) *http.Client {
	if pool == nil {
		return &http.Client{}
	}
	return &http.Client{Transport: pool.Get(ch)}
}

// InjectSystemPrompt prepends or appends a system prompt to the IR request's
// message list. If a system message already exists, the channel's system
// prompt is appended to it. Otherwise a new system message is prepended.
func InjectSystemPrompt(req *codec.Request, prompt string) {
	if prompt == "" {
		return
	}

	// Look for an existing system message to append to
	for i, msg := range req.Messages {
		if msg.Role == codec.RoleSystem {
			if len(msg.Content) > 0 && msg.Content[0].Type == codec.ContentTypeText {
				req.Messages[i].Content[0].Text = msg.Content[0].Text + "\n" + prompt
			} else {
				req.Messages[i].Content = append(req.Messages[i].Content, codec.ContentBlock{
					Type: codec.ContentTypeText,
					Text: prompt,
				})
			}
			return
		}
	}

	// No existing system message; prepend one
	sysMsg := codec.TextMessage(codec.RoleSystem, prompt)
	req.Messages = append([]codec.Message{sysMsg}, req.Messages...)
}
