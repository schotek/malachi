// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// submissionTimeout bounds one DATA write and the final 250 after the
// terminating dot; the context bounds the whole delivery.
const submissionTimeout = 2 * time.Minute

// Stages of a delivery, as reported in SendError.Stage.
const (
	StageConnect = "connect" // dial, TLS, greeting, EHLO
	StageAuth    = "auth"
	StageSize    = "size" // the server's SIZE limit is smaller than the message
	StageMail    = "mail" // MAIL FROM
	StageRcpt    = "rcpt" // RCPT TO
	StageData    = "data" // DATA, the message body, the final reply
)

// SendError is a classified delivery failure. Err carries the contract
// code and a cleaned message (never credentials, never more of the
// recipient list than the server echoed); Permanent says a retry with the
// same message and settings cannot help.
type SendError struct {
	Err       *api.Error
	Stage     string
	Permanent bool
}

func (e *SendError) Error() string {
	kind := "transient"
	if e.Permanent {
		kind = "permanent"
	}
	return fmt.Sprintf("send %s (%s): %v", e.Stage, kind, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

// Deliver submits one already-serialised message of size bytes read from r
// to the envelope recipients rcpts. It connects, authenticates, checks the
// server's SIZE limit, runs MAIL/RCPT/DATA and quits. Cancelling ctx closes
// the socket. The result is nil or a *SendError.
func Deliver(ctx context.Context, cfg api.ServerConfig, password, from string, rcpts []string, r io.Reader, size int64) error {
	if cfg.AuthMethod != api.AuthPassword {
		return &SendError{Err: api.ErrNotImplemented, Stage: StageAuth, Permanent: true}
	}
	if !validEnvelopeAddress(from) {
		return &SendError{Err: api.NewError(api.CodeInvalidArgument, "invalid envelope sender"), Stage: StageMail, Permanent: true}
	}
	rcpts = dedupe(rcpts)
	if len(rcpts) == 0 {
		return &SendError{Err: api.NewError(api.CodeInvalidArgument, "no recipients"), Stage: StageRcpt, Permanent: true}
	}
	utf8 := !isASCII(from)
	for _, to := range rcpts {
		if !validEnvelopeAddress(to) {
			return &SendError{Err: api.NewError(api.CodeInvalidArgument, "invalid envelope recipient"), Stage: StageRcpt, Permanent: true}
		}
		utf8 = utf8 || !isASCII(to)
	}

	c, _, err := Connect(ctx, cfg)
	if err != nil {
		return classifySend(ctx, StageConnect, err)
	}
	defer c.Close()
	c.SubmissionTimeout = submissionTimeout

	// Server text after this point may echo the credentials.
	classifySend := func(ctx context.Context, stage string, err error) *SendError {
		return redact(classifySend(ctx, stage, err), password)
	}

	if err := authenticate(ctx, c, cfg, password); err != nil {
		return classifySend(ctx, StageAuth, err)
	}

	if limit, ok := c.MaxMessageSize(); ok && limit > 0 && size > int64(limit) {
		return &SendError{
			Err:       api.NewError(api.CodeServerError, "message of %d bytes exceeds the server limit of %d bytes", size, limit),
			Stage:     StageSize,
			Permanent: true,
		}
	}

	if err := c.Mail(from, &smtp.MailOptions{Size: size, UTF8: utf8}); err != nil {
		return classifySend(ctx, StageMail, err)
	}
	for _, to := range rcpts {
		if err := c.Rcpt(to, nil); err != nil {
			return classifySend(ctx, StageRcpt, err)
		}
	}

	dc, err := c.Data()
	if err != nil {
		return classifySend(ctx, StageData, err)
	}
	// A failure here must not be followed by Close: that would send the
	// terminating dot and deliver a truncated message. The deferred
	// c.Close drops the connection instead.
	if _, err := io.Copy(dc, &taggedReader{r: r}); err != nil {
		return classifySend(ctx, StageData, err)
	}
	if err := dc.Close(); err != nil {
		return classifySend(ctx, StageData, err)
	}
	quit(c)
	return nil
}

// classifySend turns any error of a delivery stage into a *SendError.
func classifySend(ctx context.Context, stage string, err error) *SendError {
	// The caller gave up: whatever the socket reported afterwards is noise.
	if ctx.Err() != nil {
		return &SendError{Err: api.NewError(api.CodeCancelled, "cancelled"), Stage: stage}
	}
	if errors.Is(err, errNoAuthMechanism) {
		return &SendError{Err: errNoAuthMechanism, Stage: stage, Permanent: true}
	}

	var re *readError
	if errors.As(err, &re) {
		var apiErr *api.Error
		if !errors.As(re.err, &apiErr) {
			apiErr = api.NewError(api.CodeStorageError, "reading message: %s", transport.CleanMessage(re.err.Error()))
		}
		return &SendError{Err: apiErr, Stage: stage}
	}

	var se *smtp.SMTPError
	if errors.As(err, &se) {
		if stage == StageAuth {
			// 535/534 → authFailed, 530/538 → tlsError, else serverError;
			// none of them is final: credentials or settings can change.
			return &SendError{Err: toAPIError(classify(ctx, transport.StageAuth, err)), Stage: stage}
		}
		return &SendError{
			Err:       api.NewError(api.CodeServerError, "%s: server said %d %s", stage, se.Code, transport.CleanMessage(se.Message)),
			Stage:     stage,
			Permanent: !se.Temporary(),
		}
	}

	if strings.Contains(err.Error(), "does not support SMTPUTF8") {
		return &SendError{
			Err:       api.NewError(api.CodeServerError, "server does not support SMTPUTF8, needed for a non-ASCII address"),
			Stage:     stage,
			Permanent: true,
		}
	}

	tstage := transport.StageCommand
	if stage == StageAuth {
		tstage = transport.StageAuth
	}
	return &SendError{Err: toAPIError(classify(ctx, tstage, err)), Stage: stage}
}

// redact removes the password from a SendError's message in case the
// server echoed it. The api.Error is copied: some are shared sentinels.
func redact(se *SendError, password string) *SendError {
	if password == "" || !strings.Contains(se.Err.Message, password) {
		return se
	}
	e := *se.Err
	e.Message = strings.ReplaceAll(e.Message, password, "***")
	se.Err = &e
	return se
}

// toAPIError is for errors that are already classified (*api.Error, possibly
// wrapped); anything else becomes a generic server error.
func toAPIError(err error) *api.Error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return api.NewError(api.CodeServerError, "%s", transport.CleanMessage(err.Error()))
}

// readError marks an error that came from the message reader rather than
// from the connection.
type readError struct{ err error }

func (e *readError) Error() string { return e.err.Error() }
func (e *readError) Unwrap() error { return e.err }

type taggedReader struct{ r io.Reader }

func (t *taggedReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF {
		err = &readError{err: err}
	}
	return n, err
}

// validEnvelopeAddress rejects anything that could break out of the
// MAIL FROM:<…> / RCPT TO:<…> argument: control characters, whitespace
// and angle brackets. Syntax beyond that is the server's business.
func validEnvelopeAddress(a string) bool {
	if a == "" || len(a) > maxHeaderBytes {
		return false
	}
	for _, r := range a {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '<' || r == '>' {
			return false
		}
	}
	return strings.Count(a, "@") == 1 && a[0] != '@' && a[len(a)-1] != '@'
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// dedupe drops repeated recipients (case-insensitively, as every real
// server treats them) while keeping the first occurrence's position.
func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		dup := false
		for _, seen := range out {
			if strings.EqualFold(a, seen) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}
