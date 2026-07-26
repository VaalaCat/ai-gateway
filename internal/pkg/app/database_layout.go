package app

import "fmt"

// DatabaseLayoutMode controls database routing explicitly during the split rollout.
// The zero value preserves the legacy single-database deployment.
type DatabaseLayoutMode uint32

const (
	DatabaseLayoutLegacySingle DatabaseLayoutMode = iota
	DatabaseLayoutSplit
)

func (m DatabaseLayoutMode) Validate() error {
	if m != DatabaseLayoutLegacySingle && m != DatabaseLayoutSplit {
		return fmt.Errorf("invalid database layout mode: %d", m)
	}
	return nil
}
