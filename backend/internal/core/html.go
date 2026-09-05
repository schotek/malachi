// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
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
	withhold := func(why string, err error) {
		res.HTMLWithheld = true
		res.HTML = ""
		b.log.Warn("html withheld", "id", id, "why", why, "err", err)
	}
	f, err := b.store.OpenMessageRaw(ctx, accountID, id)
	if err != nil {
		withhold("raw message unavailable", err)
		return
	}
	defer f.Close()
	parsed, err := mime.Parse(f, mime.DefaultLimits())
	if err != nil {
		withhold("raw message unparsable", err)
		return
	}
	if !parsed.HasHTML {
		withhold("no html part on re-parse", nil)
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
	if policy == api.RemoteAllow {
		in.RemoteImage = b.remoteImageHook(ctx, in)
	}
	out, err := b.Sanitize(in)
	if err != nil {
		withhold("sanitiser refused the body", err)
		return
	}
	res.HTML = out.HTML
	res.Blocked = out.Blocked
	res.Links = out.Links
	res.SanitizerVersion = out.Version
	if len(out.CIDs) > 0 {
		res.InlineParts = make(map[string]string, len(out.CIDs))
		for _, cid := range out.CIDs {
			res.InlineParts[cid] = partOf[cid]
		}
	}
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
	f, err := s.b.store.OpenMessageRaw(ctx, a.ID, m.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodePartNotFound, "message content is not stored")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	defer f.Close()
	part, err := mime.ExtractPart(f, p.PartID, mime.DefaultLimits(), api.MaxAttachmentDataBytes)
	switch {
	case errors.Is(err, mime.ErrPartNotFound):
		return nil, api.NewError(api.CodePartNotFound, "no part %q", p.PartID)
	case errors.Is(err, mime.ErrPartTooBig):
		e := api.NewError(api.CodeAttachmentTooBig, "part %q exceeds %d bytes", p.PartID, api.MaxAttachmentDataBytes)
		e.Data = map[string]int64{"limit": api.MaxAttachmentDataBytes}
		return nil, e
	case err != nil:
		return nil, api.NewError(api.CodeMalformedMessage, "%v", err)
	}
	return &api.MessagePartResult{
		PartID:      part.PartID,
		ContentType: part.ContentType,
		Filename:    part.Filename,
		Size:        int64(len(part.Body)),
		Data:        part.Body,
	}, nil
}
