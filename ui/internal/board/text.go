// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package board holds the texts of the board (Nástěnka): the window's
// second mode beside Mail, which lists conversations and issues as cases
// in four states (Hot, Waiting for You, Waiting for Them, For Your
// Information), in three styles (List, Columns, Today), with the
// assistant's notes and the board's triage by the user's Claude Code.
//
// This file holds the texts: one function per text of the macOS client's
// Board.Text (macos/Sources/MalachiCore/Board/BoardText.swift and
// BoardTriageText.swift), with the same grouping and comparable names, and
// the board's view literals that moved there. The model and view logic
// are ported in this package too (case.go, view.go, controller.go,
// triage.go; the triage run in ui/internal/boardtriage, the suggested
// reply in ui/internal/boardreply); this file stays the reference of the
// msgids, which the macOS and Windows clients look up with key = msgid.
//
// The package is pure (no GTK, no gettext, no cgo: the caller passes a
// Translator). Strings from a case (a model's name, a run's note) are
// display text the caller has cleaned already; they are formatted in,
// never concatenated into a sentence.
package board

import "fmt"

// Translator translates a msgid of the malachi domain: T a plain one, N a
// plural form for n, C one disambiguated by a context. It has the method
// set of jira.Translator, so GTK passes the same adapter over i18n.T, i18n.N
// and i18n.C (i18n.Tr); the package stays free of the API types.
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// State is a case's state (Board.State).
type State int

// The states.
const (
	// StateHot needs the user now.
	StateHot State = iota
	// StateYou waits for the user's answer.
	StateYou
	// StateThem: the user waits for someone else.
	StateThem
	// StateInfo is for reading only.
	StateInfo
)

// States lists every state in the board's order.
var States = []State{StateHot, StateYou, StateThem, StateInfo}

// Mail is the mode switch's segment and View menu item of the mail
// (Board.texts().mail).
func Mail(tr Translator) string { return tr.T("Mail") }

// BoardName is the mode switch's segment and View menu item of the board,
// the window's title in it and the navigation column's caption
// (Board.texts().board).
func BoardName(tr Translator) string {
	// TRANSLATORS: the window's second mode beside Mail: the conversations
	// that need something, sorted into four states.
	return tr.T("Board")
}

// StateName is a state's name: its section, column, tile and pill.
func StateName(s State, tr Translator) string {
	switch s {
	case StateHot:
		// TRANSLATORS: a state of the board, its column and filter: the
		// cases that need the user now (a deadline, an escalation).
		return tr.T("Hot")
	case StateYou:
		// TRANSLATORS: a state of the board, its column and filter: the
		// cases that wait for the user's answer.
		return tr.T("Waiting for You")
	case StateThem:
		// TRANSLATORS: a state of the board, its column and filter: the
		// cases where the user waits for someone else's answer.
		return tr.T("Waiting for Them")
	case StateInfo:
		// TRANSLATORS: a state of the board, its column and filter: the
		// cases with nothing to do, only for reading.
		return tr.T("For Your Information")
	}
	return ""
}

// FilterKind is which cases the list shows (Board.Filter).
type FilterKind int

// The filters.
const (
	FilterAll FilterKind = iota
	FilterState
	FilterDone
	// FilterSnoozed is the cases off the board until a reminder.
	FilterSnoozed
)

// Filter is a filter of the list; State counts for FilterState only.
type Filter struct {
	Kind  FilterKind
	State State
}

// FilterTitle is a filter's name in the navigation column and the Show
// menu.
func FilterTitle(f Filter, tr Translator) string {
	switch f.Kind {
	case FilterState:
		return StateName(f.State, tr)
	case FilterDone:
		return Done(tr)
	case FilterSnoozed:
		return Snoozed(tr)
	}
	// TRANSLATORS: the board's filter that shows every case still on it.
	return tr.T("Overview")
}

// AllAccounts is the account filter that shows every account.
func AllAccounts(tr Translator) string { return tr.C("account filter", "All Accounts") }

// Done is the Done filter, its section, and the detail's button.
func Done(tr Translator) string {
	// TRANSLATORS: the board's filter and section of the finished cases,
	// and the button that marks a case finished.
	return tr.T("Done")
}

// CaseCount is "1 case", "23 cases".
func CaseCount(n int, tr Translator) string {
	// TRANSLATORS: the number of cases on the board, after the account
	// filter: "All Accounts · 23 cases".
	return fmt.Sprintf(tr.N("%d case", "%d cases", n), n)
}

// MessageCount is "1 message", "3 messages".
func MessageCount(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("%d message", "%d messages", n), n)
}

