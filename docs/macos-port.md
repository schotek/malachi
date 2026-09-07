<!--
SPDX-FileCopyrightText: 2026 Vladislav Janeček
SPDX-License-Identifier: GPL-3.0-or-later
-->

# Exploration: a native macOS client

**Status: exploration only. Nothing here is planned or approved.**
[CLAUDE.md](../CLAUDE.md) rule 4 ("Linux only") stands unchanged until it is
explicitly revised. This document records what a macOS port would actually
cost, measured against the tree as it stands on 2026-09-07, so that the
question does not have to be re-researched from scratch later.

The question asked: can there be a macOS variant of the finished GTK client
using native macOS UI, and what would it involve?

Short answer: **yes, and the architecture is built for it — but it is three
projects of very different difficulty, and the hardest one is not code.**

## 1. Measured starting point

The backend was compiled and its test suite run on macOS 26.6.2,
darwin/arm64, Go 1.26.2, with no source changes and no build tags:

```
go build ./...      → OK
go test ./...       → 24 of 25 packages pass
```

The single failure:

```
--- FAIL: TestAttachmentImportMetadata
    attachments_test.go:108: "a\x00b\n.md": got text/plain, want text/markdown
```

[`internal/core/attachments.go:143`](../backend/internal/core/attachments.go#L143)
calls `mime.TypeByExtension`, which consults the host MIME database. macOS
maps `.md` differently than a Fedora host does.

This is worth fixing regardless of any port: the same test fails on any
Linux host whose MIME database lacks `.md`, including a minimal Flatpak
runtime without `shared-mime-info`. Attachment content types should come
from a table we control, not from the host.

That the backend is otherwise clean is not luck. It follows from the socket
boundary and from the platform-specific pieces already sitting behind
interfaces. `backend/go.mod` pulls in no GUI toolkit; the only
Linux-implying dependency is `godbus/dbus`, and it compiles on darwin.

## 2. Layer one — backend adaptation (small, well bounded)

Linux is reachable from exactly three places, each already behind an
interface, so each is an added implementation and not a fork:

| Concern | Interface | macOS replacement | Size |
|---|---|---|---|
| Secret storage | `auth.Keyring`, 3 methods ([auth.go:30](../backend/internal/auth/auth.go#L30)), selected by `MALACHI_KEYRING` in [main.go:86](../backend/cmd/malachid/main.go#L86) | Keychain via Security.framework | ~200 lines |
| Address books | `contacts.Directory`, 2 methods ([contacts.go:38](../backend/internal/contacts/contacts.go#L38)) | Contacts.framework, or return empty (it already degrades silently) | small |
| XDG paths | [config.go:88-95](../backend/internal/config/config.go#L88-L95) | `~/Library/Application Support`, `~/Library/Caches` | trivial |
| GNOME Online Accounts | `core.GOAClient` ([backend.go:101](../backend/internal/core/backend.go#L101)) | **none exists** | see §3 |

The keyring is the model to follow for the rest: a narrow interface, a
runtime switch, and a refusing implementation (`auth.UnavailableKeyring`)
for when the platform cannot serve it. Nothing about that shape is
Linux-specific, and generalising the token source the same way would be an
improvement to the backend on its own terms.

The RPC socket needs no work. `XDG_RUNTIME_DIR` is absent on macOS, and the
existing fallback to the cache directory already covers that case.

Estimated: **2-4 weeks**, excluding §3.

## 3. Layer two — GNOME Online Accounts is the real blocker

GOA currently does three jobs at once: it holds OAuth tokens for Microsoft
Graph, supplies XOAUTH2 access tokens for Gmail IMAP/SMTP, and enumerates
accounts for the wizard (`account.linked`). macOS has no equivalent, and no
part of that is replaceable by a local API.

An own OAuth2 flow is deliberately unimplemented today —
[`goa_accounts.go:154`](../backend/internal/core/goa_accounts.go#L154)
returns `notImplemented` for any `OAuth2Config` without
`source: goa` — and CLAUDE.md states the reason: going through GOA means
using GNOME's registered client ID, "proto žádný CASA audit".

Registering our own client IDs reverses that:

- **Google.** IMAP/SMTP access is a restricted scope. Restricted scopes
  require an annual third-party security assessment (CASA) before the app
  can be published to users outside a test list. Real money, and months of
  process, renewed yearly.
- **Microsoft.** Lighter, but still an app registration, publisher
  verification, and consent review to reach organisational tenants.

**This is the most expensive part of the whole idea and none of it is
programming.** The alternative is a macOS client that only speaks plain
IMAP/SMTP with a password — which excludes Gmail and Microsoft 365, and
therefore most prospective users. That trade-off must be decided before any
UI work starts, because it determines whether the result is worth shipping.

Note that the constraint is not macOS-specific in principle. It applies to
any Linux desktop without GOA too, which is why `OAuth2Config` without a
`source` exists as a reserved shape rather than a missing feature.

## 4. Layer three — the UI is a rewrite, not a port

Present size of the GTK UI:

- **14 578 lines of Go** across 81 files (3 059 of them tests)
- **2 449 lines of Blueprint** across 9 `.blp` files

Effectively none of it transfers. AppKit and GTK share no widget model, no
layout model, and no lifecycle. What has to be built again:

| Package | Non-test lines | What it is |
|---|---|---|
| `window` | 5 937 | folder sidebar, message list, reader, actions, attachments |
| `compose` | 1 773 | address entry, drafts, `mailto:`, recipient completion |
| `accountwizard` | 1 037 | account setup and autodetection |
| `widget`, `style`, `settingspanel` | 669 | shared widgets and styling |

What maps across better than expected, because both sides are WebKit:

- [`ui/internal/htmlview`](../ui/internal/htmlview/) → **WKWebView**. JavaScript
  off, a strict content policy and a severed network are all expressible;
  the `malachi-cid:` scheme becomes a `WKURLSchemeHandler`. The security
  properties in [security.md §3.2](security.md) must be re-established and
  re-reviewed on the new renderer — they are not inherited.
- [`ui/internal/editor`](../ui/internal/editor/) → the same `contenteditable`
  approach in a `WKWebView`. The cgo shim in `evaluate.go` becomes
  unnecessary; WKWebView has async evaluation natively.

Platform services map one-to-one:

| GTK / freedesktop | macOS |
|---|---|
| GSettings | `NSUserDefaults` |
| gettext (`i18n.T`, `_()`) | `.strings` / String Catalog |
| gsound | `NSSound` |
| XDG Background portal ([background.go](../ui/internal/background/background.go)) | `SMAppService` |
| `malachid` started by the UI | LaunchAgent |

### Language choice

**Swift is the recommendation.** Go with AppKit bindings reproduces exactly
the generated-binding problems the project already carries with gotk4
(CLAUDE.md: "v některých částech API se vyskytují memory leaky a pády"),
without gotk4's community, and still ends in hand-written Objective-C.

The cost of Swift is that `pkg/api` types must be re-declared. That cost is
bounded and known: 43 method and notification names, documented in
[api.md](api.md) (1 257 lines), with `TestDocsCoverAllMethods` guaranteeing
the document stays complete. Newline-delimited JSON-RPC over a unix socket
is straightforward from Swift. Nothing security-relevant — sanitisation,
MIME parsing, threading, credential handling — is duplicated. That is the
entire point of the boundary, and this is the case it was designed for.

## 5. Distribution

New and non-trivial, separate from writing the client:

- Xcode build, separate from `make` and `scripts/build.sh`
- Apple Developer Program membership, code signing, notarisation
- App Sandbox entitlements for outgoing network and Keychain access
- `malachid` shipped inside the bundle and managed as a LaunchAgent
- an update mechanism (Sparkle, or the App Store, which reopens the licence
  question below)

## 6. Repository and licence

Two decisions to take before any code, not after.

**Where it lives.** Rule 4 forbids macOS code, build paths and abstractions
in this repository. The clean resolution is that **the macOS UI lives in a
separate repository** and speaks only the documented API. The core repo then
needs no `//go:build darwin` anywhere — only platform-neutral extension
points (a keyring provider, a token provider), which are an improvement in
their own right. This is precisely the scenario that "Why two processes and
not one binary with a clean package boundary?" in
[architecture.md](architecture.md) uses to justify the socket — reason 2,
"replaceable UI" — and it keeps rule 4 intact rather than bending it.

**Licence.** `backend/` is AGPL-3.0-only. A separate macOS client talking to
`malachid` over a socket is the boundary case
[LICENSING.md](../LICENSING.md) already anticipates with the commercial core
licence. If the macOS UI is to be proprietary, or App Store distributed,
settle this first — not after a year of work.

## 7. Estimates

| Phase | Effort |
|---|---|
| Backend on macOS (keyring, paths, contacts, MIME fix) | 2-4 weeks |
| Own OAuth2 flow | 2-3 weeks of code, **months** of audit and process |
| macOS UI to parity with the GTK client | 4-8 months, one person |

## 8. Open questions

1. Is a macOS client without Gmail and Microsoft 365 worth shipping? If not,
   §3 is a prerequisite, not a later phase.
2. Who pays for and owns the CASA assessment, and does that change the
   answer for Linux desktops without GOA as well?
3. Separate repository (recommended) or a rule 4 revision?
4. Proprietary or GPL macOS UI, and does App Store distribution matter?
5. Should the token source be generalised behind an interface now, the way
   `auth.Keyring` already is, independently of any port?

Question 5 is the only one that is worth acting on regardless of whether the
port ever happens.
