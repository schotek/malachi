// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Stage says where in a probe an error happened; it decides between the
// network, TLS and server error codes when the error itself is ambiguous.
type Stage string

const (
	StageDial     Stage = "dial"     // TCP connect (+ implicit TLS handshake)
	StageTLS      Stage = "tls"      // STARTTLS upgrade
	StageGreeting Stage = "greeting" // greeting / EHLO / CAPABILITY
	StageAuth     Stage = "auth"
	StageCommand  Stage = "command" // anything after authentication
)

// Classify maps a transport or library error to the contract error. It
// never returns nil for a non-nil err, and the message never carries
// credentials: it is built from the stage and the error's own text,
// control-stripped and capped. Protocol-specific errors (IMAP NO/BAD, SMTP
// reply codes) are handled by the protocol packages before they get here.
func Classify(ctx context.Context, stage Stage, err error) *api.Error {
	if err == nil {
		return nil
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	msg := CleanMessage(string(stage) + ": " + err.Error())

	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		return api.NewError(api.CodeCancelled, "cancelled")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.Is(ctx.Err(), context.DeadlineExceeded), isTimeout(err):
		return api.NewError(api.CodeServerTimeout, "%s", msg)
	case isTLSError(err):
		return api.NewError(api.CodeTLSError, "%s", msg)
	}

	switch stage {
	case StageDial:
		return api.NewError(api.CodeNetworkError, "%s", msg)
	case StageTLS:
		return api.NewError(api.CodeTLSError, "%s", msg)
	}
	if isConnectionLost(err) {
		return api.NewError(api.CodeNetworkError, "%s", msg)
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		return api.NewError(api.CodeNetworkError, "%s", msg)
	}
	return api.NewError(api.CodeServerError, "%s", msg)
}

func isTimeout(err error) bool {
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}

func isTLSError(err error) bool {
	var (
		cve  *tls.CertificateVerificationError
		ua   x509.UnknownAuthorityError
		hn   x509.HostnameError
		ci   x509.CertificateInvalidError
		rh   tls.RecordHeaderError
		alrt tls.AlertError
	)
	return errors.As(err, &cve) || errors.As(err, &ua) || errors.As(err, &hn) ||
		errors.As(err, &ci) || errors.As(err, &rh) || errors.As(err, &alrt)
}

// isConnectionLost covers the server (or our watchdog) closing the
// connection under a running exchange.
func isConnectionLost(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}

// CleanMessage makes server or library text safe to forward: valid UTF-8,
// no control characters, at most MaxMessageBytes.
func CleanMessage(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > MaxMessageBytes {
		s = s[:MaxMessageBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}
