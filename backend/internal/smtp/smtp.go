// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package smtp is the client side of message submission.
//
// Library: github.com/emersion/go-smtp (client only) and
// github.com/emersion/go-message/mail for serialisation.
//
// It has three entry points:
//
//   - Probe / Verify (probe.go) check an endpoint during account setup:
//     connect under the configured security, authenticate, report the
//     scrubbed capabilities.
//   - BuildMessage (build.go) serialises a draft into an RFC 5322 message.
//     Every header value is cleaned before it is written, so user or
//     server supplied text can never inject header lines; Bcc is never
//     part of the message, only of the envelope.
//   - Deliver (deliver.go) submits one already-built message over a fresh
//     connection and classifies failures into a *SendError that says
//     whether a retry can help.
//
// The persistent outbox queue and its retry worker live in
// internal/outbox; this package holds no state between calls and never
// logs traffic or credentials.
package smtp
