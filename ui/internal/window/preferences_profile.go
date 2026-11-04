// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Preferences → AI → About You (docs/plans/assistant-profile.md §5; macOS
// AboutYouGroup.swift): the grammatical gender the user writes in, what
// they wrote about themselves, a note for each mail and Jira account, the
// switch that shares the profile with apps over MCP, and the notes an
// assistant proposed. Everything goes through the application's
// ProfileStore (window/assistant_profile.go); the texts are
// assistant.ProfileTexts. Text the user is typing is saved after a second
// of quiet, when the field loses the keyboard and when the dialog closes,
// and only when it differs from what the daemon has; a change of the store
// never overwrites a field that holds unsaved typing or the keyboard.

// profileSaveDelay is the quiet after typing before the text is saved.
const profileSaveDelay = time.Second

// profileNotePreviewRunes caps the note an account's row shows as its
// subtitle.
const profileNotePreviewRunes = 40

// profileNoteKind reports an account that can have a note
// (api.ProfileNoteKinds).
func profileNoteKind(a api.Account) bool {
	k := a.Config.Protocol()
	for _, n := range api.ProfileNoteKinds {
		if k == n {
			return true
		}
	}
	return false
}

// profileNotePreview is an account row's subtitle: the note's first
// characters on one line, noNote for none.
func profileNotePreview(note, noNote string) string {
	line := strings.Join(strings.Fields(note), " ")
	if line == "" {
		return noNote
	}
	if utf8.RuneCountInString(line) <= profileNotePreviewRunes {
		return line
	}
	r := []rune(line)
	return strings.TrimSpace(string(r[:profileNotePreviewRunes])) + "…"
}

// profileNeedsSave says whether a field's text differs from what the
// daemon stored (the daemon trims, so trailing spaces alone do not count).
func profileNeedsSave(text, stored string) bool {
	return strings.TrimSpace(text) != strings.TrimSpace(stored)
}

// capRunes cuts s to at most n characters; ok false when it had to.
func capRunes(s string, n int) (string, bool) {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s, true
	}
	return string([]rune(s)[:n]), false
}

// profileGenders are the choices of I Write As, in this order.
var profileGenders = []api.ProfileGender{api.GenderUnset, api.GenderMasculine, api.GenderFeminine, api.GenderNeutral}

// profileField is one text area of the group with its counter: the
// profile's text or an account's note.
type profileField struct {
	view    *gtk.TextView
	counter *gtk.Label
	hint    *gtk.Label
	limit   func() int
	// stored is the daemon's text, what the field is compared with.
	stored func() string
	// save writes text.
	save func(text string)

	setting bool // the text is being set programmatically
	focused bool
	timer   glib.SourceHandle
}

func newProfileField(placeholder string, limit func() int, stored func() string, save func(string)) (*profileField, gtk.Widgetter) {
	f := &profileField{limit: limit, stored: stored, save: save}
	f.view = gtk.NewTextView()
	f.view.SetWrapMode(gtk.WrapWordChar)
	f.view.SetAcceptsTab(false)
	f.view.SetTopMargin(8)
	f.view.SetBottomMargin(8)
	f.view.SetLeftMargin(8)
	f.view.SetRightMargin(8)
	f.view.SetSizeRequest(-1, 96)
	f.hint = gtk.NewLabel(placeholder)
	f.hint.SetUseMarkup(false)
	f.hint.SetXAlign(0)
	f.hint.SetYAlign(0)
	f.hint.SetWrap(true)
	f.hint.SetMarginTop(8)
	f.hint.SetMarginStart(10)
	f.hint.SetMarginEnd(10)
	f.hint.SetCanTarget(false)
	f.hint.AddCSSClass("dim-label")
	overlay := gtk.NewOverlay()
	overlay.SetChild(f.view)
	overlay.AddOverlay(f.hint)
	frame := gtk.NewFrame("")
	frame.SetChild(overlay)
	f.counter = gtk.NewLabel("")
	f.counter.SetXAlign(1)
	f.counter.AddCSSClass("dim-label")
	f.counter.AddCSSClass("caption")
	f.counter.AddCSSClass("numeric")
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.Append(frame)
	box.Append(f.counter)

	buf := f.view.Buffer()
	buf.ConnectChanged(func() { f.changed() })
	focus := gtk.NewEventControllerFocus()
	focus.ConnectEnter(func() { f.focused = true })
	focus.ConnectLeave(func() {
		f.focused = false
		f.flush()
	})
	f.view.AddController(focus)
	f.update()
	return f, box
}

