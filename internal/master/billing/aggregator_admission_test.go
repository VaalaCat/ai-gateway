package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func newCoreAggregatorForTest(t *testing.T) (*Aggregator, *testAppProvider) {
	t.Helper()
	_, application := setupTestDB(t)
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetCoreFlushContextFn(func(ctx context.Context, tokens []dao.TokenDailyRow, channels []dao.ChannelDailyRow, hourly []dao.BillingHourlyRow) error {
		return dao.NewAdminMutation(dao.NewContext(application)).Billing().UpsertCoreBillingRows(ctx, tokens, channels, hourly)
	})
	return agg, application
}

func coreFact(requestID string, createdAt int64, promptTokens int) *models.BillingLog {
	return &models.BillingLog{
		RequestID: requestID, UserID: 1, TokenID: 2, TokenName: "token",
		ChannelID: 3, ChannelName: "channel", OwnerType: "admin", ModelName: "m",
		PromptTokens: promptTokens, TotalCost: int64(promptTokens), Status: 1, CreatedAt: createdAt,
	}
}

func rebuildCoreHourThrough(ctx context.Context, application *testAppProvider, date string, hour int, watermark uint) error {
	_, err := dao.NewAdminMutation(dao.NewContext(application)).Billing().
		RebuildCoreHourSliceThroughID(ctx, date, hour, nil, &watermark)
	return err
}

func rebuildCoreDailyThrough(ctx context.Context, application *testAppProvider, date string, watermark uint) error {
	_, err := dao.NewAdminMutation(dao.NewContext(application)).Billing().
		RebuildCoreDailyForDateThroughID(ctx, date, &watermark)
	return err
}

func rebuildAllCoreHours(ctx context.Context, agg *Aggregator, application *testAppProvider, date string, afterHour func(int) error) error {
	for hour := 0; hour < 24; hour++ {
		h := hour
		if err := agg.RunCoreHourRebuildSlice(ctx, date, h, func(watermark uint) error {
			return rebuildCoreHourThrough(ctx, application, date, h, watermark)
		}); err != nil {
			return err
		}
		if afterHour != nil {
			if err := afterHour(hour); err != nil {
				return err
			}
		}
	}
	return nil
}

func coreProjectionPromptTotals(t *testing.T, application *testAppProvider) (int64, int64, int64) {
	t.Helper()
	totals := make([]int64, 3)
	for i, model := range []any{&models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingHourlyBucket{}} {
		require.NoError(t, application.db.Model(model).Select("COALESCE(SUM(prompt_tokens), 0)").Scan(&totals[i]).Error)
	}
	return totals[0], totals[1], totals[2]
}

func requireCorePromptTotals(t *testing.T, application *testAppProvider, want int64) {
	t.Helper()
	token, channel, hourly := coreProjectionPromptTotals(t, application)
	require.Equal(t, want, token)
	require.Equal(t, want, channel)
	require.Equal(t, want, hourly)
}

func requirePromptSubmit(t *testing.T, agg *Aggregator, fact *models.BillingLog) {
	t.Helper()
	done := make(chan struct{})
	go func() { agg.SubmitBilling(fact); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SubmitBilling waited for a rebuild callback or sleep")
	}
}

func TestCoreDateRebuildFutureHourSubmitCountsOnce(t *testing.T) {
	agg, application := newCoreAggregatorForTest(t)
	date := "2026-07-23"
	first := coreFact("hour-0", time.Date(2026, 7, 23, 0, 10, 0, 0, time.UTC).Unix(), 5)
	require.NoError(t, application.db.Create(first).Error)
	afterHour0 := make(chan struct{})
	releaseSleep := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- agg.RunCoreDateRebuild(t.Context(), date, true, func() error {
			return rebuildAllCoreHours(t.Context(), agg, application, date, func(hour int) error {
				if hour == 0 {
					close(afterHour0)
					<-releaseSleep
				}
				return nil
			})
		}, func(watermark uint) error { return rebuildCoreDailyThrough(t.Context(), application, date, watermark) })
	}()
	<-afterHour0
	future := coreFact("hour-5", time.Date(2026, 7, 23, 5, 10, 0, 0, time.UTC).Unix(), 7)
	require.NoError(t, application.db.Create(future).Error)
	requirePromptSubmit(t, agg, future)
	close(releaseSleep)
	require.NoError(t, <-done)
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCorePromptTotals(t, application, 12)
}