// TodoPhrase is the Today page's sentence under its title: n counts the
// hot cases and the ones waiting for the user that are new, due today or
// back from a reminder (NeedsYouToday).
func TodoPhrase(n int, tr Translator) string {
	if n < 1 {
		return tr.T("Nothing needs you today.")
	}
	return fmt.Sprintf(tr.N("%d thing needs you today.", "%d things need you today.", n), n)
}

// AndMore is the row under the top cases waiting for the user.
func AndMore(n int, tr Translator) string {
	// TRANSLATORS: the row under the first few cases waiting for the user;
	// %d is how many more there are.
	return fmt.Sprintf(tr.N("and %d more", "and %d more", n), n)
}

// DueGroup is a group of the Today page's deadlines (Board.DueGroupKind).
type DueGroup int

// The groups.
const (
	DueOverdue DueGroup = iota
	DueToday
	DueTomorrow
	// DueThisWeek is two to seven days ahead.
	DueThisWeek
	DueLater
)

// DueGroupTitle is a deadline group's title; Today and Tomorrow are also a
// deadline's label.
func DueGroupTitle(k DueGroup, tr Translator) string {
	switch k {
	case DueOverdue:
		// TRANSLATORS: a group of deadlines that have passed.
		return tr.T("Overdue")
	case DueToday:
		return Today(tr)
	case DueTomorrow:
		// TRANSLATORS: a group of deadlines, a deadline's day, and the
		// reminder preset of tomorrow 09:00.
		return tr.T("Tomorrow")
	case DueThisWeek:
		// TRANSLATORS: a group of deadlines two to seven days ahead.
		return tr.T("Next 7 Days")
	case DueLater:
		// TRANSLATORS: a group of deadlines more than a week ahead.
		return tr.C("deadline", "Later")
	}
	return ""
}

// Today is the style of the day's overview and a deadline's day.
func Today(tr Translator) string {
	// TRANSLATORS: the board's style that shows the day (what needs the
	// user, deadlines), a group of deadlines and a deadline's day.
	return tr.T("Today")
}

// SourceKind is who decided a case's state (Board.StateSource).
type SourceKind int

// The sources.
const (
	SourceRules SourceKind = iota
	SourceAssistantKept
	SourceAssistantChanged
	SourceUser
	SourceAssistantOff
)

// Source is who decided a case's state; From is the rules' state the
// assistant changed (SourceAssistantChanged only).
type Source struct {
	Kind SourceKind
	From State
}

// SourceText says who decided a case's state, under "Why is this here?".
// model names the assistant's model, "" when unknown; it is cleaned by
// the caller.
func SourceText(s Source, model string, tr Translator) string {
	switch s.Kind {
	case SourceAssistantKept:
		if model == "" {
			return tr.T("The daemon’s rules set the state and the assistant kept it.")
		}
		// TRANSLATORS: %s is the name of the assistant's model, such as
		// "claude-sonnet-4-5".
		return fmt.Sprintf(tr.T("The daemon’s rules set the state and the assistant (%s) kept it."), model)
	case SourceAssistantChanged:
		if model == "" {
			// TRANSLATORS: %s is the state the rules chose, such as "Waiting for You".
			return fmt.Sprintf(tr.T("The assistant refined the state. The rules suggested: %s."), StateName(s.From, tr))
		}
		// TRANSLATORS: the first %s is the name of the assistant's model,
		// the second the state the rules chose, such as "Waiting for You".
		return fmt.Sprintf(tr.T("The assistant (%s) refined the state. The rules suggested: %s."), model, StateName(s.From, tr))
	case SourceUser:
		return tr.T("You moved this case yourself.")
	case SourceAssistantOff:
		return tr.T("The daemon’s rules set the state; the assistant is off.")
	}
	return tr.T("The daemon’s rules set the state. The assistant has not looked at this case yet.")
}

// separator joins the parts of a line ("All Accounts · 23 cases").
const separator = " · "

// StatusLine is the status bar's line: who sorted the board. model and
// note (the last run's) are cleaned by the caller; a note follows after a
// separator.
func StatusLine(annotated bool, model, note string, tr Translator) string {
	if !annotated {
		return assistantOffLine(tr)
	}
	var line string
	if model == "" {
		line = tr.T("Sorted by rules · refined by the assistant")
	} else {
		// TRANSLATORS: %s is the name of the assistant's model.
		line = fmt.Sprintf(tr.T("Sorted by rules · refined by the assistant (%s)"), model)
	}
	if note != "" {
		line += separator + note
	}
	return line
}

// assistantOffLine is the status line when the assistant does not refine
// the board.
func assistantOffLine(tr Translator) string {
	return tr.T("Sorted by the daemon’s rules · assistant off")
}

