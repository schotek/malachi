# Architecture

Malachi Mail is a desktop email client for Linux. This document explains the
shape of the system and the reasoning behind it. The RPC contract is in
[api.md](api.md); the threat model is in [security.md](security.md).

## 1. Two processes

```
┌─────────────────────┐        JSON-RPC 2.0         ┌───────────────────────────┐
│  malachi (GTK4 UI)  │ ◄──── unix socket ────────► │  malachid (Go daemon)     │
│  thin client        │  $XDG_RUNTIME_DIR/malachi/  │  all mail logic           │
│  displays, commands │        rpc.sock             │                           │
└─────────────────────┘                             │  account  auth   imap     │
                                                    │  smtp     store  search   │
                                                    │  sanitize thread rpc      │
                                                    └─────────────┬─────────────┘
                                                                  │
                                                       ┌──────────┴──────────┐
                                                       │  SQLite (WAL, FTS5) │
                                                       │  ~/.local/share/    │
                                                       │  malachi/store.db   │
                                                       └─────────────────────┘
```

The backend (`backend/`, Go) owns everything that is not pixels:

- IMAP and SMTP (`emersion/go-imap`, `go-message`, `go-smtp`, `go-sasl`)
- the offline store, synchronisation, conflict handling
- conversation threading
- full-text search (SQLite FTS5)
- **HTML sanitisation** (see §4)
- drafts, the outbox queue, sending
- OAuth2 flows and credential storage (libsecret)

The UI (`ui/`, Go + gotk4 + libadwaita) displays data and sends commands.
It holds no mail state beyond what is on screen and never interprets mail
content beyond rendering what the backend hands it.

### Why two processes and not one binary with a clean package boundary?

