// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package ingest decides what of a downloaded message is stored on this
// device and stores it: the whole message, or a skeleton of it whose large
// attachments stay on the mail server (Preferences.AttachmentOfflineDays,
// docs/api.md §4.8). The IMAP and Graph syncers store every body through
// Store, message.download stores the whole message through it, and the
// background pass reduces messages that aged past the policy with Strip.
//
// The rule is conservative on purpose: only a part the message could lose
// without the reader noticing is ever left on the server (an attachment of
// at least api.LargeAttachmentMinBytes that the HTML body does not show),
// only for messages the server still has, and only when the skeleton
// provably shows everything the original shows (mime.VerifySkeleton).
// Any doubt stores the whole message.
package ingest

import (
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// HydratedKeep is how long a message message.download made whole stays
// whole before the background pass may reduce it again: the user who
// opened an attachment is likely to open it again soon.
const HydratedKeep = 7 * 24 * time.Hour

// MaxMessageBytes caps the messages whose bodies are downloaded, by the
// syncers and by message.download alike; a larger one is never stored
// (bodyState tooBig). It is also the most mime.Parse reads.
const MaxMessageBytes = 25 << 20

// Policy is what Preferences.AttachmentOfflineDays says about keeping large
// attachments on this device: 0 keeps every one, N > 0 those of messages
// received in the last N days, api.AttachmentOfflineNone (-1) none.
type Policy struct {
	AttachmentOfflineDays int
}

// Cutoff is the start of the window whose messages keep their large
// attachments: midnight UTC of the day N days before now, the same day
// boundary the syncers' retention window uses. The zero time when the
// policy has no window (0 keeps everything, -1 keeps nothing).
func (p Policy) Cutoff(now time.Time) time.Time {
	if p.AttachmentOfflineDays <= 0 {
		return time.Time{}
	}
	return time.Date(now.Year(), now.Month(), now.Day()-p.AttachmentOfflineDays, 0, 0, 0, 0, time.UTC)
}

// keepsNone reports the policy that keeps no large attachment locally.
func (p Policy) keepsNone() bool { return p.AttachmentOfflineDays == api.AttachmentOfflineNone }

// Target is the stored message a decision is about.
type Target struct {
	AccountID, MessageID string
	// Role is the role of the message's folder: Drafts and the outbox
	// always keep everything.
	Role api.FolderRole
	// HasServerCopy: the message has a server identity (an IMAP UID, a
	// Graph id) that message.download can fetch it back by.
	HasServerCopy bool
	// InternalDate is when the server received the message, Date its Date
	// header (chosen by the sender); the age is judged by the first that is
	// set, and a message with neither counts as new.
	InternalDate, Date time.Time
	// HydratedAt is when message.download last made the message whole;
	// zero if never.
	HydratedAt time.Time
}

// age is the moment the message's age is judged by; false when it has no
// date at all.
func (t Target) age() (time.Time, bool) {
	switch {
	case !t.InternalDate.IsZero():
		return t.InternalDate, true
	case !t.Date.IsZero():
		return t.Date, true
	}
	return time.Time{}, false
}

// Plan is what Decide found.
type Plan struct {
	// Omit names the parts (Attachment.PartID) to leave on the server now;
	// empty keeps the whole message.
	Omit map[string]bool
	// OmitBytes is the decoded size of the parts in Omit.
	OmitBytes int64
	// CandidateBytes is the decoded size of every part the rule could
	// leave on the server once the message is old enough, whether Omit
	// holds it now or not; 0 means the message never loses anything.
	CandidateBytes int64
}

// Decide applies the rule to a parsed message (the whole one, never a
// skeleton). The candidates are the attachments of at least
// api.LargeAttachmentMinBytes whose Content-ID, if any, the HTML body does
// not reference (mime.CIDReferences; when that list may be incomplete,
// every part with a Content-ID counts as referenced); a truncated parse
// and a signed or encrypted message have none. The candidates are left on
// the server only when the download is not on demand, the folder is not
// Drafts or the outbox, the server has a copy, the grace after an
// on-demand download (HydratedKeep) is over, and the policy keeps no
// large attachment or the message is older than its cutoff.
func Decide(p *mime.Parsed, t Target, pol Policy, now time.Time, onDemand bool) Plan {
	var plan Plan
	if p == nil || p.Truncated || p.Crypto {
		return plan
	}
	var (
		refs        map[string]bool
		refsLoaded  bool
		refsPartial bool
		candidates  []api.Attachment
	)
	for _, a := range p.Attachments {
		if a.PartID == "" || a.Size < api.LargeAttachmentMinBytes ||
			a.PartID == p.TextPartID || a.PartID == p.HTMLPartID {
			continue
		}
		if a.ContentID != "" {
			if !refsLoaded {
				var complete bool
				refs, complete = mime.CIDReferences(p.RawHTML)
				refsLoaded, refsPartial = true, !complete
			}
			if refsPartial || refs[mime.NormalizeCID(a.ContentID)] {
				continue
			}
		}
		candidates = append(candidates, a)
		plan.CandidateBytes += a.Size
	}
	if len(candidates) == 0 || !omitNow(t, pol, now, onDemand) {
		return plan
	}
	plan.Omit = make(map[string]bool, len(candidates))
	for _, a := range candidates {
		if !plan.Omit[a.PartID] {
			plan.Omit[a.PartID] = true
			plan.OmitBytes += a.Size
		}
	}
	return plan
}

// omitNow is the part of the rule about the message rather than its parts.
func omitNow(t Target, pol Policy, now time.Time, onDemand bool) bool {
	switch {
	case onDemand, !t.HasServerCopy:
		return false
	case t.Role == api.RoleDrafts || t.Role == api.RoleOutbox:
		return false
	case !t.HydratedAt.IsZero() && !t.HydratedAt.Before(now.Add(-HydratedKeep)):
		return false
	case pol.keepsNone():
		return true
	case pol.AttachmentOfflineDays > 0:
		at, ok := t.age()
		return ok && at.Before(pol.Cutoff(now))
	}
	return false
}
