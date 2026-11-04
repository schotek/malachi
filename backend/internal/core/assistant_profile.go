// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Assistant serves the assistant.* methods (docs/api.md §4.18): the
// profile the clients put into every prompt of their assistant and the
// MCP bridge gives to MCP clients, and the notes an assistant proposed
// for it. The daemon only stores them; it sends them nowhere.
func (b *Backend) Assistant() api.AssistantService { return &assistantService{b} }

type assistantService struct{ b *Backend }

var _ api.AssistantService = (*assistantService)(nil)

// The profile and the pending proposals are JSON in meta. Both are
// written under profileMu (read, change, write).
const (
	metaAssistantProfile   = "assistant.profile"
	metaAssistantProposals = "assistant.profile.proposals"
)

// storedProfile is the JSON of metaAssistantProfile. Notes is keyed by
// account id; a note of an account that is gone is dropped on the next
// write and never returned.
type storedProfile struct {
	Text         string            `json:"text,omitempty"`
	Gender       api.ProfileGender `json:"gender,omitempty"`
	Notes        map[string]string `json:"notes,omitempty"`
	ShareWithMCP *bool             `json:"shareWithMcp,omitempty"` // nil = true
	UpdatedAt    *time.Time        `json:"updatedAt,omitempty"`
}

func (s *assistantService) ProfileGet(ctx context.Context, _ api.AssistantProfileGetParams) (*api.AssistantProfileGetResult, error) {
	accounts, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	sp, err := s.b.loadProfile(ctx)
	if err != nil {
		return nil, err
	}
	props, err := s.b.loadProposals(ctx)
	if err != nil {
		return nil, err
	}
	return &api.AssistantProfileGetResult{
		Profile:   publicProfile(sp, accounts),
		Proposals: liveProposals(props, accounts),
		Limits:    api.DefaultProfileLimits(),
	}, nil
}

func (s *assistantService) ProfileSet(ctx context.Context, p api.AssistantProfileSetParams) (*api.AssistantProfileSetResult, error) {
	// Everything is checked before anything is written.
	var text, gender *string
	if p.Text != nil {
		t, err := cleanProfileText(*p.Text, api.MaxProfileText, "text")
		if err != nil {
			return nil, err
		}
		text = &t
	}
	if p.Gender != nil {
		if !p.Gender.Valid() {
			return nil, api.NewError(api.CodeInvalidArgument, "unknown gender %q", *p.Gender)
		}
		g := string(*p.Gender)
		gender = &g
	}
	s.b.profileMu.Lock()
	defer s.b.profileMu.Unlock()
	accounts, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	notes := map[string]string{}
	for _, n := range p.Accounts {
		if _, dup := notes[string(n.AccountID)]; dup {
			return nil, api.NewError(api.CodeInvalidArgument, "account %q is named twice", n.AccountID)
		}
		if err := noteAccount(accounts, n.AccountID); err != nil {
			return nil, err
		}
		note, err := cleanProfileText(n.Note, api.MaxProfileAccountNote, "note")
		if err != nil {
			return nil, err
		}
		notes[string(n.AccountID)] = note
	}
	sp, err := s.b.loadProfile(ctx)
	if err != nil {
		return nil, err
	}
	before := sp.clone()
	if text != nil {
		sp.Text = *text
	}
	if gender != nil {
		sp.Gender = api.ProfileGender(*gender)
	}
	for id, note := range notes {
		sp.setNote(id, note)
	}
	if p.ShareWithMCP != nil {
		v := *p.ShareWithMCP
		sp.ShareWithMCP = &v
	}
	sp.dropGoneNotes(accounts)
	if !sp.sameAs(before) {
		if err := s.b.saveProfile(ctx, &sp); err != nil {
			return nil, err
		}
		s.b.assistantProfileChanged()
	}
	return &api.AssistantProfileSetResult{Profile: publicProfile(sp, accounts)}, nil
}

