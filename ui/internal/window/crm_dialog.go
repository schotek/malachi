// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The Save to eWay-CRM dialog (crm_dialog.blp; the macOS CRMDialogSession
// and CRMDialogController): crm.lookup while a spinner shows, then the
// crm.Dialog over its answer with the search field (crm.search once typing
// pauses, from 2 bytes; the results join the groups unchecked), Private,
// Cancel and Save. Save closes the dialog and hands the choice to the
// window's controls, which run crm.save in the background
// (crmControls.runSave). Everything from the CRM is plain text.

// crmDialogPhase is where the dialog is.
type crmDialogPhase int

const (
	// crmLoading: crm.lookup runs.
	crmLoading crmDialogPhase = iota
	// crmReady: model is set.
	crmReady
	// crmFailed: crm.lookup failed; problem says why, only Cancel is left.
	crmFailed
)

// crmEntryRow is a listed item's row: its check box and, for a deal or a
// project, the Superior Item radio.
type crmEntryRow struct {
	check    *gtk.CheckButton
	superior *gtk.CheckButton
}

// crmDialog is one open Save to eWay-CRM dialog.
type crmDialog struct {
	w        *Window
	controls *crmControls // nil for the development sample: Save only closes
	// The messages the dialog saves, of messageAccount.
	messageAccount api.AccountID
	messageIDs     []api.MessageID

	phase   crmDialogPhase
	problem string
	model   *crm.Dialog

	searching     bool
	searchProblem string
	searchGen     int
	finished      bool
	// rendering is set while render writes the check boxes: their
	// toggled handlers ignore what render does.
	rendering bool

	dialog        *adw.Dialog
	cancel, save  *gtk.Button
	summary       *gtk.Label
	search        *gtk.SearchEntry
	searchSpinner *adw.Spinner
	searchError   *gtk.Label
	loading       *gtk.Box
	message       *gtk.Label
	groups        *gtk.Box
	searchHint    *gtk.Label
	partial       *gtk.Label
	private       *adw.SwitchRow

	// rows are the listed items' rows while the listed items stay the
	// same (listed).
	rows   map[api.CrmItemRef]crmEntryRow
	listed []api.CrmItemRef
}

// newCrmDialog builds the dialog for messageIDs of messageAccount with
// summary as its line; loaded fills it.
func newCrmDialog(w *Window, controls *crmControls, messageAccount api.AccountID, messageIDs []api.MessageID, summary string) *crmDialog {
	b := data.Builder("crm_dialog.ui")
	get := func(id string) any { return b.GetObject(id).Cast() }
	d := &crmDialog{
		w:              w,
		controls:       controls,
		messageAccount: messageAccount,
		messageIDs:     messageIDs,
		dialog:         get("crm_dialog").(*adw.Dialog),
		cancel:         get("crm_cancel").(*gtk.Button),
		save:           get("crm_save").(*gtk.Button),
		summary:        get("crm_summary").(*gtk.Label),
		search:         get("crm_search").(*gtk.SearchEntry),
		searchSpinner:  get("crm_search_spinner").(*adw.Spinner),
		searchError:    get("crm_search_problem").(*gtk.Label),
		loading:        get("crm_loading").(*gtk.Box),
		message:        get("crm_message").(*gtk.Label),
		groups:         get("crm_groups").(*gtk.Box),
		searchHint:     get("crm_search_hint").(*gtk.Label),
		partial:        get("crm_partial").(*gtk.Label),
		private:        get("crm_private").(*adw.SwitchRow),
		rows:           map[api.CrmItemRef]crmEntryRow{},
	}
	t := crm.DialogTexts(false, i18n.Tr)
	d.cancel.SetLabel(i18n.T("_Cancel"))
	d.save.SetLabel(t.Save)
	d.summary.SetText(summary)
	d.summary.SetTooltipText(summary)
	d.search.SetPlaceholderText(t.Search)
	get("crm_loading_label").(*gtk.Label).SetText(crm.ActionText(crm.ActionLookup, i18n.Tr))
	d.searchHint.SetText(t.SearchHint)
	d.partial.SetText(t.Partial)
	d.private.SetTitle(t.Private)
	d.private.SetSubtitle(t.PrivateHint)

	d.cancel.ConnectClicked(func() { d.dialog.Close() })
	d.save.ConnectClicked(d.saveChoice)
	d.search.ConnectSearchChanged(func() { d.startSearch(d.search.Text()) })
	// Enter in the search field saves, as the default button would.
	d.search.ConnectActivate(d.saveChoice)
	d.private.NotifyProperty("active", func() {
		if d.rendering || d.model == nil {
			return
		}
		d.model.SetPrivate(d.private.Active())
		d.render()
	})
	d.dialog.ConnectClosed(func() {
		d.finished = true
		d.searchGen++
	})
	d.render()
	return d
}

