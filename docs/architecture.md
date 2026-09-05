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
- Microsoft Graph for Microsoft 365 / Outlook.com mailboxes (REST over
  `net/http`, tokens from GNOME Online Accounts)
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
  internal/account    config.toml form of an account (bootstrap import)
  internal/auth       keyring interface, OAuth2, SASL; auth/secretservice is the
                      org.freedesktop.secrets client, auth/goa the GNOME Online
                      Accounts client (Microsoft Graph tokens)
  internal/transport  TLS policy, dialling, timeouts, error classification
  internal/discover   account.discover: GNOME Online Accounts, ISPDB, provider
                      autoconfig, SRV, Microsoft 365 hint (MX), guesses
  internal/core       composes services, owns the supervisor lifecycle (one
                      dispatcher per account kind) and the notification coalescer
  internal/graph      Microsoft Graph client + sync supervisor for Microsoft 365
                      accounts, sendMail delivery (probe.go: mailbox test)
  internal/imap       IMAP client + sync supervisor, one syncer per enabled
                      account (probe.go: connection test)
  internal/mime       MIME parsing (headers, text extraction, part tree; hostile input)
  internal/smtp       sending + outbox (probe.go: connection test)
  internal/store      SQLite, migrations, all SQL
  internal/search     FTS5 indexing and query parsing
  internal/thread     conversation threading
  internal/sanitize   HTML sanitisation (security-critical)
  testdata/mime       MIME samples, including malformed ones
  testdata/autoconfig Thunderbird autoconfig samples, including hostile ones
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
`<data dir>/attachments/`, see §7), `accounts` (0004; the non-secret
`api.AccountConfig` as JSON plus the columns the store enforces or sorts
by), and the mail tables (0005): `folders` (the mirror of `LIST` with role,
subscription, selectability, UIDVALIDITY/UIDNEXT/HIGHESTMODSEQ, server and
local counts), `messages` (envelope, flags, the curated header subset,
attachment metadata, plus the cached `text_body` / `has_html` and a
`body_state` of `none|fetched|tooBig|failed`; UID 0 marks a row whose local
move has not been pushed yet) and `message_ops` (the operation log: one
`flag|move|delete` row per message and local change, with the source
folder/UID snapshot, attempts and backoff). Raw RFC 822 messages are files
under `<data dir>/messages/<account>/<id>` (see §7). `outbox` (0006) holds
the delivery metadata of a queued message (envelope sender and recipients,
`queued|sending|sent|failed`, attempts, next attempt, last error); the
message itself is a `messages` row in the account's local `outbox` role
folder with its raw file next to received mail. Planned: `threads`,
`messages_fts` (external-content FTS5).

### 3.2 Sync model (implemented)

`internal/imap` runs one supervisor with one syncer goroutine per enabled
account; `internal/core` owns its lifecycle:

- **Lifecycle.** `malachid` starts the supervisor after the config.toml
  import (`Backend.StartSync`: `Run` plus `Start` for every enabled
  account); the account service hooks it — `account.add` starts,
  `account.remove` stops *before* the rows are deleted, `account.setEnabled`
  starts or stops, `account.update` restarts an enabled account once the
  keyring accepted the new password — and `config.set` wakes every syncer
  (`Reload`) so a new interval or retention window applies at once.
  `sync.status` merges the account list with the live states (paused
  accounts report `disabled`, accounts without a running syncer `idle`).
- **Retention window.** `offlineDays` bounds what exists locally: headers
  *and* bodies of messages within the window are fetched (`UID SEARCH
  SINCE`), older messages are not stored at all. Shrinking the window prunes
  on the next pass; growing it backfills silently.
- **Local-first mutations.** `message.flag/move/delete` change the rows and
  queue `message_ops` in one transaction, then nudge the syncer
  (`Trigger`). Every cycle pushes the queued operations first (in order,
  merged into UID sets, retry with backoff, dropped after a limit), then
  synchronises folders. A move keeps the local id; the row waits with UID 0
  until the server's COPYUID (or a Message-ID reconciliation on servers
  without UIDPLUS) assigns the new one. Flags are server-wins after the
  queued change has been pushed.
