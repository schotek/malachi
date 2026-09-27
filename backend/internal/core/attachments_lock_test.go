// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The daemon must never open its own store lock: on Linux and macOS closing
// any descriptor of the file drops the lock (store.Lock). attachment.import
// takes any absolute path a client names, so it refuses that file, also
// through a link.
func TestAttachmentImportRefusesTheStoreLock(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	lock, err := store.Lock(ctx, b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	paths := []string{lock.Path()}
	if link := filepath.Join(t.TempDir(), "lock-link"); os.Symlink(lock.Path(), link) == nil {
		paths = append(paths, link)
	}
	if hard := filepath.Join(filepath.Dir(lock.Path()), "lock-hardlink"); os.Link(lock.Path(), hard) == nil {
		paths = append(paths, hard)
	}
	for _, p := range paths {
		_, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: "acc", Path: p})
		if err == nil {
			t.Fatalf("%s: imported", p)
		}
		if code := errCode(t, err); code != api.CodeInvalidArgument || !strings.Contains(err.Error(), "store lock") {
			t.Errorf("%s: %v (code %d), want invalidArgument naming the store lock", p, err, code)
		}
	}
}
