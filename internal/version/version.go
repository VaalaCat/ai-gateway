package version

import (
	"fmt"
	"runtime"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Print() string {
	return fmt.Sprintf("ai-gateway %s\nCommit:  %s\nBuilt:   %s\nGo:      %s",
		Version, Commit, BuildDate, runtime.Version())
}
