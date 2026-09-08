// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// syncFolder is the one algorithm for every kind of pass — initial,
// incremental, window change, expunge: SELECT (UIDVALIDITY reset), the
// server's UID set within the retention window against the local one,
// envelopes of new messages, flags of known ones, bodies newest first,
// then the folder's counters. since is the window start (zero = all).
// A NO/BAD for the mailbox is returned as a status error so the caller can
// skip the folder; any other error ends the pass.
func (s *Syncer) syncFolder(ctx context.Context, sess *session, f store.Folder, since time.Time, progress func(float64)) error {
	syncStart := time.Now() // compared with the store's own timestamps, not s.now()
	sel, err := sess.selectMailbox(ctx, f.Mailbox)
	if err != nil {
		return err
	}
	prevUIDNext := f.UIDNext
	if sel.UIDValidity != f.UIDValidity {
		if f.UIDValidity != 0 {
			s.log.Info("uidvalidity changed, resetting folder", "folder", f.ID)
			if err := s.deps.Store.ResetFolder(ctx, f.ID, sel.UIDValidity); err != nil {
				return storageError(err)
			}
		}
		prevUIDNext = 0
	}

	server, filtered, err := s.searchWindow(ctx, sess, sel, since)
	if err != nil {
		return err
	}
	local, err := s.deps.Store.ListUIDs(ctx, f.ID)
	if err != nil {
		return storageError(err)
	}
	gone, fresh, common := diffUIDs(local, server)
	s.log.Debug("folder diff", "folder", f.ID, "exists", sel.NumMessages, "server", len(server),
		"local", len(local), "gone", len(gone), "fresh", len(fresh), "common", len(common), "localWindow", filtered)
	if filtered {
		// The server returned everything: apply the window here. New UIDs
		// are kept only when their INTERNALDATE is inside it; known rows
		// that aged out are dropped like an expunge.
		if fresh, err = s.filterByInternalDate(ctx, sess, f.ID, fresh, since); err != nil {
			return err
		}
		old, err := s.deps.Store.ListUIDsOlderThan(ctx, f.ID, since)
		if err != nil {
			return storageError(err)
		}
		if len(old) > 0 {
			gone = append(gone, old...)
			common = subtractUIDs(common, old)
		}
	}
	progress(0.05)

	if len(gone) > 0 {
		if err := s.deps.Store.DeleteMessagesByUID(ctx, f.ID, gone); err != nil {
			return storageError(err)
		}
	}
	if err := s.fetchEnvelopes(ctx, sess, f, fresh, func(fr float64) { progress(0.05 + 0.35*fr) }); err != nil {
		return err
	}
	if err := s.syncFlags(ctx, sess, f, common, func(fr float64) { progress(0.4 + 0.1*fr) }); err != nil {
		return err
	}
	if err := s.fetchBodies(ctx, sess, f, prevUIDNext, func(fr float64) { progress(0.5 + 0.5*fr) }); err != nil {
		return err
	}
	if _, err := s.deps.Store.DeleteStalePending(ctx, f.ID, syncStart); err != nil {
		return storageError(err)
	}
	if _, _, err := s.deps.Store.RecountFolder(ctx, f.ID); err != nil {
		return storageError(err)
	}

	state := store.FolderSyncState{
		UIDValidity:    sel.UIDValidity,
		UIDNext:        uint32(sel.UIDNext),
		ServerMessages: int(sel.NumMessages),
		ServerUnseen:   f.ServerUnseen,
		LastSyncAt:     time.Now(),
	}
	// A fresh STATUS is the baseline of the next change detection; the
	// SELECT values are from before this pass.
	st, err := folderStatus(ctx, sess, f.Mailbox)
	switch {
	case err == nil && st != nil:
		if st.UIDValidity == sel.UIDValidity && st.UIDNext != 0 {
			state.UIDNext = uint32(st.UIDNext)
		}
		if st.NumMessages != nil {
			state.ServerMessages = int(*st.NumMessages)
		}
		if st.NumUnseen != nil {
			state.ServerUnseen = int(*st.NumUnseen)
		}
	case err != nil && !isStatusError(err):
		return err
	}
	if state.UIDNext == 0 {
		// A server without UIDNEXT: derive it so new-message detection works.
		state.UIDNext = 1
		if len(server) > 0 {
			state.UIDNext = server[len(server)-1] + 1
		}
	}
	if err := s.deps.Store.SetFolderSyncState(ctx, f.ID, state); err != nil {
		return storageError(err)
	}
	progress(1)
	return nil
}

