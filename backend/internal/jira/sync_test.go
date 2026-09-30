// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// scene is the site most sync tests start from (fictional Acme data):
//
//	ITSD-1 "Tiskárna nefunguje"  Petr's, 10 days old: an old comment of
//	       Petr's, a status change 5 days ago, a comment of Petr's a day
//	       ago, one of Jana's (the user) two hours ago
//	ITSD-2 "Hotový požadavek"    done 2 days ago
//	WEB-1  "Nový web"            Jana's, a day old, assigned to her, watched
//	WEB-2  "Stará úloha"         60 days old, open, assigned to Jana
//	WEB-3  "Zapomenutá úloha"    60 days old, nobody's
//	WEB-4  "Nová chyba"          Petr's, a day old
//	MOB-1  "Aplikace padá"       in a space that is not selected
type scene struct {
	a, done, b, oldOpen, oldIdle, g, mob *jiratest.Issue
	cOld, cFresh, cMine                  string
}

func newScene(h *harness) *scene {
	f := h.f
	sc := &scene{}
	h.at(h.ago(10*day), func() {
		sc.a = f.AddIssue("ITSD", "Tiskárna nefunguje", func(is *jiratest.Issue) {
			is.Reporter = f.Petr
			is.Description = "<p>Tiskárna ve 3. patře <b>netiskne</b>.</p>"
		})
		sc.cOld = f.AddComment(sc.a.ID, f.Petr, "<p>Zkoušel jsem restart.</p>")
	})
	h.at(h.ago(5*day), func() { f.SetStatus(sc.a.ID, "3", f.Petr) })
	h.at(h.ago(1*day), func() { sc.cFresh = f.AddComment(sc.a.ID, f.Petr, "<p>Pořád to nejde.</p>") })
	h.at(h.ago(2*time.Hour), func() { sc.cMine = f.AddComment(sc.a.ID, f.Me, "<p>Podívám se na to.</p>") })
	h.at(h.ago(4*day), func() {
		sc.done = f.AddIssue("ITSD", "Hotový požadavek", func(is *jiratest.Issue) { is.Reporter = f.Petr })
	})
	h.at(h.ago(2*day), func() { f.SetStatus(sc.done.ID, "10001", f.Petr) })
	h.at(h.ago(1*day), func() {
		sc.b = f.AddIssue("WEB", "Nový web", func(is *jiratest.Issue) {
			is.Assignee, is.Watching = f.Me, true
			is.Description = "<p>Zadání webu.</p>"
		})
		sc.g = f.AddIssue("WEB", "Nová chyba", func(is *jiratest.Issue) { is.Reporter = f.Petr })
		sc.mob = f.AddIssue("MOB", "Aplikace padá")
	})
	h.at(h.ago(60*day), func() {
		sc.oldOpen = f.AddIssue("WEB", "Stará úloha", func(is *jiratest.Issue) { is.Assignee = f.Me })
		sc.oldIdle = f.AddIssue("WEB", "Zapomenutá úloha", func(is *jiratest.Issue) { is.Reporter = f.Petr })
	})
	return sc
}

func modesRun(t *testing.T, fn func(t *testing.T, mode jiratest.Mode)) {
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) { fn(t, mode) })
	}
}

