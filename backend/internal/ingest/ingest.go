// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
)

var (
	// ErrTooBig: the message is over the size cap; nothing was stored. It
	// matches store.ErrTooBig too.
	ErrTooBig = fmt.Errorf("ingest: message over the size cap: %w", store.ErrTooBig)
	// ErrUnparsable: the message's header cannot be read. A body
	// downloaded for the first time is stored as received all the same (the
	// caller marks it failed, as the syncers always did); a download of a
	// message stored before stores nothing.
	ErrUnparsable = errors.New("ingest: message unparsable")
	// ErrMismatch: a message downloaded again is not the one stored (another
	// Message-ID, or other parts); nothing was stored.
	ErrMismatch = errors.New("ingest: the server's message is not the stored one")
)

// Request is one downloaded message for Store.
type Request struct {
	Target
	// Body streams the message as the server sends it.
	Body io.Reader
	// Limit caps the message in bytes (<= 0 or more than MaxMessageBytes:
	// MaxMessageBytes); a larger one is ErrTooBig.
	Limit  int64
	Policy Policy
	// Now is the moment the policy is applied at; zero = time.Now().
	Now time.Time
	// OnDemand is a download the user asked for (message.download): the
	// message is stored whole and the grace of HydratedKeep starts.
	OnDemand bool
	// Expect is the state the caller read the message's row in; a row
	// that changed meanwhile fails the commit with store.ErrConflict (a
	// concurrent download of the same message won).
	Expect store.RawExpect
	// Verify is the stored row a download must match, nil for a body the
	// syncer downloads for the first time: the Message-IDs must agree when
	// both are known, and with Strict, for a row whose body was fetched
	// before, so must the part numbers and sizes. Strict is for IMAP, where
	// a UID names the same bytes for good; Microsoft 365 rebuilds a
	// message's MIME, so its part list may change.
	Verify *store.Message
	Strict bool
}

// Result is what Store kept.
type Result struct {
	// Size is the number of bytes downloaded (the message's length).
	Size int64
	// RemoteParts are the parts left on the server, RemoteBytes their
	// decoded size; empty when the message is stored whole.
	RemoteParts []string
	RemoteBytes int64
}

