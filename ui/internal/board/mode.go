// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

// The main window's two modes: Mail (the folders, the list, the reader and
// the assistant panel, as always) and Board (the page of the board:
// Controller, View). The window shows one of them at a time under the same
// status bar; the toolbar's switch and the View menu change it. This is the
// part that needs no widgets: which actions a mode allows, which requests
// bring the mail back, when the user counts as looking at the folder, and
// which style the board takes as it shows.
//
// The macOS client leads (MalachiCore/Board/Board.swift, Board.Style in
// BoardView.swift); this is its port. The texts are Mail and BoardName.

// Mode is what the main window shows (Board.Mode). Its value is the index
// of the toolbar switch's segment.
type Mode int

// The modes.
const (
	ModeMail Mode = iota
	ModeBoard
)

// Modes lists every mode in the switch's order.
var Modes = []Mode{ModeMail, ModeBoard}

// InitialMode is the mode a new main window starts in. The mode is not
// remembered.
const InitialMode = ModeMail

// Command is one of the window's actions, as far as a mode cares
// (Board.Command).
type Command int

// The commands.
const (
	// CommandSwitchMode switches between the modes.
	CommandSwitchMode Command = iota
	// CommandNewMessage is New Message.
	CommandNewMessage
	// CommandCheckForNewMail is Check for New Mail.
	CommandCheckForNewMail
	// CommandMailView is a view of the mail: the sidebar's, the message
	// list's and the assistant panel's toggles, the filter, Find…, Load
	// Images, Always Load Images From This Sender.
	CommandMailView
	// CommandMessageAction is everything that acts on the selected message
	// or conversation (reply, flags, moves, the assistant's message
	// actions, Summarize Unread in This Folder).
	CommandMessageAction
	// CommandBoardView is a view of the board: its style (List, Columns,
	// Today).
	CommandBoardView
)

// Commands lists every command.
var Commands = []Command{
	CommandSwitchMode, CommandNewMessage, CommandCheckForNewMail, CommandMailView, CommandMessageAction,
	CommandBoardView,
}

// Allows reports whether command c may run in mode m. Mail allows
// everything but the board's views; Board only what does not need the
// folders, the list and the reader: the list keeps its selection while
// hidden, and no key may act on a message the user cannot see.
func Allows(c Command, m Mode) bool {
	if m == ModeBoard {
		switch c {
		case CommandSwitchMode, CommandNewMessage, CommandCheckForNewMail, CommandBoardView:
			return true
		}
		return false
	}
	return c != CommandBoardView
}

// Request is a request from elsewhere in the application that may reach
// the main window in either mode (Board.Request).
type Request int

// The requests.
const (
	// RequestShowOutbox is the status bar's Outbox: the outbox folder in
	// the list.
	RequestShowOutbox Request = iota
	// RequestRevealAssistant unfolds the assistant panel (the Assistant
	// button, a menu run in the panel).
	RequestRevealAssistant
	// RequestOpenMessageWindow opens a message in its own window (a
	// notification's click).
	RequestOpenMessageWindow
	// RequestCompose opens a compose window (New Message, mailto:).
	RequestCompose
)

// Requests lists every request.
var Requests = []Request{RequestShowOutbox, RequestRevealAssistant, RequestOpenMessageWindow, RequestCompose}

// ModeFor is the mode the main window is in after request r: what shows
// the mail in the main window brings Mail back, what opens a window of its
// own leaves the mode as it is.
func ModeFor(r Request, current Mode) Mode {
	switch r {
	case RequestShowOutbox, RequestRevealAssistant:
		return ModeMail
	}
	return current
}

// ViewsMail reports whether the user looks at the selected folder: the main
// window shows the mail and is the active window. An active window showing
// the board does not count, so the folder's desktop notifications stay.
func ViewsMail(m Mode, windowIsKey bool) bool {
	return m == ModeMail && windowIsKey
}

// Styles lists every style in the switch's order (Board.Style.allCases).
var Styles = []Style{StyleList, StyleColumns, StyleToday}

// Nick is the style's value in the settings (the key board-default-style:
// "list", "columns", "today"); the Style's number is an index and is never
// stored.
func (s Style) Nick() string {
	switch s {
	case StyleColumns:
		return "columns"
	case StyleToday:
		return "today"
	}
	return "list"
}

// ParseStyle is the style a stored nick names; an unknown or empty one is
// the List.
func ParseStyle(nick string) Style {
	for _, s := range Styles {
		if s.Nick() == nick {
			return s
		}
	}
	return StyleList
}

// StyleOnShow is the style the board takes as it shows: the default (from
// the settings) the first time in a run, else the one it has, which is the
// user's last choice. A default changed after the first show waits for the
// next launch: the style never changes under the user.
func StyleOnShow(current, defaultStyle Style, firstShow bool) Style {
	if firstShow {
		return defaultStyle
	}
	return current
}
