// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// ErrGone reports that the server no longer has the message where the
// store says it is: the mailbox does not exist any more, its UIDVALIDITY
// changed, or the UID names no message any more (another client deleted or
// moved it). The next pass of the folder removes the local copy.
var ErrGone = errors.New("imap: the message is no longer on the server")

// codeExpungeIssued is RFC 5530's EXPUNGEISSUED, which go-imap does not
// name: another session expunged what the command asked for.
const codeExpungeIssued imap.ResponseCode = "EXPUNGEISSUED"

// Location is where FetchMessage finds a message: its mailbox, the
// mailbox's UIDVALIDITY the UID belongs to (0 = not checked) and the UID.
type Location struct {
	Mailbox     string
	UIDValidity uint32
	UID         uint32
}

// FetchMessage downloads one message on a connection of its own, apart
// from the account's syncer and its IDLE (message.download): it logs in
// under the endpoint's policy (a pinned certificate included), opens the
// mailbox read-only (EXAMINE), checks its UIDVALIDITY and streams the
// message (UID FETCH BODY.PEEK[]) into fn with the size the server
// announced, then logs out. Nothing on the server changes: EXAMINE and
// PEEK set no flag.
//
// Errors: ErrGone; unavailable for a refusal for the moment (refused); the
// *api.Error of the connection, the login or a command (networkError,
// serverTimeout, authFailed, tlsError, serverError for any other NO…); and
// fn's own error, returned as it is, unless the message stopped arriving
// under fn, which is the connection's failure. When fn fails the
// connection is closed rather than drained: a message may be 25 MiB.
func FetchMessage(ctx context.Context, cfg api.ServerConfig, secret string, loc Location, fn func(r io.Reader, size int64) error) error {
	if loc.Mailbox == "" || loc.UID == 0 {
		return fmt.Errorf("%w: no mailbox or UID", ErrGone)
	}
	sess, err := openSession(ctx, cfg, secret, nil)
	if err != nil {
		return err
	}
	var sel *imap.SelectData
	err = sess.do(ctx, commandTimeout, func() error {
		var err error
		sel, err = sess.Select(loc.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
		return err
	})
	switch {
	case isNo(err) && noCode(err) == "":
		// RFC 3501 has one NO for "no such mailbox" and "can't access
		// mailbox", and servers without RFC 5530's codes use it for both:
		// the mailbox list tells them apart.
		missing, lerr := mailboxMissing(ctx, sess, loc.Mailbox)
		switch {
		case lerr != nil:
			sess.Close()
			return lerr
		case missing:
			sess.logout()
			return fmt.Errorf("%w: the mailbox does not exist", ErrGone)
		}
		sess.logout()
		return err
	case isNo(err):
		sess.logout()
		return refused(err, "the mailbox cannot be opened")
	case err != nil:
		sess.Close()
		return err
	case loc.UIDValidity != 0 && sel.UIDValidity != loc.UIDValidity:
		sess.logout()
		return fmt.Errorf("%w: the mailbox's UIDVALIDITY changed", ErrGone)
	}

	var (
		found   bool
		fnErr   error
		readErr error
	)
	err = sess.do(ctx, bodyBatchTimeout, func() error {
		cmd := sess.Fetch(imap.UIDSetNum(imap.UID(loc.UID)), &imap.FetchOptions{
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
					// The library routes a response to this command by the
					// UID it carries before the literal; checked again.
					if it.Literal == nil || uid != loc.UID || found || fnErr != nil {
						continue
					}
					found = true
					src := newLiteralReader(it.Literal)
					if err := fn(src, it.Literal.Size()); err != nil {
						fnErr = err
						// Unread bytes would have to be drained; the
						// connection is given up instead.
						sess.raw.Close()
						if src.err != nil {
							// The connection broke under the literal. The
							// library must not read the literal again (its
							// reader runs on), and it stops by itself.
							readErr = src.err
							return readErr
						}
					}
				}
			}
		}
		return cmd.Close()
	})
	switch {
	case fnErr != nil && readErr != nil:
		sess.Close()
		return classify(ctx, transport.StageCommand, readErr)
	case fnErr != nil:
		sess.Close()
		return fnErr
	case found:
		// The whole literal reached fn; whatever the tagged answer says
		// about the command cannot change the message.
		if err != nil {
			sess.Close()
		} else {
			sess.logout()
		}
		return nil
	case isNo(err):
		// A UID that names no message is no error (below); a NO without a
		// code says nothing about the message, and stays the server's.
		sess.logout()
		return refused(err, "the server refused the UID")
	case err != nil:
		sess.Close()
		return err
	}
	sess.logout()
	return fmt.Errorf("%w: no message with that UID", ErrGone)
}

