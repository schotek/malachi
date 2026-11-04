// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/caldav"
	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/imip"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// IMIPMail is one iTIP message the daemon sends itself (client scheduling
// of a caldav account, D8; a reply to an invitation, D18): the METHOD,
// the text/calendar body, the recipients and the client's localised
// subject and text (api.MailText, nil = a technical fallback).
type IMIPMail struct {
	Method     string
	Data       []byte
	Recipients []string
	Mail       *api.MailText
	// InReplyTo and References thread a reply under the invitation
	// message (bare ids); empty = none.
	InReplyTo  string
	References []string
	// Attachments are files of a new event that go with its REQUEST
	// (D21); the caller has checked their sizes.
	Attachments []calendar.Attachment
}

// queueIMIP queues m in the outbox of the mail account accountID, as
// message.unsubscribe's mailto does with a plain message
// (smtp.BuildMessage with the calendar part, store.EnqueueOutbox, the
// outbox woken): nothing is sent from here. The account must exist and be
// able to send (compose); otherwise, and without recipients,
// invitationsUnavailable.
func (b *Backend) queueIMIP(ctx context.Context, accountID string, m IMIPMail) error {
	if accountID == "" || len(m.Recipients) == 0 {
		return api.NewError(api.CodeInvitationsUnavailable, "no mail account or no recipients for the invitation mail")
	}
	a, err := b.requireAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if !a.Enabled || !can(a.Config, api.CapabilityCompose) {
		return api.NewError(api.CodeInvitationsUnavailable, "account %q cannot send the invitation mail", accountID)
	}
	if len(m.Data) == 0 || len(m.Data) > api.MaxICSBytes {
		return api.NewError(api.CodeInvalidArgument, "the invitation's calendar data is empty or too large")
	}
	to := make([]api.Address, 0, len(m.Recipients))
	for _, r := range m.Recipients {
		addr := api.Address{Address: strings.TrimSpace(r)}
		if validateAddress(addr) != nil {
			continue
		}
		to = append(to, addr)
	}
	if len(to) == 0 {
		return api.NewError(api.CodeInvitationsUnavailable, "no usable recipient for the invitation mail")
	}
	subject, text := imipText(m)
	now := time.Now()
	from := api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
	in := smtp.BuildInput{
		From:       from,
		To:         to,
		Subject:    subject,
		Text:       text,
		Date:       now,
		MessageID:  smtp.NewMessageID(a.Config.Email),
		InReplyTo:  m.InReplyTo,
		References: m.References,
		Calendar:   &smtp.CalendarPart{Method: m.Method, Data: m.Data},
	}
	for _, att := range m.Attachments {
		data := att.Data
		name := strings.TrimSpace(calendar.CleanLine(att.Name, calendar.MaxNameBytes))
		if name == "" {
			name = "attachment"
		}
		ct := att.MediaType
		if ct == "" {
			ct = "application/octet-stream"
		}
		in.Attachments = append(in.Attachments, smtp.Attachment{Filename: name, ContentType: ct, Size: int64(len(data)),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }})
	}
	rcpts := make([]string, len(to))
	for i, t := range to {
		rcpts[i] = t.Address
	}
	queued, err := b.store.EnqueueOutbox(ctx, store.EnqueueInput{
		Message: store.Message{
			AccountID:    a.ID,
			From:         []api.Address{from},
			To:           to,
			Subject:      subject,
			Date:         now,
			InternalDate: now,
			RFCMessageID: in.MessageID,
			InReplyTo:    m.InReplyTo,
			Snippet:      mime.Snippet(text, snippetRunes),
		},
		Text:         text,
		EnvelopeFrom: a.Config.Email,
		Recipients:   rcpts,
		Build:        func(w io.Writer) error { return smtp.BuildMessage(w, in) },
		Limit:        outgoingLimit,
	})
	if err != nil {
		return enqueueError(err, "", store.Draft{})
	}
	b.Delivery.Wake(a.ID)
	b.outboxChanged(a.ID)
	b.log.Info("invitation mail queued", "account", a.ID, "message", queued.ID, "method", m.Method, "recipients", len(rcpts))
	return nil
}

// imipText is the subject and text of an iTIP message: the client's, with
// line breaks out of the subject, else the event's title (the daemon has
// no words of its own; docs/calendar.md §4.2 MailText).
func imipText(m IMIPMail) (subject, text string) {
	if m.Mail != nil {
		subject = strings.Join(strings.Fields(m.Mail.Subject), " ")
		text = m.Mail.Text
		subject, text = cutUTF8(subject, api.MaxMailTextBytes), cutUTF8(text, api.MaxMailTextBytes)
	}
	if subject == "" {
		if inv, err := imip.ParseMessage(m.Data, nil); err == nil {
			subject = strings.Join(strings.Fields(inv.Event.Title), " ")
		}
	}
	if subject == "" {
		subject = m.Method
	}
	return subject, text
}

// cutUTF8 cuts s to at most n bytes on a character boundary.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// sendCalDAVIMIP is caldav.ProviderOptions.SendIMIP: the messages a
// caldav provider built go to the outbox of opts.MailAccountID (the
// provider resolves the account's default into it), one outbox message
// each.
func (b *Backend) sendCalDAVIMIP(ctx context.Context, msgs []caldav.IMIPMessage, opts calsync.WriteOptions) error {
	for _, msg := range msgs {
		m := IMIPMail{Method: msg.Method, Data: msg.Data, Recipients: msg.Recipients, Mail: opts.Mail}
		if strings.EqualFold(msg.Method, imip.MethodRequest) {
			m.Attachments = opts.Attachments
		}
		if err := b.queueIMIP(ctx, string(opts.MailAccountID), m); err != nil {
			return err
		}
	}
	return nil
}