- **Cycle.** connect → `LIST` (special-use, `STATUS`) → push ops → per
  changed folder: UIDVALIDITY check (reset on change), UID diff against the
  window, envelopes and `BODYSTRUCTURE` first, then bodies newest-first
  (raw file → `internal/mime` → text body; over the raw cap → `tooBig`,
  unparsable → `failed`), server flags for the rest → `IDLE` on INBOX where
  offered, polling at the configured interval otherwise, or a trigger.
- **Failures.** A network error degrades the account to `offline` with
  backoff; a refused login to `authRequired` (plus `notify.authRequired`,
  no retry until the account is updated); nothing blocks the UI.
- **Notifications.** `notify.newMessage` only for messages that arrive
  after a folder's initial sync and only once their body is stored;
  `notify.syncState` goes through a coalescer in `internal/core` that sends
  status/folder/error/lastSync changes at once and progress-only changes at
  most every 500 ms per account, from its own goroutine so a slow client
  never stalls a syncer.

#### Microsoft Graph accounts (`kind: graph`)

Microsoft 365 and Outlook.com mailboxes are synchronised by
`internal/graph` instead of `internal/imap`; `internal/core` routes every
supervisor call by the account's kind, so the lifecycle hooks, `sync.status`
and the notifications above are the same. What differs:

- **Token.** The account has no servers and no password. The sign-in
  belongs to GNOME Online Accounts (`GraphConfig.source = "goa"`); the
  daemon asks `org.gnome.OnlineAccounts` for an access token
  (`internal/auth/goa`), caches it in memory until shortly before its
  expiry and never stores it. A rejected or revoked sign-in is
  `authRequired`, retried every five minutes (the user fixes it in GNOME
  Settings, which nothing wakes the daemon for); no session bus is `error`.
- **Identity.** Messages are keyed by Graph's immutable id
  (`messages.remote_id`, requested with `Prefer: IdType="ImmutableId"`),
  which survives a move; folders by the Graph folder id (`folders.mailbox`).
  Roles come from the well-known folder names (inbox, sentitems, drafts,
  deleteditems, junkemail, archive).
- **Cycle.** resolve roles → list the folder hierarchy → push ops (`PATCH`
  read/flag, `POST …/move`, `POST …/permanentDelete` with `DELETE` as the
  fallback) → per folder a delta query (`mailFolders/{id}/messages/delta`,
  cursor in `folders.delta_link`, the first enumeration bounded by
  `receivedDateTime ge <window>`) → bodies newest-first through
  `messages/{id}/$value` (four at a time; the same raw-file → `internal/mime`
  pipeline as IMAP) → tombstones applied only after every folder of the
  pass ran, so a message that reappears elsewhere is moved locally, not
  deleted and re-created. A cursor the service rejects (410 /
  `SyncStateNotFound`) restarts the folder from scratch.
- **Polling.** Graph offers no push a desktop can receive (change
  notifications need a public webhook), so the inbox is polled every minute
  and every folder at the sync interval; triggers interrupt the wait.
  Throttling (429/503 with `Retry-After`) is honoured per request up to a
  minute, then the syncer backs off as a whole.

**When extending this, read Geary's `engine/imap-engine` and
Evolution's `camel-imapx`.** Not to copy code, but to learn how they handle
broken MIME, servers that violate RFC 3501/9051 (Exchange, some Dovecot
setups, quota and UID gaps), thread display when headers lie, and the
countless "this server says X but means Y" cases. Both projects have a
decade of scar tissue that is cheaper to read than to re-earn.

### 3.3 Sending (implemented, text/plain phase)

