<!--
SPDX-FileCopyrightText: 2026 Vladislav Janeček
SPDX-License-Identifier: GPL-3.0-or-later
-->

# The macOS client

How the Swift/AppKit client in `macos/` is built, for people (and agents)
who change it. What it does and how to build it is in
[macos/README.md](../macos/README.md); why there is a native client per
platform at all is in [architecture.md §6](architecture.md#6-platform).
§12 keeps what the port was measured to take and what is still open.

The one sentence that governs everything below: **the GTK UI is the
template, and the daemon is the only place logic lives.** The macOS client
is a mirror of `ui/`, not a second design, and it holds no mail knowledge
the GTK client does not hold either.

## 1. Process model

```
┌──────────────────────────────┐                     ┌────────────────────┐
│  Malachi Mail.app            │                     │  malachid (Go)     │
│  Contents/MacOS/MalachiMail  │ ◄── JSON-RPC 2.0 ─► │  all the logic     │
│                              │     unix socket     │                    │
│  starts ──► Contents/MacOS/malachid ───────────────┤  --config --store  │
│             --socket --config --store              │  MALACHI_KEYRING=  │
│             MALACHI_KEYRING=helper                 │    helper          │
│             MALACHI_KEYRING_HELPER=…/malachi-keychain                   │
│                                                    └────────┬───────────┘
│  Contents/MacOS/malachi-keychain ◄── one process per get/set/delete ────┘
│      (login keychain, Security.framework)
│  Contents/MacOS/malachi-mcp  ◄── spawned by an MCP client, same socket
└──────────────────────────────┘
```

The app does what the GTK UI does (`ui/internal/daemon`,
[architecture.md §2](architecture.md#2-transport)): it looks for
`malachid` (`MALACHI_DAEMON`, then beside its executable, then `PATH`),
probes the socket, starts the daemon when nothing answers, waits for the
socket, restarts it after an exit with a backoff, and stops the daemon it
started when the application quits. The daemon gets macOS paths for the
configuration and the store (`~/Library/Application Support/Malachi
Mail/`) as flags and keeps its own default socket path, so `malachi-mcp`
and `.mcp.json` work unchanged (`MalachiCore/Daemon/Paths.swift`,
`DaemonSupervisor.swift`).

The daemon's keyring on macOS is the helper keyring
(`backend/internal/auth/helper`, [security.md §6](security.md#6-credentials)):
`MALACHI_KEYRING=helper` and `MALACHI_KEYRING_HELPER` pointing at the
bundled `malachi-keychain`, set by `DaemonSupervisor.environment` unless
`MALACHI_KEYRING` is already in the environment. The helper is a separate
executable target (`MalachiKeychain`) with no dependency on the rest of
the package: `Request.swift` is the protocol (parsed and validated without
touching the Keychain, so it is testable without prompts),
`Keychain.swift` the Security-framework half, `main.swift` the glue. It
prints only the protocol: the value on stdout as the answer to `get`, a
diagnostic on stderr, never data.

## 2. Package and module boundaries

`macos/Package.swift` (tools 6.0, strict concurrency, macOS 14, no SwiftPM
resources) has three targets and their tests:

| Target | May import | Holds |
|---|---|---|
| `MalachiCore` | Foundation, Network, UniformTypeIdentifiers, os | The typed API, the transport, the daemon supervisor, the pure logic ported from the Go UI (models, threads, folding, favourites, address parsing, quoting, wizard fields, HTML documents, formatting, error texts), the `@MainActor` controllers, settings, i18n, the open directory. **No AppKit, no WebKit.** Everything here is covered by `swift test`. |
| `MalachiMail` | AppKit, WebKit, UserNotifications, ServiceManagement, `MalachiCore` | Windows, views, the menu bar, the toolbar, the WebKit wrappers, the platform services. Thin: it renders what a controller holds and sends clicks back. |
| `MalachiKeychain` | Foundation, Security | `malachi-keychain`, the keyring helper. Independent of the other two. |

The dependency direction is `MalachiMail → MalachiCore`, never back, and
nothing imports the Go modules: the API types are re-declared in
`MalachiCore/API/` from `docs/api.md`. That is the same rule as for the GTK
UI ("UI smí z `backend/` importovat pouze `pkg/api`"), one step further
out: the contract is the socket and the document, not a Go package.

Within `MalachiCore`, `Controllers/` is the layer that talks to the
daemon: `ConnectionController` (the reconnect loop and the protocol
check), `MailboxController` (accounts, folders, the list and its threads),
`SyncController` (the status line from `sync.status`, `notify.syncState`
and the connection, the rows of its popover, the sign-in and certificate
banners), `MessageCache` (`message.get`/`message.body`/
`message.part` with a bounded cache), `ActionsController` (flags, moves,
trash, archive, junk, outbox retry, remote images, trusted senders,
reply/forward through `draft.create`), `ComposeController` and
`ComposeDraftController` (recipients, autosave, send, discard),
`WizardController` (discover → the browser sign-in or a password →
test → add/update; a refused server certificate offers trust through
`CertTrust`, the port of `ui/internal/certtrust`, and the pin rides along
in the endpoint fields until the host or port changes),
`MailPreferencesController` (`config.get`/`config.set`),
`MCPRegistrationController` (runs the bundled `malachi-mcp status` /
`install` / `uninstall --json` through `BridgeRunner` for Settings → AI).
Each is a `@MainActor` class over an injected `RPCClient` (or a process
runner) and a `toast` sink, tested against a scripted daemon or a fake
bridge script (§9), with no view in sight.

`MalachiMail/App/Contracts.swift` and `Integration.swift` are the seams:
the protocols the shell offers (`Toasts`, `Alerts` with its
confirmations, `confirmTrustCertificate` among them, `MessageActions`,
`EditorView`) and the one place where the sidebar, list, reader, actions,
compose, wizard and notifications are wired to the main window and the
app state.

## 3. The parity principle

The reference for every screen is its Blueprint in `ui/data/ui/*.blp` and
the Go file behind it (`ui/internal/window`, `compose`, `accountwizard`,
`widget`, `editor`, `htmlview`). "Mirror" means:

- every element of the Blueprint has a counterpart in the same order and
  the same place; sizes and margins from the Blueprints and
  `ui/internal/style` become Auto Layout constants; the source files name
  the Blueprint and the Go function they port in their header comment;
- every behaviour of the Go UI (actions, confirmations, optimistic changes
  with revert, timeouts, error texts, generation counters, `closed`/`op`
  guards, the mark-as-read delay, the autosave delay) is ported 1:1, with
  the same names where the Go names are meaningful, so a reader can diff
  the two;
- every user-visible string goes through `L10n.T/N/C` with the **GTK
  msgid as the key**, so `po/` translates both clients at once (§8); a
  string with no GTK counterpart is marked `// macOS-only string` and
  stays English;
- the pure-logic Go tests (`*_test.go` of `window`, `widget`, `compose`,
  `accountwizard`, `editor`, `htmlview`) are ported 1:1 to Swift Testing
  suites of the same shape (§9);
- mail data is hostile input here as there: only `stringValue` /
  `NSTextView.string`, never `NSAttributedString(html:)` or RTF over
  anything from a message; HTML only in the WebKit views of §5.

Where macOS conventions win, the deviation is deliberate, small and
listed in the table in [macos/README.md](../macos/README.md#differences-from-the-gtk-ui)
(the unified toolbar with the folder's counts as the window subtitle, pane
folding without back navigation, the status bar across the bottom of the
window, Settings without search, ⌥⌘↑/↓ for reordering, the ⌘R setting,
`NSAlert` button order, the quarantine attribute on attachments, the
*Glass* sound). A new deviation goes into that table, not silently into
the code.

## 4. The API layer

`MalachiCore/API/` re-declares `backend/pkg/api` (`API.swift` is the
method table of `methods.go`; the other files follow `types.go` by area).
Rules that keep it honest against a daemon it did not ship with:

- every method is a type conforming to `RPCMethod` (`Params`, `Result`,
  `name`, a default `timeout`), and `RPCClient.call(API.MessageList.self,
  params)` is the only way to call one, so a typo in a method name cannot
  compile; stubs the daemon answers with `notImplemented` (`search.query`)
  are declared too, so the table is the whole contract;
- property names are the JSON names verbatim (no `CodingKeys`); Go
  `omitempty` is `Optional`; a Go nil slice arrives as `null` and is
  wrapped `@NullAsEmpty` so a missing or null array is `[]`, never a
  decoding error;
- wire enums (`FolderRole`, `Flag`, `SyncStatus`, …) are extensible
  structs, so a value from a newer daemon decodes instead of failing the
  whole result; `ErrorCode` is `RawRepresentable<Int>` with the documented
  constants and a `name`, and `RPCError.data` survives (for
  `attachmentTooBig`'s `limit`/`size`);
- `API.protocolVersion` is checked against `system.info` on every
  connection (`ConnectionController`); a mismatch is a state the window
  shows, not something to work around;
- timeouts are the GTK UI's (`Platform/RPCTimeouts.swift`): 5 s by
  default, 3 s for `system.info`, 60 s for `message.part` and
  `attachment.get`, 30 s for `message.body` under `allow`,
  `message.embedded`, `draft.create`, `account.add`/`update`, 15 s for
  `account.discover`, 45 s for `account.test`, 10 s for
  `account.oauthStart` and 75 s for each `account.oauthWait` (the daemon
  answers `pending` after a minute and the wizard asks again).

`docs/api.md` and `backend/pkg/api` are not changed from here. A feature
that needs a new method is added to the daemon and the document first
(CLAUDE.md rule 5), then to the GTK UI, then here.

## 5. The WebKit security layer

HTML from a message is rendered by `WebViews/MessageWebView.swift`, HTML
being composed by `WebViews/ComposeWebView.swift`. Both are **layer 2** of
[security.md §3.2](security.md#32-defences) and §3.3, re-established for
WebKit on macOS, since none of WebKitGTK's settings carry over. The
backend's sanitiser (layer 1) is what makes the content safe; these views
are what keep a sanitiser bug from becoming a compromise, and they are
never a reason to relax layer 1. The header comment of each file walks
through §3.2 / §3.3 bullet by bullet with the property that implements it;
in short:

| Property | Viewer (`MessageWebView`) | Editor (`ComposeWebView`) |
|---|---|---|
| Content JavaScript | off (`allowsContentJavaScript = false`) | off; the bridge is a `WKUserScript`, which runs anyway and the CSP does not govern |
| Storage | `WKWebsiteDataStore.nonPersistent()` | same |
| CSP | `default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'` as a `<meta>` in the document (`HTML/ViewerDocument.swift`), identical to GTK; a WKWebView has no default policy, so the document is the only carrier | `default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:` (`HTML/EditorDocument.swift`) |
| Network | a proxy nothing answers on (`127.0.0.1:1`) for whatever might slip past the CSP, **plus a content rule list** that blocks every load except `malachi-cid:`, `data:` and `about:blank`; no body is loaded before the list is compiled and installed, and none at all when it cannot be compiled (`ContentRules`: two stores tried, a failure is never cached, the reader falls back to the plain text with the hint) | same, allowing `cid:` instead of `malachi-cid:`; the editor reports a failure instead of loading |
| Navigation | only the initial `about:blank` load of the main frame (the document is loaded with `baseURL: nil`); a link activation is cancelled and handed to `onLink` when `allowedLink` accepts it (the actions layer confirms a masked link); everything else cancelled; `createWebViewWith` returns nil; drops refused | only the initial load; every other navigation, including a clicked link in a quoted original, cancelled; file drops taken away from WebKit and handed to attachment import |
| Pictures | `PartSchemeHandler` for `malachi-cid:<account>/<message>/<part>`: stateless, `parsePartPath` then `message.part`, images only, never SVG | `CIDSchemeHandler` for `cid:<id>`: only ids the window registered in `CIDRegistry` (files it picked, the backend's copies through `attachment.get`), every picture through `checkInline` (never SVG, within the cap) |
| Context menu | Copy and Copy Link only | none |
| Hover | the link under the pointer in a status label, from a user script | — |

The content rule list is the one thing layer 2 has here that the GTK
viewer does not: a `<link rel="preconnect">` opens a TCP connection
without a request, which neither the CSP nor the proxy setting catches
(found by a network canary during the port). The identifiers
(`io.github.schotek.Malachi.viewer.1`, `.editor.1`) are bumped with the
rules, since the store keeps the compiled list by them.

The two schemes are different on purpose: a displayed message can never
address compose attachments, and a composed draft cannot name a received
message's parts. One view per pane is reused between messages (without
network nothing persists, and the document is replaced whole).

The [security review checklist](security.md#12-review-checklist-for-prs-touching-content-handling)
applies to changes in `WebViews/`, `MessageView/`, `Compose/`,
`Attachments/`, `MalachiKeychain` and `backend/internal/auth/helper`.

## 6. Concurrency

- `RPCClient` is an actor over `NWConnection`: it owns the connection,
  matches responses by id, and publishes `notifications` and `states` as
  `AsyncStream`s in the daemon's order (a `newMessage` never overtakes
  the `syncState` that follows it). Each stream has one consumer, the
  `ConnectionController`, which forwards on the main actor.
- Everything that touches a view or a model is `@MainActor`: the
  controllers in `MalachiCore/Controllers/`, every AppKit class, the
  WebKit delegates (main-actor isolated in the Swift overlay). An RPC is a
  `Task` started on the main actor, so the continuation after `await` is
  on the main actor too, and the generation counters of the Go UI
  (`listGen`, `bodyGen`, `foldersGen`) work without locks: a late answer
  compares its generation and is dropped.
- Optimistic changes are applied, their inverse kept, and reverted in the
  error path with a toast, as in `actions.go`. Windows keep `closed` flags
  and cancel their tasks when they close, so nothing renders into a
  window that is gone.
- The keyring helper, the attachment writes and the file reads that could
  block run off the main actor (`Task.detached`, `nonisolated` helpers);
  the daemon itself is where anything slow belongs.
- Swift 6 strict concurrency is on and the build is expected to be free
  of warnings; `@preconcurrency` imports need a reason in a comment.

## 7. Settings

`MalachiCore/Settings/Settings.swift` is `UserDefaults.standard` (domain
`io.github.schotek.Malachi`) behind typed properties. The keys and defaults
are those of `data/io.github.schotek.Malachi.gschema.xml`
(`launch-at-login`, `run-in-background`, `mark-read-delay`,
`confirm-delete`, `desktop-notifications`, `notification-sound`,
`color-scheme`, `message-list-density`, `show-preview-line`,
`group-by-conversation`, `show-avatars`, `monochrome-avatars`,
`monospace-plain-text`, `text-zoom`, `collapsed-folders`,
`collapsed-accounts`, `favourite-folders`) plus one macOS-only key,
`command-r` (`reply`, the default, or `refresh`; §3). Numeric keys are
clamped to the schema's ranges, bad enum strings fall back to the default,
and a change fires its handlers through KVO on `UserDefaults`, so a
`defaults write` from outside reaches the running app exactly as a second
GTK window sharing the profile would. `launch-at-login` only mirrors
`SMAppService`, which is authoritative. As in GTK, only presentation
options live here; anything that affects mail handling (check interval,
remote content, retention) is the daemon's, through `config.get`/`config.set`.

The window frames (`Main`, `Settings`) and the two pane widths
(`main-sidebar-width`, `main-list-width`) are AppKit state in the same
domain, not settings.

## 8. Localisation

`macos/scripts/po2strings.py` (stdlib Python, run by `make macos` and
`make test-macos`) turns `po/malachi.pot` into `en.lproj` and each
`po/<lang>.po` into `<lang>.lproj`, with `Localizable.strings` for
singular entries and `Localizable.stringsdict` for plurals, under
`build/macos/locale/`; the Makefile copies them into
`Contents/Resources`. Keys are msgids verbatim; a context entry is keyed
`<ctxt>\u{4}<msgid>` (gettext's convention); a plural entry is keyed by its
singular msgid; printf formats become Foundation's (`%s` → `%@`, `%d` →
`%ld`, positional forms likewise) unless the entry is `no-c-format` (the
strftime date patterns); fuzzy, obsolete and untranslated entries are left
out so the runtime falls back to the msgid. The plural categories per
language are a table in the script (`PLURAL_CATEGORIES`) that
`I18n/PluralRules.swift` mirrors; an unknown language is an error, not a
guess.

`I18n/Localization.swift` is the gettext shim: `L10n.T(msgid)`,
`T(msgid, args…)`, `N(singular, plural, n)`, `C(context, msgid)`. The
`Catalogue` is loaded once at start from the first root that has any
`.lproj`: `Bundle.main.resourceURL`, then `MALACHI_LOCALE_DIR`, else
English; the language is `Bundle.preferredLocalizations`, which honours
the per-app language in System Settings. The plural form is picked by
`PluralRules`, not by Foundation, so no resource bundle is needed and
`Bundle.module` is never used (a hand-assembled `.app` cannot carry it).
`I18n/Strftime.swift` maps the strftime msgids of `widget/format.go` to
`DateFormatter` patterns at run time, so a translator changes a date
format in one place for both clients.

## 9. Tests

`make test-macos` runs both test targets with the generated catalogues in
`MALACHI_LOCALE_DIR`, so the Czech cases run.

- `Tests/MalachiCoreTests/` (Swift Testing): the ports of the Go UI tests
  (`MailModelTests`, `ThreadModelTests`, `CollapseStateTests`,
  `FavouriteStateTests`, `FolderTreeTests`, `ActionHelpersTests`,
  `AttachmentsTests`, `AccountsPageTests`, `NotificationTextTests`,
  `OutboxTests`, `SyncStatusTests`, `ComposeSourceTests`,
  `AddressListTests`, `PrefillTests`, `MailtoTests`, `SuggestTests`,
  `BlockedSummaryTests`, `HTMLLinksTests`, `CIDRegistryTests`,
  `EditorBridgeTests`, `WizardFieldsTests`, `WizardResultsTests`,
  `SignInTests`, `FormatTests`, `RPCErrorTextTests`, `ProviderTests`), the
  transport
  (`FramingTests`, `JSONRPCTests`, `RPCClientTests`, `SupervisorTests`),
  the API coding (`APICodingTests`, `NotificationDecodeTests`), the
  settings and i18n (`SettingsTests`, `LocalizationTests`,
  `GettextFormatTests`, `PluralRulesTests`, `StrftimeTests`) and the
  controllers (`ConnectionControllerTests`, `MailboxControllerFoldersTests`,
  `MailboxControllerListTests`, `MessageCacheTests`, `SyncControllerTests`,
  `ActionsControllerTests`, `ComposeControllerTests`, `DraftStateTests`,
  `WizardControllerTests`, `MailPreferencesTests`, `MCPRegistrationTests`
  with a `#!/bin/sh` fake bridge).
- `Tests/MalachiCoreTests/Fixtures/`: `FakeDaemon` is an in-process
  `malachid` on a real unix socket speaking the same newline-delimited
  JSON-RPC, with per-method handlers; `MailFixture` scripts it with
  accounts, folders, messages, threads, bodies and parts, records what was
  asked, and fails, delays or pushes notifications on request. Controller
  tests run against it, never against a real daemon.
- `Tests/MalachiKeychainTests/`: the helper protocol (parsing, exit
  statuses, the identifier rule) without the Keychain; a real round trip
  through the login keychain only with `MALACHI_KEYCHAIN_TEST=1`, because
  it can prompt. On the Go side `backend/internal/auth/helper` tests the
  daemon's half against a fake helper (the test binary itself), and
  `MALACHI_TEST_REAL_HELPER=<path>` drives the built `malachi-keychain`.
- `python3 -m unittest macos/scripts/test_po2strings.py` covers the
  catalogue generator against the real `po/malachi.pot`.

There is no `MalachiMailTests` target: the AppKit classes are thin and the
logic they would test lives in `Controllers/`.

## 10. Adding a feature, keeping the parity

1. **Backend first.** If the daemon has to learn something, that lands in
   `backend/` with `docs/api.md` in the same commit (CLAUDE.md rule 5).
   Nothing in `macos/` may work around a missing method.
2. **GTK second.** The Blueprint and the Go code are the template: change
   `ui/` and its tests, and add every new string to `po/` (`make po`).
3. **Mirror third.** Port the Blueprint change to the AppKit view and the
   Go change to the controller or model port, with the same msgids, the
   same behaviour and the ported test. Name the Go file and function in
   the header comment, as the existing files do.
4. **If macOS has to differ**, add the row to the deviation table in
   `macos/README.md` and say why in the code.
5. **Before handing over**: `make macos` (release, no warnings),
   `make test-macos`, the Go tests of `internal/auth/helper` when the
   helper changed, an SPDX header on every new file (GPL-3.0-or-later in
   `macos/`, AGPL-3.0-only in `backend/`), no `print` of mail data, no
   `try!` or `!` on decoded data, no `Bundle.module`, no
   `NSAttributedString(html:)`. Anything that touches content handling
   goes through the [security checklist](security.md#12-review-checklist-for-prs-touching-content-handling).

For an agent, the reading list of a change is the `.blp` file and the Go
file it names, `docs/api.md` for every method it calls, and
[security.md](security.md) §3 for anything near HTML or attachments. The
`.blp` files are the reference: when the Swift view and the Blueprint
disagree and no deviation explains it, the Swift view is wrong.

## 11. Build

`make macos` at the root builds `build/malachid` and `build/malachi-mcp`
(the Go targets) and delegates to `macos/Makefile` with `BUILD_DIR`,
`VERSION` and `APP_ID`; `make run-macos` and `make test-macos` delegate
likewise. In `macos/Makefile`, `build` is `swift build -c release`,
`locale` runs the catalogue generator, and `app` assembles `build/Malachi
Mail.app`: `MalachiMail` and `malachi-keychain` from SwiftPM's bin path,
`malachid` and `malachi-mcp` from `build/`, the `.lproj` directories,
`Info.plist` rendered from `Resources/Info.plist.in` (version, bundle id,
`CFBundleLocalizations`, the `mailto:` URL type) and checked with
`plutil`, then `codesign` of each binary and of the bundle with `SIGN`
(ad hoc by default; the README says what that costs at the Keychain and
how a self-signed identity avoids it). No `.xcodeproj` is kept; Xcode
opens `Package.swift`.

## 12. What the port took, and what is still open

This document began as the exploration of whether a native macOS client
was feasible, measured against the tree of 2026-09-07. The conclusions
that still matter, updated to what was built:

**The backend needed one extension point, not a fork.** The daemon and
its tests already compiled on macOS with no build tags; the only
Linux-bound pieces sat behind interfaces. Of the three, the keyring
(`auth.Keyring`) got its platform-neutral implementation, the helper
keyring of §1; the XDG paths are handed to the daemon as flags by the
app; the address books (`contacts.Directory`, Evolution Data Server) are
absent and the daemon degrades silently, so recipient completion runs on
the collected addresses alone. No `//go:build darwin` exists anywhere,
which is what CLAUDE.md rule 4 asks for. Two Go tests still fail on macOS
and are unrelated to the client: `TestAttachmentImportMetadata`, because
`internal/core/attachments.go` asks the host MIME database about `.md`
and macOS answers `text/plain` (a content-type table of our own would fix
that on every platform), and the timing-sensitive
`TestWorkerAuthFailureDefersQueue`.

**GNOME Online Accounts is no longer the limit; distribution is.** At
first Gmail and Microsoft 365 could not be added here: their tokens came
only from GNOME Online Accounts (Graph tokens, Gmail's XOAUTH2 tokens,
the accounts `account.linked` lists), and macOS has no equivalent. Since
2026-09-25 the daemon runs the sign-in itself (source `daemon`,
[api.md §4.1](api.md#41-account), [security.md §6](security.md#6-credentials)):
authorization code with PKCE, a one-shot listener on `127.0.0.1`, the
refresh token in the keyring, which on macOS is the helper keyring of §1
(key `oauth2.refresh_token`). Without GNOME Online Accounts,
`account.discover` answers a Google or Microsoft 365 address with that
sign-in as the primary config, and the assistant takes the same way as
the GTK one on a desktop without GNOME: *Sign In with Google* opens the
provider's page in the browser (`NSWorkspace`, https only), the wizard
waits on `account.oauthWait`, tests the account with the session and adds
it (`OAuthPageController` over `WizardController`). A revoked sign-in is
the banner *Sign in to … again in your browser* and *Sign In…* in
*Settings → Accounts*; for Gmail an app password over IMAP/SMTP remains
the fallback. The flow needed no build tag: it is platform-neutral in the
daemon, and GNOME Online Accounts stays the preferred source where it
runs.

What remains is not code but distribution. The daemon ships the
project's Microsoft registration, so Microsoft 365 and Outlook.com sign in
out of the box; it is not publisher-verified, so organisations that
restrict user consent approve it once. For Gmail no client is shipped: it
would need Google's OAuth verification and, for the restricted mail scope,
an annual third-party security assessment (CASA) before users outside a
test list can sign in. Gmail on macOS therefore uses an app password or a
client of the user's own in
`~/Library/Application Support/Malachi Mail/config.toml` (see the
[README](../README.md#oauth-clients-for-gmail-and-microsoft-365)). Who would own that is the open question
of the original exploration, and it is the same on a Linux desktop
without GNOME Online Accounts.

**Distribution is still ahead.** The bundle is ad-hoc signed for the
machine it was built on. Not done: Apple Developer Program membership,
Developer ID signing and notarisation; App Sandbox entitlements (note
that a sandbox moves the data into the app container and the socket with
it, which the MCP bridge's default path does not survive); `malachid` as
a LaunchAgent (today the app starts and stops it, like the GTK UI); an
update mechanism (Sparkle, or the App Store, which reopens the licence
question).

**Repository and licence, decided 2026-09-24.** The client lives in this
repository under `macos/`, so the API contract and the clients move
together in one commit; rule 4 was reworded the same day so that other
platforms are separate clients of the daemon's API rather than branches
of the GTK code. The client is GPL-3.0-or-later like everything outside
`backend/`; `backend/` stays AGPL-3.0-only, the boundary case
[LICENSING.md](../LICENSING.md) anticipates with the commercial core
licence. App Store distribution, if it ever matters, reopens this.