// NoSelectionTitle and NoSelectionBody fill the detail without a case.
func NoSelectionTitle(tr Translator) string { return tr.T("No Case Selected") }

// NoSelectionBody: see NoSelectionTitle.
func NoSelectionBody(tr Translator) string {
	return tr.T("Select a case to see its summary, a suggested reply and the conversation.")
}

// EmptyTitle and EmptyBody fill the empty board once it is ready.
func EmptyTitle(tr Translator) string { return tr.T("Nothing on the Board") }

// EmptyBody: see EmptyTitle.
func EmptyBody(tr Translator) string {
	return tr.T("Cases from your accounts show up here as they arrive.")
}

// SectionEmpty is a list with no row.
func SectionEmpty(tr Translator) string { return tr.T("Nothing here.") }

// ColumnEmpty is the placeholder of an empty column.
func ColumnEmpty(s State, tr Translator) string {
	if s == StateHot {
		// TRANSLATORS: the empty column Hot.
		return tr.T("Nothing burning.")
	}
	// TRANSLATORS: an empty column of the board.
	return tr.T("Empty.")
}

// FromAssistant is the heading of the commitments: the promises the
// assistant found in the user's replies.
func FromAssistant(tr Translator) string { return tr.T("✦ From the Assistant") }

// SummaryHeading heads the assistant's summary in the detail.
func SummaryHeading(tr Translator) string { return tr.T("✦ Summary from the Assistant") }

// TasksHeading heads the assistant's tasks and questions in the detail.
func TasksHeading(tr Translator) string { return tr.T("✦ Tasks and Questions") }

// DraftHeading heads the assistant's suggested reply.
func DraftHeading(tr Translator) string { return tr.T("Suggested Reply") }

// DraftNote is the line under the suggested reply: it is a local draft,
// on the board only (never in the Drafts folder) until it is sent.
func DraftNote(tr Translator) string {
	// TRANSLATORS: under the board's suggested reply; "it" is the reply.
	return tr.T("Only here on the board until you send it")
}

// ReplyLoading is the detail's reply block while the suggested reply loads.
func ReplyLoading(tr Translator) string { return tr.T("Loading the suggested reply…") }

// ReplyLoadFailed is the detail's reply block when the suggested reply
// could not be opened (TryAgain beside it unless the draft is gone).
func ReplyLoadFailed(tr Translator) string { return tr.T("The suggested reply could not be opened.") }

// ReplyRemoved is the toast when the suggested reply being edited was
// deleted elsewhere.
func ReplyRemoved(tr Translator) string { return tr.T("The suggested reply was removed elsewhere.") }

// Unstar removes the star that keeps a case hot; the app's own wording
// for removing the flag (the main toolbar's).
func Unstar(tr Translator) string { return tr.T("Unstar") }

// OpenDraft opens the suggested reply as a draft.
func OpenDraft(tr Translator) string { return tr.T("Open Draft") }

// Discard drops the suggested reply; with a mnemonic (macOS strips it).
func Discard(tr Translator) string { return tr.T("_Discard") }

// WhyLink opens the reason of a case's state.
func WhyLink(tr Translator) string { return tr.T("Why is this here?") }

// NotDone moves a done case back to the board (the detail's button, the
// context menu).
func NotDone(tr Translator) string { return tr.T("Move Back to Board") }

// Remind is the toolbar's and the detail's button that picks a reminder.
func Remind(tr Translator) string {
	// TRANSLATORS: a button that takes a case off the board until a
	// chosen time.
	return tr.T("Remind…")
}

// Reply answers a case's newest message.
func Reply(tr Translator) string { return tr.T("Reply") }

// Close closes the sliding detail panel; with a mnemonic (macOS strips
// it).
func Close(tr Translator) string { return tr.T("_Close") }

// Triage is the toolbar's button that starts the assistant's triage.
func Triage(tr Translator) string {
	// TRANSLATORS: a button: the assistant reads the conversations on the
	// board and adds notes (titles, summaries, tasks, deadlines).
	return tr.T("✦ Triage")
}

// Later is the toast of an action this preview does not have yet.
func Later(tr Translator) string { return tr.T("Not in this preview yet.") }

// Style is how the board is laid out (Board.Style).
type Style int

// The styles.
const (
	StyleList Style = iota
	StyleColumns
	StyleToday
)

// StyleTitle is a style's segment in the toolbar's switch.
func StyleTitle(s Style, tr Translator) string {
	switch s {
	case StyleList:
		// TRANSLATORS: the board's style: a navigation column, the list of
		// cases and the detail.
		return tr.C("board style", "List")
	case StyleColumns:
		// TRANSLATORS: the board's style: one column of cards per state.
		return tr.C("board style", "Columns")
	case StyleToday:
		return Today(tr)
	}
	return ""
}

