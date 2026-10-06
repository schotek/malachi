// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/thread"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Bounds of the work Evaluate does on one thread, whatever the mail holds.
const (
	maxAskScan        = 10     // the user's newest messages looked at for them.asked
	maxRecipientsScan = 10_000 // addresses of one list looked at
	maxReplyToScan    = 16     // Reply-To addresses of one message looked at
	maxReferencesScan = 64     // References identifiers looked at
	maxMessageIDBytes = 998    // a longer Message-ID is no Message-ID
	maxSubjectBytes   = 1000   // Verdict.Subject
	maxNameBytes      = 256    // Verdict.Person.Name
)

// entry is one message of a thread: the copies sharing a Message-ID
// merged, with what the rules derived.
type entry struct {
	Member           // the representative copy
	mine   bool      // a copy is the user's (Mine; on jira also AuthorID)
	at     time.Time // Arrival of the representative
	stored time.Time // the earliest StoredAt of the copies
	// firstStored is the earliest StoredAt of every row of the thread with
	// the message's Message-ID, also rows that do not count (NewestInbound).
	firstStored time.Time
	unread      bool      // a copy is unread
	flagged     bool      // the user flagged a copy
	copies      []*Member // every copy merged into the entry
	attached    bool      // the representative has attachments (any copy for the user's)
}

// Evaluate applies the rules to a thread of the account identified by id
// at time now. It never fails: a thread nothing in which counts gets a
// Verdict with State "" and no derived fields.
//
// Copies sharing a Message-ID are one message. When a copy is the user's
// (Mine), the message is the user's and that copy represents it; otherwise
// the copy stored first represents it, and a later copy (a forged twin)
// never changes its bulk classification, From, To, Importance or arrival.
//
// Mail:
//   - Members count when not hidden, not virtual, not in a folder of role
//     trash, junk or drafts, and either the user's (Mine) or classified as
//     not bulk. While any relevant inbound member is not yet classified,
//     Pending is set and no state is given.
//   - A note to self (note) is a message of the user's (Mine) whose
//     recipients, To, Cc and Bcc, are at least one and all of the user's
//     own addresses (Identity.Self: any account's). It counts (Count,
//     Members, Unread, flags, known senders) but never decides: the state
//     is decided, and Date, LatestID and Subject are taken, from the
//     counting messages without notes. A thread of nothing but notes is
//     no case.
//   - Newest deciding member inbound: hot.flagged (any counting member
//     flagged), info.yourNote (from one of the user's addresses, any
//     account's, to nothing but such addresses), hot.important (the user in its To, a known
//     sender and its own Importance: high or X-Priority 1/2),
//     you.repliedToYou (its In-Reply-To names one of the user's messages),
//     you.addressed (the user in To and a known sender), info.unknownSender
//     (the user in To, the sender unknown), info.ccOnly (in Cc), else
//     info.notAddressed. A sender is known when its From or a Reply-To
//     address is one of id's known correspondents or a recipient of one of
//     the user's counting messages in the thread.
//   - Newest deciding member the user's: never a case when it is shaped
//     like a forward; them.replied when its To names a sender (From or
//     Reply-To) of an earlier inbound counting member; them.asked when the
//     thread has no inbound counting member and one of the user's newest
//     deciding messages that is not a forward, to someone else, has a
//     question mark in its own text; otherwise no case.
//
// Jira (Thread.Issue set): no case while the user's id is unknown, or when
// the issue is done or closed; events never count; the newest counting
// item decides: the user's → them (jira.yourComment); someone else's →
// you when the user is the assignee, the reporter or wrote an item
// before, info when the user only watches, else no case.
func Evaluate(t Thread, id Identity, now time.Time) Verdict {
	jira := t.Issue != nil
	counting, pending := countingOf(t, id, now)
	deciding := counting
	if !jira {
		deciding = withoutNotes(counting, id)
	}
	var v Verdict
	fill(&v, counting, deciding, id)
	if !jira && !pending {
		v.texts = textMembers(deciding)
	}
	switch {
	case jira:
		v.State, v.Reason = jiraRule(t.Issue, counting, id)
	case pending:
		v.Pending = true
	case len(deciding) == 0:
	case deciding[len(deciding)-1].mine:
		v.State, v.Reason = mineRule(deciding, id)
	default:
		v.State, v.Reason = inboundRule(counting, deciding[len(deciding)-1], id)
	}
	return v
}

