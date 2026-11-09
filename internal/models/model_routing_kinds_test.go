package models

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRoutingMemberKindsMigration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&ModelRouting{}, &Channel{}))
	require.NoError(t, db.Create(&Channel{ChannelCore: ChannelCore{Name: "official", Status: 1}, Models: "deepseek-flash"}).Error)
	rows := []ModelRouting{
		{Name: "deepseek-flash", Scope: "global", Members: `[{"ref":"deepseek-flash","weight":3},{"ref":"other","weight":1}]`},
		{Name: "outer", Scope: "user", UserID: 1, Members: `[{"ref":"deepseek-flash"}]`},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, MigrateRoutingMemberKinds(db))
	var got []ModelRouting
	require.NoError(t, db.Order("id").Find(&got).Error)
	var first, second []RoutingMember
	require.NoError(t, json.Unmarshal([]byte(got[0].Members), &first))
	require.NoError(t, json.Unmarshal([]byte(got[1].Members), &second))
	require.Equal(t, RoutingMemberModel, first[0].Kind)
	require.Equal(t, 3, first[0].Weight)
	require.Equal(t, RoutingMemberModel, first[1].Kind)
	require.Equal(t, RoutingMemberRouting, second[0].Kind)
	require.NoError(t, MigrateRoutingMemberKinds(db))
	var again []ModelRouting
	require.NoError(t, db.Order("id").Find(&again).Error)
	require.Equal(t, got, again)
}

func TestRoutingMemberKindsMigrationRejectsAmbiguousCycleAtomically(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&ModelRouting{}, &Channel{}))
	rows := []ModelRouting{
		{Name: "safe", Scope: "global", Members: `[{"ref":"real"}]`},
		{Name: "A", Scope: "global", Members: `[{"ref":"B"}]`},
		{Name: "B", Scope: "global", Members: `[{"ref":"A"}]`},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.ErrorContains(t, MigrateRoutingMemberKinds(db), "ambiguous legacy routing cycle")
	var got []ModelRouting
	require.NoError(t, db.Order("id").Find(&got).Error)
	for i := range rows {
		require.Equal(t, rows[i].Members, got[i].Members)
	}
}