`message.send` builds the RFC 5322 message from the stored draft
(`internal/smtp` builder on `go-message`: `text/plain` quoted-printable,
`multipart/mixed` with base64 attachments, `From` always the account
identity, `Bcc` only in the envelope, every header control-stripped), writes
it as a `messages` row in the account's local `outbox` role folder plus an
`outbox` row, and deletes the draft in the same transaction. One outbox
worker goroutine per enabled account (`internal/outbox`, supervised like the
syncers and started/stopped by the same account hooks) delivers due rows
over SMTP (`internal/smtp` `Deliver`: the transport policy, PLAIN/LOGIN
auth, `SIZE`, one session per attempt). Transient failures back off from
one minute to four hours; permanent replies mark the row `failed` for
`outbox.retry`; a refused password defers the whole account and raises
`notify.authRequired`. After delivery the row becomes `sent` and the IMAP
syncer, which owns the connection, uploads the raw file to the `sent` role
folder with `APPEND` (`\Seen`) in its next cycle, deletes the local copy and
re-syncs that folder so the message comes back under a server UID. Outbox
messages never get operation-log entries: flagging and moving them is
refused and deleting them cancels the send. `SyncState.pendingOutbox` is
filled in by `internal/core` from the store on every emitted state and on
every outbox change.

A Graph account has its own outbox supervisor behind the same dispatcher:
the worker submits the very same RFC 5322 file through `sendMail` (base64
MIME) with a `Bcc` header added for the envelope recipients the builder
left out, since Graph takes recipients from the headers. The service files
the Sent copy itself, so the worker drops the local copy after delivery
and triggers a pass of the Sent folder instead of keeping the row for an
upload. New personal Outlook.com accounts have SMTP AUTH disabled by
Microsoft; sending through Graph is unaffected, which is one reason the
Graph path exists.

### 3.4 Threading (planned)

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

The UI renders that output in a WebKitGTK 6.0 view (`internal/htmlview`)
with JavaScript disabled, a strict CSP, no network access for the view (an
ephemeral session pointed at an unreachable proxy, every navigation but the
initial load refused), the message's inline pictures served through its own
`malachi-cid:` scheme from `message.part`, remote images only after the
user asked and only as the daemon fetched and inlined them, the link under
the pointer shown in a corner label, and link activation intercepted: a
link whose text reads as another site's address is confirmed first, then
opened through the OpenURI portal; `mailto:` opens a new message. The HTML
document is always shown on a light canvas whatever the desktop theme,
because colours a mail did not set cannot be adapted without breaking the
ones it did.

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
  internal/i18n       gettext binding (domain "malachi"); the daemon is language-neutral
  internal/compose    "New Message" window: recipients, drafts, attachments
  internal/editor     rich-text editor on WebKitGTK 6.0 (no mail knowledge)