// Store receives one message and commits it: the bytes are staged, parsed,
// judged by Decide and, when parts are to stay on the server, reduced to a
// skeleton that must pass mime.VerifySkeleton against the original, else
// the whole message is stored. The row gets the parse of the whole message
// whatever the file holds (attachments_json always describes every part)
// and the size downloaded; st.CommitMessageRaw keeps the row and the file
// in step. A message whose reduction is refused as unsafe is marked as
// never to be reduced (strippable 0); one whose reduction failed on the way
// (the disk, say) keeps its candidates for the background pass.
//
// Errors: ErrTooBig, ErrUnparsable, ErrMismatch, store.ErrConflict and
// store.ErrNotFound (the row changed or went meanwhile), store.ErrNoSpace,
// and the reader's own error when the download breaks off. Nothing about
// the content is logged.
func Store(ctx context.Context, st *store.Store, req Request, log *slog.Logger) (Result, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	limit := req.Limit
	if limit <= 0 || limit > MaxMessageBytes {
		limit = MaxMessageBytes
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	full, err := st.StageRaw(ctx, limit)
	if err != nil {
		return Result{}, fmt.Errorf("ingest: %w", err)
	}
	defer full.Remove()
	n, err := full.ReadFrom(req.Body)
	res := Result{Size: n}
	switch {
	case errors.Is(err, store.ErrTooBig):
		return res, ErrTooBig
	case err != nil:
		return res, fmt.Errorf("ingest: receive message: %w", err)
	}

	again := req.Verify != nil && req.Verify.BodyState == store.BodyFetched
	parsed, perr := mime.Parse(full.Reader(), mime.DefaultLimits())
	if perr != nil {
		if again {
			return res, ErrUnparsable
		}
		// As received, the way the syncers always kept such a body: there
		// is nothing to decide about and nothing to verify.
		if _, err := st.CommitMessageRaw(ctx, req.AccountID, req.MessageID, store.RawCommit{
			Source: full, StrippableBytes: 0, Expect: req.Expect,
		}); err != nil {
			return res, fmt.Errorf("ingest: %w", err)
		}
		return res, ErrUnparsable
	}
	if req.Verify != nil {
		if err := verify(req.Verify, parsed, req.Strict && again); err != nil {
			log.Warn("downloaded message does not match the stored one", "message", req.MessageID, "err", err)
			return res, err
		}
	}

	plan := Decide(parsed, req.Target, req.Policy, now, req.OnDemand)
	commit := store.RawCommit{
		Source:          full,
		StrippableBytes: plan.CandidateBytes,
		Body:            bodyUpdate(parsed, n),
		Hydrated:        req.OnDemand,
		Expect:          req.Expect,
	}
	if len(plan.Omit) > 0 {
		skel, omitted, err := reduce(ctx, st, full.Reader(), parsed, plan.Omit, limit)
		switch {
		case err == nil:
			defer skel.Remove()
			commit.Source, commit.RemoteParts, commit.RemoteBytes = skel, omitted, sizeOf(parsed, omitted)
		case errors.Is(err, mime.ErrNotReducible):
			log.Info("message stored whole: its parts cannot be left on the server safely", "message", req.MessageID)
			commit.StrippableBytes = 0
		default:
			log.Warn("message stored whole: reducing it failed", "message", req.MessageID, "err", err)
		}
	}
	if _, err := st.CommitMessageRaw(ctx, req.AccountID, req.MessageID, commit); err != nil {
		return res, fmt.Errorf("ingest: %w", err)
	}
	res.RemoteParts, res.RemoteBytes = commit.RemoteParts, commit.RemoteBytes
	return res, nil
}

// reduce writes the skeleton of the message src reads, without the parts in
// omit, into a new staged file and checks that it shows what orig, the
// parse of src, shows. An error wrapping mime.ErrNotReducible means the
// message must stay whole; any other is a failure on the way (reading,
// staging). The caller removes the staged skeleton.
func reduce(ctx context.Context, st *store.Store, src io.Reader, orig *mime.Parsed, omit map[string]bool, limit int64) (*store.Staged, []string, error) {
	skel, err := st.StageRaw(ctx, limit)
	if err != nil {
		return nil, nil, err
	}
	omitted, err := mime.Skeleton(src, skel, omit, mime.DefaultLimits())
	if err == nil {
		err = checkSkeleton(skel.Reader(), orig, omitted)
	}
	if err != nil {
		skel.Remove()
		return nil, nil, err
	}
	return skel, omitted, nil
}

// checkSkeleton parses a skeleton and verifies it against the parse of
// its original.
func checkSkeleton(r io.Reader, orig *mime.Parsed, omitted []string) error {
	parsed, err := mime.Parse(r, mime.DefaultLimits())
	if err != nil {
		// The text may quote the header; only the fact is kept.
		return fmt.Errorf("%w: the skeleton does not parse", mime.ErrNotReducible)
	}
	return mime.VerifySkeleton(orig, parsed, omitted)
}

// sizeOf sums the decoded sizes of the named parts.
func sizeOf(p *mime.Parsed, parts []string) int64 {
	var n int64
	for _, id := range parts {
		for _, a := range p.Attachments {
			if a.PartID == id {
				n += a.Size
				break
			}
		}
	}
	return n
}

// bodyUpdate is what the row stores of a parsed message; size is the
// message's length as downloaded.
func bodyUpdate(p *mime.Parsed, size int64) *store.BodyUpdate {
	return &store.BodyUpdate{
		Text:           p.Text,
		HasHTML:        p.HasHTML,
		Snippet:        p.Snippet,
		Attachments:    p.Attachments,
		HasAttachments: p.HasAttachments,
		Headers:        p.Headers,
		References:     p.References,
		State:          store.BodyFetched,
		Size:           size,
		Subject:        p.Subject,
		From:           p.From,
		Date:           p.Date,
		RFCMessageID:   p.MessageID,
		InReplyTo:      p.InReplyTo,
	}
}

// verify checks that a downloaded message is the stored one: the same
// Message-ID when both are known and, with parts, the same part numbers
// and decoded sizes, in the same order. The texts name no content.
func verify(stored *store.Message, p *mime.Parsed, parts bool) error {
	if a, b := messageIDKey(stored.RFCMessageID), messageIDKey(p.MessageID); a != "" && b != "" && a != b {
		return fmt.Errorf("%w: another Message-ID", ErrMismatch)
	}
	if !parts {
		return nil
	}
	if len(stored.Attachments) != len(p.Attachments) {
		return fmt.Errorf("%w: %d parts, stored %d", ErrMismatch, len(p.Attachments), len(stored.Attachments))
	}
	for i, a := range stored.Attachments {
		b := p.Attachments[i]
		if a.PartID != b.PartID || a.Size != b.Size {
			return fmt.Errorf("%w: part %s differs", ErrMismatch, a.PartID)
		}
	}
	return nil
}

// messageIDKey compares Message-IDs the way two parsers agree on them: the
// envelope's (IMAP ENVELOPE, Graph's internetMessageId) and the parser's
// differ at most in brackets, folding and case.
func messageIDKey(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "<>")
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}