func TestSyncBackfill(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		sc := newScene(h)
		h.mustPass()
		ctx := context.Background()

		// Folders: the views, then the spaces by name; MOB is not selected.
		fs, err := h.st.ListFolders(ctx, h.acc.ID)
		if err != nil {
			t.Fatal(err)
		}
		var order []string
		for _, f := range fs {
			order = append(order, f.Mailbox+"="+f.Name+"/"+string(f.Virtual))
			if f.Role != api.RoleNone {
				t.Fatalf("folder %s role %s", f.Mailbox, f.Role)
			}
		}
		want := "view:assignedToMe=Assigned to Me/assignedToMe view:watching=Watching/watching view:open=Open/open " +
			"space:10000=IT Service Desk/ space:10001=Web/"
		if strings.Join(order, " ") != want {
			t.Fatalf("folders = %v", order)
		}
		if f := h.folder(spaceBox("10000")); f.DeltaLink != "window:30" || !f.LastSyncAt.Equal(syncT0) {
			t.Fatalf("space cursor = %q %v", f.DeltaLink, f.LastSyncAt)
		}

		// ITSD-1's messages, read by the three-day rule of a first backfill.
		itsd := h.rows(spaceBox("10000"))
		a := sc.a.ID
		var hist string
		for rid := range itsd {
			if strings.HasPrefix(rid, "h:") && itsd[rid].ThreadID == "jira:"+a {
				hist = rid
			}
		}
		for rid, wantSeen := range map[string]bool{
			"i:" + a: true, "c:" + sc.cOld: true, "c:" + sc.cFresh: false, "c:" + sc.cMine: true, hist: true,
		} {
			m, ok := itsd[rid]
			if !ok {
				t.Fatalf("no row %s in ITSD (have %v)", rid, keysOf(itsd))
			}
			if seen(m) != wantSeen {
				t.Errorf("%s seen = %v", rid, seen(m))
			}
			if m.Subject != "ITSD-1: Tiskárna nefunguje" || m.ThreadID != "jira:"+a || m.BodyState != store.BodyFetched {
				t.Errorf("%s = subject %q thread %q body %s", rid, m.Subject, m.ThreadID, m.BodyState)
			}
		}
		host := map[jiratest.Mode]string{jiratest.Cloud: "acme.atlassian.net", jiratest.DC: "jira.acme.test"}[mode]
		fresh := itsd["c:"+sc.cFresh]
		if fresh.RFCMessageID != "comment."+sc.cFresh+".issue."+a+"@"+host+".malachi.invalid" ||
			fresh.InReplyTo != "issue."+a+"@"+host+".malachi.invalid" {
			t.Fatalf("ids = %q, %q", fresh.RFCMessageID, fresh.InReplyTo)
		}
		wantFrom := "JIRAUSER10101@users.jira.invalid"
		if mode == jiratest.Cloud {
			wantFrom = "u-" + hash24(h.f.Petr) + "@users.jira.invalid"
		}
		if len(fresh.From) != 1 || fresh.From[0].Name != "Petr Svoboda" || fresh.From[0].Address != wantFrom {
			t.Fatalf("from = %+v", fresh.From)
		}
		if txt := h.text(fresh.ID); !contains(txt, "Pořád to nejde.") {
			t.Fatalf("text = %q", txt)
		}
		if ev := h.text(itsd[hist].ID); ev != "To Do → In Progress" {
			t.Fatalf("event text = %q", ev)
		}
		if _, ok := itsd["i:"+sc.done.ID]; !ok {
			t.Fatal("the done issue is missing from its space")
		}

		// WEB: Jana's own issue read; Petr's new one unread; the old open
		// one assigned to Jana kept beyond the window; the old idle one
		// and MOB not synchronised.
		web := h.rows(spaceBox("10001"))
		if !seen(web["i:"+sc.b.ID]) || seen(web["i:"+sc.g.ID]) {
			t.Fatalf("WEB flags: own %v, Petr's %v", web["i:"+sc.b.ID].Flags, web["i:"+sc.g.ID].Flags)
		}
		if _, ok := web["i:"+sc.oldOpen.ID]; !ok {
			t.Fatal("the old open issue assigned to the user is missing")
		}
		if !seen(web["i:"+sc.oldOpen.ID]) {
			t.Fatal("the old open issue is unread")
		}
		if _, ok := web["i:"+sc.oldIdle.ID]; ok {
			t.Fatal("an issue outside the window was synchronised")
		}
		if _, ok := h.issue(sc.mob.ID); ok {
			t.Fatal("an issue of an unselected space was synchronised")
		}

		// Views.
		assigned := h.rows(viewBox(api.VirtualAssignedToMe))
		if len(assigned) != 2 || assigned["i:"+sc.b.ID].ID == "" || assigned["i:"+sc.oldOpen.ID].ID == "" {
			t.Fatalf("assigned = %v", keysOf(assigned))
		}
		watching := h.rows(viewBox(api.VirtualWatching))
		if len(watching) != 1 || watching["i:"+sc.b.ID].ID == "" {
			t.Fatalf("watching = %v", keysOf(watching))
		}
		open := h.rows(viewBox(api.VirtualOpen))
		if _, ok := open["i:"+sc.done.ID]; ok {
			t.Fatal("a done issue is in the open view")
		}
		if open["c:"+sc.cFresh].ID == "" || seen(open["c:"+sc.cFresh]) || !seen(open["c:"+sc.cOld]) {
			t.Fatalf("open view copies: %v", keysOf(open))
		}
		if open["c:"+sc.cFresh].RFCMessageID != fresh.RFCMessageID || open["c:"+sc.cFresh].ThreadID != fresh.ThreadID {
			t.Fatal("a view copy differs from its space copy")
		}
		if f := h.folder(viewBox(api.VirtualOpen)); f.Unread != 2 {
			t.Fatalf("open view unread = %d", f.Unread) // ITSD-1's fresh comment, WEB-4
		}

		// The issue rows, items and the user.
		is, ok := h.issue(a)
		if !ok || is.Key != "ITSD-1" || is.Status != "In Progress" || is.StatusCategory != api.StatusCategoryInProgress ||
			is.ReporterName != "Petr Svoboda" || !is.ServiceDesk || !is.SyncedUpdated.Equal(is.Updated) || is.RenderKey == "" ||
			fmt.Sprint(is.Views) != "[open]" {
			t.Fatalf("issue = %+v", is)
		}
		items, err := h.st.IssueItems(ctx, h.acc.ID, a)
		if err != nil || len(items) != 5 {
			t.Fatalf("items = %+v, %v", items, err)
		}
		for _, it := range items {
			switch it.Kind {
			case api.IssueItemComment:
				if it.Visibility != api.CommentPublic {
					t.Errorf("comment %s visibility %q", it.RemoteID, it.Visibility)
				}
			case api.IssueItemEvent:
				if fmt.Sprint(it.Changes) != "[{status To Do In Progress}]" || it.AuthorID != h.f.Petr {
					t.Errorf("event = %+v", it)
				}
			}
		}
		b, _ := h.issue(sc.b.ID)
		if b.AssigneeID != h.f.Me || !b.Watching || b.ServiceDesk || fmt.Sprint(b.Views) != "[assignedToMe watching open]" {
			t.Fatalf("WEB-1 = %+v", b)
		}
		raw, ok, err := h.st.GetMeta(ctx, store.MetaIssueMePrefix+h.acc.ID)
		var me meMeta
		if err != nil || !ok || json.Unmarshal([]byte(raw), &me) != nil || me.ID != h.f.Me || me.Name != "Jana Dvořáková" || me.TimeZone != "Europe/Prague" {
			t.Fatalf("me = %q %v %v", raw, ok, err)
		}
		spaces, err := h.st.IssueSpaces(ctx, h.acc.ID)
		if err != nil || len(spaces) != 2 || spaces[0].Key != "ITSD" || !spaces[0].ServiceDesk || spaces[1].Name != "Web" {
			t.Fatalf("spaces = %+v, %v", spaces, err)
		}

		if news := h.notes.takeNews(); len(news) != 0 {
			t.Fatalf("a first backfill announced %d messages", len(news))
		}
	})
}

