// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Command malachi-devmail is a developer tool: a mail server in memory
// with a sample mailbox, so that the desktop clients can be run, tried
// and photographed over the same data on every platform without an
// account anywhere. It is deliberately not installed: neither the
// Makefile nor scripts/build.sh builds or packages it. Build it by hand
// with "go build ./cmd/malachi-devmail" in backend/.
//
//	malachi-devmail serve [-imap-port N] [-smtp-port N] [-control-port N]
//	                      [-lang en|cs] [-corpus DIR] [-now RFC3339]
//	malachi-devmail add-account -socket PATH -imap-port N -smtp-port N [-lang en|cs]
//	malachi-devmail get|set|delete        (the daemon's keyring helper)
//
// serve listens on 127.0.0.1 (a port of 0, the default, picks a free one
// and the chosen ports are printed as one JSON line on stdout): plaintext
// IMAP with IDLE, plaintext SMTP submission with AUTH PLAIN and LOGIN, and
// an HTTP control endpoint whose GET /status says how many messages each
// folder of each user holds. The mailbox is the built-in sample set of
// -lang (samples/en.json, samples/cs.json: one story in two languages,
// the same people, dates relative to -now) under the user it names, or,
// with -corpus, every .eml file of that directory in the INBOX of
// test@example.test (what the Windows UI tests' devmail seeds). Mail
// submitted over SMTP to a user of the server lands in their INBOX; the
// rest is accepted and dropped. The password of every user is "test".
//
// get, set and delete make the binary the daemon's keyring helper
// (MALACHI_KEYRING=helper, MALACHI_KEYRING_HELPER=<this binary>;
// internal/auth/helper): the secrets go to the JSON file named by
// MALACHI_DEVMAIL_KEYRING, in the clear, which is fine for a throwaway
// data directory and for nothing else.
//
// add-account performs the handshake of docs/api.md §1.4 with a running
// daemon and adds the sample user's IMAP/SMTP account pointing at serve's
// ports, with the password; it prints the account id.
package main

import (
	"fmt"
	"os"
)

const usageText = `usage: malachi-devmail serve [-imap-port N] [-smtp-port N] [-control-port N] [-lang en|cs] [-corpus DIR] [-now RFC3339]
       malachi-devmail add-account -socket PATH -imap-port N -smtp-port N [-lang en|cs] [-timeout 45s]
       malachi-devmail get|set|delete   (keyring helper; the file is $MALACHI_DEVMAIL_KEYRING)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "serve":
		code = serve(os.Args[2:], os.Stdout, os.Stderr)
	case "add-account":
		code = addAccount(os.Args[2:], os.Stdout, os.Stderr)
	case "get", "set", "delete":
		code = keyring(os.Args[1], os.Stdin, os.Stdout, os.Stderr)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText)
	default:
		fmt.Fprintf(os.Stderr, "malachi-devmail: unknown command %q\n\n%s", os.Args[1], usageText)
		code = 2
	}
	os.Exit(code)
}
