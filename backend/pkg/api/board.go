// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// ---------------------------------------------------------------------------
// Board (docs/api.md §4.13)
// ---------------------------------------------------------------------------
//
// The board sorts the user's conversations and issues into four states by
// what is owed: the daemon's rules decide from headers, structure, folder
// roles and flags (never words), an assistant may annotate a case through
// the MCP bridge, and the user's own choice always wins. Every string of a
// case comes from mail or from an assistant that read mail: it is untrusted
// plain text, cleaned by the daemon (no control or bidi characters, no
// URLs in annotations), and a client never interprets it as markup.

// BoardCaseID identifies a case: "c_" and 32 lowercase hex digits. Stable
// across thread merges (unlike ThreadID) and restarts; per account.
type BoardCaseID string

// BoardCommitmentID identifies a commitment of a case. Opaque.
type BoardCommitmentID string

// BoardRunID identifies a triage run (board.runStart). Opaque.
type BoardRunID string

// BoardState is where a case stands. A closed enum: the four values below.
type BoardState string

const (
	BoardHot  BoardState = "hot"  // needs the user now: marked important, or flagged by the user
	BoardYou  BoardState = "you"  // waits for the user's answer
	BoardThem BoardState = "them" // the user waits for someone else
	BoardInfo BoardState = "info" // nothing to do; for reading
)

// BoardStates lists the states in display order.
var BoardStates = []BoardState{BoardHot, BoardYou, BoardThem, BoardInfo}

// Valid reports whether s is one of BoardStates.
func (s BoardState) Valid() bool {
	switch s {
	case BoardHot, BoardYou, BoardThem, BoardInfo:
		return true
	}
	return false
}

// BoardReason is the code of the rule that gave a case its RuleState. An
// open enum: a client shows a generic text for a code it does not know.
// Codes are never reused for another meaning; a new rule gets a new code.
type BoardReason string

// A sender is known when the user has written to it: its From or a
// Reply-To address is in To or Cc of a message in a folder of role sent or
// outbox of any of the user's enabled mail accounts.
const (
	// The newest relevant member is inbound, the user is in its To, its
	// sender is known, and its own header says Importance: high or
	// X-Priority 1 or 2.
	BoardReasonHotImportant BoardReason = "hot.important"
	// The user flagged a member and the newest relevant member is inbound.
	BoardReasonHotFlagged BoardReason = "hot.flagged"
	// The newest relevant member is inbound, the user is in its To and its
	// sender is known.
	BoardReasonYouAddressed BoardReason = "you.addressed"
	// The newest relevant member is inbound and answers one of the user's
	// messages (In-Reply-To), whoever sent it.
	BoardReasonYouRepliedToYou BoardReason = "you.repliedToYou"
	// The user's newest message is a reply to an earlier inbound member, to
	// one of its senders (From or Reply-To).
	BoardReasonThemReplied BoardReason = "them.replied"
	// No inbound member counts, and one of the user's newest messages that
	// is not a forward, to someone else, asks a question (a question mark
	// in its own text).
	BoardReasonThemAsked BoardReason = "them.asked"
	// Inbound; the user is only in Cc.
	BoardReasonInfoCcOnly BoardReason = "info.ccOnly"
	// Inbound; the user is not among the To or Cc recipients (a list, a
	// Bcc).
	BoardReasonInfoNotAddressed BoardReason = "info.notAddressed"
	// Inbound and the user in its To, but from a sender the user has never
	// written to (not known); its Importance does not count either.
	BoardReasonInfoUnknownSender BoardReason = "info.unknownSender"
	// A note to oneself: every recipient is one of the user's addresses.
	BoardReasonInfoYourNote BoardReason = "info.yourNote"
	// Jira: the last item that is not an event is the user's comment.
	BoardReasonJiraYourComment BoardReason = "jira.yourComment"
	// Jira: someone else's item on an issue assigned to the user.
	BoardReasonJiraAssigned BoardReason = "jira.assigned"
	// Jira: someone else's item on an issue the user reported.
	BoardReasonJiraReporter BoardReason = "jira.reporter"
	// Jira: someone else's item on an issue the user commented on before.
	BoardReasonJiraCommented BoardReason = "jira.commented"
	// Jira: an issue the user only watches.
	BoardReasonJiraWatching BoardReason = "jira.watching"
	// The rules no longer make the thread a case (the user has acted), but
	// a user state, a remind, a future deadline, an open commitment or a
	// linked suggested reply (a draft that still exists) keeps it;
	// RuleState is the state the rules gave last.
	BoardReasonKept BoardReason = "kept"
)

