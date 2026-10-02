// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"reflect"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The board's input: the cases a source hands over (a conversation or an
// issue with the state it is in, what the assistant made of it and what
// the user decided), the user's commitments the assistant found, and the
// cleaning every string of them goes through before it reaches a view
// model. A case is mail, so every string here is hostile input: the view
// model shows only what CleanLine and CleanBlock let through.
//
// The macOS client leads (MalachiCore/Board/BoardCase.swift); this is its
// port. The types are values: a Case handed out by a source is never
// changed through its pointers (Issue, Annotation, UserState, Reply,
// Draft) or slices; a change makes new ones.

// CaseID identifies a case: the daemon's api.BoardCaseID ("c_" and 32 hex
// digits), or "sample-n" for the invented samples. Opaque; "" is no case.
type CaseID = api.BoardCaseID

// StateFromAPI is the board's state of a state on the wire; false for a
// value this client does not know (the enum is closed, but a newer daemon
// is no reason to fail).
func StateFromAPI(s api.BoardState) (State, bool) {
	switch s {
	case api.BoardHot:
		return StateHot, true
	case api.BoardYou:
		return StateYou, true
	case api.BoardThem:
		return StateThem, true
	case api.BoardInfo:
		return StateInfo, true
	}
	return StateInfo, false
}

// API is the state on the wire (board.setState).
func (s State) API() api.BoardState {
	switch s {
	case StateHot:
		return api.BoardHot
	case StateYou:
		return api.BoardYou
	case StateThem:
		return api.BoardThem
	}
	return api.BoardInfo
}

// statePtr is s as an optional state.
func statePtr(s State) *State { return &s }

// KnownReasons are the rule codes of docs/api.md §4.13 this client has a
// text of its own for (Reason); any other code reads ReasonUnknown.
var KnownReasons = []api.BoardReason{
	api.BoardReasonHotImportant, api.BoardReasonHotFlagged, api.BoardReasonYouAddressed,
	api.BoardReasonYouRepliedToYou, api.BoardReasonThemReplied, api.BoardReasonThemAsked,
	api.BoardReasonInfoCcOnly, api.BoardReasonInfoNotAddressed, api.BoardReasonInfoUnknownSender,
	api.BoardReasonInfoYourNote, api.BoardReasonJiraYourComment, api.BoardReasonJiraAssigned,
	api.BoardReasonJiraReporter, api.BoardReasonJiraCommented, api.BoardReasonJiraWatching, api.BoardReasonKept,
}

// AccountInfo is an account a case belongs to, as the board names it
// (Board.AccountInfo).
type AccountInfo struct {
	ID   api.AccountID
	Name string
	// Badge is the kind capsule ("JIRA", "M365"; the sidebar's
	// accountHeaderBadge).
	Badge string
	// CanReply: a reply can be written in it, a mail account's reply or an
	// issue tracker's comment (the reply or comment capability).
	CanReply bool
}

// IssueInfo is the issue behind a case of a Jira account.
type IssueInfo struct {
	Key, Status string
	Style       jira.StatusStyle
}

// ReplyTarget is the message a reply to a case answers and the folder it
// is in (replyMessageId, replyFolderId): what Reply opens and Show in Mail
// selects. On a Jira account the reply is a comment.
type ReplyTarget struct {
	Message api.MessageID
	Folder  api.FolderID
}

// VisibilityKind is where a case is listed.
type VisibilityKind int

// The kinds.
const (
	// VisibleLive is on the board.
	VisibleLive VisibilityKind = iota
	// VisibleDone: the user marked it done.
	VisibleDone
	// VisibleSnoozed is hidden until Visibility.At, then live again (the
	// source reports that).
	VisibleSnoozed
)

// Visibility is where a case is listed (Board.Visibility). The zero value
// is live.
type Visibility struct {
	Kind VisibilityKind
	// At is when the case was marked done (VisibleDone; zero when the
	// source does not know) or when it comes back (VisibleSnoozed).
	At time.Time
}

// IsLive reports a case on the board.
func (v Visibility) IsLive() bool { return v.Kind == VisibleLive }

