package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAdminChannelTransferMapping(t *testing.T) {
	t.Run("success exports plaintext and typed fields", func(t *testing.T) {
		row := models.Channel{
			ChannelCore: models.ChannelCore{Name: "main", Status: 0, Type: 1, Weight: 2},
			Key:         "secret", Models: "a,b", ModelMapping: `{"a":"upstream"}`,
			Limit: datatypes.NewJSONType(models.ChannelLimit{}),
		}
		file, err := adminChannelToFile(row)
		if err != nil {
			t.Fatal(err)
		}
		if file.Key != "secret" || file.Status != 0 || len(file.Models) != 2 || file.ModelMapping["a"] != "upstream" {
			t.Fatalf("unexpected file: %#v", file)
		}
	})

	t.Run("failure rejects corrupt stored mapping", func(t *testing.T) {
		_, err := adminChannelToFile(models.Channel{ModelMapping: "{"})
		if err == nil {
			t.Fatal("corrupt mapping exported")
		}
	})

	t.Run("boundary preserves disabled status and zero overrides", func(t *testing.T) {
		file := channelfile.AdminChannel{Name: "off", Status: 0, PriceRatio: 0}
		input := adminFileToCreateInput(file, "off-2")
		if input.Name != "off-2" || input.Status != 0 || input.PriceRatio != 0 {
			t.Fatalf("unexpected input: %#v", input)
		}
	})
}

func TestAdminChannelImportHTTP(t *testing.T) {
	t.Run("dry-run accepts BYOK file as admin target", func(t *testing.T) {
		db := setupTestDB(t)
		body := `{"schema_version":1,"kind":"byok_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"from-byok","status":1,"type":1,"key":"secret","base_url":"https://api.openai.com","models":["gpt-4o"],"model_mapping":{"gpt-4o":"upstream"},"weight":2,"affinity":{}}]}`
		status, response := runAdminChannelImport(t, db, true, body)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, response)
		}
		var preview channelfile.Preview
		if err := json.Unmarshal(response, &preview); err != nil {
			t.Fatal(err)
		}
		if preview.Kind != channelfile.KindAdminChannels || preview.Ready != 1 || preview.Failed != 0 {
			t.Fatalf("unexpected preview: %#v", preview)
		}
		var count int64
		if err := db.Model(&models.Channel{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("dry-run created %d channels: %v", count, err)
		}
	})

	t.Run("rejects unknown BYOK source field before creation", func(t *testing.T) {
		db := setupTestDB(t)
		body := `{"schema_version":1,"kind":"byok_channels","exported_at":"2026-07-16T00:00:00Z","channels":[{"name":"bad","proxy_url":"http://not-valid-for-byok"}]}`
		status, _ := runAdminChannelImport(t, db, true, body)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		var count int64
		if err := db.Model(&models.Channel{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("invalid file created %d channels: %v", count, err)
		}
	})

	t.Run("native admin file keeps target kind", func(t *testing.T) {
		db := setupTestDB(t)
		body := `{"schema_version":1,"kind":"admin_channels","exported_at":"2026-07-16T00:00:00Z","channels":[]}`
		status, response := runAdminChannelImport(t, db, true, body)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, response)
		}
		var preview channelfile.Preview
		if err := json.Unmarshal(response, &preview); err != nil {
			t.Fatal(err)
		}
		if preview.Kind != channelfile.KindAdminChannels || preview.Total != 0 {
			t.Fatalf("unexpected preview: %#v", preview)
		}
	})
}

func runAdminChannelImport(t *testing.T, db *gorm.DB, dryRun bool, body string) (int, []byte) {
	t.Helper()
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	adapter := api.NewAdapter(nil, nil, application)

	path := "/api/admin/channels/import"
	if dryRun {
		path += "?dry_run=true"
	}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	(&Handler{}).ImportHTTP(adapter)(ginCtx)
	return recorder.Code, recorder.Body.Bytes()
}