// withoutNotes returns the counting messages that are no note to self,
// in their order (counting itself when none is).
func withoutNotes(counting []*entry, id Identity) []*entry {
	n := 0
	for _, e := range counting {
		if note(e, id) {
			n++
		}
	}
	if n == 0 {
		return counting
	}
	out := make([]*entry, 0, len(counting)-n)
	for _, e := range counting {
		if !note(e, id) {
			out = append(out, e)
		}
	}
	return out
}

// note reports whether e is a note to self: the user's (Mine; a forged
// inbound "from me to me" never is), with at least one recipient in To,
// Cc and Bcc together and every one of them one of the user's own
// addresses on any account (Identity.Self). A recipient without an
// address, or more recipients than are looked at, make no note.
func note(e *entry, id Identity) bool {
	if !e.mine {
		return false
	}
	seen := 0
	for _, list := range [][]api.Address{e.To, e.Cc, e.Bcc} {
		for _, a := range list {
			if seen++; seen > maxRecipientsScan || !id.Self(a.Address) {
				return false
			}
		}
	}
	return seen > 0
}

// countingOf returns the messages of t that count, oldest first, and
// whether a relevant inbound message waits for its bulk classification.
func countingOf(t Thread, id Identity, now time.Time) (counting []*entry, pending bool) {
	jira := t.Issue != nil
	relevant := prepare(t, id, now, jira)
	counting = relevant[:0:0]
	for _, e := range relevant {
		switch {
		case jira || e.mine || e.Bulk == BulkNone:
			counting = append(counting, e)
		case e.Bulk == BulkUnclassified:
			pending = true
		}
	}
	return counting, pending
}

// FlaggedCopies lists the members whose flag the rules read for
// hot.flagged: every flagged copy of a message that counts, as Evaluate
// merges copies (not hidden, not virtual, not in trash, junk or drafts;
// a message counts when it is the user's or its representative copy is
// classified as no bulk mail; on Jira every item but events), and of a
// message that waits for its classification (it counts once classified
// as no bulk mail, and must not bring the star back then). Clearing their
// flags is what takes the star away from the thread; a flagged copy in
// the trash, or of a message that does not count, is not listed. In the
// order of the members.
func FlaggedCopies(t Thread, id Identity, now time.Time) []api.MessageID {
	jira := t.Issue != nil
	want := map[api.MessageID]bool{}
	for _, e := range prepare(t, id, now, jira) {
		if !jira && !e.mine && e.Bulk != BulkNone && e.Bulk != BulkUnclassified {
			continue
		}
		for _, c := range e.copies {
			if c.Flagged {
				want[c.ID] = true
			}
		}
	}
	var out []api.MessageID
	for _, m := range t.Members {
		if want[m.ID] {
			out = append(out, m.ID)
			delete(want, m.ID)
		}
	}
	return out
}