// StyleMenuTitle is a style's item in the View menu.
func StyleMenuTitle(s Style, tr Translator) string {
	switch s {
	case StyleList:
		return tr.T("As List")
	case StyleColumns:
		return tr.T("As Columns")
	}
	return StyleTitle(s, tr)
}

// DefaultStyleSetting is the Settings row of the style the board opens in
// (Settings → General → Board, the key board-default-style); its choices
// are DefaultStyles, named by DefaultStyleTitle.
func DefaultStyleSetting(tr Translator) string {
	// TRANSLATORS: Settings → General → Board: which style (Last Used,
	// List, Columns, Today) the board opens in.
	return tr.T("Board View")
}

// LastUsed is the choice of a setting that takes what the user had last
// (Board View, Open at Launch).
func LastUsed(tr Translator) string {
	// TRANSLATORS: a choice of Settings → General → Board (Board View,
	// Open at Launch): what the user had last.
	return tr.C("board setting", "Last Used")
}

// DefaultStyleTitle names a choice of Board View.
func DefaultStyleTitle(d DefaultStyle, tr Translator) string {
	if d.Last {
		return LastUsed(tr)
	}
	return StyleTitle(d.Style, tr)
}

// StartModeSetting is the Settings row of the mode the main window opens
// in (the key board-start-mode); its choices are StartModes, named by
// StartModeTitle.
func StartModeSetting(tr Translator) string {
	// TRANSLATORS: Settings → General → Board: whether the main window
	// opens on Mail, on the Board, or on what was shown last.
	return tr.T("Open at Launch")
}

// StartModeTitle names a choice of Open at Launch.
func StartModeTitle(s StartChoice, tr Translator) string {
	switch s {
	case StartBoard:
		return BoardName(tr)
	case StartLast:
		return LastUsed(tr)
	}
	return Mail(tr)
}

// ShowBoardSetting is the Settings switch that turns the board on or off
// (board preferences enabled), and ShowBoardSettingSubtitle its line.
func ShowBoardSetting(tr Translator) string {
	// TRANSLATORS: Settings → General → Board: a switch.
	return tr.T("Show the Board")
}

// ShowBoardSettingSubtitle: see ShowBoardSetting.
func ShowBoardSettingSubtitle(tr Translator) string {
	return tr.T("Sorts your conversations into what needs you, what waits for others and what is only for reading. Turned off, your decisions are kept.")
}

// WindowsSetting heads the group of how long each state keeps a case
// (board preferences windows).
func WindowsSetting(tr Translator) string {
	// TRANSLATORS: Settings → General → Board: a group of rows, one per
	// state, each followed by a number of days.
	return tr.T("Keep cases for")
}

// WindowsSettingSubtitle explains WindowsSetting.
func WindowsSettingSubtitle(tr Translator) string {
	return tr.T("A case leaves the board when its newest message is older than this, unless your decision, a reminder or a deadline keeps it.")
}

// Days is a value of a state's row under WindowsSetting: "30 days".
func Days(n int, tr Translator) string {
	// TRANSLATORS: how long a state of the board keeps a case.
	return fmt.Sprintf(tr.N("%d day", "%d days", n), n)
}

// You is the sender of the user's own messages in the conversation.
func You(tr Translator) string {
	// TRANSLATORS: the sender of the user's own messages in a conversation.
	return tr.T("You")
}

// Conversation is the heading of the detail's conversation:
// "Conversation · 3 messages".
func Conversation(n int, tr Translator) string {
	// TRANSLATORS: %s is a number of messages, such as "3 messages".
	return fmt.Sprintf(tr.T("Conversation · %s"), MessageCount(n, tr))
}

// Deadlines heads the Today page's deadlines.
func Deadlines(tr Translator) string { return tr.T("Deadlines") }

// DueEmpty is the Today page's deadlines without one.
func DueEmpty(tr Translator) string {
	return tr.T("No deadlines. The assistant finds deadlines in the text of messages and keeps the sentence each one comes from.")
}

// Commitments is the commitments' tile on the Today page.
func Commitments(tr Translator) string {
	// TRANSLATORS: a tile of the Today page: the promises the assistant
	// found in the user's replies.
	return tr.T("Promised")
}

// SpokenDue is a part of a row's spoken label: "Due Tomorrow".
func SpokenDue(label string, tr Translator) string {
	// TRANSLATORS: spoken by the screen reader; %s is a deadline's day,
	// such as "Tomorrow" or "20 Oct".
	return fmt.Sprintf(tr.T("Due %s"), label)
}

