// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"reflect"
	"testing"
)

func TestZoneCandidates(t *testing.T) {
	cases := []struct {
		name          string
		env, tz, link string
		want          []zoneCandidate
	}{
		{"none", "", "", "", nil},
		{"env first", "Europe/Prague", "America/New_York", "/usr/share/zoneinfo/Asia/Tokyo",
			[]zoneCandidate{{"Europe/Prague", envTimeZone}, {"America/New_York", "TZ"}, {"Asia/Tokyo", localtimeLink}}},
		{"tz with colon", "", ":Europe/Berlin", "", []zoneCandidate{{"Europe/Berlin", "TZ"}}},
		{"tz as path", "", "/usr/share/zoneinfo/Europe/Vienna", "", []zoneCandidate{{"Europe/Vienna", "TZ"}}},
		{"macos link", "", "", "/var/db/timezone/zoneinfo/Europe/Prague", []zoneCandidate{{"Europe/Prague", localtimeLink}}},
		{"relative link", "", "", "../usr/share/zoneinfo/America/Sao_Paulo", []zoneCandidate{{"America/Sao_Paulo", localtimeLink}}},
		{"link without zoneinfo", "", "", "/etc/static/localtime", nil},
		{"link ends at zoneinfo", "", "", "/usr/share/zoneinfo/", nil},
		{"blank env", "  ", "", "/usr/share/zoneinfo/UTC", []zoneCandidate{{"UTC", localtimeLink}}},
	}
	for _, c := range cases {
		if got := zoneCandidates(c.env, c.tz, c.link); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
