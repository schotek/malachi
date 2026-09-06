// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// ProbeResult is what a successful Probe learned.
type ProbeResult struct {
	Capabilities []string      // scrubbed post-login CAPABILITY
	Latency      time.Duration // dial start → ready (greeting read, STARTTLS done)
}

// Connect dials, secures the connection according to cfg.Security and reads
// the greeting. It never authenticates and never logs traffic. Errors are
// *api.Error. The caller must Close the result while ctx is still alive.
func Connect(ctx context.Context, cfg api.ServerConfig) (*Conn, time.Duration, error) {
	return connect(ctx, cfg, nil)
}

// Probe connects, authenticates with the password (or, for an oauth2
// endpoint, the access token passed in its place), reads CAPABILITY and
// logs out.
func Probe(ctx context.Context, cfg api.ServerConfig, password string) (ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, transport.EndpointTimeout)
	defer cancel()

	c, latency, err := Connect(ctx, cfg)
	if err != nil {
		return ProbeResult{}, err
	}
	defer c.Close()

	if err := login(ctx, c, cfg, password); err != nil {
		return ProbeResult{}, err
	}

	set, err := c.Capability().Wait()
	if err != nil {
		return ProbeResult{}, classify(ctx, transport.StageCommand, err)
	}
	names := make([]string, 0, len(set))
	for cap := range set {
		names = append(names, string(cap))
	}
	res := ProbeResult{Capabilities: transport.CleanCapabilities(names), Latency: latency}

	done := make(chan struct{})
	go func() { _ = c.Logout().Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(transport.QuitTimeout):
	}
	return res, nil
}

// Verify is Connect plus Close: it proves that the endpoint speaks IMAP
// under the requested security without authenticating.
func Verify(ctx context.Context, cfg api.ServerConfig) error {
	ctx, cancel := context.WithTimeout(ctx, transport.EndpointTimeout)
	defer cancel()
	c, _, err := Connect(ctx, cfg)
	if err != nil {
		return err
	}
	return c.Close()
}

// classify handles IMAP status responses before the generic rules.
func classify(ctx context.Context, stage transport.Stage, err error) error {
	var ie *imap.Error
	if errors.As(err, &ie) {
		text := transport.CleanMessage(ie.Text)
		switch {
		case stage == transport.StageTLS:
			return api.NewError(api.CodeTLSError, "STARTTLS refused: %s", text)
		case stage == transport.StageAuth && ie.Type == imap.StatusResponseTypeNo:
			return api.NewError(api.CodeAuthFailed, "authentication rejected: %s", text)
		default:
			return api.NewError(api.CodeServerError, "%s: server said %s %s", stage, ie.Type, text)
		}
	}
	return transport.Classify(ctx, stage, err)
}
