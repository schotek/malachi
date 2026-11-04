// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/pkg/api"
)

// flagToIMAP maps the contract flags to IMAP system flags and keywords.
var flagToIMAP = map[api.Flag]imap.Flag{
	api.FlagSeen:      imap.FlagSeen,
	api.FlagAnswered:  imap.FlagAnswered,
	api.FlagFlagged:   imap.FlagFlagged,
	api.FlagDraft:     imap.FlagDraft,
	api.FlagDeleted:   imap.FlagDeleted,
	api.FlagJunk:      imap.FlagJunk,
	api.FlagForwarded: imap.FlagForwarded,
}

// flagFromIMAP is the reverse map, keyed case-insensitively (flags are
// case-insensitive atoms; servers differ in what they echo).
var flagFromIMAP = func() map[string]api.Flag {
	m := make(map[string]api.Flag, len(flagToIMAP))
	for a, f := range flagToIMAP {
		m[strings.ToLower(string(f))] = a
	}
	return m
}()

// toAPIFlags converts server flags; unknown keywords are dropped. The
// result is sorted and de-duplicated (never nil).
func toAPIFlags(in []imap.Flag) []api.Flag {
	seen := make(map[api.Flag]bool, len(in))
	out := make([]api.Flag, 0, len(in))
	for _, f := range in {
		a, ok := flagFromIMAP[strings.ToLower(string(f))]
		if !ok || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// toIMAPFlags converts contract flags; unknown values are dropped.
func toIMAPFlags(in []api.Flag) []imap.Flag {
	out := make([]imap.Flag, 0, len(in))
	for _, a := range in {
		if f, ok := flagToIMAP[a]; ok {
			out = append(out, f)
		}
	}
	return out
}

// permittedFlags drops keywords the mailbox will not persist: a keyword
// stays only when PERMANENTFLAGS is unknown (empty), lists it, or contains
// \*. System flags always pass.
func permittedFlags(permanent []imap.Flag, flags []imap.Flag) []imap.Flag {
	if len(permanent) == 0 {
		return flags
	}
	allowed := make(map[string]bool, len(permanent))
	wildcard := false
	for _, p := range permanent {
		if p == imap.FlagWildcard {
			wildcard = true
		}
		allowed[strings.ToLower(string(p))] = true
	}
	out := make([]imap.Flag, 0, len(flags))
	for _, f := range flags {
		if strings.HasPrefix(string(f), `\`) || wildcard || allowed[strings.ToLower(string(f))] {
			out = append(out, f)
		}
	}
	return out
}
