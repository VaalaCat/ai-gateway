package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	largeMigrationToken = "large-migration-token"
	legacyHistorySeed   = int64(160016)
)

var legacyTailRequestIDs = []string{
	"large-tail-normal",
	"large-tail-stream",
	"large-tail-429",
	"large-tail-500",
	"large-tail-timeout",
	"large-tail-connection",
}

type legacyHistoryOptions struct {
	Days            int
	RequestsPerDay  int
	TraceEvery      int
	BatchSize       int
	Start           time.Time
	MockUpstreamURL string
}

func (o legacyHistoryOptions) validate() error {
	if o.Days <= 0 || o.RequestsPerDay <= 0 || o.TraceEvery <= 0 || o.BatchSize <= 0 {
		return fmt.Errorf("days, requests per day, trace interval, and batch size must be positive")
	}
	if o.RequestsPerDay%o.TraceEvery != 0 {
		return fmt.Errorf("requests per day must be divisible by trace interval")
	}
	if o.Start.IsZero() {
		return fmt.Errorf("history start is required")
	}
	if o.MockUpstreamURL == "" {
		return fmt.Errorf("mock upstream URL is required")
	}
	return nil
}

type legacyHistorySummary struct {
	Seed           int64 `json:"seed"`
	SeedDurationMS int64 `json:"seed_duration_ms"`
	Requests       int64 `json:"requests"`
	Traces         int64 `json:"traces"`
	PromptTokens   int64 `json:"prompt_tokens"`
	OutputTokens   int64 `json:"completion_tokens"`
	TotalCost      int64 `json:"total_cost"`
	MinCreatedAt   int64 `json:"min_created_at"`
	MaxCreatedAt   int64 `json:"max_created_at"`
}

type hourlyHistoryKey struct {
	date string
	hour int
}

type hourlyHistoryTotals struct {
	requests         int64
	promptTokens     int64
	completionTokens int64
	inputCost        int64
	outputCost       int64
	totalCost        int64
	lastUsedAt       int64
}

type legacyHistoryInserter struct {
	tx        *sql.Tx
	usageRows []models.UsageLog
	traceRows []models.UsageLogTrace
}

