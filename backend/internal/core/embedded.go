// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/remoteimg"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/pkg/api"
)

// message.embedded: an attached message (a message/rfc822 part, the .eml
// file a mail client makes of an e-mail dragged into a new one) rendered
// like a message of its own, read-only. Its bytes come out of the containing
// message's raw file through ExtractPart, are parsed by the same parser and
// sanitised by the same ruleset as any body; nothing about it is stored.
// Its parts cannot be served by URL, so its cid: pictures are inlined as
// data: URIs under the remote-image caps, and its other attachments are
// listed by name only. The parser never recurses: an .eml inside the .eml
// stays a named attachment.

// Embedded returns the headers and body of an attached message
// (docs/api.md §4.3).
func (s *messageService) Embedded(ctx context.Context, p api.MessageEmbeddedParams) (*api.MessageEmbeddedResult, error) {
	if p.AccountID == "" || p.MessageID == "" || p.PartID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId, messageId and partId are required")
	}
	if !mime.ValidPartID(p.PartID) {
		return nil, api.NewError(api.CodeInvalidArgument, "partId must be a part number such as 2 or 1.2")
	}
	switch p.RemoteContent {
	case "", api.RemoteBlock, api.RemoteAllow:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "remoteContent override must be block or allow")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := s.b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	// The containing message's senders decide about remote images: the
	// attached message's own From is forwarded content, chosen by whoever
	// attached it.
	policy, err := s.b.RemoteContentFor(ctx, p.RemoteContent, m.From, false)
	if err != nil {
		return nil, err
	}
	part, err := s.b.extractPart(ctx, a.ID, m.ID, p.PartID)
	if err != nil {
		return nil, err
	}
	if !attachedMessage(part) {
		return nil, api.NewError(api.CodeInvalidArgument, "part %q is not an attached message", p.PartID)
	}
	parsed, err := mime.Parse(bytes.NewReader(part.Body), mime.DefaultLimits())
	if err != nil {
		return nil, api.NewError(api.CodeMalformedMessage, "attached message: %v", err)
	}

	body := api.MessageBodyResult{
		MessageID:        api.MessageID(m.ID),
		BodyState:        api.BodyFetched,
		HasHTML:          parsed.HasHTML,
		Text:             parsed.Text,
		Links:            []api.Link{},
		RemoteContent:    policy,
		SanitizerVersion: sanitize.Version,
	}
	inlined := make(map[string]bool)
	if parsed.HasHTML {
		pictures := inlinePictures(parsed, part.Body)
		in := sanitize.Input{
			HTML:          parsed.RawHTML,
			Mode:          sanitize.ModeView,
			Policy:        policy,
			MaxOutputSize: viewHTMLCap,
			InlineCID: func(id string) (string, []byte, bool) {
				pic, ok := pictures[id]
				return pic.mediaType, pic.data, ok
			},
		}
		for _, cid := range s.b.sanitizeInto(ctx, m.ID, in, nil, &body) {
			inlined[cid] = true
		}
	}

	// What the body shows inline is not listed again; the rest is named
	// but, without a part number, cannot be fetched (message.part serves
	// the containing message's parts only).
	atts := make([]api.Attachment, 0, len(parsed.Attachments))
	for _, att := range parsed.Attachments {
		if att.ContentID != "" && inlined[att.ContentID] {
			continue
		}
		att.PartID = ""
		atts = append(atts, att)
	}
	msg := api.Message{
		MessageSummary: api.MessageSummary{
			ID:             api.MessageID(m.ID),
			AccountID:      api.AccountID(m.AccountID),
			FolderID:       api.FolderID(m.FolderID),
			From:           nonNilAddresses(parsed.From),
			To:             nonNilAddresses(parsed.To),
			Subject:        parsed.Subject,
			Date:           parsed.Date,
			Snippet:        parsed.Snippet,
			Flags:          nonNilFlags(nil),
			HasAttachments: len(atts) > 0,
			Size:           int64(len(part.Body)),
		},
		CC:           nonNilAddresses(parsed.CC),
		BCC:          nonNilAddresses(parsed.BCC),
		ReplyTo:      nonNilAddresses(parsed.ReplyTo),
		RFCMessageID: parsed.MessageID,
		InReplyTo:    parsed.InReplyTo,
		References:   parsed.References,
		Attachments:  atts,
	}
	if msg.References == nil {
		msg.References = []string{}
	}
	if len(parsed.Headers) > 0 {
		msg.Headers = parsed.Headers
	}
	s.b.log.Debug("embedded message", "id", m.ID, "part", p.PartID, "hasHtml", parsed.HasHTML,
		"remoteContent", policy, "htmlWithheld", body.HTMLWithheld, "attachments", len(atts))
	return &api.MessageEmbeddedResult{PartID: p.PartID, Message: msg, Body: body}, nil
}

// attachedMessage reports whether a part is an attached message: declared
// as one, or named as the file mail clients write one to.
func attachedMessage(part *mime.Part) bool {
	return part.ContentType == "message/rfc822" || strings.HasSuffix(strings.ToLower(part.Filename), ".eml")
}

// picture is one cid: part of an attached message, ready for inlining.
type picture struct {
	mediaType string
	data      []byte
}

// inlinePictures extracts the cid: parts of an attached message that its
// HTML may reference, under the caps that apply to fetched remote images:
// at most remoteimg.DefaultMaxImages of them, each within
// DefaultMaxImageBytes and all within DefaultMaxTotalBytes. The media type
// is sniffed from the bytes, never taken from the header; what is not a
// picture is left out and the reference to it falls away in the sanitiser.
func inlinePictures(parsed *mime.Parsed, raw []byte) map[string]picture {
	out := make(map[string]picture)
	total := 0
	for _, att := range parsed.Attachments {
		if att.ContentID == "" {
			continue
		}
		if len(out) >= remoteimg.DefaultMaxImages {
			break
		}
		if _, seen := out[att.ContentID]; seen {
			continue
		}
		part, err := mime.ExtractPart(bytes.NewReader(raw), att.PartID, mime.DefaultLimits(), remoteimg.DefaultMaxImageBytes)
		if err != nil || len(part.Body) == 0 {
			continue
		}
		mt := http.DetectContentType(part.Body)
		if !strings.HasPrefix(mt, "image/") {
			continue
		}
		if total+len(part.Body) > remoteimg.DefaultMaxTotalBytes {
			break
		}
		total += len(part.Body)
		out[att.ContentID] = picture{mediaType: mt, data: part.Body}
	}
	return out
}