// BoardVisibility says where a case is listed. Derived by the daemon.
type BoardVisibility string

const (
	BoardLive    BoardVisibility = "live"    // on the board
	BoardDone    BoardVisibility = "done"    // the user marked it done (DoneAt); a later inbound message reopens it
	BoardSnoozed BoardVisibility = "snoozed" // hidden until RemindAt, then live again (and listed until done or reminded again)
)

// BoardCase is a conversation (a mail thread) or an issue (a jira
// account's thread) on the board.
//
// The state a client shows is UserState when set, else Annotation.State
// when the assistant is on (BoardListResult.Assistant), the annotation is
// not stale and carries a state, else RuleState.
type BoardCase struct {
	ID        BoardCaseID `json:"id"`
	AccountID AccountID   `json:"accountId"`
	// ThreadID is the case's thread now (§4.4); a merge of threads, or a
	// move by another client that stores the messages anew, can change
	// it: the case id and the user's decisions stay.
	ThreadID   ThreadID    `json:"threadId"`
	RuleState  BoardState  `json:"ruleState"`
	RuleReason BoardReason `json:"ruleReason"`
	// UserState is the user's own choice (board.setState); absent =
	// automatic.
	UserState *BoardState `json:"userState,omitempty"`
	// Annotation is the assistant's, absent when there is none. Present
	// also when stale (Annotation.Stale); see BoardAnnotation.
	Annotation *BoardAnnotation `json:"annotation,omitempty"`
	Visibility BoardVisibility  `json:"visibility"`
	DoneAt     *time.Time       `json:"doneAt,omitempty"`   // set when Visibility is done
	RemindAt   *time.Time       `json:"remindAt,omitempty"` // set while snoozed, always in the future
	// Subject is the newest relevant member's, Re:/Fwd: stripped as in
	// ThreadSummary; for an issue "KEY: Summary".
	Subject string `json:"subject"`
	// Person is the other party: the sender of the newest inbound relevant
	// member, else the first To recipient of the user's newest member
	// that is not one of the user's addresses. Subject and Person are
	// cleaned but keep their URLs (never a link).
	Person Address `json:"person"`
	// Date is when the newest relevant member arrived: its internal date,
	// else its Date header, else when the daemon stored it; never later
	// than when the daemon stored it, nor than now.
	Date           time.Time `json:"date"`
	Snippet        string    `json:"snippet"` // of the newest relevant member
	Unread         bool      `json:"unread"`  // a relevant member is unread
	HasAttachments bool      `json:"hasAttachments"`
	MessageCount   int       `json:"messageCount"` // relevant members
	// ReplyMessageID is the member a reply answers (draft.create reply):
	// the newest inbound relevant member, else the newest relevant member.
	// On a jira account a reply is a comment on the issue.
	ReplyMessageID  MessageID `json:"replyMessageId"`
	ReplyFolderID   FolderID  `json:"replyFolderId"`
	LatestMessageID MessageID `json:"latestMessageId"` // the newest relevant member
	// Issue is set for a case of a jira account.
	Issue *BoardIssue `json:"issue,omitempty"`
	// CanArchive: board.archive would move messages — the account has the
	// move capability and a folder of role archive, and a member of the
	// case is in the folder of role inbox.
	CanArchive bool `json:"canArchive"`
	// Draft is the suggested reply linked to the case, while that draft
	// exists: linked by the user (board.setDraft) or by an annotation
	// (BoardAnnotateParams.DraftID). The link is the case's, not the
	// annotation's: it needs no annotation, survives a stale one and a
	// later one without a draft, and is listed whatever the assistant
	// preference.
	Draft *BoardDraft `json:"draft,omitempty"`
	// Version changes whenever anything above changes, including the
	// members board.get returns; clients cache board.get by (id, version).
	Version int64 `json:"version"`
}