func appendLegacyTail(rawRoot string) ([]string, error) {
	root, err := validateFixtureRoot(rawRoot)
	if err != nil {
		return nil, err
	}
	marker, err := os.ReadFile(filepath.Join(root, fixtureMarkerName))
	if err != nil || string(marker) != fixtureMarkerContents {
		return nil, fmt.Errorf("append legacy tail requires a valid temporary fixture marker: %w", err)
	}
	legacyPath := filepath.Join(root, "master.db")
	info, err := os.Lstat(legacyPath)
	if err != nil {
		return nil, fmt.Errorf("inspect legacy master.db: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("legacy master.db must be a regular file: %q", legacyPath)
	}
	if err := requireSingleLink(info, legacyPath); err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(legacyPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)&_pragma=synchronous(NORMAL)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open legacy master.db: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()

	err = db.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&models.UsageLog{}).Where("request_id IN ?", legacyTailRequestIDs).Count(&existing).Error; err != nil {
			return fmt.Errorf("inspect legacy tail scenarios: %w", err)
		}
		if existing != 0 {
			return fmt.Errorf("legacy tail scenario already exists")
		}
		var maxUsageID, maxTraceID uint
		if err := tx.Model(&models.UsageLog{}).Select("COALESCE(MAX(id), 0)").Scan(&maxUsageID).Error; err != nil {
			return fmt.Errorf("read legacy usage cursor: %w", err)
		}
		if err := tx.Model(&models.UsageLogTrace{}).Select("COALESCE(MAX(id), 0)").Scan(&maxTraceID).Error; err != nil {
			return fmt.Errorf("read legacy trace cursor: %w", err)
		}
		createdAt := time.Now().UTC().Unix()
		upstreamStatuses := []int{200, 200, 429, 500, 504, 0}
		usage := make([]models.UsageLog, len(legacyTailRequestIDs))
		traces := make([]models.UsageLogTrace, len(legacyTailRequestIDs))
		for index, requestID := range legacyTailRequestIDs {
			succeeded := index < 2
			usage[index] = models.UsageLog{
				ID: maxUsageID + uint(index) + 1, UserID: 1, TokenID: 1, ChannelID: 1,
				OwnerType: "admin", AgentID: "legacy-tail-agent", ModelName: "mock-" + strings.TrimPrefix(requestID, "large-tail-"),
				PromptTokens: 101 + index, CompletionTokens: 21 + index,
				InputCost: int64(202 + index*2), OutputCost: int64(84 + index*4), TotalCost: int64(286 + index*6),
				IsStream: index == 1, Duration: 900 + index, FirstResponseMs: 120 + index,
				Status: boolToStatus(succeeded), RequestID: requestID, TokenName: "Large migration trace token",
				ChannelName: "Mock scenario upstream", HasTrace: true, CreatedAt: createdAt + int64(index),
			}
			if !succeeded {
				usage[index].ErrorStage = "upstream_dispatch"
				usage[index].ErrorMessage = "legacy tail scenario failure"
			}
			traces[index] = models.UsageLogTrace{
				ID: maxTraceID + uint(index) + 1, RequestID: requestID, AttemptIndex: 0,
				InboundPath: "/v1/chat/completions", OutboundPath: "/v1/chat/completions",
				UpstreamStatus: upstreamStatuses[index], CreatedAt: createdAt + int64(index),
			}
		}
		if err := tx.Create(&usage).Error; err != nil {
			return fmt.Errorf("append legacy usage tail: %w", err)
		}
		if err := tx.Create(&traces).Error; err != nil {
			return fmt.Errorf("append legacy trace tail: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), legacyTailRequestIDs...), nil
}

func boolToStatus(value bool) int {
	if value {
		return 1
	}
	return 0
}

func newLegacyHistoryInserter(db *gorm.DB, requestedBatchSize int) (*legacyHistoryInserter, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	tx, err := sqlDB.Begin()
	if err != nil {
		return nil, err
	}
	return &legacyHistoryInserter{
		tx:        tx,
		usageRows: make([]models.UsageLog, 0, min(requestedBatchSize, 40)),
		traceRows: make([]models.UsageLogTrace, 0, min(requestedBatchSize, 100)),
	}, nil
}

func (i *legacyHistoryInserter) addUsage(row models.UsageLog) error {
	i.usageRows = append(i.usageRows, row)
	if len(i.usageRows) < cap(i.usageRows) {
		return nil
	}
	return i.flushUsage()
}

func (i *legacyHistoryInserter) addTrace(row models.UsageLogTrace) error {
	i.traceRows = append(i.traceRows, row)
	if len(i.traceRows) < cap(i.traceRows) {
		return nil
	}
	return i.flushTraces()
}

func (i *legacyHistoryInserter) flushUsage() error {
	if len(i.usageRows) == 0 {
		return nil
	}
	const tuple = "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	args := make([]any, 0, len(i.usageRows)*21)
	for _, row := range i.usageRows {
		args = append(args,
			row.ID, row.UserID, row.TokenID, row.ChannelID, row.OwnerType, row.AgentID, row.ModelName,
			row.PromptTokens, row.CompletionTokens, row.InputCost, row.OutputCost, row.TotalCost,
			row.IsStream, row.Duration, row.FirstResponseMs, row.Status, row.RequestID,
			row.TokenName, row.ChannelName, row.HasTrace, row.CreatedAt,
		)
	}
	query := `INSERT INTO usage_logs (
		id, user_id, token_id, channel_id, owner_type, agent_id, model_name,
		prompt_tokens, completion_tokens, input_cost, output_cost, total_cost,
		is_stream, duration, first_response_ms, status, request_id,
		token_name, channel_name, has_trace, created_at
	) VALUES ` + strings.TrimSuffix(strings.Repeat(tuple+",", len(i.usageRows)), ",")
	_, err := i.tx.Exec(query, args...)
	i.usageRows = i.usageRows[:0]
	return err
}

func (i *legacyHistoryInserter) flushTraces() error {
	if len(i.traceRows) == 0 {
		return nil
	}
	const tuple = "(?, ?, ?, ?, ?, ?, ?)"
	args := make([]any, 0, len(i.traceRows)*7)
	for _, row := range i.traceRows {
		args = append(args, row.ID, row.RequestID, row.AttemptIndex, row.InboundPath, row.OutboundPath, row.UpstreamStatus, row.CreatedAt)
	}
	query := `INSERT INTO usage_log_traces (
		id, request_id, attempt_index, inbound_path, outbound_path, upstream_status, created_at
	) VALUES ` + strings.TrimSuffix(strings.Repeat(tuple+",", len(i.traceRows)), ",")
	_, err := i.tx.Exec(query, args...)
	i.traceRows = i.traceRows[:0]
	return err
}

func (i *legacyHistoryInserter) commit() error {
	if err := i.flushUsage(); err != nil {
		return err
	}
	if err := i.flushTraces(); err != nil {
		return err
	}
	return i.tx.Commit()
}

func (i *legacyHistoryInserter) rollback() {
	_ = i.tx.Rollback()
}

func seedLegacyHistory(db *gorm.DB, opts legacyHistoryOptions) (legacyHistorySummary, error) {
	if err := opts.validate(); err != nil {
		return legacyHistorySummary{}, err
	}
	password, err := bcrypt.GenerateFromPassword([]byte("large-migration-password"), bcrypt.DefaultCost)
	if err != nil {
		return legacyHistorySummary{}, err
	}
	user := models.User{ID: 1, Username: "admin", Email: "large-migration@example.test", Password: string(password), Role: consts.RoleAdmin, Status: consts.StatusEnabled, GroupID: 1, Quota: 1 << 60, PasswordSet: true}
	token := models.Token{ID: 1, UserID: 1, Key: largeMigrationToken, Name: "Large migration trace token", Status: 1, ExpiredAt: -1, TraceEnabled: true}
	breakerEnabled, maxRetries := false, 0
	resilience := datatypes.NewJSONType(models.ChannelResilience{MaxRetries: &maxRetries, BreakerEnabled: &breakerEnabled})
	channels := []models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Name: "Mock scenario upstream", Type: 1, Status: 1, BaseURL: opts.MockUpstreamURL, Weight: 1}, Key: "mock-key", Models: "mock-success,mock-no-usage,mock-stream,mock-429,mock-500,mock-timeout", Resilience: resilience, PriceRatio: 1},
		{ChannelCore: models.ChannelCore{ID: 2, Name: "Mock connection failure", Type: 1, Status: 1, BaseURL: "http://127.0.0.1:1", Weight: 1}, Key: "mock-key", Models: "mock-connection", Resilience: resilience, PriceRatio: 1},
	}
	for _, value := range []any{&user, &token, &channels} {
		if err := db.Create(value).Error; err != nil {
			return legacyHistorySummary{}, err
		}
	}

	start := opts.Start.UTC().Truncate(24 * time.Hour)
	inserter, err := newLegacyHistoryInserter(db, opts.BatchSize)
	if err != nil {
		return legacyHistorySummary{}, err
	}
	defer inserter.rollback()
	hourly := make(map[hourlyHistoryKey]hourlyHistoryTotals, opts.Days*24)
	daily := make(map[string]hourlyHistoryTotals, opts.Days)
	summary := legacyHistorySummary{Seed: legacyHistorySeed}
	for day := 0; day < opts.Days; day++ {
		dayStart := start.AddDate(0, 0, day)
		for request := 0; request < opts.RequestsPerDay; request++ {
			global := int64(day*opts.RequestsPerDay + request + 1)
			createdAt := dayStart.Add(time.Duration(int64(request) * int64(24*time.Hour) / int64(opts.RequestsPerDay))).Unix()
			requestID := fmt.Sprintf("large-history-%09d", global)
			prompt, completion := 100+int(global%900), 20+int(global%180)
			totalCost := int64(prompt*2 + completion*4)
			hasTrace := (request+1)%opts.TraceEvery == 0
			usage := models.UsageLog{
				ID:     uint(global),
				UserID: 1, TokenID: 1, ChannelID: 1, OwnerType: "admin", AgentID: "legacy-agent",
				ModelName: "legacy/history-model", PromptTokens: prompt, CompletionTokens: completion,
				InputCost: int64(prompt * 2), OutputCost: int64(completion * 4), TotalCost: totalCost,
				IsStream: true, Duration: 900, FirstResponseMs: 120, Status: 1, RequestID: requestID,
				TokenName: token.Name, ChannelName: channels[0].Name, HasTrace: hasTrace, CreatedAt: createdAt,
			}
			if err := inserter.addUsage(usage); err != nil {
				return legacyHistorySummary{}, err
			}
			if hasTrace {
				trace := models.UsageLogTrace{ID: uint(summary.Traces + 1), RequestID: requestID, AttemptIndex: 0, InboundPath: "/v1/chat/completions", OutboundPath: "/v1/chat/completions", UpstreamStatus: 200, CreatedAt: createdAt}
				if err := inserter.addTrace(trace); err != nil {
					return legacyHistorySummary{}, err
				}
				summary.Traces++
			}
			summary.Requests++
			summary.PromptTokens += int64(prompt)
			summary.OutputTokens += int64(completion)
			summary.TotalCost += totalCost
			if summary.MinCreatedAt == 0 || createdAt < summary.MinCreatedAt {
				summary.MinCreatedAt = createdAt
			}
			summary.MaxCreatedAt = createdAt
			stamp := time.Unix(createdAt, 0).UTC()
			key := hourlyHistoryKey{date: stamp.Format("2006-01-02"), hour: stamp.Hour()}
			totals := hourly[key]
			totals.requests++
			totals.promptTokens += int64(prompt)
			totals.completionTokens += int64(completion)
			totals.inputCost += int64(prompt * 2)
			totals.outputCost += int64(completion * 4)
			totals.totalCost += totalCost
			totals.lastUsedAt = createdAt
			hourly[key] = totals
			dayTotals := daily[key.date]
			dayTotals.requests++
			dayTotals.promptTokens += int64(prompt)
			dayTotals.completionTokens += int64(completion)
			dayTotals.inputCost += int64(prompt * 2)
			dayTotals.outputCost += int64(completion * 4)
			dayTotals.totalCost += totalCost
			dayTotals.lastUsedAt = createdAt
			daily[key.date] = dayTotals
		}
	}
	if err := inserter.commit(); err != nil {
		return legacyHistorySummary{}, err
	}
	if err := seedLegacyBillingDailyHistory(db, daily, token.Name, channels[0].Name); err != nil {
		return legacyHistorySummary{}, err
	}
	if err := seedLegacyHourlyHistory(db, hourly); err != nil {
		return legacyHistorySummary{}, err
	}
	return summary, nil
}

