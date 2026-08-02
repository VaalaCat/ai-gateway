package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	fixtureMarkerName     = ".ai-gateway-chart-e2e-fixture"
	fixtureMarkerContents = "ai-gateway-chart-e2e fixture v1\n"
	fixtureUserPassword   = "fixture-password-strong"
)

func main() {
	root := flag.String("root", "/tmp/ai-gateway-chart-e2e", "fixture output directory")
	listen := flag.String("listen", ":8140", "master listen address")
	webOrigin := flag.String("web-origin", "http://localhost:8141", "web origin allowed by the master")
	degraded := flag.Bool("degraded", false, "replace the completed log database with a directory")
	legacyHistory := flag.Bool("legacy-history", false, "prepare a legacy mixed database for startup migration")
	appendLegacyTailRows := flag.Bool("append-legacy-tail", false, "append post-ready scenarios to an existing temporary legacy fixture")
	historyDays := flag.Int("history-days", 90, "legacy history days")
	requestsPerDay := flag.Int("requests-per-day", 10000, "legacy requests per day")
	traceEvery := flag.Int("trace-every", 10, "create one historical trace every N requests")
	mockUpstreamURL := flag.String("mock-upstream-url", "http://127.0.0.1:8342", "mock upstream base URL")
	flag.Parse()
	if *appendLegacyTailRows {
		requestIDs, err := appendLegacyTail(*root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		encoded, err := json.Marshal(requestIDs)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(encoded))
		return
	}
	var err error
	if *legacyHistory {
		err = prepareLegacyMigrationFixture(*root, *listen, *webOrigin, *mockUpstreamURL, *historyDays, *requestsPerDay, *traceEvery)
	} else {
		err = prepare(*root, *listen, *webOrigin, *degraded)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func prepare(fixtureRoot, listen, webOrigin string, degraded bool) error {
	fixtureRoot, err := resetFixtureRoot(fixtureRoot)
	if err != nil {
		return err
	}
	corePath, logPath := filepath.Join(fixtureRoot, "core.db"), filepath.Join(fixtureRoot, "log.db")
	connector := masterdatabase.NewConnector()
	core, err := connector.OpenCorePath(corePath)
	if err != nil {
		return err
	}
	if err := models.MigrateCoreDB(core); err != nil {
		return err
	}
	if err := masterdatabase.InitializeFreshCore(context.Background(), core, nil); err != nil {
		return err
	}
	logs, err := connector.OpenLogPath(logPath)
	if err != nil {
		return err
	}
	if err := models.MigrateLogDB(logs); err != nil {
		return err
	}
	if err := seed(core, logs); err != nil {
		return err
	}
	if err := logs.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("checkpoint log fixture: %w", err)
	}
	coreSQL, err := core.DB()
	if err != nil {
		return err
	}
	logSQL, err := logs.DB()
	if err != nil {
		return err
	}
	if err := coreSQL.Close(); err != nil {
		return err
	}
	if err := logSQL.Close(); err != nil {
		return err
	}
	if degraded {
		if err := writeDegradedBacklog(filepath.Join(fixtureRoot, "log_backlog.snapshot.gz")); err != nil {
			return err
		}
		if err := os.Chmod(logPath, 0o000); err != nil {
			return fmt.Errorf("disable closed log database: %w", err)
		}
	}
	config := fmt.Sprintf(`log_level: error
master:
  listen: %q
  core_db_path: %q
  log_db_path: %q
  legacy_db_path: %q
  jwt_secret: "chart-e2e-jwt-secret-32-bytes-long"
  admin_user: "admin"
  admin_password: "chart-e2e-password-strong"
  public_base_urls:
    - %q
`, listen, corePath, logPath, filepath.Join(fixtureRoot, "legacy.db"), webOrigin)
	return os.WriteFile(filepath.Join(fixtureRoot, "config.yaml"), []byte(config), 0o600)
}

func validateFixtureRoot(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("fixture root is empty")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("fixture root must be absolute: %q", raw)
	}
	root := filepath.Clean(raw)
	basename := filepath.Base(root)
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("resolve temp root: %w", err)
	}
	tempRoot, err = filepath.EvalSymlinks(tempRoot)
	if err != nil {
		return "", fmt.Errorf("canonicalize temp root: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return "", fmt.Errorf("canonicalize fixture parent: %w", err)
	}
	if parent != tempRoot {
		return "", fmt.Errorf("fixture root must be a direct child of %q", tempRoot)
	}
	if !validFixtureBasename(basename) {
		return "", fmt.Errorf("fixture root basename must match ai-gateway-chart-e2e[-...]: %q", basename)
	}
	root = filepath.Join(parent, basename)
	info, err := os.Lstat(root)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("fixture root must not be a symlink: %q", root)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("fixture root must be a directory: %q", root)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect fixture root: %w", err)
	}
	return root, nil
}