func (s *assistantService) ProfilePropose(ctx context.Context, p api.AssistantProfileProposeParams) (*api.AssistantProfileProposeResult, error) {
	if p.Source != api.ProfileFromPanel && p.Source != api.ProfileFromMCP {
		return nil, api.NewError(api.CodeInvalidArgument, "unknown source %q", p.Source)
	}
	text, err := cleanProfileText(p.Text, api.MaxProfileProposal, "text")
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "text is empty")
	}
	s.b.profileMu.Lock()
	defer s.b.profileMu.Unlock()
	accounts, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if p.AccountID != "" {
		if err := noteAccount(accounts, p.AccountID); err != nil {
			return nil, err
		}
	}
	props, err := s.b.loadProposals(ctx)
	if err != nil {
		return nil, err
	}
	props = liveProposals(props, accounts)
	key := proposalKey(text)
	for _, q := range props {
		if q.AccountID == p.AccountID && proposalKey(q.Text) == key {
			return &api.AssistantProfileProposeResult{Proposal: q}, nil
		}
	}
	id, err := newProposalID()
	if err != nil {
		return nil, api.NewError(api.CodeInternalError, "%v", err)
	}
	q := api.ProfileProposal{ID: id, Text: text, AccountID: p.AccountID, Source: p.Source, CreatedAt: time.Now().UTC()}
	props = append(props, q)
	if over := len(props) - api.MaxProfileProposals; over > 0 {
		props = props[over:]
	}
	if err := s.b.saveProposals(ctx, props); err != nil {
		return nil, err
	}
	s.b.assistantProfileChanged()
	return &api.AssistantProfileProposeResult{Proposal: q}, nil
}

func (s *assistantService) ProfileResolve(ctx context.Context, p api.AssistantProfileResolveParams) (*api.AssistantProfileResolveResult, error) {
	s.b.profileMu.Lock()
	defer s.b.profileMu.Unlock()
	accounts, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	all, err := s.b.loadProposals(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(all, func(q api.ProfileProposal) bool { return q.ID == p.ID })
	if p.ID == "" || i < 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "no such proposal")
	}
	q := all[i]
	rest := slices.Delete(slices.Clone(all), i, i+1)
	sp, err := s.b.loadProfile(ctx)
	if err != nil {
		return nil, err
	}
	if p.Accept {
		if q.AccountID != "" {
			if err := noteAccount(accounts, q.AccountID); err != nil {
				if e, ok := err.(*api.Error); ok && e.Code == api.CodeAccountNotFound {
					// The account went: so does its proposal.
					if err := s.b.saveProposals(ctx, rest); err != nil {
						return nil, err
					}
					s.b.assistantProfileChanged()
				}
				return nil, err
			}
		}
		text := q.Text
		if p.Text != nil {
			if text, err = cleanProfileText(*p.Text, api.MaxProfileProposal, "text"); err != nil {
				return nil, err
			}
			if text == "" {
				return nil, api.NewError(api.CodeInvalidArgument, "text is empty")
			}
		}
		if q.AccountID == "" {
			joined := appendLine(sp.Text, text)
			if utf8.RuneCountInString(joined) > api.MaxProfileText {
				return nil, api.NewError(api.CodeInvalidArgument, "the profile text would exceed %d characters", api.MaxProfileText)
			}
			sp.Text = joined
		} else {
			joined := appendLine(sp.Notes[string(q.AccountID)], text)
			if utf8.RuneCountInString(joined) > api.MaxProfileAccountNote {
				return nil, api.NewError(api.CodeInvalidArgument, "the account's note would exceed %d characters", api.MaxProfileAccountNote)
			}
			sp.setNote(string(q.AccountID), joined)
		}
		sp.dropGoneNotes(accounts)
		if err := s.b.saveProfile(ctx, &sp); err != nil {
			return nil, err
		}
	}
	if err := s.b.saveProposals(ctx, rest); err != nil {
		return nil, err
	}
	s.b.assistantProfileChanged()
	return &api.AssistantProfileResolveResult{
		Profile:   publicProfile(sp, accounts),
		Proposals: liveProposals(rest, accounts),
	}, nil
}

