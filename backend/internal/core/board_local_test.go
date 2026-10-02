// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// triggers returns the fake supervisor's trigger calls for the account
// since the last call, and forgets them.
func (x *boardBox) triggers() []string {
	x.t.Helper()
	f := x.b.Supervisor.(*fakeSupervisor)
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "trigger:"+x.acc+":") {
			out = append(out, c)
		}
	}
	f.calls = nil
	return out
}

// reply saves a reply draft to the message in.
func (x *boardBox) reply(in string, local bool) *api.DraftSaveResult {
	x.t.Helper()
	res, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: api.AccountID(x.acc), To: []api.Address{boardAlice},
		Subject: "Re: Lunch", HTMLBody: "<p>Yes.</p>", InReplyTo: api.MessageID(in), Local: local}})
	if err != nil {
		x.t.Fatal(err)
	}
	return res
}

// draft.save honours local on the first save only and never with
// replaces; draft.get and draft.list show it.
func TestDraftLocalAndGet(t *testing.T) {
	x := newBoardBox(t)
	in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	acc := api.AccountID(x.acc)
	local := x.reply(in, true)
	plain := x.reply(in, false)

	got, err := x.b.Drafts().Get(x.ctx, api.DraftGetParams{AccountID: acc, DraftID: local.DraftID})
	if err != nil || !got.Draft.Local || got.Draft.ID != local.DraftID || got.Draft.Version != 1 ||
		got.Draft.HTMLBody != local.HTMLBody || got.Draft.InReplyTo != api.MessageID(in) || got.Draft.Comment != nil {
		t.Fatalf("draft.get = %+v %v", got, err)
	}
	if got, err := x.b.Drafts().Get(x.ctx, api.DraftGetParams{AccountID: acc, DraftID: plain.DraftID}); err != nil || got.Draft.Local {
		t.Fatalf("draft.get of an ordinary draft = %+v %v", got, err)
	}
	list, err := x.b.Drafts().List(x.ctx, api.DraftListParams{AccountID: acc})
	if err != nil || len(list.Drafts) != 2 {
		t.Fatalf("draft.list: %+v %v", list, err)
	}
	for _, d := range list.Drafts {
		if d.Local != (d.ID == local.DraftID) {
			t.Fatalf("draft.list local of %s = %v", d.ID, d.Local)
		}
	}

	// A later save neither clears nor sets it.
	for _, p := range []struct {
		res  *api.DraftSaveResult
		want bool
	}{{local, true}, {plain, false}} {
		saved, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{ID: p.res.DraftID, Version: p.res.Version,
			AccountID: acc, To: []api.Address{boardAlice}, Subject: "Re: Lunch", TextBody: "Yes, sure.", InReplyTo: api.MessageID(in),
			Local: !p.want}})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := x.b.store.GetDraft(x.ctx, x.acc, string(saved.DraftID)); got.Local != p.want {
			t.Fatalf("a later save changed local to %v", got.Local)
		}
	}

	// local and replaces exclude each other; a local draft adopts nothing.
	copyID := x.put(bmail{folder: x.drafts, thread: "t_d", rfc: "c1@x", from: boardMe, to: []api.Address{boardAlice}, text: "old"})
	_, err = x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: acc, Subject: "x", Local: true, Replaces: api.MessageID(copyID)}})
	wantCode(t, "local with replaces", err, api.CodeInvalidArgument)
	cur, _ := x.b.store.GetDraft(x.ctx, x.acc, string(local.DraftID))
	_, err = x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{ID: local.DraftID, Version: cur.Version, AccountID: acc,
		Subject: "x", Replaces: api.MessageID(copyID)}})
	wantCode(t, "a local draft with replaces", err, api.CodeInvalidArgument)

	// draft.get refusals.
	for name, p := range map[string]api.DraftGetParams{
		"no account": {DraftID: local.DraftID},
		"no draft":   {AccountID: acc},
	} {
		_, err := x.b.Drafts().Get(x.ctx, p)
		wantCode(t, name, err, api.CodeInvalidArgument)
	}
	elsewhere := seedAccount(t, x.b, "else@example.invalid")
	for name, p := range map[string]api.DraftGetParams{
		"unknown":            {AccountID: acc, DraftID: "d_nope"},
		"hostile id":         {AccountID: acc, DraftID: "d_\x00‮../" + local.DraftID},
		"another account":    {AccountID: api.AccountID(elsewhere), DraftID: local.DraftID},
		"an unknown account": {AccountID: "a_nope", DraftID: local.DraftID},
	} {
		_, err := x.b.Drafts().Get(x.ctx, p)
		wantCode(t, name, err, api.CodeDraftNotFound)
	}
}