func TestCoreDateRebuildHourlyFailureRestoresDailyPending(t *testing.T) {
	agg, application := newCoreAggregatorForTest(t)
	date := "2026-07-23"
	first := coreFact("baseline", time.Date(2026, 7, 23, 1, 10, 0, 0, time.UTC).Unix(), 5)
	require.NoError(t, application.db.Create(first).Error)
	require.NoError(t, rebuildCoreHourThrough(t.Context(), application, date, 1, first.ID))
	require.NoError(t, rebuildCoreDailyThrough(t.Context(), application, date, first.ID))
	dailyCalled := false
	err := agg.RunCoreDateRebuild(t.Context(), date, true, func() error {
		require.NoError(t, agg.RunCoreHourRebuildSlice(t.Context(), date, 1, func(watermark uint) error {
			return rebuildCoreHourThrough(t.Context(), application, date, 1, watermark)
		}))
		second := coreFact("failed-hour", time.Date(2026, 7, 23, 2, 10, 0, 0, time.UTC).Unix(), 7)
		require.NoError(t, application.db.Create(second).Error)
		return agg.RunCoreHourRebuildSlice(t.Context(), date, 2, func(uint) error {
			requirePromptSubmit(t, agg, second)
			return errors.New("hour failed")
		})
	}, func(uint) error { dailyCalled = true; return nil })
	require.ErrorContains(t, err, "hour failed")
	require.False(t, dailyCalled)
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCorePromptTotals(t, application, 12)
}

func TestCoreDateRebuildDailyFailureRollsBackAndRestoresPending(t *testing.T) {
	agg, application := newCoreAggregatorForTest(t)
	date := "2026-07-23"
	first := coreFact("daily-baseline", time.Date(2026, 7, 23, 1, 10, 0, 0, time.UTC).Unix(), 5)
	require.NoError(t, application.db.Create(first).Error)
	require.NoError(t, rebuildCoreDailyThrough(t.Context(), application, date, first.ID))
	var second *models.BillingLog
	err := agg.RunCoreDateRebuild(t.Context(), date, true, func() error {
		return rebuildAllCoreHours(t.Context(), agg, application, date, func(hour int) error {
			if hour == 0 {
				second = coreFact("daily-pending", time.Date(2026, 7, 23, 5, 10, 0, 0, time.UTC).Unix(), 7)
				require.NoError(t, application.db.Create(second).Error)
				requirePromptSubmit(t, agg, second)
			}
			return nil
		})
	}, func(watermark uint) error {
		require.NoError(t, application.db.Exec(`CREATE TRIGGER fail_date_daily_channel BEFORE INSERT ON channel_daily_billings BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`).Error)
		rebuildErr := rebuildCoreDailyThrough(t.Context(), application, date, watermark)
		require.NoError(t, application.db.Exec(`DROP TRIGGER fail_date_daily_channel`).Error)
		return rebuildErr
	})
	require.ErrorContains(t, err, "forced daily failure")
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCorePromptTotals(t, application, 12)
}

func TestCoreDateRebuildZeroWatermarkDailyCallbackDoesNotBlockSubmit(t *testing.T) {
	agg, application := newCoreAggregatorForTest(t)
	date := "2026-07-23"
	callbackEntered := make(chan uint, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- agg.RunCoreDateRebuild(t.Context(), date, true, func() error { return nil }, func(watermark uint) error {
			callbackEntered <- watermark
			<-release
			return rebuildCoreDailyThrough(t.Context(), application, date, watermark)
		})
	}()
	require.Zero(t, <-callbackEntered)
	fact := coreFact("after-zero", time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix(), 9)
	require.NoError(t, application.db.Create(fact).Error)
	requirePromptSubmit(t, agg, fact)
	close(release)
	require.NoError(t, <-done)
	agg.mu.Lock()
	completed, exists := agg.completedDateDaily[date]
	agg.mu.Unlock()
	require.True(t, exists)
	require.Zero(t, completed)
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCorePromptTotals(t, application, 9)
}

