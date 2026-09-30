// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// transitionIDs renders transitions as "id:name>to[!]" (! = needs input).
func transitionIDs(ts []Transition) string {
	var parts []string
	for _, t := range ts {
		s := t.ID + ":" + t.Name + ">" + t.To.Name
		if t.NeedsInput {
			s += "!"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// Both flavours list the transitions the site offers out of the issue's
// status (the screen's fields deciding NeedsInput) and perform one by its
// id with nothing else in the request; the site's refusals come back as
// its 400.
func TestRemoteTransitions(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			is := f.AddIssue("ITSD", "Tiskárna nefunguje")
			r := remoteOf(f)

			ts, err := r.Transitions(ctx, is.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := []Transition{
				{ID: "11", Name: "Start Progress", To: Status{ID: "3", Name: "In Progress", Category: api.StatusCategoryInProgress}},
				{ID: "21", Name: "Resolve", To: Status{ID: "10001", Name: "Done", Category: api.StatusCategoryDone}, NeedsInput: true},
				{ID: "31", Name: "Wait for customer", To: Status{ID: "10002", Name: "Waiting for customer", Category: api.StatusCategoryInProgress}, NeedsInput: true},
			}
			if fmt.Sprintf("%+v", ts) != fmt.Sprintf("%+v", want) {
				t.Fatalf("transitions =\n%+v\nwant\n%+v", ts, want)
			}
			gets := f.RequestsTo(http.MethodGet, "/transitions")
			if len(gets) != 1 || !strings.Contains(gets[0].Query, "expand=transitions.fields") {
				t.Fatalf("list requests = %+v", gets)
			}

			if err := r.Transition(ctx, is.ID, "11"); err != nil {
				t.Fatalf("transition: %v", err)
			}
			posts := f.RequestsTo(http.MethodPost, "/transitions")
			if len(posts) != 1 || string(posts[0].Body) != `{"transition":{"id":"11"}}` {
				t.Fatalf("posted = %+v", posts)
			}
			if got := f.TransitionsPerformed(is.ID); fmt.Sprint(got) != "[11]" {
				t.Fatalf("performed = %v", got)
			}
			after, err := r.BulkIssues(ctx, []string{is.ID}, IssueOptions{})
			if err != nil || len(after) != 1 || after[0].Status.ID != "3" {
				t.Fatalf("after the transition: %+v, %v", after, err)
			}
			// Out of In Progress the site offers Reopen, and Start Progress
			// no longer.
			ts, err = r.Transitions(ctx, is.ID)
			if err != nil || transitionIDs(ts) != "21:Resolve>Done! 31:Wait for customer>Waiting for customer! 41:Reopen>To Do" {
				t.Fatalf("transitions now = %q, %v", transitionIDs(ts), err)
			}

			// The site's refusals: a screen with a required field, an id it
			// does not offer.
			err = r.Transition(ctx, is.ID, "21")
			if statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "resolution: Resolution is required.") {
				t.Fatalf("required field: %v", err)
			}
			err = r.Transition(ctx, is.ID, "11")
			if statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Transition id '11' is not valid") {
				t.Fatalf("transition not offered: %v", err)
			}
			if got := f.TransitionsPerformed(is.ID); fmt.Sprint(got) != "[11]" {
				t.Fatalf("a refused transition was performed: %v", got)
			}

			// Ids that cannot be the site's never reach it.
			n := len(f.RequestsTo("", "/transitions"))
			for _, bad := range []string{"", " 11", "11/../12", "x y"} {
				if _, err := r.Transitions(ctx, bad); codeOf(err) != api.CodeInvalidArgument {
					t.Errorf("Transitions(%q): %v", bad, err)
				}
				if err := r.Transition(ctx, is.ID, bad); codeOf(err) != api.CodeInvalidArgument {
					t.Errorf("Transition(%q): %v", bad, err)
				}
				if err := r.Transition(ctx, bad, "11"); codeOf(err) != api.CodeInvalidArgument {
					t.Errorf("Transition of issue %q: %v", bad, err)
				}
			}
			if m := len(f.RequestsTo("", "/transitions")); m != n {
				t.Fatalf("%d requests for bad ids", m-n)
			}

			// A workflow of the test's own, and an issue that is gone.
			f.SetTransitions(is.ID, &jiratest.Transition{ID: "7", Name: "Close", To: "10001"})
			if ts, err := r.Transitions(ctx, is.ID); err != nil || transitionIDs(ts) != "7:Close>Done" {
				t.Fatalf("own workflow = %q, %v", transitionIDs(ts), err)
			}
			f.DeleteIssue(is.ID)
			if _, err := r.Transitions(ctx, is.ID); !IsNotFound(err) {
				t.Fatalf("gone: %v", err)
			}
			if err := r.Transition(ctx, is.ID, "7"); !IsNotFound(err) {
				t.Fatalf("gone: %v", err)
			}
		})
	}
}