// board.setDraft and board.annotate make the draft local: a copy it had
// in the Drafts folder goes through a queued delete the syncer is woken
// for, and nothing uploads it again; sending it takes the usual path.
func TestBoardLinkMakesDraftLocal(t *testing.T) {
	for _, via := range []string{"setDraft", "annotate"} {
		t.Run(via, func(t *testing.T) {
			x := newBoardBox(t)
			in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
			x.drain()
			x.assistantOn()
			c := x.caseOf("t_a")
			d := x.reply(in, false)
			copyID := x.put(bmail{folder: x.drafts, thread: "t_a", rfc: "dcopy@x", from: boardMe, to: []api.Address{boardAlice}, text: "Yes."})
			if _, err := x.b.store.MarkDraftSynced(x.ctx, x.acc, string(d.DraftID), d.Version, store.DraftCopy{FolderID: x.drafts.ID,
				UID: x.uid, UIDValidity: x.drafts.UIDValidity, RFCMessageID: "dcopy@x"}, false); err != nil {
				t.Fatal(err)
			}
			x.triggers()
			switch via {
			case "setDraft":
				if _, err := x.svc.SetDraft(x.ctx, api.BoardSetDraftParams{CaseID: c.ID, DraftID: d.DraftID}); err != nil {
					t.Fatal(err)
				}
			default:
				res, err := x.svc.Annotate(x.ctx, api.BoardAnnotateParams{CaseID: c.ID, InputKey: x.inputKey(c.ID), Source: "claude", DraftID: d.DraftID})
				if err != nil || res.DraftNotLinked {
					t.Fatalf("annotate: %+v %v", res, err)
				}
			}
			got, err := x.b.store.GetDraft(x.ctx, x.acc, string(d.DraftID))
			if err != nil || !got.Local || !got.Copy.IsZero() {
				t.Fatalf("after the link: %+v %v", got, err)
			}
			if _, err := x.b.store.GetMessage(x.ctx, x.acc, copyID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the copy's row stayed: %v", err)
			}
			if tr := x.triggers(); len(tr) == 0 {
				t.Fatal("the syncer was not woken for the copy's delete")
			}
			ops, _ := x.b.store.NextOps(x.ctx, x.acc, time.Now().Add(time.Hour), 10)
			if len(ops) != 1 || ops[0].Kind != store.OpDelete {
				t.Fatalf("ops after the link: %+v", ops)
			}
			if due, _ := x.b.store.DueDraftUploads(x.ctx, x.acc, time.Now().Add(time.Hour), time.Second, 10); len(due) != 0 {
				t.Fatalf("a linked draft is due: %+v", due)
			}
			if lc := x.caseOf("t_a"); lc.Draft == nil || lc.Draft.DraftID != d.DraftID {
				t.Fatalf("the case does not show its draft: %+v", lc.Draft)
			}

			// Sent as any draft: queued, the row gone.
			res, err := x.b.Messages().Send(x.ctx, api.MessageSendParams{AccountID: api.AccountID(x.acc), DraftID: d.DraftID, Version: got.Version})
			if err != nil || res.OutboxID == "" {
				t.Fatalf("send: %+v %v", res, err)
			}
			if _, err := x.b.store.GetDraft(x.ctx, x.acc, string(d.DraftID)); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the sent draft stayed: %v", err)
			}
		})
	}
}