func TestCoreDateRebuildSameDateSerialDifferentDatesIndependent(t *testing.T) {
	agg, _ := newCoreAggregatorForTest(t)
	firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- agg.RunCoreDateRebuild(t.Context(), "2026-07-23", true, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		}, func(uint) error { return nil })
	}()
	<-firstEntered

	sameEntered := make(chan struct{})
	sameDone := make(chan error, 1)
	go func() {
		sameDone <- agg.RunCoreDateRebuild(t.Context(), "2026-07-23", true, func() error { close(sameEntered); return nil }, func(uint) error { return nil })
	}()
	otherEntered := make(chan struct{})
	otherDone := make(chan error, 1)
	go func() {
		otherDone <- agg.RunCoreDateRebuild(t.Context(), "2026-07-24", true, func() error { close(otherEntered); return nil }, func(uint) error { return nil })
	}()
	select {
	case <-otherEntered:
	case <-time.After(time.Second):
		t.Fatal("different date rebuild was blocked")
	}
	select {
	case <-sameEntered:
		t.Fatal("same date rebuild was not serialized")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	<-sameEntered
	require.NoError(t, <-sameDone)
	require.NoError(t, <-otherDone)
}

func TestCoreDateRebuildWithoutDailyProjectionOnlyUsesDateGate(t *testing.T) {
	agg, _ := newCoreAggregatorForTest(t)
	date := "2026-07-23"
	rebuildCalled := false
	err := agg.RunCoreDateRebuild(t.Context(), date, false, func() error {
		rebuildCalled = true
		agg.mu.Lock()
		defer agg.mu.Unlock()
		require.NotContains(t, agg.activeDateDaily, date)
		return nil
	}, nil)
	require.NoError(t, err)
	require.True(t, rebuildCalled)
}

func TestCoreDateRebuildFlushFailureDoesNotPublishActiveDate(t *testing.T) {
	_, application := setupTestDB(t)
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetCoreFlushContextFn(func(context.Context, []dao.TokenDailyRow, []dao.ChannelDailyRow, []dao.BillingHourlyRow) error {
		return errors.New("flush failed")
	})
	agg.SubmitBilling(coreFact("buffered", time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix(), 19))
	called := false
	err := agg.RunCoreDateRebuild(t.Context(), "2026-07-23", true, func() error { called = true; return nil }, func(uint) error { return nil })
	require.ErrorContains(t, err, "flush failed")
	require.False(t, called)
	require.Empty(t, agg.activeDateDaily)
}

func newProductionProjectionAggregatorForTest(t *testing.T) (*Aggregator, *testAppProvider) {
	t.Helper()
	_, application := setupTestDB(t)
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetProjectionFlushContextFn(func(ctx context.Context, facts []models.BillingLog) error {
		return application.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return ProjectCommittedBillingFactsInTx(ctx, tx, facts)
		})
	})
	return agg, application
}

func insertAndSubmitPendingFact(t *testing.T, agg *Aggregator, application *testAppProvider, fact *models.BillingLog) {
	t.Helper()
	require.NoError(t, application.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(fact).Error; err != nil {
			return err
		}
		return RegisterPendingBillingProjectionInTx(t.Context(), tx, fact)
	}))
	agg.SubmitPendingBilling(fact)
}

func requireAppliedReceipt(t *testing.T, application *testAppProvider, requestID string) {
	t.Helper()
	var receipt models.BillingProjectionReceipt
	require.NoError(t, application.db.First(&receipt, "request_id = ?", requestID).Error)
	require.Equal(t, models.BillingProjectionApplied, receipt.State)
}