// A hostile transitions answer: numbers for ids and strings for booleans
// are read, entries without an id, with an id of the wrong shape, with a
// duplicate id, with nothing to show or marked unavailable are dropped,
// fields of the wrong type count as no fields, names are cleaned and
// capped.
func TestRemoteTransitionsPathological(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			var r Remote
			if mode == jiratest.Cloud {
				hc := fixtureSite(t, map[string]string{"GET /rest/api/3/issue/10100/transitions": "transitions-pathological.json"}, "acme.atlassian.net")
				c, err := NewClient(Options{SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud, Login: jiratest.Login, HTTP: hc,
					Token: func(context.Context) (string, error) { return jiratest.Token, nil }})
				if err != nil {
					t.Fatal(err)
				}
				r = NewRemote(c)
			} else {
				hc := fixtureSite(t, map[string]string{"GET /jira/rest/api/2/issue/10100/transitions": "transitions-pathological.json"}, "jira.acme.test")
				r = NewRemote(testClient(t, jiratest.DCSite, hc))
			}
			ts, err := r.Transitions(ctx, "10100")
			if err != nil {
				t.Fatal(err)
			}
			long := strings.Repeat("x", maxNameBytes)
			want := "11:Start Progress>In Progress 21:Resolve>Done! 31:Odd fields>Waiting 32:Field of the wrong type>Waiting! " +
				"41:Reopen  zpět>To Do 51:>Only a target 52:Nowhere> 55:" + long + ">In Progress 56:Available as a string>In Progress"
			if got := transitionIDs(ts); got != want {
				t.Fatalf("transitions =\n%s\nwant\n%s", got, want)
			}
			byID := map[string]Transition{}
			for _, tr := range ts {
				byID[tr.ID] = tr
				for _, s := range []string{tr.Name, tr.To.Name} {
					if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || isInvisible(r) }) {
						t.Errorf("unclean text %q", s)
					}
				}
			}
			if byID["11"].To.Category != api.StatusCategoryInProgress || byID["21"].To.Category != api.StatusCategoryDone ||
				byID["41"].To.Category != api.StatusCategoryTodo || byID["51"].To.Category != "" || byID["32"].To.Category != "" {
				t.Errorf("categories: %+v", ts)
			}
			if byID["52"].To != (Status{}) {
				t.Errorf("a null target = %+v", byID["52"].To)
			}
		})
	}
}

