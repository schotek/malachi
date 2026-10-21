// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

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
// Which accounts' notes a request gets is the caller's: For the account a
// window writes from, Global for a request that writes nothing in an
// account's name.

// Profile is the user's profile with the names of its accounts.
type Profile struct {
	Text     string
	Gender   api.ProfileGender
	Accounts []AccountNote
}

// AccountNote is the note of one account, with its name and address.
type AccountNote struct {
	AccountID, Name, Email, Note string
}

// ProfileFrom is the profile the daemon returned (assistant.profileGet),
// with the names and addresses of accounts (account.list); a note of an
// account the list does not name keeps only its id.
func ProfileFrom(p api.AssistantProfile, accounts []api.Account) Profile {
	out := Profile{Text: p.Text, Gender: p.Gender}
	for _, n := range p.Accounts {
		note := AccountNote{AccountID: string(n.AccountID), Note: n.Note}
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
func (p Profile) For(accountIDs ...string) Profile {
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
	return "For the account " + n.AccountID
}

// ProfileTools are the bridge's profile tools the panel's conversation
// may use (AllowedTools); never a one-shot request, triage or a suggested
// reply.
var ProfileTools = []string{
	"mcp__malachi__get_user_profile",
	"mcp__malachi__propose_profile_note",
}

// The head of a propose_profile_note result,
// backend/cmd/malachi-mcp/tools_profile.go:
// "profile note proposal <id> is pending; …".
const (
	proposalHead = "profile note proposal "
	proposalTail = " is pending;"
)

// ParseProposalResult reads the proposal a propose_profile_note result
// names (the panel asks only for the results of its own calls, by
// ToolUseID). Only the result's first line counts, and only when it starts
// exactly with the head the bridge writes; the id is non-empty and holds
// no whitespace or control characters. The application reads the
// proposal's text from the daemon (assistant.profileGet), never from the
// result.
func ParseProposalResult(text string) (string, bool) {
	line, _, _ := strings.Cut(text, "\n")
	rest, ok := strings.CutPrefix(line, proposalHead)
	if !ok {
		return "", false
	}
	id, rest, ok := strings.Cut(rest, proposalTail)
	if !ok || !validID(id) || (rest != "" && rest[0] != ' ') {
		return "", false
	}
	return id, true
}

// ProfileStrings are the fixed texts of the profile: the group About You
// of Preferences → AI and the panel's card of a proposal.
type ProfileStrings struct {
	// Preferences.
	Group, GroupDescription, GenderRow       string
	GenderUnset, GenderMasculine             string
	GenderFeminine, GenderNeutral            string
	TextPlaceholder, NotePlaceholder, NoNote string
	ShareRow, ShareSubtitle                  string
	Pending                                  string
	// The panel's menu item and the proposal card.
	EditProfile, ProposalTitle  string
	Save, Edit, Dismiss         string
	FromPanel, FromOtherApp     string
	AccountProposal, ProposalOK string
}

// ProfileTexts returns the fixed texts of the profile, translated.
func ProfileTexts(tr Translator) ProfileStrings {
	return ProfileStrings{
		// TRANSLATORS: A group of Preferences → AI: what the user tells the assistant about themselves.
		Group: tr.T("About You"),
		// TRANSLATORS: The description under the group About You in Preferences → AI.
		GroupDescription: tr.T("The assistant gets this in the app (Claude Code, ChatGPT) and, if you allow it below, in apps connected over MCP; only with your consent."),
		// TRANSLATORS: A row of Preferences → AI → About You: the grammatical gender the user writes in (Czech verbs and adjectives depend on it).
		GenderRow: tr.T("I Write As"),
		// TRANSLATORS: A choice of the row I Write As: the user does not say.
		GenderUnset: tr.T("Not Specified"),
		// TRANSLATORS: A choice of the row I Write As: masculine grammatical forms.
		GenderMasculine: tr.T("A Man"),
		// TRANSLATORS: A choice of the row I Write As: feminine grammatical forms.
		GenderFeminine: tr.T("A Woman"),
		// TRANSLATORS: A choice of the row I Write As: forms without grammatical gender where possible.
		GenderNeutral: tr.T("Gender-Neutral"),
		// TRANSLATORS: Placeholder of the text field in Preferences → AI → About You.
		TextPlaceholder: tr.T("For example: I write briefly · suggest meetings on Mondays and Wednesdays"),
		// TRANSLATORS: Placeholder of an account's note in Preferences → AI → About You.
		NotePlaceholder: tr.T("For example: sign as the studio"),
		// TRANSLATORS: Shown at the end of an account's row in Preferences → AI → About You when it has no note.
		NoNote: tr.T("No Note"),
		// TRANSLATORS: A switch in Preferences → AI → About You.
		ShareRow: tr.T("Send the Profile to Apps over MCP"),
		// TRANSLATORS: The subtitle of the switch Send the Profile to Apps over MCP.
		ShareSubtitle: tr.T("Claude Desktop and other apps registered over MCP; a change applies after they restart."),
		// TRANSLATORS: A heading in Preferences → AI → About You: notes the assistant proposed that the user has not saved or dismissed.
		Pending: tr.T("Suggested by the Assistant"),
		// TRANSLATORS: An item of the assistant panel's menu: opens Preferences → AI → About You.
		EditProfile: tr.T("Edit What the Assistant Knows About You…"),
		// TRANSLATORS: The title of a card in the assistant panel: a note the assistant suggests adding to the user's profile.
		ProposalTitle: tr.T("The assistant suggests remembering:"),
		// TRANSLATORS: A button of a suggested profile note: add it to the profile.
		Save: tr.T("Save"),
		// TRANSLATORS: A button of a suggested profile note: change its text before saving it.
		Edit: tr.T("Edit…"),
		// TRANSLATORS: A button of a suggested profile note: drop it.
		Dismiss: tr.T("Dismiss"),
		// TRANSLATORS: Where a suggested profile note came from: the assistant panel of this app.
		FromPanel: tr.T("From the assistant panel"),
		// TRANSLATORS: Where a suggested profile note came from: another app connected over MCP, such as Claude Desktop.
		FromOtherApp: tr.T("From another app over MCP"),
		// TRANSLATORS: %s is the name of an account: a suggested note for that account.
		AccountProposal: tr.T("For the account %s"),
		// TRANSLATORS: A short confirmation after a suggested note was saved to the profile.
		ProposalOK: tr.T("Saved to your profile"),
	}
}

// ProfileCounter is the counter under a profile field, "12 / 2000".
func ProfileCounter(tr Translator, n, limit int) string {
	// TRANSLATORS: The character counter under a text field: %d characters of at most %d.
	return fmt.Sprintf(tr.T("%d / %d"), n, limit)
}

// ProfileProposalCount is the panel menu's item with pending proposals,
// "Edit What the Assistant Knows About You… (2)"; the plain item for 0.
func ProfileProposalCount(tr Translator, n int) string {
	item := ProfileTexts(tr).EditProfile
	if n <= 0 {
		return item
	}
	// TRANSLATORS: %s is the menu item Edit What the Assistant Knows About You…, %d the number of pending suggested notes.
	return fmt.Sprintf(tr.T("%s (%d)"), item, n)
}
