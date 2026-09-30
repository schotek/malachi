// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// supervisorFor is a supervisor over the harness's store and site, not
// running (FetchMessage needs no syncer).
func (h *harness) supervisorFor() *Supervisor {
	return NewSupervisor(SupervisorDeps{
		Store: h.st,
		Token: func(context.Context, string) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.token, h.tokenErr
		},
		HTTP:  h.f.HTTPClient(),
		Now:   h.clock.now,
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
}

func fetchAll(t *testing.T, sv *Supervisor, acc store.Account, m store.Message) ([]byte, error) {
	t.Helper()
	rc, size, err := sv.FetchMessage(context.Background(), acc, m)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(data)) {
		t.Fatalf("size %d, read %d", size, len(data))
	}
	return data, nil
}

func TestFetchMessageRebuildsStoredBytes(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode, func(c *api.JiraConfig) {
			c.BotNames = []string{"Issue Sync"}
		})
		f := h.f
		cp := ctxPath(f)
		f.AddSiteFile("/images/logo.png", "image/png", jiratest.PNG)
		f.AddUser(&jiratest.User{ID: "JIRAUSER10300", Name: "sync", Display: "Issue Sync"})
		var is *jiratest.Issue
		var relayed, plain string
		h.at(h.ago(2*day), func() {
			is = f.AddIssue("ITSD", "Obnova zprávy", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			logID := f.AddAttachment(is.ID, "log.txt", "text/plain", []byte("E42"))
			f.AddAttachment(is.ID, "zadani.pdf", "application/pdf", []byte("%PDF-1.7\nfake"))
			f.Update(is.ID, func(i *jiratest.Issue) {
				i.Description = `<p>Viz <img src="` + cp + `/images/logo.png"> a <a href="` + cp + `/browse/WEB-1">WEB-1</a>.</p>`
			})
			plain = f.AddComment(is.ID, f.Petr, `<p>Log: <a href="`+cp+`/secure/attachment/`+logID+`/log.txt">log.txt</a></p>`)
			relayed = f.AddComment(is.ID, "JIRAUSER10300", "<p>ITSD-7 Eva Horáková added comment - 17/09/26 09:30 GMT</p><p>Přeposláno.</p>")
			f.SetStatus(is.ID, "3", f.Petr)
		})
		h.mustPass()
		sv := h.supervisorFor()
		rows := h.rows(spaceBox("10000"))
		var checked int
		for rid, m := range rows {
			if m.ThreadID != "jira:"+is.ID {
				continue
			}
			got, err := fetchAll(t, sv, h.acc, m)
			if err != nil {
				t.Fatalf("%s: %v", rid, err)
			}
			if stored := h.raw(m.ID); !bytes.Equal(got, stored) {
				t.Fatalf("%s: the rebuild differs from the stored message:\n%s\n---\n%s", rid, got, stored)
			}
			checked++
		}
		if checked != 4 {
			t.Fatalf("checked %d messages", checked)
		}

		// The site changed: other bytes, the same Message-ID.
		h.clock.advance(time.Minute)
		f.EditComment(is.ID, plain, "<p>Opravený log.</p>")
		m := rows["c:"+plain]
		got, err := fetchAll(t, sv, h.acc, m)
		if err != nil {
			t.Fatal(err)
		}
		p, err := imime.Parse(bytes.NewReader(got), imime.DefaultLimits())
		if err != nil || p.MessageID != m.RFCMessageID || !strings.Contains(p.Text, "Opravený log.") {
			t.Fatalf("rebuilt edited comment: %v %+v", err, p)
		}

		// Gone: the comment, then the issue.
		f.DeleteComment(is.ID, relayed)
		if _, err := fetchAll(t, sv, h.acc, rows["c:"+relayed]); !errors.Is(err, ErrGone) {
			t.Fatalf("deleted comment: %v", err)
		}
		f.DeleteIssue(is.ID)
		if _, err := fetchAll(t, sv, h.acc, rows["i:"+is.ID]); !errors.Is(err, ErrGone) {
			t.Fatalf("deleted issue: %v", err)
		}
		bogus := m
		bogus.RemoteID = "x:1"
		if _, err := fetchAll(t, sv, h.acc, bogus); !errors.Is(err, ErrGone) {
			t.Fatalf("unknown item: %v", err)
		}
		// A site failure is an api error, not "gone".
		f.FailNext(jiratest.Failure{Status: 500, Times: 3})
		_, err = fetchAll(t, sv, h.acc, rows["h:"+strings.TrimPrefix(firstEvent(rows, is.ID), "h:")])
		if errors.Is(err, ErrGone) || ToAPIError(err).Code != api.CodeServerError {
			t.Fatalf("failure: %v", err)
		}
	})
}

func firstEvent(rows map[string]store.Message, issueID string) string {
	for rid, m := range rows {
		if strings.HasPrefix(rid, "h:") && m.ThreadID == "jira:"+issueID {
			return rid
		}
	}
	return ""
}
