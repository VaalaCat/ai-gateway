package dao

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type announcementReadTrace struct {
	logger.Interface
	announcements atomic.Int64
	groups        atomic.Int64
	bodies        atomic.Int64
	fullRows      atomic.Int64
	counts        atomic.Int64
}

func (trace *announcementReadTrace) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	statement, _ := sql()
	statement = strings.ToLower(statement)
	if !strings.HasPrefix(statement, "select ") {
		return
	}
	if strings.Contains(statement, "from `announcements`") {
		trace.announcements.Add(1)
		projection, _, _ := strings.Cut(statement, " from ")
		if strings.Contains(projection, "content") || strings.Contains(projection, "*") {
			trace.bodies.Add(1)
		}
		if strings.Contains(projection, "*") {
			trace.fullRows.Add(1)
		}
	}
	if strings.Contains(statement, "from `announcement_groups`") {
		trace.groups.Add(1)
	}
	if strings.Contains(statement, "count(") && strings.Contains(statement, "announcement") {
		trace.counts.Add(1)
	}
}

func (trace *announcementReadTrace) reset() {
	trace.announcements.Store(0)
	trace.groups.Store(0)
	trace.bodies.Store(0)
	trace.counts.Store(0)
	trace.fullRows.Store(0)
}

func TestAnnouncementStartupLoadsSummariesAndListsStayInMemory(t *testing.T) {
	ctx, db := setupAdminContext(t)
	group := models.UserGroup{Name: "startup-target"}
	require.NoError(t, db.Create(&group).Error)
	user := models.User{Username: "startup-reader", GroupID: group.ID}
	require.NoError(t, db.Create(&user).Error)
	rows := make([]models.Announcement, 17)
	for i := range rows {
		rows[i] = models.Announcement{Title: fmt.Sprint(i), Content: "detail body", CreatedAt: int64(100 + i), ShowForGuests: true, Revision: 1}
	}
	require.NoError(t, db.Create(&rows).Error)
	for i := 0; i < len(rows); i += 2 {
		require.NoError(t, db.Create(&models.AnnouncementGroup{AnnouncementID: rows[i].ID, GroupID: group.ID}).Error)
	}
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	InitializeAnnouncementCache(db)
	require.Positive(t, trace.announcements.Load())
	require.Positive(t, trace.groups.Load())
	require.Zero(t, trace.bodies.Load(), "startup must not select content or full announcement rows")
	require.Zero(t, trace.counts.Load())
	require.Nil(t, cacheForAnnouncements(db).entries, "body storage is allocated only on a detail miss")
	admin := NewAdminQuery(ctx).Announcement()
	guest := NewQuery(NewUserContext(&testApp{db: db}, nil)).Announcement()
	private := NewQuery(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})).Announcement()
	trace.reset()
	for _, list := range []func(AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error){
		admin.List,
		func(options AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error) {
			return announcementPublicPage(guest, options)
		},
		func(options AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error) {
			return announcementPublicPage(private, options)
		},
	} {
		for _, options := range []AnnouncementListOptions{{Page: 3, PageSize: 5}, {PageSize: 10}, {Page: 2, PageSize: 7}, {Page: 100, PageSize: 5}} {
			page, err := list(options)
			require.NoError(t, err)
			require.EqualValues(t, len(rows), page.Total)
			if options.Page == 3 {
				require.Equal(t, rows[6].ID, page.Items[0].ID, "merge global and group IDs before taking the page")
			}
		}
	}
	require.Zero(t, trace.announcements.Load(), "even the first list and deep pages use the resident index")
	require.Zero(t, trace.groups.Load())
	require.Zero(t, trace.counts.Load())
	require.Nil(t, cacheForAnnouncements(db).entries)
	for range 2 {
		detail, err := admin.Get(rows[0].ID)
		require.NoError(t, err)
		require.Equal(t, "detail body", detail.Content)
		require.Equal(t, []uint{group.ID}, detail.GroupIDs)
		detail.GroupIDs[0]++
		detail.Content = "caller mutation"
	}
	_, err := private.Get(rows[0].ID)
	require.NoError(t, err)
	_, err = guest.Get(rows[0].ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, trace.announcements.Load(), "detail loads only its own body and shares it across identities")
	require.EqualValues(t, 1, trace.bodies.Load())
	require.Zero(t, trace.fullRows.Load(), "detail selects content only, not a full announcement")
	require.Zero(t, trace.groups.Load())
}