// forgetProfileAccount removes the note and the proposals of a removed
// account; failures are logged (the next write drops them anyway).
func (b *Backend) forgetProfileAccount(ctx context.Context, accountID string) {
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	changed := false
	sp, err := b.loadProfile(ctx)
	if err == nil {
		if _, ok := sp.Notes[accountID]; ok {
			delete(sp.Notes, accountID)
			if err = b.saveProfile(ctx, &sp); err == nil {
				changed = true
			}
		}
	}
	if err != nil {
		b.log.Warn("assistant profile: forget account", "id", accountID, "err", err)
	}
	props, err := b.loadProposals(ctx)
	if err == nil {
		kept := slices.DeleteFunc(slices.Clone(props), func(q api.ProfileProposal) bool { return string(q.AccountID) == accountID })
		if len(kept) != len(props) {
			if err = b.saveProposals(ctx, kept); err == nil {
				changed = true
			}
		}
	}
	if err != nil {
		b.log.Warn("assistant profile: forget account's proposals", "id", accountID, "err", err)
	}
	if changed {
		b.assistantProfileChanged()
	}
}

// assistantProfileChanged sends notify.assistantProfileChanged.
func (b *Backend) assistantProfileChanged() {
	if n, ok := b.getNotifier().(api.AssistantNotifier); ok {
		n.AssistantProfileChanged(api.AssistantProfileChangedNotification{})
	}
}

// loadProfile reads the stored profile; an unreadable one is empty
// (logged, never with its content).
func (b *Backend) loadProfile(ctx context.Context) (storedProfile, error) {
	var sp storedProfile
	raw, found, err := b.store.GetMeta(ctx, metaAssistantProfile)
	if err != nil {
		return sp, api.NewError(api.CodeStorageError, "%v", err)
	}
	if found {
		if err := json.Unmarshal([]byte(raw), &sp); err != nil {
			b.log.Warn("assistant profile: unreadable, using an empty one")
			sp = storedProfile{}
		}
	}
	if !sp.Gender.Valid() {
		sp.Gender = api.GenderUnset
	}
	return sp, nil
}

func (b *Backend) saveProfile(ctx context.Context, sp *storedProfile) error {
	now := time.Now().UTC()
	sp.UpdatedAt = &now
	raw, err := json.Marshal(sp)
	if err != nil {
		return api.NewError(api.CodeInternalError, "%v", err)
	}
	if err := b.store.SetMeta(ctx, metaAssistantProfile, string(raw)); err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	return nil
}

func (b *Backend) loadProposals(ctx context.Context) ([]api.ProfileProposal, error) {
	var props []api.ProfileProposal
	raw, found, err := b.store.GetMeta(ctx, metaAssistantProposals)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if found {
		if err := json.Unmarshal([]byte(raw), &props); err != nil {
			b.log.Warn("assistant profile: unreadable proposals, dropping them")
			props = nil
		}
	}
	return props, nil
}

func (b *Backend) saveProposals(ctx context.Context, props []api.ProfileProposal) error {
	if props == nil {
		props = []api.ProfileProposal{}
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return api.NewError(api.CodeInternalError, "%v", err)
	}
	if err := b.store.SetMeta(ctx, metaAssistantProposals, string(raw)); err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	return nil
}

func (sp storedProfile) clone() storedProfile {
	c := sp
	c.Notes = make(map[string]string, len(sp.Notes))
	for k, v := range sp.Notes {
		c.Notes[k] = v
	}
	if sp.ShareWithMCP != nil {
		v := *sp.ShareWithMCP
		c.ShareWithMCP = &v
	}
	return c
}

func (sp storedProfile) share() bool { return sp.ShareWithMCP == nil || *sp.ShareWithMCP }

