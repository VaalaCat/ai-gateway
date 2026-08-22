package llmkit_test

import (
	"os/exec"
	"strings"
	"testing"
)

var forbiddenDependencies = []string{
	"github.com/VaalaCat/ai-gateway/internal/agent",
	"github.com/VaalaCat/ai-gateway/internal/models",
	"github.com/gin-gonic/gin",
}

func TestNoForbiddenDependencies(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{join .Deps "\n"}}`, ".")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list llmkit dependencies: %v\n%s", err, output)
	}

	for _, dependency := range strings.Fields(string(output)) {
		if forbidden, found := forbiddenDependency(dependency, forbiddenDependencies); found {
			t.Errorf("llmkit dependency %q is forbidden by %q", dependency, forbidden)
		}
	}
}

func TestForbiddenDependencyMatch(t *testing.T) {
	tests := []struct {
		name       string
		dependency string
		want       string
		wantFound  bool
	}{
		{
			name:       "exact forbidden dependency",
			dependency: "github.com/gin-gonic/gin",
			want:       "github.com/gin-gonic/gin",
			wantFound:  true,
		},
		{
			name:       "forbidden product subpackage",
			dependency: "github.com/VaalaCat/ai-gateway/internal/agent/relay",
			want:       "github.com/VaalaCat/ai-gateway/internal/agent",
			wantFound:  true,
		},
		{
			name:       "lookalike package",
			dependency: "example.com/github.com/gin-gonic/gin-compatible",
			wantFound:  false,
		},
		{
			name:       "allowed standard library dependency",
			dependency: "net/http",
			wantFound:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, found := forbiddenDependency(test.dependency, forbiddenDependencies)
			if got != test.want || found != test.wantFound {
				t.Fatalf("forbiddenDependency(%q) = %q, %v; want %q, %v", test.dependency, got, found, test.want, test.wantFound)
			}
		})
	}
}

func forbiddenDependency(dependency string, forbidden []string) (string, bool) {
	for _, packagePath := range forbidden {
		if dependency == packagePath || strings.HasPrefix(dependency, packagePath+"/") {
			return packagePath, true
		}
	}
	return "", false
}