func TestProductionProjectionDefersWholeFactAcrossRebuildAdmission(t *testing.T) {
	date := "2026-07-23"
	for _, test := range []struct {
		name string
		run  func(*Aggregator, *testAppProvider, *models.BillingLog) error
	}{
		{
			name: "date only",
			run: func(agg *Aggregator, application *testAppProvider, fact *models.BillingLog) error {
				return agg.RunCoreDateRebuild(t.Context(), date, true, func() error {
					insertAndSubmitPendingFact(t, agg, application, fact)
					return nil
				}, func(watermark uint) error {
					return rebuildCoreDailyThrough(t.Context(), application, date, watermark)
				})
			},
		},
		{
			name: "hour only",
			run: func(agg *Aggregator, application *testAppProvider, fact *models.BillingLog) error {
				return agg.RunCoreHourRebuildSlice(t.Context(), date, 6, func(watermark uint) error {
					insertAndSubmitPendingFact(t, agg, application, fact)
					return rebuildCoreHourThrough(t.Context(), application, date, 6, watermark)
				})
			},
		},
		{
			name: "date and hour",
			run: func(agg *Aggregator, application *testAppProvider, fact *models.BillingLog) error {
				return agg.RunCoreDateRebuild(t.Context(), date, true, func() error {
					return agg.RunCoreHourRebuildSlice(t.Context(), date, 6, func(watermark uint) error {
						insertAndSubmitPendingFact(t, agg, application, fact)
						return rebuildCoreHourThrough(t.Context(), application, date, 6, watermark)
					})
				}, func(watermark uint) error {
					return rebuildCoreDailyThrough(t.Context(), application, date, watermark)
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			agg, application := newProductionProjectionAggregatorForTest(t)
			fact := coreFact("pending-"+test.name, time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix(), 23)
			require.NoError(t, test.run(agg, application, fact))
			require.NoError(t, agg.FlushContext(t.Context()))
			requireCorePromptTotals(t, application, 23)
			requireAppliedReceipt(t, application, fact.RequestID)
		})
	}
}

func TestProductionProjectionRebuildErrorReleasesWholeFactForRetry(t *testing.T) {
	agg, application := newProductionProjectionAggregatorForTest(t)
	fact := coreFact("rebuild-error-pending", time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix(), 29)
	err := agg.RunCoreHourRebuildSlice(t.Context(), "2026-07-23", 6, func(uint) error {
		insertAndSubmitPendingFact(t, agg, application, fact)
		return errors.New("forced rebuild failure")
	})
	require.ErrorContains(t, err, "forced rebuild failure")
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCorePromptTotals(t, application, 29)
	requireAppliedReceipt(t, application, fact.RequestID)
}

func TestProductionProjectionDeferredFactFlushFailureRetries(t *testing.T) {
	_, application := setupTestDB(t)
	fail := true
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetProjectionFlushContextFn(func(ctx context.Context, facts []models.BillingLog) error {
		if fail {
			return errors.New("forced projection flush failure")
		}
		return application.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return ProjectCommittedBillingFactsInTx(ctx, tx, facts)
		})
	})
	fact := coreFact("deferred-flush-retry", time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix(), 37)
	require.NoError(t, agg.RunCoreHourRebuildSlice(t.Context(), "2026-07-23", 6, func(watermark uint) error {
		insertAndSubmitPendingFact(t, agg, application, fact)
		return rebuildCoreHourThrough(t.Context(), application, "2026-07-23", 6, watermark)
	}))

	require.ErrorContains(t, agg.FlushContext(t.Context()), "forced projection flush failure")
	requireCorePromptTotals(t, application, 0)
	var receipt models.BillingProjectionReceipt
	require.NoError(t, application.db.First(&receipt, "request_id = ?", fact.RequestID).Error)
	require.Equal(t, models.BillingProjectionPending, receipt.State)

	fail = false
	require.NoError(t, agg.FlushContext(t.Context()))
	requireCorePromptTotals(t, application, 37)
	requireAppliedReceipt(t, application, fact.RequestID)
}