func validFixtureBasename(name string) bool {
	const prefix = "ai-gateway-chart-e2e"
	if name == prefix {
		return true
	}
	if !strings.HasPrefix(name, prefix+"-") {
		return false
	}
	for _, char := range strings.TrimPrefix(name, prefix+"-") {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return len(name) > len(prefix)+1
}

func resetFixtureRoot(raw string) (string, error) {
	root, err := validateFixtureRoot(raw)
	if err != nil {
		return "", err
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	workingDirectory, err = filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("canonicalize working directory: %w", err)
	}
	if pathContains(root, workingDirectory) {
		return "", fmt.Errorf("fixture root must not contain the working directory: %q", root)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("canonicalize home directory: %w", err)
	}
	if pathContains(root, home) {
		return "", fmt.Errorf("fixture root must not contain the home directory: %q", root)
	}
	if _, err := os.Stat(root); err == nil {
		markerPath := filepath.Join(root, fixtureMarkerName)
		markerInfo, markerErr := os.Lstat(markerPath)
		if markerErr != nil || !markerInfo.Mode().IsRegular() {
			return "", fmt.Errorf("refuse to replace fixture root without a regular marker: %q", root)
		}
		marker, readErr := os.ReadFile(markerPath)
		if readErr != nil || string(marker) != fixtureMarkerContents {
			return "", fmt.Errorf("refuse to replace fixture root with an invalid marker: %q", root)
		}
		_ = os.Chmod(filepath.Join(root, "log.db"), 0o600)
		if err := os.RemoveAll(root); err != nil {
			return "", fmt.Errorf("remove prior fixture root: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect fixture root: %w", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return "", fmt.Errorf("create fixture root: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600); err != nil {
		return "", fmt.Errorf("write fixture marker: %w", err)
	}
	return root, nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func writeDegradedBacklog(path string) error {
	queue := deliveryqueue.New(
		deliveryqueue.Limits{MaxEntries: 10, MaxBytes: 1 << 20},
		masterlogqueue.BatchSize,
		nil,
	)
	createdAt := time.Now().UTC().Unix()
	for i := 1; i <= 2; i++ {
		requestID := fmt.Sprintf("degraded-backlog-%d", i)
		result := queue.Enqueue(masterlogqueue.LogBatch{
			Request: models.RequestLog{
				RequestID: requestID,
				UserID:    2,
				ModelName: "fixture-recovery-model",
				Status:    1,
				CreatedAt: createdAt,
			},
			Traces: []models.RequestTrace{{RequestID: requestID, AttemptIndex: 0}},
		})
		if !result.Accepted {
			return fmt.Errorf("enqueue degraded fixture batch %q: %s", requestID, result.Error)
		}
	}
	return (&deliveryqueue.Snapshotter[masterlogqueue.LogBatch]{Queue: queue, Path: path}).WriteNow()
}

func seed(core, logs *gorm.DB) error {
	now := time.Now().UTC().Truncate(time.Hour)
	password, err := bcrypt.GenerateFromPassword([]byte(fixtureUserPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash fixture user password: %w", err)
	}
	users := []models.User{
		{ID: 2, Username: "fixture-alice", Email: "fixture-alice@example.test", Password: string(password), Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1, Quota: 1 << 40, PasswordSet: true},
		{ID: 3, Username: "fixture-bob", Email: "fixture-bob@example.test", Password: string(password), Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1, Quota: 1 << 40, PasswordSet: true},
	}
	if err := core.Create(&users).Error; err != nil {
		return err
	}
	tokens := []models.Token{
		{ID: 1, UserID: 2, Key: "fixture-token-alice-a", Name: "Alice trend token A", Status: 1, ExpiredAt: -1},
		{ID: 2, UserID: 2, Key: "fixture-token-alice-b", Name: "Alice trend token B", Status: 1, ExpiredAt: -1},
		{ID: 3, UserID: 3, Key: "fixture-token-bob-a", Name: "Bob trend token A", Status: 1, ExpiredAt: -1},
	}
	if err := core.Create(&tokens).Error; err != nil {
		return err
	}
	channels := make([]models.Channel, 8)
	for index := range channels {
		channels[index] = models.Channel{ChannelCore: models.ChannelCore{ID: uint(index + 1), Name: fmt.Sprintf("production-channel-%02d", index+1), Status: 1}, Models: "*", PriceRatio: 1}
	}
	if err := core.Create(&channels).Error; err != nil {
		return err
	}
	agents := []models.Agent{{AgentID: "fixture-agent-1", Name: "Fixture primary agent", Status: 1, LastSeen: now.Unix()}}
	if err := core.Create(&agents).Error; err != nil {
		return err
	}

	usageBuckets := make([]models.UsageHourlyBucket, 0, 7*24*24)
	ttftHistograms := make([]models.UsageTTFTHistogram, 0, 7*24*24)
	tpsHistograms := make([]models.UsageTPSHistogram, 0, 7*24*24)
	for day := 0; day < 7; day++ {
		for hour := 0; hour < 24; hour++ {
			at := now.Add(-time.Duration(6-day) * 24 * time.Hour).Add(time.Duration(hour-now.Hour()) * time.Hour)
			for series := 0; series < 24; series++ {
				model := fmt.Sprintf("provider/production-model-%02d-with-an-extremely-long-series-name", series+1)
				channelID := uint(series%len(channels) + 1)
				requests := int64(series + 1)
				usage := models.UsageHourlyBucket{
					Date: at.Format("2006-01-02"), Hour: at.Hour(), ChannelID: channelID, ModelName: model, AgentID: agents[0].AgentID,
					OwnerType: "admin", ChannelName: channels[channelID-1].Name, RequestCount: requests, SuccessCount: requests,
					PromptTokens: requests * 200, CompletionTokens: requests * 80, CacheReadTokens: requests * 40, CacheWriteTokens: requests * 10,
					TotalCost: requests * 1000, StreamRequestCount: requests, SumFirstResponseMs: requests * int64(80+series),
					SumGenerationMs: requests * 600, SumStreamCompletionTokens: requests * 80, LastUsedAt: at.Unix(),
				}
				usageBuckets = append(usageBuckets, usage)
				ttftHistograms = append(ttftHistograms, models.UsageTTFTHistogram{Date: usage.Date, Hour: usage.Hour, ChannelID: channelID, ModelName: model, AgentID: agents[0].AgentID, MaxFirstResponseMs: 500, H6: requests})
				tpsHistograms = append(tpsHistograms, models.UsageTPSHistogram{Date: usage.Date, Hour: usage.Hour, ChannelID: channelID, ModelName: model, AgentID: agents[0].AgentID, MaxTps: 100, H9: requests})
			}
		}
	}
	tokenDaily := make([]models.TokenDailyBilling, 0, 7*len(tokens))
	channelDaily := make([]models.ChannelDailyBilling, 0, 7*len(channels))
	for day := 0; day < 7; day++ {
		at := now.Add(-time.Duration(6-day) * 24 * time.Hour)
		date := at.Format("2006-01-02")
		for index, token := range tokens {
			requests := int64((index + 1) * (day + 1) * 10)
			tokenDaily = append(tokenDaily, models.TokenDailyBilling{
				Date: date, UserID: token.UserID, TokenID: token.ID, TokenName: token.Name,
				RequestCount: requests, SuccessCount: requests, PromptTokens: requests * 200,
				CompletionTokens: requests * 80, CacheReadTokens: requests * 40,
				CacheWriteTokens: requests * 10, InputCost: requests * 600,
				OutputCost: requests * 400, TotalCost: requests * 1000, LastUsedAt: at.Unix(),
			})
		}
		for index, channel := range channels {
			requests := int64((index + 1) * (day + 1) * 10)
			channelDaily = append(channelDaily, models.ChannelDailyBilling{
				Date: date, ChannelID: channel.ID, OwnerType: "admin", ChannelName: channel.Name,
				ChannelType: 1, RequestCount: requests, SuccessCount: requests,
				PromptTokens: requests * 200, CompletionTokens: requests * 80,
				CacheReadTokens: requests * 40, CacheWriteTokens: requests * 10,
				InputCost: requests * 600, OutputCost: requests * 400,
				TotalCost: requests * 1000, RawCost: requests * 1000, LastUsedAt: at.Unix(),
			})
		}
	}
	if err := logs.CreateInBatches(&tokenDaily, 100).Error; err != nil {
		return err
	}
	if err := logs.CreateInBatches(&channelDaily, 100).Error; err != nil {
		return err
	}
	requestLogs := make([]models.RequestLog, 0, 12)
	for index := 0; index < 12; index++ {
		channel := channels[index%len(channels)]
		token := tokens[index%len(tokens)]
		requestLogs = append(requestLogs, models.RequestLog{
			UserID: token.UserID, TokenID: token.ID, TokenName: token.Name,
			ChannelID: channel.ID, ChannelName: channel.Name, OwnerType: "admin",
			AgentID: agents[0].AgentID, ModelName: fmt.Sprintf("provider/production-model-%02d-with-an-extremely-long-series-name", index+1),
			PromptTokens: 200 + index, CompletionTokens: 80 + index, TotalCost: int64((index + 1) * 1000),
			IsStream: true, Duration: 700 + index*10, FirstResponseMs: 80 + index,
			RequestID: fmt.Sprintf("fixture-failed-request-%02d", index+1),
			CreatedAt: now.Add(-time.Duration(index) * time.Hour).Unix(), Status: 0,
			ErrorStage:   []string{"upstream_dispatch", "outbound_encode"}[index%2],
			ErrorMessage: "fixture provider failure",
		})
	}
	for _, rows := range []any{&usageBuckets, &ttftHistograms, &tpsHistograms, &requestLogs} {
		if err := logs.CreateInBatches(rows, 500).Error; err != nil {
			return err
		}
	}
	return nil
}