// prepare returns the relevant messages of t, oldest first, copies with one
// Message-ID merged.
func prepare(t Thread, id Identity, now time.Time, jira bool) []*entry {
	// When each Message-ID was first stored, over every row (NewestInbound:
	// a copy that does not count still says the message is not new).
	first := map[string]time.Time{}
	for i := range t.Members {
		m := &t.Members[i]
		if k := normMessageID(m.MessageID); k != "" && !m.StoredAt.IsZero() {
			if f, ok := first[k]; !ok || m.StoredAt.Before(f) {
				first[k] = m.StoredAt
			}
		}
	}
	groups := map[string][]*Member{}
	var order []string
	for i := range t.Members {
		m := &t.Members[i]
		if m.Hidden || m.Virtual {
			continue
		}
		switch m.FolderRole {
		case api.RoleTrash, api.RoleJunk, api.RoleDrafts:
			continue
		}
		if jira && m.IssueKind == api.IssueItemEvent {
			continue
		}
		key := normMessageID(m.MessageID)
		if key == "" {
			key = "\x00" + strconv.Itoa(i) // no Message-ID: its own message
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], m)
	}
	out := make([]*entry, 0, len(order))
	for _, key := range order {
		copies := groups[key]
		e := &entry{}
		var rep *Member
		for _, c := range copies {
			if c.Mine || (jira && c.AuthorID != "" && c.AuthorID == id.JiraUserID) {
				e.mine = true
				if rep == nil || c.ID < rep.ID {
					rep = c
				}
			}
		}
		if rep == nil {
			for _, c := range copies {
				if rep == nil || storedFirst(c, rep) {
					rep = c
				}
			}
		}
		for _, c := range copies {
			e.unread = e.unread || c.Unread
			e.flagged = e.flagged || c.Flagged
			if e.mine {
				e.attached = e.attached || c.HasAttachments
			}
			if !c.StoredAt.IsZero() && (e.stored.IsZero() || c.StoredAt.Before(e.stored)) {
				e.stored = c.StoredAt
			}
		}
		e.Member = *rep
		e.copies = copies
		if !e.mine {
			e.attached = rep.HasAttachments
		}
		e.firstStored = e.stored
		if f, ok := first[key]; ok && (e.firstStored.IsZero() || f.Before(e.firstStored)) {
			e.firstStored = f
		}
		e.at = Arrival(e.Member, now)
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b *entry) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		if c := a.stored.Compare(b.stored); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out
}

// storedFirst reports whether copy a was stored before copy b: by StoredAt
// (an unknown time last), then the inbox copy, then by id.
func storedFirst(a, b *Member) bool {
	switch {
	case a.StoredAt.IsZero() != b.StoredAt.IsZero():
		return !a.StoredAt.IsZero()
	case !a.StoredAt.Equal(b.StoredAt):
		return a.StoredAt.Before(b.StoredAt)
	case (a.FolderRole == api.RoleInbox) != (b.FolderRole == api.RoleInbox):
		return a.FolderRole == api.RoleInbox
	}
	return a.ID < b.ID
}

