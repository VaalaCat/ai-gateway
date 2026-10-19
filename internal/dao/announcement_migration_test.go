package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAnnouncementMigrationPreservesExistingReadState(t *testing.T) {
	for _, split := range []bool{false, true} {
		name := "legacy"
		if split {
			name = "split core"
		}
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.Exec(`CREATE TABLE announcements (
				id integer PRIMARY KEY, title text NOT NULL, content text NOT NULL,
				show_for_guests numeric NOT NULL, created_at integer, updated_at integer
			)`).Error)
			require.NoError(t, db.Exec(`CREATE TABLE announcement_seens (
				user_id integer, announcement_id integer, created_at integer,
				PRIMARY KEY (user_id, announcement_id)
			)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO announcements VALUES (17, 'old notice', 'body', 1, 100, 100)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO announcement_seens VALUES (7, 17, 200)`).Error)
			migrate := models.AutoMigrate
			if split {
				require.NoError(t, db.AutoMigrate(&models.DatabaseLayout{}))
				require.NoError(t, db.Create(&models.DatabaseLayout{ID: 1, Role: models.DatabaseRoleCore, Version: models.DatabaseLayoutVersion}).Error)
				migrate = models.MigrateCoreDB
			}
			require.NoError(t, migrate(db))
			require.NoError(t, migrate(db))
			require.NoError(t, db.Create(&models.User{ID: 7, Username: "existing-reader"}).Error)
			application := &testApp{db: db}
			if split {
				application.layoutMode = app.DatabaseLayoutSplit
			}
			userCtx := NewUserContext(application, &app.UserInfo{UserID: 7})
			rows, err := NewQuery(userCtx).Announcement().List()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.EqualValues(t, 1, rows[0].Revision)
			require.True(t, rows[0].Seen, "an existing receipt still acknowledges the migrated revision")
			require.False(t, rows[0].Pinned)
			announcement := rows[0].Announcement
			require.NoError(t, NewAdminMutation(NewContext(application)).Announcement().Update(&announcement, true))
			rows, err = NewQuery(userCtx).Announcement().List()
			require.NoError(t, err)
			require.False(t, rows[0].Seen)
			require.EqualValues(t, 2, rows[0].Revision)
		})
	}
}
