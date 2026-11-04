// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

import (
	"fmt"
	"strings"
)

// Translator translates a msgid of the malachi domain: T a plain one, N
// a plural form for n, C one disambiguated by a context. GTK passes an
// adapter over i18n.T, i18n.N and i18n.C (i18n.Tr); Windows uses the same
// msgids; the tests pass one that returns the msgid. macOS keeps the
// labels of its own toolbar items.
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// GroupTitle is the title of the Toolbar group in Preferences → General
// (T13).
func GroupTitle(tr Translator) string {
	// TRANSLATORS: a group in Preferences → General with the toolbar's display mode and its editor.
	return tr.T("Toolbar")
}

// CustomizeTitle is the button of the Toolbar group that opens the
// editor, and the item of a toolbar's context menu that does (T15).
func CustomizeTitle(tr Translator) string {
	// TRANSLATORS: a button in Preferences and a context menu item of a toolbar; it opens the toolbar editor.
	return tr.T("Customize Toolbar…")
}

// DialogTitle is the title of the editor's dialog.
func DialogTitle(tr Translator) string {
	// TRANSLATORS: the title of the dialog where the user arranges a toolbar's items.
	return tr.T("Customize Toolbar")
}

// DisplayModeTitle is the title of the row with the display mode.
func DisplayModeTitle(tr Translator) string {
	// TRANSLATORS: a row in Preferences: whether the toolbar's buttons show icons, text or both.
	return tr.T("Toolbar Display")
}

// DisplayModes are the display mode's choices in the order the row
// offers them.
var DisplayModes = []DisplayMode{DisplayIcon, DisplayIconAndLabel, DisplayLabel}

// DisplayModeLabel is the choice m of the display mode row.
func DisplayModeLabel(tr Translator, m DisplayMode) string {
	switch m {
	case DisplayIconAndLabel:
		// TRANSLATORS: a choice of the Toolbar Display row: buttons with an icon and a label.
		return tr.T("Icons and Text")
	case DisplayLabel:
		// TRANSLATORS: a choice of the Toolbar Display row: buttons with a label and no icon.
		return tr.T("Text Only")
	}
	// TRANSLATORS: a choice of the Toolbar Display row: buttons with an icon and no label.
	return tr.T("Icons Only")
}

// BarTitle is bar's name in the editor's bar chooser.
func BarTitle(tr Translator, bar Bar) string {
	switch bar {
	case BarMail:
		return tr.T("Mail")
	case BarBoardList:
		// TRANSLATORS: the toolbar of the Board in its List style, in the toolbar editor.
		return tr.T("Board – List")
	case BarBoardFlow:
		// TRANSLATORS: the toolbar of the Board in its Columns and Today styles, in the toolbar editor.
		return tr.T("Board – Columns and Today")
	case BarCalendar:
		return tr.T("Calendar")
	}
	return string(bar)
}

// SectionTitle is the heading of section s of bar in the editor; a bar of
// one section is named as the bar.
func SectionTitle(tr Translator, bar Bar, s Section) string {
	switch s {
	case SectionSidebar:
		if bar == BarMail {
			return tr.T("Folders")
		}
		return sidebarText(tr)
	case SectionList:
		if bar == BarMail {
			return tr.T("Message List")
		}
		// TRANSLATORS: the part of the Board's toolbar over its list of cases, in the toolbar editor.
		return tr.T("Cases")
	case SectionMessage:
		return tr.T("Message")
	case SectionDetail:
		// TRANSLATORS: the part of the Board's toolbar over the shown case, in the toolbar editor.
		return tr.T("Case Detail")
	case SectionMain:
		if bar == BarCalendar {
			return tr.T("Calendar")
		}
		return tr.T("Board")
	}
	return string(s)
}

func sidebarText(tr Translator) string {
	// TRANSLATORS: in the toolbar editor, the button that shows or hides the sidebar and the part of a toolbar over the sidebar.
	return tr.T("Sidebar")
}