func (f *profileField) text() string {
	buf := f.view.Buffer()
	start, end := buf.Bounds()
	return buf.Text(start, end, false)
}

// set shows text unless the field holds unsaved typing or the keyboard.
func (f *profileField) set(text string) {
	if f.focused || f.timer != 0 || f.text() == text {
		f.update()
		return
	}
	f.setting = true
	f.view.Buffer().SetText(text)
	f.setting = false
	f.update()
}

func (f *profileField) update() {
	t := f.text()
	f.hint.SetVisible(t == "")
	f.counter.SetText(assistant.ProfileCounter(i18n.Tr, utf8.RuneCountInString(t), f.limit()))
}

func (f *profileField) changed() {
	if f.setting {
		return
	}
	// Typing past the limit is refused: the text goes back to the limit.
	if capped, ok := capRunes(f.text(), f.limit()); !ok {
		f.setting = true
		f.view.Buffer().SetText(capped)
		f.setting = false
		f.view.ErrorBell()
	}
	f.update()
	f.cancelTimer()
	f.timer = glib.TimeoutAdd(uint(profileSaveDelay.Milliseconds()), func() bool {
		f.timer = 0
		f.flush()
		return false
	})
}

func (f *profileField) cancelTimer() {
	if f.timer != 0 {
		glib.SourceRemove(f.timer)
		f.timer = 0
	}
}

// flush saves the text now when it changed.
func (f *profileField) flush() {
	f.cancelTimer()
	if t := f.text(); profileNeedsSave(t, f.stored()) {
		f.save(t)
	}
}

// profileGroup is the About You group of one Preferences dialog.
type profileGroup struct {
	d       *PreferencesDialog
	store   *assistantpanel.ProfileStore
	group   *adw.PreferencesGroup
	pending *adw.PreferencesGroup
	texts   assistant.ProfileStrings

	gender   *adw.ComboRow
	text     *profileField
	share    *adw.SwitchRow
	syncing  bool
	accounts []api.Account
	notes    map[api.AccountID]*profileNoteRow
	// pendingRows are the rows of the proposals group.
	pendingRows []gtk.Widgetter
	closed      bool
}

type profileNoteRow struct {
	row   *adw.ExpanderRow
	field *profileField
}

// bindProfile fills About You and Suggested by the Assistant; the
// returned function saves what is typed and stops following the store
// (the dialog closed).
func (d *PreferencesDialog) bindProfile(b *gtk.Builder, c *client.Client) (unbind func()) {
	group := b.GetObject("about_you_group").Cast().(*adw.PreferencesGroup)
	pending := b.GetObject("about_you_pending_group").Cast().(*adw.PreferencesGroup)
	if d.assist == nil || d.assist.ProfileStore() == nil {
		group.SetVisible(false)
		pending.SetVisible(false)
		return func() {}
	}
	g := &profileGroup{
		d: d, store: d.assist.ProfileStore(), group: group, pending: pending,
		texts: assistant.ProfileTexts(i18n.Tr), notes: map[api.AccountID]*profileNoteRow{},
	}
	g.build()
	remove := d.assist.OnProfileChange(g.refresh)
	g.refresh()
	g.loadAccounts(c)
	// What is there may be older than the dialog.
	g.store.Refresh()
	return func() {
		remove()
		g.text.flush()
		for _, n := range g.notes {
			n.field.flush()
		}
		g.closed = true
	}
}

