// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "fmt"

// The texts of the case detail's Suggest Reply (BoardSuggestReplyText.swift):
// a field for the user's optional instruction and a button that has the
// assistant write one reply draft for the case. The reasons are the
// triage's where the meaning is the same (TriageFailureText); Stop and the
// sign-in hint are the assistant panel's (ui/internal/assistant).

// SuggestReply is the button that asks the assistant for a reply draft.
func SuggestReply(tr Translator) string {
	// TRANSLATORS: a button in a conversation's detail on the board: the
	// assistant writes a reply draft for it.
	return tr.T("✦ Suggest Reply")
}

// SuggestFollowUp is the button on a case waiting for someone else
// (IsFollowUp): the assistant writes a follow-up to the user's own last
// message.
func SuggestFollowUp(tr Translator) string {
	// TRANSLATORS: a button in a conversation's detail on the board, for a
	// conversation where the user waits for an answer: the assistant writes
	// a polite follow-up to the user's own last message.
	return tr.T("✦ Suggest Follow-up")
}

// SuggestReplyTitle is the button: SuggestFollowUp for a follow-up, else
// SuggestReply.
func SuggestReplyTitle(followUp bool, tr Translator) string {
	if followUp {
		return SuggestFollowUp(tr)
	}
	return SuggestReply(tr)
}

// SuggestReplyPlaceholder is the placeholder of the instruction field.
func SuggestReplyPlaceholder(tr Translator) string {
	return tr.T("What should the reply say? (optional)")
}

// SuggestReplyRunning is the progress while the assistant writes.
func SuggestReplyRunning(tr Translator) string { return tr.T("Writing a suggested reply…") }

// SuggestReplyElsewhere is the control's line while the request runs for
// another case.
func SuggestReplyElsewhere(tr Translator) string {
	return tr.T("The assistant is writing a reply for another conversation")
}

// SuggestReplyFailure is why a suggested reply failed
// (Board.SuggestReplyFailure).
type SuggestReplyFailure int

// The failures.
const (
	ReplyNotFound SuggestReplyFailure = iota
	ReplyNotSignedIn
	ReplyToolsMissing
	ReplyTimeout
	ReplyCancelled
	ReplyStopped
	ReplyBackend
	ReplyNoDraft
	// ReplyLimit: the assistant's usage limit was reached.
	ReplyLimit
)

// SuggestReplyFailures lists every failure.
var SuggestReplyFailures = []SuggestReplyFailure{
	ReplyNotFound, ReplyNotSignedIn, ReplyToolsMissing, ReplyTimeout, ReplyCancelled, ReplyStopped,
	ReplyBackend, ReplyNoDraft, ReplyLimit,
}

// SuggestReplyFailed is how a request failed: "The suggested reply failed:
// it took too long."
func SuggestReplyFailed(f SuggestReplyFailure, tr Translator) string {
	// TRANSLATORS: %s is the reason, such as "it took too long".
	return fmt.Sprintf(tr.T("The suggested reply failed: %s."), SuggestReplyFailureText(f, tr))
}

// SuggestReplyFailureText is why a request failed, inside a sentence.
func SuggestReplyFailureText(f SuggestReplyFailure, tr Translator) string {
	switch f {
	case ReplyNotFound:
		return TriageFailureText(FailNotFound, tr)
	case ReplyNotSignedIn:
		return TriageFailureText(FailNotSignedIn, tr)
	case ReplyToolsMissing:
		return TriageFailureText(FailToolsMissing, tr)
	case ReplyTimeout:
		return TriageFailureText(FailTimeout, tr)
	case ReplyCancelled:
		return TriageFailureText(FailCancelled, tr)
	case ReplyStopped:
		return TriageFailureText(FailStopped, tr)
	case ReplyBackend:
		return TriageFailureText(FailBackend, tr)
	case ReplyNoDraft:
		// TRANSLATORS: why a suggested reply failed: the assistant ended
		// without creating the draft.
		return tr.T("the assistant wrote no reply")
	case ReplyLimit:
		return TriageFailureText(FailLimit, tr)
	}
	return ""
}

// The inline reply editor's texts for what was typed and not saved, or a
// send that did not go (BoardReplyPanes.swift): the note in the reply
// block of a case whose reply keeps failing to save, the toast for a send
// that failed after the user moved to another case, and the question
// before quitting with such a reply.

// ReplyNotSaved is the note in the reply block while what was typed in
// the suggested reply could not be saved (the app keeps trying).
func ReplyNotSaved(tr Translator) string {
	return tr.T("This reply could not be saved yet; Malachi Mail keeps trying.")
}

// ReplyNotSent is the toast when a reply the user sent and then left
// was not sent. title is the reply's subject, cleaned.
func ReplyNotSent(title string, tr Translator) string {
	// TRANSLATORS: %s is the reply's subject, such as "Re: Offer".
	return fmt.Sprintf(tr.T("Your reply “%s” was not sent; it is still on the board."), title)
}

// QuitUnsavedHeading heads the question before quitting while a reply on
// the board could not be saved or sent.
func QuitUnsavedHeading(tr Translator) string { return tr.T("Quit without saving a reply?") }

// QuitUnsentHeading heads the question when nothing typed is unsaved but a
// reply was sent and the send has not answered yet.
func QuitUnsentHeading(tr Translator) string {
	// TRANSLATORS: Heading of the question before quitting while a reply
	// the user sent is still being sent.
	return tr.T("Quit with a reply still sending?")
}

// QuitHeading picks the question's heading: the "still sending" one when
// nothing is unsaved but a send is unanswered, else QuitUnsavedHeading.
func QuitHeading(unsaved, sending bool, tr Translator) string {
	if sending && !unsaved {
		return QuitUnsentHeading(tr)
	}
	return QuitUnsavedHeading(tr)
}

// QuitUnsavedBody explains QuitUnsavedHeading.
func QuitUnsavedBody(tr Translator) string {
	return tr.T("A reply on the board could not be saved or sent yet. If you quit now, what you typed in it may be lost.")
}

// QuitAnyway is the question's button that quits all the same.
func QuitAnyway(tr Translator) string { return tr.T("_Quit Anyway") }
