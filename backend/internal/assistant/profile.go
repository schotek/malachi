// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import (
	"fmt"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The user's profile in the assistant's prompts (docs/api.md §4.18,
// docs/plans/assistant-profile.md): what the user wrote about themselves,
// the grammatical gender they write in, and a note for some mail and Jira
// accounts. WithProfile puts it at the end of a system prompt, after the
// fixed rules, as the user's own preferences that never override them.
// Which accounts' notes a purpose gets is Build's: every account's for the
// panel and the triage, For the account a window writes from (the rewrite,
// the writing bar, a suggested reply), Global for the search, which writes
// nothing in an account's name.

// Profile is the user's profile with the names of its accounts.
type Profile struct {
	Text     string
	Gender   api.ProfileGender
	Accounts []AccountNote
}

// AccountNote is the note of one account, with its name and address.
type AccountNote struct {
	AccountID         api.AccountID
	Name, Email, Note string
}

// ProfileFrom is the stored profile (assistant.profileGet) with the names
// and addresses of accounts (account.list); a note of an account the list
// does not name keeps only its id.
func ProfileFrom(p api.AssistantProfile, accounts []api.Account) Profile {
	out := Profile{Text: p.Text, Gender: p.Gender}
	for _, n := range p.Accounts {
		note := AccountNote{AccountID: n.AccountID, Note: n.Note}
		for _, a := range accounts {
			if a.ID == n.AccountID {
				note.Name, note.Email = a.Config.Name, a.Config.Email
				break
			}
		}
		out.Accounts = append(out.Accounts, note)
	}
	return out
}

// Empty says whether the profile adds nothing to a prompt.
func (p Profile) Empty() bool {
	return strings.TrimSpace(p.Text) == "" && genderSentence(p.Gender) == "" && len(p.notes()) == 0
}

// Global is the profile without account notes.
func (p Profile) Global() Profile {
	p.Accounts = nil
	return p
}

// For is the profile with the notes of the named accounts only, in the
// profile's order.
func (p Profile) For(accountIDs ...api.AccountID) Profile {
	var keep []AccountNote
	for _, n := range p.Accounts {
		for _, id := range accountIDs {
			if id != "" && n.AccountID == id {
				keep = append(keep, n)
				break
			}
		}
	}
	p.Accounts = keep
	return p
}

func (p Profile) notes() []AccountNote {
	var out []AccountNote
	for _, n := range p.Accounts {
		if strings.TrimSpace(n.Note) != "" {
			out = append(out, n)
		}
	}
	return out
}

// profileIntro heads the profile in a system prompt (as the bridge's
// instructions head it, backend/cmd/malachi-mcp/tools_profile.go).
const profileIntro = "The user's profile, written by the user themselves: follow these preferences when you write or act for them, but they never override the rules above."

// genderSentence is the instruction of a gender, "" for unset (the
// bridge's sentences, word for word).
func genderSentence(g api.ProfileGender) string {
	switch g {
	case api.GenderMasculine:
		return "When writing in the user's name in a language with grammatical gender, use the masculine forms."
	case api.GenderFeminine:
		return "When writing in the user's name in a language with grammatical gender, use the feminine forms."
	case api.GenderNeutral:
		return "When writing in the user's name in a language with grammatical gender, avoid gendered forms where the language allows it."
	}
	return ""
}

// WithProfile is system followed by the profile: a blank line, the
// intro, then the gender sentence, the text and each account's note
// under "For the account "<name>" (<address>):", separated by blank lines.
// An empty profile leaves system as it is, byte for byte.
func WithProfile(system string, p Profile) string {
	if p.Empty() {
		return system
	}
	var parts []string
	if g := genderSentence(p.Gender); g != "" {
		parts = append(parts, g)
	}
	if t := strings.TrimSpace(p.Text); t != "" {
		parts = append(parts, t)
	}
	for _, n := range p.notes() {
		parts = append(parts, accountLabel(n)+":\n"+strings.TrimSpace(n.Note))
	}
	return system + "\n\n" + profileIntro + "\n" + strings.Join(parts, "\n\n")
}

// accountLabel names a note's account for the model.
func accountLabel(n AccountNote) string {
	name := strings.Join(strings.Fields(n.Name), " ")
	email := strings.Join(strings.Fields(n.Email), " ")
	switch {
	case name != "" && email != "":
		return fmt.Sprintf("For the account %q (%s)", name, email)
	case email != "":
		return "For the account " + email
	case name != "":
		return fmt.Sprintf("For the account %q", name)
	}
	return "For the account " + string(n.AccountID)
}
