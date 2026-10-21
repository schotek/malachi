// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The assistant panel's part of the user's profile (docs/plans/
// assistant-profile.md P16; macOS AssistantProposalView and the header
// button of AssistantPanelViewController): a card for each note the
// assistant proposed (ContentProposal), and the header button that opens
// Preferences → AI → About You.

// proposalOutcome is what the user did with a proposal card.
type proposalOutcome int

const (
	proposalPending proposalOutcome = iota
	proposalSaved
	proposalDismissed
)

// proposalCard is what a proposal card shows.
type proposalCard struct {
	Hidden, Buttons, Saved bool
}

// proposalCardFor decides a card: dismissed hides it, saved shows "Saved
// to your profile", a pending one shows its buttons while the store still
// has the proposal (one resolved elsewhere, or not read yet, shows none).
func proposalCardFor(o proposalOutcome, found bool) proposalCard {
	switch o {
	case proposalDismissed:
		return proposalCard{Hidden: true}
	case proposalSaved:
		return proposalCard{Saved: true}
	}
	return proposalCard{Buttons: found}
}

// proposalAccountLine is "For the account <name>" for a note of an
// account (its name, else its address, else its id), "" for the global
// text.
func proposalAccountLine(t assistant.ProfileStrings, q api.ProfileProposal, notes []assistant.AccountNote) string {
	if q.AccountID == "" {
		return ""
	}
	name := string(q.AccountID)
	for _, n := range notes {
		if n.AccountID != string(q.AccountID) {
			continue
		}
		switch {
		case n.Name != "":
			name = n.Name
		case n.Email != "":
			name = n.Email
		}
		break
	}
	return fmt.Sprintf(t.AccountProposal, name)
}

// proposalRow is a note the assistant proposed: its text read from the
// profile store (never from the answer), the account it is for, and Save,
// Edit… and Dismiss.
func (p *assistantPanel) proposalRow(id string) *transcriptRow {
	t := assistant.ProfileTexts(tr)
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("assistant-card")
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	icon := gtk.NewImageFromIconName("avatar-default-symbolic")
	title := transcriptLabel("caption", "dim-label")
	title.SetText(t.ProposalTitle)
	title.SetHExpand(true)
	head.Append(icon)
	head.Append(title)
	text := transcriptLabel()
	detail := transcriptLabel("caption", "dim-label")
	save := gtk.NewButtonWithLabel(t.Save)
	edit := gtk.NewButtonWithLabel(t.Edit)
	dismiss := gtk.NewButtonWithLabel(t.Dismiss)
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	buttons.SetHAlign(gtk.AlignStart)
	for _, b := range []*gtk.Button{save, edit, dismiss} {
		b.AddCSSClass("chip-action")
		buttons.Append(b)
	}
	box.Append(head)
	box.Append(text)
	box.Append(detail)
	box.Append(buttons)

	outcome, busy := proposalPending, false
	var refresh func()
	refresh = func() {
		store := p.w.assist.ProfileStore()
		var q api.ProfileProposal
		found := false
		if store != nil {
			q, found = store.Proposal(id)
		}
		card := proposalCardFor(outcome, found)
		box.SetVisible(!card.Hidden)
		buttons.SetVisible(card.Buttons)
		for _, b := range []*gtk.Button{save, edit, dismiss} {
			b.SetSensitive(!busy)
		}
		switch {
		case card.Saved:
			detail.SetText(t.ProposalOK)
			detail.SetVisible(true)
		case found:
			text.SetText(q.Text)
			line := proposalAccountLine(t, q, store.Profile().Accounts)
			detail.SetText(line)
			detail.SetVisible(line != "")
		default:
			detail.SetVisible(false)
		}
		text.SetVisible(text.Text() != "")
	}
	resolve := func(accept bool, edited *string) {
		store := p.w.assist.ProfileStore()
		if store == nil || busy {
			return
		}
		busy = true
		refresh()
		store.Resolve(id, accept, edited, func(err error) {
			busy = false
			if err == nil {
				if accept {
					outcome = proposalSaved
				} else {
					outcome = proposalDismissed
				}
			} else {
				p.w.log.Info("assistant profile: resolve", "err", err)
				box.ErrorBell()
			}
			refresh()
		})
	}
	save.ConnectClicked(func() { resolve(true, nil) })
	dismiss.ConnectClicked(func() { resolve(false, nil) })
	edit.ConnectClicked(func() {
		store := p.w.assist.ProfileStore()
		if store == nil {
			return
		}
		q, ok := store.Proposal(id)
		if !ok {
			return
		}
		p.editProposal(q.Text, func(edited string) { resolve(true, &edited) })
	})
	refresh()
	return &transcriptRow{root: box, update: func(assistantpanel.Content) {}, refresh: refresh}
}

// editProposal asks for the proposal's text in a dialog; done gets the
// edited text on Save.
func (p *assistantPanel) editProposal(text string, done func(string)) {
	t := assistant.ProfileTexts(tr)
	d := adw.NewAlertDialog(t.ProposalTitle, "")
	d.SetHeadingUseMarkup(false)
	entry := gtk.NewEntry()
	entry.SetText(text)
	entry.SetMaxLength(api.MaxProfileProposal)
	entry.SetActivatesDefault(true)
	d.SetExtraChild(entry)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("save", t.Save)
	d.SetResponseAppearance("save", adw.ResponseSuggested)
	d.SetDefaultResponse("save")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(r string) {
		if r == "save" {
			done(entry.Text())
		}
	})
	d.Present(p.root)
}

// bindProfile: the proposal cards and the header button follow the
// profile store. The main window lives as long as the application: no
// unbinding.
func (p *assistantPanel) bindProfile(button *gtk.Button) {
	p.profileButton = button
	p.w.assist.OnProfileChange(func() {
		for _, r := range p.rows {
			if r.refresh != nil {
				r.refresh()
			}
		}
		p.updateProfileButton()
	})
	p.updateProfileButton()
}

// updateProfileButton: hidden without the profile (no store yet, an older
// daemon); the tooltip counts the pending proposals.
func (p *assistantPanel) updateProfileButton() {
	if p.profileButton == nil {
		return
	}
	store := p.w.assist.ProfileStore()
	if store == nil || !store.Supported() {
		p.profileButton.SetVisible(false)
		return
	}
	p.profileButton.SetVisible(true)
	tip := assistant.ProfileProposalCount(tr, len(store.Proposals()))
	p.profileButton.SetTooltipText(tip)
	p.profileButton.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(tip)})
}
