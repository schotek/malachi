// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package toolbar is the user's arrangement of the main window's toolbars
// (docs/plans/toolbar-customization.md, decisions T1–T11): the catalog of
// the items each toolbar can hold (Catalog), the default arrangement
// (Default), the rules of the sections an item may stand in (CanInsert),
// how an arrangement read from the settings becomes a valid one
// (Normalize), what the toolbar shows of it for the features at hand
// (Present), which actions the More Actions menu adds because the toolbar
// does not have them (MissingActions), and where ⌘F puts a search field
// the user took off the toolbar (SearchIndex). The arrangement is stored
// as the token list Tokens returns in the key Key names, an empty list
// meaning the default. Pure logic, no widgets: macOS edits it with
// AppKit's customization palette (MalachiCore/Toolbar), GTK and Windows
// will with an editor in Preferences over the same catalog and keys
// (ui/internal/toolbaredit). A client's differences, the items it lacks
// and its own defaults, are its Variant (GTK, Windows; variant.go, the
// functions ending in For, StoredFor merging an edit into the shared
// key); Fit picks what a too narrow strip leaves out; text.go has the
// editor's texts. The functions without a variant are macOS's.
//
// The item ids are the macOS toolbar identifiers (MainToolbar.ID,
// BoardToolbar.ID, CalendarToolbar.ID), so nothing is renamed; the
// toolbar's own items are neutral tokens (Space, FlexibleSpace,
// ToggleSidebar) and a section's separator is a token starting with "|".
// The toolbars' own labels are the clients'; ItemLabel names the items in the
// GTK and Windows editors.
package toolbar

import "strings"

// Bar is one of the main window's customizable toolbars.
type Bar string

const (
	// BarMail is the toolbar of the Mail mode.
	BarMail Bar = "mail"
	// BarBoardList is the Board's toolbar in the List style.
	BarBoardList Bar = "board-list"
	// BarBoardFlow is the Board's toolbar in the Columns and Today styles.
	BarBoardFlow Bar = "board-flow"
	// BarCalendar is the toolbar of the Calendar mode.
	BarCalendar Bar = "calendar"
)

// Bars are the customizable toolbars in the order of the modes.
var Bars = []Bar{BarMail, BarBoardList, BarBoardFlow, BarCalendar}

// Key is the settings key (gschema, type as) that stores bar's
// arrangement.
func Key(bar Bar) string { return "toolbar-" + string(bar) }

// DisplayModeKey is the settings key of the display mode, one for every
// toolbar (O4).
const DisplayModeKey = "toolbar-display-mode"

// DisplayMode is what the toolbar's items show (the gschema enum
// ToolbarDisplayMode).
type DisplayMode string

const (
	DisplayIcon         DisplayMode = "icon"
	DisplayIconAndLabel DisplayMode = "icon-and-label"
	DisplayLabel        DisplayMode = "label"
)

// ParseDisplayMode reads the key's nick; anything else is DisplayIcon.
func ParseDisplayMode(s string) DisplayMode {
	switch m := DisplayMode(s); m {
	case DisplayIcon, DisplayIconAndLabel, DisplayLabel:
		return m
	}
	return DisplayIcon
}

// ID names an item of a toolbar.
type ID string

// The toolbar's own items. A space may stand any number of times.
const (
	Space         ID = "space"
	FlexibleSpace ID = "flexibleSpace"
	ToggleSidebar ID = "toggleSidebar"
)

// The items of the Mail toolbar.
const (
	Mode         ID = "mode"
	NewMessage   ID = "newMessage"
	Contacts     ID = "contacts"
	Filter       ID = "filter"
	Refresh      ID = "refresh"
	Reply        ID = "reply"
	ReplyAll     ID = "replyAll"
	Forward      ID = "forward"
	Archive      ID = "archive"
	Trash        ID = "trash"
	CRM          ID = "crm"
	Star         ID = "star"
	Assistant    ID = "assistant"
	MoreActions  ID = "moreActions"
	Search       ID = "search"
	MarkRead     ID = "markRead"
	Junk         ID = "junk"
	MoveTo       ID = "moveTo"
	Print        ID = "print"
	Undo         ID = "undo"
	LoadPictures ID = "loadPictures"
)

