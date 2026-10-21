// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeAssistant serves the profile and records the proposals.
type fakeAssistant struct {
	mu        sync.Mutex
	profile   api.AssistantProfile
	proposals []api.AssistantProfileProposeParams
	fail      *api.Error
}

func (f *fakeAssistant) ProfileGet(context.Context, api.AssistantProfileGetParams) (*api.AssistantProfileGetResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	return &api.AssistantProfileGetResult{Profile: f.profile, Proposals: []api.ProfileProposal{}, Limits: api.DefaultProfileLimits()}, nil
}

func (f *fakeAssistant) ProfileSet(context.Context, api.AssistantProfileSetParams) (*api.AssistantProfileSetResult, error) {
	return nil, api.ErrNotImplemented
}

func (f *fakeAssistant) ProfilePropose(_ context.Context, p api.AssistantProfileProposeParams) (*api.AssistantProfileProposeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proposals = append(f.proposals, p)
	return &api.AssistantProfileProposeResult{Proposal: api.ProfileProposal{ID: "p_1", Text: p.Text, AccountID: p.AccountID, Source: p.Source}}, nil
}

func (f *fakeAssistant) ProfileResolve(context.Context, api.AssistantProfileResolveParams) (*api.AssistantProfileResolveResult, error) {
	return nil, api.ErrNotImplemented
}

func (f *fakeAssistant) proposed() []api.AssistantProfileProposeParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.proposals)
}

// profileBackend is the fixture with the fake profile.
type profileBackend struct {
	*fakeBackend
	as *fakeAssistant
}

func (b profileBackend) Assistant() api.AssistantService { return b.as }

const (
	fxProfileText = "Píšu stručně.‮ Tykej."
	fxProfileNote = "Podepisuj jako Studio Paprsek."
)

func sharedProfile(share bool) *fakeAssistant {
	return &fakeAssistant{profile: api.AssistantProfile{
		Text: fxProfileText, Gender: api.GenderFeminine, ShareWithMCP: share,
		Accounts: []api.ProfileAccountNote{{AccountID: fxAccount, Note: fxProfileNote}},
	}}
}

// newProfileHarness starts a fake daemon with the profile and connects a
// bridge that read it at start, as run does (unless daemonDown).
func newProfileHarness(t *testing.T, as *fakeAssistant, cfg config, daemonDown bool) *harness {
	t.Helper()
	fb := newFixture()
	sock := tempSocket(t)
	cfg.socket = sock
	if !daemonDown {
		s := rpc.NewServer(profileBackend{fb, as}, slog.New(slog.DiscardHandler))
		if err := s.Listen(sock); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = s.Serve(ctx); close(done) }()
		t.Cleanup(func() { cancel(); s.Close(); <-done })
	}
	logs := &syncWriter{}
	b := newBridge(cfg, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	b.loadStartupProfile(context.Background())
	st, ct := mcp.NewInMemoryTransports()
	ss, err := b.mcpServer().Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Close(); b.rpc.close() })
	return &harness{fb: fb, sock: sock, cs: cs, logs: logs, b: b}
}

func TestProfileSharedWithAnotherClient(t *testing.T) {
	as := sharedProfile(true)
	h := newProfileHarness(t, as, config{}, false)
	inst := h.cs.InitializeResult().Instructions
	mustContain(t, inst, serverInstructions, profileToolNote, proposeNote, profileIntro,
		"use the feminine forms", "Píšu stručně. Tykej.", fxProfileNote, "id "+string(fxAccount))
	mustNotContain(t, inst, "‮")

	out := h.ok(t, "get_user_profile", nil)
	mustContain(t, out, `"gender": "feminine"`, `"text": "Píšu stručně. Tykej."`, fxProfileNote, `"accountId": "a1"`, "feminine forms")
	mustNotContain(t, out, "‮")
	mustContain(t, h.ok(t, "list_accounts", nil), `"assistantNote": "`+fxProfileNote+`"`)

	out = h.ok(t, "propose_profile_note", map[string]any{"text": "Schůzky v pondělí.", "accountId": "a1"})
	mustContain(t, out, `"proposalId": "p_1"`, `"status": "pending"`)
	if got := as.proposed(); len(got) != 1 || got[0].Source != api.ProfileFromMCP || got[0].AccountID != fxAccount {
		t.Fatalf("proposed %+v", got)
	}
	mustNotContain(t, h.logs.String(), "Píšu", "Schůzky", fxProfileNote)
}

