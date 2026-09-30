// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// queuedComment is the MIME message message.send queues for a comment
// draft: no recipients, the sanitised HTML and its text.
func queuedComment(t *testing.T, html, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := smtp.BuildMessage(&buf, smtp.BuildInput{
		From: api.Address{Name: "Jana Dvořáková", Address: jiratest.Login}, Subject: "ITSD-1: Tiskárna nefunguje",
		Text: text, HTML: html, Date: syncT0, MessageID: "m1@acme.test",
	}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// postedComment is the body of a comment POST the site received.
type postedComment struct {
	Body       json.RawMessage `json:"body"`
	Properties []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	} `json:"properties"`
}

func (p postedComment) props() map[string]string {
	out := map[string]string{}
	for _, pr := range p.Properties {
		out[pr.Key] = string(pr.Value)
	}
	return out
}

func posts(t *testing.T, f *jiratest.Server) []postedComment {
	t.Helper()
	var out []postedComment
	for _, r := range f.RequestsTo(http.MethodPost, "/comment") {
		var p postedComment
		if err := json.Unmarshal(r.Body, &p); err != nil {
			t.Fatalf("posted body: %v: %s", err, r.Body)
		}
		if !strings.Contains(r.Query, "expand=renderedBody%2Cproperties") {
			t.Errorf("post query %q", r.Query)
		}
		out = append(out, p)
	}
	return out
}

func deliverSetup(t *testing.T, mode jiratest.Mode) (*harness, *scene, *Supervisor) {
	t.Helper()
	h := newHarness(t, mode)
	sc := newScene(h)
	var mu sync.Mutex
	calls := 0
	sv := h.runningSupervisor(&calls, &mu)
	h.notes.takeNews()
	return h, sc, sv
}

func queued(h *harness, id, issueID string, vis api.CommentVisibility, attempts int) store.OutboxEntry {
	return store.OutboxEntry{MessageID: id, AccountID: h.acc.ID, Attempts: attempts,
		Comment: &store.OutboxComment{IssueID: issueID, Visibility: vis}}
}

// sendErr unpacks a delivery failure.
func sendErr(t *testing.T, err error) *smtp.SendError {
	t.Helper()
	var se *smtp.SendError
	if !errors.As(err, &se) {
		t.Fatalf("not a SendError: %v", err)
	}
	return se
}

// A public comment on an ordinary issue: the site gets the comment in its
// format with the outbox property only, and after Deliver returns the
// syncer has stored it — the user's own, read, not announced.
func TestDeliverComment(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h, sc, sv := deliverSetup(t, mode)
		ctx := context.Background()
		raw := queuedComment(t, `<p>Hotovo, <b>vyměněno</b>.</p><ul><li>toner</li></ul><p>{html}[~admin]</p>`, "Hotovo, vyměněno.\n\n* toner\n\n{html}[~admin]")
		if err := sv.Deliver(ctx, queued(h, "m_web1", sc.b.ID, "", 0), bytes.NewReader(raw), int64(len(raw))); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		ps := posts(t, h.f)
		if len(ps) != 1 {
			t.Fatalf("%d posts", len(ps))
		}
		if got := ps[0].props(); len(got) != 1 || got[OutboxPropertyKey] != `{"id":"m_web1"}` {
			t.Errorf("properties = %v", got)
		}
		if mode == jiratest.Cloud {
			if err := jiratest.ValidateADF(ps[0].Body); err != nil {
				t.Fatalf("ADF: %v", err)
			}
			if s := string(ps[0].Body); !strings.Contains(s, `"text":"vyměněno","marks":[{"type":"strong"}]`) ||
				!strings.Contains(s, `"type":"bulletList"`) || !strings.Contains(s, `"text":"{html}[~admin]"`) {
				t.Errorf("ADF body = %s", s)
			}
		} else {
			var wiki string
			if err := json.Unmarshal(ps[0].Body, &wiki); err != nil {
				t.Fatalf("wiki body: %v", err)
			}
			if wiki != "Hotovo, *vyměněno*.\n\n* toner\n\n\\{html\\}\\[\\~admin\\]" {
				t.Errorf("wiki body = %q", wiki)
			}
		}
		cs := h.f.CommentsOf(sc.b.ID)
		if len(cs) != 1 || cs[0].Author != h.f.Me {
			t.Fatalf("site comments = %+v", cs)
		}
		// Stored by the refresh Deliver waited for.
		rows := h.rows(spaceBox("10001"))
		m, ok := rows["c:"+cs[0].ID]
		if !ok {
			t.Fatalf("the comment is not stored: %v", keysOf(rows))
		}
		if !seen(m) || m.ThreadID != store.IssueThreadID(sc.b.ID) || m.Subject != sc.b.Key+": Nový web" {
			t.Errorf("stored comment = %+v", m)
		}
		if n := h.notes.takeNews(); len(n) != 0 {
			t.Errorf("the user's own comment was announced: %+v", n)
		}
	})
}