// The items of the Board's toolbars.
const (
	BoardStyle   ID = "boardStyle"
	BoardAccount ID = "boardAccount"
	BoardTriage  ID = "boardTriage"
	BoardDone    ID = "boardDone"
	BoardRemind  ID = "boardRemind"
	BoardArchive ID = "boardArchive"
	BoardTrash   ID = "boardTrash"
	BoardReply   ID = "boardReply"
)

// The items of the Calendar's toolbar.
const (
	CalendarToday     ID = "calendarToday"
	CalendarStep      ID = "calendarStep"
	CalendarView      ID = "calendarView"
	CalendarSearch    ID = "calendarSearch"
	CalendarNewEvent  ID = "calendarNewEvent"
	CalendarAssistant ID = "calendarAssistant"
)

// Section is a part of a toolbar between two separators; a toolbar
// without separators is one section, SectionMain.
type Section string

const (
	SectionSidebar Section = "sidebar"
	SectionList    Section = "list"
	SectionMessage Section = "message"
	SectionDetail  Section = "detail"
	SectionMain    Section = "main"
)

// Separator is the token of the separator that starts s; the first
// section of a toolbar has none.
func Separator(s Section) ID { return ID("|" + string(s)) }

// IsSeparator reports whether id is a section's separator.
func IsSeparator(id ID) bool { return strings.HasPrefix(string(id), "|") }

// IsSpace reports whether id is a space, which may stand anywhere and
// any number of times.
func IsSpace(id ID) bool { return id == Space || id == FlexibleSpace }

// Feature is what a conditional item needs to be shown (T7).
type Feature string

const (
	// FeatureCRM: an eWay-CRM account exists.
	FeatureCRM Feature = "crm"
	// FeatureAssistant: the Assistant menu is on (assistant-menu and
	// the registration in Claude).
	FeatureAssistant Feature = "assistant"
	// FeatureTriage: the board's triage can run (the In App target).
	FeatureTriage Feature = "triage"
	// FeaturePrint: printing exists in this client.
	FeaturePrint Feature = "print"
)

// Priorities of an item when the toolbar runs out of room, as AppKit's
// NSToolbarItem.VisibilityPriority: higher stays longer (T5).
const (
	PriorityUser     = 2000
	PriorityHigh     = 1000
	PriorityStandard = 0
	PrioritySearch   = -500
	PriorityLow      = -1000
)

// Item is an entry of a toolbar's catalog.
type Item struct {
	ID ID
	// Sections are where the item may stand; the first is its home,
	// where Normalize puts it when it stands elsewhere.
	Sections []Section
	// Fixed: the user can neither remove nor move it (T4).
	Fixed bool
	// Feature is what the item needs to be shown, "" for nothing.
	Feature Feature
	// InMore: an action the More Actions menu offers while the toolbar
	// does not show it (T8).
	InMore bool
	// Priority is its visibility priority.
	Priority int
}

// Home is the section Normalize puts the item in.
func (it Item) Home() Section { return it.Sections[0] }

func (it Item) allows(s Section) bool {
	for _, x := range it.Sections {
		if x == s {
			return true
		}
	}
	return false
}

var (
	mailNav     = []Section{SectionList, SectionSidebar}
	mailMessage = []Section{SectionMessage}
	boardNav    = []Section{SectionList, SectionSidebar}
	calNav      = []Section{SectionMain, SectionSidebar}
)

// msg is a message action of the Mail toolbar.
func msg(id ID, inMore bool, priority int, feature Feature) Item {
	return Item{ID: id, Sections: mailMessage, InMore: inMore, Priority: priority, Feature: feature}
}