1. **The boundary is enforced, not merely agreed.** A package boundary
   inside one binary erodes under deadline pressure ("just call the parser
   from the view for now"). A socket boundary cannot be crossed by accident:
   if the UI needs something, it has to be added to `docs/api.md`, which
   forces the question "does this belong in the UI at all?".
2. **Replaceable UI.** The UI language is not final (Go/gotk4 today; Rust or
   Python bindings remain possible). The backend does not care. A TUI, a
   CLI (`malachi-cli search "from:alice"`), or a test harness can speak the
   same protocol. Everything security-relevant stays in one place.
3. **Crash isolation.** gotk4 is a generated binding with known rough edges;
   a UI crash must not corrupt the store or lose an in-flight send. The
   daemon keeps running; the UI reconnects.
4. **Background operation.** Mail should sync and notify while no window
   is open. A daemon started via the Background portal does that without a
   hidden window hack.
5. **Testability.** The backend is exercised end-to-end through the socket
   with plain JSON, without a display server.

The cost is serialisation overhead and a second process to manage. For a
mail client, where a "large" payload is one message body, the overhead is
irrelevant; `scripts/dev-run.sh` and the desktop integration hide the second
process from the user.

## 2. Transport

JSON-RPC 2.0, newline-delimited, over a unix socket owned by the user
(`0600`). Bidirectional: the daemon pushes notifications (new message, sync
state, authentication needed) on the same connection. Multiple clients may
connect; notifications are broadcast. Details: [api.md §1](api.md#1-transport).

Startup ordering is not assumed: the UI keeps retrying the socket and shows
its state; the daemon replaces a stale socket after a crash and refuses to
start twice.

## 3. Backend layout

```
backend/
  cmd/malachid        process lifecycle: flags, logging, signals, wiring
  pkg/api             the contract (types, method names, error codes, interfaces)
  internal/rpc        socket server, framing, dispatch, notification fan-out
  internal/config     config.toml + XDG paths
  internal/account    account registry (non-secret config)
  internal/auth       keyring, OAuth2, SASL
  internal/imap       IMAP client + sync engine
  internal/smtp       sending + outbox
  internal/store      SQLite, migrations, all SQL
  internal/search     FTS5 indexing and query parsing
  internal/thread     conversation threading
  internal/sanitize   HTML sanitisation (security-critical)
  testdata/mime       MIME samples, including malformed ones
```

Dependency direction: `cmd` → `rpc` → (`api` + service implementations);
service packages depend on `store`, `auth`, `api`, never on `rpc`. Events
flow out through the `api.Notifier` interface that `rpc` implements.

`pkg/api` is the only importable package. The UI imports it for types and
constants; nothing else from `backend/` is reachable.

### 3.1 Store

One SQLite database in `$XDG_DATA_HOME/malachi/store.db`, WAL mode, driver
`modernc.org/sqlite` (pure Go: no cgo, reproducible Flatpak builds, FTS5
compiled in). Schema changes are forward-only numbered migrations embedded in
the binary and applied in a transaction at startup. A migration, once
committed, is never edited.

Tables today: `meta`, `preferences`, `known_senders` (0002), `drafts` and
`attachments` (0003; attachment data as files under
`<data dir>/attachments/`, see §7). Planned (phase 1): `accounts`, `folders`
(with UIDVALIDITY, HIGHESTMODSEQ), `messages` (envelope + flags + local
state), `message_parts` (MIME tree with on-disk or in-db bodies), `threads`,
`outbox`, `messages_fts` (external-content FTS5), `sync_log`.

### 3.2 Sync model (planned)

Per account, one sync goroutine with a folder state machine:

1. `LIST` + special-use detection → folder table.
2. Per selected folder: check UIDVALIDITY (reset on change), then
   incremental fetch by UID ranges: envelopes first, bodies lazily or by
   policy (recent N days eagerly). CONDSTORE/QRESYNC when offered, flag
   re-scan by UID otherwise.
3. `IDLE` on INBOX for push; polling elsewhere.
4. Local changes (flags, moves, deletes, appends) go into an operation log
   applied to the server in order with retry/backoff; conflicts resolve
   server-wins for flags, local-wins for drafts (with `version` guarding
   concurrent UI edits).
5. Every network failure degrades to `offline`; nothing blocks the UI.

**Before implementing this, read Geary's `engine/imap-engine` and
Evolution's `camel-imapx`.** Not to copy code, but to learn how they handle
broken MIME, servers that violate RFC 3501/9051 (Exchange, some Dovecot
setups, quota and UID gaps), thread display when headers lie, and the
countless "this server says X but means Y" cases. Both projects have a
decade of scar tissue that is cheaper to read than to re-earn.

### 3.3 Threading (planned)

JWZ-style threading on `References`/`In-Reply-To` with subject fallback, per
account. Message-IDs are untrusted: cap the number considered, break
cycles, and never let one crafted message merge unrelated conversations
into a mega-thread.

## 4. Security boundary: HTML

HTML in mail is hostile input. It is sanitised **in the backend**
(`internal/sanitize`), never in the UI and never by relying on the webview.
`message.body` returns only the sanitiser's output plus a structured
report of what was removed. There is no raw-HTML path: no flag, no debug
endpoint, no test-only shortcut.

The UI (later phase) renders that output in a WebKitGTK 6.0 view with
JavaScript disabled, a strict CSP, no network access for the view, `cid:`
resources served from the backend, and link activation intercepted so the
real destination is shown and opened through the OpenURI portal.

Full threat model: [security.md](security.md).

## 5. UI layout

```
ui/
  main.go             Adw.Application, actions
  data/ui/*.blp       Blueprint UI definitions (compiled to .ui at build time, embedded)
  data/icons/
  internal/client     JSON-RPC client (transport only)
  internal/window     main window: folders | list | message; message and
                      preferences dialogs
  internal/widget     reusable widgets (message list row) and pure formatters
  internal/settings   UI-only preferences (GSettings, in-memory fallback)
  internal/style      colour scheme and the application CSS provider
  internal/compose    "New Message" window: recipients, drafts, attachments
  internal/editor     rich-text editor on WebKitGTK 6.0 (no mail knowledge)
```

Three panes built from nested `Adw.NavigationSplitView`s with breakpoints
for narrow windows. All UI structure lives in Blueprint; Go code binds
objects by ID and populates them. Callbacks from the client run on a
background goroutine and hop to the GTK main loop with `glib.IdleAdd`.

Preferences are an `Adw.PreferencesDialog` (`app.preferences`, Ctrl+,)
with *General* and *Appearance* pages. UI-only options live in GSettings
(`data/*.gschema.xml`, read through `internal/settings`); anything that
affects mail handling (check interval, remote content) is owned by the
daemon and set through the RPC API. The *Appearance* page is functional:
colour scheme goes through `adw.StyleManager`, list density, preview line
and avatars are pushed to the message rows, and body zoom, font and
monochrome avatars are a display-wide CSS provider (`internal/style`) so
every open window follows.
When the schema is not installed the store falls back to memory and logs a
warning; `make build` compiles the schema into `build/` and the run targets
export `GSETTINGS_SCHEMA_DIR`.

Composing: `app.compose` (Ctrl+N, the header button, `mailto:` through
`GApplication::open`) and the Reply / Reply All / Forward buttons open a
`compose.Window`. The editor is a WebKitGTK 6.0 view with a contenteditable
document: formatting goes through WebKit's native editing commands, a user
script reports content and caret state back over a script message handler,
and the document's CSP plus the decide-policy handler keep it offline.
The window parses recipients into `api.Address`, autosaves through
`draft.save` (the backend sanitises `htmlBody` and derives `textBody`),
imports attachments by path with `attachment.import`, shows inline images
through a `cid:` URI scheme served only for ids the window itself minted,
and sends with `message.send`. Reply/forward prefill lives in
`compose.Prefill` only until `draft.create` exists in the backend.

The *General* page: *Run in Background* makes the main window hide instead
of close (a hidden window keeps the application alive; `app.show` and
activation bring it back); *Launch at Login* asks the Background portal
(`internal/background`, plain D-Bus, never `~/.config/autostart`) to start
`malachi --gapplication-service`, which holds the application until the
first activation; the mark-as-read delay is a timer around `message.flag`;
deleting confirms with an `Adw.AlertDialog` before `message.delete`; new
mail arrives as `notify.newMessage` and becomes a `GNotification` whose
default action is `app.show`. The *Mail* group is daemon-owned
(`config.get`/`config.set`).

## 6. Platform

Linux only. Portability of the *architecture* is provided by the socket
boundary, not by conditional compilation. No Windows/macOS code paths,
build tags, or "just in case" abstractions.

Distribution: Flatpak first (`packaging/flatpak/`), AppImage second. No Snap.

## 7. Open decisions

- UI language: Go + gotk4 for phase 1; Rust + gtk4-rs or Python + PyGObject
  remain possible because the backend does not care.
- Sanitiser library: candidates listed in `internal/sanitize/sanitize.go`;
  decision pending evaluation against `docs/security.md`.
- Account definitions: `config.toml` (user-editable) vs. store (managed via
  `account.add`). Both are loadable today; pick one before phase 1 ends.
  Decided for *daemon options* (`config.get`/`config.set`): the store is
  authoritative, `config.toml` supplies bootstrap defaults, the daemon never
  writes `config.toml`.
- Daemon lifecycle at login: the UI's autostart entry launches only
  `malachi --gapplication-service`; nothing starts `malachid`. Options: the UI
  spawns it when the socket is unreachable, or a systemd user unit / second
  autostart entry.
- Whether message bodies live inside SQLite or as files under
  `$XDG_DATA_HOME/malachi/parts/` (SQLite is simpler; files are cheaper for
  large attachments). Decided for *compose attachments*: data as files
  under `<data dir>/attachments/<id>`, metadata (including SHA-256) in
  SQLite; large message parts are expected to follow the same split.