// sameAs compares what a client sees (not UpdatedAt).
func (sp storedProfile) sameAs(o storedProfile) bool {
	if sp.Text != o.Text || sp.Gender != o.Gender || sp.share() != o.share() || len(sp.Notes) != len(o.Notes) {
		return false
	}
	for k, v := range sp.Notes {
		if w, ok := o.Notes[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func (sp *storedProfile) setNote(id, note string) {
	if note == "" {
		delete(sp.Notes, id)
		return
	}
	if sp.Notes == nil {
		sp.Notes = map[string]string{}
	}
	sp.Notes[id] = note
}

// dropGoneNotes drops the notes of accounts that no longer exist or are
// of a kind that has none.
func (sp *storedProfile) dropGoneNotes(accounts []store.Account) {
	for id := range sp.Notes {
		if !hasNoteKind(accounts, id) {
			delete(sp.Notes, id)
		}
	}
}

// publicProfile is what a client gets: the notes of existing accounts,
// in account.list order.
func publicProfile(sp storedProfile, accounts []store.Account) api.AssistantProfile {
	p := api.AssistantProfile{
		Text: sp.Text, Gender: sp.Gender, Accounts: []api.ProfileAccountNote{},
		ShareWithMCP: sp.share(), UpdatedAt: sp.UpdatedAt,
	}
	for _, a := range accounts {
		if note := sp.Notes[a.ID]; note != "" && slices.Contains(api.ProfileNoteKinds, a.Config.Protocol()) {
			p.Accounts = append(p.Accounts, api.ProfileAccountNote{AccountID: api.AccountID(a.ID), Note: note})
		}
	}
	return p
}

// liveProposals drops the proposals of accounts that are gone.
func liveProposals(props []api.ProfileProposal, accounts []store.Account) []api.ProfileProposal {
	out := []api.ProfileProposal{}
	for _, q := range props {
		if q.AccountID == "" || hasNoteKind(accounts, string(q.AccountID)) {
			out = append(out, q)
		}
	}
	return out
}

func hasNoteKind(accounts []store.Account, id string) bool {
	for _, a := range accounts {
		if a.ID == id {
			return slices.Contains(api.ProfileNoteKinds, a.Config.Protocol())
		}
	}
	return false
}

// noteAccount checks an account that is to get a note: it exists
// (accountNotFound) and is of a kind that has one (invalidArgument).
func noteAccount(accounts []store.Account, id api.AccountID) error {
	for _, a := range accounts {
		if a.ID == string(id) {
			if !slices.Contains(api.ProfileNoteKinds, a.Config.Protocol()) {
				return api.NewError(api.CodeInvalidArgument, "an account of kind %q has no note", a.Config.Protocol())
			}
			return nil
		}
	}
	return api.NewError(api.CodeAccountNotFound, "unknown account %q", id)
}

// cleanProfileText cleans the user's or an assistant's text (docs/api.md
// §4.18) and refuses one over max characters.
func cleanProfileText(s string, max int, field string) (string, error) {
	// The cap of the cleaner only bounds the work: the length is checked
	// on the result, never cut.
	limit := 4 * max
	if len(s) > 16*limit {
		return "", api.NewError(api.CodeInvalidArgument, "%s is longer than %d characters", field, max)
	}
	t := calendar.CleanBlock(norm.NFC.String(s), 0)
	var b strings.Builder
	for line := range strings.SplitSeq(t, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "---") && strings.Contains(strings.ToUpper(line), "UNTRUSTED") {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	// Dropping a fence line may leave an empty line at either end or two
	// in a row: clean again. The first line loses its indentation too.
	t = strings.TrimSpace(calendar.CleanBlock(b.String(), 0))
	if utf8.RuneCountInString(t) > max {
		return "", api.NewError(api.CodeInvalidArgument, "%s is longer than %d characters", field, max)
	}
	return t, nil
}

// proposalKey is a proposal's text ignoring case and whitespace.
func proposalKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// appendLine adds line to text as a new line.
func appendLine(text, line string) string {
	if text == "" {
		return line
	}
	return text + "\n" + line
}

func newProposalID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "p_" + hex.EncodeToString(b[:]), nil
}
