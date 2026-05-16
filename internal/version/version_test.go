package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestPrint(t *testing.T) {
	out := Print()
	if !strings.Contains(out, "ai-gateway") {
		t.Errorf("missing 'ai-gateway' in output: %s", out)
	}
	if !strings.Contains(out, runtime.Version()) {
		t.Errorf("missing Go version in output: %s", out)
	}
	if !strings.Contains(out, "Commit:") {
		t.Errorf("missing Commit in output: %s", out)
	}
}
