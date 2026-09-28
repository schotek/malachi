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

// renderHTML fills res.HTML and its companions from the raw message of m
// (its row as read before), or sets res.HTMLWithheld when that cannot be
// done safely, and counts the pictures the HTML shows that are on the mail
// server only (res.RemotePictures). It never returns an error: the caller
// already has the text, and a missing formatted version is a state of the
// result, not a failure of the call. It never contacts the mail server.
func (b *Backend) renderHTML(ctx context.Context, m store.Message, policy api.RemoteContentPolicy, res *api.MessageBodyResult) {
	accountID, id := m.AccountID, m.ID
	f, err := b.store.OpenMessageRaw(ctx, accountID, id)
	if err != nil {
		b.withholdHTML(res, id, "raw message unavailable", err)
		return
	}
	// The row once the file is open, as extractPart reads it: a reduction
	// names a part remote before its skeleton replaces the file.
	cur, err := b.store.GetMessage(ctx, accountID, id)
	if err != nil {
		cur = m
	}
	// The parse is all that reads the file; sanitising, and fetching remote
	// images under allow, can take seconds more. A picture the HTML shows
	// keeps its part, Content-ID included, in a skeleton too, so that its
	// reference stays; under neverStoreAttachments a large one has an empty
	// body there (remotePictures).
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
	res.RemotePictures = b.remotePictures(ctx, m, cur, parsed, res.InlineParts)
}

// remotePictures counts the pictures the sanitised HTML shows (inline,
// Content-ID → part) that message.part cannot serve now
// (api.MessageBodyResult.RemotePictures): the parts a row of the message
// keeps on the mail server only (m, read before its file was opened, or
// cur, read after, as extractPart asks both), and those the file, parsed
// as parsed, holds empty although the row gives them a size (lostPart),
// which are recorded as remote as message.part records them, so that
// message.download fetches them; less the ones the whole copy
// message.download holds in memory has (heldPartID), which message.part
// serves from there. Only the store and memory are asked.
func (b *Backend) remotePictures(ctx context.Context, m, cur store.Message, parsed *mime.Parsed, inline map[string]string) int {
	if len(inline) == 0 {
		return 0
	}
	sizes := make(map[string]int64, len(parsed.Attachments))
	for _, a := range parsed.Attachments {
		sizes[a.PartID] = a.Size
	}
	var remote, lost []string
	for _, partID := range inline {
		switch {
		case slices.Contains(m.RemoteParts, partID), slices.Contains(cur.RemoteParts, partID):
			remote = append(remote, partID)
		case lostPart(cur, partID, int(sizes[partID])):
			lost = append(lost, partID)
		}
	}
	if len(lost) > 0 {
		slices.Sort(lost)
		if err := b.markLostParts(ctx, cur, lost...); err != nil {
			// An outbox message, or a store that refused: no download
			// brings them.
			b.log.Warn("pictures the stored message lacks not marked as on the server", "message", m.ID, "parts", lost, "err", err)
		} else {
			remote = append(remote, lost...)
		}
	}
	if len(remote) == 0 {
		return 0
	}
	held, isHeld := b.mem.get(m.AccountID, m.ID)
	n := 0
	for _, partID := range remote {
		want, ok := rowAttachment(cur, partID)
		if !ok {
			want, ok = rowAttachment(m, partID)
		}
		if isHeld && ok && heldPartID(want, held.attachments) != "" {
			continue
		}
		n++
	}
	return n
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
// downloads a message that was already whole. A part the row calls stored,
// with a size, that reads back empty is treated the same way, and recorded
// as remote (markLostParts): the file is a skeleton the row does not
// describe. Such a part is served from the whole copy message.download
// holds in memory under neverStoreAttachments, while there is one
// (heldPart).
func (b *Backend) extractPart(ctx context.Context, m store.Message, partID string) (*mime.Part, error) {
	// fromMemory serves a part the row calls remote from the held copy.
	fromMemory := func(row store.Message) (*mime.Part, error) {
		want, ok := rowAttachment(row, partID)
		if !ok {
			return nil, partNotDownloaded(partID)
		}
		part, held, err := b.heldPart(row, want, api.MaxAttachmentDataBytes)
		switch {
		case !held:
			return nil, partNotDownloaded(partID)
		case err != nil:
			return nil, partError(partID, err)
		}
		return part, nil
	}
	if slices.Contains(m.RemoteParts, partID) {
		return fromMemory(m)
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
		return fromMemory(cur)
	}
	switch {
	case errors.Is(openErr, store.ErrNotFound):
		return nil, api.NewError(api.CodePartNotFound, "message content is not stored")
	case openErr != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", openErr)
	}
	part, err := mime.ExtractPart(f, partID, mime.DefaultLimits(), api.MaxAttachmentDataBytes)
	if err != nil {
		return nil, partError(partID, err)
	}
	if lostPart(cur, partID, len(part.Body)) {
		if err := b.markLostParts(ctx, cur, partID); err != nil {
			return nil, err
		}
		return fromMemory(cur)
	}
	return part, nil
}

// partNotDownloaded is message.part's error for a part on the mail server
// only.
func partNotDownloaded(partID string) error {
	return api.NewError(api.CodePartNotDownloaded, "part %q is on the mail server only; message.download fetches it", partID)
}

// partError maps a failure of mime.ExtractPart to message.part's errors.
func partError(partID string, err error) error {
	switch {
	case errors.Is(err, mime.ErrPartNotFound):
		return api.NewError(api.CodePartNotFound, "no part %q", partID)
	case errors.Is(err, mime.ErrPartTooBig):
		e := api.NewError(api.CodeAttachmentTooBig, "part %q exceeds %d bytes", partID, api.MaxAttachmentDataBytes)
		e.Data = map[string]int64{"limit": api.MaxAttachmentDataBytes}
		return e
	}
	return api.NewError(api.CodeMalformedMessage, "%v", err)
}

// lostPart reports whether a part read back from a message's stored file
// with n bytes lacks its data although m's row calls it stored: an
// attachment the row gives a size that is empty. Only a skeleton has such
// a part (the row's sizes come from the parse of the whole message, and
// under neverStoreAttachments even a small part is left on the server),
// and here one the row does not describe: a crash kept the reduced file
// but lost the row's commits, the file is a leftover of the other codec,
// or it was reduced and made whole again while being opened.
func lostPart(m store.Message, partID string, n int) bool {
	if n > 0 {
		return false
	}
	for _, a := range m.Attachments {
		if a.PartID == partID {
			return a.Size > 0
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