```

Three panes built from nested `Adw.NavigationSplitView`s with breakpoints
for narrow windows. All UI structure lives in Blueprint; Go code binds
objects by ID and populates them. Callbacks from the client run on a
background goroutine and hop to the GTK main loop with `glib.IdleAdd`.
The window keeps a plain-Go view model (`window/model.go`: accounts,
folders, the current message page) that mirrors what the daemon returned;
widgets are rebuilt from it and asynchronous replies are guarded by
generation counters so a late answer never overwrites a newer state.

The sidebar is one `gtk.ListBox` for every enabled account: a
non-selectable header row per account (only when there are at least two),
then the account's `folder.list` as a tree, Inbox first, then the other
special-use roles, then alphabetically, nested folders indented by depth,
with an unread badge per row. A folder with subfolders carries a fold
arrow, and so does an account header; Left and Right fold and unfold the
focused row. A collapsed row adds the unread counts of everything it hides
to its own badge, so folded-away mail stays visible. Folding never moves
the selection or reloads the message list: a hidden folder is still
selected, it just has no row. Which nodes are folded is presentational and
therefore kept in GSettings (`collapsed-folders`, `collapsed-accounts`,
encoded in `internal/window/collapse.go`), not in the daemon. The list pane shows `message.list` for the
selected folder (newest first, `DefaultPageLimit` per page); further pages
are fetched with `page.nextCursor` from a *Load More* footer or when the
list is scrolled to its bottom, and a status page replaces the list while
it is empty, loading or failed (with Retry). The message pane fills the
headers from the list summary at once and then runs `message.get` and
`message.body`; the body is plain text, and `bodyState`
(`pending`/`tooBig`/`failed`) becomes a sentence in the pane rather than
an error. Actions are `win.*` (`mark-read`, `mark-unread`, `toggle-flag`,
`trash`, `archive`, `junk`, `refresh`) with accelerators Delete, a, j, u,
s and Ctrl+R; flag changes and moves are applied optimistically and
reverted with a toast when the daemon refuses.

The bottom of the sidebar carries the sync line — an `adw.Spinner` and a
caption computed from `sync.status` and `notify.syncState` across the
enabled accounts: "Syncing *folder*… 42 %", then sign-in required, sync
error, offline, otherwise "Up to date" — above the daemon connection
status. Ctrl+R and the refresh button send `sync.trigger` for the
selected folder (or for everything when nothing is selected); the
spinner starts immediately and a 30 s timer clears it if no state
notification follows. `notify.authRequired` reveals an `Adw.Banner` above
the message list ("Sign in to *account* again", or the keyring variant)
whose button opens the preferences; the banner hides when that account's
`notify.syncState` leaves `authRequired` or when the account set changes.
Folder and account names in both come from the server and are set as
plain text.

Notifications are dispatched in `window/notify.go`: `notify.newMessage`
becomes a `GNotification` (unless the window is active) and, when its
folder is the selected one, a row inserted at the top of the list, with
the folder's unread badge adjusted either way; `notify.syncState` updates
the sync line and, when an account leaves `syncing`, reloads its folders
and the list of the affected folder; `notify.authRequired` shows the
banner; `notify.accountsChanged` invalidates the compose manager's account
cache and reloads accounts and folders.

Preferences are an `Adw.PreferencesDialog` (`app.preferences`, Ctrl+,)
with *Accounts*, *General* and *Appearance* pages. The *Accounts* page lists
`account.list`, pauses with `account.setEnabled` and removes with
`account.remove` after an `Adw.AlertDialog` with a *delete local data*
check; it reloads after its own actions and when opened.

Adding an account is `internal/accountwizard`: an `Adw.Dialog` with an
`Adw.NavigationView` (identity → server settings → connection test),
opened by `app.add-account`, by the + button of the Accounts page and by
the main window's *No Accounts* page (shown when `account.list` is empty).
The wizard calls `account.discover` after the identity page (a hit goes
straight to the test, a miss opens the prefilled Servers page),
`account.test` (45 s budget, per-endpoint results; `authFailed` returns to
the identity page, other failures offer Retry / Edit Servers / Add
Anyway) and finally `account.add`. The same dialog edits an existing
account (the pencil button of an Accounts row): prefilled, no discovery,
an empty password keeps the stored one (`account.test` with `accountId`
reuses it for the test), and the last step is `account.update`. The UI
only checks address syntax, non-empty fields and the port defaults per
security mode; discovery, probing and validation are the daemon's.
UI-only options live in GSettings
(`data/*.gschema.xml`, read through `internal/settings`); anything that
affects mail handling (check interval, remote content, offline retention)
is owned by the daemon and set through the RPC API. Sidebar fold state lives
there too, as two string lists that store one entry per collapsed node; a
window listens for their `changed` signal so several windows sharing the
profile stay in step. The *Appearance* page is functional:
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
and sends with `message.send`: a rich-text draft goes out as
`multipart/alternative` with the derived text and the sanitised HTML, its
inline pictures in a `multipart/related`. (`richText` in `compose/draft.go`
is the switch back to a plain-text build.) After a send the message shows
up in the local Outbox folder (visible only while non-empty) with a banner
for its delivery state; a failed send offers Retry (`outbox.retry`) and the
trash button cancels the send (`message.delete`). The status line shows
"Sending N messages…" from `SyncState.pendingOutbox`. Reply/forward prefill lives in
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
(`config.get`/`config.set`): check interval, remote content and *Keep
Mail Offline For* (`offlineDays`; 1 week, 1 month, 3 months, 1 year or
everything, an arbitrary stored value snapping to the nearest row).
`config.set` is read-modify-write, so every change echoes the whole
preference set the dialog last received.

## 6. Platform

Linux only. Portability of the *architecture* is provided by the socket
boundary, not by conditional compilation. No Windows/macOS code paths,
build tags, or "just in case" abstractions.

Distribution: Flatpak first (`packaging/flatpak/`), AppImage second. No Snap.

## 7. Open decisions

- UI language: Go + gotk4 for phase 1; Rust + gtk4-rs or Python + PyGObject
  remain possible because the backend does not care.
- Sanitiser library: **decided**, own code over `golang.org/x/net/html`
  (already a dependency), with an own minimal CSS filter. E-mail depends on
  `<style>` blocks and inline CSS that general-purpose sanitisers drop, and
  the remote-content policy, the `cid:` rewrite and the blocked-content
  report are specific to this program; a library would have been a base to
  work around. The ruleset is versioned (`sanitizerVersion`) and fuzzed
  against the corpus.
- Account definitions: **decided**, the same rule as for daemon options
  (`config.get`/`config.set`). The store is authoritative (`accounts`,
  migration 0004, managed through `account.*`); `[[accounts]]` in
  `config.toml` are bootstrap defaults imported once at start when no
  account with the same e-mail exists and the e-mail has not been imported
  before (`meta` key `accounts.imported`, so a removed account is not
  resurrected); the daemon never writes `config.toml`.
- Secret Service session: `plain` today (see docs/security.md §6); switch
  to the DH-encrypted session if bus traffic ever becomes observable from
  a different trust domain.
- Microsoft accounts: **decided** (2026-09-04) — Microsoft Graph with the
  token from GNOME Online Accounts, never IMAP/SMTP with XOAUTH2. GNOME
  Online Accounts holds only Graph scopes (its `Mail` interface reports no
  IMAP/SMTP), so its token cannot drive XOAUTH2; an own OAuth2 flow would
  need an Entra app registration and a client id shipped with the app; and
  Microsoft has disabled SMTP AUTH for new personal Outlook.com mailboxes,
  which `sendMail` does not care about. EWS is being retired in Exchange
  Online (October 2026) and was never an option. Deferred: an own PKCE
  flow (`api.OAuth2Config`, `internal/auth` refresh-token storage) for
  desktops without GNOME Online Accounts; the API types stay reserved for
  it. Gmail remains deferred (CASA audit / bring-your-own client id).
- Internationalised e-mail domains in `account.discover`: not handled
  (IDNA encoding of the domain before the ISPDB/DNS lookups).
- Daemon lifecycle at login: the UI's autostart entry launches only
  `malachi --gapplication-service`; nothing starts `malachid`. Options: the UI
  spawns it when the socket is unreachable, or a systemd user unit / second
  autostart entry.
- Message body storage: **decided**. The raw RFC 822 message is a file
  under `<data dir>/messages/<account>/<id>` (`0600` in a `0700` per-account
  directory, removed with the folder or the account); the parsed plain
  text, the curated headers and the attachment metadata live in SQLite
  (`messages.text_body` and friends). HTML is never stored separately: when
  the sanitiser lands, `message.body` re-parses the raw file and sanitises
  on demand, so a ruleset bump never has to migrate cached HTML. Compose
  attachments follow the same split (`<data dir>/attachments/<id>` plus
  metadata including SHA-256 in SQLite).