// ItemLabel is id's label in the editor (and of a button in the Icons
// and Text and Text Only display modes).
func ItemLabel(tr Translator, bar Bar, id ID) string {
	switch id {
	case Space:
		// TRANSLATORS: a fixed gap between toolbar buttons, in the toolbar editor.
		return tr.T("Space")
	case FlexibleSpace:
		// TRANSLATORS: a gap that takes the toolbar's free room, in the toolbar editor.
		return tr.T("Flexible Space")
	case ToggleSidebar:
		return sidebarText(tr)
	case Mode:
		// TRANSLATORS: the toolbar's switch between Mail, Board and Calendar, in the toolbar editor.
		return tr.T("Mode Switch")
	case NewMessage:
		return tr.T("New Message")
	case Contacts:
		return tr.T("Contacts")
	case Filter:
		// TRANSLATORS: the toolbar item that filters the message list (All, Unread, Flagged), in the toolbar editor.
		return tr.T("Filter")
	case Refresh:
		return tr.T("Check for New Mail")
	case Reply, BoardReply:
		return tr.T("Reply")
	case ReplyAll:
		return tr.T("Reply All")
	case Forward:
		return tr.T("Forward")
	case Archive, BoardArchive:
		return tr.T("Archive")
	case Trash:
		return tr.T("Move to Trash")
	case CRM:
		return tr.T("Save to eWay-CRM…")
	case Star:
		return tr.T("Star")
	case Assistant, CalendarAssistant:
		return tr.T("Assistant")
	case MoreActions:
		return tr.T("More Actions")
	case Search:
		return tr.T("Search")
	case MarkRead:
		// One toggling button (O3), named as its first state.
		return StripMnemonic(tr.T("Mark as _Read"))
	case Junk:
		return tr.T("Mark as Junk")
	case MoveTo:
		return tr.T("Move To")
	case Print:
		return tr.T("Print")
	case Undo:
		return tr.T("Undo")
	case LoadPictures:
		return StripMnemonic(tr.T("Load _Images"))
	case BoardStyle:
		// TRANSLATORS: the Board's switch between its List, Columns and Today styles, in the toolbar editor.
		return tr.T("Board Style")
	case BoardAccount:
		return tr.C("account filter", "All Accounts")
	case BoardTriage:
		return tr.T("✦ Triage")
	case BoardDone:
		return tr.T("Done")
	case BoardRemind:
		return tr.T("Remind…")
	case BoardTrash:
		return tr.T("Delete")
	case CalendarToday:
		return tr.T("Today")
	case CalendarStep:
		// TRANSLATORS: the Calendar's pair of buttons that step to the previous and next day, week or month, in the toolbar editor.
		return tr.T("Previous and Next")
	case CalendarView:
		// TRANSLATORS: the Calendar's switch between Day, Week, Month and Agenda, in the toolbar editor.
		return tr.T("Calendar View")
	case CalendarSearch:
		return tr.T("Search Events")
	case CalendarNewEvent:
		return tr.T("New Event")
	}
	return string(id)
}

// UnavailableSubtitle is the subtitle of an item whose feature is
// missing (T7): it keeps its place and shows once the feature is back.
func UnavailableSubtitle(tr Translator) string {
	// TRANSLATORS: the subtitle of a toolbar item in the editor whose feature is off at the moment (e.g. no eWay-CRM account).
	return tr.T("Hidden while unavailable")
}

// AddItemTitle is the editor's button that offers the items to add.
func AddItemTitle(tr Translator) string {
	// TRANSLATORS: a button of the toolbar editor that opens the list of items to add.
	return tr.T("Add Item…")
}

// RemoveTitle is the editor's button that takes an item off the toolbar.
func RemoveTitle(tr Translator) string { return tr.T("Remove") }

// RestoreTitle is the editor's Restore Defaults button and the
// confirmation's button.
func RestoreTitle(tr Translator) string {
	// TRANSLATORS: a button of the toolbar editor that gives the chosen toolbar its default items back.
	return tr.T("Restore Defaults")
}

// RestoreQuestion is the confirmation's heading.
func RestoreQuestion(tr Translator) string {
	// TRANSLATORS: the question before the toolbar editor restores the default toolbar.
	return tr.T("Restore the Default Toolbar?")
}

// RestoreBody is the confirmation's text for bar.
func RestoreBody(tr Translator, bar Bar) string {
	// TRANSLATORS: %s is the toolbar's name, e.g. "Mail" or "Board – List".
	return fmt.Sprintf(tr.T("Your changes to the “%s” toolbar will be lost."), BarTitle(tr, bar))
}

// RestoreCancel is the confirmation's Cancel.
func RestoreCancel(tr Translator) string { return tr.T("Cancel") }

// StripMnemonic takes the mnemonic underscores out of a menu label
// ("__" stands for "_").
func StripMnemonic(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] == '_' {
			if i+1 < len(r) && r[i+1] == '_' {
				b.WriteRune('_')
				i++
			}
			continue
		}
		b.WriteRune(r[i])
	}
	return b.String()
}
