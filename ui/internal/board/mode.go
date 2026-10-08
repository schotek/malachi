// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "time"

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

// InitialMode is the mode a new main window starts in when nothing else
// says (the default of the key board-start-mode); StartMode decides.
const InitialMode = ModeMail

// Nick is the mode's value in the settings (the key board-last-mode:
// "mail", "board").
func (m Mode) Nick() string {
	if m == ModeBoard {
		return "board"
	}
	return "mail"
}

// ParseMode is the mode a stored nick names; false for an unknown one.
func ParseMode(nick string) (Mode, bool) {
	switch nick {
	case "mail":
		return ModeMail, true
	case "board":
		return ModeBoard, true
	}
	return ModeMail, false
}

// StartChoice is a value of Open at Launch (the key board-start-mode).
type StartChoice int

// The choices.
const (
	StartMail StartChoice = iota
	StartBoard
	// StartLast opens the mode shown last (the key board-last-mode).
	StartLast
)

// StartModes lists the choices in Settings' order.
var StartModes = []StartChoice{StartMail, StartBoard, StartLast}

// NickLast is the stored value of a choice that takes what the user had
// last (board-start-mode, board-default-style).
const NickLast = "last"

// Nick is the choice's value in the settings: "mail", "board", "last".
func (s StartChoice) Nick() string {
	switch s {
	case StartBoard:
		return "board"
	case StartLast:
		return NickLast
	}
	return "mail"
}

// ParseStartChoice is the choice a stored nick names; an unknown or empty
// one is Mail.
func ParseStartChoice(nick string) StartChoice {
	for _, s := range StartModes {
		if s.Nick() == nick {
			return s
		}
	}
	return StartMail
}

// StartMode is the mode a new main window opens in: startMode is the key
// board-start-mode, lastMode the key board-last-mode (written whenever the
// mode switches). With the board turned off (boardEnabled false) always
// Mail.
func StartMode(startMode, lastMode string, boardEnabled bool) Mode {
	if !boardEnabled {
		return ModeMail
	}
	switch ParseStartChoice(startMode) {
	case StartBoard:
		return ModeBoard
	case StartLast:
		m, _ := ParseMode(lastMode)
		return m
	}
	return ModeMail
}

// StartWait is how long a new main window waits for the daemon's board
// preferences before it settles in Mail for good (StartDecision).
const StartWait = 5 * time.Second

// StartDecision decides the mode a new main window opens in while the
// daemon's board preferences may still be on their way. start is Open at
// Launch (board-start-mode), lastMode the mode shown last
// (board-last-mode); prefsKnown whether the daemon's preferences arrived
// and enabled their Show the Board; userSwitched whether the user switched
// the mode, userInteracted whether the user acted in Mail (a click or a
// key there), waited the time since the window opened.
//
// decided is false while the window should keep waiting; until then it
// shows Mail, writes nothing to board-last-mode and may still move to the
// Board. Once decided the window applies mode if it still shows Mail
// (ModeBoard only from the preferences) and never moves on its own again:
//
//   - a start that is Mail whatever the preferences say (Mail, or Last
//     Used with Mail last): Mail at once;
//   - the user switched: settled, the window keeps what the user chose
//     (mode is Mail: the start moves nothing);
//   - the user acted in Mail: Mail, so the reader never jumps away from
//     what the user is doing;
//   - the preferences arrived: StartMode with their Show the Board;
//   - StartWait passed without them: Mail, since whether the board is on
//     is unknown, and no later jump when they come.
func StartDecision(start StartChoice, lastMode Mode, prefsKnown, enabled, userSwitched, userInteracted bool, waited time.Duration) (mode Mode, decided bool) {
	wanted := StartMode(start.Nick(), lastMode.Nick(), true)
	switch {
	case wanted == ModeMail:
		return ModeMail, true
	case userSwitched, userInteracted:
		return ModeMail, true
	case prefsKnown:
		return StartMode(start.Nick(), lastMode.Nick(), enabled), true
	case waited >= StartWait:
		return ModeMail, true
	}
	return ModeMail, false
}

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

// ParseStyle is the style a stored nick names (the key board-last-style);
// an unknown or empty one, and "last", is the List.
func ParseStyle(nick string) Style {
	for _, s := range Styles {
		if s.Nick() == nick {
			return s
		}
	}
	return StyleList
}

// DefaultStyle is a value of Board View (the key board-default-style):
// the style the board shows in, or the one used last.
type DefaultStyle struct {
	// Last: the style the user had last (board-last-style); Style is then
	// unused.
	Last  bool
	Style Style
}

// DefaultStyles lists Board View's choices in Settings' order: Last Used,
// List, Columns, Today.
var DefaultStyles = []DefaultStyle{
	{Last: true}, {Style: StyleList}, {Style: StyleColumns}, {Style: StyleToday},
}

// Nick is the choice's value in the settings: "last", "list", "columns",
// "today".
func (d DefaultStyle) Nick() string {
	if d.Last {
		return NickLast
	}
	return d.Style.Nick()
}

// ParseDefaultStyle is the choice a stored nick of board-default-style
// names: "last" is Last Used, a style's nick that style, anything else
// (empty, unknown) Last Used, the key's default.
func ParseDefaultStyle(nick string) DefaultStyle {
	for _, s := range Styles {
		if s.Nick() == nick {
			return DefaultStyle{Style: s}
		}
	}
	return DefaultStyle{Last: true}
}

// StyleOnShow is the style the board takes as it shows. Once the user
// picked a style in this run (pickedThisRun) the board keeps it (current);
// before that it takes Board View: the style used last (lastStyle, the key
// board-last-style) for Last Used, else the chosen one, so a change of
// Board View applies the next time the board shows unless the user already
// picked a style. The style never changes while the board shows.
func StyleOnShow(defaultStyle DefaultStyle, lastStyle, current Style, pickedThisRun bool) Style {
	if pickedThisRun {
		return current
	}
	if defaultStyle.Last {
		return lastStyle
	}
	return defaultStyle.Style
}

// FilterOnShow is the account filter the board takes as it shows: the one
// saved (the key board-account-filter) while that account is still among
// accounts, else every account ("").
func FilterOnShow(saved string, accounts []AccountInfo) string {
	if saved == "" {
		return ""
	}
	for _, a := range accounts {
		if string(a.ID) == saved {
			return saved
		}
	}
	return ""
}
