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

// The transport is line-delimited JSON with a 32 MiB line cap
// (internal/rpc.maxLineBytes). Inline attachment payloads are base64, so
// the limit must leave room for the 4/3 expansion plus the rest of the
// request.
func TestAttachmentDataFitsTransport(t *testing.T) {
	const maxLineBytes = 32 << 20
	if MaxAttachmentDataBytes*4/3+(1<<20) > maxLineBytes {
		t.Fatalf("MaxAttachmentDataBytes %d does not fit the %d transport line cap", MaxAttachmentDataBytes, maxLineBytes)
	}
}