// An internal comment goes only to a service-desk issue, with Jira Service
// Management's property; it arrives internal.
func TestDeliverInternalComment(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h, sc, sv := deliverSetup(t, mode)
		ctx := context.Background()
		raw := queuedComment(t, "", "Jen pro tým.")
		if err := sv.Deliver(ctx, queued(h, "m_int1", sc.a.ID, api.CommentInternal, 0), bytes.NewReader(raw), int64(len(raw))); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		ps := posts(t, h.f)
		if len(ps) != 1 || ps[0].props()[sdPublicCommentKey] != `{"internal":true}` || ps[0].props()[OutboxPropertyKey] != `{"id":"m_int1"}` {
			t.Fatalf("posts = %+v", ps)
		}
		cs := h.f.CommentsOf(sc.a.ID)
		last := cs[len(cs)-1]
		if last.JsdPublic == nil || *last.JsdPublic {
			t.Fatalf("the site took it as public: %+v", last)
		}
		items, err := h.st.IssueItems(ctx, h.acc.ID, sc.a.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, it := range items {
			if it.RemoteID == "c:"+last.ID {
				found = true
				if it.Visibility != api.CommentInternal {
					t.Errorf("stored visibility %q", it.Visibility)
				}
			}
		}
		if !found {
			t.Fatalf("the comment is not stored")
		}

		// A public comment on the same issue sets no visibility property.
		if err := sv.Deliver(ctx, queued(h, "m_pub1", sc.a.ID, api.CommentPublic, 0), bytes.NewReader(raw), int64(len(raw))); err != nil {
			t.Fatal(err)
		}
		if ps := posts(t, h.f); len(ps) != 2 || len(ps[1].props()) != 1 {
			t.Fatalf("public post = %+v", ps[len(ps)-1])
		}

		// Internal on an issue that is no service-desk issue: refused for
		// good, nothing posted.
		err = sv.Deliver(ctx, queued(h, "m_int2", sc.b.ID, api.CommentInternal, 0), bytes.NewReader(raw), int64(len(raw)))
		if se := sendErr(t, err); !se.Permanent || se.Err.Code != api.CodeInvalidArgument {
			t.Fatalf("internal on a plain issue: %v", err)
		}
		err = sv.Deliver(ctx, queued(h, "m_int3", sc.b.ID, "secret", 0), bytes.NewReader(raw), int64(len(raw)))
		if se := sendErr(t, err); !se.Permanent {
			t.Fatalf("unknown visibility: %v", err)
		}
		if n := len(posts(t, h.f)); n != 2 {
			t.Fatalf("%d posts", n)
		}
	})
}

// A retry after an attempt whose answer was lost although the site took
// the comment finds it by its property and posts nothing again; another
// user's comment with the same property does not count.
func TestDeliverIdempotentRetry(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h, sc, sv := deliverSetup(t, mode)
		ctx := context.Background()
		raw := queuedComment(t, "<p>Jednou a dost.</p>", "Jednou a dost.")
		h.f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/comment", Status: http.StatusGatewayTimeout, After: true})
		err := sv.Deliver(ctx, queued(h, "m_once", sc.g.ID, "", 0), bytes.NewReader(raw), int64(len(raw)))
		if se := sendErr(t, err); se.Permanent || se.Err.Code != api.CodeServerTimeout {
			t.Fatalf("lost answer: %v", err)
		}
		if n := len(h.f.CommentsOf(sc.g.ID)); n != 1 {
			t.Fatalf("the site has %d comments", n)
		}
		if err := sv.Deliver(ctx, queued(h, "m_once", sc.g.ID, "", 1), bytes.NewReader(raw), int64(len(raw))); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if n, p := len(h.f.CommentsOf(sc.g.ID)), len(posts(t, h.f)); n != 1 || p != 1 {
			t.Fatalf("after the retry: %d comments, %d posts", n, p)
		}
		if _, ok := h.rows(spaceBox("10001"))["c:"+h.f.CommentsOf(sc.g.ID)[0].ID]; !ok {
			t.Error("the retry did not refresh the issue")
		}

		// Someone else's comment carrying the property is not ours.
		h.f.AddComment(sc.b.ID, h.f.Petr, "<p>podvrh</p>", func(c *jiratest.Comment) {
			c.Props = map[string]any{OutboxPropertyKey: map[string]any{"id": "m_spoof"}}
		})
		if err := sv.Deliver(ctx, queued(h, "m_spoof", sc.b.ID, "", 2), bytes.NewReader(raw), int64(len(raw))); err != nil {
			t.Fatal(err)
		}
		if p := len(posts(t, h.f)); p != 2 {
			t.Fatalf("a spoofed property stopped the post: %d posts", p)
		}
	})
}