func TestSyncIncremental(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		sc := newScene(h)
		h.mustPass()
		h.notes.takeNews()
		f := h.f

		h.mu.Lock()
		storedBefore := len(h.stored)
		h.mu.Unlock()
		h.clock.advance(10 * time.Minute)
		petr := f.AddComment(sc.a.ID, f.Petr, "<p>Už to funguje?</p>")
		mine := f.AddComment(sc.a.ID, f.Me, "<p>Ano, vyměnil jsem toner.</p>")
		f.SetStatus(sc.a.ID, "10001", f.Me)
		h.clock.advance(time.Minute)
		newIssue := f.AddIssue("WEB", "Rozbitý formulář", func(is *jiratest.Issue) { is.Reporter = f.Petr })
		h.clock.advance(time.Minute)
		h.mustPass()

		itsd := h.rows(spaceBox("10000"))
		// What did not change was not built again: of ITSD-1 only the new
		// items (and their view copies) were stored.
		h.mu.Lock()
		rebuilt := map[string]bool{}
		for _, c := range h.stored[storedBefore:] {
			rebuilt[c.id] = true
		}
		h.mu.Unlock()
		for _, rid := range []string{"i:" + sc.a.ID, "c:" + sc.cOld, "c:" + sc.cFresh} {
			if rebuilt[itsd[rid].ID] {
				t.Errorf("%s was built again", rid)
			}
		}
		if !rebuilt[itsd["c:"+petr].ID] {
			t.Error("the new comment was not stored")
		}
		if m := itsd["c:"+petr]; m.ID == "" || seen(m) || m.BodyState != store.BodyFetched {
			t.Fatalf("Petr's new comment = %+v", m)
		}
		if m := itsd["c:"+mine]; m.ID == "" || !seen(m) {
			t.Fatalf("the user's own comment = %+v", m)
		}
		var events int
		for rid, m := range itsd {
			if strings.HasPrefix(rid, "h:") && m.ThreadID == "jira:"+sc.a.ID {
				events++
				if !seen(m) {
					t.Errorf("event %s unread", rid)
				}
			}
		}
		if events != 2 {
			t.Fatalf("events = %d", events)
		}
		// Done now: ITSD-1 left the open view.
		open := h.rows(viewBox(api.VirtualOpen))
		for rid, m := range open {
			if m.ThreadID == "jira:"+sc.a.ID {
				t.Fatalf("a done issue kept %s in the open view", rid)
			}
		}
		news := h.notes.takeNews()
		var got []string
		for _, n := range news {
			got = append(got, n.Message.Subject+"|"+string(n.FolderID))
			if n.AccountID != api.AccountID(h.acc.ID) || n.Message.ID == "" {
				t.Fatalf("notification = %+v", n)
			}
		}
		wantNews := []string{
			"ITSD-1: Tiskárna nefunguje|" + h.folder(spaceBox("10000")).ID,
			"WEB-5: Rozbitý formulář|" + h.folder(spaceBox("10001")).ID,
		}
		if len(got) != 2 || !(got[0] == wantNews[0] && got[1] == wantNews[1] || got[0] == wantNews[1] && got[1] == wantNews[0]) {
			t.Fatalf("notifications = %v", got)
		}
		if web := h.rows(spaceBox("10001")); seen(web["i:"+newIssue.ID]) {
			t.Fatal("a new issue of someone else's is read")
		}

		// Nothing new: nothing announced, nothing rebuilt.
		h.clock.advance(time.Minute)
		h.mu.Lock()
		before := len(h.stored)
		h.mu.Unlock()
		h.mustPass()
		h.mu.Lock()
		after := len(h.stored)
		h.mu.Unlock()
		if news := h.notes.takeNews(); len(news) != 0 || after != before {
			t.Fatalf("an idle pass announced %d, stored %d bodies", len(news), after-before)
		}
	})
}

func TestEditedCommentKeepsFlags(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		sc := newScene(h)
		h.mustPass()
		ctx := context.Background()
		space := h.rows(spaceBox("10000"))["c:"+sc.cFresh]
		if err := h.st.FlagMessages(ctx, h.acc.ID, []string{space.ID}, []api.Flag{api.FlagSeen, api.FlagFlagged}, nil); err != nil {
			t.Fatal(err)
		}
		h.clock.advance(10 * time.Minute)
		h.f.EditComment(sc.a.ID, sc.cFresh, "<p>Opraveno: pořád to nejde ani po restartu.</p>")
		h.clock.advance(time.Minute)
		h.mustPass()

		for _, box := range []string{spaceBox("10000"), viewBox(api.VirtualOpen)} {
			m := h.rows(box)["c:"+sc.cFresh]
			if !seen(m) || !hasFlag(m.Flags, api.FlagFlagged) {
				t.Fatalf("%s flags = %v", box, m.Flags)
			}
			if txt := h.text(m.ID); !contains(txt, "Opraveno") {
				t.Fatalf("%s text = %q", box, txt)
			}
			if m.ID != space.ID && box == spaceBox("10000") {
				t.Fatal("the edited comment got a new row")
			}
		}
		items, _ := h.st.IssueItems(ctx, h.acc.ID, sc.a.ID)
		for _, it := range items {
			if it.RemoteID == "c:"+sc.cFresh && !it.Edited {
				t.Fatal("the edited comment is not marked edited")
			}
			if it.RemoteID == "c:"+sc.cOld && it.Edited {
				t.Fatal("an unedited comment is marked edited")
			}
		}
		if news := h.notes.takeNews(); len(news) != 0 {
			t.Fatalf("an edit announced %d messages", len(news))
		}
	})
}