func seedLegacyBillingDailyHistory(db *gorm.DB, daily map[string]hourlyHistoryTotals, tokenName, channelName string) error {
	dates := make([]string, 0, len(daily))
	for date := range daily {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	tokens := make([]models.TokenDailyBilling, 0, len(dates))
	channels := make([]models.ChannelDailyBilling, 0, len(dates))
	for _, date := range dates {
		totals := daily[date]
		tokens = append(tokens, models.TokenDailyBilling{
			Date: date, UserID: 1, TokenID: 1, TokenName: tokenName,
			RequestCount: totals.requests, SuccessCount: totals.requests,
			PromptTokens: totals.promptTokens, CompletionTokens: totals.completionTokens,
			InputCost: totals.inputCost, OutputCost: totals.outputCost,
			TotalCost: totals.totalCost, LastUsedAt: totals.lastUsedAt,
		})
		channels = append(channels, models.ChannelDailyBilling{
			Date: date, ChannelID: 1, OwnerType: "admin", ChannelName: channelName, ChannelType: 1,
			RequestCount: totals.requests, SuccessCount: totals.requests,
			PromptTokens: totals.promptTokens, CompletionTokens: totals.completionTokens,
			InputCost: totals.inputCost, OutputCost: totals.outputCost,
			TotalCost: totals.totalCost, RawCost: totals.totalCost, LastUsedAt: totals.lastUsedAt,
		})
	}
	if err := db.CreateInBatches(&tokens, 100).Error; err != nil {
		return err
	}
	return db.CreateInBatches(&channels, 100).Error
}

func seedLegacyHourlyHistory(db *gorm.DB, hourly map[hourlyHistoryKey]hourlyHistoryTotals) error {
	usage := make([]models.UsageHourlyBucket, 0, len(hourly))
	duration := make([]models.UsageDurationHistogram, 0, len(hourly))
	ttft := make([]models.UsageTTFTHistogram, 0, len(hourly))
	tps := make([]models.UsageTPSHistogram, 0, len(hourly))
	userTTFT := make([]models.UsageUserTTFTHistogram, 0, len(hourly))
	userTPS := make([]models.UsageUserTPSHistogram, 0, len(hourly))
	keys := make([]hourlyHistoryKey, 0, len(hourly))
	for key := range hourly {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].date == keys[j].date {
			return keys[i].hour < keys[j].hour
		}
		return keys[i].date < keys[j].date
	})
	for _, key := range keys {
		totals := hourly[key]
		count := totals.requests
		at, err := time.Parse("2006-01-02", key.date)
		if err != nil {
			return err
		}
		lastUsed := at.Add(time.Duration(key.hour+1)*time.Hour - time.Second).Unix()
		usage = append(usage, models.UsageHourlyBucket{Date: key.date, Hour: key.hour, ChannelID: 1, ModelName: "legacy/history-model", AgentID: "legacy-agent", OwnerType: "admin", ChannelName: "Mock scenario upstream", RequestCount: count, SuccessCount: count, PromptTokens: totals.promptTokens, CompletionTokens: totals.completionTokens, InputCost: totals.inputCost, OutputCost: totals.outputCost, TotalCost: totals.totalCost, RawCost: totals.totalCost, StreamRequestCount: count, SumFirstResponseMs: count * 120, SumGenerationMs: count * 780, SumStreamCompletionTokens: totals.completionTokens, LastUsedAt: lastUsed})
		duration = append(duration, models.UsageDurationHistogram{Date: key.date, Hour: key.hour, ChannelID: 1, ModelName: "legacy/history-model", AgentID: "legacy-agent", MaxDurationMs: 900, H7: count})
		ttft = append(ttft, models.UsageTTFTHistogram{Date: key.date, Hour: key.hour, ChannelID: 1, ModelName: "legacy/history-model", AgentID: "legacy-agent", MaxFirstResponseMs: 120, H5: count})
		tps = append(tps, models.UsageTPSHistogram{Date: key.date, Hour: key.hour, ChannelID: 1, ModelName: "legacy/history-model", AgentID: "legacy-agent", MaxTps: 140, H10: count})
		userTTFT = append(userTTFT, models.UsageUserTTFTHistogram{Date: key.date, Hour: key.hour, UserID: 1, ModelName: "legacy/history-model", MaxFirstResponseMs: 120, H5: count})
		userTPS = append(userTPS, models.UsageUserTPSHistogram{Date: key.date, Hour: key.hour, UserID: 1, ModelName: "legacy/history-model", MaxTps: 140, H10: count})
	}
	for _, rows := range []any{&usage, &duration, &ttft, &tps, &userTTFT, &userTPS} {
		if err := db.CreateInBatches(rows, 500).Error; err != nil {
			return err
		}
	}
	return nil
}

