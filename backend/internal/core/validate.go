// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// validateDraft checks a draft.save request against the documented limits
// and normalises line endings. Every failure is invalidArgument.
func validateDraft(d *api.Draft) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if d.AccountID == "" {
		return bad("accountId is required")
	}
	// TODO(phase-1): check the account exists once the account registry
	// exists; today the UI's placeholder account must be able to save.
	if d.Version < 0 {
		return bad("version must not be negative")
	}

	n := len(d.To) + len(d.CC) + len(d.BCC)
	if n > api.MaxDraftRecipients {
		return bad("too many recipients (%d, limit %d)", n, api.MaxDraftRecipients)
	}
	for _, list := range [][]api.Address{d.To, d.CC, d.BCC} {
		for _, a := range list {
			if err := validateAddress(a); err != nil {
				return err
			}
		}
	}

	if len(d.Subject) > api.MaxDraftSubjectBytes {
		return bad("subject too long (limit %d bytes)", api.MaxDraftSubjectBytes)
	}
	if !utf8.ValidString(d.Subject) || hasHeaderBreak(d.Subject) {
		return bad("subject must be valid UTF-8 without line breaks")
	}

	if len(d.TextBody) > api.MaxDraftBodyBytes {
		return bad("textBody too long (limit %d bytes)", api.MaxDraftBodyBytes)
	}
	if len(d.HTMLBody) > api.MaxDraftBodyBytes {
		return bad("htmlBody too long (limit %d bytes)", api.MaxDraftBodyBytes)
	}
	if !utf8.ValidString(d.TextBody) || !utf8.ValidString(d.HTMLBody) {
		return bad("bodies must be valid UTF-8")
	}
	d.TextBody = strings.ReplaceAll(d.TextBody, "\r\n", "\n")

	if d.InReplyTo != "" && d.Forwarding != "" {
		return bad("inReplyTo and forwarding are mutually exclusive")
	}
	if len(d.InReplyTo) > 256 || len(d.Forwarding) > 256 {
		return bad("message id too long")
	}

	if len(d.Attachments) > api.MaxDraftAttachments {
		return bad("too many attachments (limit %d)", api.MaxDraftAttachments)
	}
	for _, a := range d.Attachments {
		if a.ID == "" {
			return bad("attachment without id")
		}
	}
	return nil
}

// validateAddress requires a syntactically valid bare address and a display
// name without header-breaking characters.
func validateAddress(a api.Address) error {
	addr := strings.TrimSpace(a.Address)
	if addr == "" {
		return api.NewError(api.CodeInvalidArgument, "empty address")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed.Address != addr {
		return api.NewError(api.CodeInvalidArgument, "invalid address %q", a.Address)
	}
	if !utf8.ValidString(a.Name) || hasHeaderBreak(a.Name) {
		return api.NewError(api.CodeInvalidArgument, "invalid display name for %q", a.Address)
	}
	return nil
}

func hasHeaderBreak(s string) bool {
	return strings.ContainsAny(s, "\r\n\x00")
}
