// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
)

// ErrGone reports that the service no longer has the message under its id
// (404): another client deleted it, or moved it out of the mailbox. The
// next pass removes the local copy.
var ErrGone = errors.New("graph: the message is no longer on the server")

// maxValueDrain is what is read of a $value body after its consumer is
// done, so the connection can be reused; a longer rest closes it.
const maxValueDrain = 1 << 20

// valuePath is the MIME content of a message.
func valuePath(remoteID string) string {
	return "me/messages/" + url.PathEscape(remoteID) + "/$value"
}

// FetchMessage downloads one message's MIME content ($value) for
// message.download and streams it into fn; the size is not announced
// (-1). The client's policy applies: the token is asked again once after a
// 401, a 429 or 503 is retried a bounded number of times.
//
// Errors: ErrGone; the *api.Error of the request (ToAPIError); and fn's
// own error, returned as it is, unless the body stopped arriving under fn,
// which is the transfer's failure. When fn fails the body is closed rather
// than drained.
func FetchMessage(ctx context.Context, c *Client, remoteID string, fn func(r io.Reader, size int64) error) error {
	if remoteID == "" {
		return fmt.Errorf("%w: no message id", ErrGone)
	}
	rc, err := c.GetRaw(ctx, valuePath(remoteID))
	switch {
	case IsNotFound(err):
		return fmt.Errorf("%w: the service answered 404", ErrGone)
	case err != nil:
		return ToAPIError(err)
	}
	src := &transferReader{r: rc}
	if err := fn(src, -1); err != nil {
		rc.Close()
		if src.err != nil {
			return ToAPIError(src.err)
		}
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, maxValueDrain))
	rc.Close()
	return nil
}

// transferReader remembers the first failure of the body it reads, so a
// broken download can be told from a failure of its consumer.
type transferReader struct {
	r   io.Reader
	err error
}

func (t *transferReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}
