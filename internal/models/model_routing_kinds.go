package models

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// MigrateRoutingMemberKinds is the only place which infers legacy targets.
// New writes and runtime traversal require explicit kinds. A legacy indirect
// cycle has path-dependent semantics that cannot be represented by one kind;
// refuse the entire migration rather than silently reroute existing traffic.
func MigrateRoutingMemberKinds(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []ModelRouting
		if err := tx.Order("id").Find(&rows).Error; err != nil {
			return err
		}
		members := make(map[uint][]RoutingMember, len(rows))
		globals := map[string]uint{}
		enabled := map[string]bool{}
		legacy := false
		for _, r := range rows {
			if r.Scope == RoutingScopeGlobal {
				globals[r.Name] = r.ID
				enabled[r.Name] = r.Enabled
			}
			var ms []RoutingMember
			if err := json.Unmarshal([]byte(r.Members), &ms); err != nil {
				return fmt.Errorf("routing %d (%s): invalid members: %w", r.ID, r.Name, err)
			}
			members[r.ID] = ms
			for _, m := range ms {
				if m.Kind == "" {
					legacy = true
				}
			}
		}
		if !legacy {
			return nil
		}
		var channels []Channel
		if err := tx.Where("status = ?", 1).Select("models").Find(&channels).Error; err != nil {
			return err
		}
		real := map[string]bool{}
		for _, ch := range channels {
			for _, name := range strings.Split(ch.Models, ",") {
				real[strings.TrimSpace(name)] = true
			}
		}
		changed := map[uint]bool{}
		for _, r := range rows {
			for i, m := range members[r.ID] {
				if m.Kind != "" {
					continue
				}
				kind := RoutingMemberModel
				if _, found := globals[m.Ref]; found && enabled[m.Ref] {
					kind = RoutingMemberRouting
					if r.Scope == RoutingScopeGlobal && m.Ref == r.Name && real[m.Ref] {
						kind = RoutingMemberModel
					}
				}
				members[r.ID][i].Kind = kind
				changed[r.ID] = true
			}
		}
		done := map[uint]bool{}
		var visit func(uint, []uint, map[uint]bool) error
		visit = func(id uint, path []uint, active map[uint]bool) error {
			if active[id] {
				return fmt.Errorf("ambiguous legacy routing cycle %v: set explicit member kinds before upgrading", append(path, id))
			}
			if done[id] {
				return nil
			}
			active[id] = true
			defer delete(active, id)
			for _, m := range members[id] {
				if m.Kind != RoutingMemberRouting {
					continue
				}
				if next, ok := globals[m.Ref]; ok {
					if err := visit(next, append(append([]uint{}, path...), id), active); err != nil {
						return err
					}
				}
			}
			done[id] = true
			return nil
		}
		for _, id := range globals {
			if err := visit(id, nil, map[uint]bool{}); err != nil {
				return err
			}
		}
		for id := range changed {
			raw, err := json.Marshal(members[id])
			if err != nil {
				return err
			}
			if err := tx.Model(&ModelRouting{}).Where("id = ?", id).UpdateColumn("members", string(raw)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