// IsDone reports a case marked done.
func (v Visibility) IsDone() bool { return v.Kind == VisibleDone }

// RemindAt is when a snoozed case comes back; false when it is not
// snoozed.
func (v Visibility) RemindAt() (time.Time, bool) {
	if v.Kind == VisibleSnoozed {
		return v.At, true
	}
	return time.Time{}, false
}

// Equal reports the same visibility, the times compared as instants.
func (v Visibility) Equal(o Visibility) bool {
	return v.Kind == o.Kind && v.At.Equal(o.At)
}

// CaseMessage is one message of a case's conversation (board.get).
type CaseMessage struct {
	// ID and Folder are "" for the invented samples.
	ID     api.MessageID
	Folder api.FolderID
	From   string
	Date   time.Time
	// Text is plain text, never markup.
	Text string
	// Mine: the user wrote it.
	Mine bool
	// Trimmed: the quoted history, the signature or the rest past the cap
	// was cut off Text.
	Trimmed bool
}

// Annotation is what the assistant made of a case. Text an assistant
// wrote: shown only as plain text and always as the assistant's.
type Annotation struct {
	// State is nil when the assistant left the state to the rules.
	State *State
	// Title is a short title of its own; the subject otherwise.
	Title   string
	Summary string
	// Why the case is where it is.
	Why string
	// Due is the deadline, nil when there is none; DueQuote the sentence
	// it comes from and DueMessage the message the sentence is in.
	Due        *time.Time
	DueQuote   string
	DueMessage api.MessageID
	Tasks      []string
	// Source names the assistant (a model name); "" when unknown.
	Source string
	// At is when it was made; zero for the samples.
	At time.Time
	// Stale: a message was added, removed or got its body since; none of
	// it counts (AnnotationOf), only the case's draft stays.
	Stale bool
}

// DraftLink is the suggested reply linked to a case: a real draft of the
// case's account, local to the board, never sent by itself. It outlives a
// stale annotation.
type DraftLink struct {
	ID api.DraftID
	// Text is its plain text.
	Text string
}

// Case is a case as the source has it (Board.Case).
type Case struct {
	ID      CaseID
	Account api.AccountID
	// Thread is the case's thread now (a merge can change it, ID stays);
	// "" for the samples.
	Thread api.ThreadID
	// Person is the other party (the sender, the reporter).
	Person string
	// Date is the latest activity.
	Date           time.Time
	Subject        string
	Snippet        string
	Unread         bool
	HasAttachments bool
	MessageCount   int
	Issue          *IssueInfo
	// RuleState is the daemon's rules' state, RuleReason the code of the
	// rule (Reason; an open set).
	RuleState  State
	RuleReason api.BoardReason
	Annotation *Annotation
	// UserState is the user's own choice; nil = automatic.
	UserState  *State
	Visibility Visibility
	// Reply is what a reply answers; nil for the samples.
	Reply *ReplyTarget
	// LatestMessage is the newest message that counts; "" for the samples.
	LatestMessage api.MessageID
	// CanArchive: Archive would move messages (else it only marks the case
	// done).
	CanArchive bool
	Draft      *DraftLink
	// Messages is the conversation, oldest first, once MessagesLoaded
	// (DataSource.LoadMessages); a later version may still show the
	// conversation of an earlier one until it arrives.
	Messages       []CaseMessage
	MessagesLoaded bool
	// MessagesFailed: loading the conversation failed (and nothing was
	// loaded before).
	MessagesFailed bool
	// Version changes whenever the case does (the daemon's version).
	Version int64
}

// Done reports a case marked done.
func (c Case) Done() bool { return c.Visibility.IsDone() }

// SetDone moves the case to done (when unknown) or back on the board; a
// case already where done says stays as it is (its date, or its remind).
func (c *Case) SetDone(done bool) {
	if done == c.Visibility.IsDone() {
		return
	}
	if done {
		c.Visibility = Visibility{Kind: VisibleDone}
	} else {
		c.Visibility = Visibility{}
	}
}

// CommitmentState is where a commitment stands.
type CommitmentState int