// searchWindow returns the ascending UIDs the server holds within the
// retention window. SEARCH SINCE is used when there is a window; a server
// that rejects it (go-imap quotes the date, which RFC 3501 allows but some
// servers refuse) is remembered for the session and its SEARCH is not
// trusted any further: the UIDs are then enumerated with UID FETCH 1:*,
// filtered is true and the caller applies the window by INTERNALDATE.
func (s *Syncer) searchWindow(ctx context.Context, sess *session, sel *imap.SelectData, since time.Time) (uids []uint32, filtered bool, err error) {
	if !since.IsZero() && !sess.noSince && !s.deps.NoSinceSearch {
		err = sess.do(ctx, commandTimeout, func() error {
			data, err := sess.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
			if err != nil {
				return err
			}
			set, _ := data.All.(imap.UIDSet)
			uids, err = uidsFromSet(set)
			return err
		})
		switch {
		case err == nil:
			sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
			return uids, false, nil
		case isStatusError(err):
			s.log.Warn("server rejected SEARCH SINCE, applying the retention window locally", "err", err)
			sess.noSince = true
		default:
			return nil, false, err
		}
	}
	uids, err = s.enumerateUIDs(ctx, sess, sel)
	if err != nil {
		return nil, false, err
	}
	return uids, !since.IsZero(), nil
}

