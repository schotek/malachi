// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"os/exec"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/maildate"
	"github.com/schotek/malachi/ui/internal/widget"
)

func (w *Window) usesDateGroups() bool {
	return !w.model.search.active && w.model.folderRole(w.model.listFolder) == api.RoleInbox
}

func (w *Window) dateSections() []maildate.Section[listKey] {
	rows := make([]maildate.Row[listKey], 0, w.model.rowCount())
	for i := 0; i < w.model.rowCount(); i++ {
		r, _ := w.model.rowAt(i)
		date, flags := r.Message.Date, r.Message.Flags
		if r.Thread {
			date, flags = r.Summary.LatestDate, r.Summary.Flags
		}
		rows = append(rows, maildate.Row[listKey]{Key: r.Key, Date: date, Flagged: hasFlag(flags, api.FlagFlagged), Member: r.Member})
	}
	return maildate.Sections(rows, time.Now(), w.dateWeekStart)
}

// rowForWidget resolves identity rather than interpreting a GTK index as
// a message index: date headings and folded sections also occupy list space.
func (w *Window) rowForWidget(row *gtk.ListBoxRow) (listRow, bool) {
	if row == nil {
		return listRow{}, false
	}
	for key, r := range w.rows {
		if r.Index() == row.Index() {
			return w.model.rowAt(w.model.rowIndexOf(key))
		}
	}
	return listRow{}, false
}

// syncDateRows is the inbox projection for both flat and conversation mode.
// Reuse message widgets and keep selection by identity through flag changes,
// pagination and midnight. Headings are never selectable message targets.
func (w *Window) syncDateRows(neighbour bool) {
	prev, hadSelection := w.selectedKey()
	prevIndex := -1
	if hadSelection {
		prevIndex = w.messageList.SelectedRow().Index()
	}
	if w.dateFolder != w.model.listFolder {
		w.dateFolder = w.model.listFolder
		w.dateCollapsed = make(map[maildate.Group]bool)
	}
	sections := w.dateSections()
	if hadSelection {
		for _, section := range sections {
			for _, key := range section.Keys {
				if key == prev {
					delete(w.dateCollapsed, section.Group)
				}
			}
		}
	}
	oldRows := w.rows
	w.reselecting = true
	// Clear the row's GTK selection state before detaching and reusing it.
	// RemoveAll alone can leave a reused row marked selected while the
	// ListBox itself no longer holds a selection, preventing reselection.
	w.messageList.UnselectAll()
	w.messageList.RemoveAll()
	w.rows = make(map[listKey]*widget.MessageRow)
	w.dateButtons = make(map[maildate.Group]*gtk.Button)
	var visible []listKey
	for _, section := range sections {
		group := section.Group
		b := data.Builder("mail_date_header.ui")
		header := b.GetObject("date_header").Cast().(*gtk.ListBoxRow)
		button := b.GetObject("toggle").Cast().(*gtk.Button)
		w.dateButtons[group] = button
		label := b.GetObject("title").Cast().(*gtk.Label)
		label.SetLabel(group.Title(i18n.Tr))
		arrow := b.GetObject("arrow").Cast().(*gtk.Image)
		if w.dateCollapsed[group] {
			arrow.SetFromIconName("pan-end-symbolic")
			button.SetTooltipText(i18n.T("Collapsed"))
		} else {
			button.SetTooltipText(i18n.T("Expanded"))
		}
		button.ConnectClicked(func() { w.toggleDateGroup(group) })
		w.messageList.Append(header)
		if w.dateCollapsed[group] {
			continue
		}
		for _, key := range section.Keys {
			row, _ := w.model.rowAt(w.model.rowIndexOf(key))
			r := oldRows[key]
			if r == nil {
				if w.model.grouped {
					r = w.newRowFor(row)
				} else {
					r = w.newMessageRow(row.Message)
				}
			} else if w.model.grouped {
				w.renderRow(r, row)
			} else {
				r.SetMessage(w.model.rowMessage(row.Message))
			}
			w.rows[key] = r
			w.messageList.Append(r)
			visible = append(visible, key)
		}
	}
	selected := false
	changed := false
	if hadSelection {
		r := w.rows[prev]
		if r == nil && prev.Thread != "" {
			r = w.rows[listKey{Thread: prev.Thread}]
			changed = r != nil
		}
		if r == nil && neighbour && len(visible) > 0 {
			key := visible[len(visible)-1]
			for _, candidate := range visible {
				if w.rows[candidate].Index() >= prevIndex {
					key = candidate
					break
				}
			}
			r = w.rows[key]
			changed = true
		}
		if r != nil {
			w.messageList.SelectRow(r.ListBoxRow)
			selected = true
		}
	}
	w.reselecting = false
	if hadSelection && !selected {
		w.onMessageRowSelected(nil)
	} else if changed {
		w.onMessageRowSelected(w.messageList.SelectedRow())
	}
	w.showListState()
	w.conversationListChanged()
}

func (w *Window) toggleDateGroup(group maildate.Group) {
	if w.dateCollapsed == nil {
		w.dateCollapsed = make(map[maildate.Group]bool)
	}
	if w.dateCollapsed[group] {
		delete(w.dateCollapsed, group)
	} else {
		w.dateCollapsed[group] = true
	}
	if w.dateCollapsed[group] {
		key, selected := w.selectedKey()
		for _, section := range w.dateSections() {
			if section.Group != group {
				continue
			}
			for _, member := range section.Keys {
				if selected && member == key {
					w.messageList.UnselectAll()
				}
			}
		}
	}
	w.syncDateRows(false)
	if button := w.dateButtons[group]; button != nil {
		button.GrabFocus()
	}
}

// A hidden window stays alive in background mode. Update when the local
// day/time zone changes and stop the timer when the window is destroyed.
func (w *Window) initDateGroups() {
	w.dateWeekStart = time.Monday
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "locale", "-k", "LC_TIME").Output(); err == nil {
		w.dateWeekStart = maildate.FirstWeekday(string(out))
	}
	day := time.Now().Format("2006-01-02 MST -0700")
	timer := glib.TimeoutAdd(30_000, func() bool {
		now := time.Now().Format("2006-01-02 MST -0700")
		if now != day {
			day = now
			if w.usesDateGroups() {
				w.syncDateRows(false)
			}
		}
		return true
	})
	w.ConnectDestroy(func() { glib.SourceRemove(timer) })
}