// board.unflag clears every flagged copy whose flag the rules read, and
// nothing else; the case is judged again and is no longer hot.flagged.
func TestBoardUnflag(t *testing.T) {
	x := newBoardBox(t)
	inbox := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi", flagged: true})
	archived := x.put(bmail{folder: x.archive, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi", flagged: true})
	trashed := x.put(bmail{folder: x.trash, thread: "t_a", rfc: "a2", from: boardAlice, to: []api.Address{boardMe}, text: "Old", flagged: true, at: -time.Hour})
	quiet := x.put(bmail{folder: x.inbox, thread: "t_q", rfc: "q1", from: boardBob, to: []api.Address{boardMe}, text: "Hi", at: time.Minute})
	x.drain()
	c := x.caseOf("t_a")
	if c.RuleReason != api.BoardReasonHotFlagged {
		t.Fatalf("setup: %s", c.RuleReason)
	}
	x.triggers()
	res, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID})
	if err != nil || res.Unflagged != 2 || res.Case.ID != c.ID {
		t.Fatalf("unflag: %+v %v", res, err)
	}
	for id, want := range map[string]bool{inbox: false, archived: false, trashed: true} {
		m, err := x.b.store.GetMessage(x.ctx, x.acc, id)
		if err != nil || slices.Contains(m.Flags, api.FlagFlagged) != want {
			t.Fatalf("flag of %s: %v %v", id, m.Flags, err)
		}
	}
	ops, _ := x.b.store.NextOps(x.ctx, x.acc, time.Now().Add(time.Hour), 10)
	if len(ops) != 2 || ops[0].Kind != store.OpFlag || ops[1].Kind != store.OpFlag {
		t.Fatalf("ops: %+v", ops)
	}
	if len(x.triggers()) == 0 {
		t.Fatal("the syncer was not woken")
	}
	x.drain()
	if got := x.caseOf("t_a"); got.RuleReason == api.BoardReasonHotFlagged || got.RuleState == api.BoardHot {
		t.Fatalf("still hot after unflag: %s/%s", got.RuleState, got.RuleReason)
	}
	// Again: nothing left to clear, no error.
	if res, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID}); err != nil || res.Unflagged != 0 {
		t.Fatalf("second unflag: %+v %v", res, err)
	}
	// Nothing flagged at all.
	if res, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: x.caseOf("t_q").ID}); err != nil || res.Unflagged != 0 {
		t.Fatalf("unflag of %s: %+v %v", quiet, res, err)
	}
	_, err = x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: "c_00000000000000000000000000000000"})
	wantCode(t, "unknown case", err, api.CodeCaseNotFound)
	_, err = x.svc.Unflag(x.ctx, api.BoardUnflagParams{})
	if err == nil {
		t.Fatal("unflag without a case id")
	}
	p := api.DefaultBoardPreferences()
	p.Enabled = false
	if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: p}); err != nil {
		t.Fatal(err)
	}
	_, err = x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID})
	wantCode(t, "board off", err, api.CodeInvalidArgument)
}

