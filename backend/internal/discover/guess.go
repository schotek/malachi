// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"context"
	"sync"

	"github.com/schotek/malachi/backend/pkg/api"
)

type candidate struct {
	host string
	port int
	sec  api.Security
}

func imapCandidates(domain string) []candidate {
	return []candidate{
		{"imap." + domain, 993, api.SecurityTLS},
		{"mail." + domain, 993, api.SecurityTLS},
		{"imap." + domain, 143, api.SecuritySTARTTLS},
		{"mail." + domain, 143, api.SecuritySTARTTLS},
	}
}

func smtpCandidates(domain string) []candidate {
	return []candidate{
		{"smtp." + domain, 587, api.SecuritySTARTTLS},
		{"mail." + domain, 587, api.SecuritySTARTTLS},
		{"smtp." + domain, 465, api.SecurityTLS},
		{"mail." + domain, 465, api.SecurityTLS},
	}
}

// guess tries the common host names in parallel and keeps the first
// candidate, in table order, that answered under the requested security.
func (d *Discoverer) guess(ctx context.Context, domain string, wantIMAP, wantSMTP bool) srvResult {
	ctx, cancel := context.WithTimeout(ctx, GuessTimeout)
	defer cancel()
	var res srvResult
	var wg sync.WaitGroup
	if wantIMAP {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res.imap = verifyFirst(ctx, imapCandidates(domain), d.VerifyIMAP)
		}()
	}
	if wantSMTP {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res.smtp = verifyFirst(ctx, smtpCandidates(domain), d.VerifySMTP)
		}()
	}
	wg.Wait()
	return res
}

func verifyFirst(ctx context.Context, cands []candidate, verify func(context.Context, api.ServerConfig) error) *api.ServerConfig {
	ok := make([]bool, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c candidate) {
			defer wg.Done()
			cfg := api.ServerConfig{Host: c.host, Port: c.port, Security: c.sec, AuthMethod: api.AuthPassword}
			ok[i] = verify(ctx, cfg) == nil
		}(i, c)
	}
	wg.Wait()
	for i, c := range cands {
		if ok[i] {
			return &api.ServerConfig{Host: c.host, Port: c.port, Security: c.sec, AuthMethod: api.AuthPassword}
		}
	}
	return nil
}
