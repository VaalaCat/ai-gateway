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
	require.NoError(t, models.MigrateCoreDB(target))
	var after models.Announcement
	require.NoError(t, target.First(&after, announcement.ID).Error)
	require.Equal(t, persisted, after)
	var targetsAfter []models.AnnouncementGroup
	require.NoError(t, target.Order("group_id ASC").Find(&targetsAfter).Error)
	require.Equal(t, targets, targetsAfter)
	var receiptsAfter []models.AnnouncementSeen
	require.NoError(t, target.Order("announcement_id ASC").Find(&receiptsAfter).Error)
	require.Equal(t, receipts, receiptsAfter)
}