// SpokenRemind is a part of a row's spoken label: "Back on the board
// Tomorrow 09:00".
func SpokenRemind(label string, tr Translator) string { return SnoozedUntil(label, tr) }

// SpokenAttachments is a part of a row's spoken label.
func SpokenAttachments(tr Translator) string { return tr.T("Has attachments") }

// SpokenUnread is a part of a row's spoken label.
func SpokenUnread(tr Translator) string {
	// TRANSLATORS: spoken by the screen reader for a case with unread mail.
	return tr.C("board row", "Unread")
}

// Reason says why the rules put a case where it is, by the rule's code.
// The codes are an open set: one this client does not know gets
// ReasonUnknown.
func Reason(code string, tr Translator) string {
	switch code {
	case "hot.important":
		return tr.T("The newest message is marked as important, addressed to you and from a sender you have written to.")
	case "hot.flagged":
		return tr.T("You flagged a message in this conversation.")
	case "you.addressed":
		return tr.T("The newest message is addressed to you by a sender you have written to.")
	case "you.repliedToYou":
		return tr.T("The newest message answers one of yours.")
	case "them.replied":
		return tr.T("You replied last; the next step is theirs.")
	case "them.asked":
		return tr.T("You asked a question and wait for the answer.")
	case "info.ccOnly":
		return tr.T("You are only in Cc on the newest message.")
	case "info.notAddressed":
		return tr.T("The message is not addressed to you (a mailing list or a Bcc).")
	case "info.yourNote":
		return tr.T("A note to yourself.")
	case "you.newContact":
		return tr.T("The newest message is addressed to you by someone you have never written to.")
	case "info.unknownSender":
		return tr.T("The newest message comes from someone you have never written to and is not addressed to you, so it waits under For Your Information.")
	case "jira.yourComment":
		return tr.T("Your comment is the latest in the issue; the next step is theirs.")
	case "jira.assigned":
		return tr.T("Someone wrote in an issue assigned to you.")
	case "jira.reporter":
		return tr.T("Someone wrote in an issue you reported.")
	case "jira.commented":
		return tr.T("Someone wrote in an issue you commented on.")
	case "jira.watching":
		return tr.T("You only watch this issue.")
	case "kept":
		return tr.T("The rules would no longer list it; your choice, a reminder, a deadline or a promise keeps it here.")
	}
	return ReasonUnknown(tr)
}

// ReasonReminded is the line "Why is this here?" adds for a case back
// from a reminder (Case.RemindedAt).
func ReasonReminded(tr Translator) string { return tr.T("A reminder you set has come due.") }

// ReasonUserKeeps is the line "Why is this here?" adds whenever the user
// chose the case's state (Case.UserState): the choice keeps it on the
// board whatever the rules say.
func ReasonUserKeeps(tr Translator) string {
	// TRANSLATORS: under "Why is this here?" when the user moved the case
	// to its state; "it" is the case.
	return tr.T("Your decision keeps it on the board.")
}

// Reminded is the badge of a case back from a reminder, until the user
// acts on it.
func Reminded(tr Translator) string {
	// TRANSLATORS: a badge on a case of the board that came back because a
	// reminder the user set came due.
	return tr.C("board badge", "Reminded")
}

// NewContact is the badge of a case whose newest message is addressed to
// the user by someone the user has never written to (you.newContact).
func NewContact(tr Translator) string {
	// TRANSLATORS: a badge on a case of the board: its sender is someone
	// the user has never written to.
	return tr.C("board badge", "New contact")
}

// TitleWithBadge is a title with a badge after it, as one label: an
// account and its kind ("Work (IMAP)"), a case and its badge. Both are
// cleaned by the caller; without a badge the title alone.
func TitleWithBadge(title, badge string, tr Translator) string {
	if badge == "" {
		return title
	}
	// TRANSLATORS: a name and a short badge after it, such as "Work
	// (IMAP)" or "Offer (Reminded)".
	return fmt.Sprintf(tr.T("%s (%s)"), title, badge)
}

// PersonAndTime is the detail's line under the title: who and when, both
// cleaned by the caller. Either alone when the other is empty (no " · "
// dangling), empty when both are.
func PersonAndTime(person, when string, tr Translator) string {
	if person == "" || when == "" {
		return person + when
	}
	// TRANSLATORS: the line under a case's title on the board: the other
	// party and the date, such as "Jana Nováková · 2 Oct 2026 14:05".
	return fmt.Sprintf(tr.T("%s · %s"), person, when)
}

// DayAndTime is a day and a time of day: "Thu at 18:00", "Tomorrow at
// 09:00", "20 Oct at 09:00".
func DayAndTime(day, clock string, tr Translator) string {
	// TRANSLATORS: a day and a time of day, such as "Thu at 18:00",
	// "Tomorrow at 09:00" or "20 Oct at 09:00".
	return fmt.Sprintf(tr.T("%s at %s"), day, clock)
}

