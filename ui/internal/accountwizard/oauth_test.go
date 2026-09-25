// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"errors"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The failure classification itself is tested in package signin; this is
// the sentence for each kind (untranslated in tests).
func TestOAuthErrorText(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{api.NewError(api.CodeCancelled, "access_denied"), "The sign-in was cancelled"},
		{api.NewError(api.CodeAuthFailed, "invalid_grant"), "The sign-in with Google was refused"},
		{api.NewError(api.CodeServerTimeout, "expired"), "The sign-in took too long; try again"},
		{context.DeadlineExceeded, "The sign-in took too long; try again"},
		{&api.Error{Code: api.CodeInvalidArgument, Message: "other mailbox", Data: map[string]any{"signedInAs": "other@gmail.com"}},
			"The browser signed in to other@gmail.com, not to this address"},
		{api.NewError(api.CodeNetworkError, "dial"), "Signing in failed: the server could not be reached"},
		{errors.New("boom"), "Signing in failed"},
	} {
		if got := oauthErrorText("Google", c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}

func TestBrowserPage(t *testing.T) {
	p := BrowserPage()
	for _, s := range []string{p.SuccessTitle, p.SuccessText, p.FailureTitle, p.FailureText} {
		if s == "" || len([]rune(s)) > 200 {
			t.Errorf("browser page text %q: empty or over the daemon's 200 characters", s)
		}
	}
}

func TestProviderLabel(t *testing.T) {
	if providerLabel("google", "me@gmail.com") != "Google" || providerLabel("microsoft365", "") != "Microsoft 365" ||
		providerLabel("", "me@example.org") != "example.org" {
		t.Error("provider label")
	}
}
