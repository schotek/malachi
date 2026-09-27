// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readAPIDoc returns docs/api.md with CRLF line ends made LF (a Windows
// checkout), or skips the test when the file is not there.
func readAPIDoc(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "docs", "api.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("docs/api.md not found at %s: %v", path, err)
	}
	return strings.ReplaceAll(string(raw), "\r", "")
}

// docs/api.md and this package are the same contract in two forms. Every
// method and notification name must appear in the document, and every
// error code as a row of the table in §2.
func TestDocsCoverAllMethods(t *testing.T) {
	doc := readAPIDoc(t)
	for _, name := range append(append([]string{}, AllMethods...), AllNotifications...) {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("%s is not documented in docs/api.md", name)
		}
	}
	for code, name := range codeNames {
		if row := fmt.Sprintf("| %d | %s |", code, name); !strings.Contains(doc, row) {
			t.Errorf("error code %d (%s) has no row %q in the table of docs/api.md §2", code, name, row)
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
