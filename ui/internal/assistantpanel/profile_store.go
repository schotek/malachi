// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

// ProfileCaller calls the daemon (the window's RPC client).
type ProfileCaller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// profileTimeout bounds one call of the store.
const profileTimeout = 15 * time.Second

// ProfileStore is the application's copy of the user's profile
// (docs/api.md §4.18): the profile with the names of its accounts for the
// prompts (Profile), the pending proposals and the limits for Preferences →
// AI → About You and the panel's cards. One per application; it reads the
// profile at start (Refresh) and again on notify.assistantProfileChanged
// and notify.accountsChanged, and writes through Set and Resolve. A daemon
// without the profile (an older one) leaves it empty and Supported false.
//
// Main loop only: every method and OnChange run on it.
type ProfileStore struct {
	// OnChange: the profile, the proposals or Supported changed.
	OnChange func()

	caller ProfileCaller
	loop   Loop
	log    *slog.Logger

	loaded, supported bool
	result            api.AssistantProfileGetResult
	accounts          []api.Account
	// gen orders the reads: an older read that ends after a newer one is
	// dropped.
	gen int
}

// NewProfileStore is an empty store; Refresh reads the profile.
func NewProfileStore(caller ProfileCaller, loop Loop, log *slog.Logger) *ProfileStore {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ProfileStore{caller: caller, loop: loop, log: log, result: emptyProfileResult()}
}

func emptyProfileResult() api.AssistantProfileGetResult {
	return api.AssistantProfileGetResult{
		Profile:   api.AssistantProfile{Gender: api.GenderUnset, Accounts: []api.ProfileAccountNote{}, ShareWithMCP: true},
		Proposals: []api.ProfileProposal{},
		Limits:    api.DefaultProfileLimits(),
	}
}

// Loaded says whether a read has ended (with the profile or without one).
func (s *ProfileStore) Loaded() bool { return s.loaded }

// Supported says whether the daemon has the profile; false until a read
// ended, and for an older daemon.
func (s *ProfileStore) Supported() bool { return s.supported }

// Stored is the profile as the daemon has it.
func (s *ProfileStore) Stored() api.AssistantProfile { return s.result.Profile }

// Limits are the daemon's limits.
func (s *ProfileStore) Limits() api.ProfileLimits { return s.result.Limits }

// Profile is the profile for the prompts, with the names of its accounts.
func (s *ProfileStore) Profile() assistant.Profile {
	return assistant.ProfileFrom(s.result.Profile, s.accounts)
}

// Proposals are the pending proposals, oldest first.
func (s *ProfileStore) Proposals() []api.ProfileProposal { return slices.Clone(s.result.Proposals) }

// Proposal is a pending proposal by id.
func (s *ProfileStore) Proposal(id string) (api.ProfileProposal, bool) {
	for _, q := range s.result.Proposals {
		if q.ID == id {
			return q, true
		}
	}
	return api.ProfileProposal{}, false
}

// Refresh reads the profile and the accounts again.
func (s *ProfileStore) Refresh() {
	s.gen++
	my := s.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), profileTimeout)
		defer cancel()
		var res api.AssistantProfileGetResult
		err := s.caller.Call(ctx, api.MethodAssistantProfileGet, api.AssistantProfileGetParams{}, &res)
		var accs api.AccountListResult
		if err == nil {
			if aerr := s.caller.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &accs); aerr != nil {
				s.log.Info("assistant profile: account names", "err", aerr)
			}
		}
		s.loop.Post(func() {
			if my != s.gen {
				return
			}
			s.loaded = true
			switch {
			case err == nil:
				s.supported = true
				s.result = normalProfileResult(res)
				s.accounts = accs.Accounts
			case unsupported(err):
				s.supported = false
				s.result, s.accounts = emptyProfileResult(), nil
			default:
				// A transient failure keeps what was read before.
				s.log.Info("assistant profile: read", "err", err)
			}
			s.changed()
		})
	}()
}

// Set writes the fields p carries; done gets the daemon's error, nil
// after it stored them (the store then holds the new profile).
func (s *ProfileStore) Set(p api.AssistantProfileSetParams, done func(error)) {
	s.gen++
	my := s.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), profileTimeout)
		defer cancel()
		var res api.AssistantProfileSetResult
		err := s.caller.Call(ctx, api.MethodAssistantProfileSet, p, &res)
		s.loop.Post(func() {
			if err == nil && my == s.gen {
				s.result.Profile = normalProfile(res.Profile)
				s.changed()
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

// Resolve accepts (with text, the user's edit, or nil) or dismisses a
// proposal; done gets the daemon's error.
func (s *ProfileStore) Resolve(id string, accept bool, text *string, done func(error)) {
	s.gen++
	my := s.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), profileTimeout)
		defer cancel()
		var res api.AssistantProfileResolveResult
		err := s.caller.Call(ctx, api.MethodAssistantProfileResolve, api.AssistantProfileResolveParams{ID: id, Accept: accept, Text: text}, &res)
		s.loop.Post(func() {
			if err == nil && my == s.gen {
				s.result.Profile = normalProfile(res.Profile)
				s.result.Proposals = nonNil(res.Proposals)
				s.changed()
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

func (s *ProfileStore) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// unsupported is the error of a daemon without the profile.
func unsupported(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && (e.Code == api.CodeMethodNotFound || e.Code == api.CodeNotImplemented)
}

func normalProfileResult(r api.AssistantProfileGetResult) api.AssistantProfileGetResult {
	r.Profile = normalProfile(r.Profile)
	r.Proposals = nonNil(r.Proposals)
	if r.Limits == (api.ProfileLimits{}) {
		r.Limits = api.DefaultProfileLimits()
	}
	return r
}

func normalProfile(p api.AssistantProfile) api.AssistantProfile {
	if !p.Gender.Valid() {
		p.Gender = api.GenderUnset
	}
	if p.Accounts == nil {
		p.Accounts = []api.ProfileAccountNote{}
	}
	return p
}

func nonNil(q []api.ProfileProposal) []api.ProfileProposal {
	if q == nil {
		return []api.ProfileProposal{}
	}
	return q
}
