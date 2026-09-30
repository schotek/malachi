// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jiraaccount

import (
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// statusPicker is the picker of the statuses that count as closed, as a
// row of the Folders group: under the title and its explanation the
// statuses of the site by category (jira.StatusGroups), each category
// under its name in a pill of its colour (widget.SetStatusPill, as the
// statuses of issues are shown), each status name a check box; and why
// the choice cannot be saved (jira.StatusesProblem). The names come from
// the site and are plain text (macOS JiraStatusPickerView).
type statusPicker struct {
	*adw.PreferencesRow

	// onToggle is a check box the user changed: its choice and whether
	// it is ticked now.
	onToggle func(choice jira.StatusChoice, on bool)

	body    *gtk.Box
	problem *gtk.Label

	groups []jira.StatusGroup
	// choices are the choices in the order of checks.
	choices []jira.StatusChoice
	checks  []*gtk.CheckButton
	// applying is the check boxes being set from the controller.
	applying bool
}

func newStatusPicker(title, subtitle string) *statusPicker {
	p := &statusPicker{PreferencesRow: adw.NewPreferencesRow()}
	p.SetTitle(title)
	p.SetActivatable(false)

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

	p.body = gtk.NewBox(gtk.OrientationVertical, 10)
	p.body.SetVisible(false)

	p.problem = gtk.NewLabel("")
	p.problem.SetUseMarkup(false)
	p.problem.SetXAlign(0)
	p.problem.SetWrap(true)
	p.problem.AddCSSClass("error")
	p.problem.AddCSSClass("caption")
	p.problem.SetVisible(false)

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetMarginTop(10)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.Append(header)
	box.Append(p.body)
	box.Append(p.problem)
	p.SetChild(box)
	return p
}

// apply shows the picker as the controller holds it: the check boxes are
// built again only when the statuses to choose from changed (the listing
// arrived), otherwise only their ticks follow.
func (p *statusPicker) apply(groups []jira.StatusGroup, problem string) {
	if !sameChoices(groups, p.groups) {
		p.groups = groups
		p.rebuild()
	} else {
		p.groups = groups
		p.choices = p.choices[:0]
		for _, g := range groups {
			p.choices = append(p.choices, g.Choices...)
		}
		p.applying = true
		for i, check := range p.checks {
			check.SetActive(p.choices[i].Selected)
		}
		p.applying = false
	}
	p.problem.SetText(problem)
	p.problem.SetVisible(problem != "")
}

// sameChoices reports the same check boxes under the same titles: only
// what is ticked may differ.
func sameChoices(a, b []jira.StatusGroup) bool {
	return slices.EqualFunc(a, b, func(x, y jira.StatusGroup) bool {
		return x.Category == y.Category && x.Title == y.Title && x.Style == y.Style &&
			slices.EqualFunc(x.Choices, y.Choices, func(p, q jira.StatusChoice) bool {
				return p.Name == q.Name && slices.Equal(p.IDs, q.IDs)
			})
	})
}

func (p *statusPicker) rebuild() {
	for child := p.body.FirstChild(); child != nil; child = p.body.FirstChild() {
		p.body.Remove(child)
	}
	p.choices = nil
	p.checks = nil
	for _, g := range p.groups {
		pill := widget.NewPill()
		pill.SetHAlign(gtk.AlignStart)
		widget.SetStatusPill(pill, g.Title, g.Style)

		flow := adw.NewWrapBox()
		flow.SetChildSpacing(14)
		flow.SetLineSpacing(6)
		for _, choice := range g.Choices {
			label := gtk.NewLabel(choice.Name)
			label.SetUseMarkup(false)
			label.SetEllipsize(pango.EllipsizeEnd)
			label.SetMaxWidthChars(32)
			check := gtk.NewCheckButton()
			check.SetChild(label)
			check.SetTooltipText(choice.Name)
			check.SetActive(choice.Selected)
			i := len(p.choices)
			check.ConnectToggled(func() {
				if p.applying || p.onToggle == nil {
					return
				}
				p.onToggle(p.choices[i], check.Active())
			})
			p.choices = append(p.choices, choice)
			p.checks = append(p.checks, check)
			flow.Append(check)
		}

		section := gtk.NewBox(gtk.OrientationVertical, 6)
		section.Append(pill)
		section.Append(flow)
		p.body.Append(section)
	}
	p.body.SetVisible(len(p.groups) > 0)
}