// BoardIssue is the issue behind a case of a jira account. Status is
// untrusted display text from the site.
type BoardIssue struct {
	Key            string              `json:"key"` // "ITSD-42"
	Status         string              `json:"status"`
	StatusCategory IssueStatusCategory `json:"statusCategory,omitempty"`
}

// BoardDraft is a draft linked to a case.
type BoardDraft struct {
	DraftID DraftID `json:"draftId"`
	// Text is the draft's plain text, at most MaxBoardDraftTextBytes
	// (cut at a character boundary).
	Text    string    `json:"text"`
	Updated time.Time `json:"updated"`
}

// BoardAnnotation is what an assistant made of a case (board.annotate).
// Every string is text an assistant wrote after reading mail: untrusted,
// cleaned by the daemon (control and bidi characters and URLs removed),
// shown only as plain text and never as the daemon's or the user's own
// words.
//
// Stale: a member was added, removed or got its body since the annotation
// was made. A client then uses none of it (no state, title, summary,
// deadline or tasks) and may say that the notes are outdated; the case's
// Draft stays. board.queue offers the case again.
type BoardAnnotation struct {
	State   *BoardState `json:"state,omitempty"` // absent: the assistant left the state to the rules
	Title   string      `json:"title"`           // one line, ≤ MaxBoardTitleBytes; "" = use the subject
	Summary string      `json:"summary"`         // a block (line breaks kept), ≤ MaxBoardSummaryBytes
	Why     string      `json:"why"`             // one line, ≤ MaxBoardWhyBytes: why the case is in its state
	Tasks   []string    `json:"tasks"`           // never null; ≤ MaxBoardTasks lines of ≤ MaxBoardTaskBytes
	// Due is a deadline the daemon verified against a verbatim quote of a
	// member (§4.13 "Quotes"); absent when there is none.
	Due *BoardDue `json:"due,omitempty"`
	// Source names the assistant (≤ MaxBoardSourceBytes, e.g. a model
	// name), untrusted text from the bridge.
	Source string    `json:"source"`
	At     time.Time `json:"at"` // when it was made
	Stale  bool      `json:"stale,omitempty"`
}

// BoardDue is a deadline with the quote it comes from.
type BoardDue struct {
	At time.Time `json:"at"`
	// Quote is the sentence of the message the deadline comes from, as
	// found in it: MinBoardQuoteBytes..MaxBoardQuoteBytes. A client always
	// shows it next to the date.
	Quote     string    `json:"quote"`
	MessageID MessageID `json:"messageId"` // a member of the case's thread
}

// BoardCommitmentState is where a commitment stands.
type BoardCommitmentState string

const (
	CommitmentOpen   BoardCommitmentState = "open"
	CommitmentDone   BoardCommitmentState = "done"   // the user ticked it off
	CommitmentClosed BoardCommitmentState = "closed" // closed by the daemon, see ClosedReason
)

// Reasons the daemon closes a commitment by itself.
const (
	CommitmentClosedReplied = "replied" // the user wrote a message newer than any they had written when it was recorded
	CommitmentClosedDone    = "done"    // the case was marked done
)

