// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// board.setDraft: the user links a reply draft to a case without an
// annotation and with the assistant off; the draft checks are board
// .annotate's; a live link is a conflict; triage keeps the user's link
// (an annotation without a draft keeps it, one with another draft is
// stored without its draft and says so); a deleted draft frees the case.
func TestBoardSetDraft(t *testing.T) {
	x := newBoardBox(t)
	in := x.put(bmail{folder: x.inbox, thread: "t_s", rfc: "s1", from: boardAlice, to: []api.Address{boardMe}, subject: "Plan",
		text: "Shall we meet on Monday?"})
	other := x.put(bmail{folder: x.inbox, thread: "t_o", rfc: "o1", from: boardBob, to: []api.Address{boardMe}, subject: "Other", text: "x", at: time.Hour})
	x.drain()
	c := x.caseOf("t_s")
	acc := api.AccountID(x.acc)
	save := func(d api.Draft) api.DraftID {
		t.Helper()
		d.AccountID = acc
		res, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: d})
		if err != nil {
			t.Fatal(err)
		}
		return res.DraftID
	}
	reply := save(api.Draft{To: []api.Address{boardAlice}, Subject: "Re: Plan", TextBody: "Monday works.", InReplyTo: api.MessageID(in)})
	reply2 := save(api.Draft{To: []api.Address{boardAlice}, Subject: "Re: Plan", TextBody: "Tuesday?", InReplyTo: api.MessageID(in)})
	stray := save(api.Draft{To: []api.Address{boardBob}, Subject: "Re: Other", TextBody: "no", InReplyTo: api.MessageID(other)})
	fresh := save(api.Draft{To: []api.Address{boardAlice}, Subject: "Hello", TextBody: "new"})
	elsewhere := seedAccount(t, x.b, "else@example.invalid")
	foreign, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: api.AccountID(elsewhere),
		To: []api.Address{boardAlice}, Subject: "Re: Plan", TextBody: "x", InReplyTo: api.MessageID(in)}})
	if err != nil {
		t.Fatal(err)
	}
	set := func(caseID api.BoardCaseID, d api.DraftID) (*api.BoardSetDraftResult, error) {
		return x.svc.SetDraft(x.ctx, api.BoardSetDraftParams{CaseID: caseID, DraftID: d})
	}

	_, err = set("", reply)
	wantCode(t, "no case id", err, api.CodeInvalidArgument)
	_, err = set(c.ID, "")
	wantCode(t, "no draft id", err, api.CodeInvalidArgument)
	_, err = set("c_00000000000000000000000000000000", reply)
	wantCode(t, "unknown case", err, api.CodeCaseNotFound)
	for name, d := range map[string]api.DraftID{
		"unknown draft": "d_nope", "a draft of another thread": stray, "a draft that replies to nothing": fresh,
		"a draft of another account": foreign.DraftID, "hostile id": "d_\x00‮../" + reply,
	} {
		_, err = set(c.ID, d)
		wantCode(t, name, err, api.CodeInvalidArgument)
	}
	if got := x.caseOf("t_s"); got.Draft != nil || got.Version != c.Version {
		t.Fatalf("refusals changed the case: %+v", got)
	}

	// Linked with the assistant off and no annotation; board.list and
	// board.get show it, and the clients are told.
	x.rec.take()
	res, err := set(c.ID, reply)
	if err != nil || res.Case.Draft == nil || res.Case.Draft.DraftID != reply || res.Case.Draft.Text != "Monday works." ||
		res.Case.Annotation != nil || res.Case.Version == c.Version {
		t.Fatalf("set: %+v %v", res, err)
	}
	waitBoardEvent(t, x.rec, x.acc)
	if l := x.caseOf("t_s"); l.Draft == nil || l.Draft.DraftID != reply {
		t.Fatalf("listed: %+v", l.Draft)
	}
	if g, err := x.svc.Get(x.ctx, api.BoardGetParams{CaseID: c.ID}); err != nil || g.Case.Draft == nil {
		t.Fatalf("get: %+v %v", g, err)
	}
	if again, err := set(c.ID, reply); err != nil || again.Case.Version != res.Case.Version {
		t.Fatalf("the same draft again: %+v %v", again, err)
	}
	_, err = set(c.ID, reply2)
	wantCode(t, "another draft over a live link", err, api.CodeConflict)

	// Triage keeps the user's link.
	x.assistantOn()
	x.drain()
	q, err := x.svc.Queue(x.ctx, api.BoardQueueParams{CaseIDs: []api.BoardCaseID{c.ID}})
	if err != nil || len(q.Items) != 1 || !q.Items[0].HasDraft {
		t.Fatalf("queue: %+v %v", q, err)
	}
	key := q.Items[0].InputKey
	a, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: key, Source: "claude", Title: "Meeting"})
	if err != nil || a.DraftNotLinked || a.Case.Draft == nil || a.Case.Draft.DraftID != reply {
		t.Fatalf("annotation without a draft: %+v %v", a, err)
	}
	a, err = x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: key, Source: "claude", Title: "Again", DraftID: reply2})
	if err != nil || !a.DraftNotLinked || a.Case.Draft == nil || a.Case.Draft.DraftID != reply || a.Case.Annotation == nil || a.Case.Annotation.Title != "Again" {
		t.Fatalf("annotation with another draft: %+v %v", a, err)
	}
	if _, err := x.b.store.GetDraft(x.ctx, x.acc, string(reply2)); err != nil {
		t.Fatalf("the triage's draft is gone: %v", err)
	}
	// The same draft as linked is no refusal.
	if a, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: key, Source: "claude", DraftID: reply}); err != nil || a.DraftNotLinked {
		t.Fatalf("annotation with the linked draft: %+v %v", a, err)
	}

	// The draft deleted (as when it is sent): no draft, and another may be
	// linked; the link is listed with the assistant off again.
	if _, err := x.b.Drafts().Delete(x.ctx, api.DraftDeleteParams{AccountID: acc, DraftID: reply}); err != nil {
		t.Fatal(err)
	}
	if got := x.caseOf("t_s"); got.Draft != nil {
		t.Fatalf("draft after its deletion: %+v", got.Draft)
	}
	if res, err := set(c.ID, reply2); err != nil || res.Case.Draft == nil || res.Case.Draft.DraftID != reply2 {
		t.Fatalf("set after the deletion: %+v %v", res, err)
	}
	p := api.DefaultBoardPreferences()
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: p}); err != nil {
		t.Fatal(err)
	}
	if got := x.caseOf("t_s"); got.Draft == nil || got.Draft.DraftID != reply2 {
		t.Fatalf("listed with the assistant off: %+v", got.Draft)
	}
	if d, err := x.svc.DiscardDraft(x.ctx, api.BoardDiscardDraftParams{CaseID: c.ID}); err != nil || d.Case.Draft != nil {
		t.Fatalf("discard: %+v %v", d, err)
	}

	// The board disabled.
	p.Enabled = false
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: p}); err != nil {
		t.Fatal(err)
	}
	_, err = set(c.ID, reply2)
	wantCode(t, "board disabled", err, api.CodeInvalidArgument)
}