func TestProfileNotShared(t *testing.T) {
	as := sharedProfile(false)
	h := newProfileHarness(t, as, config{}, false)
	inst := h.cs.InitializeResult().Instructions
	mustContain(t, inst, profileToolNote)
	mustNotContain(t, inst, profileIntro, "Píšu", fxProfileNote, proposeNote)
	if out := h.ok(t, "get_user_profile", nil); strings.TrimSpace(out) != notSharedText {
		t.Fatalf("get_user_profile %q", out)
	}
	mustNotContain(t, h.ok(t, "list_accounts", nil), "assistantNote", fxProfileNote)
	if slices.Contains(toolNames(t, h.cs), "propose_profile_note") {
		t.Fatal("propose_profile_note offered while the profile is not shared")
	}
}

func TestProfileInApp(t *testing.T) {
	// The app's own bridge: the prompts carry the profile, so the
	// instructions do not; the tools read it even when not shared.
	as := sharedProfile(false)
	h := newProfileHarness(t, as, config{inApp: true}, false)
	inst := h.cs.InitializeResult().Instructions
	mustContain(t, inst, profileToolNote, proposeNote)
	mustNotContain(t, inst, profileIntro, "Píšu")
	mustContain(t, h.ok(t, "get_user_profile", nil), fxProfileNote)
	mustContain(t, h.ok(t, "list_accounts", nil), fxProfileNote)
	h.ok(t, "propose_profile_note", map[string]any{"text": "Tykej."})
	if got := as.proposed(); len(got) != 1 || got[0].Source != api.ProfileFromPanel || got[0].AccountID != "" {
		t.Fatalf("proposed %+v", got)
	}
}

func TestProfileProposalsPerSession(t *testing.T) {
	as := sharedProfile(true)
	h := newProfileHarness(t, as, config{inApp: true}, false)
	for range maxSessionProposals {
		h.ok(t, "propose_profile_note", map[string]any{"text": "note"})
	}
	h.fail(t, "propose_profile_note", map[string]any{"text": "one more"}, "already offered 3 notes")
	if n := len(as.proposed()); n != maxSessionProposals {
		t.Fatalf("the daemon got %d proposals", n)
	}
}

func TestProfileConfinedSessions(t *testing.T) {
	// Triage and a suggested reply read the profile but never propose:
	// they read other people's mail in bulk.
	for name, cfg := range map[string]config{
		"triage run": {inApp: true, allowTriage: true, triageRun: "run_1"},
		"reply only": {inApp: true, replyOnly: "m1"},
	} {
		h := newProfileHarness(t, sharedProfile(true), cfg, false)
		names := toolNames(t, h.cs)
		if !slices.Contains(names, "get_user_profile") || slices.Contains(names, "propose_profile_note") {
			t.Errorf("%s: tools %v", name, names)
		}
		mustNotContain(t, h.cs.InitializeResult().Instructions, proposeNote)
	}
}

func TestProfileDaemonDownAtStart(t *testing.T) {
	h := newProfileHarness(t, sharedProfile(true), config{}, true)
	inst := h.cs.InitializeResult().Instructions
	mustContain(t, inst, profileToolNote)
	mustNotContain(t, inst, profileIntro, proposeNote)
	if slices.Contains(toolNames(t, h.cs), "propose_profile_note") {
		t.Fatal("propose_profile_note offered without knowing whether the profile is shared")
	}
	h.fail(t, "get_user_profile", nil, "")
}

func TestProfileOlderDaemon(t *testing.T) {
	// A daemon without the profile: list_accounts works as before.
	as := sharedProfile(true)
	as.fail = api.NewError(api.CodeMethodNotFound, "no such method")
	h := newProfileHarness(t, as, config{}, false)
	mustNotContain(t, h.cs.InitializeResult().Instructions, profileIntro)
	out := h.ok(t, "list_accounts", nil)
	mustContain(t, out, `"id": "a1"`)
	mustNotContain(t, out, "assistantNote")
}