// enumerateUIDs lists every UID of the selected mailbox with UID FETCH
// 1:* (UID), which unlike SEARCH every server gets right. The result is
// ascending.
func (s *Syncer) enumerateUIDs(ctx context.Context, sess *session, sel *imap.SelectData) ([]uint32, error) {
	if sel.NumMessages == 0 {
		return nil, nil
	}
	if sel.NumMessages > maxSearchResults {
		return nil, api.NewError(api.CodeServerError, "mailbox exceeds %d messages", maxSearchResults)
	}
	uids := make([]uint32, 0, sel.NumMessages)
	err := sess.do(ctx, commandTimeout, func() error {
		var all imap.UIDSet
		all.AddRange(1, 0) // 1:*
		cmd := sess.Fetch(all, &imap.FetchOptions{UID: true})
		for md := cmd.Next(); md != nil; md = cmd.Next() {
			for item := md.Next(); item != nil; item = md.Next() {
				switch it := item.(type) {
				case imapclient.FetchItemDataUID:
					if it.UID != 0 && len(uids) < maxSearchResults {
						uids = append(uids, uint32(it.UID))
					}
				case imapclient.FetchItemDataBodySection:
					if it.Literal != nil {
						io.Copy(io.Discard, it.Literal)
					}
				}
			}
		}
		return cmd.Close()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	return uids, nil
}

// filterByInternalDate keeps the UIDs whose INTERNALDATE is not before
// since, fetching only that item in bounded batches.
func (s *Syncer) filterByInternalDate(ctx context.Context, sess *session, folderID string, uids []uint32, since time.Time) ([]uint32, error) {
	keep := make([]uint32, 0, len(uids))
	var newest time.Time
	answered := 0
	for _, batch := range uidBatches(uids, flagBatch, maxSetBytes) {
		err := sess.do(ctx, commandTimeout, func() error {
			cmd := sess.Fetch(uidSet(batch), &imap.FetchOptions{UID: true, InternalDate: true})
			for md := cmd.Next(); md != nil; md = cmd.Next() {
				var uid uint32
				var at time.Time
				for item := md.Next(); item != nil; item = md.Next() {
					switch it := item.(type) {
					case imapclient.FetchItemDataUID:
						uid = uint32(it.UID)
					case imapclient.FetchItemDataInternalDate:
						at = it.Time
					case imapclient.FetchItemDataBodySection:
						if it.Literal != nil {
							io.Copy(io.Discard, it.Literal)
						}
					}
				}
				answered++
				if at.After(newest) {
					newest = at
				}
				if uid != 0 && !at.IsZero() && !at.Before(since) {
					keep = append(keep, uid)
				}
			}
			return cmd.Close()
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i] < keep[j] })
	s.log.Debug("retention window applied locally", "folder", folderID, "asked", len(uids),
		"answered", answered, "kept", len(keep), "newest", newest, "since", since)
	return keep, nil
}

// maxSetBytes bounds the encoded UID set of one command so that servers
// with short line limits still answer; the count limit of a batch applies
// on top of it.
const maxSetBytes = 2000

// uidBatches splits ascending UIDs into batches of at most maxCount
// members whose encoded set (ranges merged) stays within maxBytes.
func uidBatches(uids []uint32, maxCount, maxBytes int) [][]uint32 {
	var out [][]uint32
	start, size := 0, 0
	for i, u := range uids {
		cost := len(strconv.FormatUint(uint64(u), 10)) + 1
		if i > start && u == uids[i-1]+1 {
			// Extends a range: "a:b" costs the upper bound once per range,
			// which the previous member has already paid for.
			if i-1 > start && uids[i-1] == uids[i-2]+1 {
				cost = 0
			}
		}
		if i > start && (i-start >= maxCount || size+cost > maxBytes) {
			out = append(out, uids[start:i])
			start, size = i, 0
			cost = len(strconv.FormatUint(uint64(u), 10)) + 1
		}
		size += cost
	}
	if start < len(uids) {
		out = append(out, uids[start:])
	}
	return out
}

// subtractUIDs removes every UID of drop from the ascending list in.
func subtractUIDs(in, drop []uint32) []uint32 {
	skip := make(map[uint32]struct{}, len(drop))
	for _, u := range drop {
		skip[u] = struct{}{}
	}
	out := in[:0]
	for _, u := range in {
		if _, ok := skip[u]; !ok {
			out = append(out, u)
		}
	}
	return out
}

// folderChanged compares a STATUS with the stored baseline.
func folderChanged(f store.Folder, st *imap.StatusData) bool {
	if st == nil {
		return true
	}
	if st.UIDValidity != f.UIDValidity || uint32(st.UIDNext) != f.UIDNext {
		return true
	}
	if st.NumMessages != nil && int(*st.NumMessages) != f.ServerMessages {
		return true
	}
	if st.NumUnseen != nil && int(*st.NumUnseen) != f.ServerUnseen {
		return true
	}
	return false
}

// diffUIDs splits two ascending lists into local-only, server-only and
// common UIDs.
func diffUIDs(local, server []uint32) (gone, fresh, common []uint32) {
	i, j := 0, 0
	for i < len(local) && j < len(server) {
		switch {
		case local[i] < server[j]:
			gone = append(gone, local[i])
			i++
		case local[i] > server[j]:
			fresh = append(fresh, server[j])
			j++
		default:
			common = append(common, local[i])
			i++
			j++
		}
	}
	gone = append(gone, local[i:]...)
	fresh = append(fresh, server[j:]...)
	return gone, fresh, common
}

// envelopeOptions is the header fetch of a new message: the envelope plus
// the References field, which the envelope does not carry and which
// threading links on before the body is downloaded.
var envelopeOptions = imap.FetchOptions{
	UID: true, Flags: true, InternalDate: true, RFC822Size: true, Envelope: true,
	BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	BodySection:   []*imap.FetchItemBodySection{{Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"References"}, Peek: true}},
}

// maxReferencesHeaderBytes bounds what referencesFromHeader reads of a
// HEADER.FIELDS literal; a References field is a few hundred bytes, and
// the parser keeps 50 identifiers anyway.
const maxReferencesHeaderBytes = 64 << 10

// referencesFromHeader parses the References field out of a header-fields
// literal and drains the rest.
func referencesFromHeader(r io.Reader) []string {
	refs := mime.ParseReferences(io.LimitReader(r, maxReferencesHeaderBytes), mime.DefaultLimits())
	io.Copy(io.Discard, r)
	return refs
}

// fetchEnvelopes stores headers of the UIDs the store does not have yet.
// A message whose Message-ID matches a row waiting for a UID (a local
// move without COPYUID) reclaims that row instead of creating a new one.
func (s *Syncer) fetchEnvelopes(ctx context.Context, sess *session, f store.Folder, uids []uint32, progress func(float64)) error {
	for start := 0; start < len(uids); start += envelopeBatch {
		batch := uids[start:min(start+envelopeBatch, len(uids))]
		var msgs []*store.Message
		err := sess.do(ctx, commandTimeout, func() error {
			cmd := sess.Fetch(uidSet(batch), &envelopeOptions)
			for md := cmd.Next(); md != nil; md = cmd.Next() {
				m := s.messageFromFetch(md, f)
				if m.UID != 0 {
					msgs = append(msgs, m)
				}
			}
			return cmd.Close()
		})
		if err != nil {
			return err
		}
		fresh := msgs[:0]
		for _, m := range msgs {
			if m.RFCMessageID != "" {
				p, err := s.deps.Store.FindPendingMessage(ctx, f.ID, m.RFCMessageID)
				switch {
				case err == nil:
					if err := s.deps.Store.AssignUID(ctx, p.ID, m.UID, 0, m.Flags); err == nil {
						continue
					} else if !errors.Is(err, store.ErrNotFound) {
						return storageError(err)
					}
				case !errors.Is(err, store.ErrNotFound):
					return storageError(err)
				}
			}
			fresh = append(fresh, m)
		}
		if len(fresh) > 0 {
			if err := s.deps.Store.UpsertMessages(ctx, fresh); err != nil {
				return storageError(err)
			}
		}
		progress(float64(start+len(batch)) / float64(len(uids)))
	}
	progress(1)
	return nil
}

// messageFromFetch consumes one FETCH response into a store row. Every
// string is cleaned and capped; literals are drained.
func (s *Syncer) messageFromFetch(md *imapclient.FetchMessageData, f store.Folder) *store.Message {
	m := &store.Message{AccountID: s.account.ID, FolderID: f.ID, Flags: []api.Flag{}, BodyState: store.BodyNone}
	for item := md.Next(); item != nil; item = md.Next() {
		switch it := item.(type) {
		case imapclient.FetchItemDataUID:
			m.UID = uint32(it.UID)
		case imapclient.FetchItemDataFlags:
			m.Flags = toAPIFlags(it.Flags)
		case imapclient.FetchItemDataInternalDate:
			m.InternalDate = it.Time
		case imapclient.FetchItemDataRFC822Size:
			if it.Size > 0 {
				m.Size = it.Size
			}
		case imapclient.FetchItemDataEnvelope:
			if env := it.Envelope; env != nil {
				m.Subject = cleanField(env.Subject, maxFieldBytes)
				m.From = addresses(env.From)
				m.To = addresses(env.To)
				m.CC = addresses(env.Cc)
				m.BCC = addresses(env.Bcc)
				m.ReplyTo = addresses(env.ReplyTo)
				m.Date = env.Date
				m.RFCMessageID = cleanID(env.MessageID)
				if len(env.InReplyTo) > 0 {
					m.InReplyTo = cleanID(env.InReplyTo[0])
				}
			}
		case imapclient.FetchItemDataBodyStructure:
			m.Attachments, m.HasAttachments = attachmentsFromBodyStructure(it.BodyStructure)
		case imapclient.FetchItemDataBodySection:
			if it.Literal == nil {
				break
			}
			// The References field asked for by envelopeOptions; the
			// items of a FETCH reply come in the server's order.
			if it.Section != nil && it.Section.Specifier == imap.PartSpecifierHeader && len(it.Section.HeaderFields) > 0 {
				m.References = referencesFromHeader(it.Literal)
			} else {
				io.Copy(io.Discard, it.Literal)
			}
		case imapclient.FetchItemDataBinarySection:
			if it.Literal != nil {
				io.Copy(io.Discard, it.Literal)
			}
		}
	}
	if m.Date.IsZero() {
		m.Date = m.InternalDate
	}
	return m
}

// addresses converts an envelope address list: group markers dropped,
// strings cleaned, at most maxAddresses entries.
func addresses(in []imap.Address) []api.Address {
	out := make([]api.Address, 0, len(in))
	for _, a := range in {
		if a.IsGroupStart() || a.IsGroupEnd() {
			continue
		}
		addr := cleanField(a.Addr(), maxFieldBytes)
		name := cleanField(a.Name, maxFieldBytes)
		if addr == "" && name == "" {
			continue
		}
		out = append(out, api.Address{Name: name, Address: addr})
		if len(out) >= maxAddresses {
			break
		}
	}
	return out
}

// cleanID normalises a Message-ID style identifier: no brackets, no
// whitespace or control characters, capped.
func cleanID(s string) string {
	s = cleanField(strings.Trim(strings.TrimSpace(s), "<>"), maxFieldBytes)
	return strings.Join(strings.Fields(s), "")
}

// syncFlags re-reads the flags of messages both sides know.
func (s *Syncer) syncFlags(ctx context.Context, sess *session, f store.Folder, uids []uint32, progress func(float64)) error {
	done := 0
	for _, batch := range uidBatches(uids, flagBatch, maxSetBytes) {
		type flagged struct {
			uid   uint32
			flags []api.Flag
		}
		var got []flagged
		err := sess.do(ctx, commandTimeout, func() error {
			cmd := sess.Fetch(uidSet(batch), &imap.FetchOptions{UID: true, Flags: true})
			for md := cmd.Next(); md != nil; md = cmd.Next() {
				var fl flagged
				for item := md.Next(); item != nil; item = md.Next() {
					switch it := item.(type) {
					case imapclient.FetchItemDataUID:
						fl.uid = uint32(it.UID)
					case imapclient.FetchItemDataFlags:
						fl.flags = toAPIFlags(it.Flags)
					case imapclient.FetchItemDataBodySection:
						if it.Literal != nil {
							io.Copy(io.Discard, it.Literal)
						}
					}
				}
				if fl.uid != 0 && fl.flags != nil {
					got = append(got, fl)
				}
			}
			return cmd.Close()
		})
		if err != nil {
			return err
		}
		for _, fl := range got {
			if _, err := s.deps.Store.ApplyServerFlags(ctx, f.ID, fl.uid, fl.flags, 0); err != nil && !errors.Is(err, store.ErrNotFound) {
				return storageError(err)
			}
		}
		done += len(batch)
		progress(float64(done) / float64(len(uids)))
	}
	progress(1)
	return nil
}

// fetchBodies downloads the bodies the folder still lacks, newest first,
// in batches bounded by bodyBatchMessages and bodyBatchBytes. Messages
// over the raw cap are marked tooBig without a download; a message the
// server does not return is marked failed so the loop cannot stall. A
// message counts as new (notify.newMessage) once its body state is
// settled, when prevUIDNext > 0 and its UID is at or above it.
func (s *Syncer) fetchBodies(ctx context.Context, sess *session, f store.Folder, prevUIDNext uint32, progress func(float64)) error {
	attempted := map[string]bool{}
	done := 0
	for {
		refs, err := s.deps.Store.ListUnfetched(ctx, f.ID, 200)
		if err != nil {
			return storageError(err)
		}
		if len(refs) == 0 {
			break
		}
		var todo []store.MessageRef
		for _, r := range refs {
			switch {
			case attempted[r.ID]:
				s.log.Warn("body not returned by server", "message", r.ID)
				if err := s.settleBody(ctx, f, r, prevUIDNext, store.BodyFailed); err != nil {
					return err
				}
			case r.Size > s.rawLimit():
				attempted[r.ID] = true
				if err := s.settleBody(ctx, f, r, prevUIDNext, store.BodyTooBig); err != nil {
					return err
				}
			default:
				attempted[r.ID] = true
				todo = append(todo, r)
			}
		}
		total := done + len(todo)
		for len(todo) > 0 {
			var batch []store.MessageRef
			var bytes int64
			for len(todo) > 0 && len(batch) < bodyBatchMessages && (len(batch) == 0 || bytes+todo[0].Size <= bodyBatchBytes) {
				batch = append(batch, todo[0])
				bytes += todo[0].Size
				todo = todo[1:]
			}
			if err := s.fetchBodyBatch(ctx, sess, f, batch, prevUIDNext); err != nil {
				return err
			}
			done += len(batch)
			if total > 0 {
				progress(float64(done) / float64(total))
			}
		}
	}
	progress(1)
	return nil
}

// fetchBodyBatch runs one UID FETCH BODY.PEEK[] and stores every literal
// as it streams in. A storage error is remembered and returned once the
// command is fully consumed.
func (s *Syncer) fetchBodyBatch(ctx context.Context, sess *session, f store.Folder, batch []store.MessageRef, prevUIDNext uint32) error {
	byUID := make(map[uint32]store.MessageRef, len(batch))
	uids := make([]uint32, 0, len(batch))
	for _, r := range batch {
		byUID[r.UID] = r
		uids = append(uids, r.UID)
	}
	var storeErr error
	err := sess.do(ctx, bodyBatchTimeout, func() error {
		cmd := sess.Fetch(uidSet(uids), &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		})
		for md := cmd.Next(); md != nil; md = cmd.Next() {
			var uid uint32
			for item := md.Next(); item != nil; item = md.Next() {
				switch it := item.(type) {
				case imapclient.FetchItemDataUID:
					uid = uint32(it.UID)
				case imapclient.FetchItemDataBodySection:
					if it.Literal == nil {
						continue
					}
					ref, ok := byUID[uid]
					if !ok || storeErr != nil {
						io.Copy(io.Discard, it.Literal)
						continue
					}
					delete(byUID, uid)
					if err := s.storeBody(ctx, f, ref, it.Literal, prevUIDNext); err != nil {
						storeErr = err
					}
				case imapclient.FetchItemDataBinarySection:
					if it.Literal != nil {
						io.Copy(io.Discard, it.Literal)
					}
				}
			}
		}
		return cmd.Close()
	})
	if err != nil {
		return err
	}
	return storeErr
}

