package sync

import (
	"reflect"
	"sort"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestExpandAudience_OwnerOnly(t *testing.T) {
	a, _ := setupPrivChanDB(t)
	q := dao.NewAdminQuery(dao.NewContext(a))
	a.db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{ID: 10, Type: 1}, OwnerID: 1, Name: "a", Status: 1})

	ids, err := ExpandPrivateChannelAudience(q, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("want [1], got %+v", ids)
	}
}

func TestExpandAudience_WithUserShare(t *testing.T) {
	a, _ := setupPrivChanDB(t)
	q := dao.NewAdminQuery(dao.NewContext(a))
	a.db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{ID: 10, Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	a.db.Create(&models.PrivateChannelShare{ChannelID: 10, TargetType: "user", TargetID: 2})

	ids, _ := ExpandPrivateChannelAudience(q, 10, 1)
	sortUints(ids)
	if !reflect.DeepEqual(ids, []uint{1, 2}) {
		t.Fatalf("want [1 2], got %+v", ids)
	}
}

func TestExpandAudience_WithGroupShare(t *testing.T) {
	a, _ := setupPrivChanDB(t)
	q := dao.NewAdminQuery(dao.NewContext(a))
	a.db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{ID: 10, Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	a.db.Create(&models.PrivateChannelShare{ChannelID: 10, TargetType: "group", TargetID: 5})
	a.db.Create(&models.User{ID: 7, GroupID: 5, Username: "u7"})
	a.db.Create(&models.User{ID: 8, GroupID: 5, Username: "u8"})
	a.db.Create(&models.User{ID: 9, GroupID: 99, Username: "u9"}) // not in shared group

	ids, _ := ExpandPrivateChannelAudience(q, 10, 1)
	sortUints(ids)
	if !reflect.DeepEqual(ids, []uint{1, 7, 8}) {
		t.Fatalf("want [1 7 8], got %+v", ids)
	}
}

func TestExpandAudience_NoChannel(t *testing.T) {
	a, _ := setupPrivChanDB(t)
	q := dao.NewAdminQuery(dao.NewContext(a))
	// No channel, no shares — just returns the owner
	ids, _ := ExpandPrivateChannelAudience(q, 999, 1)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("want [1], got %+v", ids)
	}
}

func TestExpandAudience_UserAndGroupOverlap(t *testing.T) {
	a, _ := setupPrivChanDB(t)
	q := dao.NewAdminQuery(dao.NewContext(a))
	a.db.Create(&models.PrivateChannel{ChannelCore: models.ChannelCore{ID: 10, Type: 1}, OwnerID: 1, Name: "a", Status: 1})
	a.db.Create(&models.PrivateChannelShare{ChannelID: 10, TargetType: "user", TargetID: 7})
	a.db.Create(&models.PrivateChannelShare{ChannelID: 10, TargetType: "group", TargetID: 5})
	a.db.Create(&models.User{ID: 7, GroupID: 5, Username: "u7"}) // user 7 already in group 5

	ids, _ := ExpandPrivateChannelAudience(q, 10, 1)
	sortUints(ids)
	if !reflect.DeepEqual(ids, []uint{1, 7}) {
		t.Fatalf("dedup failed, want [1 7], got %+v", ids)
	}
}

func sortUints(s []uint) { sort.Slice(s, func(i, j int) bool { return s[i] < s[j] }) }
