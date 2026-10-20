// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// TestInvitationPillRow checks, with a GTK display (MALACHI_GTK_SMOKE=1),
// that a message row's invitation pill and the strike-through of its
// subject follow calendar.PillKind and StrikesSubject, on a message row
// and a conversation row, and that a row of an issue shows none.
func TestInvitationPillRow(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	kinds := []api.InvitationKind{api.InvitationKindInvitation, api.InvitationKindUpdate, api.InvitationKindCancelled,
		api.InvitationKindReply, api.InvitationKindOther}
	r := NewMessageRow()
	for _, k := range kinds {
		for _, st := range []api.InvitationState{api.InvitationUnanswered, api.InvitationInvalid} {
			s := api.MessageSummary{Subject: "Vendor demo", Invitation: &api.InvitationSummary{Kind: k, State: st}}
			p := calendar.InvitationPillOf(s)
			want, wantStruck := calendar.PillTitle(p, i18n.Tr), calendar.StrikesSubject(p)

			r.SetMessage(Message{Subject: s.Subject, Invitation: p})
			if got, struck := r.InvitationPill(); got != want || struck != wantStruck {
				t.Errorf("message %s/%s: pill %q struck %v, want %q %v", k, st, got, struck, want, wantStruck)
			}
			r.SetThread(Thread{Subject: s.Subject, Count: 2, Invitation: p})
			if got, struck := r.InvitationPill(); got != want || struck != wantStruck {
				t.Errorf("conversation %s/%s: pill %q struck %v, want %q %v", k, st, got, struck, want, wantStruck)
			}
		}
	}
	r.SetMessage(Message{Subject: "Plain"})
	if got, struck := r.InvitationPill(); got != "" || struck {
		t.Errorf("plain message: pill %q struck %v", got, struck)
	}
	r.SetMessage(Message{Subject: "KEY-1: Summary", Issue: &jira.IssueRow{Key: "KEY-1"}, Invitation: calendar.PillCancelled})
	if got, struck := r.InvitationPill(); got != "" || struck {
		t.Errorf("issue row: pill %q struck %v", got, struck)
	}
}

// TestCrmPillRow checks, with a GTK display (MALACHI_GTK_SMOKE=1), the
// pill of a message saved in eWay-CRM (crm.PillFor): shown on a message
// row, not on a conversation row or a row of an issue.
func TestCrmPillRow(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	p, ok := crm.PillFor(api.MessageSummary{CrmSaved: true}, i18n.Tr)
	if !ok {
		t.Fatal("no pill for a saved message")
	}
	r := NewMessageRow()
	r.SetMessage(Message{Subject: "Offer", Crm: p})
	if !r.crmPill.Visible() || r.crmPill.Text() != crm.PillLabel || r.crmPill.TooltipText() != p.Tooltip {
		t.Errorf("pill %q %q", r.crmPill.Text(), r.crmPill.TooltipText())
	}
	r.SetThread(Thread{Subject: "Offer", Count: 2})
	if r.crmPill.Visible() {
		t.Error("a conversation row has no pill")
	}
	r.SetMessage(Message{Subject: "Offer", Crm: p, Issue: &jira.IssueRow{Key: "ACME-1"}})
	if r.crmPill.Visible() {
		t.Error("a row of an issue has no pill")
	}
	r.SetMessage(Message{Subject: "Offer"})
	if r.crmPill.Visible() {
		t.Error("an unsaved message has no pill")
	}
}
