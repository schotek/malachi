// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package account

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestServerCertificatePinFromTOML(t *testing.T) {
	const doc = `
[[accounts]]
name = "Bridge"
email = "me@example.org"

[accounts.imap]
host = "100.64.0.7"
port = 1143
security = "starttls"
username = "me@example.org"
auth_method = "password"
certificate_sha256 = "AB:CD:EF"

[accounts.smtp]
host = "100.64.0.7"
port = 1025
security = "starttls"
username = "me@example.org"
auth_method = "password"
`
	var cfg struct{ Accounts []Config }
	meta, err := toml.Decode(doc, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if u := meta.Undecoded(); len(u) > 0 {
		t.Fatalf("undecoded keys %v", u)
	}
	// Taken as written; account validation normalises and checks it.
	got := cfg.Accounts[0].ToAPI()
	if got.IMAP.CertificateSHA256 != "AB:CD:EF" || got.SMTP.CertificateSHA256 != "" {
		t.Fatalf("imap %+v smtp %+v", got.IMAP, got.SMTP)
	}
}
