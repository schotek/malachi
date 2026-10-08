// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

// The board's keys and its Escape, the part that needs no widgets: which
// shortcuts there are, with their texts for the shortcuts window and the
// menus, and where Escape goes. Every client binds the same keys: Primary
// is Ctrl on Linux and Windows, ⌘ on macOS. The single keys work only
// while the board shows and the keyboard is not in a text field (a reply
// editor, a recipient field, the instruction of Suggest Reply), so typing
// never acts on a case.

// KeyAction is what a key of the board does.
type KeyAction int

// The actions.
const (
	// KeyShowMail switches the main window to Mail (Primary+1).
	KeyShowMail KeyAction = iota
	// KeyShowBoard switches it to the Board (Primary+2).
	KeyShowBoard
	// KeyArchive is Archive on the selected case (E).
	KeyArchive
	// KeyDone is Done on the selected case, or Move Back to Board on a
	// done one (D).
	KeyDone
	// KeyRemind opens Remind… on the selected case (R).
	KeyRemind
)

// Key is a shortcut of the board.
type Key struct {
	Action KeyAction
	// Primary: with the platform's primary modifier (Ctrl, ⌘); else a
	// single key without modifiers.
	Primary bool
	// Rune is the key, lower case: '1', '2', 'e', 'd', 'r'.
	Rune rune
	// BoardOnly: only while the board shows and the keyboard is not in a
	// text field. The mode keys work in both modes.
	BoardOnly bool
	// Title is the shortcut's text in the shortcuts window.
	Title string
}

// BoardKeys are the board's shortcuts in the shortcuts window's order.
func BoardKeys(tr Translator) []Key {
	return []Key{
		{Action: KeyShowMail, Primary: true, Rune: '1', Title: ShowMailKey(tr)},
		{Action: KeyShowBoard, Primary: true, Rune: '2', Title: ShowBoardKey(tr)},
		{Action: KeyArchive, Rune: 'e', BoardOnly: true, Title: Archive(tr)},
		{Action: KeyDone, Rune: 'd', BoardOnly: true, Title: DoneKey(tr)},
		{Action: KeyRemind, Rune: 'r', BoardOnly: true, Title: Remind(tr)},
	}
}

// KeysGroup heads the board's group in the shortcuts window.
func KeysGroup(tr Translator) string { return BoardName(tr) }

// ShowMailKey is the shortcut window's text of Primary+1.
func ShowMailKey(tr Translator) string {
	// TRANSLATORS: the shortcuts window: the key that shows the mail in the
	// main window.
	return tr.T("Show Mail")
}

// ShowBoardKey is the shortcut window's text of Primary+2.
func ShowBoardKey(tr Translator) string {
	// TRANSLATORS: the shortcuts window: the key that shows the board in the
	// main window.
	return tr.T("Show Board")
}

// DoneKey is the shortcut window's text of D.
func DoneKey(tr Translator) string {
	// TRANSLATORS: the shortcuts window: the key that marks the selected
	// case done, or moves a done one back to the board.
	return tr.T("Mark as Done or Move Back to Board")
}

// KeyFor is the action of a key pressed while the board shows: r is the
// key (any case), primary whether the primary modifier is held and other
// whether any other modifier (Shift, Alt) is; inText whether the keyboard
// is in a text field. False when the key is not the board's.
//
// r must be layout-independent for the digit keys (Primary+1, Primary+2):
// on a Czech QWERTZ layout the number row types +, ě, š ... so a client
// that passes the typed character never matches. Clients pass the digit
// of the physical number-row key: GTK the keyval of the key with the US
// layout (TranslateKeyboardState / the hardware keycode), macOS the key
// code (kVK_ANSI_1/2, not charactersIgnoringModifiers), Windows the
// virtual key (VK_1/VK_2, not the character).
func KeyFor(r rune, primary, other, inText bool, mode Mode, tr Translator) (KeyAction, bool) {
	if other {
		return 0, false
	}
	if r >= 'A' && r <= 'Z' {
		r += 'a' - 'A'
	}
	for _, k := range BoardKeys(tr) {
		if k.Rune != r || k.Primary != primary {
			continue
		}
		if k.BoardOnly && (mode != ModeBoard || inText) {
			return 0, false
		}
		return k.Action, true
	}
	return 0, false
}

// EscapeTarget is what Escape does on the board.
type EscapeTarget int

// The targets.
const (
	// EscapeNothing: Escape does nothing here (the List's own page).
	EscapeNothing EscapeTarget = iota
	// EscapeClosePopup closes the open popup (a menu, a popover, the
	// recipients' suggestions) and nothing else.
	EscapeClosePopup
	// EscapeFocusStatePill moves the keyboard from the reply editor or a
	// recipient field to the detail's state pill; nothing closes.
	EscapeFocusStatePill
	// EscapeClosePanel closes the sliding detail panel (Columns, Today, a
	// narrow List).
	EscapeClosePanel
)

// EscapeFor is where Escape goes, in two steps: an open popup closes
// first; the keyboard in the editor or the recipient fields goes to the
// state pill; anywhere else on the page an open panel closes. Without a
// panel nothing happens.
func EscapeFor(focusInEditorOrRecipients, popupOpen, panelOpen bool) EscapeTarget {
	switch {
	case popupOpen:
		return EscapeClosePopup
	case focusInEditorOrRecipients:
		return EscapeFocusStatePill
	case panelOpen:
		return EscapeClosePanel
	}
	return EscapeNothing
}

// NumberRowDigit is the digit of a physical number-row key, 1 or 2, from
// its hardware keycode as GTK reports it on Linux (evdev code + 8 under
// both X11 and Wayland: KEY_1 is 2, so 10; KEY_2 is 3, so 11), whatever the
// keyboard layout types there; 0 for any other key. The GTK client passes
// it to KeyFor for Primary+1 and Primary+2 when the typed character is not
// a digit (a Czech QWERTZ types + and ě on those keys).
func NumberRowDigit(keycode uint) rune {
	switch keycode {
	case 10:
		return '1'
	case 11:
		return '2'
	}
	return 0
}