// BoardCommitment is something the user promised in one of their own
// messages, as an assistant found it (board.commit). Text is the
// assistant's wording (untrusted, plain text); Quote is verbatim from the
// user's own text of MessageID.
type BoardCommitment struct {
	ID        BoardCommitmentID    `json:"id"`
	CaseID    BoardCaseID          `json:"caseId"`
	AccountID AccountID            `json:"accountId"`
	MessageID MessageID            `json:"messageId"` // the user's message it is in
	Text      string               `json:"text"`      // one line, ≤ MaxBoardCommitmentTextBytes
	Quote     string               `json:"quote"`
	Due       *time.Time           `json:"due,omitempty"`
	State     BoardCommitmentState `json:"state"`
	// ClosedReason is set for State closed: CommitmentClosedReplied or
	// CommitmentClosedDone. An open enum.
	ClosedReason string    `json:"closedReason,omitempty"`
	At           time.Time `json:"at"` // when it was recorded
}

// BoardMessage is one member of a case as board.get shows it: plain text
// only, never HTML.
type BoardMessage struct {
	ID       MessageID `json:"id"`
	FolderID FolderID  `json:"folderId"`
	From     Address   `json:"from"`
	Date     time.Time `json:"date"`
	Mine     bool      `json:"mine"` // in a folder of role sent or outbox
	// Text is the message's own text: the quoted history cut off as
	// message.body with trimQuoted cuts it (the stored plain text cut by
	// the text rules when they find the quote in it, else the text of the
	// HTML part when its quoted history was cut, else the stored text
	// whole), then the signature (after "\n-- \n") cut off, cleaned, at
	// most MaxBoardMessageTextBytes. In doubt the text stays whole.
	Text string `json:"text"`
	// Trimmed: something was cut off Text (quoted history, signature or
	// the length cap).
	Trimmed bool `json:"trimmed,omitempty"`
}

// BoardTrigger says what started a triage run.
type BoardTrigger string

const (
	TriggerManual   BoardTrigger = "manual"   // the user pressed Triage
	TriggerAuto     BoardTrigger = "auto"     // the client's automatic schedule
	TriggerExternal BoardTrigger = "external" // annotations without a runId (Claude Desktop, Claude Code); never passed to board.runStart
)

// BoardRunError is the class of a failed triage run, never free text.
// An open enum for readers; board.runEnd stores any value it does not
// know as RunFailed.
type BoardRunError string

const (
	RunCancelled BoardRunError = "cancelled"
	RunTimeout   BoardRunError = "timeout"
	RunSignedOut BoardRunError = "signedOut"
	RunFailed    BoardRunError = "failed"
)

// BoardRun is a triage run as board.list reports it.
type BoardRun struct {
	At        time.Time     `json:"at"`                // when it started
	EndedAt   *time.Time    `json:"endedAt,omitempty"` // absent while it runs
	Trigger   BoardTrigger  `json:"trigger"`
	Source    string        `json:"source"`
	Annotated int           `json:"annotated"`       // cases annotated in it
	Error     BoardRunError `json:"error,omitempty"` // absent after a success
}

// BoardTriage is the state of triage the client's status line and
// automatic schedule need.
type BoardTriage struct {
	// LastRun is a run still open if there is one (the newest by start),
	// else the run with the latest activity (its end; an external run's
	// latest call), of any trigger; absent before the first.
	LastRun *BoardRun `json:"lastRun,omitempty"`
	// AnnotatedTodayAuto counts cases annotated by runs with trigger auto
	// that started today (the daemon's local day).
	AnnotatedTodayAuto int `json:"annotatedTodayAuto"`
	// Queue counts the live cases board.queue would offer: in triage
	// accounts, without an annotation or with a stale one. 0 while the
	// assistant is off.
	Queue int `json:"queue"`
	// Usage24h sums the token usage of the runs that ended within the 24
	// hours before board.list answered (the daemon's clock, by each run's
	// end) and carry usage (board.runEnd with usage); absent when none
	// does. Computed when board.list answers: it shrinks as runs age out
	// of the window without a notification.
	Usage24h *BoardUsageTotal `json:"usage24h,omitempty"`
}

// BoardUsage is the token usage of a triage run as the client's assistant
// reported it, each counter 0..MaxBoardUsageTokens.
type BoardUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
}

