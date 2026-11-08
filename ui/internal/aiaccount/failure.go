// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package aiaccount

import (
	"errors"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Field is a field of the dialog that can be flagged and focused.
type Field int

// The fields.
const (
	FieldNone Field = iota
	FieldName
	FieldServer
	FieldKey
	FieldModel
	FieldBoardModel
	FieldConsent
)

// Failure is what the dialog shows after a failed call: the banner and
// the field to flag. An empty Banner means the client's general sentence
// for the error (Env.ErrorText) with the step's action.
type Failure struct {
	Banner string
	Field  Field
}

// FailureOf words a failed account.test, account.add or account.update
// of an AI provider account by the error's code (docs/api.md §2): a
// refused key, an unreachable or slow provider, an insecure connection
// (the TLS details are TrustOffer's), a provider error. Anything else
// (invalidArgument, keyringError, …) gets the general sentence.
func FailureOf(err error, tr Translator) Failure {
	var e *api.Error
	if !errors.As(err, &e) || e == nil {
		return Failure{}
	}
	s := Texts(tr)
	switch e.Code {
	case api.CodeAuthFailed:
		return Failure{Banner: s.KeyRefused, Field: FieldKey}
	case api.CodeAuthRequired:
		return Failure{Banner: s.KeyMissing, Field: FieldKey}
	case api.CodeNetworkError, api.CodeOffline:
		return Failure{Banner: s.Unreachable, Field: FieldServer}
	case api.CodeServerTimeout:
		return Failure{Banner: s.Timeout, Field: FieldServer}
	case api.CodeTLSError:
		return Failure{Banner: s.Insecure, Field: FieldServer}
	case api.CodeServerError:
		return Failure{Banner: s.ServerError}
	case api.CodeAssistantConsentRequired:
		return Failure{Banner: s.ConsentMissing, Field: FieldConsent}
	}
	return Failure{}
}

// asAPIError reads the daemon's error out of err.
func asAPIError(err error, e **api.Error) bool {
	return errors.As(err, e) && *e != nil
}
