package private_channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

func TestPrivateChannelTransferMapping(t *testing.T) {
	cipher, err := byokcrypto.NewFromConfig("", "transfer-test-secret")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success decrypts plaintext and typed fields", func(t *testing.T) {
		sealed, err := cipher.Seal("plain-key", 7)
		if err != nil {
			t.Fatal(err)
		}
		row := models.PrivateChannel{
			ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 7, Name: "mine", Status: 0,
			KeyCipher: sealed, Models: datatypes.JSONSlice[string]{"gpt-4o"},
			ModelMapping: datatypes.NewJSONType(map[string]string{"gpt-4o": "upstream"}),
		}
		file, err := privateChannelToFile(row, cipher)
		if err != nil {
			t.Fatal(err)
		}
		if file.Key != "plain-key" || file.Status != 0 || file.ModelMapping["gpt-4o"] != "upstream" {
			t.Fatalf("unexpected file: %#v", file)
		}
	})

	t.Run("failure rejects ciphertext for another owner", func(t *testing.T) {
		sealed, _ := cipher.Seal("plain-key", 7)
		_, err := privateChannelToFile(models.PrivateChannel{OwnerID: 8, KeyCipher: sealed}, cipher)
		if err == nil {
			t.Fatal("owner mismatch decrypted")
		}
	})

	t.Run("boundary preserves disabled and zero fields", func(t *testing.T) {
		input := privateFileToCreateInput(channelfile.BYOKChannel{
			Name: "off", Status: 0, Type: 1, Models: []string{"m"}, ModelMapping: map[string]string{},
		}, "off-2")
		if input.Name != "off-2" || input.Status != 0 || input.Weight != 0 {
			t.Fatalf("unexpected input: %#v", input)
		}
	})
}

func TestPrivateChannelImportHTTP(t *testing.T) {
	t.Run("commit accepts admin file and creates owned BYOK channel", func(t *testing.T) {
		h, ctx, db := newHandlerTestCtx(t)
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"from-admin","status":1,"type":1,"key":"sk-shared","base_url":"https://api.openai.com","models":["gpt-4o"],"model_mapping":{"gpt-4o":"gpt-4o"},"weight":2,"proxy_url":"http://admin-proxy","header_override":"{\"x\":\"y\"}","disable_keepalive":true,"resilience":{"max_retries":2},"price_ratio":2,"free":true,"limit":{"disable_at":1,"rules":[]},"affinity":{}}]}`
		status, response := runPrivateChannelImport(t, h, ctx.App, ctx.UserInfo, false, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d, body = %s", status, response)
		}
		var result channelfile.ImportResult
		if err := json.Unmarshal(response, &result); err != nil {
			t.Fatal(err)
		}
		if result.Kind != channelfile.KindBYOKChannels || result.Created != 1 {
			t.Fatalf("unexpected result: %#v", result)
		}
		var row models.PrivateChannel
		if err := db.First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.OwnerID != ctx.UserInfo.UserID || row.Name != "from-admin" || row.KeyLast4 != "ared" ||
			len(row.Models) != 1 || row.Models[0] != "gpt-4o" || row.ModelMapping.Data()["gpt-4o"] != "gpt-4o" {
			t.Fatalf("unexpected created channel: %#v", row)
		}
	})

	t.Run("cross import still runs BYOK validation", func(t *testing.T) {
		h, ctx, _ := newHandlerTestCtx(t)
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"blocked","status":1,"type":1,"key":"sk-test","base_url":"http://10.0.0.1","models":["gpt-4o"],"model_mapping":{},"affinity":{}}]}`
		status, response := runPrivateChannelImport(t, h, ctx.App, ctx.UserInfo, true, body)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, response)
		}
		var preview channelfile.Preview
		if err := json.Unmarshal(response, &preview); err != nil {
			t.Fatal(err)
		}
		if preview.Kind != channelfile.KindBYOKChannels || preview.Ready != 0 || preview.Failed != 1 {
			t.Fatalf("unexpected preview: %#v", preview)
		}
	})

	t.Run("rejects unknown admin source field before creation", func(t *testing.T) {
		h, ctx, db := newHandlerTestCtx(t)
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"bad","owner_id":99}]}`
		status, _ := runPrivateChannelImport(t, h, ctx.App, ctx.UserInfo, true, body)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		var count int64
		if err := db.Model(&models.PrivateChannel{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("invalid file created %d channels: %v", count, err)
		}
	})

	t.Run("native BYOK file keeps target kind", func(t *testing.T) {
		h, ctx, _ := newHandlerTestCtx(t)
		body := `{"schema_version":1,"kind":"byok_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]}`
		status, response := runPrivateChannelImport(t, h, ctx.App, ctx.UserInfo, true, body)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, response)
		}
		var preview channelfile.Preview
		if err := json.Unmarshal(response, &preview); err != nil {
			t.Fatal(err)
		}
		if preview.Kind != channelfile.KindBYOKChannels || preview.Total != 0 {
			t.Fatalf("unexpected preview: %#v", preview)
		}
	})
}

func runPrivateChannelImport(
	t *testing.T,
	h *Handler,
	application app.Application,
	userInfo *app.UserInfo,
	dryRun bool,
	body string,
) (int, []byte) {
	t.Helper()
	path := "/api/private-channels/import"
	if dryRun {
		path += "?dry_run=true"
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	ginCtx.Set(consts.CtxKeyUserInfo, userInfo)
	h.ImportHTTP(api.NewAdapter(nil, nil, application))(ginCtx)
	return recorder.Code, recorder.Body.Bytes()
}
