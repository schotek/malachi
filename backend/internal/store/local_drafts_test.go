// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// localBox is an account with an inbox, a Drafts folder and one case
// whose thread holds an inbound message.
type localBox struct {
	ctx    context.Context
	s      *Store
	inbox  Folder
	drafts Folder
	msg    *Message
	c      BoardCase
}

func newLocalBox(t *testing.T) *localBox {
	t.Helper()
	x := &localBox{ctx: context.Background(), s: openTestStore(t)}
	seedAccount(t, x.s, "acc")
	x.inbox = seedFolder(t, x.s, "acc", "INBOX", api.RoleInbox)
	x.drafts = seedFolder(t, x.s, "acc", "Drafts", api.RoleDrafts)
	x.msg = boardMail(t, x.s, x.inbox, 1, "a1", "")
	drainAll(t, x.s, boardNow, testDecider)
	x.c = caseOf(t, x.s, "acc", x.msg.ThreadID)
	return x
}

// draft saves a reply to the case's message.
func (x *localBox) draft(t *testing.T, local bool) Draft {
	t.Helper()
	d := Draft{AccountID: "acc", Subject: "Re: a1", TextBody: "Yes.", InReplyTo: x.msg.ID, Local: local}
	if err := x.s.SaveDraft(x.ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	return d
}

// uploaded records a copy of the draft at uid, with its row in Drafts.
func (x *localBox) uploaded(t *testing.T, d Draft, uid uint32, rfc string) (DraftCopy, *Message) {
	t.Helper()
	c := DraftCopy{FolderID: x.drafts.ID, UID: uid, UIDValidity: x.drafts.UIDValidity, RFCMessageID: rfc}
	if found, err := x.s.MarkDraftSynced(x.ctx, "acc", d.ID, d.Version, c, false); err != nil || !found {
		t.Fatalf("mark synced: %v %v", found, err)
	}
	return c, seedCopy(t, x.s, x.drafts, uid, "", rfc)
}

func (x *localBox) get(t *testing.T, id string) Draft {
	t.Helper()
	d, err := x.s.GetDraft(x.ctx, "acc", id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func deleteOps(t *testing.T, s *Store) []uint32 {
	t.Helper()
	ops, err := s.NextOps(context.Background(), "acc", time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	var uids []uint32
	for _, op := range ops {
		if op.Kind == OpDelete {
			uids = append(uids, op.UID)
		}
	}
	return uids
}

// A local draft is never due and arms no upload; the flag is written on
// creation only.
func TestLocalDraftNeverUploaded(t *testing.T) {
	x := newLocalBox(t)
	later := time.Now().Add(2 * time.Minute)
	local := x.draft(t, true)
	if got := dueIDs(t, x.s, later); len(got) != 0 {
		t.Fatalf("a local draft is due: %v", got)
	}
	if next, err := x.s.NextDraftUpload(x.ctx, "acc", time.Minute); err != nil || !next.IsZero() {
		t.Fatalf("next upload for a local draft: %v %v", next, err)
	}
	// A later save cannot clear it.
	local.Local = false
	local.TextBody = "Yes, sure."
	if err := x.s.SaveDraft(x.ctx, &local, nil); err != nil {
		t.Fatal(err)
	}
	if !x.get(t, local.ID).Local || len(dueIDs(t, x.s, later)) != 0 {
		t.Fatal("a save cleared local")
	}
	// Nor set it on an ordinary draft.
	plain := x.draft(t, false)
	plain.Local = true
	if err := x.s.SaveDraft(x.ctx, &plain, nil); err != nil {
		t.Fatal(err)
	}
	if x.get(t, plain.ID).Local {
		t.Fatal("a save set local")
	}
	if got := dueIDs(t, x.s, later); len(got) != 1 || got[0] != plain.ID {
		t.Fatalf("due = %v", got)
	}
	// A local draft takes over no copy of the Drafts folder.
	row := seedCopy(t, x.s, x.drafts, 30, "", "other@x")
	c := CopyOf(*row, x.drafts)
	local.Adopt = &c
	if err := x.s.SaveDraft(x.ctx, &local, nil); !errors.Is(err, ErrDraftLocal) {
		t.Fatalf("adopt into a local draft: %v", err)
	}
}

// Linking a draft (board.setDraft) makes it local and deletes the copy it
// had; an upload picked before the link deletes its own copy when it
// ends, and records nothing.
func TestLinkMakesDraftLocal(t *testing.T) {
	x := newLocalBox(t)
	d := x.draft(t, false)
	_, row := x.uploaded(t, d, 10, "v1@x")
	d.TextBody = "Yes, at noon."
	if err := x.s.SaveDraft(x.ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	// The syncer picks version 2 for its upload...
	due := dueIDs(t, x.s, time.Now().Add(2*time.Minute))
	if len(due) != 1 || due[0] != d.ID {
		t.Fatalf("due = %v", due)
	}
	// ...and the draft is linked while the upload runs.
	if _, err := x.s.SetBoardDraft(x.ctx, x.c.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	got := x.get(t, d.ID)
	if !got.Local || !got.Copy.IsZero() || got.SyncedVersion != 0 {
		t.Fatalf("after the link: %+v", got)
	}
	if _, err := x.s.GetMessage(x.ctx, "acc", row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old copy's row stayed: %v", err)
	}
	if uids := deleteOps(t, x.s); !slices.Equal(uids, []uint32{10}) {
		t.Fatalf("delete ops after the link: %v", uids)
	}
	// The upload ends: its copy goes too, nothing is recorded.
	found, err := x.s.MarkDraftSynced(x.ctx, "acc", d.ID, 2, DraftCopy{FolderID: x.drafts.ID, UID: 11, UIDValidity: x.drafts.UIDValidity, RFCMessageID: "v2@x"}, false)
	if err != nil || found {
		t.Fatalf("upload of a draft made local: found=%v err=%v", found, err)
	}
	if got := x.get(t, d.ID); !got.Local || !got.Copy.IsZero() || got.SyncedVersion != 0 {
		t.Fatalf("an in-flight upload recorded a copy: %+v", got)
	}
	if uids := deleteOps(t, x.s); !slices.Equal(uids, []uint32{10, 11}) {
		t.Fatalf("delete ops after the upload: %v", uids)
	}
	if got := dueIDs(t, x.s, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("due after the link: %v", got)
	}
	// Linking the same draft again changes nothing and stays local.
	before := caseOf(t, x.s, "acc", x.msg.ThreadID)
	if c, err := x.s.SetBoardDraft(x.ctx, x.c.ID, d.ID); err != nil || c.Version != before.Version {
		t.Fatalf("link again: %v %v", c.Version, err)
	}
}

// A link by an annotation (board.annotate draftId) does the same; a draft
// it does not link (the case has one) stays as it was.
func TestAnnotateLinkMakesDraftLocal(t *testing.T) {
	x := newLocalBox(t)
	d := x.draft(t, false)
	x.uploaded(t, d, 10, "v1@x")
	c, _, err := x.s.AnnotateBoardCase(x.ctx, BoardAnnotationInput{CaseID: x.c.ID, InputKey: x.c.InputKey, DraftID: d.ID, Source: "t", Now: boardNow})
	if err != nil || c.DraftID != d.ID {
		t.Fatalf("annotate: %v %v", c.DraftID, err)
	}
	if got := x.get(t, d.ID); !got.Local || !got.Copy.IsZero() {
		t.Fatalf("after annotate: %+v", got)
	}
	if uids := deleteOps(t, x.s); !slices.Equal(uids, []uint32{10}) {
		t.Fatalf("delete ops: %v", uids)
	}
	other := x.draft(t, false)
	c = caseOf(t, x.s, "acc", x.msg.ThreadID)
	if c, _, err = x.s.AnnotateBoardCase(x.ctx, BoardAnnotationInput{CaseID: c.ID, InputKey: c.InputKey, DraftID: other.ID, Source: "t", Now: boardNow}); err != nil || c.DraftID != d.ID {
		t.Fatalf("second annotate: %v %v", c.DraftID, err)
	}
	if x.get(t, other.ID).Local {
		t.Fatal("a draft the annotation did not link became local")
	}
}

// A linked draft that exists keeps its case when the rules drop it; once
// the draft is deleted the thread is judged again and the case goes.
func TestLinkedDraftKeepsCase(t *testing.T) {
	x := newLocalBox(t)
	d := x.draft(t, true)
	if _, err := x.s.SetBoardDraft(x.ctx, x.c.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	none := func(*BoardThread) (BoardVerdict, error) { return BoardVerdict{RulesVersion: "test"}, nil }
	if err := x.s.MarkBoardThreadsDirty(x.ctx, "acc", []string{x.msg.ThreadID}); err != nil {
		t.Fatal(err)
	}
	drainAll(t, x.s, boardNow, none)
	c := caseOf(t, x.s, "acc", x.msg.ThreadID)
	if c.RuleReason != api.BoardReasonKept || c.Draft == nil {
		t.Fatalf("case after the rules dropped it: %s %+v", c.RuleReason, c.Draft)
	}
	if err := x.s.DeleteDraft(x.ctx, "acc", d.ID); err != nil {
		t.Fatal(err)
	}
	// The deletion alone marks the thread dirty (trigger drafts_board_ad).
	if !dirtyThreads(t, x.s)["acc/"+x.msg.ThreadID] {
		t.Fatal("deleting a linked draft left its thread clean")
	}
	drainAll(t, x.s, boardNow, none)
	if _, err := x.s.BoardCaseByThread(x.ctx, "acc", x.msg.ThreadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the case outlived its draft: %v", err)
	}
}

// link links d to the box's case; with edit the draft is then saved again
// (the inline editor), which marks it edited.
func (x *localBox) link(t *testing.T, d *Draft, edit bool) {
	t.Helper()
	if _, err := x.s.SetBoardDraft(x.ctx, x.c.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if edit {
		d.TextBody = "Yes, at noon — written by the user."
		var atts []string
		for _, a := range d.Attachments {
			atts = append(atts, a.ID)
		}
		if err := x.s.SaveDraft(x.ctx, d, atts); err != nil {
			t.Fatal(err)
		}
	}
}

func deleteOpsAll(t *testing.T, s *Store) []Op {
	t.Helper()
	ops, err := s.NextOps(context.Background(), "acc", time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []Op
	for _, op := range ops {
		if op.Kind == OpDelete {
			out = append(out, op)
		}
	}
	return out
}

// D1: a save while a case links the draft marks it edited, for good; a
// save before the link does not; linking an ordinary draft marks it too
// (whoever wrote it, it was not a local suggestion).
func TestLinkedSaveMarksDraftEdited(t *testing.T) {
	x := newLocalBox(t)
	d := x.draft(t, true)
	d.TextBody = "Saved again before the link."
	if err := x.s.SaveDraft(x.ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if x.get(t, d.ID).Edited {
		t.Fatal("a save before the link marked the draft edited")
	}
	x.link(t, &d, false)
	if x.get(t, d.ID).Edited {
		t.Fatal("the link of a local draft marked it edited")
	}
	x.link(t, &d, true)
	if !x.get(t, d.ID).Edited {
		t.Fatal("a save while linked did not mark the draft edited")
	}

	y := newLocalBox(t)
	plain := y.draft(t, false)
	y.link(t, &plain, false)
	if got := y.get(t, plain.ID); !got.Local || !got.Edited {
		t.Fatalf("an ordinary draft linked: %+v", got)
	}
	other := y.draft(t, false)
	other.TextBody = "never linked"
	if err := y.s.SaveDraft(y.ctx, &other, nil); err != nil {
		t.Fatal(err)
	}
	if y.get(t, other.ID).Edited {
		t.Fatal("an unlinked draft became edited")
	}
}

// D2 and D3 at the prune: an orphaned case goes, a done case past the done
// retention loses its reply (the case stays for the ordinary prune); in
// both, an edited reply becomes an ordinary draft (due for upload) and an
// untouched one is deleted with its attachments, in the prune's
// transaction, so nothing is left for the sweep. A live case keeps its
// reply whatever its age.
func TestPruneReleasesLinkedDrafts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		done, edit bool
	}{{"orphan untouched", false, false}, {"orphan edited", false, true}, {"done untouched", true, false}, {"done edited", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			x := newLocalBox(t)
			att := importTestAttachment(t, x.s, "acc", "a.txt", "data")
			d := Draft{AccountID: "acc", Subject: "Re: a1", TextBody: "Yes.", InReplyTo: x.msg.ID, Local: true}
			if err := x.s.SaveDraft(x.ctx, &d, []string{att.ID}); err != nil {
				t.Fatal(err)
			}
			x.link(t, &d, tc.edit)
			far := boardNow.Add(400 * 24 * time.Hour)
			// Nothing is pruned for its age: Before lies in the past.
			prune := BoardPrune{Before: boardNow.Add(-400 * 24 * time.Hour), DoneBefore: far, Now: far, OrphanBefore: boardNow.Add(-time.Hour)}
			if accts, err := x.s.PruneBoardCases(x.ctx, prune); err != nil || len(accts) != 0 {
				t.Fatalf("a live case with a reply lost something: %v %v", accts, err)
			}
			if c := caseOf(t, x.s, "acc", x.msg.ThreadID); c.Draft == nil {
				t.Fatal("a live case lost its reply")
			}
			if tc.done {
				if _, err := x.s.SetBoardDone(x.ctx, x.c.ID, true, boardNow); err != nil {
					t.Fatal(err)
				}
				list, err := x.s.ListBoard(x.ctx, BoardListQuery{AccountIDs: []string{"acc"}, Now: far, Windows: boardWindows})
				if err != nil || len(list.Cases) != 1 {
					t.Fatalf("a long-done case with a reply is not listed until the prune: %d %v", len(list.Cases), err)
				}
			} else if _, err := x.s.DB().Exec(`UPDATE board_cases SET orphaned_at = ? WHERE id = ?`, stamp(boardNow.Add(-2*time.Hour)), x.c.ID); err != nil {
				t.Fatal(err)
			}
			if accts, err := x.s.PruneBoardCases(x.ctx, prune); err != nil || !slices.Equal(accts, []string{"acc"}) {
				t.Fatalf("prune: %v %v", accts, err)
			}
			c, err := x.s.GetBoardCase(x.ctx, x.c.ID)
			switch {
			case tc.done && (err != nil || c.DraftID != "" || c.Draft != nil):
				t.Fatalf("the done case after the prune: %+v %v", c, err)
			case !tc.done && !errors.Is(err, ErrNotFound):
				t.Fatalf("the orphan after the prune: %v", err)
			}
			got, err := x.s.GetDraft(x.ctx, "acc", d.ID)
			want := ReleaseDeleted
			if tc.edit {
				want = ReleaseOrdinary
				if err != nil || got.Local || !got.Edited || len(got.Attachments) != 1 {
					t.Fatalf("the edited reply: %+v %v", got, err)
				}
				if due := dueIDs(t, x.s, time.Now().Add(time.Hour)); !slices.Equal(due, []string{d.ID}) {
					t.Fatalf("the edited reply is not due for upload: %v", due)
				}
			} else {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("the untouched reply survived: %+v %v", got, err)
				}
				if n := countRows(t, x.s, `SELECT COUNT(*) FROM attachments WHERE id = ?`, att.ID); n != 0 {
					t.Fatal("the untouched reply's attachment stayed")
				}
				if _, err := os.Stat(x.s.AttachmentPath(att.ID)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the untouched reply's attachment file stayed: %v", err)
				}
			}
			if left, err := x.s.UnlinkedLocalDrafts(x.ctx, time.Now().Add(time.Hour), 0); err != nil || len(left) != 0 {
				t.Fatalf("left for the sweep: %v %v", left, err)
			}
			reason := "orphan"
			if tc.done {
				reason = "done"
			}
			u, err := x.s.TakeBoardUnlinkedDrafts(x.ctx)
			if err != nil || len(u) != 1 || u[0] != (BoardUnlinkedDraft{AccountID: "acc", DraftID: d.ID, CaseID: x.c.ID, Reason: reason, Outcome: want}) {
				t.Fatalf("recorded: %+v %v", u, err)
			}
			if tc.done {
				list, err := x.s.ListBoard(x.ctx, BoardListQuery{AccountIDs: []string{"acc"}, Now: far, Windows: boardWindows})
				if err != nil || len(list.Cases) != 0 {
					t.Fatalf("a long-done case without a reply is listed: %d %v", len(list.Cases), err)
				}
			}
		})
	}
}

// The sweep's list: local, unlinked, never edited, not saved since
// before, of an account that exists; never an ordinary draft. An edited
// local draft without a case (a path that missed the release) is never
// listed: ReleaseEditedLocalDrafts makes it ordinary.
func TestUnlinkedLocalDrafts(t *testing.T) {
	x := newLocalBox(t)
	local := x.draft(t, true)
	x.draft(t, false)
	if got, _ := x.s.UnlinkedLocalDrafts(x.ctx, time.Now().Add(-time.Hour), 0); len(got) != 0 {
		t.Fatalf("a draft saved since is listed: %v", got)
	}
	if got, _ := x.s.UnlinkedLocalDrafts(x.ctx, time.Now().Add(time.Hour), 0); len(got) != 1 || got[0][1] != local.ID {
		t.Fatalf("unlinked local drafts: %v", got)
	}
	edited := x.draft(t, true)
	x.link(t, &edited, true)
	// The case disappears behind the store's back.
	if _, err := x.s.DB().Exec(`DELETE FROM board_cases WHERE id = ?`, x.c.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.s.UnlinkedLocalDrafts(x.ctx, time.Now().Add(time.Hour), 0); len(got) != 1 || got[0][1] != local.ID {
		t.Fatalf("an edited draft is listed for the sweep: %v", got)
	}
	if accts, err := x.s.ReleaseEditedLocalDrafts(x.ctx); err != nil || !slices.Equal(accts, []string{"acc"}) {
		t.Fatalf("release edited: %v %v", accts, err)
	}
	if got := x.get(t, edited.ID); got.Local {
		t.Fatalf("the edited draft is still local: %+v", got)
	}
	if accts, err := x.s.ReleaseEditedLocalDrafts(x.ctx); err != nil || len(accts) != 0 {
		t.Fatalf("release edited again: %v %v", accts, err)
	}
	// The account removed with its local data kept: its drafts stay.
	if err := x.s.DeleteAccount(x.ctx, "acc", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.s.UnlinkedLocalDrafts(x.ctx, time.Now().Add(time.Hour), 0); len(got) != 0 {
		t.Fatalf("a removed account's draft is listed: %v", got)
	}
}

// The account removed with its local data kept: an edited reply stays as an
// ordinary draft, an untouched one is deleted; its stray copies go with
// its operations.
func TestDeleteAccountReleasesLinkedDraft(t *testing.T) {
	for _, edit := range []bool{false, true} {
		x := newLocalBox(t)
		d := x.draft(t, true)
		x.link(t, &d, edit)
		if _, err := x.s.MarkDraftSynced(x.ctx, "acc", d.ID, d.Version, DraftCopy{FolderID: x.drafts.ID, RFCMessageID: "late@x"}, false); err != nil {
			t.Fatal(err)
		}
		if err := x.s.DeleteAccount(x.ctx, "acc", false); err != nil {
			t.Fatal(err)
		}
		got, err := x.s.GetDraft(x.ctx, "acc", d.ID)
		if edit && (err != nil || got.Local) {
			t.Fatalf("edited: %+v %v", got, err)
		}
		if !edit && !errors.Is(err, ErrNotFound) {
			t.Fatalf("untouched: %+v %v", got, err)
		}
		if n := countRows(t, x.s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 0 {
			t.Fatalf("stray copies after the account went: %d", n)
		}
	}
}

// A thread merge where both cases link a draft: the absorbed case's draft
// becomes an ordinary draft when edited (due for upload) and is deleted
// when untouched, in the merge; when the surviving case's draft is gone,
// the absorbed one's link survives instead, still local; when the
// absorbed one's is gone, nothing is reported.
func TestMergeReleasesAbsorbedDraft(t *testing.T) {
	// gone: whose draft is deleted before the merge ("" none). Which case
	// survives is the store's choice, so both are tried: in one of the two
	// the surviving case's draft is the one gone.
	for _, tc := range []struct {
		gone string
		edit bool
	}{{"", false}, {"", true}, {"x", false}, {"y", false}} {
		keepGone := tc.gone != ""
		ctx := context.Background()
		s := openTestStore(t)
		seedAccount(t, s, "acc")
		inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
		mx := boardMail(t, s, inbox, 1, "x", "")
		my := boardMail(t, s, inbox, 2, "y", "")
		drainAll(t, s, boardNow, testDecider)
		cx, cy := caseOf(t, s, "acc", mx.ThreadID), caseOf(t, s, "acc", my.ThreadID)
		drafts := map[string]string{}
		for _, p := range []struct {
			c BoardCase
			m *Message
		}{{cx, mx}, {cy, my}} {
			d := Draft{AccountID: "acc", TextBody: "reply", InReplyTo: p.m.ID, Local: true}
			if err := s.SaveDraft(ctx, &d, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetBoardDraft(ctx, p.c.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			if tc.edit {
				d.TextBody = "reply, edited"
				if err := s.SaveDraft(ctx, &d, nil); err != nil {
					t.Fatal(err)
				}
			}
			drafts[p.c.ID] = d.ID
		}
		alive := ""
		if keepGone {
			del, keep := cx, cy
			if tc.gone == "y" {
				del, keep = cy, cx
			}
			if err := s.DeleteDraft(ctx, "acc", drafts[del.ID]); err != nil {
				t.Fatal(err)
			}
			alive = drafts[keep.ID]
		}
		both := boardMail(t, s, inbox, 3, "both", "x", "y", "x")
		merged := sameThread(t, s, mx, my, both)
		survivor := caseOf(t, s, "acc", merged)
		absorbed := cx.ID
		if survivor.ID == cx.ID {
			absorbed = cy.ID
		}
		unlinked, err := s.TakeBoardUnlinkedDrafts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if keepGone {
			// The surviving link names the draft that exists.
			if survivor.DraftID != alive || survivor.Draft == nil {
				t.Fatalf("keepGone: survivor links %q, want %q", survivor.DraftID, alive)
			}
			if d, err := s.GetDraft(ctx, "acc", alive); err != nil || !d.Local {
				t.Fatalf("keepGone: the linked draft is not local: %+v %v", d, err)
			}
			// Either the survivor took the absorbed link, or the absorbed
			// draft was the gone one: nothing lost a case.
			if len(unlinked) != 0 {
				t.Fatalf("keepGone: reported %+v", unlinked)
			}
			continue
		}
		if survivor.DraftID != drafts[survivor.ID] {
			t.Fatalf("survivor links %q, want its own %q", survivor.DraftID, drafts[survivor.ID])
		}
		if kept, _ := s.GetDraft(ctx, "acc", drafts[survivor.ID]); !kept.Local {
			t.Fatal("the surviving case's draft is no longer local")
		}
		released, err := s.GetDraft(ctx, "acc", drafts[absorbed])
		due, derr := s.DueDraftUploads(ctx, "acc", time.Now().Add(time.Hour), time.Minute, 10)
		if derr != nil {
			t.Fatal(derr)
		}
		want := ReleaseDeleted
		if tc.edit {
			want = ReleaseOrdinary
			if err != nil || released.Local {
				t.Fatalf("the absorbed case's edited draft: %+v %v", released, err)
			}
			if len(due) != 1 || due[0].ID != drafts[absorbed] {
				t.Fatalf("due after the merge: %+v", due)
			}
		} else {
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("the absorbed case's untouched draft survived: %+v %v", released, err)
			}
			if len(due) != 0 {
				t.Fatalf("due after the merge: %+v", due)
			}
		}
		if len(unlinked) != 1 || unlinked[0] != (BoardUnlinkedDraft{AccountID: "acc", DraftID: drafts[absorbed], CaseID: survivor.ID, Reason: "merge", Outcome: want}) {
			t.Fatalf("reported: %+v", unlinked)
		}
		if left, err := s.UnlinkedLocalDrafts(ctx, time.Now().Add(time.Hour), 0); err != nil || len(left) != 0 {
			t.Fatalf("left for the sweep: %v %v", left, err)
		}
	}
}

// D4, the store's side: linking a draft whose copy is a Graph item does
// not delete the copy but records it as a stray copy for the syncer's edit
// check. Without the check (the upkeep) it waits; a copy changed on the
// server is forgotten (it stays, the user's own); one not changed is
// deleted through the operation log.
func TestLinkKeepsGraphDraftCopyForTheEditCheck(t *testing.T) {
	for _, changed := range []bool{true, false} {
		x := newLocalBox(t)
		d := x.draft(t, false)
		c := DraftCopy{FolderID: x.drafts.ID, RemoteID: "AAMk-1", RFCMessageID: "g1@x"}
		if found, err := x.s.MarkDraftSynced(x.ctx, "acc", d.ID, d.Version, c, false); err != nil || !found {
			t.Fatalf("mark synced: %v %v", found, err)
		}
		row := seedCopy(t, x.s, x.drafts, 0, "AAMk-1", "g1@x")
		x.link(t, &d, false)
		if got := x.get(t, d.ID); !got.Local || !got.Copy.IsZero() {
			t.Fatalf("after the link: %+v", got)
		}
		if _, err := x.s.GetMessage(x.ctx, "acc", row.ID); err != nil {
			t.Fatalf("the link deleted a Graph copy before the edit check: %v", err)
		}
		if ops := deleteOpsAll(t, x.s); len(ops) != 0 {
			t.Fatalf("delete ops before the check: %+v", ops)
		}
		if accts, err := x.s.DropStrayDraftCopies(x.ctx, "", nil, time.Now()); err != nil || len(accts) != 0 {
			t.Fatalf("the upkeep resolved a Graph copy: %v %v", accts, err)
		}
		var asked []StrayDraftCopy
		edited := func(_ context.Context, e StrayDraftCopy) (bool, error) {
			asked = append(asked, e)
			return changed, nil
		}
		accts, err := x.s.DropStrayDraftCopies(x.ctx, "acc", edited, time.Now())
		if err != nil || len(asked) != 1 || asked[0].Copy.RemoteID != "AAMk-1" || asked[0].SyncedAt.IsZero() {
			t.Fatalf("the check: %+v %v", asked, err)
		}
		_, gerr := x.s.GetMessage(x.ctx, "acc", row.ID)
		if changed {
			if len(accts) != 0 || gerr != nil || len(deleteOpsAll(t, x.s)) != 0 {
				t.Fatalf("a copy changed on the server was deleted: %v %v", accts, gerr)
			}
		} else {
			ops := deleteOpsAll(t, x.s)
			if !slices.Equal(accts, []string{"acc"}) || !errors.Is(gerr, ErrNotFound) || len(ops) != 1 || ops[0].RemoteID != "AAMk-1" {
				t.Fatalf("an unchanged copy was not deleted: %v %v %+v", accts, gerr, ops)
			}
		}
		if n := countRows(t, x.s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 0 {
			t.Fatalf("stray copies left: %d", n)
		}
		if got := x.get(t, d.ID); !got.Local {
			t.Fatal("the draft is no longer local")
		}
	}
}

// migrateRawTo runs migrations 1..upTo on a new store file without the
// store's migrator, as an older build left it, and returns its path.
func migrateRawTo(t *testing.T, upTo int, fill ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		t.Fatal(err)
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.version > upTo {
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range fill {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// A store at 0017 with board data: the frozen 0018 adds local, makes the
// drafts linked within their account local (their copy columns left as
// they were), and replaces drafts_board_ad. Checked on the raw chain up
// to 18, as the owner's store ran it.
func TestMigration0018LocalDrafts(t *testing.T) {
	path := migrateRawTo(t, 18,
		`INSERT INTO drafts (id, account_id, version, subject, created_at, updated_at) VALUES ('d_x', 'acc', 1, 's', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z')`)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var local int
	if err := db.QueryRow(`SELECT local FROM drafts WHERE id = 'd_x'`).Scan(&local); err != nil || local != 0 {
		t.Fatalf("local after 0018: %d %v", local, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('drafts_local', 'drafts_board_ad')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("0018 objects: %d %v", n, err)
	}
}

// A store at 0018 that has been used (local drafts linked with and
// without a recorded copy, an unlinked local draft, an ordinary draft with
// a copy, cases, an annotation, a run, a dirty thread): 0019 adds edited
// and draft_stray_copies, counts every local draft as edited, moves the
// copies local drafts record to draft_stray_copies with their columns
// cleared, and leaves ordinary drafts, case versions, annotations, runs and
// the dirty set alone. The first stray pass queues the delete of the IMAP
// copy; the Graph copy waits for its syncer's check.
func TestMigration0019EditedDrafts(t *testing.T) {
	ctx := context.Background()
	path := migrateRawTo(t, 18,
		`INSERT INTO folders (id, account_id, mailbox, name, path, role, uidvalidity) VALUES ('f_drafts', 'acc', 'Drafts', 'Drafts', 'Drafts', 'drafts', 5)`,
		`INSERT INTO messages (id, account_id, folder_id, uid, rfc_message_id, flags) VALUES ('m_copy', 'acc', 'f_drafts', 7, 'v@x', '["\\Draft"]')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, rfc_message_id, flags) VALUES ('m_plain', 'acc', 'f_drafts', 8, 'p@x', '["\\Draft"]')`,
		// Linked, with the copy the frozen 0018 left on record.
		`INSERT INTO drafts (id, account_id, version, subject, created_at, updated_at, rfc_message_id, server_folder_id, server_uidvalidity, server_uid, synced_version, synced_at, local)
			VALUES ('d_linked', 'acc', 2, 's', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'v@x', 'f_drafts', 5, 7, 2, '2026-10-01T00:00:00.000Z', 1)`,
		`INSERT INTO drafts (id, account_id, version, subject, created_at, updated_at, rfc_message_id, server_folder_id, server_remote_id, synced_version, synced_at, local)
			VALUES ('d_graph', 'acc', 1, 's', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'g@x', 'f_drafts', 'AAMk-9', 1, '2026-10-01T00:00:00.000Z', 1)`,
		`INSERT INTO drafts (id, account_id, version, subject, created_at, updated_at, local) VALUES ('d_nocopy', 'acc', 3, 's', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 1)`,
		`INSERT INTO drafts (id, account_id, version, subject, created_at, updated_at, local) VALUES ('d_unlinked', 'acc', 1, 's', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 1)`,
		`INSERT INTO drafts (id, account_id, version, subject, created_at, updated_at, rfc_message_id, server_folder_id, server_uidvalidity, server_uid, synced_version)
			VALUES ('d_plain', 'acc', 1, 's', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'p@x', 'f_drafts', 5, 8, 1)`,
		`INSERT INTO board_cases (id, account_id, thread_id, rule_state, draft_id, version) VALUES ('c_1', 'acc', 't_1', 'you', 'd_linked', 4)`,
		`INSERT INTO board_cases (id, account_id, thread_id, rule_state, draft_id, version) VALUES ('c_3', 'acc', 't_3', 'you', 'd_graph', 6)`,
		`INSERT INTO board_cases (id, account_id, thread_id, rule_state, draft_id, version) VALUES ('c_4', 'acc', 't_4', 'you', 'd_nocopy', 2)`,
		`INSERT INTO board_annotations (case_id, input_key, created_at) VALUES ('c_1', 'k', '2026-10-01T00:00:00.000Z')`,
		`INSERT INTO board_runs (id, trigger, started_at) VALUES ('r_1', 'manual', '2026-10-01T00:00:00.000Z')`,
		`DELETE FROM board_dirty`,
		`INSERT INTO board_dirty (account_id, thread_id) VALUES ('acc', 't_9')`,
	)
	s, err := Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, want := range map[string]bool{"d_linked": true, "d_graph": true, "d_nocopy": true, "d_unlinked": true, "d_plain": false} {
		d, err := s.GetDraft(ctx, "acc", id)
		if err != nil || d.Local != want || d.Edited != want {
			t.Fatalf("%s after 0019: local=%v edited=%v %v", id, d.Local, d.Edited, err)
		}
		if want && !d.Copy.IsZero() {
			t.Fatalf("%s kept its copy columns: %+v", id, d.Copy)
		}
		if !want && (d.Copy.UID != 8 || d.Copy.RFCMessageID != "p@x" || d.SyncedVersion != 1) {
			t.Fatalf("an ordinary draft lost its copy: %+v", d)
		}
	}
	if d, _ := s.GetDraft(ctx, "acc", "d_linked"); d.SyncedVersion != 0 || d.Version != 2 {
		t.Fatalf("d_linked after 0019: %+v", d)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 2 {
		t.Fatalf("stray copies after 0019: %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM draft_stray_copies WHERE account_id = 'acc'
		AND ((uid = 7 AND uidvalidity = 5 AND folder_id = 'f_drafts' AND rfc_message_id = 'v@x' AND remote_id = '')
		  OR (remote_id = 'AAMk-9' AND rfc_message_id = 'g@x' AND synced_at = '2026-10-01T00:00:00.000Z'))
		AND recorded_at != ''`); n != 2 {
		t.Fatalf("stray copies' columns after 0019: %d", n)
	}
	for id, v := range map[string]int{"c_1": 4, "c_3": 6, "c_4": 2} {
		if n := countRows(t, s, `SELECT version FROM board_cases WHERE id = ?`, id); n != v {
			t.Fatalf("case %s version %d after 0019, was %d", id, n, v)
		}
	}
	if got := dirtyThreads(t, s); len(got) != 1 || !got["acc/t_9"] {
		t.Fatalf("dirty set after 0019: %v", got)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM board_annotations`) + countRows(t, s, `SELECT COUNT(*) FROM board_runs`); n != 2 {
		t.Fatalf("annotations and runs after 0019: %d", n)
	}
	// Linked or not, a local draft is not for the sweep any more.
	if got, err := s.UnlinkedLocalDrafts(ctx, time.Now().Add(time.Hour), 0); err != nil || len(got) != 0 {
		t.Fatalf("for the sweep after 0019: %v %v", got, err)
	}

	// The first pass (the upkeep, or the IMAP syncer) deletes the IMAP
	// copy; the Graph copy waits for its syncer.
	accts, err := s.DropStrayDraftCopies(ctx, "", nil, time.Now())
	if err != nil || !slices.Equal(accts, []string{"acc"}) {
		t.Fatalf("first pass: %v %v", accts, err)
	}
	if _, err := s.GetMessage(ctx, "acc", "m_copy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the copy's row stayed: %v", err)
	}
	if _, err := s.GetMessage(ctx, "acc", "m_plain"); err != nil {
		t.Fatalf("an ordinary draft's copy went: %v", err)
	}
	if uids := deleteOps(t, s); !slices.Equal(uids, []uint32{7}) {
		t.Fatalf("delete ops after the first pass: %v", uids)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM draft_stray_copies WHERE remote_id = 'AAMk-9'`); n != 1 {
		t.Fatalf("the Graph copy did not wait: %d", n)
	}

	// The unlinked one, edited now, is made ordinary by the upkeep's
	// safety net rather than swept.
	if accts, err := s.ReleaseEditedLocalDrafts(ctx); err != nil || !slices.Equal(accts, []string{"acc"}) {
		t.Fatalf("release edited: %v %v", accts, err)
	}
	if d, _ := s.GetDraft(ctx, "acc", "d_unlinked"); d.Local {
		t.Fatal("the unlinked edited draft is still local")
	}

	clearDirty(t, s)
	if err := s.DeleteDraft(ctx, "acc", "d_linked"); err != nil {
		t.Fatal(err)
	}
	if !dirtyThreads(t, s)["acc/t_1"] {
		t.Fatal("drafts_board_ad did not mark the case's thread")
	}
}

// The full chain 0001→0019 on an empty store: drafts has local and edited,
// draft_stray_copies and its index exist, 0018's objects are there, and
// the version is the last migration's.
func TestMigrationChainLocalDrafts(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`SELECT COUNT(*) FROM pragma_table_info('drafts') WHERE name IN ('local', 'edited')`,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('draft_stray_copies', 'draft_stray_copies_by_account')`,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('drafts_local', 'drafts_board_ad')`,
	} {
		if n := countRows(t, s, q); n != 2 {
			t.Fatalf("%s: %d", q, n)
		}
	}
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	last := migs[len(migs)-1].version
	if last != 19 {
		t.Fatalf("last migration %d", last)
	}
	if n := countRows(t, s, `SELECT MAX(version) FROM schema_migrations`); n != last {
		t.Fatalf("schema version %d, want %d", n, last)
	}
}

// Copies that arrive for a local draft before they can be addressed (known
// by their Message-ID only) go to draft_stray_copies, every one of them —
// not only the first — and are deleted once the folder's pass has stored
// their rows; one that never becomes addressable is given up after
// StrayDraftCopyKeep, with nothing queued.
func TestLocalDraftPendingCopy(t *testing.T) {
	x := newLocalBox(t)
	d := x.draft(t, true)
	for _, rfc := range []string{"late1@x", "late2@x", "never@x"} {
		c := DraftCopy{FolderID: x.drafts.ID, RFCMessageID: rfc}
		if found, err := x.s.MarkDraftSynced(x.ctx, "acc", d.ID, d.Version, c, false); err != nil || found {
			t.Fatalf("mark synced: %v %v", found, err)
		}
	}
	if got := x.get(t, d.ID); !got.Copy.IsZero() || got.SyncedVersion != 0 {
		t.Fatalf("a local draft recorded a copy: %+v", got)
	}
	if n := countRows(t, x.s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 3 {
		t.Fatalf("stray copies: %d", n)
	}
	if accts, err := x.s.DropStrayDraftCopies(x.ctx, "acc", nil, time.Now()); err != nil || len(accts) != 0 {
		t.Fatalf("drop before the rows: %v %v", accts, err)
	}
	r1 := seedCopy(t, x.s, x.drafts, 40, "", "late1@x")
	r2 := seedCopy(t, x.s, x.drafts, 41, "", "late2@x")
	if accts, err := x.s.DropStrayDraftCopies(x.ctx, "other", nil, time.Now()); err != nil || len(accts) != 0 {
		t.Fatalf("another account's drop: %v %v", accts, err)
	}
	if accts, err := x.s.DropStrayDraftCopies(x.ctx, "acc", nil, time.Now()); err != nil || !slices.Equal(accts, []string{"acc"}) {
		t.Fatalf("drop after the rows: %v %v", accts, err)
	}
	for _, r := range []*Message{r1, r2} {
		if _, err := x.s.GetMessage(x.ctx, "acc", r.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("a copy's row stayed: %v", err)
		}
	}
	if uids := deleteOps(t, x.s); !slices.Equal(uids, []uint32{40, 41}) {
		t.Fatalf("delete ops: %v", uids)
	}
	if n := countRows(t, x.s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 1 {
		t.Fatalf("stray copies left: %d", n)
	}
	// Within the week it stays; after it, it is forgotten.
	if _, err := x.s.DropStrayDraftCopies(x.ctx, "acc", nil, time.Now().Add(StrayDraftCopyKeep-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, x.s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 1 {
		t.Fatalf("given up too early: %d", n)
	}
	if accts, err := x.s.DropStrayDraftCopies(x.ctx, "acc", nil, time.Now().Add(StrayDraftCopyKeep+time.Hour)); err != nil || len(accts) != 0 {
		t.Fatalf("give up: %v %v", accts, err)
	}
	if n := countRows(t, x.s, `SELECT COUNT(*) FROM draft_stray_copies`); n != 0 {
		t.Fatalf("not given up: %d", n)
	}
	if uids := deleteOps(t, x.s); !slices.Equal(uids, []uint32{40, 41}) {
		t.Fatalf("delete ops after giving up: %v", uids)
	}
}