// The states.
const (
	CommitmentOpen CommitmentState = iota
	// CommitmentDone: the user ticked it off.
	CommitmentDone
	// CommitmentClosed: the daemon closed it (the user replied, the case
	// was done).
	CommitmentClosed
)

// Commitment is something the user promised, as the assistant found it in
// a case.
type Commitment struct {
	ID     api.BoardCommitmentID
	CaseID CaseID
	Text   string
	// Quote is the sentence it comes from.
	Quote string
	Due   *time.Time
	// Message is the user's message the sentence is in; "" for the
	// samples.
	Message api.MessageID
	// State: only open ones are shown.
	State CommitmentState
}

// Run is the assistant's last pass over the board (board.list
// triage.lastRun).
type Run struct {
	// Model names the assistant (the run's source).
	Model string
	// Date is when it ended, or started while it runs.
	Date time.Time
	Note string
	// Annotated counts the cases it annotated.
	Annotated int
	// Running: it has not ended yet.
	Running bool
	// Error is the class of its failure (api.BoardRunError); "" after a
	// success.
	Error string
	// Trigger is who started it (api.BoardTrigger: manual, auto,
	// external); "" when not known.
	Trigger string
	// Started is when it started; zero when not known.
	Started time.Time
}

// TriageInfo is what the triage needs to know (board.list triage), for the
// triage's controller and schedule (ui/internal/boardtriage).
type TriageInfo struct {
	// Queue counts the live cases waiting for the assistant (0 while it is
	// off).
	Queue int
	// AnnotatedToday counts the cases automatic runs annotated today.
	AnnotatedToday int
	// Usage24h is the tokens triage runs used in the last 24 hours, as the
	// daemon summed them when it listed; nil when no run reported any.
	Usage24h *api.BoardUsageTotal
}

// IsFailure reports a board that could not be listed: the cases shown are
// old or none (unavailable, failed, unsupported).
func (p Phase) IsFailure() bool {
	switch p {
	case PhaseUnavailable, PhaseFailed, PhaseUnsupported:
		return true
	}
	return false
}

// Snapshot is everything a source knows at one moment (Board.Snapshot).
// Its zero value is the empty board in PhaseLoading; EmptySnapshot is the
// empty board that is ready (macOS Snapshot.empty).
type Snapshot struct {
	Accounts    []AccountInfo
	Cases       []Case
	Commitments []Commitment
	// Annotated: the assistant is on and its annotations count.
	Annotated bool
	Run       *Run
	Phase     Phase
	Triage    TriageInfo
	// Truncated: more cases than the daemon lists (the oldest left out).
	Truncated bool
}

// EmptySnapshot is no account, no case, the assistant off, ready.
func EmptySnapshot() Snapshot { return Snapshot{Phase: PhaseReady} }

// Equal reports the same snapshot, field by field (the sources report a
// snapshot only when it changed).
func (s Snapshot) Equal(o Snapshot) bool { return reflect.DeepEqual(s, o) }

// Case is the case with id, false when the snapshot has none.
func (s Snapshot) Case(id CaseID) (Case, bool) {
	for _, c := range s.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return Case{}, false
}

// AnnotationOf is the annotation that counts: none while the assistant is
// off or when it is stale.
func AnnotationOf(c Case, annotated bool) *Annotation {
	if !annotated || c.Annotation == nil || c.Annotation.Stale {
		return nil
	}
	return c.Annotation
}

// StateOf is the state a case shows (docs/api.md §4.13): the user's
// choice, else the assistant's (when annotations count, the annotation is
// not stale and has one), else the rules'.
func StateOf(c Case, annotated bool) State {
	if c.UserState != nil {
		return *c.UserState
	}
	if a := AnnotationOf(c, annotated); a != nil && a.State != nil {
		return *a.State
	}
	return c.RuleState
}