func TestFlagsFollowEveryCopy(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	sc := newScene(h)
	h.mustPass()
	ctx := context.Background()
	view := h.rows(viewBox(api.VirtualOpen))["c:"+sc.cFresh]
	if err := h.st.FlagMessages(ctx, h.acc.ID, []string{view.ID}, []api.Flag{api.FlagSeen}, nil); err != nil {
		t.Fatal(err)
	}
	space := h.rows(spaceBox("10000"))["c:"+sc.cFresh]
	if err := h.st.FlagMessages(ctx, h.acc.ID, []string{space.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	// Offline: the flags still reach every copy.
	h.f.FailNext(jiratest.Failure{Path: "/search/jql", Status: 503, Times: 3})
	if err := h.pass(false); err == nil {
		t.Fatal("the pass did not fail")
	}
	for _, box := range []string{spaceBox("10000"), viewBox(api.VirtualOpen)} {
		m := h.rows(box)["c:"+sc.cFresh]
		if !seen(m) || !hasFlag(m.Flags, api.FlagFlagged) {
			t.Fatalf("%s flags = %v", box, m.Flags)
		}
	}
	if n, _ := h.st.CountPendingOps(ctx, h.acc.ID); n != 0 {
		t.Fatalf("%d operations left", n)
	}
	// The later change wins everywhere.
	if err := h.st.FlagMessages(ctx, h.acc.ID, []string{space.ID}, nil, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	h.mustPass()
	if m := h.rows(viewBox(api.VirtualOpen))["c:"+sc.cFresh]; seen(m) {
		t.Fatal("the view copy stayed read")
	}
	if f := h.folder(viewBox(api.VirtualOpen)); f.Unread != 2 {
		t.Fatalf("open view unread = %d", f.Unread)
	}
	// A view the issue joins later inherits the flags.
	h.clock.advance(10 * time.Minute)
	h.f.Assign(sc.a.ID, h.f.Me, h.f.Petr)
	h.clock.advance(time.Minute)
	h.mustPass()
	m := h.rows(viewBox(api.VirtualAssignedToMe))["c:"+sc.cFresh]
	if m.ID == "" || seen(m) || !hasFlag(m.Flags, api.FlagFlagged) {
		t.Fatalf("new view copy = %+v", m)
	}
}

func TestRenameMoveAndScope(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		sc := newScene(h)
		h.mustPass()
		f := h.f
		gDesc := h.rows(spaceBox("10001"))["i:"+sc.g.ID]
		if err := h.st.FlagMessages(context.Background(), h.acc.ID, []string{gDesc.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
			t.Fatal(err)
		}

		h.clock.advance(10 * time.Minute)
		f.Update(sc.a.ID, func(is *jiratest.Issue) { is.Summary = "Tiskárna opět tiskne" })
		newKey := f.MoveIssue(sc.g.ID, "ITSD")
		f.MoveIssue(sc.b.ID, "MOB")
		h.clock.advance(time.Minute)
		h.mustPass()

		for _, box := range []string{spaceBox("10000"), viewBox(api.VirtualOpen)} {
			for rid, m := range h.rows(box) {
				if m.ThreadID == "jira:"+sc.a.ID && m.Subject != "ITSD-1: Tiskárna opět tiskne" {
					t.Fatalf("%s %s subject %q", box, rid, m.Subject)
				}
			}
		}
		itsd, web := h.rows(spaceBox("10000")), h.rows(spaceBox("10001"))
		moved := itsd["i:"+sc.g.ID]
		if moved.ID == "" || moved.Subject != newKey+": Nová chyba" || !hasFlag(moved.Flags, api.FlagFlagged) {
			t.Fatalf("moved issue = %+v", moved)
		}
		if _, ok := web["i:"+sc.g.ID]; ok {
			t.Fatal("the moved issue stayed in its old space")
		}
		if is, _ := h.issue(sc.g.ID); is.Key != newKey || is.SpaceID != "10000" {
			t.Fatalf("moved issue row = %+v", is)
		}
		// Moved to a space that is not selected: the incremental search
		// no longer sees it, the hourly reconciliation finds it gone.
		h.clock.advance(time.Hour)
		h.mustPass()
		if _, ok := h.issue(sc.b.ID); ok {
			t.Fatal("an issue moved out of the selected spaces is kept")
		}
		for _, box := range []string{spaceBox("10001"), viewBox(api.VirtualAssignedToMe), viewBox(api.VirtualWatching)} {
			for rid, m := range h.rows(box) {
				if m.ThreadID == "jira:"+sc.b.ID {
					t.Fatalf("%s kept %s", box, rid)
				}
			}
		}
		if news := h.notes.takeNews(); len(news) != 0 {
			t.Fatalf("renames and moves announced %d messages", len(news))
		}
	})
}

func TestReconcileDeletes(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		sc := newScene(h)
		h.mustPass()
		h.f.DeleteIssue(sc.done.ID)
		h.f.DeleteComment(sc.a.ID, sc.cOld)

		// A deleted issue does not show in the incremental search; the
		// hourly reconciliation finds it gone.
		h.clock.advance(10 * time.Minute)
		h.mustPass()
		if _, ok := h.issue(sc.done.ID); !ok {
			t.Fatal("deleted before the reconciliation was due")
		}
		// The deleted comment went with the issue's change.
		if _, ok := h.rows(spaceBox("10000"))["c:"+sc.cOld]; ok {
			t.Fatal("a deleted comment was kept")
		}
		if _, ok := h.rows(viewBox(api.VirtualOpen))["c:"+sc.cOld]; ok {
			t.Fatal("a deleted comment was kept in a view")
		}
		h.clock.advance(time.Hour)
		h.mustPass()
		if _, ok := h.issue(sc.done.ID); ok {
			t.Fatal("a deleted issue was kept")
		}
		if _, ok := h.rows(spaceBox("10000"))["i:"+sc.done.ID]; ok {
			t.Fatal("a deleted issue's rows were kept")
		}
		if _, ok := h.issue(sc.a.ID); !ok {
			t.Fatal("reconciliation deleted a live issue")
		}
	})
}

// incompleteIDs makes every id enumeration end at its page cap.
type incompleteIDs struct {
	Remote
	n int
}

func (r *incompleteIDs) SearchIDs(ctx context.Context, jql string, cursor Cursor) (IDPage, error) {
	r.n++
	return IDPage{IDs: []string{fmt.Sprint(900000 + r.n)}, Next: Cursor(fmt.Sprintf("p%d", r.n))}, nil
}

func TestReconcileNeedsCompleteEnumerations(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	sc := newScene(h)
	h.mustPass()
	h.f.DeleteIssue(sc.done.ID)
	h.syncer.deps.Remote = &incompleteIDs{Remote: h.syncer.deps.Remote}
	h.clock.advance(2 * time.Hour)
	h.mustPass()
	if _, ok := h.issue(sc.done.ID); !ok {
		t.Fatal("an issue was deleted on an incomplete enumeration")
	}
}