func prepareLegacyMigrationFixture(fixtureRoot, listen, webOrigin, mockUpstreamURL string, days, requestsPerDay, traceEvery int) error {
	root, err := resetFixtureRoot(fixtureRoot)
	if err != nil {
		return err
	}
	legacyPath := filepath.Join(root, "master.db")
	db, err := gorm.Open(sqlite.Open(legacyPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)&_pragma=synchronous(NORMAL)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	if err := models.AutoMigrate(db); err != nil {
		return err
	}
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)
	seedStarted := time.Now()
	summary, err := seedLegacyHistory(db, legacyHistoryOptions{Days: days, RequestsPerDay: requestsPerDay, TraceEvery: traceEvery, BatchSize: 1000, Start: start, MockUpstreamURL: mockUpstreamURL})
	if err != nil {
		return err
	}
	summary.SeedDurationMS = max(int64(1), time.Since(seedStarted).Milliseconds())
	if err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "legacy-summary.json"), append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	config := fmt.Sprintf(`log_level: info
master:
  listen: %q
  core_db_path: %q
  log_db_path: %q
  legacy_db_path: %q
  jwt_secret: "large-migration-e2e-jwt-secret-32-bytes"
  admin_user: "admin"
  admin_password: "large-migration-password"
  public_base_urls:
    - %q
`, listen, filepath.Join(root, "core.db"), filepath.Join(root, "log.db"), legacyPath, webOrigin)
	return os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o600)
}
