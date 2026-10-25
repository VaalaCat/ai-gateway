package dao

import (
	"container/list"
	"errors"
	"slices"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

const announcementCacheName = "announcement-memory-v1"
const announcementCacheBytes = 8 << 20
const announcementCacheEntries = 1024

var announcementCacheInstall sync.Mutex

// Summary indexes are resident, not LRU entries. Only IDs are duplicated across
// audiences; content is deliberately absent. transactions protects this state.
type announcementIndex struct {
	byID    map[uint]models.AnnouncementSummary
	all     []uint
	global  []uint
	guests  []uint
	byGroup map[uint][]uint
}

type announcementBody struct {
	id      uint
	content string
	bytes   int
}

type announcementCache struct {
	// Readers may run concurrently. Writers hold this gate through commit and
	// index publication/body invalidation, so old detail reads cannot refill stale
	// bodies after a write. This is database-local, not a cross-process lock.
	transactions sync.RWMutex
	index        announcementIndex
	mu           sync.Mutex
	bytes        int
	entries      map[uint]*list.Element
	lru          list.List
}

func (cache *announcementCache) Name() string { return announcementCacheName }

func (cache *announcementCache) Initialize(db *gorm.DB) error {
	index := announcementIndex{byID: make(map[uint]models.AnnouncementSummary), byGroup: make(map[uint][]uint)}
	err := db.Transaction(func(tx *gorm.DB) error {
		var summaries []models.AnnouncementSummary
		if err := tx.Model(&models.Announcement{}).
			Select("id, title, show_for_guests, pinned, revision, created_at, updated_at").
			Order("pinned DESC, created_at DESC, id DESC").Scan(&summaries).Error; err != nil {
			return err
		}
		for _, row := range summaries {
			row.GroupIDs = []uint{}
			index.byID[row.ID] = row
		}
		var targets []models.AnnouncementGroup
		if err := tx.Order("group_id ASC").Find(&targets).Error; err != nil {
			return err
		}
		for _, target := range targets {
			if row, ok := index.byID[target.AnnouncementID]; ok {
				row.GroupIDs = append(row.GroupIDs, target.GroupID)
				index.byID[row.ID] = row
			}
		}
		// summaries is already ordered, so build every list in linear time.
		for _, summary := range summaries {
			row := index.byID[summary.ID]
			index.all = append(index.all, row.ID)
			if row.ShowForGuests {
				index.guests = append(index.guests, row.ID)
			}
			if len(row.GroupIDs) == 0 {
				index.global = append(index.global, row.ID)
			}
			for _, group := range row.GroupIDs {
				index.byGroup[group] = append(index.byGroup[group], row.ID)
			}
		}
		return nil
	})
	if err == nil {
		cache.index = index
	}
	return err
}

// InitializeAnnouncementCache loads all summaries before routes start serving.
// Failure is fatal rather than exposing a partial or empty announcement index.
func InitializeAnnouncementCache(db *gorm.DB) { cacheForAnnouncements(db) }

func cacheForAnnouncements(db *gorm.DB) *announcementCache {
	announcementCacheInstall.Lock()
	defer announcementCacheInstall.Unlock()
	if plugin, exists := db.Config.Plugins[announcementCacheName]; exists {
		return plugin.(*announcementCache)
	}
	cache := &announcementCache{}
	if err := db.Use(cache); err != nil {
		panic(err)
	}
	return cache
}

func (index *announcementIndex) compare(a, b uint) int {
	x, y := index.byID[a], index.byID[b]
	if x.Pinned != y.Pinned {
		if x.Pinned {
			return -1
		}
		return 1
	}
	if x.CreatedAt != y.CreatedAt {
		if x.CreatedAt > y.CreatedAt {
			return -1
		}
		return 1
	}
	if a > b {
		return -1
	}
	if a < b {
		return 1
	}
	return 0
}

func (index *announcementIndex) insert(ids []uint, id uint) []uint {
	at, _ := slices.BinarySearchFunc(ids, id, index.compare)
	return slices.Insert(ids, at, id)
}

func (index *announcementIndex) remove(ids []uint, id uint) []uint {
	at, found := slices.BinarySearchFunc(ids, id, index.compare)
	if found {
		ids = slices.Delete(ids, at, at+1)
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func (index *announcementIndex) replace(id uint, after *models.Announcement) {
	old, exists := index.byID[id]
	if exists && after != nil && old.Pinned == after.Pinned && old.CreatedAt == after.CreatedAt &&
		old.ShowForGuests == after.ShowForGuests && slices.Equal(old.GroupIDs, after.GroupIDs) {
		index.byID[id] = announcementSummary(*after)
		return
	}
	if exists {
		index.all = index.remove(index.all, id)
		if old.ShowForGuests {
			index.guests = index.remove(index.guests, id)
		}
		if len(old.GroupIDs) == 0 {
			index.global = index.remove(index.global, id)
		}
		for _, group := range old.GroupIDs {
			ids := index.remove(index.byGroup[group], id)
			if len(ids) == 0 {
				delete(index.byGroup, group)
			} else {
				index.byGroup[group] = ids
			}
		}
		delete(index.byID, id)
	}
	if after == nil {
		return
	}
	index.byID[id] = announcementSummary(*after)
	index.all = index.insert(index.all, id)
	if after.ShowForGuests {
		index.guests = index.insert(index.guests, id)
	}
	if len(after.GroupIDs) == 0 {
		index.global = index.insert(index.global, id)
	}
	for _, group := range after.GroupIDs {
		index.byGroup[group] = index.insert(index.byGroup[group], id)
	}
}

// page merges global and group lists without allocating a whole combined list.
// Empty second lists (admin/guest) use direct slicing, including deep pages.
func (index *announcementIndex) page(first, second []uint, offset, limit int) []models.AnnouncementSummary {
	count := min(limit, max(0, len(first)+len(second)-offset))
	rows := make([]models.AnnouncementSummary, 0, count)
	if count == 0 {
		return rows
	}
	if len(first) == 0 {
		first, second = second, nil
	}
	if len(second) == 0 {
		for _, id := range first[offset : offset+count] {
			rows = append(rows, cloneAnnouncementSummary(index.byID[id]))
		}
		return rows
	}
	i, j := 0, 0
	for position := 0; len(rows) < count; position++ {
		var id uint
		if j == len(second) || (i < len(first) && index.compare(first[i], second[j]) < 0) {
			id = first[i]
			i++
		} else {
			id = second[j]
			j++
		}
		if position >= offset {
			rows = append(rows, cloneAnnouncementSummary(index.byID[id]))
		}
	}
	return rows
}

func cloneAnnouncementSummary(row models.AnnouncementSummary) models.AnnouncementSummary {
	row.GroupIDs = append([]uint{}, row.GroupIDs...)
	return row
}

func announcementVisible(row models.AnnouncementSummary, group uint) bool {
	return len(row.GroupIDs) == 0 || slices.Contains(row.GroupIDs, group)
}

func (cache *announcementCache) removeBody(element *list.Element) {
	entry := element.Value.(announcementBody)
	cache.bytes -= entry.bytes
	delete(cache.entries, entry.id)
	cache.lru.Remove(element)
}

// Caller holds mu. Allocation grows with actual use, never with the limits.
func (cache *announcementCache) putBody(id uint, content string) {
	bytes := 128 + len(content)
	if bytes > announcementCacheBytes {
		return
	}
	if old := cache.entries[id]; old != nil {
		cache.removeBody(old)
	}
	for len(cache.entries) >= announcementCacheEntries || cache.bytes+bytes > announcementCacheBytes {
		cache.removeBody(cache.lru.Back())
	}
	if cache.entries == nil {
		cache.entries = make(map[uint]*list.Element)
	}
	cache.entries[id] = cache.lru.PushFront(announcementBody{id, content, bytes})
	cache.bytes += bytes
}

// Only detail requests call body; list requests never access the body LRU.
// Caller holds transactions.RLock through this read and its cache fill.
func (cache *announcementCache) body(db *gorm.DB, id uint) (string, error) {
	cache.mu.Lock()
	if element := cache.entries[id]; element != nil {
		cache.lru.MoveToFront(element)
		body := element.Value.(announcementBody).content
		cache.mu.Unlock()
		return body, nil
	}
	cache.mu.Unlock()
	var row struct{ Content string }
	if err := db.Model(&models.Announcement{}).Select("content").Where("id = ?", id).Take(&row).Error; err != nil {
		return "", err
	}
	cache.mu.Lock()
	cache.putBody(id, row.Content)
	cache.mu.Unlock()
	return row.Content, nil
}

func (m *announcementMutation) transaction(id *uint, write func(*gorm.DB) error) error {
	db := m.ctx.GetCoreDB()
	// Cache publication requires a real commit, not a nested transaction's savepoint.
	if _, inTx := db.Statement.ConnPool.(gorm.TxCommitter); inTx {
		return errors.New("announcement writes require a standalone transaction")
	}
	cache := cacheForAnnouncements(db)
	cache.transactions.Lock()
	defer cache.transactions.Unlock()
	var before, after *models.Announcement
	err := db.Transaction(func(tx *gorm.DB) error {
		load := func() (*models.Announcement, error) {
			var rows []models.Announcement
			if err := tx.Where("id = ?", *id).Find(&rows).Error; err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				return nil, nil
			}
			rows[0].GroupIDs = []uint{}
			if err := tx.Model(&models.AnnouncementGroup{}).Where("announcement_id = ?", *id).Order("group_id ASC").Pluck("group_id", &rows[0].GroupIDs).Error; err != nil {
				return nil, err
			}
			return &rows[0], nil
		}
		var err error
		if *id != 0 {
			before, err = load()
			if err != nil {
				return err
			}
		}
		if err := write(tx); err != nil {
			return err
		}
		after, err = load()
		return err
	})
	if err != nil {
		return err
	}
	cache.index.replace(*id, after)
	if before == nil || after == nil || before.Content != after.Content {
		cache.mu.Lock()
		if element := cache.entries[*id]; element != nil {
			cache.removeBody(element)
		}
		cache.mu.Unlock()
	}
	return nil
}