func TestWindowRetention(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		f := h.f
		var mid, midOpen, far *jiratest.Issue
		h.at(h.ago(20*day), func() {
			mid = f.AddIssue("WEB", "Dvacet dní", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			midOpen = f.AddIssue("WEB", "Dvacet dní, moje", func(is *jiratest.Issue) { is.Reporter, is.Assignee = f.Petr, f.Me })
			f.AddComment(mid.ID, f.Petr, "<p>Stará poznámka.</p>")
		})
		h.at(h.ago(45*day), func() { far = f.AddIssue("WEB", "Čtyřicet pět dní") })
		h.mustPass()
		if _, ok := h.issue(far.ID); ok {
			t.Fatal("an issue beyond 30 days was synchronised")
		}

		h.reconfigure(func(c *api.JiraConfig) { c.OfflineDays = 10 })
		h.clock.advance(time.Minute)
		h.mustPass()
		if _, ok := h.issue(mid.ID); ok {
			t.Fatal("the window shrank but the issue stayed")
		}
		if _, ok := h.issue(midOpen.ID); !ok {
			t.Fatal("an open issue assigned to the user left with the window")
		}
		if f := h.folder(spaceBox("10001")); f.DeltaLink != "window:10" {
			t.Fatalf("cursor = %q", f.DeltaLink)
		}

		h.reconfigure(func(c *api.JiraConfig) { c.OfflineDays = 60 })
		h.clock.advance(time.Minute)
		h.mustPass()
		web := h.rows(spaceBox("10001"))
		for _, is := range []*jiratest.Issue{mid, far} {
			m, ok := web["i:"+is.ID]
			if !ok || !seen(m) {
				t.Fatalf("%s after the window grew: %+v", is.Key, m)
			}
		}
		if news := h.notes.takeNews(); len(news) != 0 {
			t.Fatalf("a grown window announced %d old messages", len(news))
		}
	})
}

func TestOnlyMine(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode, func(c *api.JiraConfig) { c.OnlyMine = true })
		f := h.f
		var others, reported, commented, watched *jiratest.Issue
		h.at(h.ago(3*day), func() {
			others = f.AddIssue("ITSD", "Cizí požadavek", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			reported = f.AddIssue("ITSD", "Můj požadavek")
			commented = f.AddIssue("ITSD", "Okomentovaný", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			f.AddComment(commented.ID, f.Me, "<p>Přidávám se.</p>")
			watched = f.AddIssue("WEB", "Sledovaný", func(is *jiratest.Issue) { is.Reporter, is.Watching = f.Petr, true })
		})
		h.mustPass()
		if _, ok := h.issue(others.ID); ok {
			t.Fatal("someone else's issue was synchronised")
		}
		for _, is := range []*jiratest.Issue{reported, watched} {
			if _, ok := h.issue(is.ID); !ok {
				t.Fatalf("%s is missing", is.Key)
			}
		}
		// Data Center has no updatedBy(): an issue the user only commented
		// on is not found there.
		if _, ok := h.issue(commented.ID); ok != (mode == jiratest.Cloud) {
			t.Fatalf("commented issue stored = %v", ok)
		}

		// Watching stops, which leaves the issue's updated time alone: the
		// reconciliation drops it.
		f.SetWatching(watched.ID, false)
		h.clock.advance(2 * time.Hour)
		h.mustPass()
		if _, ok := h.issue(watched.ID); ok {
			t.Fatal("an issue no longer the user's was kept")
		}
		if _, ok := h.issue(reported.ID); !ok {
			t.Fatal("the user's issue was dropped")
		}
	})
}

func TestHideEventsToggle(t *testing.T) {
	h := newHarness(t, jiratest.DC)
	sc := newScene(h)
	h.mustPass()
	countEvents := func(box string) int {
		n := 0
		for rid, m := range h.rows(box) {
			if strings.HasPrefix(rid, "h:") && m.ThreadID == "jira:"+sc.a.ID {
				n++
			}
		}
		return n
	}
	if countEvents(spaceBox("10000")) != 1 || countEvents(viewBox(api.VirtualOpen)) != 1 {
		t.Fatal("no event before the toggle")
	}
	h.reconfigure(func(c *api.JiraConfig) { c.HideEvents = true })
	h.clock.advance(time.Minute)
	h.mustPass()
	if countEvents(spaceBox("10000")) != 0 || countEvents(viewBox(api.VirtualOpen)) != 0 {
		t.Fatal("events stayed after hideEvents")
	}
	items, _ := h.st.IssueItems(context.Background(), h.acc.ID, sc.a.ID)
	for _, it := range items {
		if it.Kind == api.IssueItemEvent {
			t.Fatal("an event item stayed after hideEvents")
		}
	}
	h.reconfigure(func(c *api.JiraConfig) { c.HideEvents = false })
	h.clock.advance(time.Minute)
	h.mustPass()
	if countEvents(spaceBox("10000")) != 1 {
		t.Fatal("events did not come back")
	}
	for rid, m := range h.rows(spaceBox("10000")) {
		if strings.HasPrefix(rid, "h:") && !seen(m) {
			t.Fatal("a returning event is unread")
		}
	}
	if news := h.notes.takeNews(); len(news) != 0 {
		t.Fatalf("events announced %d messages", len(news))
	}
}