var catalogs = map[Bar][]Item{
	BarMail: {
		{ID: Mode, Sections: []Section{SectionSidebar}, Fixed: true, Priority: PriorityUser},
		{ID: ToggleSidebar, Sections: mailNav, Priority: PriorityHigh},
		{ID: NewMessage, Sections: mailNav},
		{ID: Contacts, Sections: mailNav, Priority: PriorityLow},
		{ID: Filter, Sections: mailNav},
		{ID: Refresh, Sections: mailNav},
		msg(Reply, true, PriorityStandard, ""),
		msg(ReplyAll, true, PriorityStandard, ""),
		msg(Forward, true, PriorityStandard, ""),
		msg(Archive, true, PriorityStandard, ""),
		msg(Trash, true, PriorityStandard, ""),
		msg(CRM, true, PriorityStandard, FeatureCRM),
		msg(Star, true, PriorityStandard, ""),
		msg(Assistant, false, PriorityStandard, FeatureAssistant),
		msg(MoreActions, false, PriorityHigh, ""),
		msg(Search, false, PriorityStandard, ""),
		// The palette's items (T10) are in the menus already, and an
		// untouched toolbar keeps its More Actions as it was: not InMore.
		msg(MarkRead, false, PriorityLow, ""),
		msg(Junk, false, PriorityLow, ""),
		msg(MoveTo, false, PriorityLow, ""),
		msg(Print, false, PriorityLow, FeaturePrint),
		msg(Undo, false, PriorityLow, ""),
		msg(LoadPictures, false, PriorityLow, ""),
	},
	BarBoardList: {
		{ID: Mode, Sections: []Section{SectionSidebar}, Fixed: true, Priority: PriorityUser},
		{ID: ToggleSidebar, Sections: boardNav, Priority: PriorityHigh},
		{ID: BoardStyle, Sections: boardNav, Priority: PriorityHigh},
		{ID: Contacts, Sections: boardNav, Priority: PriorityLow},
		{ID: BoardAccount, Sections: boardNav},
		{ID: BoardTriage, Sections: boardNav, Feature: FeatureTriage, Priority: PriorityLow},
		{ID: BoardDone, Sections: []Section{SectionDetail}, Priority: PriorityHigh},
		{ID: BoardRemind, Sections: []Section{SectionDetail}, Priority: PriorityLow},
		{ID: BoardArchive, Sections: []Section{SectionDetail}, Priority: PriorityLow},
		{ID: BoardTrash, Sections: []Section{SectionDetail}, Priority: PriorityLow},
		{ID: BoardReply, Sections: []Section{SectionDetail}},
	},
	BarBoardFlow: {
		{ID: Mode, Sections: []Section{SectionMain}, Fixed: true, Priority: PriorityUser},
		{ID: BoardStyle, Sections: []Section{SectionMain}, Priority: PriorityHigh},
		{ID: Contacts, Sections: []Section{SectionMain}, Priority: PriorityLow},
		{ID: BoardAccount, Sections: []Section{SectionMain}},
		{ID: BoardTriage, Sections: []Section{SectionMain}, Feature: FeatureTriage, Priority: PriorityLow},
	},
	BarCalendar: {
		{ID: Mode, Sections: []Section{SectionSidebar}, Fixed: true, Priority: PriorityUser},
		{ID: ToggleSidebar, Sections: calNav, Priority: PriorityHigh},
		{ID: CalendarToday, Sections: calNav, Priority: PriorityHigh},
		{ID: CalendarStep, Sections: calNav, Priority: PriorityHigh},
		{ID: CalendarView, Sections: calNav},
		{ID: CalendarSearch, Sections: calNav, Priority: PrioritySearch},
		{ID: Contacts, Sections: calNav},
		{ID: CalendarNewEvent, Sections: calNav, Priority: PriorityHigh},
		{ID: CalendarAssistant, Sections: calNav, Feature: FeatureAssistant, Priority: PriorityLow},
		{ID: Print, Sections: calNav, Feature: FeaturePrint, Priority: PriorityLow},
	},
}