// StateSourceOf is who decided StateOf. An annotation that leaves the
// state to the rules kept it; a stale one counts as none.
func StateSourceOf(c Case, annotated bool) Source {
	if c.UserState != nil {
		return Source{Kind: SourceUser}
	}
	if !annotated {
		return Source{Kind: SourceAssistantOff}
	}
	a := AnnotationOf(c, annotated)
	if a == nil {
		return Source{Kind: SourceRules}
	}
	if a.State == nil || *a.State == c.RuleState {
		return Source{Kind: SourceAssistantKept}
	}
	return Source{Kind: SourceAssistantChanged, From: c.RuleState}
}

// The caps of the cleaned strings, in UTF-8 bytes (or a count).
const (
	capPerson   = 200
	capTitle    = 300
	capSnippet  = 400
	capReason   = 400
	capAccount  = 120
	capBadge    = 64
	capIssueKey = 64
	capStatus   = 64
	capQuote    = 300
	capTask     = 300
	capTasks    = 20
	// capTaskScan is the tasks looked at to find capTasks non-empty ones.
	capTaskScan    = 200
	capCommitments = 100
	capCommitment  = 300
	capModel       = 64
	capNote        = 300
	capSummary     = 2000
	capDraft       = 4000
	capMessage     = api.MaxBoardMessageTextBytes
	capMessages    = 50
)

