// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"

	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// maxHeaderBytes bounds the header block Deliver inspects for Bcc.
const maxHeaderBytes = 1 << 20

// Deliver submits one already-serialised message of size bytes read from r
// through sendMail. Graph takes the recipients from the headers, not from
// an envelope, so blind recipients — which the builder keeps out of the
// headers on purpose — are added as a Bcc header first. The Sent copy is
// filed by the service. The result is nil or a *smtp.SendError, so the
// outbox worker treats it like an SMTP outcome.
func Deliver(ctx context.Context, c *Client, from string, rcpts []string, r io.Reader, size int64) error {
	raw, err := io.ReadAll(io.LimitReader(r, api.MaxOutgoingMessageBytes+1))
	if err != nil {
		return &smtp.SendError{Err: api.NewError(api.CodeStorageError, "reading message: %s", transport.CleanMessage(err.Error())), Stage: smtp.StageData}
	}
	if int64(len(raw)) > api.MaxOutgoingMessageBytes {
		return &smtp.SendError{Err: api.NewError(api.CodeServerError, "message larger than %d bytes", api.MaxOutgoingMessageBytes), Stage: smtp.StageSize, Permanent: true}
	}
	raw, err = withBcc(raw, rcpts)
	if err != nil {
		return &smtp.SendError{Err: api.NewError(api.CodeServerError, "message headers: %s", transport.CleanMessage(err.Error())), Stage: smtp.StageData, Permanent: true}
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(encoded, raw)
	body := func() (io.Reader, error) { return bytes.NewReader(encoded), nil }
	err = c.PostRaw(ctx, "me/sendMail", body, "text/plain", int64(len(encoded)))
	if err == nil {
		return nil
	}
	return classifySend(ctx, err)
}

// classifySend maps a sendMail failure to the outbox worker's contract.
func classifySend(ctx context.Context, err error) *smtp.SendError {
	if ctx.Err() != nil {
		return &smtp.SendError{Err: api.NewError(api.CodeCancelled, "cancelled"), Stage: smtp.StageData}
	}
	var se *StatusError
	if !errors.As(err, &se) {
		return &smtp.SendError{Err: ToAPIError(err), Stage: smtp.StageConnect}
	}
	ae := ToAPIError(err)
	switch {
	case se.Status == http.StatusUnauthorized:
		return &smtp.SendError{Err: ae, Stage: smtp.StageAuth}
	case se.Status == http.StatusTooManyRequests, se.Status >= 500:
		return &smtp.SendError{Err: ae, Stage: smtp.StageData}
	case se.Status == http.StatusForbidden && strings.Contains(se.Code, "SendAs"):
		return &smtp.SendError{Err: ae, Stage: smtp.StageMail, Permanent: true}
	case se.Status == http.StatusRequestEntityTooLarge, strings.Contains(se.Code, "SizeExceeded"):
		return &smtp.SendError{Err: ae, Stage: smtp.StageSize, Permanent: true}
	}
	// Any other 4xx will not change with a retry of the same message.
	return &smtp.SendError{Err: ae, Stage: smtp.StageData, Permanent: true}
}

// withBcc returns the message with a Bcc header naming the envelope
// recipients that appear in neither To nor Cc. The header block is read
// with a cap; a message whose header block cannot be delimited is an
// error.
func withBcc(raw []byte, rcpts []string) ([]byte, error) {
	end := headerEnd(raw)
	if end < 0 {
		return nil, fmt.Errorf("no end of header block within %d bytes", maxHeaderBytes)
	}
	header := raw[:end]
	msg, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(append(append([]byte{}, header...), "\r\n\r\n"...))))
	if err != nil {
		return nil, err
	}
	visible := map[string]bool{}
	for _, name := range []string{"To", "Cc"} {
		for _, v := range msg.Header[name] {
			list, err := mail.ParseAddressList(v)
			if err != nil {
				continue
			}
			for _, a := range list {
				visible[strings.ToLower(a.Address)] = true
			}
		}
	}
	var bcc []string
	seen := map[string]bool{}
	for _, r := range rcpts {
		key := strings.ToLower(strings.TrimSpace(r))
		if key == "" || visible[key] || seen[key] {
			continue
		}
		seen[key] = true
		bcc = append(bcc, (&mail.Address{Address: strings.TrimSpace(r)}).String())
	}
	if len(bcc) == 0 {
		return raw, nil
	}
	line := []byte("Bcc: " + strings.Join(bcc, ",\r\n ") + "\r\n")
	out := make([]byte, 0, len(raw)+len(line))
	out = append(out, header...)
	out = append(out, line...)
	out = append(out, raw[end:]...)
	return out, nil
}

// headerEnd is the offset of the blank line ending the header block (the
// start of the CRLF CRLF or LF LF sequence), or -1.
func headerEnd(raw []byte) int {
	limit := min(len(raw), maxHeaderBytes)
	for i := 0; i < limit; i++ {
		if raw[i] != '\n' {
			continue
		}
		if i+1 < len(raw) && raw[i+1] == '\n' {
			return i + 1
		}
		if i+2 < len(raw) && raw[i+1] == '\r' && raw[i+2] == '\n' {
			return i + 1
		}
	}
	return -1
}
