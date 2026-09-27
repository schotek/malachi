// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
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

// The preference fields added later are pointers so that config.set can
// tell "absent" (an older client, left unchanged) from false and 0: nil is
// left out of the JSON, a set value is always sent, and decoding keeps the
// difference.
func TestPreferencesJSON(t *testing.T) {
	old := Preferences{SyncIntervalSeconds: 300, RemoteContent: RemoteBlock, OfflineDays: 30}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"syncIntervalSeconds":300,"remoteContent":"block","offlineDays":30}`; string(raw) != want {
		t.Fatalf("nil fields: %s, want %s", raw, want)
	}
	zero := old
	zero.CompressStore, zero.AttachmentOfflineDays = Ptr(false), Ptr(0)
	if raw, err = json.Marshal(zero); err != nil {
		t.Fatal(err)
	}
	if want := `{"syncIntervalSeconds":300,"remoteContent":"block","offlineDays":30,"compressStore":false,"attachmentOfflineDays":0}`; string(raw) != want {
		t.Fatalf("false and 0: %s, want %s", raw, want)
	}

	var p ConfigSetParams
	if err := json.Unmarshal([]byte(`{"preferences":{"syncIntervalSeconds":0,"remoteContent":"allow","offlineDays":0}}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Preferences.CompressStore != nil || p.Preferences.AttachmentOfflineDays != nil {
		t.Fatalf("absent fields decoded as set: %+v", p.Preferences)
	}
	if err := json.Unmarshal([]byte(`{"preferences":{"remoteContent":"block","compressStore":false,"attachmentOfflineDays":-1}}`), &p); err != nil {
		t.Fatal(err)
	}
	if c, d := p.Preferences.CompressStore, p.Preferences.AttachmentOfflineDays; c == nil || *c || d == nil || *d != AttachmentOfflineNone {
		t.Fatalf("false and -1 decoded as %v %v", c, d)
	}
}