func (g *profileGroup) build() {
	t := g.texts
	g.group.SetTitle(t.Group)
	g.group.SetDescription(t.GroupDescription)

	g.gender = adw.NewComboRow()
	g.gender.SetTitle(t.GenderRow)
	labels := make([]string, len(profileGenders))
	for i, gd := range profileGenders {
		labels[i] = genderLabel(t, gd)
	}
	g.gender.SetModel(gtk.NewStringList(labels))
	g.gender.NotifyProperty("selected", func() {
		i := int(g.gender.Selected())
		if g.syncing || i < 0 || i >= len(profileGenders) || profileGenders[i] == g.store.Stored().Gender {
			return
		}
		gd := profileGenders[i]
		g.store.Set(api.AssistantProfileSetParams{Gender: &gd}, g.written)
	})
	g.group.Add(g.gender)

	var area gtk.Widgetter
	g.text, area = newProfileField(t.TextPlaceholder,
		func() int { return g.store.Limits().Text },
		func() string { return g.store.Stored().Text },
		func(text string) { g.store.Set(api.AssistantProfileSetParams{Text: &text}, g.written) })
	textRow := adw.NewPreferencesRow()
	textRow.SetTitle(t.Group)
	textRow.SetActivatable(false)
	textRow.SetChild(paddedProfileBox(area))
	g.group.Add(textRow)

	g.share = adw.NewSwitchRow()
	g.share.SetTitle(t.ShareRow)
	g.share.SetSubtitle(t.ShareSubtitle)
	g.share.NotifyProperty("active", func() {
		v := g.share.Active()
		if g.syncing || v == g.store.Stored().ShareWithMCP {
			return
		}
		g.store.Set(api.AssistantProfileSetParams{ShareWithMCP: &v}, g.written)
	})
	g.group.Add(g.share)

	g.pending.SetTitle(t.Pending)
}

func paddedProfileBox(child gtk.Widgetter) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.SetMarginTop(10)
	box.SetMarginBottom(10)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.Append(child)
	return box
}

func genderLabel(t assistant.ProfileStrings, g api.ProfileGender) string {
	switch g {
	case api.GenderMasculine:
		return t.GenderMasculine
	case api.GenderFeminine:
		return t.GenderFeminine
	case api.GenderNeutral:
		return t.GenderNeutral
	}
	return t.GenderUnset
}

// written follows a write: a refused one beeps and shows what is stored
// (there is no msgid for its reason).
func (g *profileGroup) written(err error) {
	if g.closed || err == nil {
		return
	}
	g.d.log.Info("assistant profile: write refused", "err", err)
	g.group.ErrorBell()
	g.refresh()
}

// loadAccounts lists the mail and Jira accounts for their notes.
func (g *profileGroup) loadAccounts(c *client.Client) {
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var res api.AccountListResult
		err := c.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &res)
		glib.IdleAdd(func() {
			if g.closed || err != nil {
				return
			}
			g.setAccounts(res.Accounts)
		})
	}()
}

func (g *profileGroup) setAccounts(all []api.Account) {
	var keep []api.Account
	for _, a := range all {
		if profileNoteKind(a) {
			keep = append(keep, a)
		}
	}
	for id, n := range g.notes {
		n.field.flush()
		g.group.Remove(n.row)
		delete(g.notes, id)
	}
	g.accounts = keep
	// The rows go between the text and the switch.
	g.group.Remove(g.share)
	for _, a := range keep {
		id := a.ID
		row := adw.NewExpanderRow()
		row.SetUseMarkup(false)
		row.SetTitle(a.Config.Name)
		field, area := newProfileField(g.texts.NotePlaceholder,
			func() int { return g.store.Limits().AccountNote },
			func() string { return g.storedNote(id) },
			func(text string) {
				g.store.Set(api.AssistantProfileSetParams{Accounts: []api.ProfileAccountNote{{AccountID: id, Note: text}}}, g.written)
			})
		inner := adw.NewPreferencesRow()
		inner.SetTitle(a.Config.Name)
		inner.SetActivatable(false)
		inner.SetChild(paddedProfileBox(area))
		row.AddRow(inner)
		g.group.Add(row)
		g.notes[id] = &profileNoteRow{row: row, field: field}
	}
	g.group.Add(g.share)
	g.refresh()
}

