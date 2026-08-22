package upstream

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// BuildHTTPClient creates an HTTP client for upstream requests.
// The transport is obtained from the pool to enable connection reuse and apply
// per-host idle connection limits configured in relay settings.
//
// 改成包级函数 + 显式 pool 参数，让 backend 不再依赖 *Handler。
// pool 为 nil 时 fallback 到 default Transport，避免 panic 让单元测试装配更松。
func BuildHTTPClient(pool app.TransportPool, ch *models.Channel) *http.Client {
	client := &http.Client{CheckRedirect: checkSameOriginRedirect}
	if pool == nil {
		return client
	}
	client.Transport = pool.Get(ch)
	return client
}

func checkSameOriginRedirect(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	origin := via[0].URL
	if !strings.EqualFold(request.URL.Scheme, origin.Scheme) ||
		!strings.EqualFold(request.URL.Host, origin.Host) {
		return fmt.Errorf("refusing cross-origin redirect from %s://%s to %s://%s",
			origin.Scheme, origin.Host, request.URL.Scheme, request.URL.Host)
	}
	return nil
}

// InjectSystemPrompt prepends or appends a system prompt to the IR request's
// message list. If a system message already exists, the channel's system
// prompt is appended to it. Otherwise a new system message is prepended.
func InjectSystemPrompt(req *llmkit.Request, prompt string) {
	if prompt == "" {
		return
	}

	// Look for an existing system message to append to
	for i, msg := range req.Messages {
		if msg.Role == llmkit.RoleSystem {
			if len(msg.Content) > 0 && msg.Content[0].Type == llmkit.ContentTypeText {
				req.Messages[i].Content[0].Text = msg.Content[0].Text + "\n" + prompt
			} else {
				req.Messages[i].Content = append(req.Messages[i].Content, llmkit.ContentBlock{
					Type: llmkit.ContentTypeText,
					Text: prompt,
				})
			}
			return
		}
	}

	// No existing system message; prepend one
	sysMsg := llmkit.Message{
		Role: llmkit.RoleSystem,
		Content: []llmkit.ContentBlock{{
			Type: llmkit.ContentTypeText,
			Text: prompt,
		}},
	}
	req.Messages = append([]llmkit.Message{sysMsg}, req.Messages...)
}