// Answers that are no transitions list, and a list past the cap.
func TestRemoteTransitionsMalformed(t *testing.T) {
	ctx := context.Background()
	for name, body := range map[string]string{
		"string":   `{"transitions":"none"}`,
		"missing":  `{"expand":"transitions"}`,
		"array":    `[]`,
		"nonsense": `nonsense`,
	} {
		hc := testSite(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }, "jira.acme.test")
		if _, err := NewRemote(testClient(t, "https://jira.acme.test", hc)).Transitions(ctx, "10100"); codeOf(err) != api.CodeServerError {
			t.Errorf("%s: %v", name, err)
		}
	}
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><body>Log in</body></html>")
	}, "jira.acme.test")
	if _, err := NewRemote(testClient(t, "https://jira.acme.test", hc)).Transitions(ctx, "10100"); codeOf(err) != api.CodeServerError {
		t.Errorf("html: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(`{"transitions":[`)
	for i := range maxTransitions + 50 {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":"%d","name":"T%d","to":{"id":"3","name":"In Progress"}}`, i+1, i+1)
	}
	sb.WriteString(`]}`)
	hc = testSite(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, sb.String()) }, "jira.acme.test")
	ts, err := NewRemote(testClient(t, "https://jira.acme.test", hc)).Transitions(ctx, "10100")
	if err != nil || len(ts) != maxTransitions || ts[0].ID != "1" || ts[maxTransitions-1].ID != fmt.Sprint(maxTransitions) {
		t.Fatalf("oversized list: %d kept, %v", len(ts), err)
	}
}

