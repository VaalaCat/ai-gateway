package passthrough

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/protocolconfig"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/videotask"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// prepareProviderRequest preserves non-chat fields and streams multipart files.
// Every retry opens the replay body again; binary uploads never enter JSON codecs.
func prepareProviderRequest(rctx *state.RelayContext, a state.Attempt) (*http.Request, []byte, error) {
	orig := rctx.Request
	ch := a.Channel
	endpoint := protocolconfig.ResolveEndpointPath(ch.Endpoints, rctx.Input.InboundProto)
	if endpoint == "" {
		endpoint = orig.URL.Path
	}
	if task := rctx.Input.VideoTask; task != nil {
		ui := rctx.Input.UserInfo
		if ui == nil || string(a.Source) != task.Source || a.SourceID != task.ChannelID || a.RealModel != task.Model || !task.Verify(ch, ui.UserID, ui.TokenID) {
			return nil, nil, fmt.Errorf("invalid video task binding")
		}
		_, action, ok := videotask.Path(orig.URL.Path)
		if !ok {
			return nil, nil, fmt.Errorf("invalid video task path")
		}
		endpoint = strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(task.ID) + action
	}
	target, err := protocolconfig.JoinUpstreamURL(ch.GetBaseURL(), endpoint)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, nil, err
	}
	// Video content variants and provider query parameters retain their values.
	parsed.RawQuery = orig.URL.RawQuery
	upstreamModel := state.ApplyModelMapping(ch, a.RealModel)
	body := rctx.Input.Body
	var reader io.ReadCloser
	contentType := orig.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "multipart/form-data" {
		input, err := rctx.Resources.Body().Open()
		if err != nil {
			return nil, nil, err
		}
		pipe, output := io.Pipe()
		writer := multipart.NewWriter(output)
		contentType = writer.FormDataContentType()
		reader = pipe
		go func() {
			defer input.Close()
			err := rewriteMultipart(input, params["boundary"], writer, upstreamModel)
			if err == nil {
				err = writer.Close()
			}
			_ = output.CloseWithError(err)
		}()
	} else if len(body) > 0 {
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || fields == nil {
			return nil, nil, fmt.Errorf("invalid provider request JSON")
		}
		// Remix can omit model; the task binding supplies it for routing only.
		if _, exists := fields["model"]; exists || rctx.Input.VideoTask == nil {
			fields["model"], _ = json.Marshal(upstreamModel)
		}
		body, err = json.Marshal(fields)
		if err != nil {
			return nil, nil, err
		}
		reader = io.NopCloser(bytes.NewReader(body))
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(orig.Context(), orig.Method, parsed.String(), reader)
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, nil, err
	}
	if reader != nil && mediaType != "multipart/form-data" {
		req.ContentLength = int64(len(body))
	}
	copyProviderHeaders(req, orig, ch, contentType)
	return req, body, nil
}

func rewriteMultipart(input io.Reader, boundary string, writer *multipart.Writer, model string) error {
	parts := multipart.NewReader(input, boundary)
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		out, err := writer.CreatePart(part.Header)
		if err != nil {
			_ = part.Close()
			return err
		}
		if part.FormName() == "model" && part.FileName() == "" {
			_, err = io.WriteString(out, model)
		} else {
			_, err = io.Copy(out, part)
		}
		_ = part.Close()
		if err != nil {
			return err
		}
	}
}

// bindVideoResponse replaces only the ID; all native provider result fields are
// retained. The opaque handle carries a signed user/token/channel binding.
func bindVideoResponse(rctx *state.RelayContext, a state.Attempt, resp *http.Response) error {
	if rctx.Input.InboundProto != protocolconfig.ProtocolVideos || strings.HasSuffix(rctx.Request.URL.Path, "/content") {
		return nil
	}
	defer resp.Body.Close()
	const limit = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if len(raw) > limit {
		return fmt.Errorf("video metadata response too large")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("invalid video metadata response")
	}
	var id string
	if value, exists := fields["id"]; exists && json.Unmarshal(value, &id) == nil && id != "" {
		ui := rctx.Input.UserInfo
		if ui == nil {
			return fmt.Errorf("video creator unavailable")
		}
		task := videotask.Task{UserID: ui.UserID, TokenID: ui.TokenID, Source: string(a.Source), ChannelID: a.SourceID, Model: a.RealModel, RequestModel: rctx.Input.Model, ID: id}
		if rctx.Input.VideoTask != nil && !strings.HasSuffix(rctx.Request.URL.Path, "/remix") {
			// GET/DELETE refer to the original task, even if a provider returns an alias.
			fields["id"], _ = json.Marshal(strings.TrimSuffix(strings.TrimPrefix(rctx.Request.URL.Path, "/v1/videos/"), "/content"))
		} else {
			handle, err := task.Seal(a.Channel)
			if err != nil {
				return err
			}
			fields["id"], _ = json.Marshal(handle)
		}
		raw, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	resp.Header.Del("Content-Length")
	resp.Header.Del("ETag")
	resp.Header.Del("Content-MD5")
	return nil
}

func copyProviderHeaders(req, orig *http.Request, ch *models.Channel, contentType string) {
	upstream.ForwardClientHeaders(req.Header, orig.Header, false)
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+ch.Key)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if ch.Organization != "" {
		req.Header.Set(consts.HeaderOpenAIOrg, ch.Organization)
	}
}