// Valid reports whether no counter is negative.
func (u BoardUsage) Valid() bool {
	return u.InputTokens >= 0 && u.OutputTokens >= 0 && u.CacheCreationInputTokens >= 0 && u.CacheReadInputTokens >= 0
}

// Clamped returns u with every counter brought into 0..MaxBoardUsageTokens.
func (u BoardUsage) Clamped() BoardUsage {
	c := func(n int64) int64 { return min(max(n, 0), MaxBoardUsageTokens) }
	return BoardUsage{InputTokens: c(u.InputTokens), OutputTokens: c(u.OutputTokens),
		CacheCreationInputTokens: c(u.CacheCreationInputTokens), CacheReadInputTokens: c(u.CacheReadInputTokens)}
}

// BoardUsageTotal is BoardUsage summed over Runs runs (BoardTriage.Usage24h).
type BoardUsageTotal struct {
	BoardUsage
	Runs int `json:"runs"` // the runs that contributed, ≥ 1
}

// BoardWindows are how long cases of each state stay on the board, in
// days from Date, 1..MaxBoardWindowDays. A user state, a remind (also one
// that came due, until the user marks the case done or sets another), a
// future deadline or an open commitment keeps a case regardless.
type BoardWindows struct {
	Hot  int `json:"hot"`
	You  int `json:"you"`
	Them int `json:"them"`
	Info int `json:"info"`
}

// BoardPreferences are the board's daemon-side preferences
// (board.preferences, board.setPreferences; not part of Preferences).
type BoardPreferences struct {
	// Enabled: the daemon computes the board. Default true.
	Enabled bool `json:"enabled"`
	// Assistant: annotations count and board.queue hands out mail text.
	// Default false; a client turns it on only after the user's consent.
	Assistant bool         `json:"assistant"`
	Windows   BoardWindows `json:"windows"` // default 90/30/30/14
	// TriageAccounts limits triage to these accounts; empty = every
	// enabled mail account (kind imap or graph). A jira account is
	// triaged only when listed. Never null.
	TriageAccounts []AccountID `json:"triageAccounts"`
	// AutoTriage: the client runs triage on its own schedule. Default
	// false. The daemon only stores it.
	AutoTriage bool `json:"autoTriage"`
	// AutoTriageMinutes is the least time between automatic runs,
	// MinBoardAutoTriageMinutes..MaxBoardAutoTriageMinutes, default 30.
	AutoTriageMinutes int `json:"autoTriageMinutes"`
	// AutoTriageDailyCases caps the cases automatic runs annotate per local
	// day, 0..MaxBoardAutoTriageDailyCases, default 60; 0 = none.
	AutoTriageDailyCases int `json:"autoTriageDailyCases"`
}

// Limits and defaults of the board. Byte counts are UTF-8 bytes after the
// daemon's cleaning.
const (
	MaxBoardCases                    = 1000 // board.list
	MaxBoardMessages                 = 50   // board.get, the newest
	MaxBoardMessageTextBytes         = 8000 // BoardMessage.Text: a whole ordinary mail; board.get ≤ 50 × 8000 bytes of text
	MaxBoardDraftTextBytes           = 4000 // BoardDraft.Text
	DefaultBoardQueueLimit           = 3
	MaxBoardQueueLimit               = 5
	MaxBoardQueueMessages            = 8        // per case, the newest
	MaxBoardQueueMessageBytes        = 3000     // BoardQueueMessage.Text
	MaxBoardQueueCaseBytes           = 12 << 10 // the texts of one case together
	MaxBoardTitleBytes               = 300      // one line
	MaxBoardWhyBytes                 = 400      // one line
	MaxBoardSummaryBytes             = 2000     // a block
	MaxBoardTasks                    = 10       // lines
	MaxBoardTaskBytes                = 300      // each, one line
	MaxBoardCommitmentTextBytes      = 300      // one line
	MaxBoardSourceBytes              = 64       // one line
	MinBoardQuoteBytes               = 10       // and at least MinBoardQuoteChars characters that are not spaces
	MinBoardQuoteChars               = 10
	MaxBoardQuoteBytes               = 300
	MaxBoardWindowDays               = 365
	MaxBoardRemind                   = 365 * 24 * time.Hour // board.remind: until ≤ now + this
	BoardDueEarliest                 = -24 * time.Hour      // a deadline lies within these of when its message arrived (as BoardCase.Date)
	BoardDueLatest                   = 400 * 24 * time.Hour
	DefaultBoardHotDays              = 90
	DefaultBoardYouDays              = 30
	DefaultBoardThemDays             = 30
	DefaultBoardInfoDays             = 14
	MinBoardAutoTriageMinutes        = 5
	MaxBoardAutoTriageMinutes        = 1440
	DefaultBoardAutoTriageMinutes    = 30
	MaxBoardAutoTriageDailyCases     = 1000
	DefaultBoardAutoTriageDailyCases = 60
	MaxBoardUsageTokens              = 1_000_000_000_000 // each counter of BoardUsage; larger values are clamped
)

