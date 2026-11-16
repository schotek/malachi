// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package embed

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
)

// EnvDNSServers names the DNS servers the client hands the daemon
// ("192.0.2.53,[2001:db8::53]:53"; a missing port is 53). Go's own DNS
// client, which every MX, TXT and SRV lookup uses even in a cgo build,
// reads its servers from /etc/resolv.conf; where that file is absent
// (Android) it asks 127.0.0.1 and fails, so discovery never sees that a
// domain's mail is hosted by Microsoft 365. Host names still resolve
// through the system (cgo). Unset: nothing changes.
const EnvDNSServers = "MALACHI_DNS_SERVERS"

// dnsDialTimeout bounds one attempt at one server.
const dnsDialTimeout = 5 * time.Second

// parseDNSServers returns the servers of EnvDNSServers as host:port,
// skipping entries that are not IP addresses.
func parseDNSServers(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			host, port = strings.Trim(s, "[]"), "53"
		}
		if net.ParseIP(host) == nil || port == "" {
			continue
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	return out
}

// useDNSServers makes Go's DNS client ask the client's servers in turn,
// whatever /etc/resolv.conf says (it is still read for the options).
func useDNSServers(log *slog.Logger) {
	servers := parseDNSServers(os.Getenv(EnvDNSServers))
	if len(servers) == 0 {
		return
	}
	var d net.Dialer
	net.DefaultResolver.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var errs []error
		for _, s := range servers {
			c, cancel := context.WithTimeout(ctx, dnsDialTimeout)
			conn, err := d.DialContext(c, network, s)
			cancel()
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
	log.Info("dns servers from the client", "count", len(servers))
}
