package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/api.md and this package are the same contract in two forms. Every
// method and notification name must appear in the document.
func TestDocsCoverAllMethods(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "api.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("docs/api.md not found at %s: %v", path, err)
	}
	doc := string(raw)
	for _, name := range append(append([]string{}, AllMethods...), AllNotifications...) {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("%s is not documented in docs/api.md", name)
		}
	}
	for code, name := range codeNames {
		if !strings.Contains(doc, name) {
			t.Errorf("error code %d (%s) is not documented in docs/api.md", code, name)
		}
	}
}

func TestErrorCodesUnique(t *testing.T) {
	seen := map[string]ErrorCode{}
	for code, name := range codeNames {
		if prev, dup := seen[name]; dup {
			t.Errorf("name %q used by %d and %d", name, prev, code)
		}
		seen[name] = code
	}
}
