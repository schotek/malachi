// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import "strings"

// Offer is the update a build is offered (docs/services.md §2.4).
type Offer struct {
	Release Release
	Package Package
	// Security: the offered release or a newer-than-running release it
	// skips over is a security release.
	Security bool
}

// Select picks the highest release newer than current that is not
// withdrawn and has a package for channel. nil: none.
func Select(m *Manifest, channel string, current Version) *Offer {
	if m == nil {
		return nil
	}
	var offer *Offer
	for _, r := range m.Releases { // newest first
		if r.Version.Compare(current) <= 0 {
			break
		}
		if r.Withdrawn {
			continue
		}
		if offer == nil {
			p, ok := r.Packages[channel]
			if !ok {
				continue // not for this channel (yet), and not skipped over
			}
			offer = &Offer{Release: r, Package: p}
		}
		offer.Security = offer.Security || r.Security
	}
	return offer
}

// NoticeFor is the manifest's notice when it is for current: no "below",
// or current lower than it.
func NoticeFor(m *Manifest, current Version) *Notice {
	if m == nil || m.Notice == nil {
		return nil
	}
	if b := m.Notice.Below; b != nil && current.Compare(*b) >= 0 {
		return nil
	}
	return m.Notice
}

// Text picks the text for locale ("cs", "cs-CZ", "cs_CZ"): the exact tag,
// then its language, then English.
func Text(texts map[string]string, locale string) string {
	l := strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	if i := strings.IndexByte(l, '.'); i >= 0 { // cs_CZ.UTF-8
		l = l[:i]
	}
	for k, t := range texts {
		if strings.ToLower(k) == l && l != "" {
			return t
		}
	}
	if i := strings.IndexByte(l, '-'); i > 0 {
		l = l[:i]
	}
	if t, ok := texts[l]; ok && l != "" {
		return t
	}
	return texts["en"]
}
