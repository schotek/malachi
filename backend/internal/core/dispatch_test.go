// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"fmt"
	"testing"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// An account of a kind this daemon does not know (one a newer version put
// into the store) reaches neither the IMAP nor the Graph side: it has no
// IMAP settings, and the IMAP syncer would dereference them. An account
// without a kind is still IMAP.
func TestKindDispatchLeavesUnknownKindsAlone(t *testing.T) {
	imapSup, graphSup := newFakeSupervisor(), newFakeSupervisor()
	imapOut, graphOut := newFakeOutbox(), newFakeOutbox()
	k := newKindSupervisor(imapSup, graphSup)
	o := newKindOutbox(imapOut, graphOut)

	a := store.Account{ID: "acc_new", Config: api.AccountConfig{Name: "Issues", Email: "me@example.invalid", Kind: "tracker"}}
	k.Start(a)
	o.Start(a)
	k.Restart(a)
	o.Restart(a)
	if k.Trigger(a.ID, "", false) {
		t.Error("a sync was triggered for an unknown kind")
	}
	if _, ok := k.State(a.ID); ok {
		t.Error("an unknown kind has a sync state")
	}
	if o.Wake(a.ID) {
		t.Error("an outbox woke for an unknown kind")
	}
	k.Stop(a.ID)
	o.Stop(a.ID)
	if len(imapSup.calls) != 0 || len(graphSup.calls) != 0 || len(imapOut.recorded()) != 0 || len(graphOut.recorded()) != 0 {
		t.Fatalf("an unknown kind reached a supervisor: imap %v graph %v, outbox imap %v graph %v",
			imapSup.calls, graphSup.calls, imapOut.recorded(), graphOut.recorded())
	}

	old := store.Account{ID: "acc_old", Config: validConfig()}
	old.Config.Kind = ""
	k.Start(old)
	o.Start(old)
	if fmt.Sprint(imapSup.calls) != "[start:acc_old]" || fmt.Sprint(imapOut.recorded()) != "[start:acc_old]" {
		t.Fatalf("an account without a kind: imap %v, outbox %v", imapSup.calls, imapOut.recorded())
	}
}