func TestBotReattributionAndRerender(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		const botName = "Issue Sync – Synchronization for Jira"
		h := newHarness(t, mode, func(c *api.JiraConfig) {
			c.BotNames = []string{botName}
			c.MetadataFilters = []string{`^Remote comment create date:.*$`}
		})
		f := h.f
		f.AddUser(&jiratest.User{ID: "JIRAUSER10200", Name: "issuesync", Display: botName})
		var is *jiratest.Issue
		var relayed string
		h.at(h.ago(1*day), func() {
			is = f.AddIssue("ITSD", "Synchronizovaný požadavek", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			relayed = f.AddComment(is.ID, "JIRAUSER10200", "<p>ITSD-9 Eva Horáková added comment - 18/09/26 10:00 GMT</p>"+
				"<p>Remote comment create date: 18/09/26 10:00 GMT</p><p>Dobrý den, posílám podklady.</p>")
		})
		h.mustPass()
		m := h.rows(spaceBox("10000"))["c:"+relayed]
		if len(m.From) != 1 || m.From[0].Name != "Eva Horáková" || !strings.HasPrefix(m.From[0].Address, "n-") ||
			!m.Date.Equal(time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)) {
			t.Fatalf("relayed comment = from %+v date %v", m.From, m.Date)
		}
		if txt := h.text(m.ID); contains(txt, "added comment") || contains(txt, "Remote comment") || !contains(txt, "posílám podklady") {
			t.Fatalf("relayed text = %q", txt)
		}
		items, _ := h.st.IssueItems(context.Background(), h.acc.ID, is.ID)
		var via string
		for _, it := range items {
			if it.RemoteID == "c:"+relayed {
				via = it.Via
			}
		}
		if via != "Issue Sync" {
			t.Fatalf("via = %q", via)
		}

		// Without the rule the bot is the author again: the stored
		// comment is rebuilt in place.
		h.reconfigure(func(c *api.JiraConfig) { c.BotNames, c.MetadataFilters = nil, nil })
		h.clock.advance(time.Minute)
		h.mustPass()
		again := h.rows(spaceBox("10000"))["c:"+relayed]
		if again.ID != m.ID || len(again.From) != 1 || again.From[0].Name != botName || !again.Date.Equal(syncT0.Add(-day)) {
			t.Fatalf("after the rule went: %+v %v", again.From, again.Date)
		}
		if txt := h.text(again.ID); !contains(txt, "added comment") {
			t.Fatalf("text after the rule went = %q", txt)
		}
		items, _ = h.st.IssueItems(context.Background(), h.acc.ID, is.ID)
		for _, it := range items {
			if it.RemoteID == "c:"+relayed && it.Via != "" {
				t.Fatalf("via kept: %q", it.Via)
			}
		}
	})
}

// TestRerenderAnnouncesMessagesChanged: a pass that rebuilds stored rows in
// place (other rendering rules, a rename, an edit) ends with one
// notify.messagesChanged on the jira account naming the folders holding
// the copies; a pass that only adds rows, or changes nothing, sends none.
func TestRerenderAnnouncesMessagesChanged(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		const botName = "Issue Sync – Synchronization for Jira"
		h := newHarness(t, mode)
		f := h.f
		f.AddUser(&jiratest.User{ID: "JIRAUSER10200", Name: "issuesync", Display: botName})
		var is *jiratest.Issue
		var relayed string
		h.at(h.ago(1*day), func() {
			is = f.AddIssue("ITSD", "Synchronizovaný požadavek", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			relayed = f.AddComment(is.ID, "JIRAUSER10200", "<p>ITSD-9 Eva Horáková added comment - 18/09/26 10:00 GMT</p>"+
				"<p>Remote comment create date: 18/09/26 10:00 GMT</p><p>Dobrý den, posílám podklady.</p>")
		})
		h.mustPass()
		if got := h.notes.takeChanged(); len(got) != 0 {
			t.Fatalf("the first pass announced changed messages: %+v", got)
		}
		h.clock.advance(time.Minute)
		h.mustPass()
		if got := h.notes.takeChanged(); len(got) != 0 {
			t.Fatalf("an idle pass announced changed messages: %+v", got)
		}
		want := []api.FolderID{api.FolderID(h.folder(spaceBox("10000")).ID), api.FolderID(h.folder(viewBox(api.VirtualOpen)).ID)}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		expectOne := func(what string) {
			t.Helper()
			got := h.notes.takeChanged()
			if len(got) != 1 {
				t.Fatalf("%s: %d notifications, want one: %+v", what, len(got), got)
			}
			if got[0].AccountID != api.AccountID(h.acc.ID) || !slices.Equal(got[0].FolderIDs, want) {
				t.Fatalf("%s: notification = %+v, want account %s folders %v", what, got[0], h.acc.ID, want)
			}
		}

		// New rendering rules: every stored item is rebuilt in place, the
		// relayed comment re-attributed.
		h.reconfigure(func(c *api.JiraConfig) {
			c.BotNames = []string{botName}
			c.MetadataFilters = []string{`^Remote comment create date:.*$`}
		})
		h.clock.advance(time.Minute)
		h.mustPass()
		expectOne("after the rules changed")
		m := h.rows(spaceBox("10000"))["c:"+relayed]
		if len(m.From) != 1 || m.From[0].Name != "Eva Horáková" {
			t.Fatalf("relayed comment from = %+v", m.From)
		}

		// A new comment only adds rows.
		h.clock.advance(10 * time.Minute)
		edited := f.AddComment(is.ID, f.Petr, "<p>Ještě jedna poznámka.</p>")
		h.clock.advance(time.Minute)
		h.mustPass()
		if got := h.notes.takeChanged(); len(got) != 0 {
			t.Fatalf("a new comment announced changed messages: %+v", got)
		}

		// A rename retitles the stored copies.
		h.clock.advance(10 * time.Minute)
		f.Update(is.ID, func(is *jiratest.Issue) { is.Summary = "Synchronizovaný požadavek (přejmenován)" })
		h.clock.advance(time.Minute)
		h.mustPass()
		expectOne("after a rename")

		// An edit rebuilds the comment's copies.
		h.clock.advance(10 * time.Minute)
		f.EditComment(is.ID, edited, "<p>Ještě jedna poznámka, opravená.</p>")
		h.clock.advance(time.Minute)
		h.mustPass()
		expectOne("after an edit")
	})
}