// present shows the dialog over parent.
func (d *crmDialog) present(parent gtk.Widgetter) {
	d.dialog.Present(parent)
}

// loaded is crm.lookup's answer.
func (d *crmDialog) loaded(res api.CrmLookupResult, err error) {
	if d.finished {
		return
	}
	if err != nil {
		d.w.log.Info("crm.lookup failed", "code", crmErrorCode(err))
		d.phase, d.problem = crmFailed, crmErrorText(crm.ActionLookup, err)
	} else {
		d.model = crm.NewDialog(res)
		d.phase = crmReady
	}
	d.render()
	if d.phase == crmReady {
		d.search.GrabFocus()
	}
}

// startSearch is the search field as typed (GtkSearchEntry waits for the
// pause in typing): crm.search, nothing below 2 bytes; the results join
// the groups unchecked (crm.Dialog.Merge).
func (d *crmDialog) startSearch(text string) {
	d.searchGen++
	gen := d.searchGen
	d.searchProblem = ""
	d.searching = false
	if d.model == nil || d.finished {
		d.render()
		return
	}
	params, ok := d.model.SearchParams(text)
	if !ok {
		d.render()
		return
	}
	if d.controls == nil {
		d.render() // the development sample: no daemon to ask
		return
	}
	d.searching = true
	d.render()
	w := d.w
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), crmSearchTimeout)
		defer cancel()
		var res api.CrmSearchResult
		err := w.client.Call(ctx, api.MethodCrmSearch, params, &res)
		glib.IdleAdd(func() {
			if gen != d.searchGen || d.finished {
				return
			}
			d.searching = false
			if err != nil {
				w.log.Info("crm.search failed", "code", crmErrorCode(err))
				d.searchProblem = crmErrorText(crm.ActionSearch, err)
			} else {
				d.model.Merge(res)
			}
			d.render()
		})
	}()
}

// saveChoice is Save: the dialog closes and the controls run crm.save.
func (d *crmDialog) saveChoice() {
	if d.finished || d.phase != crmReady || d.model == nil || !d.model.CanSave() {
		return
	}
	params, ok := d.model.SaveParams(d.messageAccount, d.messageIDs)
	if !ok {
		return
	}
	d.dialog.Close()
	if d.controls != nil {
		d.controls.runSave(params)
	}
}

// render draws the dialog's state: the phase, the groups (rebuilt only
// when the listed items change, so the keyboard focus stays), the checks
// and the radio, Private, the hints and Save.
func (d *crmDialog) render() {
	d.rendering = true
	defer func() { d.rendering = false }()
	editing := d.model != nil && d.model.Editing()
	t := crm.DialogTexts(editing, i18n.Tr)
	d.dialog.SetTitle(t.Title)

	ready := d.phase == crmReady && d.model != nil
	d.loading.SetVisible(d.phase == crmLoading)
	switch {
	case d.phase == crmFailed:
		d.message.SetText(d.problem)
		d.message.SetVisible(true)
	case ready && d.model.IsEmpty():
		d.message.SetText(t.Empty)
		d.message.SetVisible(true)
	default:
		d.message.SetVisible(false)
	}
	d.search.SetSensitive(ready)
	d.searchSpinner.SetVisible(d.searching)
	d.searchError.SetText(d.searchProblem)
	d.searchError.SetVisible(d.searchProblem != "")
	d.partial.SetVisible(ready && d.model.Partial())
	d.searchHint.SetVisible(ready && d.model.OffersSearchHint())
	d.private.SetSensitive(ready)
	d.private.SetActive(ready && d.model.Private())
	d.save.SetSensitive(ready && d.model.CanSave())
	d.renderGroups(t)
}

// renderGroups shows the model's groups: rebuilt when the listed items
// changed, else only their check boxes and radios set.
func (d *crmDialog) renderGroups(t crm.DialogStrings) {
	var groups []crm.DialogGroup
	if d.model != nil {
		groups = d.model.Groups()
	}
	var refs []api.CrmItemRef
	for _, g := range groups {
		for _, e := range g.Entries {
			refs = append(refs, e.Ref())
		}
	}
	if !slices.Equal(refs, d.listed) {
		d.rebuild(groups, t)
		d.listed = refs
	}
	for _, g := range groups {
		for _, e := range g.Entries {
			r, ok := d.rows[e.Ref()]
			if !ok {
				continue
			}
			r.check.SetActive(e.Checked)
			if r.superior != nil {
				r.superior.SetActive(e.Superior)
			}
		}
	}
}

