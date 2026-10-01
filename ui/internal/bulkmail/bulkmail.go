// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package bulkmail is the view logic of bulk mail: the tag a newsletter,
// mailing-list or automated message carries in the message list, the strip
// above an opened message (with the Unsubscribe button), the confirmation
// before the daemon acts, the dialog that offers the sender's web page when
// the daemon could not verify a one-click request, and the texts of the
// toasts.
//
// The daemon classifies the mail and does the unsubscribing
// (message.unsubscribe, docs/api.md); this package only turns its API
// values into texts and small view models. It is pure (no GTK, no gettext,
// no cgo: the caller passes a Translator) so that its rules are tested
// without a display. It is the reference that is ported one to one to
// macOS (macos/Sources/MalachiCore/Bulk/) and Windows
// (windows/src/Malachi.Core/Bulk/).
//
// Every string of a message here (the list id, the domain, the target, the
// URL) is hostile input; the clients show it as plain text only.
package bulkmail

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain (jira.Translator:
// T a plain one, N a plural form for n, C one with a context).
type Translator = jira.Translator

// TagContext is the gettext context of the list tags (written out in the
// calls below, where xgettext can see it).
const TagContext = "message tag"

// Tag is the neutral pill next to a message's subject in the list: "" for
// personal mail (b nil) and for a kind this client does not know.
func Tag(b *api.BulkInfo, tr Translator) string {
	if b == nil {
		return ""
	}
	switch b.Kind {
	case api.BulkNewsletter:
		return tr.C("message tag", "Bulk")
	case api.BulkList:
		// TRANSLATORS: tag of a message from a discussion mailing list
		return tr.C("message tag", "Mailing List")
	case api.BulkAutomated:
		// TRANSLATORS: tag of a machine-sent message (receipt, ticket)
		return tr.C("message tag", "Automated")
	}
	return ""
}

// StripKind is what the strip above a message is, which picks its icon.
type StripKind int

const (
	// StripNone: no strip is shown.
	StripNone StripKind = iota
	// StripNewsletter: bulk mail (megaphone).
	StripNewsletter
	// StripList: a mailing list (people).
	StripList
	// StripAutomated: an automated message (gear).
	StripAutomated
	// StripJunk: bulk mail in the junk folder (warning).
	StripJunk
	// StripUnsubscribed: the user already unsubscribed (check).
	StripUnsubscribed
)

// Strip is the bar above the message body.
type Strip struct {
	Kind StripKind
	// Text is plain text, never markup.
	Text string
	// Action is the label of the button, with a mnemonic; "" = no button.
	Action string
	// Warning asks for the warning style.
	Warning bool
}

// Visible reports a strip to show.
func (s Strip) Visible() bool { return s.Kind != StripNone }

// StripFor is the strip of message m shown from a folder of the given
// role. date formats the full date of the client's message headers.
func StripFor(m *api.Message, role api.FolderRole, date func(time.Time) string, tr Translator) Strip {
	if m == nil || m.Bulk == nil {
		return Strip{}
	}
	b := m.Bulk
	offer := m.Unsubscribe
	switch b.Kind {
	case api.BulkNewsletter, api.BulkList:
		if role == api.RoleJunk {
			return Strip{
				Kind:    StripJunk,
				Text:    tr.T("Unsubscribing would confirm to the sender that your address exists."),
				Warning: true,
			}
		}
	case api.BulkAutomated:
		return Strip{Kind: StripAutomated, Text: tr.T("Automated message")}
	default:
		return Strip{}
	}
	if offer != nil && offer.UnsubscribedAt != nil && date != nil {
		return Strip{
			Kind: StripUnsubscribed,
			Text: fmt.Sprintf(tr.T("Unsubscribed on %s"), date(*offer.UnsubscribedAt)),
		}
	}
	isURL := offer != nil && offer.Method == api.UnsubscribeURL
	if b.Kind == api.BulkList {
		s := Strip{Kind: StripList, Text: fmt.Sprintf(tr.T("Message from mailing list %s"), listName(b, offer))}
		if offer != nil {
			if isURL {
				s.Action = tr.T("_Leave List…")
			} else {
				s.Action = tr.T("_Leave List")
			}
		}
		return s
	}
	s := Strip{Kind: StripNewsletter, Text: fmt.Sprintf(tr.T("Bulk message from %s"), senderName(b, offer))}
	if offer != nil {
		if isURL {
			s.Action = tr.T("_Unsubscribe…")
		} else {
			s.Action = tr.T("_Unsubscribe")
		}
	}
	return s
}

// senderName is the domain the sender writes from; the list id or the
// offer's target stand in for a missing one.
func senderName(b *api.BulkInfo, offer *api.UnsubscribeOffer) string {
	switch {
	case b != nil && b.Domain != "":
		return b.Domain
	case b != nil && b.ListID != "":
		return b.ListID
	case offer != nil:
		return offer.Target
	}
	return ""
}

// listName is the list id of a mailing list; the domain falls in for a
// list without one.
func listName(b *api.BulkInfo, offer *api.UnsubscribeOffer) string {
	if b != nil && b.ListID != "" {
		return b.ListID
	}
	return senderName(b, offer)
}

// Confirmation is the dialog that asks before the daemon acts or a page
// opens. The texts are plain text; Confirm has a mnemonic.
type Confirmation struct {
	Heading string
	Body    string
	Confirm string
}

// Confirm is the confirmation of the unsubscribe offer of m; false when
// the message has none.
func Confirm(m *api.Message, tr Translator) (Confirmation, bool) {
	if m == nil || m.Unsubscribe == nil {
		return Confirmation{}, false
	}
	offer := m.Unsubscribe
	b := m.Bulk
	switch offer.Method {
	case api.UnsubscribeOneClick:
		return Confirmation{
			Heading: fmt.Sprintf(tr.T("Unsubscribe from %s?"), subject(b, offer)),
			Body:    fmt.Sprintf(tr.T("Malachi Mail will ask %s to stop sending these messages. The sender may still send a few more over the next days."), offer.Target),
			Confirm: tr.T("_Unsubscribe"),
		}, true
	case api.UnsubscribeMailto:
		heading := fmt.Sprintf(tr.T("Unsubscribe from %s?"), subject(b, offer))
		if b != nil && b.Kind == api.BulkList {
			heading = fmt.Sprintf(tr.T("Leave the mailing list %s?"), listName(b, offer))
		}
		return Confirmation{
			Heading: heading,
			Body:    fmt.Sprintf(tr.T("Malachi Mail will send an unsubscribe request to %s from your account. It will appear in Sent."), offer.Target),
			Confirm: tr.T("_Send Request"),
		}, true
	case api.UnsubscribeURL:
		return Confirmation{
			Heading: tr.T("Open the unsubscribe page?"),
			Body:    fmt.Sprintf(tr.T("The sender does not offer unsubscribing in one step. This page opens in your browser:\n%s"), offer.URL),
			Confirm: tr.T("_Open in Browser"),
		}, true
	}
	return Confirmation{}, false
}

// subject is what the heading of an unsubscribe confirmation names: the
// list id of a list, else the sender's domain.
func subject(b *api.BulkInfo, offer *api.UnsubscribeOffer) string {
	if b != nil && b.Kind == api.BulkList {
		return listName(b, offer)
	}
	return senderName(b, offer)
}

// Fallback is the dialog after the daemon answered openUrl for a one-click
// offer it could not verify: it sent nothing and offers the sender's page.
func Fallback(m *api.Message, res api.MessageUnsubscribeResult, tr Translator) Confirmation {
	var b *api.BulkInfo
	var offer *api.UnsubscribeOffer
	if m != nil {
		b, offer = m.Bulk, m.Unsubscribe
	}
	return Confirmation{
		Heading: tr.T("The sender could not be verified"),
		Body:    fmt.Sprintf(tr.T("Malachi Mail sent nothing because the message is not signed by %s. You can unsubscribe on the sender's page instead:\n%s"), senderName(b, offer), res.URL),
		Confirm: tr.T("_Open in Browser"),
	}
}

// Applied is the offer of a message after message.unsubscribe answered res:
// a copy with the time the daemon remembered for unsubscribed and queued,
// the offer unchanged for any other outcome (and nil for nil).
func Applied(offer *api.UnsubscribeOffer, res api.MessageUnsubscribeResult) *api.UnsubscribeOffer {
	if offer == nil {
		return nil
	}
	c := *offer
	if res.Outcome == api.UnsubscribeDone || res.Outcome == api.UnsubscribeQueued {
		at := time.Now()
		if res.UnsubscribedAt != nil {
			at = *res.UnsubscribedAt
		}
		c.UnsubscribedAt = &at
	}
	return &c
}

// Queued is the toast after the unsubscribe request went to the outbox.
func Queued(tr Translator) string { return tr.T("Unsubscribe request queued") }

// ErrorWhat is the action of the error toast, in the progressive form
// widget.RPCErrorText takes.
func ErrorWhat(tr Translator) string { return tr.T("Unsubscribing") }

// Refused is the sentence for error 1505 (unsubscribeFailed): the sender's
// server refused or did not answer the one-click request.
func Refused(tr Translator) string { return tr.T("The sender's server refused the request.") }

// OpenableURL returns raw when a page may be opened in the browser: an
// https URL with a host and no control, space or bidi characters; false
// for anything else (http, javascript:, data:, a relative reference).
func OpenableURL(raw string) (string, bool) {
	if raw == "" || len(raw) > 2048 {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return "", false
	}
	return raw, true
}
