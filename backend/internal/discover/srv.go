// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"context"
	"strings"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

type srvResult struct {
	imap, smtp *api.ServerConfig
}

// lookupSRV follows RFC 6186 / RFC 8314: implicit-TLS services first, then
// the STARTTLS ones. A target of "." means the service is not offered.
func (d *Discoverer) lookupSRV(ctx context.Context, domain string) srvResult {
	var res srvResult
	if res.imap = d.srv(ctx, "imaps", domain, api.SecurityTLS); res.imap == nil {
		res.imap = d.srv(ctx, "imap", domain, api.SecuritySTARTTLS)
	}
	if res.smtp = d.srv(ctx, "submissions", domain, api.SecurityTLS); res.smtp == nil {
		res.smtp = d.srv(ctx, "submission", domain, api.SecuritySTARTTLS)
	}
	return res
}

func (d *Discoverer) srv(ctx context.Context, service, domain string, sec api.Security) *api.ServerConfig {
	ctx, cancel := context.WithTimeout(ctx, SRVTimeout)
	defer cancel()
	_, addrs, err := d.Resolver.LookupSRV(ctx, service, "tcp", domain)
	if err != nil {
		d.Log.Debug("srv lookup", "service", service, "domain", domain, "err", err)
		return nil
	}
	for _, a := range addrs {
		host := strings.ToLower(strings.TrimSuffix(a.Target, "."))
		if host == "" || !transport.ValidHost(host) || a.Port == 0 {
			continue
		}
		return &api.ServerConfig{Host: host, Port: int(a.Port), Security: sec, AuthMethod: api.AuthPassword}
	}
	return nil
}
