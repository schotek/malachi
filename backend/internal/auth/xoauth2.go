// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"errors"

	"github.com/emersion/go-sasl"
)

// XOAuth2 is the SASL mechanism Google and Microsoft use for OAuth2 bearer
// tokens over IMAP and SMTP. go-sasl ships OAUTHBEARER (RFC 7628) but not
// this older cousin, which is what the servers advertise first.
const XOAuth2 = "XOAUTH2"

// NewXOAuth2Client returns the SASL XOAUTH2 client for username and an
// access token. The exchange is one initial response; on failure the
// server sends a base64 JSON report as a challenge, which an empty reply
// acknowledges so that the outcome surfaces as the server's own NO. The
// token is held for the exchange only and never appears in an error.
func NewXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

type xoauth2Client struct {
	username, token string
	challenged      bool
}

func (c *xoauth2Client) Start() (mech string, ir []byte, err error) {
	ir = []byte("user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01")
	return XOAuth2, ir, nil
}

func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	if c.challenged {
		return nil, errors.New("xoauth2: unexpected second challenge")
	}
	c.challenged = true
	// The challenge is the server's error report; the empty response lets
	// it finish with its verdict.
	return []byte{}, nil
}