// The sections of each toolbar in order; every one but the first starts
// with its separator.
var sections = map[Bar][]Section{
	BarMail:      {SectionSidebar, SectionList, SectionMessage},
	BarBoardList: {SectionSidebar, SectionList, SectionDetail},
	BarBoardFlow: {SectionMain},
	BarCalendar:  {SectionSidebar, SectionMain},
}

// The default arrangements, the toolbars as they were before
// customization (MainToolbar.defaultItems, BoardToolbar.listItems and
// flowItems, CalendarToolbar.items); the new items of T10 are in the
// palette only, so an untouched toolbar does not change.
var defaults = map[Bar][]ID{
	BarMail: {
		Mode, FlexibleSpace,
		Separator(SectionList), ToggleSidebar, NewMessage, Contacts, Filter, Refresh, FlexibleSpace,
		Separator(SectionMessage), Reply, ReplyAll, Forward, FlexibleSpace,
		Archive, Trash, Space, CRM, Space, Star, Assistant, MoreActions, Search,
	},
	BarBoardList: {
		Mode, FlexibleSpace,
		Separator(SectionList), ToggleSidebar, BoardStyle, Contacts, FlexibleSpace, BoardAccount, BoardTriage,
		Separator(SectionDetail), BoardDone, BoardRemind, BoardArchive, BoardTrash, FlexibleSpace, BoardReply,
	},
	BarBoardFlow: {
		Mode, BoardStyle, Contacts, FlexibleSpace, BoardAccount, BoardTriage,
	},
	BarCalendar: {
		Mode, FlexibleSpace,
		Separator(SectionMain), ToggleSidebar, CalendarToday, CalendarStep, CalendarView, FlexibleSpace,
		CalendarSearch, Contacts, CalendarNewEvent, CalendarAssistant,
	},
}

// Catalog is every item bar can hold, spaces and separators aside, in
// the palette's order.
func Catalog(bar Bar) []Item { return append([]Item(nil), catalogs[bar]...) }

// Sections are bar's sections in order.
func Sections(bar Bar) []Section { return append([]Section(nil), sections[bar]...) }