// rebuild makes the groups again: a heading per kind and a boxed list of
// check rows, the item's name over its detail.
func (d *crmDialog) rebuild(groups []crm.DialogGroup, t crm.DialogStrings) {
	for child := d.groups.FirstChild(); child != nil; child = d.groups.FirstChild() {
		d.groups.Remove(child)
	}
	d.rows = map[api.CrmItemRef]crmEntryRow{}
	// One radio group across every deal and project: the superior item
	// is one at most.
	var radioGroup *gtk.CheckButton
	for i, g := range groups {
		heading := plainLabel(crm.GroupTitle(g.Kind, i18n.Tr), "heading")
		if i > 0 {
			heading.SetMarginTop(12)
		}
		d.groups.Append(heading)
		list := gtk.NewListBox()
		list.SetSelectionMode(gtk.SelectionNone)
		list.AddCSSClass("boxed-list")
		for _, e := range g.Entries {
			ref := e.Ref()
			row := adw.NewActionRow()
			row.SetUseMarkup(false)
			row.SetTitle(e.Item.Name)
			row.SetTitleLines(1)
			row.SetTooltipText(e.Item.Name)
			if e.Item.Detail != "" {
				row.SetSubtitle(e.Item.Detail)
				row.SetSubtitleLines(1)
			}
			check := gtk.NewCheckButton()
			check.SetVAlign(gtk.AlignCenter)
			row.AddPrefix(check)
			row.SetActivatableWidget(check)
			check.ConnectToggled(func() {
				if d.rendering || d.model == nil {
					return
				}
				d.model.Toggle(ref, check.Active())
				// A refused check (more than 50 changes) is drawn back.
				d.render()
			})
			er := crmEntryRow{check: check}
			if e.CanBeSuperior() {
				radio := gtk.NewCheckButtonWithLabel(t.SuperiorItem)
				radio.SetVAlign(gtk.AlignCenter)
				radio.AddCSSClass("caption")
				if radioGroup == nil {
					radioGroup = radio
				} else {
					radio.SetGroup(radioGroup)
				}
				radio.ConnectToggled(func() {
					if d.rendering || d.model == nil || !radio.Active() {
						return
					}
					d.model.SetSuperior(ref)
					d.render()
				})
				row.AddSuffix(radio)
				er.superior = radio
			}
			d.rows[ref] = er
			list.Append(row)
		}
		d.groups.Append(list)
	}
}

// crmSampleLookup are invented suggestions for the dialog's look
// (MALACHI_GTK_START crm-dialog, the smoke test): fictional people and
// companies, as the macOS CRM.sampleLookup.
func crmSampleLookup() api.CrmLookupResult {
	g := func(n string) string { return "00000000-0000-4000-8000-00000000000" + n }
	return api.CrmLookupResult{
		AccountID: "sample",
		Saved:     []api.CrmSavedMessage{{MessageID: "m_sample"}},
		Suggestions: []api.CrmItem{
			{Kind: api.CrmContact, GUID: g("1"), Name: "Novák, Jan", Detail: "jan.novak@acme.example", Reason: api.CrmReasonAddress},
			{Kind: api.CrmContact, GUID: g("2"), Name: "Dvořáková, Jana", Detail: "jana@acme.example", Reason: api.CrmReasonAddress},
			{Kind: api.CrmCompany, GUID: g("3"), Name: "Acme s.r.o.", Detail: "acme.example", Reason: api.CrmReasonDomain},
			{Kind: api.CrmCompany, GUID: g("4"), Name: "Acme Holding a.s.", Reason: api.CrmReasonContact},
			{Kind: api.CrmDeal, GUID: g("5"), Name: "Fleet renewal 2027", Detail: "Open", Reason: api.CrmReasonContact, Superior: true},
			{Kind: api.CrmDeal, GUID: g("6"), Name: "Service contract", Detail: "Open", Reason: api.CrmReasonCompany},
			{Kind: api.CrmProject, GUID: g("7"), Name: "Rollout Brno", Reason: api.CrmReasonCompany},
		},
		Partial: true,
	}
}

// showCrmSample is the dialog over crmSampleLookup without a daemon
// (development only): Save and Cancel only close it, the search finds
// nothing.
func (w *Window) showCrmSample() *crmDialog {
	d := newCrmDialog(w, nil, "sample", []api.MessageID{"m_sample"}, "Offer for the fleet renewal · Jan Novák")
	d.present(w)
	d.loaded(crmSampleLookup(), nil)
	return d
}