// TileToolTip is the tooltip of a count tile of the Today page: "Hot: 3
// cases", or the promises of the commitments' tile.
func TileToolTip(t Tile, tr Translator) string {
	if t.Kind == TileCommitments {
		// TRANSLATORS: the tooltip of the Today page's tile of promises;
		// %s is the tile's title ("Promised").
		return fmt.Sprintf(tr.N("%s: %d promise", "%s: %d promises", t.Count), t.Title, t.Count)
	}
	// TRANSLATORS: the tooltip of a tile of the Today page; %s is a state
	// of the board, such as "Hot", %d how many cases are in it.
	return fmt.Sprintf(tr.N("%s: %d case", "%s: %d cases", t.Count), t.Title, t.Count)
}

// ReasonUnknown is the reason of a rule this client does not know.
func ReasonUnknown(tr Translator) string { return tr.T("The daemon’s rules put the case here.") }

// Phase is how far the board's data is (Board.Phase).
type Phase int

// The phases.
const (
	PhaseLoading Phase = iota
	PhasePreparing
	PhaseReady
	PhaseUnavailable
	PhaseFailed
	PhaseUnsupported
	PhaseOff
)

// EmptyTitleOf is the empty board's title by the phase
// (Board.Text.emptyTitle(_:)).
func EmptyTitleOf(p Phase, tr Translator) string {
	switch p {
	case PhaseLoading:
		return tr.T("Loading the Board…")
	case PhasePreparing:
		return tr.T("Preparing the Board…")
	case PhaseReady:
		return EmptyTitle(tr)
	case PhaseUnavailable, PhaseFailed, PhaseUnsupported:
		return tr.T("Board Unavailable")
	case PhaseOff:
		return tr.T("The Board Is Off")
	}
	return ""
}

// EmptyBodyOf is the empty board's body by the phase
// (Board.Text.emptyBody(_:)).
func EmptyBodyOf(p Phase, tr Translator) string {
	switch p {
	case PhasePreparing:
		return preparing(tr)
	case PhaseReady:
		return EmptyBody(tr)
	case PhaseUnavailable:
		return tr.T("The board needs a running mail backend.")
	case PhaseFailed:
		return tr.T("The board could not be loaded. Malachi Mail tries again shortly.")
	case PhaseUnsupported:
		return unsupported(tr)
	case PhaseOff:
		return tr.T("The board is turned off in the settings. Your decisions are kept for when it is on again.")
	}
	return ""
}

// Notice is the line above the cases while they are not the whole truth;
// "" when they are.
func Notice(p Phase, truncated bool, tr Translator) string {
	switch p {
	case PhasePreparing:
		return preparing(tr)
	case PhaseUnavailable:
		return tr.T("The mail backend is not running: the board shows what it knew last.")
	case PhaseFailed:
		return tr.T("The board could not be loaded: it shows what it knew last. Malachi Mail tries again shortly.")
	case PhaseUnsupported:
		return unsupported(tr)
	}
	if truncated {
		return tr.T("Only the newest 1,000 cases are on the board.")
	}
	return ""
}

func unsupported(tr Translator) string {
	return tr.T("This mail backend has no board. A newer Malachi Mail backend brings it.")
}

func preparing(tr Translator) string {
	return tr.T("Malachi Mail is sorting your mail for the first time. This takes a minute or two.")
}

// StaleNotes is the detail's note when the assistant's notes no longer
// count.
func StaleNotes(tr Translator) string {
	return tr.T("The assistant’s notes are out of date: the conversation changed since.")
}

// MessagesLoading and MessagesFailed are the detail's conversation while it
// loads, and when it cannot.
func MessagesLoading(tr Translator) string { return tr.T("Loading the conversation…") }

// MessagesFailed: see MessagesLoading.
func MessagesFailed(tr Translator) string { return tr.T("The conversation could not be loaded.") }

// TryAgain is the link beside MessagesFailed that asks again.
func TryAgain(tr Translator) string { return tr.T("Try Again") }

// AssistantMark is the mark in front of text the assistant wrote: the
// glyph of the summary's heading. Not translated.
const AssistantMark = "✦"

// SpokenAssistant is what the screen reader says for text the assistant
// wrote: "Assistant: Lunch on Friday".
func SpokenAssistant(text string, tr Translator) string {
	// TRANSLATORS: spoken by the screen reader before a title or a reason
	// the assistant wrote; %s is that text.
	return fmt.Sprintf(tr.T("Assistant: %s"), text)
}

