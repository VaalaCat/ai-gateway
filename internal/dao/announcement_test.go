package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAnnouncementTransactionsAndDeletedGroupIsolation(t *testing.T) {
	ctx, db := setupAdminContext(t)
	group := models.UserGroup{Name: "announcement-target"}
	require.NoError(t, db.Create(&group).Error)
	other := models.UserGroup{Name: "announcement-other"}
	require.NoError(t, db.Create(&other).Error)
	user := models.User{Username: "announcement-recipient", GroupID: other.ID}
	require.NoError(t, db.Create(&user).Error)
	mutation := NewAdminMutation(ctx).Announcement()
	announcement := models.Announcement{Title: "original", Content: "body", GroupIDs: []uint{group.ID}, ShowForGuests: false}
	require.NoError(t, mutation.Create(&announcement))
	userCtx := NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})

	// A vanished target stays private rather than becoming globally visible.
	require.NoError(t, db.Delete(&group).Error)
	rows, err := NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.Empty(t, rows)
	require.ErrorIs(t, NewMutation(userCtx).Announcement().MarkSeen(announcement.ID, announcement.Revision), ErrAnnouncementNotVisible)

	// Failure while replacing recipients must restore both content and targets.
	callback := "test:announcement_target_failure"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_groups" {
			tx.AddError(errors.New("forced target insert failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	changed := models.Announcement{ID: announcement.ID, Title: "changed", Content: "changed body", GroupIDs: []uint{other.ID}, ShowForGuests: true}
	require.ErrorContains(t, mutation.Update(&changed, true), "forced target insert failure")
	var persisted models.Announcement
	require.NoError(t, db.First(&persisted, announcement.ID).Error)
	require.Equal(t, "original", persisted.Title)
	require.False(t, persisted.ShowForGuests)
	require.EqualValues(t, 1, persisted.Revision)
	var targets []models.AnnouncementGroup
	require.NoError(t, db.Where("announcement_id = ?", announcement.ID).Find(&targets).Error)
	require.Equal(t, []models.AnnouncementGroup{{AnnouncementID: announcement.ID, GroupID: group.ID}}, targets)

	created := models.Announcement{Title: "new", Content: "body", GroupIDs: []uint{other.ID}}
	require.ErrorContains(t, mutation.Create(&created), "forced target insert failure")
	var count int64
	require.NoError(t, db.Model(&models.Announcement{}).Where("title = ?", "new").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Callback().Create().Remove(callback))

	// Delete rolls back the parent and targets if receipt cleanup fails.
	require.NoError(t, db.Create(&models.AnnouncementSeen{UserID: user.ID, AnnouncementID: announcement.ID}).Error)
	deleteCallback := "test:announcement_delete_failure"
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(deleteCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_seens" {
			tx.AddError(errors.New("forced receipt cleanup failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(deleteCallback) })
	require.ErrorContains(t, mutation.Delete(announcement.ID), "forced receipt cleanup failure")
	require.NoError(t, db.First(&persisted, announcement.ID).Error)
	require.NoError(t, db.Model(&models.AnnouncementGroup{}).Where("announcement_id = ?", announcement.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAnnouncementSeenSurvivesMembershipAndTargetEdits(t *testing.T) {
	ctx, db := setupAdminContext(t)
	group := models.UserGroup{Name: "announcement-team"}
	require.NoError(t, db.Create(&group).Error)
	user := models.User{Username: "announcement-user", GroupID: group.ID}
	require.NoError(t, db.Create(&user).Error)
	mutation := NewAdminMutation(ctx).Announcement()
	announcement := models.Announcement{Title: "notice", Content: "body", GroupIDs: []uint{group.ID}}
	require.NoError(t, mutation.Create(&announcement))
	userCtx := NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})
	require.NoError(t, NewMutation(userCtx).Announcement().MarkSeen(announcement.ID, announcement.Revision))
	announcement.GroupIDs = []uint{}
	announcement.Content = "edited"
	require.NoError(t, mutation.Update(&announcement, false))
	rows, err := NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].Seen)
	require.Empty(t, rows[0].GroupIDs)
	require.NoError(t, db.Delete(&user).Error)
	rows, err = NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.Empty(t, rows)
	require.ErrorIs(t, NewMutation(userCtx).Announcement().MarkSeen(announcement.ID, announcement.Revision), ErrAnnouncementNotVisible)
}

func TestAnnouncementMultiGroupRecipients(t *testing.T) {
	ctx, db := setupAdminContext(t)
	groups := []models.UserGroup{{Name: "recipient-a"}, {Name: "recipient-b"}, {Name: "excluded"}}
	require.NoError(t, db.Create(&groups).Error)
	announcement := models.Announcement{Title: "shared", Content: "body", GroupIDs: []uint{groups[0].ID, groups[1].ID}}
	require.NoError(t, NewAdminMutation(ctx).Announcement().Create(&announcement))
	for i, group := range groups {
		user := models.User{Username: group.Name, GroupID: group.ID}
		require.NoError(t, db.Create(&user).Error)
		userCtx := NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})
		rows, err := NewQuery(userCtx).Announcement().List()
		require.NoError(t, err)
		if i == 2 {
			require.Empty(t, rows)
			continue
		}
		require.Len(t, rows, 1)
		require.Equal(t, []uint{groups[0].ID, groups[1].ID}, rows[0].GroupIDs)
	}
}

func TestAnnouncementsPinnedChronologicalOrdering(t *testing.T) {
	ctx, db := setupAdminContext(t)
	user := models.User{Username: "ordering-recipient"}
	require.NoError(t, db.Create(&user).Error)
	mutation := NewAdminMutation(ctx).Announcement()
	announcements := []models.Announcement{
		{Title: "newest ordinary", Content: "body", CreatedAt: 300, ShowForGuests: true},
		{Title: "old pinned", Content: "body", CreatedAt: 100, Pinned: true, ShowForGuests: true},
		{Title: "new pinned", Content: "body", CreatedAt: 200, Pinned: true, ShowForGuests: true},
		{Title: "same time larger ID", Content: "body", CreatedAt: 200, Pinned: true, ShowForGuests: true},
		{Title: "old ordinary", Content: "body", CreatedAt: 50, ShowForGuests: true},
	}
	for i := range announcements {
		require.NoError(t, mutation.Create(&announcements[i]))
	}
	want := []uint{announcements[3].ID, announcements[2].ID, announcements[1].ID, announcements[0].ID, announcements[4].ID}
	for _, list := range []func() ([]models.Announcement, error){
		NewAdminQuery(ctx).Announcement().List,
		NewAdminQuery(ctx).Announcement().ListPublic,
	} {
		rows, err := list()
		require.NoError(t, err)
		ids := make([]uint, len(rows))
		for i := range rows {
			ids[i] = rows[i].ID
		}
		require.Equal(t, want, ids)
	}
	userCtx := NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})
	rows, err := NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	ids := make([]uint, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	require.Equal(t, want, ids)

	announcements[0].Pinned = true
	require.NoError(t, mutation.Update(&announcements[0], false))
	rows, err = NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.Equal(t, announcements[0].ID, rows[0].ID)
	announcements[0].Pinned = false
	require.NoError(t, mutation.Update(&announcements[0], false))
	rows, err = NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.Equal(t, announcements[0].ID, rows[3].ID)
}

