// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jiraaccount

import (
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/jira"
)

// listEditor is one list of texts of a Jira account's settings, as a row
// of a boxed list: the senders of notification e-mails, the bot accounts,
// the hidden lines or the name prefixes. Under the title and its
// explanation the entries, each with a button that removes it; a field
// with Add for a new one (Enter adds too); the entries offered with one
// click (jira.Suggestions); and why the entry typed last was not added
// (jira.CheckEntry). It shows what the Controller holds and reports what
// the user did; it checks nothing itself (macOS JiraListEditorView).
//
// The entries are the user's own texts, but an account edited elsewhere
// may carry anything: they are plain text, cut in the middle when long,
// whole in the tooltip.
type listEditor struct {
	*adw.PreferencesRow

	kind        jira.ListKind
	removeLabel string

	// onAdd is Add or Enter with the field's text; true empties the
	// field.
	onAdd func(text string) bool
	// onRemove is the remove button of the entry at an index.
	onRemove func(i int)
	// onSuggestion is a suggestion's button, with its entry.
	onSuggestion func(value string)
	// onTyped is the field's text changing under the user's hands.
	onTyped func()

	entriesBox     *gtk.Box
	field          *gtk.Entry
	suggestionsBox *adw.WrapBox
	problem        *gtk.Label

	entries     []string
	suggestions []jira.Suggestion
	// clearing is the field being emptied after an add, which is not
	// typing.
	clearing bool
}

func newListEditor(kind jira.ListKind, title, subtitle, addLabel, removeLabel string) *listEditor {
	e := &listEditor{PreferencesRow: adw.NewPreferencesRow(), kind: kind, removeLabel: removeLabel}
	e.SetTitle(title)
	e.SetActivatable(false)

	titleLabel := gtk.NewLabel(title)
	titleLabel.SetXAlign(0)
	titleLabel.SetWrap(true)
	subtitleLabel := gtk.NewLabel(subtitle)
	subtitleLabel.SetXAlign(0)
	subtitleLabel.SetWrap(true)
	subtitleLabel.AddCSSClass("dim-label")
	subtitleLabel.AddCSSClass("caption")
	subtitleLabel.SetVisible(subtitle != "")
	header := gtk.NewBox(gtk.OrientationVertical, 2)
	header.Append(titleLabel)
	header.Append(subtitleLabel)

	e.entriesBox = gtk.NewBox(gtk.OrientationVertical, 0)
	e.entriesBox.SetVisible(false)

	e.field = gtk.NewEntry()
	e.field.SetHExpand(true)
	if kind == jira.ListMetadataFilters {
		e.field.AddCSSClass("monospace")
	}
	// The title names the field for assistive technologies.
	titleLabel.SetMnemonicWidget(e.field)
	add := gtk.NewButtonWithLabel(addLabel)
	addRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	addRow.Append(e.field)
	addRow.Append(add)

	e.suggestionsBox = adw.NewWrapBox()
	e.suggestionsBox.SetChildSpacing(6)
	e.suggestionsBox.SetLineSpacing(6)
	e.suggestionsBox.SetVisible(false)

	e.problem = gtk.NewLabel("")
	e.problem.SetUseMarkup(false)
	e.problem.SetXAlign(0)
	e.problem.SetWrap(true)
	e.problem.AddCSSClass("error")
	e.problem.AddCSSClass("caption")
	e.problem.SetVisible(false)

	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(10)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.Append(header)
	box.Append(e.entriesBox)
	box.Append(addRow)
	box.Append(e.suggestionsBox)
	box.Append(e.problem)
	e.SetChild(box)

	e.field.ConnectActivate(func() { e.commit() })
	e.field.ConnectChanged(func() {
		if !e.clearing && e.onTyped != nil {
			e.onTyped()
		}
	})
	add.ConnectClicked(func() {
		e.commit()
		e.field.GrabFocus()
	})
	return e
}

// setPlaceholder is the field's placeholder: what an empty list stands
// for ("@acme.atlassian.net" for the senders of Jira Cloud).
func (e *listEditor) setPlaceholder(text string) {
	e.field.SetPlaceholderText(text)
}

// apply shows the list as the controller holds it.
func (e *listEditor) apply(entries []string, suggestions []jira.Suggestion, problem string) {
	if !slices.Equal(entries, e.entries) {
		e.entries = slices.Clone(entries)
		e.rebuildEntries()
	}
	if !slices.Equal(suggestions, e.suggestions) {
		e.suggestions = slices.Clone(suggestions)
		e.rebuildSuggestions()
	}
	e.problem.SetText(problem)
	e.problem.SetVisible(problem != "")
	if problem != "" {
		e.field.AddCSSClass("error")
	} else {
		e.field.RemoveCSSClass("error")
	}
}

// pending reports text in the field that was not added yet.
func (e *listEditor) pending() bool { return e.field.Text() != "" }

// commit adds what the field holds, as Add does; it reports whether
// nothing is left in it that could not be added.
func (e *listEditor) commit() bool {
	if e.onAdd == nil || !e.onAdd(e.field.Text()) {
		return false
	}
	e.clearing = true
	e.field.SetText("")
	e.clearing = false
	return true
}

func (e *listEditor) rebuildEntries() {
	for child := e.entriesBox.FirstChild(); child != nil; child = e.entriesBox.FirstChild() {
		e.entriesBox.Remove(child)
	}
	for i, text := range e.entries {
		label := gtk.NewLabel(text)
		label.SetUseMarkup(false)
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetEllipsize(pango.EllipsizeMiddle)
		label.SetTooltipText(text)
		if e.kind == jira.ListMetadataFilters {
			label.AddCSSClass("monospace")
		}
		remove := gtk.NewButtonFromIconName("list-remove-symbolic")
		remove.AddCSSClass("flat")
		remove.AddCSSClass("circular")
		remove.SetVAlign(gtk.AlignCenter)
		remove.SetTooltipText(e.removeLabel)
		remove.ConnectClicked(func() {
			if e.onRemove != nil {
				e.onRemove(i)
			}
		})
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		row.Append(label)
		row.Append(remove)
		e.entriesBox.Append(row)
	}
	e.entriesBox.SetVisible(len(e.entries) > 0)
}

func (e *listEditor) rebuildSuggestions() {
	for child := e.suggestionsBox.FirstChild(); child != nil; child = e.suggestionsBox.FirstChild() {
		e.suggestionsBox.Remove(child)
	}
	for _, s := range e.suggestions {
		// The label names data (a bot's name, a pattern): a label child
		// keeps it plain and cuts a long one.
		label := gtk.NewLabel(s.Label)
		label.SetUseMarkup(false)
		label.SetEllipsize(pango.EllipsizeMiddle)
		button := gtk.NewButton()
		button.SetChild(label)
		button.SetTooltipText(s.Value)
		value := s.Value
		button.ConnectClicked(func() {
			if e.onSuggestion != nil {
				e.onSuggestion(value)
			}
		})
		e.suggestionsBox.Append(button)
	}
	e.suggestionsBox.SetVisible(len(e.suggestions) > 0)
}
