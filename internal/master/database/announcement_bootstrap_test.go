package database

import (
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestBootstrapAnnouncementsPreservesTargetsReceiptsAndFalseFlag(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	announcement := models.Announcement{Title: "private", Content: "body", ShowForGuests: false, Pinned: true, Revision: 4}
	require.NoError(t, source.Create(&announcement).Error)
	targets := make([]models.AnnouncementGroup, defaultMigrationReadBatch+1)
	receipts := make([]models.AnnouncementSeen, defaultMigrationReadBatch+1)
	for i := range targets {
		targets[i] = models.AnnouncementGroup{AnnouncementID: announcement.ID, GroupID: uint(i + 1)}
		// One user and many announcements exercise the second composite key.
		receipts[i] = models.AnnouncementSeen{UserID: 7, AnnouncementID: uint(i + 1), Revision: 4}
	}
	require.NoError(t, source.CreateInBatches(targets, 200).Error)
	require.NoError(t, source.CreateInBatches(receipts, 200).Error)
	target := openBootstrapTarget(t)
	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1+len(targets)+len(receipts), result.CopiedRows)
	var persisted models.Announcement
	require.NoError(t, target.First(&persisted, announcement.ID).Error)
	require.False(t, persisted.ShowForGuests)
	require.True(t, persisted.Pinned)
	require.EqualValues(t, 4, persisted.Revision)
	var gotTargets []models.AnnouncementGroup
	require.NoError(t, target.Order("group_id ASC").Find(&gotTargets).Error)
	require.Equal(t, targets, gotTargets)
	var gotReceipts []models.AnnouncementSeen
	require.NoError(t, target.Order("announcement_id ASC").Find(&gotReceipts).Error)
	require.Equal(t, receipts, gotReceipts)
}

func TestBootstrapAnnouncementsDefaultsOldReceiptsToFirstRevision(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	// Reproduce a source created before revision-aware announcements existed.
	require.NoError(t, source.Exec("ALTER TABLE announcements DROP COLUMN pinned").Error)
	require.NoError(t, source.Exec("ALTER TABLE announcements DROP COLUMN revision").Error)
	require.NoError(t, source.Exec("ALTER TABLE announcement_seens DROP COLUMN revision").Error)
	require.NoError(t, source.Exec(`INSERT INTO announcements (id, title, content, show_for_guests, created_at, updated_at)
		VALUES (17, 'old notice', 'body', 1, 100, 100)`).Error)
	require.NoError(t, source.Exec(`INSERT INTO announcement_seens (user_id, announcement_id, created_at) VALUES (7, 17, 200)`).Error)
	target := openBootstrapTarget(t)
	_, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	var announcement models.Announcement
	require.NoError(t, target.First(&announcement, 17).Error)
	var receipt models.AnnouncementSeen
	require.NoError(t, target.Where("user_id = ? AND announcement_id = ?", 7, 17).First(&receipt).Error)
	require.EqualValues(t, 1, announcement.Revision)
	require.Equal(t, announcement.Revision, receipt.Revision)
	require.False(t, announcement.Pinned)
}
