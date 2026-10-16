// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar/tz"
)

// envTimeZone names the user's IANA zone ("Europe/Prague"). The macOS and
// Windows clients set it from their system zone when they start the
// daemon; on Linux TZ or /etc/localtime usually say it. Go names the
// zone it reads from /etc/localtime only "Local", which tz.SetPreferred
// cannot use (docs/calendar.md §3.2: Windows zone names of Graph map to
// the user's own zone).
const envTimeZone = "MALACHI_TIME_ZONE"

// localtimeLink is the file whose symlink target names the system zone
// (Linux, macOS; absent on Windows).
const localtimeLink = "/etc/localtime"

// zoneCandidate is one source of the local zone's name.
type zoneCandidate struct {
	name   string
	source string
}

// zoneCandidates are the names of the local zone in the order they are
// tried: MALACHI_TIME_ZONE, TZ (without POSIX's leading colon, or the
// zoneinfo tail of a path), the zoneinfo tail of /etc/localtime's link
// target. Empty values are left out.
func zoneCandidates(malachiTZ, tzEnv, link string) []zoneCandidate {
	var out []zoneCandidate
	add := func(name, source string) {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, zoneCandidate{name: name, source: source})
		}
	}
	add(malachiTZ, envTimeZone)
	t := strings.TrimPrefix(strings.TrimSpace(tzEnv), ":")
	if tail, ok := zoneinfoTail(t); ok {
		t = tail
	}
	add(t, "TZ")
	if tail, ok := zoneinfoTail(link); ok {
		add(tail, localtimeLink)
	}
	return out
}

// zoneinfoTail is the zone name after the last "zoneinfo/" of a path
// ("/usr/share/zoneinfo/Europe/Prague", macOS
// "/var/db/timezone/zoneinfo/Europe/Prague"); false without one.
func zoneinfoTail(path string) (string, bool) {
	const marker = "zoneinfo/"
	i := strings.LastIndex(path, marker)
	if i < 0 {
		return "", false
	}
	tail := strings.Trim(path[i+len(marker):], "/")
	return tail, tail != ""
}

// setPreferredZone finds the named local zone and hands it to
// tz.SetPreferred, once, before the core starts; the first candidate
// that loads wins.
func setPreferredZone(log *slog.Logger) {
	link, _ := os.Readlink(localtimeLink) // absent (Windows) or not a link: no candidate
	for _, c := range zoneCandidates(os.Getenv(envTimeZone), os.Getenv("TZ"), link) {
		loc, err := time.LoadLocation(c.name)
		if err != nil || loc == time.UTC || c.name == "Local" {
			log.Debug("local time zone candidate unusable", "source", c.source, "zone", c.name)
			continue
		}
		tz.SetPreferred(loc)
		log.Debug("local time zone", "source", c.source, "zone", loc.String())
		return
	}
	log.Debug("local time zone has no name; Windows zones keep their territory 001 zone")
}
