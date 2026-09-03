// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// ProbeResult is what a successful Probe learned.
type ProbeResult struct {
	Capabilities []string      // scrubbed post-login CAPABILITY
	Latency      time.Duration // dial start → ready (greeting read, STARTTLS done)
}

// Conn is an open, not yet authenticated client whose connection is closed
// when the context that opened it ends.
type Conn struct {
	*imapclient.Client
	stop func() bool
}

// Close releases the connection and the watchdog.
func (c *Conn) Close() error {
	c.stop()
	return c.Client.Close()
}

// Connect dials, secures the connection according to cfg.Security and reads
// the greeting. It never authenticates and never logs traffic. Errors are
// *api.Error. The caller must Close the result while ctx is still alive.
func Connect(ctx context.Context, cfg api.ServerConfig) (*Conn, time.Duration, error) {
	start := time.Now()
	raw, err := transport.DialContext(ctx, cfg.Host, cfg.Port, cfg.Security)
	if err != nil {
		return nil, 0, err
	}
	// The library has no context support and a 30 s internal read timeout;
	// closing the socket when ctx ends is what enforces our budget.
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	opts := &imapclient.Options{TLSConfig: transport.TLSConfig(cfg.Host)}

	var c *imapclient.Client
	if cfg.Security == api.SecuritySTARTTLS {
		c, err = imapclient.NewStartTLS(raw, opts)
		if err != nil {
			stop()
			return nil, 0, classify(ctx, transport.StageTLS, err)
		}
	} else {
		c = imapclient.New(raw, opts)
		if err := c.WaitGreeting(); err != nil {
			c.Close()
			stop()
			return nil, 0, classify(ctx, transport.StageGreeting, err)
		}
	}
	return &Conn{Client: c, stop: stop}, time.Since(start), nil
}

// Probe connects, authenticates with the password, reads CAPABILITY and
// logs out. Only password authentication is supported so far.
func Probe(ctx context.Context, cfg api.ServerConfig, password string) (ProbeResult, error) {
	if cfg.AuthMethod != api.AuthPassword {
		return ProbeResult{}, api.ErrNotImplemented
	}
	ctx, cancel := context.WithTimeout(ctx, transport.EndpointTimeout)
	defer cancel()

	c, latency, err := Connect(ctx, cfg)
	if err != nil {
		return ProbeResult{}, err
	}
	defer c.Close()

	caps := c.Caps()
	switch {
	case caps.Has(imap.AuthCap(sasl.Plain)):
		err = c.Authenticate(sasl.NewPlainClient("", cfg.Username, password))
	case !caps.Has(imap.CapLoginDisabled):
		err = c.Login(cfg.Username, password).Wait()
	default:
		return ProbeResult{}, api.NewError(api.CodeServerError, "server offers no usable authentication mechanism")
	}
	if err != nil {
		return ProbeResult{}, classify(ctx, transport.StageAuth, err)
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
