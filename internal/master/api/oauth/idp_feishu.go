package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

// FeishuAdapter 实现飞书 v2 OAuth：
// - token endpoint 接受 JSON body，响应顶层除 access_token 外还有 code/error
// - userinfo 响应被包装为 {code, msg, data:{...}}，无标准 sub 字段
// 我们用 union_id 作为 oauth_identities.subject（跨应用稳定，飞书官方推荐）。
type FeishuAdapter struct {
	HTTP *http.Client
}

func NewFeishuAdapter(client *http.Client) *FeishuAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &FeishuAdapter{HTTP: client}
}

func (c *FeishuAdapter) ExchangeCode(ctx context.Context, p *models.OAuthProvider, code, redirectURI string) (*TokenResponse, error) {
	// json.Marshal on map[string]string cannot fail; error intentionally ignored
	payload, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     p.ClientID,
		"client_secret": p.ClientSecret,
		"code":          code,
		"redirect_uri":  redirectURI,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("feishu token endpoint %d: %s", resp.StatusCode, string(body))
	}

	var raw struct {
		Code             int    `json:"code"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int    `json:"expires_in"`
		Scope            string `json:"scope"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("feishu token decode: %w", err)
	}
	if raw.Code != 0 {
		return nil, fmt.Errorf("feishu token: code=%d err=%s desc=%s", raw.Code, raw.Error, raw.ErrorDescription)
	}
	if raw.AccessToken == "" {
		return nil, fmt.Errorf("feishu token: missing access_token")
	}
	return &TokenResponse{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		TokenType:    raw.TokenType,
		ExpiresIn:    raw.ExpiresIn,
		Scope:        raw.Scope,
	}, nil
}

func (c *FeishuAdapter) FetchUserinfo(ctx context.Context, p *models.OAuthProvider, accessToken string) (*UserinfoPayload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("feishu userinfo %d: %s", resp.StatusCode, string(body))
	}

	var raw struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			UnionID         string `json:"union_id"`
			OpenID          string `json:"open_id"`
			UserID          string `json:"user_id"`
			Name            string `json:"name"`
			EnName          string `json:"en_name"`
			Email           string `json:"email"`
			EnterpriseEmail string `json:"enterprise_email"`
			AvatarURL       string `json:"avatar_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("feishu userinfo decode: %w", err)
	}
	if raw.Code != 0 {
		return nil, fmt.Errorf("feishu userinfo: code=%d msg=%s", raw.Code, raw.Msg)
	}

	email := raw.Data.Email
	if email == "" {
		email = raw.Data.EnterpriseEmail
	}
	return &UserinfoPayload{
		Sub:               raw.Data.UnionID,
		Email:             email,
		PreferredUsername: raw.Data.EnName,
		Name:              raw.Data.Name,
		Picture:           raw.Data.AvatarURL,
	}, nil
}