// CleanLine makes one line of display text safe: it drops invalid UTF-8
// (and the replacement character), control and format characters (Cc, Cf:
// NUL, bidirectional overrides such as U+202E, zero-width characters),
// turns every whitespace (line breaks, tabs, U+2028, U+2029) into a space,
// collapses runs of spaces, trims, and caps the result at maxBytes UTF-8
// bytes on a character boundary (no grapheme cluster the cut broke is
// kept). It stops reading once the cap is passed, or after 8 × maxBytes
// input characters when most of them are dropped, so a huge input costs no
// more than a short one.
func CleanLine(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var out []byte
	limit := maxBytes
	space := false
	left := budget(maxBytes)
	for _, r := range s {
		if left == 0 {
			// Out of budget: the text ends here. r goes along past the
			// limit only so the cut can tell whether it broke a character.
			limit = min(limit, len(out))
			if !space && kept(r) {
				out = utf8.AppendRune(out, r)
			}
			break
		}
		left--
		if r == utf8.RuneError {
			continue
		}
		if unicode.IsSpace(r) {
			space = len(out) > 0
			continue
		}
		if dropped(r) {
			continue
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = utf8.AppendRune(out, r)
		if len(out) > maxBytes {
			break
		}
	}
	return capped(out, limit)
}

// CleanBlock makes a block of display text safe: like CleanLine, but line
// breaks stay (CR LF, CR, NEL, VT, FF, U+2028 and U+2029 become "\n"),
// other whitespace becomes a space, spaces at the end of a line go, at
// most one empty line is kept in a row, and the block is trimmed.
func CleanBlock(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var out []byte
	limit := maxBytes
	spaces, breaks := 0, 0
	afterCR := false
	left := budget(maxBytes)
	for _, r := range s {
		if left == 0 {
			// Out of budget, as in CleanLine.
			limit = min(limit, len(out))
			if spaces == 0 && breaks == 0 && kept(r) {
				out = utf8.AppendRune(out, r)
			}
			break
		}
		left--
		cr := afterCR
		afterCR = false
		if r == utf8.RuneError {
			continue
		}
		if unicode.IsSpace(r) {
			switch r {
			case '\n':
				if !cr {
					breaks++
				}
				spaces = 0
			case '\r':
				breaks++
				spaces = 0
				afterCR = true
			case '\v', '\f', 0x85, 0x2028, 0x2029:
				breaks++
				spaces = 0
			default:
				spaces++
			}
			continue
		}
		if dropped(r) {
			continue
		}
		if len(out) > 0 {
			// A run of whitespace is bounded by the cap: it is cut anyway.
			for range min(breaks, 2) {
				out = append(out, '\n')
			}
			for range min(spaces, maxBytes) {
				out = append(out, ' ')
			}
		}
		breaks, spaces = 0, 0
		out = utf8.AppendRune(out, r)
		if len(out) > maxBytes {
			break
		}
	}
	return capped(out, limit)
}

// budget is how many input characters the cleaners read for a cap of
// maxBytes bytes: what they keep stops them at the cap, so only dropped
// characters and whitespace run into this.
func budget(maxBytes int) int {
	const most = int(^uint(0) >> 1)
	if maxBytes > most/8 {
		return most
	}
	return maxBytes * 8
}

// kept reports whether the cleaners keep r as it is (not whitespace, not
// dropped).
func kept(r rune) bool {
	return r != utf8.RuneError && !unicode.IsSpace(r) && !dropped(r)
}

// dropped reports the control and format characters the cleaners drop
// (Cc, Cf, Zl, Zp).
func dropped(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
}

// capped is out cut to at most n bytes on a character boundary, the
// whitespace before the cut trimmed. A cut inside a grapheme cluster (a
// letter whose combining mark is past it, one regional indicator of a
// flag) drops the whole cluster. What follows the cut is still in out, so
// the boundary is that of the whole text.
func capped(out []byte, n int) string {
	end := 0
	for end < len(out) {
		_, w := utf8.DecodeRune(out[end:])
		if end+w > n {
			break
		}
		end += w
	}
	for end > 0 && !graphemeBoundary(out, end) {
		_, w := utf8.DecodeLastRune(out[:end])
		end -= w
	}
	for end > 0 && (out[end-1] == ' ' || out[end-1] == '\n') {
		end--
	}
	return string(out[:end])
}

// graphemeBoundary reports whether a cut at byte i of b (a rune boundary of
// cleaned text) falls between two grapheme clusters, by the rules of UAX
// #29 that cleaned text can still meet: no break before a combining or
// spacing mark or an emoji modifier (GB9, GB9a), inside a Hangul syllable
// (GB6–GB8) or inside a pair of regional indicators (GB12, GB13); always
// one at a line break. Prepend characters and Indic conjuncts are not
// considered (a cut there keeps a little more).
func graphemeBoundary(b []byte, i int) bool {
	if i <= 0 || i >= len(b) {
		return true
	}
	prev, _ := utf8.DecodeLastRune(b[:i])
	next, _ := utf8.DecodeRune(b[i:])
	if prev == '\n' || next == '\n' {
		return true
	}
	if hangulJoins(hangulKind(prev), hangulKind(next)) {
		return false
	}
	if extends(next) {
		return false
	}
	if regionalIndicator(prev) && regionalIndicator(next) {
		n := 0
		for j := i; j > 0; {
			r, w := utf8.DecodeLastRune(b[:j])
			if !regionalIndicator(r) {
				break
			}
			n++
			j -= w
		}
		return n%2 == 0
	}
	return true
}

// extends reports a character that belongs to the cluster before it: a
// mark (Mn, Me, Mc and Other_Grapheme_Extend), an emoji modifier, or one of
// the two spacing marks that are letters (Thai and Lao AM).
func extends(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc, unicode.Other_Grapheme_Extend) ||
		(r >= 0x1F3FB && r <= 0x1F3FF) || r == 0x0E33 || r == 0x0EB3
}

func regionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// The Hangul syllable types of UAX #29.
const (
	hangulNone = iota
	hangulL
	hangulV
	hangulT
	hangulLV
	hangulLVT
)

func hangulKind(r rune) int {
	switch {
	case (r >= 0x1100 && r <= 0x115F) || (r >= 0xA960 && r <= 0xA97C):
		return hangulL
	case (r >= 0x1160 && r <= 0x11A7) || (r >= 0xD7B0 && r <= 0xD7C6):
		return hangulV
	case (r >= 0x11A8 && r <= 0x11FF) || (r >= 0xD7CB && r <= 0xD7FB):
		return hangulT
	case r >= 0xAC00 && r <= 0xD7A3:
		if (r-0xAC00)%28 == 0 {
			return hangulLV
		}
		return hangulLVT
	}
	return hangulNone
}

func hangulJoins(p, n int) bool {
	switch p {
	case hangulL:
		return n == hangulL || n == hangulV || n == hangulLV || n == hangulLVT
	case hangulLV, hangulV:
		return n == hangulV || n == hangulT
	case hangulLVT, hangulT:
		return n == hangulT
	}
	return false
}
