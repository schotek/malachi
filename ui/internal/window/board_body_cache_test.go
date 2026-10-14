// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestBoardBodyOnlyCacheKeepsAccount(t *testing.T) {
	const id api.MessageID = "board-message"
	lm := &loadedMessage{body: &api.MessageBodyResult{}}
	w := &Window{loaded: map[api.MessageID]*loadedMessage{id: lm}}
	called := false
	w.fetchBodyOnly("account-a", id, func(got *loadedMessage) {
		called = true
		if got != lm {
			t.Fatal("cached body was replaced")
		}
	})
	if !called || lm.account != "account-a" {
		t.Fatal("body-only fetch lost the account identity")
	}
	evictAccount(w.loaded, "account-b")
	if w.loaded[id] != lm {
		t.Fatal("another account's update evicted the board body")
	}
	evictAccount(w.loaded, "account-a")
	if w.loaded[id] != nil {
		t.Fatal("own account update kept stale body")
	}
}
