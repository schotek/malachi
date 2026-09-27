// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The HTML side of message.body and message.part: re-parse the raw message,
// sanitise, and serve the parts the sanitised HTML points at.

// viewHTMLCap bounds the sanitised HTML of one message, inlined remote
// images excepted (they have their own caps in internal/remoteimg).
const viewHTMLCap = 4 << 20

// remoteFetchBudget is how long one message.body call may spend fetching
// remote images in total; the UI waits longer than that for such a call.
const remoteFetchBudget = 10 * time.Second

// renderHTML fills res.HTML and its companions from the raw message, or sets
// res.HTMLWithheld when that cannot be done safely. It never returns an
// error: the caller already has the text, and a missing formatted version
// is a state of the result, not a failure of the call.
func (b *Backend) renderHTML(ctx context.Context, accountID, id string, policy api.RemoteContentPolicy, res *api.MessageBodyResult) {
	f, err := b.store.OpenMessageRaw(ctx, accountID, id)
	if err != nil {
		b.withholdHTML(res, id, "raw message unavailable", err)
		return
	}
	// The parse is all that reads the file; sanitising, and fetching remote
	// images under allow, can take seconds more. The pictures the HTML shows
	// are always stored, even when other parts are on the server only.
	parsed, err := mime.Parse(f, mime.DefaultLimits())
	f.Close()
	if err != nil {
		b.withholdHTML(res, id, "raw message unparsable", err)
		return
	}
	if !parsed.HasHTML {
		b.withholdHTML(res, id, "no html part on re-parse", nil)
		return
	}

	// A cid: reference may only reach a part this message has, and the
	// rewritten URL names the message so the view's scheme handler is
	// stateless.
	known := make(map[string]string)
	partOf := make(map[string]string)
	for _, att := range parsed.Attachments {
		if att.ContentID == "" {
			continue
		}
		known[att.ContentID] = accountID + "/" + id + "/" + att.PartID
		partOf[att.ContentID] = att.PartID
	}
	in := sanitize.Input{
		HTML:          parsed.RawHTML,
		Mode:          sanitize.ModeView,
		Policy:        policy,
		KnownCIDs:     known,
		MaxOutputSize: viewHTMLCap,
	}
	b.sanitizeInto(ctx, id, in, partOf, res)
}

// withholdHTML records in res that the formatted version cannot be shown
// safely; the text is still there.
func (b *Backend) withholdHTML(res *api.MessageBodyResult, id, why string, err error) {
	res.HTMLWithheld = true
	res.HTML = ""
	b.log.Warn("html withheld", "id", id, "why", why, "err", err)
}

// sanitizeInto runs the sanitiser over in (fetching the remote images first
// under RemoteAllow) and fills res from its output, or withholds the HTML
// when the sanitiser refuses it. partOf maps a Content-ID to the part number
// InlineParts reports; nil when the caller inlined the pictures itself (an
// attached message, embedded.go). It returns the Content-IDs whose
// references survived.
func (b *Backend) sanitizeInto(ctx context.Context, id string, in sanitize.Input, partOf map[string]string, res *api.MessageBodyResult) []string {
	if in.Policy == api.RemoteAllow {
		in.RemoteImage = b.remoteImageHook(ctx, in)
	}
	out, err := b.Sanitize(in)
	if err != nil {
		b.withholdHTML(res, id, "sanitiser refused the body", err)
		return nil
	}
	res.HTML = out.HTML
	res.Blocked = out.Blocked
	res.Links = out.Links
	res.SanitizerVersion = out.Version
	if partOf != nil && len(out.CIDs) > 0 {
		res.InlineParts = make(map[string]string, len(out.CIDs))
		for _, cid := range out.CIDs {
			res.InlineParts[cid] = partOf[cid]
		}
	}
	return out.CIDs
}

// remoteImageHook fetches, in parallel and within the budget, every https:
// image the sanitiser would inline, and returns the hook that hands them
// over. The sanitiser walks synchronously, so the URLs are collected by a
// first pass whose output is discarded; fetching one image at a time inside
// the walk would let a slow server eat the whole budget on its own.
func (b *Backend) remoteImageHook(ctx context.Context, in sanitize.Input) func(string) (string, []byte, bool) {
	var urls []string
	seen := make(map[string]bool)
	probe := in
	probe.RemoteImage = func(u string) (string, []byte, bool) {
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
		return "", nil, false
	}
	_, _ = b.Sanitize(probe) // an error will surface on the real pass
	nothing := func(string) (string, []byte, bool) { return "", nil, false }
	if len(urls) == 0 || b.FetchRemoteImages == nil {
		return nothing
	}
	fctx, cancel := context.WithTimeout(ctx, remoteFetchBudget)
	defer cancel()
	fetched := b.FetchRemoteImages(fctx, urls)
	b.log.Debug("remote images fetched", "asked", len(urls), "got", len(fetched))
	return func(u string) (string, []byte, bool) {
		img, ok := fetched[u]
		return img.MediaType, img.Data, ok
	}
}