func TestAnnouncementStartupFailureDoesNotPublishPartialIndex(t *testing.T) {
	ctx, db := setupAdminContext(t)
	row := models.Announcement{Title: "persisted", Content: "body", Revision: 1}
	require.NoError(t, db.Create(&row).Error)
	callback := "test:fail-announcement-startup"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_groups" {
			tx.AddError(errors.New("forced startup failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	require.Panics(t, func() { InitializeAnnouncementCache(db) })
	require.NoError(t, db.Callback().Query().Remove(callback))
	InitializeAnnouncementCache(db)
	page, err := NewAdminQuery(ctx).Announcement().List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, row.ID, page.Items[0].ID)
}

func TestAnnouncementEmptyStartupAndLocalBodyInvalidation(t *testing.T) {
	ctx, db := setupAdminContext(t)
	InitializeAnnouncementCache(db)
	require.Nil(t, cacheForAnnouncements(db).entries)
	mutation := NewAdminMutation(ctx).Announcement()
	admin := NewAdminQuery(ctx).Announcement()
	rows := []models.Announcement{
		{Title: "older", Content: "older body", CreatedAt: 100},
		{Title: "newer", Content: "newer body", CreatedAt: 200},
	}
	for i := range rows {
		require.NoError(t, mutation.Create(&rows[i]))
	}
	require.Nil(t, cacheForAnnouncements(db).entries, "writes must not eagerly populate body storage")
	for _, row := range rows {
		_, err := admin.Get(row.ID)
		require.NoError(t, err)
	}
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	rows[0].Title, rows[0].Pinned, rows[0].ShowForGuests = "retitled", true, true
	require.NoError(t, mutation.Update(&rows[0], true))
	trace.reset()
	page, err := admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.Equal(t, rows[0].ID, page.Items[0].ID)
	require.EqualValues(t, 2, page.Items[0].Revision)
	detail, err := admin.Get(rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "retitled", detail.Title)
	require.Equal(t, "older body", detail.Content)
	require.Zero(t, trace.announcements.Load(), "metadata and revision changes retain the body")
	rows[0].Content = "edited body"
	require.NoError(t, mutation.Update(&rows[0], false))
	trace.reset()
	_, err = admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	_, err = admin.Get(rows[1].ID)
	require.NoError(t, err)
	require.Zero(t, trace.announcements.Load(), "content edits neither warm through lists nor evict another body")
	detail, err = admin.Get(rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "edited body", detail.Content)
	_, err = admin.Get(rows[0].ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, trace.bodies.Load())
	require.NoError(t, mutation.Delete(rows[0].ID))
	trace.reset()
	page, err = admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, rows[1].ID, page.Items[0].ID)
	_, err = admin.Get(rows[0].ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = admin.Get(rows[1].ID)
	require.NoError(t, err)
	require.Zero(t, trace.announcements.Load())
}

func TestAnnouncementRollbackPreservesIndexAndWarmBody(t *testing.T) {
	ctx, db := setupAdminContext(t)
	group := models.UserGroup{Name: "rollback-target"}
	require.NoError(t, db.Create(&group).Error)
	mutation := NewAdminMutation(ctx).Announcement()
	admin := NewAdminQuery(ctx).Announcement()
	row := models.Announcement{Title: "original", Content: "body", GroupIDs: []uint{group.ID}}
	require.NoError(t, mutation.Create(&row))
	_, err := admin.Get(row.ID)
	require.NoError(t, err)
	callback := "test:rollback-announcement-target"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_groups" {
			tx.AddError(errors.New("forced target failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	changed := row
	changed.Title, changed.Content, changed.ShowForGuests, changed.Pinned = "changed", "changed body", true, true
	require.ErrorContains(t, mutation.Update(&changed, true), "forced target failure")
	created := models.Announcement{Title: "uncommitted", Content: "body", GroupIDs: []uint{group.ID}}
	require.ErrorContains(t, mutation.Create(&created), "forced target failure")
	require.NoError(t, db.Callback().Create().Remove(callback))
	deleteCallback := "test:rollback-announcement-delete"
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(deleteCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_seens" {
			tx.AddError(errors.New("forced receipt failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(deleteCallback) })
	require.ErrorContains(t, mutation.Delete(row.ID), "forced receipt failure")
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	page, err := admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, "original", page.Items[0].Title)
	require.False(t, page.Items[0].Pinned)
	page.Items[0].GroupIDs[0]++
	detail, err := admin.Get(row.ID)
	require.NoError(t, err)
	require.Equal(t, "original", detail.Title)
	require.Equal(t, "body", detail.Content)
	require.EqualValues(t, 1, detail.Revision)
	require.Equal(t, []uint{group.ID}, detail.GroupIDs)
	guest, err := NewQuery(NewUserContext(&testApp{db: db}, nil)).Announcement().List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Zero(t, guest.Total)
	require.Zero(t, trace.announcements.Load(), "rolled-back writes preserve warmed bodies and published summaries")
}

func TestAnnouncementBodyEntryEvictionDoesNotEvictOrTouchSummaries(t *testing.T) {
	ctx, db := setupAdminContext(t)
	rows := make([]models.Announcement, announcementCacheEntries+1)
	for i := range rows {
		rows[i] = models.Announcement{Title: "notice", Content: fmt.Sprintf("body-%d", i), CreatedAt: int64(i + 1), Revision: 1}
	}
	rows[0].Pinned = true
	require.NoError(t, db.CreateInBatches(&rows, 100).Error)
	InitializeAnnouncementCache(db)
	admin := NewAdminQuery(ctx).Announcement()
	for _, row := range rows[:announcementCacheEntries] {
		_, err := admin.Get(row.ID)
		require.NoError(t, err)
	}
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	page, err := admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Equal(t, rows[0].ID, page.Items[0].ID)
	require.EqualValues(t, len(rows), page.Total)
	require.Zero(t, trace.announcements.Load())
	_, err = admin.Get(rows[announcementCacheEntries].ID)
	require.NoError(t, err)
	trace.reset()
	_, err = admin.Get(rows[announcementCacheEntries-1].ID)
	require.NoError(t, err)
	require.Zero(t, trace.bodies.Load(), "recent body survives")
	detail, err := admin.Get(rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, rows[0].Content, detail.Content)
	require.EqualValues(t, 1, trace.bodies.Load(), "listing an old body must not promote its LRU entry")
	trace.reset()
	page, err = admin.List(AnnouncementListOptions{Page: 11, PageSize: 100})
	require.NoError(t, err)
	require.EqualValues(t, len(rows), page.Total)
	require.Len(t, page.Items, len(rows)-1000)
	require.Zero(t, trace.announcements.Load(), "body eviction leaves every summary resident")
}

func TestAnnouncementBodyByteBudgetAndOversizedLegacyContent(t *testing.T) {
	ctx, db := setupAdminContext(t)
	// Seed historical rows before initialization; DAO writes retain their normal content limit.
	rows := []models.Announcement{
		{Title: "large-a", Content: strings.Repeat("a", announcementCacheBytes/2+1)},
		{Title: "large-b", Content: strings.Repeat("b", announcementCacheBytes/2+1)},
		{Title: "oversized", Content: strings.Repeat("c", announcementCacheBytes+1)},
	}
	require.NoError(t, db.Create(&rows).Error)
	InitializeAnnouncementCache(db)
	admin := NewAdminQuery(ctx).Announcement()
	for _, row := range rows[:2] {
		detail, err := admin.Get(row.ID)
		require.NoError(t, err)
		require.Equal(t, row.Content, detail.Content)
	}
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	_, err := admin.Get(rows[0].ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, trace.bodies.Load(), "byte cap evicts before the entry cap is reached")
	trace.reset()
	for range 2 {
		detail, err := admin.Get(rows[2].ID)
		require.NoError(t, err)
		require.Equal(t, rows[2].Content, detail.Content)
	}
	require.EqualValues(t, 2, trace.bodies.Load(), "oversized bodies remain readable but are not retained")
	trace.reset()
	_, err = admin.Get(rows[0].ID)
	require.NoError(t, err)
	require.Zero(t, trace.bodies.Load(), "oversized reads do not evict a cacheable body")
}

func TestAnnouncementOldDetailCannotRefillAfterContentEdit(t *testing.T) {
	ctx, db := setupAdminContext(t)
	mutation := NewAdminMutation(ctx).Announcement()
	row := models.Announcement{Title: "old", Content: "old body"}
	require.NoError(t, mutation.Create(&row))
	entered, release := make(chan struct{}), make(chan struct{})
	var pause, resume sync.Once
	unblock := func() { resume.Do(func() { close(release) }) }
	defer unblock()
	callback := "test:pause-announcement-body"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "announcements" && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "content" {
			pause.Do(func() { close(entered); <-release })
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	reader := make(chan error, 1)
	go func() { _, err := NewAdminQuery(ctx).Announcement().Get(row.ID); reader <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("detail body read did not start")
	}
	writer := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		changed := row
		changed.Content = "new body"
		close(started)
		writer <- mutation.Update(&changed, false)
	}()
	<-started
	unblock()
	for _, result := range []<-chan error{reader, writer} {
		select {
		case err := <-result:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent detail or content edit did not finish")
		}
	}
	for range 2 {
		detail, err := NewAdminQuery(ctx).Announcement().Get(row.ID)
		require.NoError(t, err)
		require.Equal(t, "new body", detail.Content)
	}
}