// The site's answers map onto the outbox's retry rules.
func TestDeliverErrors(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h, sc, sv := deliverSetup(t, mode)
		ctx := context.Background()
		raw := queuedComment(t, "<p>x</p>", "x")
		// A refused token is tried on the other route too where there is
		// one (cloud with a cloud id).
		unauthorised, requests := 1, 9
		if mode == jiratest.Cloud {
			unauthorised, requests = 2, 10
		}
		for _, c := range []struct {
			status    int
			times     int
			header    map[string]string
			permanent bool
			code      api.ErrorCode
		}{
			{http.StatusBadRequest, 1, nil, true, api.CodeServerError},
			{http.StatusForbidden, 1, nil, true, api.CodeServerError},
			{http.StatusNotFound, 1, nil, true, api.CodeServerError},
			{http.StatusRequestEntityTooLarge, 1, nil, true, api.CodeServerError},
			{http.StatusConflict, 1, nil, true, api.CodeServerError},
			{http.StatusUnauthorized, unauthorised, nil, false, api.CodeAuthFailed},
			// Longer than the client waits by itself: no retry within.
			{http.StatusTooManyRequests, 1, map[string]string{"Retry-After": "600"}, false, api.CodeServerTimeout},
			{http.StatusInternalServerError, 1, nil, false, api.CodeServerError},
			{http.StatusBadGateway, 1, nil, false, api.CodeServerError},
		} {
			h.f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/comment", Status: c.status, Times: c.times, Header: c.header})
			err := sv.Deliver(ctx, queued(h, "m_err", sc.b.ID, "", 0), bytes.NewReader(raw), int64(len(raw)))
			se := sendErr(t, err)
			if se.Permanent != c.permanent || se.Err.Code != c.code {
				t.Errorf("HTTP %d: permanent %v code %v (%v)", c.status, se.Permanent, se.Err.Code, err)
			}
			if strings.Contains(err.Error(), jiratest.Token) {
				t.Errorf("HTTP %d: the error carries the token", c.status)
			}
		}
		if n := len(h.f.CommentsOf(sc.b.ID)); n != 0 {
			t.Fatalf("%d comments after failures", n)
		}

		// What no retry changes, before anything is posted.
		for name, e := range map[string]store.OutboxEntry{
			"no comment":      {MessageID: "m_mail", AccountID: h.acc.ID},
			"no issue":        queued(h, "m_x", "", "", 0),
			"unknown account": {MessageID: "m_x", AccountID: "acc_gone", Comment: &store.OutboxComment{IssueID: sc.b.ID}},
		} {
			err := sv.Deliver(ctx, e, bytes.NewReader(raw), int64(len(raw)))
			if se := sendErr(t, err); !se.Permanent {
				t.Errorf("%s: %v", name, err)
			}
		}
		empty := queuedComment(t, "<p> </p><hr>", " ")
		err := sv.Deliver(ctx, queued(h, "m_empty", sc.b.ID, "", 0), bytes.NewReader(empty), int64(len(empty)))
		if se := sendErr(t, err); !se.Permanent || se.Err.Code != api.CodeInvalidArgument {
			t.Errorf("empty comment: %v", err)
		}
		err = sv.Deliver(ctx, queued(h, "m_junk", sc.b.ID, "", 0), strings.NewReader(""), 0)
		if se := sendErr(t, err); !se.Permanent {
			t.Errorf("no message: %v", err)
		}
		if n := len(posts(t, h.f)); n != requests {
			t.Errorf("%d posts, want %d: the refused ones may not have gone out", n, requests)
		}
	})
}
