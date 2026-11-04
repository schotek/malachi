// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// The assistant profile (docs/api.md §4.18; the design is
// docs/plans/assistant-profile.md): what the user wrote about themselves
// for an assistant, the grammatical gender they write in, a note for
// each mail and Jira account, whether the MCP bridge shares it with apps
// other than Malachi Mail, and the notes an assistant proposed and the
// user has not accepted or dismissed yet. Every string is the user's own
// (or, for a proposal, an assistant's) plain text, cleaned by the
// daemon: no control, bidi or invisible characters, no line that looks
// like the bridge's fence of untrusted content.

// ---------------------------------------------------------------------------
// Limits, in characters (Unicode code points) of the cleaned text.
// Exceeding one is invalidArgument; nothing is cut silently.
// ---------------------------------------------------------------------------

const (
	MaxProfileText        = 2000 // AssistantProfile.Text
	MaxProfileAccountNote = 500  // ProfileAccountNote.Note
	MaxProfileProposal    = 300  // ProfileProposal.Text
	MaxProfileProposals   = 20   // pending proposals; the oldest goes
)

// ProfileGender is the grammatical gender the user writes in.
type ProfileGender string

const (
	GenderUnset     ProfileGender = "unset"
	GenderMasculine ProfileGender = "masculine"
	GenderFeminine  ProfileGender = "feminine"
	GenderNeutral   ProfileGender = "neutral"
)

// Valid reports one of the four values.
func (g ProfileGender) Valid() bool {
	switch g {
	case GenderUnset, GenderMasculine, GenderFeminine, GenderNeutral:
		return true
	}
	return false
}

// ProfileSource says where a proposal came from.
type ProfileSource string

const (
	// ProfileFromPanel is the assistant panel of Malachi Mail (a bridge
	// started with --in-app).
	ProfileFromPanel ProfileSource = "panel"
	// ProfileFromMCP is another MCP client (Claude Desktop, Claude Code).
	ProfileFromMCP ProfileSource = "mcp"
)

// ProfileNoteKinds are the account kinds that can have a note: an
// assistant writes in the user's name only in mail and Jira comments.
var ProfileNoteKinds = []AccountKind{AccountIMAP, AccountGraph, AccountJira}

// ProfileAccountNote is the note of one account.
type ProfileAccountNote struct {
	AccountID AccountID `json:"accountId"`
	Note      string    `json:"note"`
}

// AssistantProfile is the stored profile. Accounts lists only accounts
// that exist and have a note, in account.list order.
type AssistantProfile struct {
	Text     string               `json:"text"`
	Gender   ProfileGender        `json:"gender"`
	Accounts []ProfileAccountNote `json:"accounts"`
	// ShareWithMCP: the MCP bridge gives the profile to MCP clients other
	// than Malachi Mail itself. Default true.
	ShareWithMCP bool       `json:"shareWithMcp"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// ProfileProposal is a note an assistant proposed for the profile; it
// becomes part of it only when the user accepts it
// (assistant.profileResolve).
type ProfileProposal struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// AccountID: a note for this account; absent: the global text.
	AccountID AccountID     `json:"accountId,omitempty"`
	Source    ProfileSource `json:"source"`
	CreatedAt time.Time     `json:"createdAt"`
}

// ProfileLimits are the limits of the profile, for a client's counters.
type ProfileLimits struct {
	Text        int `json:"text"`
	AccountNote int `json:"accountNote"`
	Proposal    int `json:"proposal"`
}

// DefaultProfileLimits are the limits above.
func DefaultProfileLimits() ProfileLimits {
	return ProfileLimits{Text: MaxProfileText, AccountNote: MaxProfileAccountNote, Proposal: MaxProfileProposal}
}

type AssistantProfileGetParams struct{}

type AssistantProfileGetResult struct {
	Profile   AssistantProfile  `json:"profile"`
	Proposals []ProfileProposal `json:"proposals"`
	Limits    ProfileLimits     `json:"limits"`
}

// AssistantProfileSetParams changes the fields it carries; an absent one
// stays. Accounts sets the note of each account it names ("" removes it)
// and leaves the others.
type AssistantProfileSetParams struct {
	Text         *string              `json:"text,omitempty"`
	Gender       *ProfileGender       `json:"gender,omitempty"`
	Accounts     []ProfileAccountNote `json:"accounts,omitempty"`
	ShareWithMCP *bool                `json:"shareWithMcp,omitempty"`
}

type AssistantProfileSetResult struct {
	Profile AssistantProfile `json:"profile"`
}

type AssistantProfileProposeParams struct {
	Text      string        `json:"text"`
	AccountID AccountID     `json:"accountId,omitempty"`
	Source    ProfileSource `json:"source"`
}

type AssistantProfileProposeResult struct {
	Proposal ProfileProposal `json:"proposal"`
}

// AssistantProfileResolveParams accepts or dismisses a proposal. Text,
// with Accept, is the user's edited text of the proposal.
type AssistantProfileResolveParams struct {
	ID     string  `json:"id"`
	Accept bool    `json:"accept"`
	Text   *string `json:"text,omitempty"`
}

type AssistantProfileResolveResult struct {
	Profile   AssistantProfile  `json:"profile"`
	Proposals []ProfileProposal `json:"proposals"`
}

// AssistantProfileChangedNotification: the profile or its proposals
// changed; clients run assistant.profileGet again.
type AssistantProfileChangedNotification struct{}