// storeBody writes the raw message, parses it and stores the result; the
// literal is always drained so the decoder can continue.
func (s *Syncer) storeBody(ctx context.Context, f store.Folder, ref store.MessageRef, lit io.Reader, prevUIDNext uint32) error {
	_, err := s.deps.Store.WriteMessageRaw(ctx, s.account.ID, ref.ID, lit, s.rawLimit())
	io.Copy(io.Discard, lit)
	switch {
	case errors.Is(err, store.ErrTooBig):
		return s.settleBody(ctx, f, ref, prevUIDNext, store.BodyTooBig)
	case err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return storageError(err)
	}
	raw, err := s.deps.Store.OpenMessageRaw(ctx, s.account.ID, ref.ID)
	if err != nil {
		return storageError(err)
	}
	parsed, perr := mime.Parse(raw, mime.DefaultLimits())
	raw.Close()
	if perr != nil {
		s.log.Warn("message body unparsable", "message", ref.ID, "err", perr)
		return s.settleBody(ctx, f, ref, prevUIDNext, store.BodyFailed)
	}
	u := store.BodyUpdate{
		Text:           parsed.Text,
		HasHTML:        parsed.HasHTML,
		Snippet:        parsed.Snippet,
		Attachments:    parsed.Attachments,
		HasAttachments: parsed.HasAttachments,
		Headers:        parsed.Headers,
		References:     parsed.References,
		State:          store.BodyFetched,
		Subject:        parsed.Subject,
		From:           parsed.From,
		Date:           parsed.Date,
		RFCMessageID:   parsed.MessageID,
		InReplyTo:      parsed.InReplyTo,
	}
	if err := s.deps.Store.SetMessageBody(ctx, ref.ID, u); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // deleted meanwhile
		}
		return storageError(err)
	}
	s.notifyNew(ctx, f, ref, prevUIDNext)
	return nil
}

