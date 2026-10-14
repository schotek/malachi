// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contextmenu

import "fmt"

// Translator is what the texts need of the catalogue (i18n.Tr in GTK,
// L10n in the other clients); the tests pass one that returns the msgids.
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// Labels are the states that choose an item's label.
type Labels struct {
	// Comment: Reply is Comment (an issue tracker's account).
	Comment bool
	// Flagged: ToggleFlag is Unstar.
	Flagged bool
	// Outbox: Trash is Cancel Sending.
	Outbox bool
	// Junk: EmptyFolder is Empty Junk.
	Junk bool
	// Favourite: Favourite is Remove from Favourites.
	Favourite bool
	// Collapsed: Fold is Expand.
	Collapsed bool
	// Count: OpenMessage opens this many rows (2 or more).
	Count int
}

// Label is an item's text. Some carry a mnemonic underscore, as the same
// items do in the clients' other menus; each client shows it its own way.
// Most are the msgids of the window's other controls.
func Label(tr Translator, id ItemID, l Labels) string {
	switch id {
	case OpenMessage:
		if l.Count > 1 {
			// TRANSLATORS: In the menu of several selected messages: opens each in a window of its own; %d is their number.
			return fmt.Sprintf(tr.N("Open %d Message in a New Window", "Open %d Messages in New Windows", l.Count), l.Count)
		}
		// TRANSLATORS: In the menu of a message in the list: opens it in a window of its own.
		return tr.T("Open in New Window")
	case EditDraft:
		return tr.T("Edit")
	case Reply:
		if l.Comment {
			return tr.T("Comment")
		}
		return tr.T("Reply")
	case ReplyAll:
		return tr.T("Reply All")
	case Forward:
		return tr.T("Forward")
	case MarkRead:
		return tr.T("Mark as _Read")
	case MarkUnread:
		return tr.T("Mark as _Unread")
	case ToggleFlag:
		if l.Flagged {
			return tr.T("Unstar")
		}
		return tr.T("Star")
	case ChangeStatus:
		return tr.T("Change Status")
	case Archive:
		return tr.T("Archive")
	case MoveTo:
		return tr.T("Move To")
	case Junk:
		return tr.T("Mark as Junk")
	case Trash:
		if l.Outbox {
			return tr.T("Cancel Sending")
		}
		return tr.T("Move to Trash")
	case Assistant:
		return tr.T("Assistant")
	case CheckMail:
		return tr.T("Check for New Mail")
	case MarkAllRead:
		// TRANSLATORS: In the menu of a folder in the sidebar.
		return tr.T("Mark All as Read")
	case EmptyFolder:
		if l.Junk {
			// TRANSLATORS: In the menu of the Junk folder: deletes its messages for good.
			return tr.T("Empty Junk")
		}
		// TRANSLATORS: In the menu of the Trash folder: deletes its messages for good.
		return tr.T("Empty Trash")
	case Favourite:
		if l.Favourite {
			return tr.T("Remove from Favourites")
		}
		return tr.T("Add to Favourites")
	case Fold:
		if l.Collapsed {
			return tr.T("Expand")
		}
		return tr.T("Collapse")
	}
	return ""
}

// Question is a confirmation: its heading, its text and the label of the
// button that goes ahead.
type Question struct {
	Heading, Body, Confirm string
}

// EmptyQuestion asks before EmptyFolder deletes the n messages the folder
// shows (folder.empty): for good, and only what Malachi Mail keeps, so it
// says that older messages kept on the server only stay there.
func EmptyQuestion(tr Translator, junk bool, n int) Question {
	q := Question{
		// TRANSLATORS: The heading of the question before the Trash is emptied.
		Heading: tr.T("Empty the Trash?"),
		// TRANSLATORS: The button that empties the Trash or the Junk folder.
		Confirm: tr.T("_Empty"),
	}
	if junk {
		// TRANSLATORS: The heading of the question before the Junk folder is emptied.
		q.Heading = tr.T("Empty the Junk Folder?")
	}
	// TRANSLATORS: %d is the number of messages the folder shows.
	q.Body = fmt.Sprintf(tr.N("%d message will be deleted for good. Older messages that are only on the mail server stay there.",
		"%d messages will be deleted for good. Older messages that are only on the mail server stay there.", n), n)
	return q
}

// MovingText is the action of Move To in an error toast ("Could not …").
func MovingText(tr Translator, n int) string {
	if n == 1 {
		// TRANSLATORS: What failed when a message could not be moved to another folder.
		return tr.T("Moving the message")
	}
	// TRANSLATORS: What failed when messages could not be moved to another folder; %d is their number.
	return fmt.Sprintf(tr.N("Moving %d message", "Moving %d messages", n), n)
}

// MarkingReadText is the action of Mark All as Read in an error toast.
func MarkingReadText(tr Translator) string {
	// TRANSLATORS: What failed when a folder could not be marked read.
	return tr.T("Marking the folder as read")
}

// EmptyingText is the action of EmptyFolder in an error toast.
func EmptyingText(tr Translator, junk bool) string {
	if junk {
		// TRANSLATORS: What failed when the Junk folder could not be emptied.
		return tr.T("Emptying the Junk folder")
	}
	// TRANSLATORS: What failed when the Trash could not be emptied.
	return tr.T("Emptying the Trash")
}