func TestResumeAfterInterruptedPass(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		f := h.f
		var issues []*jiratest.Issue
		h.at(h.ago(1*day), func() {
			for i := range 6 {
				is := f.AddIssue("WEB", fmt.Sprintf("Úloha %d", i), func(is *jiratest.Issue) { is.Reporter = f.Petr })
				f.AddComment(is.ID, f.Petr, fmt.Sprintf("<p>Komentář %d</p>", i))
				issues = append(issues, is)
			}
			f.AddAttachment(issues[3].ID, "zadani.txt", "text/plain", []byte("Zadání úlohy 3"))
		})
		content := "/secure/attachment/"
		if mode == jiratest.Cloud {
			content = "/attachment/content/"
		}
		f.FailNext(jiratest.Failure{Path: content, Status: 502})
		if err := h.pass(false); err == nil {
			t.Fatal("the pass did not fail")
		}
		if n := len(h.rows(spaceBox("10001"))); n == 0 {
			t.Fatal("nothing was stored before the failure")
		}
		// The user reads a message of the interrupted issue meanwhile.
		for _, m := range h.rows(spaceBox("10001")) {
			if m.ThreadID == "jira:"+issues[3].ID {
				if err := h.st.FlagMessages(context.Background(), h.acc.ID, []string{m.ID}, []api.Flag{api.FlagFlagged}, nil); err != nil {
					t.Fatal(err)
				}
			}
		}
		h.clock.advance(time.Minute)
		h.mustPass()
		web := h.rows(spaceBox("10001"))
		if len(web) != 12 {
			t.Fatalf("rows = %d (%v)", len(web), keysOf(web))
		}
		for rid, m := range web {
			if m.BodyState != store.BodyFetched {
				t.Fatalf("%s body %s", rid, m.BodyState)
			}
			if m.ThreadID == "jira:"+issues[3].ID && !hasFlag(m.Flags, api.FlagFlagged) {
				t.Fatalf("%s lost its flag in the resumed pass", rid)
			}
		}
		for _, is := range issues {
			row, ok := h.issue(is.ID)
			if !ok || !row.SyncedUpdated.Equal(row.Updated) {
				t.Fatalf("%s not settled: %+v", is.Key, row)
			}
		}
		desc := web["i:"+issues[3].ID]
		if len(desc.Attachments) != 1 || desc.Attachments[0].Filename != "zadani.txt" {
			t.Fatalf("attachments = %+v", desc.Attachments)
		}
		if news := h.notes.takeNews(); len(news) != 0 {
			t.Fatalf("the first backfill announced %d messages", len(news))
		}
	})
}

// runSyncer starts Run and returns a stop function.
func (h *harness) runSyncer() func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.syncer.Run(ctx) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(waitTimeout):
			h.t.Error("syncer did not stop")
		}
	}
}

func (h *harness) waitStatus(status api.SyncStatus) api.SyncState {
	h.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		st := h.syncer.State()
		if st.Status == status && (status != api.SyncIdle || st.LastSync != nil) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("state stayed %+v, want %s", h.syncer.State(), status)
	return api.SyncState{}
}

func TestAuthFailureState(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		h.mu.Lock()
		h.token = "revoked"
		h.mu.Unlock()
		stop := h.runSyncer()
		defer stop()
		st := h.waitStatus(api.SyncAuthRequired)
		if st.Error == nil || st.Error.Code != api.CodeAuthFailed {
			t.Fatalf("state = %+v", st)
		}
		// Retries keep failing; the user is told once.
		time.Sleep(200 * time.Millisecond)
		if n := h.notes.authCount(); n != 1 {
			t.Fatalf("authRequired sent %d times", n)
		}
		h.mu.Lock()
		h.token = jiratest.Token
		h.mu.Unlock()
		h.syncer.Wake()
		h.waitStatus(api.SyncIdle)
		if !h.hasFolder(spaceBox("10000")) {
			t.Fatal("no folders after recovery")
		}
	})
}

func TestRateLimitBackoff(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	newScene(h)
	h.f.FailNext(jiratest.Failure{Path: "/search/jql", Status: 429, Header: map[string]string{"Retry-After": "120"}})
	err := h.pass(false)
	if RetryAfter(err) != 120*time.Second || ToAPIError(err).Code != api.CodeServerTimeout {
		t.Fatalf("err = %v (retry after %v)", err, RetryAfter(err))
	}
	h.syncer.fail(err)
	if st := h.syncer.State(); st.Status != api.SyncOffline {
		t.Fatalf("state = %+v", st)
	}
	// The site's wait is honoured (the backoff alone would be 5 s)…
	real := NewSyncer(h.acc, Deps{})
	real.fail(err)
	attempt := 0
	if d := real.retryDelay(err, &attempt); d != 120*time.Second {
		t.Fatalf("delay = %v", d)
	}
	// …up to a cap.
	real.retryAfter = time.Hour
	if d := real.retryDelay(err, &attempt); d != passRetryAfterCap {
		t.Fatalf("capped delay = %v", d)
	}
	attempt = 0
	real.retryAfter = 0
	if d := real.retryDelay(err, &attempt); d < 4*time.Second || d > 6*time.Second {
		t.Fatalf("backoff = %v", d)
	}
	authErr := api.NewError(api.CodeAuthFailed, "no")
	if d := real.retryDelay(authErr, &attempt); d != authRetry {
		t.Fatalf("auth delay = %v", d)
	}

	// And the syncer recovers.
	stop := h.runSyncer()
	defer stop()
	h.waitStatus(api.SyncIdle)
}