func TestConcurrentReannouncementsPreserveEveryRevision(t *testing.T) {
	ctx, db := setupAdminContext(t)
	mutation := NewAdminMutation(ctx).Announcement()
	announcement := models.Announcement{Title: "notice", Content: "body"}
	require.NoError(t, mutation.Create(&announcement))
	const updates = 8
	start := make(chan struct{})
	results := make(chan error, updates)
	for range updates {
		go func() {
			copy := announcement
			<-start
			results <- mutation.Update(&copy, true)
		}()
	}
	close(start)
	for range updates {
		require.NoError(t, <-results)
	}
	var persisted models.Announcement
	require.NoError(t, db.First(&persisted, announcement.ID).Error)
	require.EqualValues(t, updates+1, persisted.Revision)
}

func TestConcurrentAcknowledgementCannotConfirmReannouncement(t *testing.T) {
	ctx, db := setupAdminContext(t)
	user := models.User{Username: "concurrent-reader"}
	require.NoError(t, db.Create(&user).Error)
	announcement := models.Announcement{Title: "notice", Content: "body"}
	admin := NewAdminMutation(ctx).Announcement()
	require.NoError(t, admin.Create(&announcement))
	userCtx := NewUserContext(&testApp{db: db}, &app.UserInfo{UserID: user.ID})
	receipts := NewMutation(userCtx).Announcement()
	start := make(chan struct{})
	ack := make(chan error, 1)
	updated := make(chan error, 1)
	go func() {
		<-start
		ack <- receipts.MarkSeen(announcement.ID, 1)
	}()
	go func() {
		copy := announcement
		<-start
		updated <- admin.Update(&copy, true)
	}()
	close(start)
	require.NoError(t, <-updated)
	if err := <-ack; err != nil {
		require.ErrorIs(t, err, ErrAnnouncementRevisionConflict)
	}
	rows, err := NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 2, rows[0].Revision)
	require.False(t, rows[0].Seen)
	require.NoError(t, receipts.MarkSeen(announcement.ID, 2))
	require.ErrorIs(t, receipts.MarkSeen(announcement.ID, 1), ErrAnnouncementRevisionConflict)
	rows, err = NewQuery(userCtx).Announcement().List()
	require.NoError(t, err)
	require.True(t, rows[0].Seen)
}
