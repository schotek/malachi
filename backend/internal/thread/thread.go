// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package thread decides how messages group into conversations.
//
// The store keeps one thread id per message and links a message as it is
// written: every thread its In-Reply-To or References name, every twin
// with the same Message-ID and every stored message that names it become
// one thread. That is a union, not the JWZ tree: it is idempotent, does not
// depend on the order messages arrive in and has no cycles to break. This
// package holds the policy of that union (Resolve) and the subject
// normalisation the listing shows; the SQL lives in the store.
//
// Message-IDs are attacker-controlled. They are linking hints only, matched
// exactly, never an identity, and the caps here bound what a crafted
// message can do: a thread never grows past MaxThreadSize by merging (the
// rest forms further threads), a lookup never reads more than MaxLinkRows
// rows, and the parser keeps at most 50 References. There is no subject
// fallback: two messages that share a subject but no header link stay
// apart, which loses a badly built reply now and then and never merges
// strangers. Threads are per account; every query is bound by the account.
//
// A server-threaded account (Microsoft Graph) stores the server's
// conversation id. Such ids carry no IDPrefix; Resolve never moves or
// merges them and only lets local messages join them.
package thread

import (
	"sort"
	"strings"
	"unicode"
)

// IDPrefix starts every locally assigned thread id ("t_" + 32 hex).
const IDPrefix = "t_"

// IsLocalID reports whether id was assigned locally, as opposed to a
// server's conversation id.
func IsLocalID(id string) bool { return strings.HasPrefix(id, IDPrefix) }

// Policy caps the linking work.
type Policy struct {
	// MaxThreadSize is the most members a merge may produce; a merge that
	// would exceed it is skipped and the message keeps its own thread.
	// 0 means no cap.
	MaxThreadSize int
	// MaxLinkRows is how many candidate rows one lookup query reads.
	MaxLinkRows int
}

// DefaultPolicy is what the daemon runs with. Legitimate conversations
// rarely pass a hundred messages; 500 bounds both the rows a merge rewrites
// and the reach of forged References.
var DefaultPolicy = Policy{MaxThreadSize: 500, MaxLinkRows: 512}

// Link says how a candidate thread is connected to the message, best first.
type Link int

const (
	LinkInReplyTo Link = iota // the message's In-Reply-To names a member
	LinkReference             // References names a member; Pos counts from the newest end
	LinkTwin                  // a member has the same Message-ID
	LinkChild                 // a member's In-Reply-To or References names the message
)

// Candidate is a thread the message is linked to.
type Candidate struct {
	ThreadID string
	Size     int // members, as counted in the store
	Link     Link
	Pos      int // LinkReference: distance from the newest reference (0 = nearest)
}

// Plan says which threads become one: every thread in Absorb takes the id
// Canonical.
type Plan struct {
	Canonical string
	Absorb    []string
}

// Resolve decides the merge for a message in thread own (ownSize members)
// linked to cands. ok is false when nothing changes: own is a server id,
// there is no candidate, or the cap leaves nothing to merge.
//
// A server id among the candidates wins (the best linked one); only local
// threads then join it and other server threads stay as they are. Among
// local threads the largest wins, so the fewest rows are rewritten; on a
// tie a candidate beats the message's own thread (the newcomer joins, the
// existing conversation keeps its id) and the smaller id beats the other.
// The message's own thread must fit under the cap or nothing moves;
// further candidates join greedily in link order while they fit.
func Resolve(own string, ownSize int, cands []Candidate, p Policy) (Plan, bool) {
	if !IsLocalID(own) {
		return Plan{}, false
	}
	best := make(map[string]Candidate, len(cands))
	for _, c := range cands {
		if c.ThreadID == "" || c.ThreadID == own {
			continue
		}
		if b, seen := best[c.ThreadID]; !seen || rankLess(c, b) {
			best[c.ThreadID] = c
		}
	}
	if len(best) == 0 {
		return Plan{}, false
	}
	list := make([]Candidate, 0, len(best))
	for _, c := range best {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return rankLess(list[i], list[j]) })

	canonical, size := "", 0
	for _, c := range list {
		if !IsLocalID(c.ThreadID) {
			canonical, size = c.ThreadID, c.Size
			break
		}
	}
	if canonical == "" {
		for _, c := range list {
			if canonical == "" || c.Size > size || (c.Size == size && c.ThreadID < canonical) {
				canonical, size = c.ThreadID, c.Size
			}
		}
		if ownSize > size {
			canonical, size = own, ownSize
		}
	}

	fits := func(add int) bool { return p.MaxThreadSize <= 0 || size+add <= p.MaxThreadSize }
	var absorb []string
	if canonical != own {
		if !fits(ownSize) {
			return Plan{}, false
		}
		size += ownSize
		absorb = append(absorb, own)
	}
	for _, c := range list {
		if c.ThreadID == canonical || !IsLocalID(c.ThreadID) || !fits(c.Size) {
			continue
		}
		size += c.Size
		absorb = append(absorb, c.ThreadID)
	}
	if len(absorb) == 0 {
		return Plan{}, false
	}
	return Plan{Canonical: canonical, Absorb: absorb}, true
}

// rankLess orders candidates best link first, then nearest reference, then
// by id so the order is stable.
func rankLess(a, b Candidate) bool {
	if a.Link != b.Link {
		return a.Link < b.Link
	}
	if a.Pos != b.Pos {
		return a.Pos < b.Pos
	}
	return a.ThreadID < b.ThreadID
}

// replyPrefixes are the reply and forward markers NormalizeSubject strips:
// the English forms and the localised ones Outlook writes. Nothing groups
// by the result, so being generous costs nothing.
var replyPrefixes = map[string]bool{
	"re": true, "fw": true, "fwd": true,
	"aw": true, "wg": true, // German
	"sv": true, "vs": true, // Swedish, Finnish
	"tr":  true,              // French
	"rv":  true,              // Spanish
	"res": true, "enc": true, // Portuguese
	"odp": true, "pd": true, // Polish
	"ynt": true, // Turkish
}

// maxPrefixes bounds the loop; a subject with more stacked markers keeps
// the rest.
const maxPrefixes = 16

// NormalizeSubject strips leading reply and forward markers ("Re:",
// "Fwd:", "AW:", "Re[2]:" and the like, repeated) for display, so a thread
// does not rename itself as replies arrive. Mailing-list tags stay. The
// result is trimmed; a subject that was only markers comes back empty.
func NormalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	for range maxPrefixes {
		rest, ok := stripReplyPrefix(s)
		if !ok {
			break
		}
		s = rest
	}
	return s
}

// stripReplyPrefix removes one marker: up to four ASCII letters from
// replyPrefixes, an optional "[n]" or "(n)" counter, and the colon.
func stripReplyPrefix(s string) (string, bool) {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	i := 0
	for i < len(s) && i < 4 && isASCIILetter(s[i]) {
		i++
	}
	if i == 0 || (i < len(s) && isASCIILetter(s[i])) || !replyPrefixes[strings.ToLower(s[:i])] {
		return "", false
	}
	rest := strings.TrimLeft(s[i:], " \t")
	if len(rest) > 0 && (rest[0] == '[' || rest[0] == '(') {
		closing := byte(']')
		if rest[0] == '(' {
			closing = ')'
		}
		j := 1
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j == 1 || j >= len(rest) || rest[j] != closing {
			return "", false
		}
		rest = strings.TrimLeft(rest[j+1:], " \t")
	}
	if len(rest) == 0 || rest[0] != ':' {
		return "", false
	}
	return strings.TrimLeftFunc(rest[1:], unicode.IsSpace), true
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
