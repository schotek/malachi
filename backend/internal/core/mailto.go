// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// mailtoFields is what a mailto: URI asks for: to, cc, bcc, subject and
// body; nothing else is interpreted. Body is plain text with control
// characters removed and LF line ends, within api.MaxDraftBodyBytes.
type mailtoFields struct {
	To, CC, BCC []api.Address
	Subject     string
	Body        string
}

// parseMailto reads a mailto: URI. The URI comes from a web page, another
// application or a message header and is hostile: unusable addresses are
// dropped and the rest is capped like a draft. The error is an
// api.CodeInvalidArgument one for a URI that is not mailto: or whose body
// is too long, or that names too many recipients.
func parseMailto(mailto string) (mailtoFields, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	var f mailtoFields
	u, err := url.Parse(mailto)
	if err != nil || !strings.EqualFold(u.Scheme, "mailto") {
		return f, bad("mailto must be a mailto: URI")
	}
	if to, err := url.PathUnescape(u.Opaque); err == nil {
		f.To = mailtoAddresses(to)
	}
	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}
		v := values[0]
		switch strings.ToLower(key) {
		case "to":
			f.To = append(f.To, mailtoAddresses(v)...)
		case "cc":
			f.CC = mailtoAddresses(v)
		case "bcc":
			f.BCC = mailtoAddresses(v)
		case "subject":
			f.Subject = capSubject(strings.TrimSpace(cleanSubject(v)))
		case "body":
			v = strings.ReplaceAll(strings.ToValidUTF8(v, "�"), "\r\n", "\n")
			v = strings.Map(func(r rune) rune {
				if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
					return -1
				}
				return r
			}, v)
			if len(v) > api.MaxDraftBodyBytes {
				return f, bad("mailto body too long (limit %d bytes)", api.MaxDraftBodyBytes)
			}
			f.Body = v
		}
	}
	if len(f.To)+len(f.CC)+len(f.BCC) > api.MaxDraftRecipients {
		return f, bad("too many recipients (limit %d)", api.MaxDraftRecipients)
	}
	return f, nil
}
