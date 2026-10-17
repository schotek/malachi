// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import "strings"

// The calendar scopes (docs/calendar.md D15) and what a grant records of
// the scopes it was issued with.
//
// Only a session started with StartRequest.Calendar asks for
// calendarScopes; every other sign-in asks for the mail scopes alone, so
// a user who never turns the calendar on never sees a new consent screen.
// The grant records the "scope" of the token response (the requested set
// when the response names none). A refresh never asks for a scope
// (golang.org/x/oauth2 sends none), so it cannot widen a grant.
//
// The refresh-token item (auth.KeyRefreshToken) always holds the bare
// refresh token, as every daemon version reads it. The granted scope is a
// separate item, KeyGrantedScope, written only while the grant allows the
// calendar and removed when a later grant (or a refresh's answer) does
// not. A missing item means "mail scopes only", so every grant from
// before the calendar, or made without it, reads as mail only.

// KeyGrantedScope is the keyring key of the recorded scope of a daemon
// account's grant (next to auth.KeyRefreshToken). Removing an account
// removes it with the account's other secrets.
const KeyGrantedScope = "oauth2.scope"

// calendarScopes are asked for in addition to the mail scopes by a
// calendar sign-in. Google has none until a Google calendar provider
// exists: a calendar session for it is refused.
var calendarScopes = map[Provider][]string{
	Microsoft: {
		"https://graph.microsoft.com/Calendars.ReadWrite",
		"https://graph.microsoft.com/Calendars.Read.Shared",
	},
}

// maxScopeLen bounds the recorded scope string.
const maxScopeLen = 4096

// graphResourcePrefixes are the forms in which Microsoft may name a Graph
// scope in a token response besides the bare name.
var graphResourcePrefixes = []string{
	"https://graph.microsoft.com/",
	"00000003-0000-0000-c000-000000000000/",
}

// SupportsCalendar reports whether p has calendar scopes, i.e. whether a
// sign-in with StartRequest.Calendar can be started for it.
func SupportsCalendar(p Provider) bool { return len(calendarScopes[p]) > 0 }

// HasCalendarScope reports whether a recorded scope string grants
// calendar access: Microsoft Calendars.ReadWrite (or its .Shared
// superset), compared case-insensitively with or without the Graph
// resource prefix. "" (a grant from before the calendar, or one whose
// scope was never recorded) is mail scopes only.
func HasCalendarScope(scope string) bool {
	for _, s := range strings.Fields(scope) {
		name := s
		for _, p := range graphResourcePrefixes {
			if len(name) > len(p) && strings.EqualFold(name[:len(p)], p) {
				name = name[len(p):]
				break
			}
		}
		if strings.EqualFold(name, "Calendars.ReadWrite") || strings.EqualFold(name, "Calendars.ReadWrite.Shared") {
			return true
		}
	}
	return false
}

// HasCalendarScope reports whether the grant allows calendar access.
func (g Grant) HasCalendarScope() bool { return HasCalendarScope(g.Scope) }

// cleanScope keeps a provider's scope string printable ASCII, separated
// by single spaces and bounded; it is data from the token endpoint.
func cleanScope(s string) string {
	var b strings.Builder
	for _, f := range strings.Fields(s) {
		ok := true
		for i := 0; i < len(f); i++ {
			if f[i] < 0x21 || f[i] > 0x7e {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if b.Len()+1+len(f) > maxScopeLen {
			break
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(f)
	}
	return b.String()
}