// Lookup is id's entry in bar's catalog.
func Lookup(bar Bar, id ID) (Item, bool) {
	for _, it := range catalogs[bar] {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// Layout is an arrangement of a toolbar: item ids in order with the
// separators of its sections between them.
type Layout []ID

// Default is bar's default arrangement.
func Default(bar Bar) Layout { return append(Layout(nil), defaults[bar]...) }

// Tokens is the layout as the settings key stores it.
func (l Layout) Tokens() []string {
	out := make([]string, len(l))
	for i, id := range l {
		out[i] = string(id)
	}
	return out
}

// Equal reports whether two layouts are the same.
func (l Layout) Equal(o Layout) bool {
	if len(l) != len(o) {
		return false
	}
	for i := range l {
		if l[i] != o[i] {
			return false
		}
	}
	return true
}

// Stored is what the settings key keeps for l: nothing for bar's
// default, so an untouched toolbar follows the default of later
// versions.
func Stored(bar Bar, l Layout) []string {
	if Normalize(bar, l.Tokens()).Equal(Default(bar)) {
		return []string{}
	}
	return Normalize(bar, l.Tokens()).Tokens()
}

// MaxTokens bounds what Normalize reads of a stored list.
const MaxTokens = 128

// Normalize makes a valid arrangement of bar from a stored token list:
// an empty list is the default; unknown ids (of an older or newer
// version), a second copy of an item and anything past MaxTokens go;
// separators stand in their order, each once, the missing ones where
// they belong; an item in a section it may not stand in goes to the end
// of its home section; a fixed item stands at the start of its home
// section in the default's order. It is NormalizeFor with the zero
// Variant (macOS).
func Normalize(bar Bar, stored []string) Layout { return NormalizeFor(Variant{}, bar, stored) }

// SectionAt is the section of the item at index i of l (a separator
// belongs to the section it starts).
func SectionAt(bar Bar, l Layout, i int) Section {
	secs := sections[bar]
	if len(secs) == 0 {
		return SectionMain
	}
	s := secs[0]
	for j := 0; j <= i && j < len(l); j++ {
		if IsSeparator(l[j]) {
			s = Section(strings.TrimPrefix(string(l[j]), "|"))
		}
	}
	return s
}

// CanInsert reports whether the user may put id into section s of bar
// (macOS: toolbar(_:itemIdentifier:canBeInsertedAt:)). Spaces go
// anywhere; separators and fixed items never move.
func CanInsert(bar Bar, id ID, s Section) bool { return CanInsertFor(Variant{}, bar, id, s) }

// Immovable are the items of bar the user can neither move nor remove,
// its separators included (macOS: toolbarImmovableItemIdentifiers).
func Immovable(bar Bar) []ID { return ImmovableFor(Variant{}, bar) }

// Features are what the window has at the moment.
type Features struct {
	// Have holds the features that exist (T7).
	Have map[Feature]bool
	// HiddenSeparators are separators the toolbar leaves out while their
	// section's items still show (Mail: the list pane collapsed, its
	// items merge into the message section).
	HiddenSeparators map[ID]bool
	// HiddenSections are sections left out with their items (the Board
	// List's detail while no case is shown).
	HiddenSections map[Section]bool
}

// Allowed is what the palette offers: the catalog without the items
// whose feature is missing, in the catalog's order (spaces are the
// client's own).
func Allowed(bar Bar, f Features) []ID { return AllowedFor(Variant{}, bar, f) }

// Shown is an entry of what the toolbar holds.
type Shown struct {
	ID ID
	// Hidden: the item keeps its place but is not shown, its feature
	// missing (T7).
	Hidden bool
}

// Present is what the toolbar holds for l: the hidden sections with
// their separators and the hidden separators left out, an item whose
// feature is missing kept in its place and hidden.
func Present(bar Bar, l Layout, f Features) []Shown { return PresentFor(Variant{}, bar, l, f) }

// MissingActions are the actions of bar the More Actions menu adds
// because l does not show them, in the catalog's order (T8); an action
// whose feature is missing is not one. The client adds the ones its
// toolbar moved to its overflow.
func MissingActions(bar Bar, l Layout, f Features) []ID {
	return MissingActionsFor(Variant{}, bar, l, f)
}

// SearchItem is bar's search field, "" for a toolbar without one.
func SearchItem(bar Bar) ID {
	switch bar {
	case BarMail:
		return Search
	case BarCalendar:
		return CalendarSearch
	}
	return ""
}

// SearchIndex is where ⌘F puts bar's search field into l for the time
// of a search while the user took it off the toolbar (O5): the end of
// the search's home section; -1 while l has it or bar has none. The
// stored arrangement does not change.
func SearchIndex(bar Bar, l Layout) int {
	id := SearchItem(bar)
	if id == "" {
		return -1
	}
	for _, x := range l {
		if x == id {
			return -1
		}
	}
	it, _ := Lookup(bar, id)
	home := it.Home()
	// Before the separator of the first section after home, else the end.
	for i := range l {
		if IsSeparator(l[i]) && sectionAfter(bar, home, SectionAt(bar, l, i)) {
			return i
		}
	}
	return len(l)
}

// sectionAfter reports whether b comes after a in bar's order.
func sectionAfter(bar Bar, a, b Section) bool {
	ia, ib := -1, -1
	for i, s := range sections[bar] {
		if s == a {
			ia = i
		}
		if s == b {
			ib = i
		}
	}
	return ib > ia
}