// settleBody records a terminal body state and reports the message.
// rawLimit is the largest message whose body is downloaded.
func (s *Syncer) rawLimit() int64 {
	if s.deps.MaxRawMessageBytes > 0 {
		return s.deps.MaxRawMessageBytes
	}
	return maxRawMessageBytes
}

func (s *Syncer) settleBody(ctx context.Context, f store.Folder, ref store.MessageRef, prevUIDNext uint32, state store.BodyState) error {
	if err := s.deps.Store.MarkBodyState(ctx, ref.ID, state); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return storageError(err)
	}
	s.notifyNew(ctx, f, ref, prevUIDNext)
	return nil
}

// notifyNew emits notify.newMessage for a message that arrived after the
// folder's previous pass (docs/api.md §5). Folders holding the user's own
// mail (sent, drafts, trash, junk, outbox) never notify: a copy of a sent
// message is not new mail.
func (s *Syncer) notifyNew(ctx context.Context, f store.Folder, ref store.MessageRef, prevUIDNext uint32) {
	if s.deps.Notifier == nil || prevUIDNext == 0 || ref.UID < prevUIDNext {
		return
	}
	switch f.Role {
	case api.RoleSent, api.RoleDrafts, api.RoleTrash, api.RoleJunk, api.RoleOutbox:
		return
	}
	m, err := s.deps.Store.GetMessage(ctx, s.account.ID, ref.ID)
	if err != nil {
		return
	}
	s.deps.Notifier.NewMessage(api.NewMessageNotification{
		AccountID: api.AccountID(s.account.ID),
		FolderID:  api.FolderID(f.ID),
		Message:   summaryOf(m),
	})
}

// summaryOf is the list-view projection of a stored message.
func summaryOf(m store.Message) api.MessageSummary {
	sum := api.MessageSummary{
		ID:             api.MessageID(m.ID),
		AccountID:      api.AccountID(m.AccountID),
		FolderID:       api.FolderID(m.FolderID),
		ThreadID:       api.ThreadID(m.ThreadID),
		From:           m.From,
		To:             m.To,
		Subject:        m.Subject,
		Date:           m.Date,
		Snippet:        m.Snippet,
		Flags:          m.Flags,
		HasAttachments: m.HasAttachments,
		Size:           m.Size,
	}
	if sum.From == nil {
		sum.From = []api.Address{}
	}
	if sum.Flags == nil {
		sum.Flags = []api.Flag{}
	}
	if sum.Date.IsZero() {
		sum.Date = m.InternalDate
	}
	return sum
}

// storageError wraps a store failure into the contract code that maps to
// the "error" sync status.
func storageError(err error) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return api.NewError(api.CodeStorageError, "%v", err)
}