// The supervisor performs a transition with the account's client and
// waits for the issue's refresh: the new status and its event row (the
// user's own, read, not announced) are stored when it returns. What the
// issue does not offer, or what needs input, is refused before the site is
// asked; the site's refusals and a revoked token are mapped.
func TestSupervisorTransition(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		sc := newScene(h)
		var mu sync.Mutex
		calls := 0
		sv := h.runningSupervisor(&calls, &mu)
		h.notes.takeNews()
		ctx := context.Background()
		f := h.f

		// ITSD-1 is In Progress.
		ts, err := sv.Transitions(ctx, h.acc.ID, sc.a.ID)
		if err != nil || transitionIDs(ts) != "21:Resolve>Done! 31:Wait for customer>Waiting for customer! 41:Reopen>To Do" {
			t.Fatalf("transitions = %q, %v", transitionIDs(ts), err)
		}
		for _, id := range []string{"21", "31", "99", "", "4 1"} {
			if err := sv.Transition(ctx, h.acc.ID, sc.a.ID, id); ToAPIError(err).Code != api.CodeInvalidArgument {
				t.Errorf("transition %q: %v", id, err)
			}
		}
		if n := len(f.RequestsTo(http.MethodPost, "/transitions")); n != 0 {
			t.Fatalf("%d transitions posted for refused ids", n)
		}
		eventsOf := func() map[string]bool {
			out := map[string]bool{}
			for rid, m := range h.rows(spaceBox("10000")) {
				if strings.HasPrefix(rid, "h:") && m.ThreadID == "jira:"+sc.a.ID {
					out[rid] = seen(m)
				}
			}
			return out
		}
		before := eventsOf()
		if len(before) != 1 {
			t.Fatalf("events before = %v", before)
		}

		h.clock.advance(time.Minute)
		if err := sv.Transition(ctx, h.acc.ID, sc.a.ID, "41"); err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if got := f.TransitionsPerformed(sc.a.ID); fmt.Sprint(got) != "[41]" {
			t.Fatalf("performed = %v", got)
		}
		// Stored by the refresh Transition waited for.
		is, ok := h.issue(sc.a.ID)
		if !ok || is.Status != "To Do" || is.StatusCategory != api.StatusCategoryTodo {
			t.Fatalf("stored issue = %+v", is)
		}
		after := eventsOf()
		if len(after) != 2 {
			t.Fatalf("events after = %v", after)
		}
		var fresh string
		for rid, read := range after {
			if before[rid] {
				continue
			}
			fresh = rid
			if !read {
				t.Errorf("the event of the user's own transition is unread")
			}
		}
		decos, err := h.st.IssueDecorations(ctx, h.acc.ID, []string{fresh})
		if err != nil {
			t.Fatal(err)
		}
		if d := decos[fresh]; d.Item.Kind != api.IssueItemEvent || fmt.Sprint(d.Item.Changes) != "[{status In Progress To Do}]" || d.Item.AuthorID != f.Me {
			t.Fatalf("event = %+v", decos[fresh].Item)
		}
		if n := h.notes.takeNews(); len(n) != 0 {
			t.Errorf("announced: %+v", n)
		}
		// Reopened: the issue is in the open view again with Start Progress
		// on offer.
		if _, open := h.rows(viewBox(api.VirtualOpen))["i:"+sc.a.ID]; !open {
			t.Error("the reopened issue is not in the open view")
		}
		if ts, err := sv.Transitions(ctx, h.acc.ID, sc.a.ID); err != nil || !strings.HasPrefix(transitionIDs(ts), "11:Start Progress>In Progress ") {
			t.Fatalf("transitions now = %q, %v", transitionIDs(ts), err)
		}

		// The site refuses the transition: its message comes through.
		f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/transitions", Status: http.StatusBadRequest,
			Body: `{"errorMessages":["Transition is not allowed: the issue is locked"],"errors":{}}`})
		err = sv.Transition(ctx, h.acc.ID, sc.a.ID, "11")
		if ae := ToAPIError(err); ae == nil || ae.Code != api.CodeServerError || !strings.Contains(ae.Message, "the issue is locked") {
			t.Fatalf("refused transition: %v", err)
		}
		if is, _ := h.issue(sc.a.ID); is.Status != "To Do" {
			t.Fatalf("a refused transition changed the stored issue: %+v", is)
		}
		// A revoked token: authFailed, and the keyring is asked again.
		times := 1
		if mode == jiratest.Cloud {
			times = 2 // the site and the gateway route
		}
		mu.Lock()
		asked := calls
		mu.Unlock()
		f.FailNext(jiratest.Failure{Method: http.MethodGet, Path: "/transitions", Status: http.StatusUnauthorized, Times: times})
		if _, err := sv.Transitions(ctx, h.acc.ID, sc.a.ID); ToAPIError(err).Code != api.CodeAuthFailed {
			t.Fatalf("revoked token: %v", err)
		}
		if _, err := sv.Transitions(ctx, h.acc.ID, sc.a.ID); err != nil {
			t.Fatalf("after the token was asked again: %v", err)
		}
		mu.Lock()
		again := calls
		mu.Unlock()
		if again != asked+1 {
			t.Fatalf("the keyring was asked %d times after a 401", again-asked)
		}
		// Gone or hidden.
		f.DeleteIssue(sc.g.ID)
		if _, err := sv.Transitions(ctx, h.acc.ID, sc.g.ID); ToAPIError(err).Code != api.CodeMessageGone {
			t.Fatalf("gone: %v", err)
		}
		if err := sv.Transition(ctx, h.acc.ID, sc.g.ID, "11"); ToAPIError(err).Code != api.CodeMessageGone {
			t.Fatalf("gone: %v", err)
		}
		// Bad references.
		if _, err := sv.Transitions(ctx, "acc_nobody", sc.a.ID); ToAPIError(err).Code != api.CodeAccountNotFound {
			t.Fatalf("unknown account: %v", err)
		}
		if _, err := sv.Transitions(ctx, h.acc.ID, "not an id"); ToAPIError(err).Code != api.CodeInvalidArgument {
			t.Fatalf("bad issue id: %v", err)
		}
	})
}

// A transition on an issue the store does not hold (the site offers it,
// the syncer never saw it) is performed without a refresh.
func TestSupervisorTransitionUnknownIssue(t *testing.T) {
	h := newHarness(t, jiratest.DC)
	newScene(h)
	var mu sync.Mutex
	calls := 0
	sv := h.runningSupervisor(&calls, &mu)
	mob := h.f.AddIssue("MOB", "Jinde") // an unselected space: never stored
	if err := sv.Transition(context.Background(), h.acc.ID, mob.ID, "11"); err != nil {
		t.Fatal(err)
	}
	if got := h.f.TransitionsPerformed(mob.ID); fmt.Sprint(got) != "[11]" {
		t.Fatalf("performed = %v", got)
	}
	if _, ok := h.issue(mob.ID); ok {
		t.Fatal("an issue of an unselected space was stored")
	}
}
