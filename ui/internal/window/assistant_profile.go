// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"maps"
	"slices"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/client"
)

// The user's profile for the assistant (docs/api.md §4.18,
// docs/plans/assistant-profile.md): one store for the application
// (assistantpanel.ProfileStore), read when the connection comes and on
// notify.assistantProfileChanged and notify.accountsChanged; every prompt
// carries it, Preferences → AI → About You edits it, the panel's proposal
// cards resolve through it. macOS AppState.profile.

// profileState is the Assistant's part for the profile.
type profileState struct {
	store     *assistantpanel.ProfileStore
	connected bool
	observers map[int]func()
	nextID    int
}

// AttachProfile makes the application's profile store over rpc, once.
// Call it from the application's startup, after AttachBoardTriage, whose
// connection poll tells it when the daemon comes.
func (a *Assistant) AttachProfile(rpc *client.Client) *assistantpanel.ProfileStore {
	if a.profile.store != nil {
		return a.profile.store
	}
	s := assistantpanel.NewProfileStore(rpc, glibLoop{}, a.log.With("part", "profile"))
	s.OnChange = a.profileChanged
	a.profile.store = s
	if rpc.State() == client.Connected {
		a.profileConnectionChanged(true)
	}
	return s
}

// ProfileStore is the application's profile; nil before AttachProfile.
func (a *Assistant) ProfileStore() *assistantpanel.ProfileStore { return a.profile.store }

// Profile is the user's profile for the prompts, every account's note;
// empty before AttachProfile (compose.Assistant).
func (a *Assistant) Profile() assistant.Profile {
	if a.profile.store == nil {
		return assistant.Profile{}
	}
	return a.profile.store.Profile()
}

// RefreshProfile reads the profile again (the window's notifications).
func (a *Assistant) RefreshProfile() {
	if a.profile.store != nil {
		a.profile.store.Refresh()
	}
}

// profileConnectionChanged: the connection came (the profile is read) or
// went. A nil Assistant (a triage made by a test) does nothing.
func (a *Assistant) profileConnectionChanged(connected bool) {
	if a == nil || connected == a.profile.connected {
		return
	}
	a.profile.connected = connected
	if connected {
		a.RefreshProfile()
	}
}

// OnProfileChange calls f after every change of the profile store, until
// the returned function is called.
func (a *Assistant) OnProfileChange(f func()) (remove func()) {
	if a.profile.observers == nil {
		a.profile.observers = map[int]func(){}
	}
	id := a.profile.nextID
	a.profile.nextID++
	a.profile.observers[id] = f
	return func() { delete(a.profile.observers, id) }
}

func (a *Assistant) profileChanged() {
	// Copy first: a handler may remove itself.
	fs := make([]func(), 0, len(a.profile.observers))
	for _, id := range slices.Sorted(maps.Keys(a.profile.observers)) {
		fs = append(fs, a.profile.observers[id])
	}
	for _, f := range fs {
		f()
	}
}