// Part returns the decoded content of one MIME part of a received message
// (docs/api.md §4.3): what the view's malachi-cid: scheme fetches, and what
// saving an attachment will use.
func (s *messageService) Part(ctx context.Context, p api.MessagePartParams) (*api.MessagePartResult, error) {
	if p.AccountID == "" || p.MessageID == "" || p.PartID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId, messageId and partId are required")
	}
	if !mime.ValidPartID(p.PartID) {
		return nil, api.NewError(api.CodeInvalidArgument, "partId must be a part number such as 2 or 1.2")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := s.b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	part, err := s.b.extractPart(ctx, m, p.PartID)
	if err != nil {
		return nil, err
	}
	return &api.MessagePartResult{
		PartID:      part.PartID,
		ContentType: part.ContentType,
		Filename:    part.Filename,
		Size:        int64(len(part.Body)),
		Data:        part.Body,
	}, nil
}

// extractPart reads one part of a stored message, decoded and capped at
// api.MaxAttachmentDataBytes, with the failures mapped to the API errors
// message.part documents. m is the message's row as read before the call.
//
// A part the row names as kept on the server is partNotDownloaded,
// whatever the file holds and whether there is one: a skeleton has an
// empty body in the part's place, which must never pass for its content.
// The file can change while it is opened, so the row is asked twice: m,
// read before the file was opened, because a download names the part
// stored only after the whole file is in place; and the row read after,
// because a reduction names the part remote before its skeleton replaces
// the whole file. A part either calls remote is: at worst the client
// downloads a message that was already whole. A large part the row calls
// stored that reads back empty is treated the same way, and recorded as
// remote (markLostParts): the file is a skeleton the row does not describe.
func (b *Backend) extractPart(ctx context.Context, m store.Message, partID string) (*mime.Part, error) {
	notDownloaded := func() error {
		return api.NewError(api.CodePartNotDownloaded, "part %q is on the mail server only; message.download fetches it", partID)
	}
	if slices.Contains(m.RemoteParts, partID) {
		return nil, notDownloaded()
	}
	f, openErr := b.store.OpenMessageRaw(ctx, m.AccountID, m.ID)
	if openErr == nil {
		defer f.Close()
	}
	cur, err := b.getMessage(ctx, m.AccountID, m.ID)
	if err != nil {
		return nil, err
	}
	if slices.Contains(cur.RemoteParts, partID) {
		return nil, notDownloaded()
	}
	switch {
	case errors.Is(openErr, store.ErrNotFound):
		return nil, api.NewError(api.CodePartNotFound, "message content is not stored")
	case openErr != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", openErr)
	}
	part, err := mime.ExtractPart(f, partID, mime.DefaultLimits(), api.MaxAttachmentDataBytes)
	switch {
	case errors.Is(err, mime.ErrPartNotFound):
		return nil, api.NewError(api.CodePartNotFound, "no part %q", partID)
	case errors.Is(err, mime.ErrPartTooBig):
		e := api.NewError(api.CodeAttachmentTooBig, "part %q exceeds %d bytes", partID, api.MaxAttachmentDataBytes)
		e.Data = map[string]int64{"limit": api.MaxAttachmentDataBytes}
		return nil, e
	case err != nil:
		return nil, api.NewError(api.CodeMalformedMessage, "%v", err)
	}
	if lostPart(cur, partID, len(part.Body)) {
		if err := b.markLostParts(ctx, cur, partID); err != nil {
			return nil, err
		}
		return nil, notDownloaded()
	}
	return part, nil
}

// lostPart reports whether a part read back from a message's stored file
// with n bytes lacks its data although m's row calls it stored: an
// attachment of api.LargeAttachmentMinBytes or more that is empty. Only a
// skeleton has such a part, the background pass leaves nothing smaller on
// the server, and here one the row does not describe: a crash kept the
// reduced file but lost the row's commits, the file is a leftover of the
// other codec, or it was reduced and made whole again while being opened.
func lostPart(m store.Message, partID string, n int) bool {
	if n > 0 {
		return false
	}
	for _, a := range m.Attachments {
		if a.PartID == partID {
			return a.Size >= api.LargeAttachmentMinBytes
		}
	}
	return false
}

// markLostParts records parts of a stored message whose data its file lacks
// (lostPart) as kept on the mail server, as a reduction would have, so
// that message.download fetches them again and the caller can answer
// partNotDownloaded. An error when that cannot help: an outbox message has
// no copy on a server, and a store that refuses the change has to be
// reported. Only ids are logged.
func (b *Backend) markLostParts(ctx context.Context, m store.Message, parts ...string) error {
	err := b.store.MarkPartsRemote(context.WithoutCancel(ctx), m.AccountID, m.ID, parts)
	switch {
	case err == nil:
		b.log.Warn("stored message lacks parts its row called stored; marked as on the server", "message", m.ID, "parts", parts)
		return nil
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict):
		// Gone, or its body is to be downloaded anyway.
		return nil
	case errors.Is(err, store.ErrOutbox):
		return api.NewError(api.CodeStorageError, "the stored message lacks the data of part %s", strings.Join(parts, ", "))
	}
	return api.NewError(api.CodeStorageError, "%v", err)
}