func (g *profileGroup) storedNote(id api.AccountID) string {
	for _, n := range g.store.Stored().Accounts {
		if n.AccountID == id {
			return n.Note
		}
	}
	return ""
}

// refresh shows the store: hidden for an older daemon, insensitive until
// it loaded.
func (g *profileGroup) refresh() {
	if g.closed {
		return
	}
	s := g.store
	hidden := s.Loaded() && !s.Supported()
	g.group.SetVisible(!hidden)
	g.group.SetSensitive(s.Loaded() && s.Supported())
	p := s.Stored()
	g.syncing = true
	for i, gd := range profileGenders {
		if gd == p.Gender {
			g.gender.SetSelected(uint(i))
		}
	}
	g.share.SetActive(p.ShareWithMCP)
	g.syncing = false
	g.text.set(p.Text)
	for id, n := range g.notes {
		note := g.storedNote(id)
		n.field.set(note)
		n.row.SetSubtitle(profileNotePreview(note, g.texts.NoNote))
	}
	g.refreshPending(hidden)
}

// refreshPending rebuilds the proposals group.
func (g *profileGroup) refreshPending(hidden bool) {
	for _, r := range g.pendingRows {
		g.pending.Remove(r)
	}
	g.pendingRows = nil
	props := g.store.Proposals()
	g.pending.SetVisible(!hidden && len(props) > 0)
	for _, q := range props {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(q.Text)
		row.SetTitleLines(0)
		row.SetSubtitle(g.proposalSubtitle(q))
		id := q.ID
		for _, b := range []struct {
			label string
			do    func()
		}{
			{g.texts.Save, func() { g.store.Resolve(id, true, nil, g.written) }},
			{g.texts.Edit, func() { g.editProposal(id) }},
			{g.texts.Dismiss, func() { g.store.Resolve(id, false, nil, g.written) }},
		} {
			btn := gtk.NewButtonWithLabel(b.label)
			btn.SetVAlign(gtk.AlignCenter)
			do := b.do
			btn.ConnectClicked(func() { do() })
			row.AddSuffix(btn)
		}
		g.pending.Add(row)
		g.pendingRows = append(g.pendingRows, row)
	}
}

// proposalSubtitle is where a proposal came from, and its account.
func (g *profileGroup) proposalSubtitle(q api.ProfileProposal) string {
	from := g.texts.FromOtherApp
	if q.Source == api.ProfileFromPanel {
		from = g.texts.FromPanel
	}
	if q.AccountID == "" {
		return from
	}
	name := string(q.AccountID)
	for _, a := range g.accounts {
		if a.ID == q.AccountID && a.Config.Name != "" {
			name = a.Config.Name
		}
	}
	return from + " · " + strings.Replace(g.texts.AccountProposal, "%s", name, 1)
}

// editProposal asks for the proposal's text and saves the edit.
func (g *profileGroup) editProposal(id string) {
	var q api.ProfileProposal
	found := false
	for _, p := range g.store.Proposals() {
		if p.ID == id {
			q, found = p, true
		}
	}
	if !found {
		return
	}
	entry := gtk.NewEntry()
	entry.SetText(q.Text)
	entry.SetActivatesDefault(true)
	entry.SetMaxLength(g.store.Limits().Proposal)
	d := adw.NewAlertDialog(g.texts.ProposalTitle, "")
	d.SetExtraChild(entry)
	d.AddResponse("cancel", calendar.Cancel(i18n.Tr))
	d.AddResponse("save", g.texts.Save)
	d.SetResponseAppearance("save", adw.ResponseSuggested)
	d.SetDefaultResponse("save")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(r string) {
		if r != "save" || g.closed {
			return
		}
		text := entry.Text()
		g.store.Resolve(id, true, &text, g.written)
	})
	d.Present(g.d)
	entry.GrabFocus()
}
