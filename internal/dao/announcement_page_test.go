package dao

import (
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAnnouncementNumberPagesAcrossAudiences(t *testing.T) {
	ctx, db := setupAdminContext(t)
	group := models.UserGroup{Name: "paged-group"}
	require.NoError(t, db.Create(&group).Error)
	user := models.User{Username: "paged-reader", GroupID: group.ID}
	require.NoError(t, db.Create(&user).Error)
	mutation := NewAdminMutation(ctx).Announcement()
	for i := range 37 {
		row := models.Announcement{Title: "page", Content: strings.Repeat("body", 100), CreatedAt: int64(100 + i/4), Pinned: i%3 == 0, ShowForGuests: true}
		if i%2 == 0 {
			row.GroupIDs = []uint{group.ID}
		}
		require.NoError(t, mutation.Create(&row))
	}
	admin := NewAdminQuery(ctx).Announcement()
	userQuery := NewQuery(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})).Announcement()
	guestQuery := NewQuery(NewUserContext(&testApp{db: db}, nil)).Announcement()
	var ordered []models.Announcement
	require.NoError(t, db.Order("pinned DESC, created_at DESC, id DESC").Find(&ordered).Error)
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	for name, list := range map[string]func(AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error){
		"admin": admin.List,
		"public": func(options AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error) {
			return announcementPublicPage(guestQuery, options)
		},
		"user": func(options AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error) {
			return announcementPublicPage(userQuery, options)
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Jump directly to page five before reading any preceding pages.
			fifth, err := list(AnnouncementListOptions{Page: 5, PageSize: 7})
			require.NoError(t, err)
			require.Equal(t, ordered[28].ID, fifth.Items[0].ID)
			require.EqualValues(t, 37, fifth.Total)
			var ids []uint
			for number := 1; number <= 6; number++ {
				page, err := list(AnnouncementListOptions{Page: number, PageSize: 7})
				require.NoError(t, err)
				require.Equal(t, number, page.Page)
				require.Equal(t, 7, page.PageSize)
				require.LessOrEqual(t, len(page.Items), 7)
				for _, row := range page.Items {
					ids = append(ids, row.ID)
				}
			}
			beyond, err := list(AnnouncementListOptions{Page: 100, PageSize: 7})
			require.NoError(t, err)
			require.Empty(t, beyond.Items)
			require.EqualValues(t, 37, beyond.Total)
			require.Len(t, ids, len(ordered))
			for i := range ordered {
				require.Equal(t, ordered[i].ID, ids[i])
			}
		})
	}
	page, err := admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Len(t, page.Items, 10)
	for _, options := range []AnnouncementListOptions{{Page: -1}, {PageSize: -1}, {PageSize: 101}, {Page: int(^uint(0) >> 1), PageSize: 100}} {
		_, err := admin.List(options)
		require.ErrorIs(t, err, ErrInvalidAnnouncement)
		_, err = guestQuery.List(options)
		require.ErrorIs(t, err, ErrInvalidAnnouncement)
		_, err = userQuery.List(options)
		require.ErrorIs(t, err, ErrInvalidAnnouncement)
	}
	require.Zero(t, trace.announcements.Load(), "all page numbers and sizes use the resident index")
	require.Zero(t, trace.groups.Load())
	require.Zero(t, trace.counts.Load())
}

func TestAnnouncementNumberPagesInvalidateAfterDeletion(t *testing.T) {
	ctx, db := setupAdminContext(t)
	mutation := NewAdminMutation(ctx).Announcement()
	rows := []models.Announcement{
		{Title: "new unpinned", CreatedAt: 200},
		{Title: "pinned anchor", CreatedAt: 100, Pinned: true},
		{Title: "pinned newer id", CreatedAt: 100, Pinned: true},
		{Title: "old unpinned", CreatedAt: 100},
		{Title: "old unpinned newer id", CreatedAt: 100},
		{Title: "last pinned", CreatedAt: 99, Pinned: true},
	}
	for i := range rows {
		rows[i].Content, rows[i].ShowForGuests = "body", true
		require.NoError(t, mutation.Create(&rows[i]))
	}
	admin := NewAdminQuery(ctx).Announcement()
	guest := NewQuery(NewUserContext(&testApp{db: db}, nil)).Announcement()
	page, err := admin.List(AnnouncementListOptions{PageSize: 2})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, []uint{rows[2].ID, rows[1].ID}, []uint{page.Items[0].ID, page.Items[1].ID})
	require.NoError(t, mutation.Delete(rows[1].ID))
	for _, list := range []func(AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error){
		admin.List,
		func(options AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error) {
			return announcementPublicPage(guest, options)
		},
	} {
		next, err := list(AnnouncementListOptions{PageSize: 2, Page: 2})
		require.NoError(t, err)
		require.Len(t, next.Items, 2)
		require.EqualValues(t, 5, next.Total)
		require.Equal(t, []uint{rows[0].ID, rows[4].ID}, []uint{next.Items[0].ID, next.Items[1].ID})
		last, err := list(AnnouncementListOptions{PageSize: 2, Page: 3})
		require.NoError(t, err)
		require.Len(t, last.Items, 1)
		require.Equal(t, rows[3].ID, last.Items[0].ID)
	}
}