// DefaultBoardPreferences returns the preferences of a store that never
// had any set.
func DefaultBoardPreferences() BoardPreferences {
	return BoardPreferences{
		Enabled: true,
		Windows: BoardWindows{
			Hot: DefaultBoardHotDays, You: DefaultBoardYouDays,
			Them: DefaultBoardThemDays, Info: DefaultBoardInfoDays,
		},
		TriageAccounts:       []AccountID{},
		AutoTriageMinutes:    DefaultBoardAutoTriageMinutes,
		AutoTriageDailyCases: DefaultBoardAutoTriageDailyCases,
	}
}

// QuoteField names what a quoteNotFound error is about (QuoteNotFoundData).
type QuoteField string

const (
	QuoteFieldDue        QuoteField = "due"        // BoardAnnotateParams.Due.Quote
	QuoteFieldCommitment QuoteField = "commitment" // BoardCommitParams.Quote
)

// QuoteNotFoundData is Error.Data of quoteNotFound.
type QuoteNotFoundData struct {
	Field QuoteField `json:"field"`
}

// --- board.list ------------------------------------------------------------

type BoardListParams struct {
	AccountIDs []AccountID `json:"accountIds,omitempty"` // empty = every enabled account
}

type BoardListResult struct {
	// Cases: live, done and snoozed, newest Date first, at most
	// MaxBoardCases (the newest). Never null.
	Cases []BoardCase `json:"cases"`
	// Commitments: the open commitments of the live cases listed. Never
	// null.
	Commitments []BoardCommitment `json:"commitments"`
	Enabled     bool              `json:"enabled"`   // BoardPreferences.Enabled
	Assistant   bool              `json:"assistant"` // BoardPreferences.Assistant
	Triage      BoardTriage       `json:"triage"`
	// Ready: the daemon has evaluated every thread once since the board
	// was enabled or its rules changed; until then Cases may be partial.
	Ready     bool `json:"ready"`
	Truncated bool `json:"truncated,omitempty"` // more than MaxBoardCases
}

// --- board.get -------------------------------------------------------------

type BoardGetParams struct {
	CaseID BoardCaseID `json:"caseId"`
}

type BoardGetResult struct {
	Case BoardCase `json:"case"`
	// Messages are the case's relevant members, the newest
	// MaxBoardMessages, oldest first. Never null.
	Messages []BoardMessage `json:"messages"`
}

// --- the user's decisions ----------------------------------------------------

type BoardSetStateParams struct {
	CaseID BoardCaseID `json:"caseId"`
	State  *BoardState `json:"state"` // null or absent = back to automatic
}

type BoardSetStateResult struct {
	Case BoardCase `json:"case"`
}

type BoardSetDoneParams struct {
	CaseID BoardCaseID `json:"caseId"`
	Done   bool        `json:"done"`
}

type BoardSetDoneResult struct {
	Case BoardCase `json:"case"`
}