// A case the user set a state on keeps it after unflag; a linked
// suggested reply keeps a case the rules drop.
func TestBoardUnflagKeeps(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_s", rfc: "s1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi", flagged: true})
	x.drain()
	c := x.caseOf("t_s")
	you := api.BoardYou
	if _, err := x.svc.SetState(x.ctx, api.BoardSetStateParams{CaseID: c.ID, State: &you}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	if got := x.caseOf("t_s"); got.UserState == nil || *got.UserState != api.BoardYou || got.RuleReason == api.BoardReasonHotFlagged {
		t.Fatalf("after unflag: %v %s", got.UserState, got.RuleReason)
	}
}

// The hourly upkeep deletes local drafts no case links once they rested
// for localDraftKeep, keeps recent and linked ones and ordinary drafts,
// and deletes the server copies no draft holds any more.
func TestBoardUpkeepLocalDrafts(t *testing.T) {
	x := newBoardBox(t)
	in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_a")
	old := x.reply(in, true)
	recent := x.reply(in, true)
	plain := x.reply(in, false)
	linked := x.reply(in, true)
	if _, err := x.svc.SetDraft(x.ctx, api.BoardSetDraftParams{CaseID: c.ID, DraftID: linked.DraftID}); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-localDraftKeep - time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	for _, id := range []api.DraftID{old.DraftID, plain.DraftID, linked.DraftID} {
		if _, err := x.b.store.DB().Exec(`UPDATE drafts SET updated_at = ? WHERE id = ?`, stale, string(id)); err != nil {
			t.Fatal(err)
		}
	}
	// A copy no draft holds any more (as migration 0019 moves the copy of
	// a draft linked before it).
	copyID := x.put(bmail{folder: x.drafts, thread: "t_a", rfc: "dcopy@x", from: boardMe, to: []api.Address{boardAlice}, text: "Yes."})
	if _, err := x.b.store.DB().Exec(`INSERT INTO draft_stray_copies (account_id, folder_id, uidvalidity, uid, rfc_message_id, recorded_at)
		VALUES (?, ?, ?, ?, 'dcopy@x', ?)`, x.acc, x.drafts.ID, x.drafts.UIDValidity, x.uid, stale); err != nil {
		t.Fatal(err)
	}
	x.triggers()
	x.b.boardUpkeep(x.ctx)
	for id, want := range map[api.DraftID]bool{old.DraftID: false, recent.DraftID: true, plain.DraftID: true, linked.DraftID: true} {
		_, err := x.b.store.GetDraft(x.ctx, x.acc, string(id))
		if (err == nil) != want {
			t.Fatalf("draft %s after upkeep: kept=%v, want %v", id, err == nil, want)
		}
	}
	var strays int
	if err := x.b.store.DB().QueryRow(`SELECT COUNT(*) FROM draft_stray_copies`).Scan(&strays); err != nil || strays != 0 {
		t.Fatalf("stray copies after upkeep: %d %v", strays, err)
	}
	if _, err := x.b.store.GetMessage(x.ctx, x.acc, copyID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the copy's row stayed: %v", err)
	}
	if len(x.triggers()) == 0 {
		t.Fatal("the syncer was not woken for the copy's delete")
	}
}

// The upkeep's prune of an orphaned case: the reply the user edited
// becomes an ordinary draft due for upload, an untouched one is deleted;
// the sweep that follows in the same upkeep takes neither (D2).
func TestBoardUpkeepReleasesOrphanReply(t *testing.T) {
	for _, edit := range []bool{false, true} {
		x := newBoardBox(t)
		in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
		x.drain()
		c := x.caseOf("t_a")
		d := x.reply(in, true)
		if _, err := x.svc.SetDraft(x.ctx, api.BoardSetDraftParams{CaseID: c.ID, DraftID: d.DraftID}); err != nil {
			t.Fatal(err)
		}
		if edit {
			if _, err := x.b.Drafts().Save(x.ctx, api.DraftSaveParams{Draft: api.Draft{ID: d.DraftID, Version: d.Version,
				AccountID: api.AccountID(x.acc), To: []api.Address{boardAlice}, Subject: "Re: Lunch",
				HTMLBody: "<p>Yes, typed by me.</p>", InReplyTo: api.MessageID(in)}}); err != nil {
				t.Fatal(err)
			}
		}
		stale := time.Now().Add(-localDraftKeep - time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		orphaned := x.b.boardNow().Add(-store.BoardOrphanGrace - time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := x.b.store.DB().Exec(`UPDATE drafts SET updated_at = ? WHERE id = ?`, stale, string(d.DraftID)); err != nil {
			t.Fatal(err)
		}
		if _, err := x.b.store.DB().Exec(`UPDATE board_cases SET orphaned_at = ? WHERE id = ?`, orphaned, string(c.ID)); err != nil {
			t.Fatal(err)
		}
		x.b.boardUpkeep(x.ctx)
		got, err := x.b.store.GetDraft(x.ctx, x.acc, string(d.DraftID))
		if edit && (err != nil || got.Local) {
			t.Fatalf("the edited reply after upkeep: %+v %v", got, err)
		}
		if !edit && !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("the untouched reply after upkeep: %+v %v", got, err)
		}
		if _, err := x.b.store.GetBoardCase(x.ctx, string(c.ID)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("the orphan after upkeep: %v", err)
		}
	}
}

// A draft that became local after the syncer listed it is not built for
// upload: buildDraft answers store.ErrNotFound, which the syncers skip.
func TestBuildDraftSkipsLocal(t *testing.T) {
	x := newBoardBox(t)
	in := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	local := x.reply(in, true)
	plain := x.reply(in, false)
	if _, err := x.b.buildDraft(x.ctx, x.acc, string(local.DraftID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("build of a local draft: %v", err)
	}
	if _, err := x.b.buildDraft(x.ctx, x.acc, string(plain.DraftID)); err != nil {
		t.Fatalf("build of an ordinary draft: %v", err)
	}
}

// board.unflag on a paused account clears the flags locally and leaves
// the operations queued for when it resumes.
func TestBoardUnflagPaused(t *testing.T) {
	x := newBoardBox(t)
	id := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi", flagged: true})
	x.drain()
	c := x.caseOf("t_a")
	if _, err := x.b.Accounts().SetEnabled(x.ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(x.acc), Enabled: false}); err != nil {
		t.Fatal(err)
	}
	res, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID})
	if err != nil || res.Unflagged != 1 {
		t.Fatalf("unflag on a paused account: %+v %v", res, err)
	}
	if m, err := x.b.store.GetMessage(x.ctx, x.acc, id); err != nil || slices.Contains(m.Flags, api.FlagFlagged) {
		t.Fatalf("flags: %v %v", m.Flags, err)
	}
	ops, _ := x.b.store.NextOps(x.ctx, x.acc, time.Now().Add(time.Hour), 10)
	if len(ops) != 1 || ops[0].Kind != store.OpFlag {
		t.Fatalf("ops waiting: %+v", ops)
	}
}

