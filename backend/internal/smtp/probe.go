// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// ProbeResult is what a successful Probe learned.
type ProbeResult struct {
	Capabilities []string      // scrubbed EHLO keywords the backend knows about
	Latency      time.Duration // dial start → EHLO answered (over TLS for STARTTLS)
}

// knownExtensions is the list probed with Client.Extension; go-smtp does
// not expose the raw EHLO response.
var knownExtensions = []string{
	"AUTH", "STARTTLS", "SIZE", "8BITMIME", "SMTPUTF8", "PIPELINING", "CHUNKING",
	"BINARYMIME", "DSN", "ENHANCEDSTATUSCODES", "REQUIRETLS", "DELIVERBY", "MT-PRIORITY", "RRVS", "HELP",
}

// Conn is an open client whose connection is closed when the context that
// opened it ends.
type Conn struct {
	*smtp.Client
	stop func() bool
}

// Close releases the connection and the watchdog.
func (c *Conn) Close() error {
	c.stop()
	return c.Client.Close()
}

// Connect dials, secures the connection according to cfg.Security and
// completes EHLO. It never authenticates and never logs traffic.
func Connect(ctx context.Context, cfg api.ServerConfig) (*Conn, time.Duration, error) {
	start := time.Now()
	raw, err := transport.DialContext(ctx, cfg.Host, cfg.Port, cfg.Security)
	if err != nil {
		return nil, 0, err
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })

	var c *smtp.Client
	if cfg.Security == api.SecuritySTARTTLS {
		c, err = smtp.NewClientStartTLS(raw, transport.TLSConfig(cfg.Host))
		if err != nil {
			stop()
			return nil, 0, classify(ctx, transport.StageTLS, err)
		}
	} else {
		c = smtp.NewClient(raw)
	}
	c.CommandTimeout = transport.CommandTimeout
	// After STARTTLS the library forgets the plaintext EHLO, so this sends
	// a fresh one over TLS.
	if err := c.Hello("localhost"); err != nil {
		c.Close()
		stop()
		return nil, 0, classify(ctx, transport.StageGreeting, err)
	}
	return &Conn{Client: c, stop: stop}, time.Since(start), nil
}

// Probe connects, authenticates with the password and quits. Only password
// authentication is supported so far.
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

	var caps []string
	for _, ext := range knownExtensions {
		if ok, param := c.Extension(ext); ok {
			if param != "" {
				ext += " " + param
			}
			caps = append(caps, ext)
		}
	}

	switch {
	case c.SupportsAuth(sasl.Plain):
		err = c.Auth(sasl.NewPlainClient("", cfg.Username, password))
	case c.SupportsAuth(sasl.Login):
		err = c.Auth(sasl.NewLoginClient(cfg.Username, password))
	default:
		return ProbeResult{}, api.NewError(api.CodeServerError, "server offers no usable authentication mechanism")
	}
	if err != nil {
		return ProbeResult{}, classify(ctx, transport.StageAuth, err)
	}

	res := ProbeResult{Capabilities: transport.CleanCapabilities(caps), Latency: latency}
	done := make(chan struct{})
	go func() { _ = c.Quit(); close(done) }()
	select {
	case <-done:
	case <-time.After(transport.QuitTimeout):
	}
	return res, nil
}

// Verify is Connect plus Close: it proves that the endpoint speaks SMTP
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

// classify handles SMTP reply codes before the generic rules.
func classify(ctx context.Context, stage transport.Stage, err error) error {
	if stage == transport.StageTLS && strings.Contains(err.Error(), "doesn't support STARTTLS") {
		return api.NewError(api.CodeTLSError, "server does not offer STARTTLS")
	}
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		text := transport.CleanMessage(se.Message)
		switch {
		case stage == transport.StageAuth && (se.Code == 535 || se.Code == 534 ||
			se.EnhancedCode == smtp.EnhancedCode{5, 7, 8} || se.EnhancedCode == smtp.EnhancedCode{5, 7, 9}):
			return api.NewError(api.CodeAuthFailed, "authentication rejected: %d %s", se.Code, text)
		case stage == transport.StageAuth && (se.Code == 530 || se.Code == 538):
			return api.NewError(api.CodeTLSError, "server requires TLS before authentication: %d %s", se.Code, text)
		default:
			return api.NewError(api.CodeServerError, "%s: server said %d %s", stage, se.Code, text)
		}
	}
	return transport.Classify(ctx, stage, err)
}