type BoardRemindParams struct {
	CaseID BoardCaseID `json:"caseId"`
	// Until must lie in the future and within MaxBoardRemind; null or
	// absent = remind no more (live again).
	Until *time.Time `json:"until"`
}

type BoardRemindResult struct {
	Case BoardCase `json:"case"`
}

type BoardArchiveParams struct {
	CaseID BoardCaseID `json:"caseId"`
}

type BoardArchiveResult struct {
	Archived  int       `json:"archived"`            // messages moved to the archive folder
	NoArchive bool      `json:"noArchive,omitempty"` // the account cannot archive: only marked done
	Case      BoardCase `json:"case"`
}

// BoardUnflagParams asks board.unflag to clear the flag (the star) of
// every copy of the case's members whose flag the rules read
// (BoardReasonHotFlagged).
type BoardUnflagParams struct {
	CaseID BoardCaseID `json:"caseId"`
}

type BoardUnflagResult struct {
	Case      BoardCase `json:"case"`      // as stored; the rules judge it again afterwards
	Unflagged int       `json:"unflagged"` // rows whose flag was cleared; 0 is no error
}

type BoardDiscardDraftParams struct {
	CaseID BoardCaseID `json:"caseId"`
}

type BoardDiscardDraftResult struct {
	Case BoardCase `json:"case"`
}

// BoardSetDraftParams links a draft to a case as its suggested reply
// (board.setDraft): a draft of the case's account that replies to a member
// of the case, as BoardAnnotateParams.DraftID.
type BoardSetDraftParams struct {
	CaseID  BoardCaseID `json:"caseId"`
	DraftID DraftID     `json:"draftId"`
}

type BoardSetDraftResult struct {
	Case BoardCase `json:"case"` // with Draft
}

// --- triage (the MCP bridge under --allow-triage) ----------------------------

type BoardQueueParams struct {
	AccountIDs []AccountID   `json:"accountIds,omitempty"` // empty = every triage account
	CaseIDs    []BoardCaseID `json:"caseIds,omitempty"`    // empty = any; others are skipped
	Limit      int           `json:"limit,omitempty"`      // 0 = DefaultBoardQueueLimit; 1..MaxBoardQueueLimit
}

type BoardQueueResult struct {
	Items     []BoardQueueItem `json:"items"`     // never null; newest Date first
	Remaining int              `json:"remaining"` // further cases the queue would offer
}

// BoardQueueItem is a case handed to an assistant: everything it may read
// of it. Every string but the ids and InputKey comes from mail.
type BoardQueueItem struct {
	CaseID    BoardCaseID `json:"caseId"`
	AccountID AccountID   `json:"accountId"`
	// InputKey names the members the item was built from (the ids and
	// body states of the members that count; a new version of the rules
	// alone does not change it); board.annotate and board.commit pass it
	// back and are refused (conflict) when the case changed since.
	InputKey       string      `json:"inputKey"`
	RuleState      BoardState  `json:"ruleState"`
	RuleReason     BoardReason `json:"ruleReason"`
	UserState      *BoardState `json:"userState,omitempty"`
	Subject        string      `json:"subject"`
	ReplyMessageID MessageID   `json:"replyMessageId"` // for create_draft mode reply
	Issue          *BoardIssue `json:"issue,omitempty"`
	// Own lists the user's addresses on the account (the account's and the
	// senders of its sent folders), lower case.
	Own      []string            `json:"own"`
	Messages []BoardQueueMessage `json:"messages"` // the newest MaxBoardQueueMessages, oldest first
	// HasDraft: the case already links a draft that exists (BoardCase
	// .Draft); a draftId passed to board.annotate is then not linked.
	HasDraft bool `json:"hasDraft,omitempty"`
}