// board.unflag on a jira account: the item's copy in a virtual folder is
// cleared with it, as message.flag does there, and is not counted in
// unflagged.
func TestBoardUnflagJira(t *testing.T) {
	x := newBoardBox(t)
	res, err := x.b.Accounts().Add(x.ctx, api.AccountAddParams{Config: jiraConfig(), Credentials: api.Credentials{Password: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	jacc := string(res.AccountID)
	folders := seedFolders(t, x.b, jacc, []store.Folder{
		{Mailbox: "space:10000", Name: "IT Service Desk", Path: "IT Service Desk", Selectable: true, Subscribed: true},
		{Mailbox: "view:assignedToMe", Name: "Assigned", Path: "Assigned", Virtual: api.VirtualAssignedToMe, Selectable: true, Subscribed: true},
	})
	if err := x.b.store.PutIssue(x.ctx, store.Issue{AccountID: jacc, IssueID: "1", Key: "ITSD-1", SpaceID: "10000", Summary: "Printer",
		Status: "Open", StatusID: "1", StatusCategory: api.StatusCategoryTodo, AssigneeID: "me", ReporterID: "boss"}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range []store.Folder{folders["space:10000"], folders["view:assignedToMe"]} {
		m := &store.Message{AccountID: jacc, FolderID: f.ID, RemoteID: "i:1", Subject: "ITSD-1: Printer",
			From: []api.Address{{Name: "boss", Address: "boss@users.jira.invalid"}}, Date: x.base, InternalDate: x.base,
			RFCMessageID: "<i.1.issue.1@acme.malachi.invalid>", Size: 10, ThreadID: store.IssueThreadID("1"),
			Flags: []api.Flag{api.FlagFlagged}}
		if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{m}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if err := x.b.store.PutIssueItems(x.ctx, jacc, []store.IssueItem{{RemoteID: "i:1", IssueID: "1", Kind: api.IssueItemDescription, AuthorID: "boss"}}); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.SetMeta(x.ctx, store.MetaIssueMePrefix+jacc, `{"id":"me"}`); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.MarkBoardAccountDirty(x.ctx, jacc, time.Time{}); err != nil {
		t.Fatal(err)
	}
	x.drain()
	_, cases := x.list()
	c, ok := cases["jira:1"]
	if !ok {
		t.Fatal("no jira case")
	}
	u, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID})
	if err != nil || u.Unflagged != 1 {
		t.Fatalf("unflag: %+v %v", u, err)
	}
	for _, id := range ids {
		if m, err := x.b.store.GetMessage(x.ctx, jacc, id); err != nil || slices.Contains(m.Flags, api.FlagFlagged) {
			t.Fatalf("flag of %s: %v %v", id, m.Flags, err)
		}
	}
}