// normMessageID returns a Message-ID without space and angle brackets, ""
// for none or an over-long one. Case is kept (the local part is
// case-sensitive).
func normMessageID(s string) string {
	if len(s) > maxMessageIDBytes {
		return ""
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return strings.TrimSpace(s)
}

// fill sets the derived fields of v from the counting messages; the
// newest of the deciding ones (counting without notes to self, unless
// there is none) gives Date, LatestID, Subject and the person a message of
// the user's names.
func fill(v *Verdict, counting, deciding []*entry, id Identity) {
	if len(counting) == 0 {
		return
	}
	if len(deciding) == 0 {
		deciding = counting
	}
	latest := deciding[len(deciding)-1]
	v.LatestID = latest.ID
	v.Date = latest.at
	v.Subject = cleanField(thread.NormalizeSubject(capBytes(latest.Subject, maxInputBytes)), maxSubjectBytes)
	v.ReplyID, v.ReplyFolderID = latest.ID, latest.FolderID
	var newestMine *entry
	for i := len(counting) - 1; i >= 0; i-- {
		e := counting[i]
		if !e.mine {
			if v.NewestInboundAt.IsZero() {
				v.NewestInboundAt = e.at
				v.ReplyID, v.ReplyFolderID = e.ID, e.FolderID
				v.Person = CleanAddress(e.From)
			}
			v.inbound = append(v.inbound, arrival{stored: e.firstStored, at: e.at, id: e.MessageID})
		}
	}
	for i := len(deciding) - 1; i >= 0 && newestMine == nil; i-- {
		if deciding[i].mine {
			newestMine = deciding[i]
		}
	}
	if v.NewestInboundAt.IsZero() && newestMine != nil {
		for i, a := range newestMine.To {
			if i >= maxRecipientsScan {
				break
			}
			if !id.Self(a.Address) && strings.TrimSpace(a.Address) != "" {
				v.Person = CleanAddress(a)
				break
			}
		}
	}
	v.Count = len(counting)
	v.Members = make([]Member, len(counting))
	for i, e := range counting {
		m := e.Member
		m.Text, m.OwnText, m.OwnTextSet = "", "", false
		v.Members[i] = m
		v.Unread = v.Unread || e.unread
		v.HasAttachments = v.HasAttachments || e.attached
	}
}

// CleanAddress cleans an address from mail for a board result: the name
// one line of at most 256 bytes, the address at most 320 bytes and "" when
// it holds a space (cleanField: control, format and invisible characters
// removed, URLs kept).
func CleanAddress(a api.Address) api.Address {
	addr := cleanField(a.Address, maxAddrBytes)
	if strings.Contains(addr, " ") {
		addr = ""
	}
	return api.Address{Name: cleanField(a.Name, maxNameBytes), Address: addr}
}

// inboundRule decides a thread whose newest deciding member, latest, is
// inbound; flags, known senders and the user's messages a reply answers
// are read over every counting message, notes to self included.
func inboundRule(counting []*entry, latest *entry, id Identity) (api.BoardState, api.BoardReason) {
	for _, e := range counting {
		if e.flagged {
			return api.BoardHot, api.BoardReasonHotFlagged
		}
	}
	if yourNote(latest, id) {
		return api.BoardInfo, api.BoardReasonInfoYourNote
	}
	inTo := anyOwned(latest.To, id)
	known := knownSender(latest, counting, id)
	if inTo && known && important(latest.Importance, latest.XPriority) {
		return api.BoardHot, api.BoardReasonHotImportant
	}
	if parent := normMessageID(latest.InReplyTo); parent != "" {
		for _, e := range counting {
			if e.mine && normMessageID(e.MessageID) == parent {
				return api.BoardYou, api.BoardReasonYouRepliedToYou
			}
		}
	}
	switch {
	case inTo && known:
		return api.BoardYou, api.BoardReasonYouAddressed
	case inTo:
		return api.BoardInfo, api.BoardReasonInfoUnknownSender
	case anyOwned(latest.Cc, id):
		return api.BoardInfo, api.BoardReasonInfoCcOnly
	}
	return api.BoardInfo, api.BoardReasonInfoNotAddressed
}

// knownSender reports whether the sender of e (its From, or one of its
// Reply-To addresses) is someone the user has written to: a known
// correspondent of id, or a To or Cc recipient of one of the user's
// counting messages of the thread. The user's own addresses are known only
// when they are among those.
func knownSender(e *entry, counting []*entry, id Identity) bool {
	senders := make([]string, 0, 1+min(len(e.ReplyTo), maxReplyToScan))
	if a := normAddr(e.From.Address); a != "" {
		senders = append(senders, a)
	}
	for i, a := range e.ReplyTo {
		if i >= maxReplyToScan {
			break
		}
		if a := normAddr(a.Address); a != "" {
			senders = append(senders, a)
		}
	}
	for _, a := range senders {
		if id.Knows(a) {
			return true
		}
	}
	for _, m := range counting {
		if !m.mine {
			continue
		}
		for _, list := range [][]api.Address{m.To, m.Cc} {
			for i, r := range list {
				if i >= maxRecipientsScan {
					break
				}
				if slices.Contains(senders, normAddr(r.Address)) {
					return true
				}
			}
		}
	}
	return false
}

// yourNote: from one of the user's addresses (any account's, Self) to at
// least one recipient, all of them the user's.
func yourNote(e *entry, id Identity) bool {
	if !id.Self(e.From.Address) || len(e.To)+len(e.Cc) == 0 {
		return false
	}
	for _, list := range [][]api.Address{e.To, e.Cc} {
		for i, a := range list {
			if i >= maxRecipientsScan || !id.Self(a.Address) {
				return false
			}
		}
	}
	return true
}

func anyOwned(list []api.Address, id Identity) bool {
	for i, a := range list {
		if i >= maxRecipientsScan {
			return false
		}
		if id.Owns(a.Address) {
			return true
		}
	}
	return false
}

// maxPriorityBytes bounds a priority header looked at.
const maxPriorityBytes = 64

// important reports whether the message's own header asks for attention:
// Importance: high, or X-Priority 1 or 2 (optionally followed by a
// comment such as "(Highest)").
func important(importance, xPriority string) bool {
	if len(importance) <= maxPriorityBytes && strings.EqualFold(strings.TrimSpace(importance), "high") {
		return true
	}
	if len(xPriority) > maxPriorityBytes {
		return false
	}
	p := strings.TrimSpace(xPriority)
	if p == "" || (p[0] != '1' && p[0] != '2') {
		return false
	}
	return len(p) == 1 || p[1] < '0' || p[1] > '9'
}

// mineRule decides a thread whose newest deciding member is the user's,
// over the deciding messages (counting is them here: notes to self left
// out).
func mineRule(counting []*entry, id Identity) (api.BoardState, api.BoardReason) {
	last := len(counting) - 1
	latest := counting[last]
	if forwardShaped(&latest.Member, startsThread(&latest.Member)) {
		return "", ""
	}
	senders := map[string]bool{}
	inbound := false
	for _, e := range counting[:last] {
		if e.mine {
			continue
		}
		inbound = true
		for _, a := range append([]api.Address{e.From}, capList(e.ReplyTo, maxReplyToScan)...) {
			if a := normAddr(a.Address); a != "" && !id.Owns(a) {
				senders[a] = true
			}
		}
	}
	if inbound {
		for i, a := range latest.To {
			if i >= maxRecipientsScan {
				break
			}
			if senders[normAddr(a.Address)] {
				return api.BoardThem, api.BoardReasonThemReplied
			}
		}
		return "", ""
	}
	for i, checked := last, 0; i >= 0 && checked < maxAskScan; i, checked = i-1, checked+1 {
		e := counting[i]
		if i != last && forwardShaped(&e.Member, startsThread(&e.Member)) {
			continue
		}
		if toSomeoneElse(e.To, id) && hasQuestion(capBytes(OwnText(e.Member), maxOwnTextBytes)) {
			return api.BoardThem, api.BoardReasonThemAsked
		}
	}
	return "", ""
}

// textMembers returns the ids of the members whose text mineRule reads,
// decided from the deciding messages alone, without their text
// (Verdict.TextMembers).
func textMembers(counting []*entry) []api.MessageID {
	n := len(counting)
	if n == 0 || !counting[n-1].mine {
		return nil
	}
	for _, e := range counting[:n-1] {
		if !e.mine {
			return []api.MessageID{counting[n-1].ID} // its forward shape only
		}
	}
	out := make([]api.MessageID, 0, min(n, maxAskScan))
	for i := n - 1; i >= 0 && len(out) < maxAskScan; i-- {
		out = append(out, counting[i].ID)
	}
	return out
}

// startsThread reports whether m starts its thread: it answers nothing (no
// In-Reply-To, no References), whatever its place among the members.
func startsThread(m *Member) bool {
	if normMessageID(m.InReplyTo) != "" {
		return false
	}
	for i, r := range m.References {
		if i >= maxReferencesScan {
			break
		}
		if normMessageID(r) != "" {
			return false
		}
	}
	return true
}

func capList(l []api.Address, n int) []api.Address {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func toSomeoneElse(to []api.Address, id Identity) bool {
	for i, a := range to {
		if i >= maxRecipientsScan {
			return false
		}
		if normAddr(a.Address) != "" && !id.Owns(a.Address) {
			return true
		}
	}
	return false
}

// jiraRule decides an issue (the decisions' narrower rule).
func jiraRule(is *Issue, counting []*entry, id Identity) (api.BoardState, api.BoardReason) {
	me := id.JiraUserID
	if me == "" || is.StatusCategory == api.StatusCategoryDone || is.Closed || len(counting) == 0 {
		return "", ""
	}
	if counting[len(counting)-1].mine {
		return api.BoardThem, api.BoardReasonJiraYourComment
	}
	switch {
	case is.AssigneeID == me:
		return api.BoardYou, api.BoardReasonJiraAssigned
	case is.ReporterID == me:
		return api.BoardYou, api.BoardReasonJiraReporter
	}
	commented := is.Commented
	for _, e := range counting {
		commented = commented || e.mine
	}
	switch {
	case commented:
		return api.BoardYou, api.BoardReasonJiraCommented
	case is.Watching:
		return api.BoardInfo, api.BoardReasonJiraWatching
	}
	return "", ""
}