// BoardQueueMessage is one member of a BoardQueueItem.
type BoardQueueMessage struct {
	MessageID MessageID `json:"messageId"`
	From      Address   `json:"from"`
	To        []Address `json:"to,omitempty"`
	Cc        []Address `json:"cc,omitempty"`
	Date      time.Time `json:"date"`
	Mine      bool      `json:"mine"`
	// Text as BoardMessage.Text but from the stored plain text only (the
	// quoted history the plain-text rules find cut off, never the HTML
	// part's), at most MaxBoardQueueMessageBytes and
	// MaxBoardQueueCaseBytes for the item's texts together.
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"` // something was cut off Text
}

type BoardAnnotateParams struct {
	CaseID   BoardCaseID `json:"caseId"`
	InputKey string      `json:"inputKey"`
	// RunID counts the call in that run; absent, unknown or ended = in
	// the implicit external run of Source and the day.
	RunID   BoardRunID  `json:"runId,omitempty"`
	State   *BoardState `json:"state,omitempty"`
	Title   string      `json:"title,omitempty"`
	Summary string      `json:"summary,omitempty"`
	Why     string      `json:"why,omitempty"`
	Tasks   []string    `json:"tasks,omitempty"`
	Due     *BoardDue   `json:"due,omitempty"`
	// DraftID links a draft of the case's account that replies to a member
	// of the case (draft.create reply), unless the case already links
	// another draft that exists (BoardAnnotateResult.DraftNotLinked); ""
	// leaves the case's link as it is.
	DraftID DraftID `json:"draftId,omitempty"`
	Source  string  `json:"source"`
}

type BoardAnnotateResult struct {
	Case BoardCase `json:"case"`
	// DraftNotLinked: the annotation was stored, but DraftID was not
	// linked because the case links another draft that exists (the user's
	// or an earlier annotation's). The draft passed stays among the
	// account's drafts, unlinked.
	DraftNotLinked bool `json:"draftNotLinked,omitempty"`
}

type BoardCommitParams struct {
	CaseID    BoardCaseID `json:"caseId"`
	InputKey  string      `json:"inputKey"`
	RunID     BoardRunID  `json:"runId,omitempty"`
	MessageID MessageID   `json:"messageId"` // a member of the case that is the user's own
	Text      string      `json:"text"`
	Quote     string      `json:"quote"`
	Due       *time.Time  `json:"due,omitempty"`
	Source    string      `json:"source"`
}

type BoardCommitResult struct {
	Commitment BoardCommitment `json:"commitment"`
}

type BoardSetCommitmentParams struct {
	CommitmentID BoardCommitmentID `json:"commitmentId"`
	Done         bool              `json:"done"` // false reopens a done or closed one
}

type BoardSetCommitmentResult struct {
	Commitment BoardCommitment `json:"commitment"`
}

// --- preferences -------------------------------------------------------------

type BoardPreferencesParams struct{}

type BoardPreferencesResult struct {
	Preferences BoardPreferences `json:"preferences"`
}

// BoardSetPreferencesParams replaces every preference: a client sends the
// object board.preferences gave it with its changes.
type BoardSetPreferencesParams struct {
	Preferences BoardPreferences `json:"preferences"`
}

type BoardSetPreferencesResult struct {
	Preferences BoardPreferences `json:"preferences"` // as stored
}

// --- runs --------------------------------------------------------------------

type BoardRunStartParams struct {
	Trigger BoardTrigger `json:"trigger"` // manual or auto
	Source  string       `json:"source"`
}

type BoardRunStartResult struct {
	RunID BoardRunID `json:"runId"`
}

type BoardRunEndParams struct {
	RunID BoardRunID    `json:"runId"`
	Error BoardRunError `json:"error,omitempty"` // absent = success
	// Usage is the run's token usage; absent = unknown. Stored only when
	// this call ends the run (ignored like the rest on a run that ended).
	Usage *BoardUsage `json:"usage,omitempty"`
}

type BoardRunEndResult struct{}

// --- notification ------------------------------------------------------------

// BoardChangedNotification says that what board.list returns changed for
// these accounts (absent = any). Clients list again.
type BoardChangedNotification struct {
	AccountIDs []AccountID `json:"accountIds,omitempty"`
}