// ShowInMail selects the case's message in the mail.
func ShowInMail(tr Translator) string { return tr.T("Show in Mail") }

// ShowInMailGone and ShowInMailFailed: Show in Mail could not find the
// message: the daemon said it is gone, or it could not be asked.
func ShowInMailGone(tr Translator) string {
	return tr.T("Show in Mail failed: the message is no longer on the server.")
}

// ShowInMailFailed: see ShowInMailGone.
func ShowInMailFailed(tr Translator) string {
	return tr.T("Show in Mail failed: the message could not be loaded.")
}

// Archive archives a case's messages.
func Archive(tr Translator) string { return tr.T("Archive") }

// Snoozed is the filter and section of the cases that come back later.
func Snoozed(tr Translator) string {
	// TRANSLATORS: a filter and section of the board: the cases off the
	// board until a reminder.
	return tr.T("Snoozed")
}

// SnoozedUntil is "Back on the board: Tomorrow at 09:00", for a snoozed
// case.
func SnoozedUntil(label string, tr Translator) string {
	// TRANSLATORS: %s is a day and a time, such as "Tomorrow at 09:00" or
	// "20 Oct at 09:00".
	return fmt.Sprintf(tr.T("Back on the board: %s"), label)
}

// RemindKind is a remind preset (Board.RemindPreset.Kind).
type RemindKind int

// The presets.
const (
	RemindLaterToday RemindKind = iota
	RemindTomorrow
	RemindNextWeek
	// RemindThisEvening is 20:00 today, offered from 17:00 to 18:59.
	RemindThisEvening
	// RemindThisMorning is 09:00 today, offered before 05:00 in place of
	// RemindTomorrow.
	RemindThisMorning
)

// RemindKinds lists every preset kind.
var RemindKinds = []RemindKind{RemindLaterToday, RemindTomorrow, RemindNextWeek, RemindThisEvening, RemindThisMorning}

// RemindPreset is a remind preset's item.
func RemindPreset(k RemindKind, tr Translator) string {
	switch k {
	case RemindLaterToday:
		// TRANSLATORS: a reminder preset: in three hours, rounded up to the
		// hour, at 20:00 at the latest.
		return tr.T("Later Today")
	case RemindTomorrow:
		return tr.T("Tomorrow")
	case RemindNextWeek:
		// TRANSLATORS: a reminder preset: next Monday at 09:00.
		return tr.T("Next Week")
	case RemindThisEvening:
		// TRANSLATORS: a reminder preset: today at 20:00.
		return tr.T("This Evening")
	case RemindThisMorning:
		// TRANSLATORS: a reminder preset offered after midnight: today at
		// 09:00.
		return tr.T("This Morning")
	}
	return ""
}

// RemindNoMore ends a reminder and puts the case back on the board at
// once (board.remind with null).
func RemindNoMore(tr Translator) string {
	// TRANSLATORS: a menu item of a snoozed case: it ends the reminder and
	// the case is back on the board now.
	return tr.T("Back on the Board Now")
}

// Archived says what Archive did: messages moved, or only marked done.
func Archived(n int, noArchive bool, tr Translator) string {
	if noArchive {
		return tr.T("Marked as done. This account has no archive.")
	}
	if n < 1 {
		return tr.T("Marked as done. No message was in the inbox.")
	}
	return fmt.Sprintf(tr.N("Archived %d message.", "Archived %d messages.", n), n)
}

// Undo is the button of the Archive toast that takes the archive back.
func Undo(tr Translator) string {
	// TRANSLATORS: the button of a toast that takes back what was just
	// done (an archive).
	return tr.T("Undo")
}

// UndoFailed is the toast when taking an archive back failed.
func UndoFailed(tr Translator) string {
	return tr.T("Could not undo the archive.")
}

// Action is what a write of the board did, for the toast of its failure
// (Board.Text.Action).
type Action int

// The actions.
const (
	ActionMove Action = iota
	ActionDone
	ActionReopen
	ActionRemind
	ActionArchive
	ActionCommitment
	ActionDiscardDraft
	// ActionUnflag is Unstar (board.unflag).
	ActionUnflag
	// ActionPreferences is a change of the board's preferences.
	ActionPreferences
)

// Actions lists every action.
var Actions = []Action{
	ActionMove, ActionDone, ActionReopen, ActionRemind, ActionArchive,
	ActionCommitment, ActionDiscardDraft, ActionUnflag, ActionPreferences,
}

// Why is why a write or load failed, as far as the error says; the client
// maps its errors to it (Board.Text's failureReason).
type Why int