func TestAnnouncementCacheHitsInvalidationAndIsolation(t *testing.T) {
	ctx, db := setupAdminContext(t)
	group := models.UserGroup{Name: "cache-target"}
	require.NoError(t, db.Create(&group).Error)
	user := models.User{Username: "cache-reader", GroupID: group.ID}
	require.NoError(t, db.Create(&user).Error)
	row := models.Announcement{Title: "cached", Content: "old body", ShowForGuests: true, GroupIDs: []uint{group.ID}}
	mutation := NewAdminMutation(ctx).Announcement()
	require.NoError(t, mutation.Create(&row))
	admin := NewAdminQuery(ctx).Announcement()
	private := NewQuery(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})).Announcement()
	guest := NewQuery(NewUserContext(&testApp{db: db}, nil)).Announcement()
	counter := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = counter
	_, err := guest.List(AnnouncementListOptions{})
	require.NoError(t, err)
	_, err = admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	_, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Zero(t, counter.announcements.Load(), "cold list requests do not read announcement rows")
	require.Zero(t, counter.groups.Load())
	_, err = admin.Get(row.ID)
	require.NoError(t, err)
	counter.reset()
	_, err = guest.List(AnnouncementListOptions{})
	require.NoError(t, err)
	_, err = admin.List(AnnouncementListOptions{})
	require.NoError(t, err)
	_, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	detail, err := private.Get(row.ID)
	require.NoError(t, err)
	require.Equal(t, "old body", detail.Content)
	require.Zero(t, counter.announcements.Load(), "lists and warmed detail authorization use summaries, not SQL IDs")
	require.Zero(t, counter.groups.Load())

	require.NoError(t, NewMutation(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})).Announcement().MarkSeen(row.ID, row.Revision))
	page, err := private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.True(t, page.Items[0].Seen, "receipts are not shared cache state")
	otherUser := models.User{Username: "independent-cache-reader", GroupID: group.ID}
	require.NoError(t, db.Create(&otherUser).Error)
	otherQuery := NewQuery(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: otherUser.ID})).Announcement()
	otherPage, err := otherQuery.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.False(t, otherPage.Items[0].Seen)
	otherDetail, err := otherQuery.Get(row.ID)
	require.NoError(t, err)
	require.False(t, otherDetail.Seen, "a shared body never shares recipient receipts")
	row.Content, row.ShowForGuests, row.GroupIDs = "new body", false, []uint{}
	require.NoError(t, mutation.Update(&row, true))
	public, err := guest.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Empty(t, public.Items)
	detail, err = private.Get(row.ID)
	require.NoError(t, err)
	require.Equal(t, "new body", detail.Content)
	require.False(t, detail.Seen)
	require.Empty(t, detail.GroupIDs)
	_, err = guest.Get(row.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	row.ShowForGuests, row.Pinned = true, true
	require.NoError(t, mutation.Update(&row, false))
	public, err = guest.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Len(t, public.Items, 1)
	require.True(t, public.Items[0].Pinned)
	otherGroup := models.UserGroup{Name: "other-cache-target"}
	require.NoError(t, db.Create(&otherGroup).Error)
	row.GroupIDs = []uint{otherGroup.ID}
	require.NoError(t, mutation.Update(&row, false))
	_, err = private.Get(row.ID)
	require.ErrorIs(t, err, ErrAnnouncementNotVisible)
	page, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	row.GroupIDs = []uint{group.ID}
	require.NoError(t, mutation.Update(&row, false))
	page, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NoError(t, db.Model(&user).Update("group_id", group.ID+1).Error)
	_, err = private.Get(row.ID)
	require.ErrorIs(t, err, ErrAnnouncementNotVisible)
	page, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.NoError(t, db.Model(&user).Update("group_id", group.ID).Error)
	page, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	row.GroupIDs = nil
	require.NoError(t, mutation.Update(&row, false))
	require.NoError(t, db.Delete(&user).Error)
	page, err = private.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Zero(t, page.Total, "missing users cannot receive even global announcements")
	_, err = private.Get(row.ID)
	require.ErrorIs(t, err, ErrAnnouncementNotVisible)
	require.ErrorIs(t, NewMutation(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})).Announcement().MarkSeen(row.ID, row.Revision), ErrAnnouncementNotVisible)

	otherCtx, otherDB := setupAdminContext(t)
	require.NotSame(t, cacheForAnnouncements(db), cacheForAnnouncements(otherDB))
	other, err := NewAdminQuery(otherCtx).Announcement().List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Empty(t, other.Items)
	require.NoError(t, mutation.Delete(row.ID))
	public, err = guest.List(AnnouncementListOptions{})
	require.NoError(t, err)
	require.Empty(t, public.Items)
	_, err = admin.Get(row.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAnnouncementSummaryMembershipTotalsFollowCommittedWrites(t *testing.T) {
	ctx, db := setupAdminContext(t)
	groups := []models.UserGroup{{Name: "membership-first"}, {Name: "membership-second"}}
	require.NoError(t, db.Create(&groups).Error)
	users := []models.User{{Username: "membership-first", GroupID: groups[0].ID}, {Username: "membership-second", GroupID: groups[1].ID}}
	require.NoError(t, db.Create(&users).Error)
	mutation := NewAdminMutation(ctx).Announcement()
	global := models.Announcement{Title: "global", Content: "body", CreatedAt: 100}
	target := models.Announcement{Title: "target", Content: "body", CreatedAt: 200, ShowForGuests: true, GroupIDs: []uint{groups[0].ID}}
	require.NoError(t, mutation.Create(&global))
	require.NoError(t, mutation.Create(&target))
	admin := NewAdminQuery(ctx).Announcement()
	guest := NewQuery(NewUserContext(&testApp{db: db}, nil)).Announcement()
	first := NewQuery(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: users[0].ID})).Announcement()
	second := NewQuery(NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: users[1].ID})).Announcement()
	trace := &announcementReadTrace{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = trace
	check := func(adminIDs, guestIDs, firstIDs, secondIDs []uint) {
		t.Helper()
		trace.reset()
		page, err := admin.List(AnnouncementListOptions{})
		require.NoError(t, err)
		pages := []models.AnnouncementPage[models.AnnouncementSummary]{page}
		for _, query := range []UserAnnouncementQuery{guest, first, second} {
			page, err := announcementPublicPage(query, AnnouncementListOptions{})
			require.NoError(t, err)
			pages = append(pages, page)
		}
		for i, want := range [][]uint{adminIDs, guestIDs, firstIDs, secondIDs} {
			require.EqualValues(t, len(want), pages[i].Total, "audience %d", i)
			ids := make([]uint, len(pages[i].Items))
			for j, row := range pages[i].Items {
				ids[j] = row.ID
			}
			require.Equal(t, want, ids, "audience %d", i)
		}
		require.Zero(t, trace.announcements.Load())
		require.Zero(t, trace.groups.Load())
		require.Zero(t, trace.counts.Load(), "totals come from current index lengths, never COUNT")
	}
	all := []uint{target.ID, global.ID}
	check(all, []uint{target.ID}, all, []uint{global.ID})
	target.GroupIDs, target.ShowForGuests = []uint{groups[1].ID}, false
	require.NoError(t, mutation.Update(&target, false))
	check(all, []uint{}, []uint{global.ID}, all)
	target.GroupIDs, target.ShowForGuests = []uint{groups[0].ID, groups[1].ID}, true
	require.NoError(t, mutation.Update(&target, false))
	check(all, []uint{target.ID}, all, all)
	target.GroupIDs, target.ShowForGuests = nil, false
	require.NoError(t, mutation.Update(&target, false))
	check(all, []uint{}, all, all)
	require.NoError(t, mutation.Delete(target.ID))
	check([]uint{global.ID}, []uint{}, []uint{global.ID}, []uint{global.ID})
	added := models.Announcement{Title: "inserted", Content: "body", CreatedAt: 300, ShowForGuests: true}
	require.NoError(t, mutation.Create(&added))
	all = []uint{added.ID, global.ID}
	check(all, []uint{added.ID}, all, all)
}