// isNo reports a NO answer to one command: a refusal about the command's
// object, the connection itself being fine.
func isNo(err error) bool {
	var ie *imap.Error
	return errors.As(err, &ie) && ie.Type == imap.StatusResponseTypeNo
}

// noCode is the RFC 5530 response code of a NO answer, upper-cased (an
// atom, which a server may send in any case); "" for none.
func noCode(err error) imap.ResponseCode {
	var ie *imap.Error
	if errors.As(err, &ie) && ie.Type == imap.StatusResponseTypeNo {
		return imap.ResponseCode(strings.ToUpper(string(ie.Code)))
	}
	return ""
}

// refused maps a NO about the message's mailbox or UID by its code: only
// NONEXISTENT (not there) and EXPUNGEISSUED (another client expunged it)
// say the message is gone. UNAVAILABLE, INUSE and LIMIT are a refusal for
// the moment, unavailable, to be tried again later; any other code, and
// none, stays the server's error (err, serverError), since reporting the
// message gone would tell the user it was deleted.
func refused(err error, what string) error {
	switch code := noCode(err); code {
	case imap.ResponseCodeNonExistent, codeExpungeIssued:
		return fmt.Errorf("%w: %s (%s)", ErrGone, what, code)
	case imap.ResponseCodeUnavailable, imap.ResponseCodeInUse, imap.ResponseCodeLimit:
		return api.NewError(api.CodeUnavailable, "%s for now (%s); try again later", what, code)
	}
	return err
}

// mailboxMissing reports whether the server's mailbox list lacks the
// mailbox, or shows it as one that holds no messages (\NonExistent,
// \Noselect). The name is matched exactly (a wildcard in it may list
// others), INBOX in any case.
func mailboxMissing(ctx context.Context, sess *session, mailbox string) (bool, error) {
	var list []*imap.ListData
	err := sess.do(ctx, commandTimeout, func() error {
		var err error
		list, err = sess.List("", mailbox, nil).Collect()
		return err
	})
	if err != nil {
		return false, err
	}
	inbox := strings.EqualFold(mailbox, "INBOX")
	for _, d := range list {
		if d.Mailbox != mailbox && !(inbox && strings.EqualFold(d.Mailbox, "INBOX")) {
			continue
		}
		for _, attr := range d.Attrs {
			if strings.EqualFold(string(attr), string(imap.MailboxAttrNonExistent)) ||
				strings.EqualFold(string(attr), string(imap.MailboxAttrNoSelect)) {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}

// literalReader reads one literal and remembers the first failure of the
// transfer, so that it can be told from a failure of the literal's
// consumer. The library ends a literal at its announced size or where the
// connection closes, whichever comes first, and reports both as io.EOF: a
// literal shorter than announced is io.ErrUnexpectedEOF here, so a message
// cut off in transit is never stored as if it were whole.
type literalReader struct {
	r    io.Reader
	want int64
	n    int64
	err  error
}

func newLiteralReader(lit imap.LiteralReader) *literalReader {
	return &literalReader{r: lit, want: lit.Size()}
}

// Read reads the literal. After a failure it only repeats the failure:
// the library's reader has handed the connection back to its own reader
// goroutine by then, and must not be read again.
func (l *literalReader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	if err == io.EOF && l.n < l.want {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF {
		l.err = err
	}
	return n, err
}