func TestManualIntervalWaitsForTriggers(t *testing.T) {
	h := newHarness(t, jiratest.DC)
	newScene(h)
	h.mu.Lock()
	h.prefs.IntervalSeconds = 0
	h.mu.Unlock()
	stop := h.runSyncer()
	defer stop()
	first := h.waitStatus(api.SyncIdle)
	time.Sleep(50 * time.Millisecond)
	if st := h.syncer.State(); !st.LastSync.Equal(*first.LastSync) {
		t.Fatal("a manual account ran a pass on its own")
	}
	h.syncer.Trigger(false)
	deadline := time.Now().Add(waitTimeout)
	for {
		st := h.syncer.State()
		if st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(*first.LastSync) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the trigger ran no pass")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestViewsFollowConfiguration(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	sc := newScene(h)
	h.mustPass()
	h.reconfigure(func(c *api.JiraConfig) {
		c.DisabledFolders = []api.VirtualFolder{api.VirtualWatching}
		c.Spaces = c.Spaces[1:] // WEB only
	})
	h.clock.advance(time.Minute)
	h.mustPass()
	if h.hasFolder(viewBox(api.VirtualWatching)) || h.hasFolder(spaceBox("10000")) {
		t.Fatal("a disabled view or a deselected space kept its folder")
	}
	if _, ok := h.issue(sc.a.ID); ok {
		t.Fatal("an issue of a deselected space was kept")
	}
	for rid, m := range h.rows(viewBox(api.VirtualOpen)) {
		if m.ThreadID == "jira:"+sc.a.ID {
			t.Fatalf("the open view kept %s of a deselected space", rid)
		}
	}
	if b, _ := h.issue(sc.b.ID); fmt.Sprint(b.Views) != "[assignedToMe open]" {
		t.Fatalf("views = %v", b.Views)
	}
	// The view comes back, filled with copies of what is stored.
	h.reconfigure(func(c *api.JiraConfig) { c.DisabledFolders = nil })
	h.clock.advance(time.Minute)
	h.mustPass()
	w := h.rows(viewBox(api.VirtualWatching))
	if m, ok := w["i:"+sc.b.ID]; !ok || m.BodyState != store.BodyFetched || !seen(m) {
		t.Fatalf("watching view after re-enabling: %v", keysOf(w))
	}
	// Closed statuses decide the open view.
	h.reconfigure(func(c *api.JiraConfig) { c.ClosedStatuses = []api.StatusRef{{ID: "1", Name: "To Do"}} })
	h.clock.advance(time.Minute)
	h.mustPass()
	if _, ok := h.rows(viewBox(api.VirtualOpen))["i:"+sc.b.ID]; ok {
		t.Fatal("an issue in a closed status stayed open")
	}
}

func TestTruncatedCommentsAreNoDeletion(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	f := h.f
	var is *jiratest.Issue
	var first string
	h.at(h.ago(2*day), func() {
		is = f.AddIssue("WEB", "Dlouhá diskuse")
		for i := range api.MaxThreadMessages {
			id := f.AddComment(is.ID, f.Petr, fmt.Sprintf("<p>Komentář %d</p>", i))
			if i == 0 {
				first = id
			}
		}
	})
	h.mustPass()
	if _, ok := h.rows(spaceBox("10001"))["c:"+first]; !ok {
		t.Fatal("the oldest comment is missing")
	}
	// One more: the site now returns the newest 500, without the first.
	h.clock.advance(10 * time.Minute)
	last := f.AddComment(is.ID, f.Petr, "<p>Poslední.</p>")
	h.clock.advance(time.Minute)
	h.mustPass()
	web := h.rows(spaceBox("10001"))
	if _, ok := web["c:"+first]; !ok {
		t.Fatal("a comment the truncated list left out was deleted")
	}
	if _, ok := web["c:"+last]; !ok || len(web) != api.MaxThreadMessages+2 {
		t.Fatalf("rows = %d", len(web))
	}
	// A comment deleted among the newest is still noticed.
	h.clock.advance(time.Minute)
	f.DeleteComment(is.ID, last)
	h.clock.advance(time.Minute)
	h.mustPass()
	if _, ok := h.rows(spaceBox("10001"))["c:"+last]; ok {
		t.Fatal("a deleted recent comment was kept")
	}
}

func TestMovesAndDeletionsAreDropped(t *testing.T) {
	h := newHarness(t, jiratest.DC)
	sc := newScene(h)
	h.mustPass()
	ctx := context.Background()
	m := h.rows(spaceBox("10000"))["c:"+sc.cOld]
	if err := h.st.DeleteMessages(ctx, h.acc.ID, []string{m.ID}); err != nil {
		t.Fatal(err)
	}
	if n, _ := h.st.CountPendingOps(ctx, h.acc.ID); n != 1 {
		t.Fatalf("pending = %d", n)
	}
	h.clock.advance(time.Minute)
	h.mustPass()
	if n, _ := h.st.CountPendingOps(ctx, h.acc.ID); n != 0 {
		t.Fatalf("pending after the pass = %d", n)
	}
	for _, r := range h.f.RequestsTo("", "") {
		if r.Method != "GET" && r.Method != "POST" {
			t.Fatalf("the site got a %s", r.Method)
		}
	}
}

func TestVanished(t *testing.T) {
	cl := CommentList{Comments: []Comment{{ID: "120"}, {ID: "130"}}, Truncated: true}
	hist := []History{{ID: "50"}, {ID: "60"}}
	for _, tc := range []struct {
		rid        string
		cl         CommentList
		hideEvents bool
		want       bool
	}{
		{"c:119", cl, false, false}, // older than what the cap returned
		{"c:1000", cl, false, true}, // newer than the oldest returned, missing: deleted
		{"c:125", cl, false, true},  // within the returned range, missing: deleted
		{"c:119", CommentList{Comments: cl.Comments}, false, true},
		{"c:5", CommentList{Truncated: true}, false, true},
		{"h:49", cl, false, false},
		{"h:55", cl, false, true},
		{"h:49", cl, true, true},
		{"x:1", cl, false, true},
	} {
		if got := vanished(tc.rid, tc.cl, hist, tc.hideEvents); got != tc.want {
			t.Errorf("vanished(%s, truncated %v, hide %v) = %v", tc.rid, tc.cl.Truncated, tc.hideEvents, got)
		}
	}
}