// The reasons.
const (
	// WhyNone: the error does not say.
	WhyNone Why = iota
	// WhyPromiseGone: board.setCommitment's caseNotFound.
	WhyPromiseGone
	// WhyBackendDown: not connected, or the connection dropped.
	WhyBackendDown
	// WhyTimeout: no answer in time.
	WhyTimeout
	// WhyCaseGone: caseNotFound.
	WhyCaseGone
	// WhyNotAccepted: invalidArgument.
	WhyNotAccepted
	// WhyNoBoard: methodNotFound, notImplemented.
	WhyNoBoard
	// WhyNotSaved: storageError.
	WhyNotSaved
	// WhyDraftGone: draftNotFound.
	WhyDraftGone
)

// actionText names what failed, the subject of the toast's sentence.
func actionText(a Action, tr Translator) string {
	switch a {
	case ActionMove:
		return tr.T("Moving the case")
	case ActionDone:
		return tr.T("Marking the case done")
	case ActionReopen:
		return tr.T("Moving the case back to the board")
	case ActionRemind:
		return tr.T("Setting the reminder")
	case ActionArchive:
		return tr.T("Archiving")
	case ActionCommitment:
		return tr.T("Changing the promise")
	case ActionDiscardDraft:
		return tr.T("Discarding the draft")
	case ActionUnflag:
		return tr.T("Removing the star")
	case ActionPreferences:
		return tr.T("Changing the board’s settings")
	}
	return ""
}

// whyText is a reason inside the toast's sentence; "" for WhyNone.
func whyText(w Why, tr Translator) string {
	switch w {
	case WhyPromiseGone:
		return tr.T("the promise no longer exists")
	case WhyBackendDown:
		return tr.T("the mail backend is not running")
	case WhyTimeout:
		return tr.T("the mail backend did not answer in time")
	case WhyCaseGone:
		return tr.T("the case is no longer on the board")
	case WhyNotAccepted:
		return tr.T("the board did not accept it")
	case WhyNoBoard:
		return tr.T("this mail backend has no board")
	case WhyNotSaved:
		return tr.T("the mail backend could not save it")
	case WhyDraftGone:
		return tr.T("the draft no longer exists")
	}
	return ""
}

// Failed is the toast of a failed write or load: what failed and, when
// the error says, why. Never the daemon's own message (it is not for the
// user).
func Failed(a Action, w Why, tr Translator) string {
	what := actionText(a, tr)
	why := whyText(w, tr)
	if why == "" {
		// TRANSLATORS: a toast; %s is an action such as "Moving the case".
		return fmt.Sprintf(tr.T("%s failed."), what)
	}
	// TRANSLATORS: a toast; the first %s is an action such as "Moving the
	// case", the second the reason, such as "the case is no longer on the
	// board".
	return fmt.Sprintf(tr.T("%s failed: %s."), what, why)
}

// The view literals of the macOS client (MalachiMail/Board, the View menu).

// MoveTo is the context menu's submenu of the states.
func MoveTo(tr Translator) string {
	// TRANSLATORS: a menu item whose submenu lists the board's states.
	return tr.T("Move To")
}

// MarkAsDone is the context menu's item that marks a case done.
func MarkAsDone(tr Translator) string { return tr.T("Mark as Done") }

// RemindMe is the context menu's submenu of the remind presets.
func RemindMe(tr Translator) string { return tr.T("Remind Me") }

// StateLabel is the accessibility name of the detail's state pill.
func StateLabel(tr Translator) string { return tr.T("Status") }

// AccountsCaption is the navigation column's second caption.
func AccountsCaption(tr Translator) string { return tr.T("Accounts") }

// MarkPromiseDone is the tooltip and accessibility name of a promise's
// tick.
func MarkPromiseDone(tr Translator) string {
	// TRANSLATORS: a button beside a promise the assistant found in the
	// user's reply.
	return tr.T("Mark Promise as Done")
}

// PromiseCount is "1 promise", "3 promises" (the commitments' spoken
// count).
func PromiseCount(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("%d promise", "%d promises", n), n)
}

// Quoted puts the sentence a deadline comes from in quotation marks.
func Quoted(s string, tr Translator) string {
	// TRANSLATORS: quotation marks around a sentence quoted from a message;
	// use your language's quotation marks.
	return fmt.Sprintf(tr.T("“%s”"), s)
}

// TriageSettingsAccountsNone is Triage These Accounts' line while the list
// names only accounts that are gone or turned off
// (boardtriage.TriageAccountsNone): the daemon keeps such a list rather
// than widen the triage to every account.
func TriageSettingsAccountsNone(tr Translator) string {
	// TRANSLATORS: under "Triage These Accounts" when the accounts chosen
	// for the triage were all removed, so it triages nothing.
	return tr.T("No account is selected, so the triage reads nothing.")
}
