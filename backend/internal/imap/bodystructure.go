// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/pkg/api"
)

// attachmentsFromBodyStructure derives the attachment list from a
// BODYSTRUCTURE the same way internal/mime does from the parsed message:
// the first non-attachment text/plain and text/html parts are the body,
// every other leaf is an attachment (message/rfc822 included, without
// descending into it). Inline means a Content-ID without an "attachment"
// disposition. The second result says whether a non-inline attachment
// exists.
func attachmentsFromBodyStructure(bs imap.BodyStructure) ([]api.Attachment, bool) {
	if bs == nil {
		return nil, false
	}
	var out []api.Attachment
	textDone, htmlDone, has := false, false, false
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok || sp == nil {
			return true
		}
		if len(out) >= maxAttachmentsPerMessage {
			return false
		}
		ct := normalizeMediaType(sp.Type, sp.Subtype)
		disp := ""
		if d := sp.Disposition(); d != nil {
			disp = strings.ToLower(strings.TrimSpace(d.Value))
		}
		isAttachment := disp == "attachment"
		switch {
		case ct == "text/plain" && !isAttachment && !textDone:
			textDone = true
			return true
		case ct == "text/html" && !isAttachment && !htmlDone:
			htmlDone = true
			return true
		}
		id := partID(path)
		cid := cleanField(strings.Trim(strings.TrimSpace(sp.ID), "<>"), maxFieldBytes)
		a := api.Attachment{
			PartID:      id,
			Filename:    attachmentName(id, ct, sp.Filename()),
			ContentType: ct,
			Size:        int64(sp.Size),
			Inline:      cid != "" && !isAttachment,
			ContentID:   cid,
		}
		if !a.Inline {
			has = true
		}
		out = append(out, a)
		return true
	})
	return out, has
}

// partID formats a BODYSTRUCTURE path as an IMAP part number ("2.1"); the
// root of a non-multipart message is "1", which is what Walk reports.
func partID(path []int) string {
	if len(path) == 0 {
		return "1"
	}
	var b strings.Builder
	for i, n := range path {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}

// normalizeMediaType lower-cases type/subtype and falls back to
// application/octet-stream when either is not an RFC 2045 token or the
// leaf claims to be multipart.
func normalizeMediaType(typ, sub string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	s := strings.ToLower(strings.TrimSpace(sub))
	if t == "multipart" || !isToken(t) || !isToken(s) || len(t)+len(s) > 126 {
		return "application/octet-stream"
	}
	return t + "/" + s
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte("()<>@,;:\\\"/[]?=", c) >= 0 {
			return false
		}
	}
	return true
}

// extensionByType mirrors internal/mime so fallback names agree.
var extensionByType = map[string]string{
	"text/plain":       ".txt",
	"text/html":        ".html",
	"text/calendar":    ".ics",
	"text/csv":         ".csv",
	"message/rfc822":   ".eml",
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/gif":        ".gif",
	"image/webp":       ".webp",
	"image/svg+xml":    ".svg",
	"application/pdf":  ".pdf",
	"application/zip":  ".zip",
	"application/json": ".json",
}

// attachmentName sanitises the announced file name or falls back to
// "attachment-<partID>" with an obvious extension.
func attachmentName(id, ct, raw string) string {
	if raw != "" {
		if name := safename.Filename(raw); name != safename.Fallback {
			return name
		}
	}
	return safename.Fallback + "-" + id + extensionByType[ct]
}

// cleanField makes a server-supplied string safe to store and display:
// valid UTF-8, control characters replaced by spaces, trimmed, capped at
// max bytes on a rune boundary.
func cleanField(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
		for len(s) > 0 && !isRuneBoundary(s) {
			s = s[:len(s)-1]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func isRuneBoundary(s string) bool {
	return strings.ToValidUTF8(s, "") == s
}
