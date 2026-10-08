# Architecture

Malachi Mail is a desktop email client, Linux first, with native clients
for macOS and Windows over the same daemon. This document explains the
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
- the offline store (compressed on request, the large attachments of older
  mail left on the server if the user wishes), synchronisation, conflict
  handling
- conversation threading
- full-text search (SQLite FTS5)
- **HTML sanitisation** (see §4)
- drafts, the outbox queue, sending
- OAuth2 flows and credential storage (libsecret)

The UI (`ui/`, Go + gotk4 + libadwaita) displays data and sends commands.
It holds no mail state beyond what is on screen and never interprets mail
content beyond rendering what the backend hands it.

A second client, `malachi-mcp` (`backend/cmd/malachi-mcp`, see
[mcp.md](mcp.md)), exposes a gated subset of the same protocol to AI agents
over the Model Context Protocol. Like the UI it holds no mail logic; unlike
the UI it returns text only and offers the tools that change or send mail
only when started with a flag.

A third client, the macOS application (`macos/`, Swift/AppKit, §6 and
[macos-port.md](macos-port.md)), speaks the same protocol over the same socket and
mirrors the GTK UI screen for screen. It needed two additions to the
daemon, both extension points chosen at run time rather than platform
code: the helper keyring (`internal/auth/helper`, §3), and run-time
defaults for the two storage preferences (`MALACHI_DEFAULT_COMPRESS_STORE`,
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS`, §3.1), which its daemon gets
because many Macs have small disks. Neither added anything
macOS-specific to the contract.

A fourth client, the Windows application (`windows/`, C#/WinUI 3, §6 and
[windows-port.md](windows-port.md)), speaks the same protocol over the same
kind of socket (AF_UNIX, which Windows has had since 2018) and mirrors the
GTK UI the same way. It needed no new extension point: it uses the helper
keyring as macOS does, with a helper of its own over Credential Manager.
What the daemon did need were fixes that Windows showed up but that are
platform-neutral (the helper's executable check through `exec.LookPath`,
raw message files closed before they are deleted, file operations that
outlast a reader, two path flags of `malachi-mcp`), and no change to the
contract.

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
irrelevant; the UI starts the daemon itself (`ui/internal/daemon`, §2), so
the user never sees the second process.

## 2. Transport

JSON-RPC 2.0, newline-delimited, over a unix socket owned by the user
(`0600` where the platform has file modes). A connection starts with a
mutual proof of a key the daemon makes at every start and writes beside
the socket (`rpc.sock.key`): the daemon proves that it holds the key, then
the client does, before any other request and before any notification
([api.md §1.4](api.md#14-handshake); what it protects against is in
[security.md §8](security.md#8-local-storage)). Bidirectional: the daemon
pushes notifications (new message, sync state, a mail account that needs
signing in) on the same connection. Multiple clients may connect, each
authenticating on its own; notifications are broadcast to the
authenticated ones. Details: [api.md §1](api.md#1-transport).

Startup ordering is not assumed: the UI keeps retrying the socket and shows
its state; the daemon locks its store first, so a second daemon for the
same store exits after about a second, replaces a stale socket after a
crash and refuses to start on a socket whose daemon answers (a flood that
makes that check fail can defeat it only for a daemon of another store,
see [security.md §8](security.md#8-local-storage)). The daemon writes its key
before it accepts a connection, and a client reads the key only once the
daemon has answered `system.hello`, afresh for every connection, so a
socket that answers still means a daemon that is ready, and a restarted
daemon's new key is picked up by the next connection.

Nothing on the desktop runs the daemon (the Flatpak has one command, the
autostart entry is the UI, there is no systemd unit), so the UI does:
`ui/internal/daemon` looks for `malachid` beside its own executable
(`/app/bin`, `build/`, the install prefix; `MALACHI_DAEMON` overrides,
`none` switches it off), starts it with `--socket` when nothing answers on
the socket, waits for the socket before the first dial, restarts it after
an exit with an exponential backoff, and sends it SIGTERM when the
application quits. A daemon that already answers (`make run-backend`, a
debugger, one left behind by a UI crash) is used as is and never stopped.
The supervisor is process management only; it never speaks the protocol:
its probe connects and closes again without sending a line, which the
daemon takes quietly before authentication.
Inside Flatpak the socket sits in `$XDG_RUNTIME_DIR/app/<app-id>`, the one
directory shared between sandbox instances (`api.SocketBase`); anywhere
else a later UI instance could not find the daemon and would start a
second one over the same store.

## 3. Backend layout

```
backend/
  cmd/malachid        process lifecycle: flags, logging, signals, wiring
  pkg/api             the contract (types, method names, error codes, interfaces)
                      and the client side of the connection handshake
  internal/rpc        socket server, key file and connection handshake (no
                      backend code before it), framing, dispatch,
                      notification fan-out
  internal/config     config.toml + XDG paths
  internal/account    config.toml form of an account (bootstrap import)
  internal/auth       keyring interface, OAuth2, SASL; auth/secretservice is the
                      org.freedesktop.secrets client, auth/goa the GNOME Online
                      Accounts client (Microsoft Graph and Gmail tokens),
                      auth/oauth2flow the backend's own sign-in (authorization
                      code + PKCE on a 127.0.0.1 redirect listener, the
                      provider table, client registry, keyring-backed token
                      source; account.oauthStart), auth/helper the
                      platform-neutral keyring over an external program
                      (MALACHI_KEYRING=helper; the macOS app supplies
                      malachi-keychain over the login keychain, the
                      Windows app malachi-credentials over Credential
                      Manager)
  internal/transport  TLS policy, dialling, timeouts, error classification
  internal/discover   account.discover: GNOME Online Accounts, ISPDB, provider
                      autoconfig, SRV, Microsoft 365 / Google provider answer
                      (MX, autoconfig hosts, gmail.com) with alternatives (the
                      own sign-in, Gmail with an app password), guesses
  internal/core       composes services, owns the supervisor lifecycle (one
                      dispatcher per account kind), the notification coalescer,
                      message.download and the background maintenance (§3.1)
  internal/graph      Microsoft Graph client + sync supervisor for Microsoft 365
                      accounts, sendMail delivery (probe.go: mailbox test)
  internal/imap       IMAP client + sync supervisor, one syncer per enabled
                      account (probe.go: connection test)
  internal/jira       Jira client (Cloud REST v3, Data Center REST v2) + sync
                      supervisor for issue-tracker accounts: issues read as
                      threads of synthesised RFC 5322 messages, comments
                      posted from the outbox, notification mail matched
                      (§3.6; probe.go: site detection, sign-in test, the
                      space list); jira/botclean cleans comments a bot
                      relayed, jira/jiratest is the fake site of the tests
  internal/ingest     what of a downloaded message is stored: the attachment
                      policy, the skeleton and its check, the commit; used by
                      both syncers and message.download (§3.2)
  internal/mime       MIME parsing (headers, text extraction, part tree; hostile
                      input); the skeleton of a message without some of its
                      parts, and the cid: references of its HTML
  internal/smtp       sending + outbox (probe.go: connection test)
  internal/store      SQLite, migrations, all SQL; the raw message files (plain
                      or zstd, per-message locks, conversion and sweep) and the
                      staging area they are received into
  internal/search     FTS5 indexing and query parsing
  internal/thread     conversation threading
  internal/board      the board's rules (which thread is a case, its state
                      and reason), the verbatim quote check and the
                      cleaning of annotations; pure, no store (§3.7)
  internal/sanitize   HTML sanitisation (security-critical)
  cmd/malachi-mcp     MCP (stdio) bridge for AI agents: a JSON-RPC client of
                      the socket with a flag-gated tool catalogue (docs/mcp.md)
  testdata/mime       MIME samples, including malformed ones (also the
                      jira-notification-*.eml samples of §3.6)
  testdata/autoconfig Thunderbird autoconfig samples, including hostile ones
  testdata/jira       rendered Jira HTML, bot comments and REST pages,
                      including hostile and malformed ones
  testdata/board      the user's own replies and forwards as the board's
                      rules read them (innerText quotes, forged quote
                      markers, question marks in quotes, URLs, signatures)
```

Dependency direction: `cmd` → `rpc` → (`api` + service implementations);
service packages depend on `store`, `auth`, `api`, never on `rpc`. Events
flow out through the `api.Notifier` interface that `rpc` implements.
`cmd/malachi-mcp` imports only `pkg/api` (and the MCP SDK): it is a client
of the daemon that happens to live in the same module.

`pkg/api` is the only importable package. The UI imports it for types,
constants and the handshake (`api.ClientHandshake`); nothing else from
`backend/` is reachable.

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
under `<data dir>/messages/<account>/` (below, and §7). `outbox` (0006) holds
the delivery metadata of a queued message (envelope sender and recipients,
`queued|sending|sent|failed`, attempts, next attempt, last error); the
message itself is a `messages` row in the account's local `outbox` role
folder with its raw file next to received mail. Threads have no table of
their own: `messages.thread_id` (never empty since 0011) plus the indexes
`(account_id, thread_id, date, id)` and `(folder_id, thread_id, date, …)`
let a listing group a folder by conversation at query time; `message_refs`
(0011) is the derived index of the identifiers each message points at
(`In-Reply-To` and `References`), so a parent arriving after its replies
finds them (§3.4). `messages_fts` (0013) is the full-text index behind
search, with `search_docs` mapping its rowids to message ids (§3.5).
`message_files` (0014) accounts for the raw files, and the same migration
gives `messages` the columns of a message stored without its large
attachments (below). Migration 0015 is the issue-tracker accounts (§3.6):
`accounts` rebuilt with a `realm` column, so that an address is unique
per realm (`""` for mailboxes, the site for a `jira` account, which may
therefore carry the address of the user's mailbox), `folders.virtual`
(the fixed views), `messages.hidden` (the display filter of a
notification mail), the `issues`, `issue_items` and `issue_spaces` tables
the synthesised rows are built from, `issue_mail_links` (a notification
mail to its issue), and the columns `drafts` and `outbox` need for a
comment (`comment_visibility`, `issue_id`).

Migration 0016 is bulk mail: `messages.bulk` (`newsletter`, `list`,
`automated`, `none`, `''` for a row not classified yet) and
`messages.list_id`, written by the pure `internal/bulk` from the curated
`List-Id`, `List-Post`, `List-Unsubscribe`, `Precedence` and
`Auto-Submitted` headers and the fields of bulk-sending services
(`bulk.SenderFingerprints`, rule version 2: they only ever make a message
`automated`; a new rule version reclassifies every row in `core.Maintain`) and never for an issue-tracker account. The IMAP
syncer fetches those fields with the envelope (`HEADER.FIELDS`, bounded
like `References`) and classifies a new row at once, keeping the headers in
`headers_json` so `message.get` has its unsubscribe offer before the body
exists; `ingest` classifies again from the parsed message
(`store.SetMessageBody`), and `core.Maintain` classifies the rows stored
before the migration in batches from the raw file's header block (`meta`
key `bulk.classified`, `<rule version>:done` when finished, over again
when the rule version changes). The table `unsubscriptions` remembers per
account which list (`list:<List-Id>`) or sender (`from:<address>`) the
user left through `message.unsubscribe` (`core/unsubscribe.go`:
DKIM-verified one-click through `internal/oneclick`, or a plain-text
request queued in the outbox without a draft, `EnqueueInput.DraftID` empty);
`DeleteAccount` removes its rows. See `docs/security.md` §7.2.

Migration 0017 is the board (§3.7): `board_cases` (one row per thread of
an account that is a case), `board_annotations`, `board_commitments`,
`board_runs` and the dirty set `board_dirty`, filled by triggers on
`messages`, `issues`, `issue_items` and the `issues.me.` keys of `meta`;
two triggers on `drafts` raise the version of a case whose linked draft
changes. It creates objects only and scans nothing; the daemon fills the
board for stored mail in the background. No board table has a foreign key
to `accounts`; `DeleteAccount` removes the account's rows.

Migration 0018 is local drafts (§3.7): `drafts.local` (a draft that stays
on this device and is not uploaded to the Drafts folder) with the
partial index `drafts_local`; it marks the drafts already linked to a
case local (leaving the copy columns they had) and replaces the
`drafts_board_ad` trigger of 0017 so that deleting a linked draft also
marks its case's thread dirty. 0017 was already in use (a store migrated
by a build of the day), so the column went into a migration of its own
rather than into 0017; 0018 in turn ran on the owner's store the same
morning, before the review that followed, so what that review added went
into 0019. Migration 0019 is `drafts.edited` (the user may have written
in it: saved while linked, or ordinary when linked; never cleared) and
the table `draft_stray_copies` (copies in a Drafts folder no draft holds
any more that the daemon still means to delete: not addressable yet, or a
Graph copy awaiting its syncer's edit check). On a store that ran 0018 it
counts every local draft as edited (whether the user typed in it cannot
be known any more) and moves the copies local drafts still record to
`draft_stray_copies`, clearing their copy columns; case versions and
`board_dirty` stay as they were.

A raw message is `<account>/<id>`, the bytes as received, or
`<account>/<id>.zst`, the same bytes as one zstd frame
(`github.com/klauspost/compress`, pure Go; level 3 with a 4 MiB window and
entropy coding of literal-only blocks, without which base64 attachments are
stored as they came; `store/rawcodec.go`). The name decides how a file
is read, never its content: mail may begin with zstd's magic number on
purpose, so a plain file is never sniffed, and a `.zst` that is not such a
frame is an error (`store.ErrRawCorrupt`), never read as the message. Every
frame records its content size and a checksum of it, and a new `.zst` is
decoded and checked before it is renamed into place; a reader returns no
more than the frame records and never more than 64 MiB
(`store.MaxRawBytes`), so a damaged file fails the read instead of
yielding a shorter or longer message. New files are written in the store's
codec (`Preferences.compressStore`, which `core` hands to
`store.SetRawCodec` at start and on every change) and older ones are
converted in the background (below); a reader tries the store codec's name
first, then the other, so a message stays readable throughout. Nothing
outside `internal/store` touches the files: callers go through
`OpenMessageRaw` (a `RawMessage` whose `Size` and `Rewind` behave the same
in either codec, which is what an IMAP `APPEND` literal and SMTP `SIZE`
need), `PutMessageRaw` and `WithMessageRaw`. A write goes to a temporary
file beside the final name and is renamed into place; a replacement is
flushed before the rename, and the directory before a file in the other
codec is removed, so a crash leaves the old or the new file whole, while a
message's first file is not flushed (a crash costs a download, as before).
Outbox messages are the exception: always plain, flushed with their
directory before the transaction that deletes the draft they replace, and
never converted, since they are the only copy of mail not sent yet.

The two storage preferences, `compressStore` and `attachmentOfflineDays`
(§3.2), have no `config.toml` key and resolve differently from the others
(api.md §4.8): a stored value, else a run-time default the starting process
put in the environment (`MALACHI_DEFAULT_COMPRESS_STORE`,
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS`, `core/runtime_defaults.go`; only
the macOS app sets them, to on and 30 days), else off and 0; in
`config.set` an absent one stays as it is. `StartSync` stores such a
default as the preference where none is stored yet, in an existing store
too, so a daemon started later without the environment keeps what applied;
an invalid value is logged and ignored. Like `MALACHI_KEYRING`, it is
chosen at run time, not by a build tag (§6). The third,
`neverStoreAttachments` (§3.2), follows the same rules without a run-time
default: stored, else off.

`message_files` holds the codec, the message's length and the file's length
of every raw file, so neither `system.storage` nor the conversion has to
stat the directory; it is a table of its own so that updating it never
rewrites a `messages` row with a large `text_body`, and every write,
conversion and removal keeps it current. The partial state lives in
`messages` (§3.2): `raw_state` (`full|partial`), `remote_parts` (the ids of
the parts whose bodies the file leaves out) and `remote_bytes` (their
decoded size), which are set together or not at all; `strippable_bytes`
(what the background pass may leave on the server: -1 not evaluated, 0
nothing under the rule it was judged by, -2 `store.StrippableNever`, never
under any rule); `hydrated_at` (when `message.download` last made the
message whole). `Attachment.remote` is derived from `remote_parts` when a row is
read and never stored in `attachments_json`, whose change would reindex the
message for search, and a change of these columns alone never moves
`updated_at`. `CommitMessageRaw` changes a file and its row in two phases
around the rename, so a crash never leaves a row that calls a part stored
when the file lacks it: the row first names the union of the old and the
new remote sets, then the file is replaced, then the row gets its final
state. That first commit is flushed to disk before a stored file is
replaced (synchronous `FULL` on a connection of its own; the store's other
commits reach the disk with the next checkpoint), and a part the row calls
stored, with a size, that still reads back empty is answered and recorded
as remote (`store.MarkPartsRemote`). Writers of one message take its lock in
turn; a reader holds nothing once its file is open, since a rename or a
removal leaves an open file's content alone, and the background passes only
try the lock and skip a busy message. Windows refuses to rename over or
remove an open file: there a rename or a removal waits a moment
(`internal/fsretry`) with the message's names locked, so its readers
finish and no new one starts, and a file one of them keeps open for
longer leaves the operation undone as busy (`store.ErrBusy`, told by
whether a reader had the file open at the last attempt), for a later pass
or the sweep. A commit whose new file did not take the stored one's name,
whatever the cause (a reader the daemon does not count, such as another
program or a virus scanner, or a new file that could not be written),
undoes its first phase, the stored file being as it was; one whose
rename failed with the new file gone from its temporary name keeps it,
since that rename may have gone through (a reply lost on a network file
system); and of a
message left with both variants a reader takes the newer, the one the
sweep keeps. A deletion removes the files once its rows are committed,
and a message a writer holds at that moment is removed by that writer
before it lets go. A download is received into `<data dir>/staging/`
first (random names, created exclusively, on the file system of
`messages/` so that a commit is a rename), which the daemon
empties whenever it opens the store; under `neverStoreAttachments` it is
received into memory instead (`store.StageMemory`), and only the file the
commit writes from there reaches the disk (§3.2).

Background work on the files is the raw maintenance loop that
`core.Maintain` runs after its one-off upgrade passes
(`core/raw_maintenance.go`). At its start and every hour it sweeps
(`store.SweepMessageFiles`): temporary and staged files, the files of
messages without a row and the empty directories of unknown accounts go
once they are an hour old (a directory with files stays: two stores in one
data directory share `messages/`; but the directory of an account this
store deleted goes whole, whatever its age, since `store.DeleteAccount`
records it in `meta` under `raw.deleted.<account>` until it is gone, which
a reader holding one of its files on Windows, or a crash, can delay); of
a message left with both variants the newer valid one stays; missing
accounting rows are added and wrong ones corrected, which on the first
start after migration 0014 accounts for every older file
(`meta` `raw.accounted`; until then `system.storage`
estimates those files from the message sizes). Then it runs its steps in
order, each a `RawStep` with its progress in `meta` under
`raw.step.<name>` (`<key>|<cursor>`, or `<key>|done`), restarted from the
beginning when its key changes: `codec`
(key: the target codec) converts the files to the store's codec in either
direction, 64 per batch and in two phases — every new file written, checked
and flushed beside its source, the directories flushed, and only then each
source removed, unless a writer replaced either file meanwhile
(`os.SameFile`) — and starts over when a sweep finds files in the other
codec; `attachments` (key: the policy and the date, so it runs daily as
mail ages) reduces the messages that aged past `attachmentOfflineDays`,
oldest first, or under `neverStoreAttachments` (key `3:never:<date>`, the
version of the rule first, daily as well) every stored message (§3.2);
`restartRawStep` starts a step's pass over although it finished for its key, clearing the progress at once
and again when the loop next looks, so that a batch running meanwhile
cannot store its own progress over the restart. After each batch the
loop pauses as long as the batch took, at least 20 ms, so it never takes
more than half a core; `config.set` wakes it to look at the keys again,
and so does a check every minute while it is idle. A full disk
(`store.ErrNoSpace`) stops it, `system.storage` reporting
`conversion: noSpace`, until the next `config.set` or restart. At
shutdown `malachid` waits for the batch in progress, within the same
10 seconds it gives the syncers, before it closes the store.

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
  on the next pass; growing it backfills silently. Within the window,
  `attachmentOfflineDays` may leave the large attachments of the older
  messages on the server (below).
- **Local-first mutations.** `message.flag/move/delete` change the rows and
  queue `message_ops` in one transaction, then nudge the syncer
  (`Trigger`). Every cycle pushes the queued operations first (in order,
  merged into UID sets, retry with backoff, dropped after a limit), then
  synchronises folders. A move keeps the local id; the row waits with UID 0
  until the server's COPYUID (or a Message-ID reconciliation on servers
  without UIDPLUS) assigns the new one. Flags are server-wins after the
  queued change has been pushed.
- **Cycle.** connect → `LIST` (special-use, `STATUS`) → push ops → per
  changed folder, in `store.SortSyncOrder` (the inbox first, then drafts
  and sent, then ordinary folders, and junk, archive and trash with their
  subtrees last, so the folder the user is looking at is never queued
  behind the bulky ones; the display order is `folder.list`'s own):
  UIDVALIDITY check (reset on change), UID diff against the
  window, envelopes and `BODYSTRUCTURE` first, then bodies newest-first
  (each through `internal/ingest`, below: staged, parsed, stored whole or
  as a skeleton, text body; over the 25 MiB cap → `tooBig`, unparsable →
  `failed`), server flags for the rest → `IDLE` on INBOX where
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
  With `source: "daemon"` the token comes from the backend's own sign-in
  instead (`internal/auth/oauth2flow`, refresh token in the keyring, §7),
  and a revoked sign-in opens a re-sign-in the UI completes in the
  browser.
- **Identity.** Messages are keyed by Graph's immutable id
  (`messages.remote_id`, requested with `Prefer: IdType="ImmutableId"`),
  which survives a move; folders by the Graph folder id (`folders.mailbox`).
  Roles come from the well-known folder names (inbox, sentitems, drafts,
  deleteditems, junkemail, archive) — one request each, so a mailbox that
  has been synchronised before takes them from `folders.role` instead and
  asks the service again only on a `full` pass.
- **Cycle.** resolve roles → list the folder hierarchy (ordered by
  `store.SortSyncOrder`, as above) → push ops (`PATCH`
  read/flag, `POST …/move`, `POST …/permanentDelete` with `DELETE` as the
  fallback) → per folder a delta query (`mailFolders/{id}/messages/delta`,
  cursor in `folders.delta_link`, the first enumeration bounded by
  `receivedDateTime ge <window>`) → bodies newest-first through
  `messages/{id}/$value` (four at a time; through `internal/ingest` as for
  IMAP) → tombstones applied only after every folder of the
  pass ran, so a message that reappears elsewhere is moved locally, not
  deleted and re-created. A cursor the service rejects (410 /
  `SyncStateNotFound`) restarts the folder from scratch.
- **Cursors across restarts.** The delta cursors are persisted, and a
  daemon restart resumes from them: enumerating the window again is
  reserved for a folder with no cursor, an explicit `sync.trigger`
  `full: true`, a retention window that *grew* (delta never replays older
  mail; a window that shrank prunes incrementally), and a folder no pass
  has enumerated for a week. Without that, every start re-read every folder
  and the inbox waited behind them.
  The inbox has a shorter backstop: after an incremental pass, at most
  every ten minutes, its message count within the retention window is
  compared with the server's (`$count`, or `totalItemCount` without a
  window), and a mismatch makes the next inbox pass enumerate (at most once
  an hour, `internal/graph/inbox_check.go`).
- **Polling.** Graph offers no push a desktop can receive (change
  notifications need a public webhook), so the inbox is polled every minute
  and every folder at the sync interval; triggers interrupt the wait.
  Throttling (429/503 with `Retry-After`) is honoured per request up to a
  minute, then the syncer backs off as a whole.

#### Stored bodies and attachments on demand

Every body a syncer downloads, over IMAP or Graph, and every
`message.download` goes through `ingest.Store`: the bytes are received into
the staging area (at most 25 MiB, `ingest.MaxMessageBytes`), parsed, judged
by the attachment policy and, when parts are to stay on the server,
rewritten into a skeleton that has to pass a check, then committed together
with the row (`store.CommitMessageRaw`, §3.1); an unparsable body is stored
as received and marked `failed`, as before. Each message is decided when it
is downloaded, so a first sync of a large mailbox does not fill the disk
with attachments it is about to drop. It saves disk, not transfer: a
message is always downloaded whole.

The policy (`ingest.Decide`, `Preferences.attachmentOfflineDays`: 0 keeps
every attachment, N those of the last N days, -1 small ones only): the
candidates are the attachments of at least 100 KiB
(`api.LargeAttachmentMinBytes`) that are not the text or HTML body and whose
Content-ID the HTML does not reference. The references come from
`mime.CIDReferences`, a superset of what the sanitiser resolves (every
attribute and every text node, not only `img src`; when the list may be
incomplete, every part with a Content-ID counts as referenced), so a
message never loses a picture it shows (under this preference;
`neverStoreAttachments` below leaves the large ones on the server). The
candidates stay on the server
when the message is older than the cutoff (midnight UTC N days back, the
retention window's day boundary), judged by the server's internal date and
only without one by the `Date` header the sender chose, or under -1 at
once; never for a message in Drafts or the outbox, one without a copy on the
server (no UID, no Graph id), a signed or encrypted one (`Parsed.Crypto`: a
signature covers the parts as they are), a parse that hit a limit, or
within seven days of an on-demand download (`hydrated_at`,
`ingest.HydratedKeep`).

The skeleton (`mime.Skeleton`) is the message again with the omitted
leaves' bodies left empty: every header as go-message reads it, the other
leaves byte for byte, the multipart structure with the same boundaries and
hand-written delimiter lines, split by the same readers, limits and part
numbering as `mime.Parse`. It is only a candidate: `ingest` parses it, and
`mime.VerifySkeleton` must find the same envelope, headers, text and HTML
bodies, snippet, part numbers and attachment list as in the original, every
size equal except the omitted parts', which must be 0. Anything doubtful —
a missing boundary, a reader error, a limit, a line that would read as a
delimiter once its line ending became CRLF — stores the whole message, and
a message that cannot be reduced safely (this, a signed or encrypted one, a
parse that hit a limit, a stored file that does not parse) is marked so
(`strippable_bytes` -2, `store.StrippableNever`) and not tried again under
any policy. The row always gets the parse of the whole message:
`attachments_json`, `text_body` and the search index describe every part,
and a skeleton changes only the file and the partial-state columns (§3.1).

Mail that ages past the policy after it was stored is reduced by the
maintenance step `attachments` (§3.1, `ingest.Strip`), oldest first, from
the stored file alone, without a network request; a tightened setting is
applied the same way. A loosened one applies to mail downloaded from then
on: nothing is fetched back in the background, and an older attachment
comes when the user opens it (§7).

`message.download` (`core/download.go`) fetches a stored message again when
the user asks for content the device does not hold: a `remote` attachment,
or a body still `pending`. It uses a connection of its own, apart from the
syncer and its IDLE, and only reads: IMAP `EXAMINE` with the folder's
UIDVALIDITY checked, then `UID FETCH BODY.PEEK[]` (`imap.FetchMessage`), or
Graph's `me/messages/{id}/$value` (`graph.FetchMessage`). `ingest.Store`
then keeps the whole message, after checking that the download is the
stored message: the same Message-ID and, on IMAP, where a UID names the
same bytes for good, the same part numbers and sizes (Microsoft 365
rebuilds a message's MIME, so its part ids may change). A message whose
local move has not reached the server yet is found through the UID
snapshot of its pending operation. Calls for one message share one
download, an account runs two at a time, and a download runs detached from
its caller within 4 minutes; pausing or removing the account cancels its
downloads and waits for them before rows or files go. A message the server
no longer has is `messageGone` and triggers a pass of its folder, which
removes the local copy; on IMAP only a `NO [NONEXISTENT]` or
`[EXPUNGEISSUED]`, a mailbox `LIST` does not show after a `NO` without a
code, another UIDVALIDITY or a `UID FETCH` answered without the message
say so, while `[UNAVAILABLE]`, `[INUSE]` and `[LIMIT]` are `unavailable`
and any other `NO` a `serverError`. A message announced over the cap is
refused unread. An IMAP literal shorter than the size the server
announced, or a Graph body that breaks off, is a network error and never
stored as a message; the syncers check the same.

`neverStoreAttachments` (`ingest.Policy.NeverStore`) overrides
`attachmentOfflineDays`: every attachment of at least one byte that the
HTML does not show through `cid:` is a candidate, and so is a picture it
shows of 100 KiB and more (`api.LargeAttachmentMinBytes`; with a
reference list that may be incomplete, a part with a Content-ID of that
size), whatever the message's age and however recently it was
downloaded; a smaller picture the HTML shows stays with the text. The
exceptions above stay as they are (Drafts, the outbox, no copy on the
server, signed or encrypted, a parse that hit a limit or failed, a
skeleton that does not verify: stored whole). A picture left on the server
keeps its part, headers and Content-ID in the skeleton, with an empty
body, so the HTML still points at it (below). A body a syncer downloads,
and a `pending` one `message.download` fetches, is received into memory
(`store.StageMemory`) instead of `staging/`, and its skeleton is built
there too, so only the file committed, the skeleton or a whole message of
the exceptions, reaches the disk. Switching the preference on changes the
key of the `attachments` step to `<rule>:never:<date>`
(`ingest.NeverStoreRule`, now 3; 2 did not take the pictures the HTML
shows, and a daemon with a new rule runs the pass at once), a pass a day
that reduces every stored message, also those downloaded on request
(`StripQuery.AnyHydrated`), and catches what came to be stored whole since
the last one (a message moved out of Drafts, one a batch passed over
while it changed); a day with nothing to do costs a search of the partial
indexes. After the messages stored whole, each batch takes the messages
stored partial whose file still holds a non-empty attachment
(`StripQuery.Partial`, through `messages_partial`: a message
`attachmentOfflineDays` reduced keeps its small attachments):
`ingest.Strip` parses the stored skeleton, whose omitted parts are empty
and so no candidates, builds the new skeleton from it and verifies it
against that parse, and commits the union of the old and the new remote
sets (phase A widens the row first, as for any reduction) with
`remote_bytes` grown by the parts omitted now.
A reduction under `NeverStore` records `strippable_bytes` 0 (nothing more
to leave on the server, so the pass never takes the message up again),
and a partial message with nothing left to omit is settled at 0 without
a new file; one under the size threshold keeps the candidates' size, so
that a later switch-on takes it up. A partial message that cannot be
reduced further safely is -2, as a whole one. Before the first pass,
once per switch-on and rule (`meta` `attachments.never_store.reevaluated`
holds the rule it was done by, `3`, or `done` for rule 2; cleared while
the preference is off), the messages settled at 0 whose file still holds
a non-empty attachment (any listed one of a whole message, one outside
`remote_parts` of a partial one; a picture the HTML shows is listed too)
are marked not evaluated again (`store.ReevaluateSettled`); the daily
passes do not repeat that. A store judged under rule 2 in the same
switch-on holds, in its settled messages, only pictures the HTML shows,
so from `done` only a message whose file holds a part of 100 KiB or more
is marked; a message never to be reduced (-2) keeps its mark.
Switching the preference either way and enabling a paused account start
the pass over at once (`restartRawStep`), so that switching it off and on
again the same day runs a pass although one finished under that day's
key. The syncers read the preferences for each body as it arrives, and
`message.download` when the server starts sending, and they tell the
daemon which policy a message was stored under (`storedUnder`): one
stored without `NeverStore` while the preference is on by then (switched
on while it was being received, perhaps after the switch-on's pass) is
marked not evaluated again if it had settled at 0
(`store.ReevaluateSettledMessage`) and the pass starts over, which takes
it up whether it was stored whole or reduced under the size threshold. Switching it
off brings nothing back, as loosening `attachmentOfflineDays` does: the
next download of a message stores it whole.

Under the preference, `message.download` of a message whose body is stored
writes nothing: `ingest.Hold` receives the download into memory and checks
it against the row as `ingest.Store` would, and the whole message goes into
the daemon's memory cache (`core/memcache.go`), the row and the skeleton
staying as they were and the parts `remote`. That holds in Drafts too, for
a reduced message moved there. A `pending` body is stored as a skeleton and
held whole as well, unless it is one of the exceptions, which is stored
whole and not held. The cache is bounded by bytes (256 MiB, the least
recently used message evicted first), drops a message unused for 30
minutes, and is emptied when the preference is switched off and when the
daemon closes (`Backend.Close`), an account's messages when the account is
paused or removed; a generation counter keeps a download that finishes
after a switch-off from putting its message back. Nothing of it is written
to disk or logged; `put` keeps the downloaded bytes it is handed without
a copy, and a download received into memory takes room for the size an
IMAP server announces up front, within the 25 MiB cap. A message held
answers `message.download` at once, as long as the copy has every part
the row keeps on the server (`heldMissing`); `message.download` holds a
copy only then (Microsoft 365 rebuilds a message it serves again and may
name or type a part anew, which its check by Message-ID alone lets
through) and answers `serverError` otherwise, so that an unusable copy
never keeps a message from being downloaded again. `message.part` and
`message.embedded` (`extractPart`) and the quoter of `draft.create` and
`draft.open` (`importParts`) take a part the row calls remote from the
held copy (`heldPart`): the part with the same id, file name, type and
size, else the only one with that name, type and size, else the only one
with that name and type (Microsoft 365 numbers the parts anew when it
serves a message again), never one of another size while one of the right
size is there; without a copy or a match the part is `partNotDownloaded`,
or `skipped`, as before. A part the row gives a
size that reads back with no bytes counts as remote in either mode, since
under this one even a small part is left on the server.

`message.body` still shows a message whose large pictures are on the
server: the sanitiser rewrites their `cid:` references to `malachi-cid:`
as for any part the message has, and `renderHTML` counts in
`remotePictures` the entries of `inlineParts` that `message.part` cannot
serve now: parts the row keeps on the server (read before the file is
opened and again after, as `extractPart` does), and parts the file holds
empty although the row gives them a size, which it records as remote
(`markLostParts`) so that `message.download` fetches them, less those the
held copy has (`heldPartID`). It asks the store and memory only; showing
a message never contacts the server. The UI offers to download the
pictures and asks for the body again, which counts 0 while the copy lasts.
A reply or forward quotes such a picture only from the held copy and
otherwise lists it in `skipped` with `remote` set, as any other part.

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
refused and deleting them cancels the send. `SyncState.pendingOutbox`
(queued or sending) and `SyncState.failedOutbox` (given up, waiting for
`outbox.retry` or a delete) are filled in by `internal/core` from the store
on every emitted state and on every outbox change.

A Graph account has its own outbox supervisor behind the same dispatcher:
the worker submits the very same RFC 5322 file through `sendMail` (base64
MIME) with a `Bcc` header added for the envelope recipients the builder
left out, since Graph takes recipients from the headers. The service files
the Sent copy itself, so the worker drops the local copy after delivery
and triggers a pass of the Sent folder instead of keeping the row for an
upload. New personal Outlook.com accounts have SMTP AUTH disabled by
Microsoft; sending through Graph is unaffected, which is one reason the
Graph path exists.

### 3.4 Threading (implemented)

Every message carries a thread id, assigned inside the transaction that
writes it (`UpsertMessages`, `SetMessageBody`, `EnqueueOutbox`), so a
`notify.newMessage` already names the conversation. The rule is a union,
not the JWZ tree: the new message's thread merges with every thread its
`In-Reply-To` or `References` name, with every twin that shares its
`Message-ID`, and with every stored message that names it (found through
`message_refs`). That is idempotent, independent of arrival order (Sent
often syncs after Inbox; References may only arrive with the body) and has
no cycles to break. The policy lives in `internal/thread` (`Resolve`), the
SQL in `internal/store/threads.go`; a store from before threading is
linked in batches by `core.Maintain` at the next start (`meta` key
`threads.linked`, resumable).

Message-IDs are untrusted, so they are linking hints matched exactly,
never an identity, and the caps bound a crafted message: at most 50
`References` are kept (the nearest ancestors), a lookup reads at most 512
rows, and a merge that would pass 500 members is skipped, so the rest
forms further threads instead of one mega-thread. There is no subject
fallback: a reply without headers stays apart rather than joining
strangers. Threads never cross accounts. Microsoft Graph accounts keep the
server's `conversationId` as the thread id; local messages (an outbox
reply) may join such a thread, server threads are never merged or renamed
locally. Local ids are `t_` + 32 hex, server ids carry no prefix. The
larger thread keeps its id on a merge and, on a tie, the one that was
there first (a reply joins the conversation, the conversation does not
take the reply's id), so ids are stable for most of a conversation's life
but not forever: a client holding a stale id refreshes.

Listing is per folder (`thread.list`, `thread.get`, `core/threads.go`,
docs/api.md §4.4): a thread appears in a folder when a member is there,
and its aggregates cover the members in that folder, grouped at query
time over the folder/thread index; `latest` is the newest member in full,
so a client shows a conversation row from the listing alone. Actions stay
per message. The IMAP header fetch asks for the `References` field next
to the envelope (`BODY.PEEK[HEADER.FIELDS (REFERENCES)]`), so a reply is
linked before its body arrives.

### 3.5 Search (implemented)

`search.query` (docs/api.md §4.6) searches the local store only: what the
`offlineDays` window holds, bodies once downloaded. The index is a
contentless FTS5 table (`messages_fts`, migration 0013): it keeps tokens,
never a second copy of the text, with columns for the subject, the sender,
the recipients (To, Cc, Bcc), the attachment names and the plain-text
body. The `unicode61` tokenizer removes diacritics and case, and the
query side turns every word into a prefix query, so "priloh" finds
"Přílohy"; the index keeps 2- and 3-character prefixes because search
runs while the user types (on 50,000 messages "pr" took 295 ms without
them and 10 ms with them, for about 70 % more index). `messages.id` is a
TEXT key whose implicit rowid VACUUM may renumber, so `search_docs` gives
every message a rowid of its own. Triggers on `messages` keep the index
current on insert, on a change of an indexed column (the body arrives
after the envelope) and on every deletion, cascades included; the rows of
a store from before search are indexed in batches by `core.Maintain`
(`meta` key `search.indexed`, resumable), a few megabytes of text per
write transaction so sync is never held up.

`internal/search` is pure: it parses the documented syntax (never failing:
anything unknown is plain words), compiles the words and phrases into an
FTS5 expression in which every value is quoted, so nothing typed is
operator syntax, and cuts the excerpt around the first match with the same
folding. The expression is bound as an SQL parameter; the filters
(`is:`, `has:`, `before:`/`after:`, `in:`, the scope) are SQL. The store
sorts the matches by date as narrow (id, date) rows and joins only the
page, and counts at most `api.MaxSearchTotal` of them: sorting whole rows,
or counting every match of a two-letter prefix, made a search of a large
store several times slower. Folder and account are joined at query time,
so a move needs no reindexing; Trash and Junk are left out unless a folder
is named. The query is never logged.

### 3.6 Issue-tracker accounts (`kind: jira`)

A Jira site is read by `internal/jira` as a third kind of account beside
IMAP and Graph (`api.md` §4.1; decided 2026-09-29, §7): the selected
spaces (projects) and three fixed views are its folders, every issue is a
thread, and the description, each comment and each change of the status
or the assignee is a message of it. Nothing of the sync engine, the
store, threading, search, the reading pane or the MCP bridge knows the
site: the syncer turns what the site says into RFC 5322 messages and
stores them through `internal/ingest` like mail, and the issue tables of
migration 0015 (§3.1) only decorate them (`MessageSummary.issue`,
`ThreadSummary.issue`, `Folder.virtual`). `internal/core` routes the
lifecycle by kind through the same table as Graph (`kindSupervisor` in
`core/dispatch.go`, one entry per kind; the outbox likewise), so
`sync.status`, the notifications and the account hooks of §3.2 are the
same. What differs:

- **Token.** Jira Cloud takes the Atlassian account's e-mail (`login`)
  and an API token as HTTP Basic authentication, Data Center a personal
  access token as a Bearer token; both are the account's
  `credentials.password` in the keyring (`auth.KeyPassword`), asked for
  once per syncer and dropped when the site refuses it. A scoped Cloud
  token works only through the Atlassian API gateway
  (`https://api.atlassian.com/ex/jira/<cloudId>`): a request the site
  answers with 401 is tried there once, and the route that worked is
  kept for the process. The token goes to the site's origin and to that
  gateway only: redirects are followed by the client itself, never to
  another host with the credentials and never from https to http
  (`client.go`); `account.test` and `account.listSpaces` use a stored
  token only for the site it was stored for (`accounts_jira.go`). No
  OAuth (§7). A refused token is `authRequired` with `notify.authRequired`
  once, retried after 30 minutes unless the account is updated.
- **Identity.** An issue's rows share `thread_id = "jira:" + issue id`
  (`store.IssueThreadID`, outside the linker's `t_` ids, so
  `References` never merge two issues) and its space's folder
  (`folders.mailbox = "space:" + id`); an item's copies share
  `messages.remote_id` (`i:<issue>`, `c:<comment>`, `h:<history>`) and
  the Message-ID. `issues` holds the issue as last seen (key, summary,
  status, assignee, reporter, watching, `updated` from the site and
  `synced_updated` for what the rows reflect, the `render_key` they were
  built with, the views they are in), `issue_items` the items, and the
  syncer never re-upserts a known row (`UpsertMessages` would reset the
  flags, which are the only local state). The user's own site identity
  (`/myself`) is `meta` `issues.me.<account>`.
- **Synthesis** (`synth.go`, `images.go`, `events.go`). A message is a
  function of what the site says and of the account's rendering settings
  alone: no clock, no random boundary, so a rebuild is byte for byte the
  stored message while the site says the same, and `message.download`
  (`fetch.go`, `rebuildsMIME` in `core/download.go`, `Strict` off as for
  Graph) can rebuild it from the site; a `X-Malachi-Revision` header
  hashes what it was built from, with `synthVersion` (2). Senders are
  `<user id>@users.jira.invalid` (a hash of the id when it is no plain
  local part, of the name for a comment a bot relayed), Message-IDs
  `issue.<id>@<site host>.malachi.invalid`, `comment.<cid>.issue.<id>@…`
  and `history.<hid>.issue.<id>@…`, comments and events in reply to the
  description, the subject `KEY: Summary` on every row (retitled on a
  rename). The site's rendered HTML stays hostile input for the
  sanitiser at display; synthesis only makes relative links absolute and
  embeds the pictures the site itself serves as `cid:` parts (downloaded
  with the account's credentials, sniffed, never SVG, at most 32 of at
  most 16 MiB each within one 16 MiB message budget; over that a picture
  stays a link); the issue's files are attachment parts of the item
  whose HTML names them, else of the description. Events are
  `text/plain`, one language-neutral line per change (`To Do → In
  Progress`, `—` for an empty side); clients build the sentence from
  `changes`. Every message goes through `internal/ingest` (§3.2) under
  the attachment policy like mail.
- **Cycle** (`sync.go`, `issues.go`; one goroutine per account). The
  queued flag operations first (`ops.go`: a flag set on one copy of an
  item is given to every copy, then dropped; moves and deletions the
  account cannot ask for); then the user (daily) and the site's spaces
  (every 6 h, `issue_spaces`) and the folders from the configuration
  (`folders.go`: the views minus `disabledFolders`, then the spaces);
  then the changed issues: a space folder never enumerated for the
  account's window (a new account or space, a window that grew, a
  `full` trigger) gets its window enumerated (`updated >= "-<days>d"`,
  100 a page) plus the open issues assigned to the user whatever their
  age (at most 500); the others are searched for what changed since the
  last pass started, 5 minutes earlier (`updated >= "-<minutes>m"`:
  relative JQL, so the profile's time zone never matters), plus what the
  store owes a refresh (an interrupted pass, other rendering settings,
  the queued keys of triggers and deliveries) fetched by id (Cloud
  `bulkfetch` and `reconcileIssues` against the search index's lag, DC
  `id in (…)`). Each changed issue is materialised, four at a time:
  comments (`renderedBody`, the newest 500) and, unless `hideEvents`, the
  changelog (status and assignee only); items that vanished and copies
  in folders the issue left deleted; a new row only where none is, with
  the flags of an existing copy, else read when the item is the user's
  own or an event, or created more than three days before the first
  enumeration of its space, or before the last pass; bodies built and
  stored for new rows and changed items (an edited comment keeps its
  flags, `edited` set); envelopes follow renames and re-attributions;
  the item rows, then last the issue row with `synced_updated`, so an
  interrupted materialisation is completed by the next pass. A new item
  of someone else is announced once (`notify.newMessage` on the space
  folder's row), never on a first enumeration and never for an event.
  Then the reconciliation (below), the retention, the folder counts,
  the space folders' cursors (`last_sync_at` = the pass's start,
  `delta_link` = `window:<days>`), an account-level `notify.syncState`
  and, when rows changed in place, one `notify.messagesChanged`.
- **Polling and timings.** Jira has no push a desktop can use, so a pass
  runs every 60 s while the sync interval is not 0 (manual: on a trigger
  only), and an issue trigger (`TriggerIssue`, from a notification mail
  or a delivered comment) brings one forward, debounced 2 s and at most
  30 early passes a minute. Throttling (429/503 with `Retry-After` or
  `X-RateLimit-Reset`) is honoured per request up to a minute, then by
  the syncer as a whole up to 15 minutes; failures back off from 5 s to
  5 minutes (±20 %); at most 4 requests of an account are in flight (the
  Cloud limiter counts per user), 60 s per request and 5 minutes per
  picture, JSON answers capped at 32 MiB.
- **Reconciliation and retention** (`reconcile.go`). The incremental
  search sees what changed, not what went — a deleted issue, one moved
  out of the selected spaces or (with `onlyMine`) no longer the user's,
  and watching an issue changes nothing of its `updated`. Hourly, and on
  a `full` pass, the syncer enumerates the ids in scope (the window, the
  open issues assigned to the user, the watched ones; ids only, 1000 a
  page) and compares: an id the store lacks, or whose watching differs,
  is refreshed; a stored issue the enumeration should have named and
  did not is fetched directly and deleted when gone, out of the
  selected spaces or no longer the user's (unless a notification mail
  named it), else refreshed. An enumeration that stopped at its cap
  proves nothing. Retention, on every pass: an issue last updated before
  the account's own window (`JiraConfig.offlineDays`, 0 = 30, at most
  365; the `offlineDays` preference does not apply) plus a day's grace
  is deleted with every row and file, unless it is open and assigned to
  the user (at most 500 of those, the most recently updated).
  `DeleteIssues` also shows the notification mail it hid again.
- **Views** (`assignedToMe`, `watching`, `open`; `Folder.virtual`, role
  `none`). Copies of the space folders' rows for the issues in view, with
  ids of their own, the same `remote_id`, Message-ID and `thread_id`;
  `open` is the statuses not in `closedStatuses`, else not of the
  category done. They cover the selected spaces only. `thread.get`
  without `folderId` and an account-wide `search.query` leave the
  copies out, so a message is found once; a flag on any copy reaches
  all. The storage they multiply is accepted (§7).
- **Bot comments** (`jira/botclean`). An integration that mirrors
  comments between two sites ("Issue Sync – Synchronization for Jira")
  posts every remote comment under its own account with a header line
  `KEY-1 Jana Dvořáková added comment - 10/06/26 14:39 GMT+2`. For a
  comment whose author is one of `botNames` (compared after
  normalisation, as whole words), the cleaner takes the author and the
  time from that header, strips `authorPrefixes` from the name, removes
  the header, and re-attributes the message (`from` the person, `via`
  the bot); from every comment it removes the lines matching one of
  `metadataFilters` (RE2, whole trimmed lines), never emptying a
  comment. It works on the tree of the rendered HTML (`x/net/html`, at
  most 1 MiB, bounded lines and depth; preformatted, table, list and
  quoted blocks left whole; a panic returns the input) and is a cleaner,
  not a boundary: its output goes through `internal/sanitize` at display
  like any HTML. Time zones are a fixed table (CET, CEST, GMT±N,
  numeric offsets; "CET" between the last Sundays of March and October
  is read as +2, as the prototype's Europe/Prague did), no tz database.
  The rules, `hideEvents` and `synthVersion` make the account's
  `render_key`; a change rebuilds every stored item in place under its
  ids on the next pass, which ends with `notify.messagesChanged`.
- **Comments** (`core/comments.go`, `jira/deliver.go`, `comment.go`,
  `adf.go`, `wiki.go`). The account's capabilities are `["comment",
  "forward"]`: `draft.create` `reply` makes a comment draft of the
  message's issue (`Draft.comment`: the issue, `visibility` `public` or,
  on a service-desk issue, `internal`), local only (no Drafts folder on
  the site, `draft_sync` is not armed), with no recipients, attachments
  or quote; the other modes are `invalidArgument`. `message.send` queues
  it into the account's outbox as a MIME message like mail (`From` the
  user as the site names them, the issue's subject, `In-Reply-To` the
  message, so the queued row sits in the issue's thread) with the issue
  and the visibility in the `outbox` row, and the outbox worker hands
  it to `Supervisor.Deliver`: the sanitised HTML (or the plain text) is
  read into a small document model and written as the Atlassian
  Document Format on Cloud or wiki markup on Data Center (paragraphs,
  marks, links to http(s) and mailto only, lists, quotes, code, rules,
  headings; pictures dropped, the user's text never read as markup; at
  most 32 767 characters, on Cloud of the ADF's JSON) and posted with
  two entity properties: `io.github.schotek.malachi.outbox` = `{"id":
  <outbox message id>}`, by which a retry after an answer that never
  came finds the comment the site took among its newest 50 instead of
  posting it twice, and `sd.public.comment` for an internal comment.
  After the post the issue is refreshed and waited for (30 s at most),
  so the comment is normally in the thread, read and unannounced, when
  the outbox row goes; no Sent copy, and known senders and collected
  addresses learn nothing. Permanent failures (`smtp.SendError`
  `Permanent`) are the site's 400, 403, 404, 413 and other 4xx and a body
  that converts to nothing; a refused token defers the account's queue
  with `notify.authRequired`. A message of the account is forwarded by
  e-mail from a mail account: `draft.create` `forward` with
  `messageAccountId` naming the account the message is in reads the
  original and its remote parts there and copies the parts into the mail
  account's attachment store (`core/drafts.go`).
- **Notification mail** (`core/issue_mail.go`, `jira/notification.go`;
  the threat model in [security.md §4.1](security.md#41-notification-mail-of-an-issue-tracker)).
  The site tells its users of every change by e-mail, which arrives in
  their mail accounts. When a mail syncer stores such a message (the
  `Stored` hook every syncer calls with the message id and the
  attachment policy, before the message is announced), core asks every
  enabled `jira` account whose `notificationMail` is not `ignore`, in the
  accounts' order, whether the message is the site's (every `From`
  address matches `notificationSenders`: `addr@host` or `@host`, ASCII
  case only, no display name; empty means `@<site host>` on Cloud and
  nobody on Data Center) about an issue of a selected space (the first
  key in brackets or parentheses within the subject's first 1024 bytes,
  a scanner without regular expressions). The first that agrees links
  the message to the issue (`issue_mail_links`, which also keeps the
  issue under `onlyMine`, `issues.via_mail`) and refreshes it: a message
  that arrived within 15 minutes whose issue is not stored is waited for
  (5 s at most, 6 such waits a minute), so that a hidden message
  produces no `notify.newMessage`; otherwise the refresh is triggered,
  unless the message is older than the account's window or than what
  is stored of the issue, or the issue was asked for within 10 minutes
  and the site did not give it. With `hide`, and only once the issue is
  stored in the account, the message is hidden: `messages.hidden`, a
  display filter — left out of every listing, count, thread and search,
  `message.get` still answers, nothing changes on the mail server and
  no flag is set. The links are judged again (`settleIssueMail`) when
  the account's configuration changes, it is enabled or paused, its
  first pass with a configuration ends, and hourly in `core.Maintain`;
  after a change the account's mail from its senders within its window
  is scanned for notifications stored before (200 messages a step,
  50 000 at most). A paused or removed account shows its mail again.
  Every hide or show is `notify.messagesChanged` on the mail account,
  gathered for 250 ms; `outboxAwareNotifier` drops `notify.newMessage`
  of a hidden message.
- **Local only.** Flags (the site has no read state), the copies in the
  views, the hidden marks, comment drafts, and everything the account
  stores: the site is written to by a posted comment alone. There are
  no server drafts, no move, delete, archive or junk, no `message.send`
  of mail, and `account.linked` never lists the account.
- **Setup and the fake site.** `account.detectSite` asks the address
  anonymously what it is (`/rest/api/2/serverInfo`, on Cloud also
  `/_edge/tenant_info` for the cloud id; a Data Center site that refuses
  anonymous requests is recognised by Jira's headers), normalising the
  URL (`NormaliseSiteURL`: https unless typed http for Data Center, no
  user info, a context path kept, a pasted page link cut before
  `browse`/`secure`/`rest`); `account.listSpaces` signs in and lists the
  user, the spaces (at most 1000, with an estimate of the issues in the
  window when asked) and the statuses; `account.test` probes the sign-in.
  Tests never reach the network: `jira/jiratest` is an in-memory site
  behind `httptest` in either flavour (page tokens and `startAt`, the
  gateway route, a media host that must not see the token, index lag,
  Retry-After, revoked tokens), driven by the package's tests and core's
  end-to-end ones (the MCP bridge's tests script their fake daemon
  instead); `backend/testdata/jira` holds hostile rendered HTML, bot
  comments and malformed REST pages, `testdata/mime` the
  `jira-notification-*.eml` samples.

### 3.7 The board

The board ([api.md §4.13](api.md#413-board), decided 2026-10-01, §7) sorts
the user's conversations and issues by what is owed. A **case** is one
thread of an account (§3.4; for a `jira` account an issue, §3.6) in one of
four states: `hot` (needs the user now), `you` (waits for the user's
answer), `them` (the user waits for someone else), `info` (for reading).
The daemon computes the cases from what it stores; the user's decisions
are kept beside them; an assistant may add notes through the MCP bridge.
Four layers, each of which knows only the one below:

- `internal/board` — the rules, pure: no store, no I/O, no clock
  (`Evaluate(Thread, Identity, now) Verdict`), with the quote check, the
  cleaning of annotation strings and the plain-text excerpts.
- `internal/store/board*.go` — the tables of migration 0017, the dirty
  set and its drain (`DrainBoard`), the listing, the queue, the user's
  decisions, annotations, commitments and runs.
- `internal/core/board_*.go` — the worker that drains the dirty set,
  the first evaluation of stored mail, the hourly upkeep, the identities,
  the preferences and the seventeen `board.*` methods.
- `cmd/malachi-mcp` — `list_board` for every agent and, under
  `--allow-triage`, the triage tools ([mcp.md](mcp.md), *Triage of the
  board*). The daemon itself never talks to a model.

**Data** (migration 0017). `board_cases` holds one row per thread that is
a case, `UNIQUE (account_id, thread_id)`, with an id of its own (`c_` and
32 hex digits) that outlives thread ids: a thread merge moves the row to
the surviving thread (`store/threads.go` calls `mergeBoardCaseTx`, which
reads plain columns only, so board data can never fail the mail write
that merges). Its columns are of two kinds. The **derived** ones
(`rule_state`, `rule_reason`, `rules_version`, `input_key`,
`members_key`, subject, snippet, person, date, counts, the reply and
latest message, the issue's key and status, `can_archive`) are a cache the
drain rebuilds from the thread at any time. The **user's** ones
(`user_state`, `done_at`, `remind_at` with `reminded`, and `done_seen`,
the Message-IDs the case had when it was marked done) are authoritative
and never recomputed. Two columns are used for more than their name
says, because no migration was allowed (0017 is frozen, §7): while a
remind is set (`remind_at` ahead, not yet `reminded`) `done_seen` holds
the remind's marker, a first line `~remind <stamp>` (the store's clock
when the remind was set) followed by the inbound Message-IDs the case
had then (`BoardCase.RemindSetAt`; done and remind exclude each other, so
the column is free); and `board_runs.day` of a manual or automatic run
is `lowerBound` when its token count is only a lower bound (the unique
index on `day` covers `external` runs only, whose `day` is the
**daemon's** local day, `YYYY-MM-DD`; the comment in 0017 that says the
caller's is wrong). `orphaned_at` and `member_ids` carry a case across a
move by another client (below). `input_key` is a hash over the ids and
body states of the members that count, the key an annotation is checked
against; `members_key` also covers what `board.get` shows of them;
`version` rises on every change of the row, its annotation or its linked
draft. A linked draft is local (`drafts.local`, §5 *Local drafts*): the
suggested reply is edited and sent from the board and does not reach the
server's Drafts folder while it has its case. It keeps a live case: the
drain keeps a case whose linked draft exists (`kept`), the prune keeps it
and the board lists it; a done case lists it for the done retention (30
days), after which the prune drops the link. Deleting the draft marks the
thread dirty (trigger `drafts_board_ad`). One rule,
`store.releaseLinkedDraftTx`, applies wherever a linked draft loses its
case without Send or Discard, in the transaction that drops the link: a
thread merge where both cases link an existing draft (the surviving case
keeps its own), the prune of a case whose thread was gone for
`BoardOrphanGrace`, the end of a done case's retention, and the removal
of the account with its local data kept. An edited draft
(`drafts.edited`: saved while linked — only the board's editor saves a
linked draft — or ordinary when it was linked) becomes an ordinary draft
(`local` cleared: it uploads, nothing typed is lost or left invisible);
an untouched suggestion is deleted with its attachments and never
reaches the server. Each such release is noted under
`board.unlinkedDrafts` in `meta` for the upkeep's log and upload
wake-up. The hourly upkeep deletes local drafts that were never linked
and never edited and were not saved for 6 hours
(`store.UnlinkedLocalDrafts`, `core.sweepLocalDrafts`; longer than a
triage run may stay open), and makes any edited local draft without a
case ordinary first (`store.ReleaseEditedLocalDrafts`, a safety net that
finds nothing unless a path was missed). `board.unflag` clears the flags behind `hot.flagged`: the rows are
chosen by `board.FlaggedCopies`, which merges copies exactly as the rules
do, over the thread loaded as the drain loads it
(`store.BoardCaseThread`). `board_annotations` holds one annotation per case (replaced as a
whole) with its own `input_key` and the draft it linked;
`board_commitments` the user's promises with the date of the user's
newest message when each was recorded (`replied_after`);
`board_runs` the triage runs. Annotations and commitments go with their
case (`ON DELETE CASCADE`).

**The dirty set.** `board_dirty` is a set of `(account, thread)` to
evaluate again. Triggers fill it on every write a case depends on: a
message stored, deleted (also through the cascade of a folder or account
deletion), moved, merged, flagged or read, hidden, classified as bulk
mail, given its body or a changed envelope; an issue stored, deleted, or
its key, summary, status, assignee, reporter or watching changed; an issue
item stored, deleted or changed in kind or author; and the account's Jira
user (`issues.me.<account>` in `meta`) arriving or changing, which marks
every issue of the account. An UPDATE trigger fires only when a watched
column really changed, and each trigger inserts only what is missing
instead of `INSERT OR IGNORE`, because the conflict clause of the
statement that fires a trigger overrides the trigger's own (an upsert of
`messages` would turn the IGNORE into a failure). No code may write these
tables with `INSERT OR REPLACE`, which would bypass the DELETE triggers.
Code marks threads too: a board method that changed a case
(`boardWritten`), an account whose configuration the rules read changed,
a folder role that changed (compared hourly against `meta` `board.roles`,
so a change made while the daemon was down is seen), the known
correspondents changing.

**The worker** (`core/board_worker.go`, one goroutine started with the
sync) drains the set in batches: at most 50 threads, 10 000 loaded
members or 500 ms inside one write transaction, so the syncers' writes
(whose busy timeout is 5 s) keep flowing, with a 20 ms pause between
batches. `DrainBoard` checks for dirty rows before it opens a transaction;
for each thread it loads the visible members (the newest 2000) and, for an
issue, the issue, its items and the Jira user, calls the decider and
writes the outcome under a savepoint, then removes the thread from the
set. A sync write cannot slip between the read and the removal: it waits
for the transaction and marks the thread again. A thread whose
evaluation fails is rolled back to its savepoint, logged by its ids only,
and dropped from the set; the batch goes on. The decider
(`core/board_adapter.go`) runs inside the transaction and must not call
the store: the identities are read before the batch, and the text of a
member is read lazily, only for the members the rules look at
(`Verdict.TextMembers`: none unless the newest member that is no note to
self is the user's, then the user's up to ten newest messages after the
newest inbound one, among which forwards are looked for). The own text of such a member that has HTML needs the raw
message parsed and sanitised, which must not happen inside the
transaction: the decider leaves the thread as it is and asks for it, the
worker derives it after the batch (`core/board_owntext.go`, a two-level
cache of 8 MiB) and marks the thread again; a thread that asked once is
judged with what there is the next time. `board.get` is the exception: it
derives up to eight such texts, newest first, while the caller waits
(`boardGetDerive`) and shows the stored text for the rest, which the
worker derives afterwards. The worker wakes when the
notifier sees new mail, changed messages or the end of a sync pass, when
a board method changed something, every 30 s, and when the earliest
remind comes due.

**The first evaluation** (`core/board_backfill.go`). The migration scans
nothing. `core.Maintain` marks dirty, in batches of 2000 messages by id,
the threads with a visible member dated within the longest window, with
the cursor in `meta` `board.rules` (`<rules version>:<last id>`, then
`<rules version>:done`), so an interrupted pass resumes. A stored value of
another rules version marks every case dirty and starts the pass over;
so do turning the board on and a window growing beyond the longest one
before (`restartBoardBackfill`, which a pass under way notices before it
records anything more). `board.list` says `ready: false` until the pass
is done and the set is empty.

**The rules** (`internal/board`, `RulesVersion` "6"). What counts: a
visible member (not hidden, not in a virtual folder) outside the folders
of role trash, junk and drafts that is the user's or classified as no bulk
mail (§3.1, migration 0016), and on an issue no event. **Mine** is a row
in a folder of role sent or outbox, never a `From` naming the user. Copies
of one Message-ID are one message: the user's when a copy is mine, else
represented by the copy stored first, so a later twin (another client's
move, a forged duplicate) never changes what the rules read. Members are
ordered by their arrival (`board.Arrival`: the internal date, else the
`Date` header, else the time stored, never later than the time stored or
now); an issue's by the site's time of the item, never by a date a bot's
relayed text claims. The user's addresses on an account are its own and
the ten most frequent senders of its sent folder, used only to tell
whether mail is addressed to the user; the addresses of all the user's
accounts together (`Identity.WithSelf`, read with them and kept until they
change; a change marks the stored mail of the longest window dirty) tell
a **note to self**: a message of the user's whose `To`, `Cc` and `Bcc`
are at least one and all such addresses, which counts but never decides
the state, the case's date or its subject (§4.13); an **own forward inside
a thread** (version 6; looked for only among the user's messages after
the newest inbound one, newest ten) passes the same way, so the deciding
member is the newest that is neither (`Verdict.DecidingMessageID`,
`DecidingMine`), and a commitment closes against it alone. Inbound mail from
such an address to nothing but them (`info.yourNote`); the **known correspondents** are
the addresses in `To` or `Cc` of the sent and outbox folders of every
enabled mail account (most recent first, at most 20 000), read hourly or
when the accounts change. The order of the rules, the reasons and the
known-sender rule are in the API's table: a deciding member that is inbound
goes through `hot.flagged`, `info.yourNote`, `hot.important` (known
senders), `you.repliedToYou`, `you.addressed` (known), `you.newContact`
(an unknown sender with the user in `To`: it waits for the user but is
told apart), `info.unknownSender` (unknown, the user not in `To`),
`info.ccOnly`, `info.notAddressed`; the user's own flag (`hot.flagged`)
wins whoever wrote last, so `board.unflag` is offered also when the
user's message is the newest; a deciding member that is the user's gives
`them` only by `them.replied` (an answer to someone who wrote in the thread) or
`them.asked` (no inbound member, and a question mark in the user's own
text of one of their newest ten messages that is not a forward), and a
message of the user's shaped like a forward never makes a case by itself. Words of a
message never count, except that question mark. Jira: no case while the
Jira user is unknown, for an issue in the done category or in the
account's `closedStatuses`; events never decide; the newest item the
user's → `them`, else `you` when the user is the assignee, the reporter or
wrote an item before, `info` when the user only watches. While an inbound
member waits for the bulk classification the verdict is **pending**: the
case stays as it was, and the classification marks the thread again. The
**own text** of a message of the user's (`board.OwnText`) is cut down to
what the user wrote, failing closed: for HTML mail the text of the HTML
part after the same quote trimming `message.body` applies with
`trimQuoted`, otherwise the plain text after `sanitize.TrimQuotedText`;
then every `>`-quoted line, an attribution line whose quote is not
prefixed and everything below it, and the signature go, and a text over
100 000 lines or starting with a quote has none. Every change of the
rules, or of what the store hands them, gets a new `RulesVersion`, and a
new value makes the daemon evaluate every case and the stored mail of
the longest window again (version 3: the members carry their
`References`, so a reply known only by them no longer starts its
thread; 4: the quote trimming cuts at an Outlook header block without a
separator line; 5: notes to self, and the members carry their `Bcc`; 6: an own forward in
a thread passes like a note, the user's flag is `hot` whoever wrote last,
`you.newContact`). The first start after such a change judges the whole
board again; the user's states, done, reminds, annotations and
commitments stay.

**Outcome of a verdict** (`store/board.go`). With a state, the case is
created (only when its date lies within the longest window, so the first
evaluation does not fill the board with old mail) or its derived columns
updated. With none, an existing case is **kept**, with reason `kept` and
its last rule state, while the user set a state, a remind is set (ahead,
or come due and not yet followed by done or another remind), a commitment
is open (counted after the commitments this very verdict closed, so the
reply that closes the last one lets the case go), or a current annotation
has a deadline still ahead **and the `assistant` preference is on**;
otherwise it is deleted. A thread with no visible member keeps its case **orphaned**
(off the board): a move between folders by another client deletes one
row before the other folder's sync stores it again, perhaps as a thread
of its own, which adopts the orphan through `member_ids`; after a day
(`BoardOrphanGrace`) the hourly prune deletes it.

**Visibility.** A case is `live`, `done` (`done_at`) or `snoozed` (a remind
ahead). Done reopens when an inbound member that counts was stored after
`done_at`, arrived no earlier than a day before it, and is not one of
`done_seen` (a copy another client moved is stored anew); so neither a
backfill of old mail, a forged `Date`, a move nor the user's own message
reopens it. Done clears a remind and closes the open commitments
(`closedReason: done`); a remind clears done. Whatever takes a case out
of done (an inbound member, `board.setDone` with `done: false`, which
the clients' Undo of Archive uses, a remind) opens again the commitments
that done closed (`closed_reason = 'done'`, closed at or after `done_at`;
those the user's reply closed stay closed); an inbound member that
reopens it also clears the user's state, so the rules judge it again.
The prune drops a long-done case whatever its user state, remind or
deadline. A remind that comes due
(`ClearDueBoardReminds`, from the worker's timer and the hourly upkeep;
one past while the daemon was down fires at start) makes the case live and
keeps it, past its window too, until the user marks it done or sets
another, and shows it as **reminded** (`remindedAt`, §4.13): a client
lists it first in its state until the user acts on it; there is no
notification. New mail ends a remind too: an inbound member that counts,
stored after the remind was set and not a member then (the three tests
of done: stored after, arrived no earlier than a day before, not in the
marker's list) makes the case live at once, without `remindedAt`, as the
remind did not come due. A remind set before the marker existed, or
merged from a row without it, is not woken by mail. Every action of the
user's on the case, and new inbound mail, clear `remindedAt`; with the
clock gone back so that a fired remind lies ahead again the case still
counts as reminded, not snoozed. A case is listed within the window of its state in effect (the
user's, the assistant's while it counts, else the rules'; preferences
`windows`, default 90/30/30/14 days) or while something keeps it; a done
case for 30 days. The hourly upkeep deletes cases older than the longest
window that nothing keeps and that were not done in the last 30 days.

**Annotations and commitments** (`core/board_service.go`,
`store/board_triage.go`). `board.queue` hands out the live cases of the
triage accounts without a current annotation, with the store's input key
and the plain text of the newest eight members that count. `board.annotate`
and `board.commit` carry that key back; the daemon compares it with the
case's, and `AnnotateBoardCase` computes the key again inside its own
transaction, so notes about members that changed meanwhile are refused
(`conflict`). An annotation whose key is no longer the case's is
**stale**: it counts for nothing, `board.queue` offers the case again,
and only its draft link survives. Strings are cleaned (control, bidi and
invisible characters, URLs) and capped. A deadline and a commitment carry
a verbatim quote, normalised alike on both sides (`board.QuoteIn`): a
deadline's must be in the stored plain text of a member that counts, a
commitment's in the user's own text of a member that is mine (so the
other party's words never become the user's promise), and the date must
lie between a day before and 400 days after that message arrived. A
linked draft must be a draft of the case's account replying to a member
of the case. A commitment closes by itself (`replied`) when the user
writes a message newer than any they had written when it was recorded
and that message is the deciding member (a note to self or an own forward
closes nothing); adding or changing a commitment raises the case's
`version`.
**Runs**: a client records the runs it starts (`board.runStart`, manual or
auto, and `board.runEnd` with an error class and its token `usage`, whose
`lowerBound` says a stopped, expired or grace-expired run counted too
little; `usage24h` is a lower bound when any summed run was, and the
clients write "at least"); each annotate and commit
call counts in its run as accepted or rejected, a call without a known
open run in an implicit `external` run per `source` and local day. A run
left open is ended as `failed` at the next start or by the hourly upkeep
once it is two hours old (so after two to three); runs
are kept 90 days. `board.list` reports the latest run, the cases automatic
runs annotated today (the clients' daily cap) and the size of the queue.

**Preferences** are the board's own (`meta` `board.prefs`, JSON of
`api.BoardPreferences`; not `config.get`/`config.set`): `enabled`
(default on), `assistant` (off; a client turns it on only after the user's
consent), the four windows, `triageAccounts` (empty: every enabled mail
account; a `jira` account only when named), and `autoTriage`,
`autoTriageMinutes`, `autoTriageDailyCases`, which the daemon only stores
for the client's schedule. With the board off nothing is computed, every
method but the preferences refuses, and the user's decisions are kept.

**Notifications.** `notify.boardChanged` names the accounts whose listing
changed (absent: any) and goes out at most once per second from a timer,
never from the caller's goroutine; it carries no case, a client lists
again. The worker sends it for what a batch changed, the methods for what
they wrote, the upkeep for reminds, prunes and runs.

**What a client shows** is described in [macos-port.md](macos-port.md),
`docs/windows-port.md` §11.8 and, for GTK, the sources in
`ui/internal/board`; all three clients have the board (§7). The reply of
`board.archive` carries `moved` (each message and the folder it left), from
which a client builds its Undo: `message.move` back, then `board.setDone`
with `done: false`.

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
  internal/daemon     starts, watches and stops the malachid process (§2)
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

With *Group by Conversation* on (GSettings `group-by-conversation`, off by
default) the list pane shows `thread.list` of the folder instead of
`message.list`: one row per conversation with the participants, a member
count and a fold arrow; unfolding asks `thread.get {folderId}` and lists
the folder's members as indented rows (`window/thread_model.go` is the
plain-Go model, `window/threads.go` the ListBox mirror, reconciled by key
so the scroll position survives). A single-message conversation is a
plain row. Selecting a conversation row shows its newest member and marks
only that one read; an action on it (trash, archive, junk, read/unread,
star) applies to every member in the folder, which the `message.*`
methods take as a list. Left and Right fold and unfold, Enter toggles a
conversation row and opens a member. A notified arrival is folded into
its row from `threadId`; the outbox folder is never grouped.

The search bar over the list (Ctrl+F; `window/search.go`, the plain-Go
side in `window/search_model.go`) turns the list pane into
`search.query` results while it is open: from the second character,
300 ms after typing pauses, in the scope chosen under the entry (the
selected folder, its account, or every account; GSettings `search-scope`
remembers the choice). Results are a flat list whatever the grouping; a
row names the folder, and the account when every account is searched,
and shows the excerpt with the matched words in bold (Pango attributes
from the daemon's byte ranges, never markup). The results are a snapshot:
new mail and syncs leave them alone, a change of the text, the scope or
the selected folder (in the narrower scopes) asks again. Every action
works on a result as on any row, since each summary carries its account
and folder. While the entry has the keyboard the single-key shortcuts
(a, j, s, u, Delete) are lifted from the application, which would
otherwise take the keys before the entry sees them.

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
encoded in `internal/window/collapse.go`), not in the daemon. A folder can
also be pinned to a *Favourites* section at the top of the sidebar with the
star at the right end of its row: it waits for the pointer, except on a
pinned folder's row in the tree, where the filled star is what says the
folder is pinned. The section lists pinned folders without their children,
in tree order, and gives every account a heading while it exists.
Pins are presentational in the same way (`favourite-folders`,
`internal/window/favourites.go`); the daemon knows nothing of them. The list pane shows `message.list` for the
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

The bottom of the sidebar carries the status line, a menu button with an
`adw.Spinner` and a caption computed from `sync.status` and
`notify.syncState` across the enabled accounts: "Syncing *folder*…
42 %", then sign-in required, certificate problems, sending, messages
not sent, sync error, offline, otherwise "Up to date · *time of the last
check*"; the account is named when exactly one of several is in trouble.
While the daemon is not connected the line says so instead. Its popover
lists every account with its state and action and names the daemon
(`ui/internal/window/status.go`). Ctrl+R and the refresh button send `sync.trigger` for the
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
through a `cid:` URI scheme served only for ids the window registered
(files it picked, and the backend's copies read through `attachment.get`),
and sends with `message.send`: a rich-text draft goes out as
`multipart/alternative` with the derived text and the sanitised HTML, its
inline pictures in a `multipart/related`. (`richText` in `compose/draft.go`
is the switch back to a plain-text build.) After a send the message shows
up in the local Outbox folder (visible only while non-empty) with a banner
for its delivery state; a failed send offers Retry (`outbox.retry`) and the
trash button cancels the send (`message.delete`). The status line at the
bottom of the sidebar (on macOS a bar along the bottom of the window) shows
"Sending N messages…" from `SyncState.pendingOutbox` and "N messages not
sent" from `SyncState.failedOutbox`; clicked, it opens a popover with each
account's state, last sync and action (sign in, edit, try again, check),
a link to the Outbox for unsent messages and the daemon's version
(`ui/internal/window/status.go`). The message list's header shows the
selected folder's counts as its subtitle. Reply, Reply All
and Forward ask the backend for the template (`draft.create`,
`ui/internal/window/compose_open.go`): recipients, subject and the
original quoted as sanitised HTML with its pictures copied into the
attachment store; the UI contributes only the localised line above the
quote (`compose.Attribution`) and shows what the sanitiser removed as a
toast. `compose.Prefill`, the UI's own plain-text quote, is the fallback
for a daemon that cannot answer.

Drafts on the server: every saved draft that is not local gets a copy in
the account's Drafts folder (api.md §4.5). The syncers upload it once it has rested for
30 seconds (`store.DueDraftUploads`; core arms a wake-up for the syncer,
`core/draft_sync.go`), over IMAP with `APPEND` and `\Draft`, over Graph
with `POST me/messages` and the MIME message; `core.buildDraft` writes it
like `message.send` does, plus `Bcc`, under a fresh `Message-ID` for every
upload (Gmail may drop an `APPEND` whose `Message-ID` it knows).
`store.MarkDraftSynced` then records the new copy on the draft
(`drafts.rfc_message_id`, `server_*`, migration 0012) and deletes the
previous one like a permanent delete, through an ordinary `message_ops`
entry: its local row goes at once, the server copy with the next push. A
draft's copy and a message of the Drafts folder are the same when their
`Message-ID` or server identity match (`store.DraftForMessage`);
`draft.open` returns the draft for such a message, or builds one from a
message another client left (`core/draft_open.go`, the quoter of
`draft.create` without the quote), and `replaces` lets its first save
take the message over — only when nothing was lost on the way. Sending,
`draft.delete` and a trash, move or delete of the copy delete the other
half. On Gmail a permanent delete moves the message to the Trash and
expunges it there, since an expunge elsewhere only drops a label. Known
limits: a draft whose copy another client deleted stays in the store
(invisible; the local rows may simply have left the retention window, so
their absence proves nothing), and a copy Outlook changed in place is kept
next to the new upload rather than overwritten (Graph's delta reports only
its flags). In both UIs a message of the Drafts folder opens in the
compose window (double-click, Enter, or the pane's *Edit* banner).

Local drafts (migrations 0018 `drafts.local` and 0019 `drafts.edited`, `draft_stray_copies`): a board case's suggested
reply stays in the store. `store.DueDraftUploads` and `NextDraftUpload`
skip it, so no syncer uploads it and core arms no wake-up for it; it is
set by `draft.save` on the first save only (the bridge under
`--reply-only` and `--triage-run`) and by linking the draft to a case
(`store.SetBoardDraft`, `AnnotateBoardCase` through `makeDraftLocalTx`).
A local draft never records a copy: the link hands the copy the draft
had to `strayCopyTx`, which deletes an IMAP copy that can be addressed at
once (`dropCopyTx`, same transaction) and otherwise records it in
`draft_stray_copies`. A Graph copy is always recorded there, because
Outlook edits drafts in place: the Graph syncer asks the service whether
the item changed after `synced_at` (`editedSince`, the check that keeps an
edited copy when a new version is uploaded) and deletes it only when it
did not; a changed one is forgotten and stays in the Drafts folder as the
user's own draft. IMAP has no such notion and needs none: another client
saves an edited draft as a new message under a new UID, so deleting the
old UID cannot destroy its text. An upload picked before the draft became
local ends in `MarkDraftSynced`, which then deletes the copy it made (or
records it as stray when it cannot be addressed yet), and `buildDraft`
already refuses a draft that became local after the syncer listed it.
`store.DropStrayDraftCopies` works the stray copies off: each syncer calls
it at the start of its pass (a failure is logged and the pass goes on)
and the board's upkeep hourly (without the Graph check: those wait for
their syncer); an entry it cannot resolve within a week (a row that never
arrives, a UIDVALIDITY that changed, a syncer that does not run) is
forgotten with a warning, and the copy then stays where the user sees
it. Sending a local draft is an ordinary `message.send`.

The *General* page: *Run in Background* makes the main window hide instead
of close (a hidden window keeps the application alive; `app.show` and
activation bring it back); *Launch at Login* asks the Background portal
(`internal/background`, plain D-Bus, never `~/.config/autostart`) to start
`malachi --gapplication-service`, which holds the application until the
first activation; the mark-as-read delay is a timer around `message.flag`;
deleting confirms with an `Adw.AlertDialog` before `message.delete`; new
mail arrives as `notify.newMessage` and becomes a `GNotification` whose
default action is `app.open-message` with the account and message: the
click opens the message in its own window and marks it read
(`window/notify_open.go`; the summary from the list, else `message.get`,
and the main window instead for a message the daemon no longer has). The *Mail* group is daemon-owned
(`config.get`/`config.set`): check interval, remote content, *Keep
Mail Offline For* (`offlineDays`; 1 week, 1 month, 3 months, 1 year or
everything, an arbitrary stored value snapping to the nearest row), *Keep
Attachments Offline For* (`attachmentOfflineDays`; small attachments only,
1 week, 1 month, 3 months or everything, snapping alike), *Never Store
Attachments* (`neverStoreAttachments`; while the daemon confirms it on, the
row above is insensitive, `attachmentDaysApply`, so a failed save reverts
that too) and *Compress Stored Mail* (`compressStore`), the last three
hidden for a daemon that does not report them; *Disk Space Used* shows
`system.storage` (the total, what compression saves, what is on the server
only, a conversion under way), asked again every 5 seconds while the
dialog is open.
`config.set` is read-modify-write, so every change echoes the whole
preference set the dialog last received.

An attachment kept on the mail server (`Attachment.remote`), or one of a
message whose body is still `pending`, has a server icon on its chip
(`partState` in `window/attachments.go`). Opening, previewing or saving
it, Save All, an attached message and a forward first ask the daemon for
the whole message (`message.download`, `window/download.go`; one call per
message, the chips' spinner after 400 ms), then do what was asked. A
forward downloads first whenever an attachment is remote or the message is
not fully loaded yet (the call answers at once when nothing is missing); if
the download fails it asks whether to go on without the attachments, except
when there is no daemon connection, the daemon lacks the method, or the
message is over the daemon's size cap, where it forwards straight away and
`draft.create` lists what it could not take. After a download a part is
found again by id, name and type; one that cannot be matched is reported as
gone, never replaced by another part. Under *Never Store Attachments* the
parts stay remote after the download, which the daemon holds in memory
only (§3.2), so the chip keeps its server icon and every later action asks
`message.download` again, answered at once while the daemon still holds
the message. A message whose large pictures the daemon kept on the server
(`message.body` `remotePictures`) shows a second bar under the
remote-images bar, *N pictures of this message are on the server only*
with *Download Pictures*: `message.download`, then `message.body` again
(keeping remote images the user already loaded) and a redraw; a reply
downloads first when the body on display counts such pictures (the
daemon's count, not the parts' `remote`), and a forward whenever any part
is remote. The UI never decides what is stored: it only asks, on these
clicks. The files written for opening and previewing (`openDir`) go when
the application starts and when it shuts down, whatever the preferences
(`SweepOpenedAttachments` in `main.go`; `purgeOpenDir` removes nothing but
an absolute path ending in `malachi/open`).

## 6. Platform

One core, a native UI per platform. The daemon is platform-neutral Go
and the GTK UI is Linux code: no Windows/macOS code paths, build tags or
"just in case" abstractions in either. Other platforms get their own
native UI as a separate client of the daemon's API (Swift/AppKit on macOS,
C#/WinUI 3 on Windows); the GTK UI is the template they mirror feature for
feature, and [macos-port.md](macos-port.md) and
[windows-port.md](windows-port.md) describe how they are built and kept in
step.
Portability of the *architecture* is provided by the socket boundary, not
by conditional compilation.

The macOS client is `macos/`, a SwiftPM package with three targets
([macos-port.md](macos-port.md)). `MalachiCore` has no AppKit in it: the transport
and the daemon supervision (the counterparts of `ui/internal/client` and
`ui/internal/daemon`: an actor over `NWConnection` with the same newline
framing, and an actor over `Foundation.Process` with the same locate →
probe → spawn → poll → SIGTERM sequence), the API types re-declared from
`docs/api.md`, the pure logic of the Go UI ported function for function
(the window model, threads, folding, favourites, address parsing,
quoting, the wizard's fields and results, the viewer and editor
documents) with the Go tests ported alongside, the `@MainActor`
controllers that call the daemon, the settings over `UserDefaults` with
the GSettings keys, and the gettext shim whose keys are the GTK msgids, so
`po/` translates both clients (`macos/scripts/po2strings.py` generates the
`.lproj` catalogues at build time). `MalachiMail` is the AppKit shell:
the three panes under one unified toolbar, the message list, the reader
with a WKWebView that re-establishes layer 2 of [security.md
§3.2](security.md#32-defences) (JavaScript off, the same CSP, a
`malachi-cid:` scheme handler, no network, plus a content rule list), the
compose window with the same contenteditable editor and bridge script,
the settings window, the account assistant, notifications, the login
item. `MalachiKeychain` is `malachi-keychain`, the daemon's keyring helper
over the login keychain (`MALACHI_KEYRING=helper`, §3). `make macos`
assembles `build/Malachi Mail.app` with `malachid`, `malachi-mcp` and
`malachi-keychain` inside `Contents/MacOS/`; the app hands the daemon
macOS paths for the config and the store (`~/Library/Application
Support/Malachi Mail/`), the helper as its keyring, the defaults of the
two storage preferences (compressed, attachments of 30 days kept;
`MALACHI_DEFAULT_*`, §3.1), and keeps the daemon's default socket path so
the MCP bridge needs no configuration.

The Windows client is `windows/`, a .NET solution of three projects and a
helper ([windows-port.md](windows-port.md)), ported from the macOS client
and validated against the GTK UI. `Malachi.Core` has neither WinUI nor
platform calls in it and is tested on any OS: the API types re-declared
from `docs/api.md`, the transport over an AF_UNIX socket with the same
handshake, the daemon supervision (locate → probe → spawn → poll → stop,
where the stop is a `CTRL_BREAK_EVENT` that Go takes as an interrupt and a
kill after 15 s), the pure logic of the Go UI and of the macOS client
ported with their tests, the controllers, and the presentation logic macOS
keeps in AppKit, now with tests of its own; settings with the GSettings
keys live in the registry and strings are read from `po/` at run time with
the GTK msgids as keys. `Malachi.Platform.Windows` holds the Windows
services behind Core's interfaces (the process host, the key file's owner
and DACL check, the registry, the Mark of the Web on attachments, launch
at login, the `mailto:` registration, the notification-area icon).
`Malachi.App` is the WinUI 3 shell, whose viewer, editor and previewer
re-establish layer 2 of [security.md §3.2](security.md#32-defences) for
WebView2 (script off where it can be, the same CSP, no network through a
resolver rule and a dead proxy, every request answered by the app).
`Malachi.Credentials` is `malachi-credentials.exe`, the daemon's keyring
helper over Credential Manager (`MALACHI_KEYRING=helper`, §3). `make
windows` assembles a self-contained, unpackaged folder with `malachid.exe`,
`malachi-mcp.exe` and the helper beside `MalachiMail.exe`; the app hands
the daemon `%LOCALAPPDATA%\Malachi Mail\` for the config and the store and
keeps the daemon's default socket path, whose directory it creates with a
DACL for the user alone.

What macOS and Windows cannot have follows from the daemon, not from the
clients: without GNOME Online Accounts, Gmail and Microsoft 365 sign in
through the daemon's own browser sign-in (§7; Gmail needs a Google client
of the user's own, or an app password), and recipient completion runs on
the collected addresses alone because there is no Evolution Data Server.
The deliberate deviations from the GTK UI are listed in `macos/README.md`
(one toolbar, pane folding instead of back navigation, ⌥⌘↑/↓ for
reordering, a ⌘R setting, `NSAlert` button order, the quarantine
attribute on attachments, the system new-mail sound) and in
`windows/README.md` (search in the title bar, pane folding with the title
bar's pane and back buttons, Windows keys with a Ctrl+R setting,
`ContentDialog` button order, an own attachment previewer, the Mark of the
Web, the notification-area icon, drafts saved on Quit); everything else is
meant to match, and the `.blp` files are the reference when it does not.

Distribution on Linux: Flatpak (`packaging/flatpak/`) and native packages
(`make deb` / `make rpm`). No Snap. The macOS and Windows clients are built
from source for now; their distribution (signing, notarisation or an
installer, and for Windows a licence permission for Microsoft's platform
components) is open ([macos-port.md §12](macos-port.md#12-what-the-port-took-and-what-is-still-open),
[windows-port.md §17](windows-port.md#17-before-a-public-release)).

## 7. Open decisions

- UI language: Go + gotk4 for phase 1; Rust + gtk4-rs or Python + PyGObject
  remain possible because the backend does not care. The other platforms'
  clients are settled: Swift/AppKit on macOS (2026-09-24) and C#/WinUI 3 on
  Windows (2026-09-27, below).
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
  it. *Superseded 2026-09-25 by "Own OAuth2 sign-in" below; Graph stays
  the only Microsoft path.*
- Gmail: **decided** (2026-09-06) — IMAP and SMTP with SASL XOAUTH2, the
  token from GNOME Online Accounts (`internal/auth/goa`, `internal/auth`
  `XOAuth2Client`). The objection above does not carry over: GNOME Online
  Accounts asks Google for the `https://mail.google.com/` scope and its
  `Mail` interface names `imap.gmail.com` / `smtp.gmail.com` with XOAUTH2,
  so the existing sync engine and sender do the work; and GNOME's own
  registered OAuth client is what Google sees, which is what made the CASA
  assessment moot — Malachi registers no client of its own, exactly as
  with Microsoft 365. Only a sign-in through GNOME Online Accounts is
  offered (`account.discover` answers a Google address with the hint even
  over the ISPDB's password entry, which needs an app password). Gmail's
  All Mail is listed as the archive target but never synchronised, and
  Important and Starred are not listed: they are views of messages the
  other folders already hold, and the store has no cross-folder identity
  to fold them into. Gmail's SMTP files its own Sent copy, so such an
  account skips the APPEND like a Graph one. Rejected: an own OAuth
  client (the CASA assessment), an app-password path (dead end once Google
  finishes retiring basic authentication), the Gmail REST API (nothing it
  adds over IMAP is needed). *Partly superseded 2026-09-25: the own
  sign-in and the app password below are now offered where GNOME Online
  Accounts is not; the REST API stays rejected.*
- Own OAuth2 sign-in: **decided** (2026-09-25) — the backend runs the
  authorization-code flow with PKCE itself (`source: daemon`,
  `internal/auth/oauth2flow`, `account.oauthStart` / `oauthWait` /
  `oauthCancel`) for Gmail (IMAP/SMTP with XOAUTH2, as through GNOME
  Online Accounts) and Microsoft 365 / Outlook.com (Graph, as through GNOME
  Online Accounts), so macOS and desktops without GNOME Online Accounts
  can add them. The redirect is a one-shot listener on `127.0.0.1`; the UI
  opens the browser, the backend never does; the refresh token lives in
  the keyring only (docs/security.md §6). The OAuth clients are
  configurable — `config.toml` `[oauth2.google]` / `[oauth2.microsoft]`,
  or an account's own `clientId` / `tenantId` — over a built-in table
  (`oauth2flow.builtinClients`, one Go map, no API change to fill it).
  Without a client the flow answers `oauthClientMissing`.
  Built-in clients, **decided** (2026-09-25): Microsoft ships the
  project's own Entra registration (multitenant + personal accounts,
  public client with PKCE, loopback redirect). It is not
  publisher-verified — that needs a verified organisation in Microsoft
  Partner Center and the project is run by an individual — so personal
  accounts sign in directly and organisations that restrict consent
  approve the app once through their administrator. Google ships none:
  its restricted mail scope needs an app verification with a yearly paid
  CASA assessment; Gmail uses GNOME Online Accounts, an app password, or a
  client of the user's own. Revisit both when an organisation (a foundation
  or fiscal host) can own the registrations. GNOME Online Accounts
  stays preferred on Linux: where it runs, `account.discover` answers an
  address not signed in there with its hint first and the own sign-in as
  the alternative; a sign-in it holds is used as before. A lost refresh
  token (`invalid_grant`) opens a re-sign-in by itself
  (`notify.authRequired` with `authUrl`). Not included: Microsoft over
  IMAP/SMTP with XOAUTH2, the `custom` provider (stays `notImplemented`),
  client fields in the Settings (`config.set`).
- Gmail app password: **decided** (2026-09-25) — allowed as the fallback
  `account.discover` lists last for a Google address (IMAP/SMTP with
  `authMethod: password`, `imap.gmail.com:993` / `smtp.gmail.com:465`),
  for accounts with 2-Step Verification when neither GNOME Online Accounts
  nor an own OAuth client is available. It reverses the rejection of
  2026-09-06 as an emergency path only: Google may end it with basic
  authentication, and it comes after the OAuth paths in `alternatives`.
- Internationalised e-mail domains in `account.discover`: not handled
  (IDNA encoding of the domain before the ISPDB/DNS lookups).
- Daemon lifecycle: **decided** (2026-09-07) — the UI starts `malachid`
  when nothing answers on the socket and stops it when the application
  quits (§2, `ui/internal/daemon`). Rejected: a systemd user unit (none in
  a Flatpak, and the Background portal can only autostart the application
  itself) and a second autostart entry (same reason). Open: a daemon
  orphaned by a UI crash is adopted by the next UI but not stopped by it,
  so it runs until logout; and a `--gapplication-service` instance
  without a window starts the daemon but never connects to it, so a
  mailto: composer opened that way sees no accounts until the main
  window exists.
- Message body storage: **decided**. The raw RFC 822 message is a file
  under `<data dir>/messages/<account>/<id>` (`0600` in a `0700` per-account
  directory, removed with the folder or the account; one that outlives its
  row is swept by the raw maintenance of `core.Maintain` once it is an
  hour old, §3.1); the parsed plain
  text, the curated headers and the attachment metadata live in SQLite
  (`messages.text_body` and friends). HTML is never stored separately:
  `message.body` re-parses the raw file and sanitises on demand, so a
  ruleset bump never has to migrate cached HTML. Compose attachments follow
  the same split (`<data dir>/attachments/<id>` plus metadata including
  SHA-256 in SQLite). *Amended 2026-09-27 by the two decisions below: the
  file may be one zstd frame (`<id>.zst`), and a skeleton of the message
  whose large attachments stayed on the server.*
- Compressed message store: **decided** (2026-09-27) — a raw message file
  may be one zstd frame (`<id>.zst`, §3.1) written with
  `github.com/klauspost/compress` (pure Go without cgo, no dependencies of
  its own, BSD-3-Clause), under the preference `compressStore`: on in the
  macOS app (`MALACHI_DEFAULT_COMPRESS_STORE=1`, since many Macs have
  256 GB disks), off elsewhere (a file system such as btrfs compresses by
  itself), chosen at run time, never by a build tag, and applied to
  existing stores too; a change converts the stored mail in the background,
  in both directions. Measured on a real store of 3,725 messages (three
  accounts; Apple M4, one core): the raw files were 96 % of the footprint
  (910 MiB beside a 42 MB `store.db`); zstd at `SpeedDefault` with entropy
  coding of literal-only blocks keeps 60 % of them (72 % without that
  option, which stores base64 attachments as they came) at about 450 MB/s
  compressing and 900 MB/s decompressing, for 0.3 ms more per message
  opened (34 ms more for a 25 MiB message) and 0.6 ms more CPU per message
  synchronised. Rejected: compressing inside `store.db` (the FTS index and
  the text bodies are about 4 % of the footprint, and FTS5 `detail=column`
  would break the phrase queries `internal/search` sends); SQLite page
  compression (ZIPVFS and CEROD are proprietary, and sqlite-zstd needs cgo
  and a Rust extension `modernc.org/sqlite` cannot load); the file system's
  compression (APFS does not compress ordinary writes, and anything else is
  code per platform, §6); the standard library's deflate (slower
  decompression); a zstd dictionary (it helps only messages under 32 KiB).
- Attachments on demand: **decided** (2026-09-27) — the attachments of at
  least 100 KiB (`api.LargeAttachmentMinBytes`) that the HTML does not show
  through `cid:` may stay on the mail server, under the preference
  `attachmentOfflineDays` (small ones only, N days, or all; 30 days in the
  macOS app through `MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS`, all
  elsewhere): the message is stored as a verified skeleton and made whole
  by `message.download` when the user opens, saves or forwards such a part
  (§3.2). On the same store, leaving those attachments on the server (the
  pictures the HTML shows kept) keeps 36 % of the raw files, and 19 %
  together with compression. A download comes only from the account's own
  server, read-only, on a user action or when an MCP tool asks for that
  attachment or a forward (docs/mcp.md); signed and encrypted mail is never
  reduced. Rejected: fetching single parts (IMAP `BODY[n]`) and reassembling
  the message (parsers disagree about broken MIME, so the result could not
  be verified against the original, and Graph has no per-part MIME);
  extracting attachments into a store of their own (the skeleton keeps the
  message one file that the parser, `message.part` and the quoter read as
  before); go-message's `MultipartWriter.SetBoundary` for writing the
  skeleton (it rejects boundaries its own reader accepts, so the delimiters
  are written by hand); downloading on the syncer's session (one goroutine
  that waits in IDLE on the inbox: a download would have to wait for it or
  interrupt it); keeping the remote flags inside `attachments_json` (every
  change would fire the search index's update trigger, so `remote_parts` is
  a column of its own); re-downloading in the background when the setting
  is loosened (the user's decision: an older attachment comes when it is
  opened). Not included: saving transfer on a first sync (a message is
  downloaded whole and reduced at once), messages over the 25 MiB cap
  (still `tooBig`). *Amended 2026-09-27: never store attachments.* The
  preference `neverStoreAttachments` (off, and without a default from the
  environment on any platform) leaves every attachment the HTML does not
  show on the server, whatever its size or the message's age, and the
  pictures it shows of 100 KiB and more (after a test store of 729 MiB, of
  which about 400 MiB were pictures the HTML shows and 302 MiB of them
  pictures of 100 kB and more: the user's decision), with the same
  exceptions stored whole; the messages already stored are reduced in the
  background, also those downloaded on request, and a store already in the
  mode is judged again once when the rule grows (`ingest.NeverStoreRule`).
  Such a message shows its text and small pictures at once and offers to
  download the large ones (`message.body` `remotePictures`); showing it
  never contacts the server. A download the user
  asks for is received and held in the daemon's memory, not in the store
  (256 MiB, the least recently used first, 30 minutes unused), and the
  parts are served from there (§3.2); a file the user opens or previews
  must still be a file for the viewer, so both UIs remove their directory
  for opening at every start and quit. Memory, because nothing of the
  message reaches the disk, it is gone when the daemon quits or crashes
  without anything to sweep, and it needs no code per platform (§6).
  Rejected: temporary files removed at quit (a crash leaves them until the
  next start, and until they go snapshots and backups may take them, while
  removing a file does not overwrite its blocks); a RAM disk (`hdiutil
  attach ram://` on macOS, a tmpfs mount on Linux: code per platform, a
  volume the user's other processes and the Finder see, and one a crash
  leaves in place).
- Contacts and recipient completion: **decided** (2026-09-06) — the
  system address books through Evolution Data Server over D-Bus
  (`internal/contacts/eds`: `Sources5` for the registry,
  `AddressBook10` for the books, `GetContactList` with a
  `contains x-evolution-any-field` query, read-only), plus an own table of
  addresses the user wrote to (`collected_addresses`, fed by the outbox
  worker after a delivery and once from the Sent folders, never from
  incoming `From`). On GNOME the EDS backend for Microsoft 365 already
  provides the personal contacts, the recent people (Graph `/me/people`,
  cached offline) and the organisation directory (searched live), so
  nothing of that is duplicated here; elsewhere completion runs on the
  collected addresses alone. Only the books of the sending account's own
  collection are searched (matched on the GNOME Online Accounts id, else
  on the collection's e-mail); a user with two tenants does not get them
  mixed. Rejected: Microsoft Graph directly (duplicates EDS on GNOME, and
  without GNOME Online Accounts there is no token anyway); an own CardDAV
  client (the same work EDS already does, and Microsoft 365 has no
  CardDAV); reading EDS's cache files (two on-disk formats, the directory
  is not cached at all, and a Flatpak hole into `~/.cache`). The D-Bus
  interface of EDS is formally private and versioned in its bus names;
  a bump means one constant here and one line in the Flatpak manifest.
- RPC socket authentication: **decided** (2026-09-26) — every connection
  proves, in both directions and with HMAC-SHA256 over two fresh nonces,
  knowledge of a key the daemon makes at every start and writes beside
  the socket (`<socket>.key`): protocol 2,
  [api.md §1.4](api.md#14-handshake); what it protects against and what
  not is in [security.md §8](security.md#8-local-storage). Before, the
  socket's mode was the only gate, and Go can neither set nor check the
  permissions of an AF_UNIX socket on Windows; the MCP bridge's check of
  the socket's owner and mode (`syscall.Stat_t`) did not even compile
  there. A key file works the same on every platform without build tags
  (§6, CLAUDE.md rule 4). Rejected: checking owner and mode only where
  the OS reports them (no protection on Windows); kernel peer credentials
  (`SO_PEERCRED`, `LOCAL_PEERCRED`, `SIO_AF_UNIX_GETPEERPID`: code per
  platform); named pipes on Windows (a second transport); TCP on loopback
  with a token (a socket every local user can reach). Complementary, and
  done since 2026-09-27: the Windows client creates the socket's directory
  with a protected DACL for the user and SYSTEM, and uses a key file only
  when the user owns it and its DACL lets nobody else read or change it
  ([windows-port.md §5](windows-port.md#5-transport-handshake-daemon-supervisor)).
- One daemon per store: **decided** (2026-09-27) — the daemon takes an
  exclusive lock on its store before it touches the store or the socket:
  an EXCLUSIVE SQLite transaction, never committed, on
  `<store>.daemon.lock` beside it (`store.Lock`; a store reached through a
  symbolic link has the lock of its target), which the kernel drops with
  the process however it ends. The socket check alone could be fooled: on
  Windows and macOS a flood of connections makes it fail outright, and a
  second daemon then took over a live daemon's socket, synced the same
  store and reset its outbox, which could send a message twice. SQLite is
  already the store's engine and locks files alike on every platform
  (POSIX record locks, LockFileEx), so this needs no code per platform
  (§6, CLAUDE.md rule 4). One rule comes with POSIX record locks: they
  belong to the process, and closing any descriptor of the file drops
  them, so nothing else in the daemon may open the lock file
  (`store.IsLockFile` guards `attachment.import`). Not `<store>.lock`:
  that is the directory SQLite's own dot-file locking makes on a file
  system without byte-range locks. Rejected: `flock`/`LockFileEx` called
  directly (code per platform); a PID file (a liveness check per platform,
  reused PIDs); the socket as the lock (a flood cannot be told from a dead
  daemon).
- Windows client: **decided** (2026-09-27) — C# on .NET 10 (LTS) with
  WinUI 3 on the Windows App SDK 2.5, taken as its component packages
  (the metapackage adds some 60 MB of AI libraries); Windows 11, x64 and
  ARM64; unpackaged and self-contained, per user, registering itself in
  HKCU for `mailto:`, notifications and launch at login; secrets through
  the helper keyring over Credential Manager; the UI a mirror of the GTK
  UI ported from the macOS client (windows-port.md §0). Rejected: MSIX,
  whose AppData virtualisation hides `config.toml` and the store from the
  user and breaks the registration with the Claude apps, the reason macOS
  has no App Sandbox either. Open before a public binary: code signing, an
  installer, and a GPLv3 §7 permission for the Microsoft components the
  app folder carries ([LICENSING.md](../LICENSING.md)).
- Stopping the daemon on Windows: **decided** (2026-09-27) — the client
  starts the daemon in a process group of its own and stops it with
  `CTRL_BREAK_EVENT` (the app attaching to the daemon's console when it
  has none of its own), which Go delivers as an interrupt, the path
  SIGTERM takes
  elsewhere; a kill after 15 s, as SIGKILL is on the other platforms. No
  backend change, and the GTK semantics stay (a daemon a crashed UI left
  behind is adopted, never stopped). Rejected: an authenticated
  `system.shutdown` method (the supervisors are process management and
  never speak the protocol, and any client, the MCP bridge included,
  could stop the daemon); a flag that stops the daemon at the end of its
  stdin (it would die with a crashed UI, unlike on GTK); a named event
  (Windows code in `backend/`, rule 4); a stop file (polling); a kill
  alone (no clean shutdown, and a message killed between SMTP `DATA` and
  its bookkeeping may be sent twice at the next start).
- Jira as an account kind: **decided** (2026-09-29) — a Jira site is a
  third kind of account (`jira`, §3.6) whose syncer synthesises RFC 5322
  messages from the issues (the description, each comment, each status
  or assignee change) and stores them through `internal/ingest` like
  mail, `thread_id = jira:<issue id>`; the issue tables of migration
  0015 only decorate them (`MessageSummary.issue`, `ThreadSummary.issue`,
  `Folder.virtual`). So the store, threading, search, the outbox, the
  reading pane's locked views, notifications and the MCP bridge work on
  issues without knowing the site, and a client adds a projection, not
  a second kind of list. Rejected: a composed view over a store of the
  site's own objects (a second implementation of every listing, count,
  thread, search, notification and MCP tool, and a second renderer for
  the site's HTML beside the sanitiser and the locked views); joining an
  issue with the mail that discusses it (`References` never merge into a
  `jira:` thread; a notification mail is linked to its issue instead).
  Not included: Jira Service Management queues (a folder is a whole
  space), assigning, saved JQL as folders, attachments in outgoing
  comments. *Amended 2026-09-30:* a status transition is written too
  (`issue.transitions` / `issue.transition`, capability `transition`):
  only one the site lists for the user and only when it needs no screen
  or required field (those are listed as `needsInput` and refused before
  anything is posted); the issue is refreshed afterwards, so the change
  shows as its event row. Asking for a transition's fields in the client
  is not included.
- Jira sign-in: **decided** (2026-09-29) — an API token on Cloud (HTTP
  Basic with the Atlassian account's e-mail; a scoped token through the
  API gateway `api.atlassian.com/ex/jira/<cloudId>`) and a personal
  access token on Data Center, never OAuth 2.0 (3LO). Atlassian's
  developer terms forbid a client secret in a public source tree, the
  flow has no PKCE (ECO-283), and every user of one registered app
  shares its rate-limit budget, so a built-in client of the Microsoft
  kind cannot exist, and an API token is not subject to that shared
  budget. The token is the account's password in the keyring and goes
  to its site and that gateway only ([security.md §6](security.md#6-credentials)).
  Deferred: OAuth with an organisation's own registration through
  `oauth2flow` (a fixed redirect port, since Atlassian takes the
  callback URL literally, and a client secret for a new provider).
  Known cost: an organisation may disable API tokens or shorten their
  life (a year at most since December 2024); the account then fails
  with `authRequired` and the token is replaced in the account's
  settings.
- Jira polling and reconciliation: **decided** (2026-09-29) — a pass a
  minute with a relative incremental JQL (`updated >= "-Nm"`), and an
  hourly enumeration of the ids in scope (§3.6). Jira offers no push a
  desktop can receive (webhooks need a public endpoint) and no deletion
  feed: the incremental search cannot see a deleted issue, one moved out
  of scope, or a change of watching, so the enumeration is the only way
  to find them, and it is bounded (ids only, 1000 a page, nothing
  concluded from one that stopped at its cap). Rejected: `updated` in
  absolute time (the profile's time zone shifts it); a full enumeration
  every pass (the rate limit); Retry-After ignored beyond a request (the
  syncer backs off as a whole).
- The views as copies: **decided** (2026-09-29) — the fixed views
  (assigned to me, watching, open) are folders of copied `messages` rows
  that share `remote_id`, Message-ID and `thread_id` with the space
  folder's, not a query at listing time. The store has no cross-folder
  identity (the reason Gmail's Important and Starred are not listed
  either), and a copy keeps every listing, count, flag, thread and
  notification path as it is; the multiplied storage is accepted, the
  copies are left out of account-wide `thread.get` and `search.query`,
  and a flag on one copy reaches all. Rejected: a saved search per view
  (a second listing path in every client); labels (the store's model).
- Notification mail of the site: **decided** (2026-09-29, the user's
  decision; the study advised a later phase) — a notification e-mail of
  the site in a mail account refreshes its issue at once (`sync`, the
  default) and may be hidden (`hide`, opt-in), only when its sender is
  the site's, its subject names an issue of a selected space and the
  issue is stored in the account ([security.md §4.1](security.md#41-notification-mail-of-an-issue-tracker)).
  Hiding is a display filter in the local store, never a move, a
  deletion or a flag on the mail server: the message stays where the
  user's other clients see it and is shown again when the account no
  longer covers it. Rejected: hiding by default; hiding on the sender
  alone (a forged message could hide mail, and the issue is where the
  user reads what the mail said); filing the mail on the server.
- Account uniqueness per realm: **decided** (2026-09-29) — `accounts`
  rebuilt (0015) with `realm`, the site for a `jira` account and `""`
  for mailboxes, unique on (email, realm): the user's Atlassian login is
  normally the address of their mailbox, which the inline `UNIQUE` of
  0004 refused. Rejected: a synthetic address for the account (the
  address is what a comment's `From` and the wizard show); dropping the
  constraint (two mailboxes with one address would double every
  notification); a key other than the site (an account per site is what
  a token is for).
- Data Center: **decided** (2026-09-29, the user's decision; the study
  advised Cloud only) — Data Center and Server are supported in full
  (REST v2, `startAt` paging, `expand=changelog`, wiki markup for
  comments, a context path, a site that refuses anonymous requests),
  behind one `Remote` interface both flavours implement. It is verified
  by the fake site only: no Data Center instance is available, and the
  manual tests run against a production Cloud site.
- macOS first for Jira: **decided** (2026-09-29) — the order of
  [macos-port.md §10](macos-port.md#10-adding-a-feature-keeping-the-parity)
  (backend, GTK, then the mirrors) is reversed for the Jira accounts and
  the conversation view: the pure UI logic is written first as a Go
  reference in `ui/internal/jira`, `ui/internal/capabilities` and
  `ui/internal/conversation` (tested on the Mac, which cannot build
  GTK), ported 1:1 to `MalachiCore`, and the GTK widgets and the Windows
  client follow; the msgids are in `po/` already (appended by hand,
  `make po` renumbers them), the Windows ones listed in
  `windows/parity-exclusions.txt`, and the Swift functions without a Go
  mirror are marked "Swift-first" for the port. The user's Jira lives on
  the Mac. The GTK UI followed (2026-09-30): the Go reference used as it
  is through `i18n.Tr`, the Swift-first functions mirrored in
  `ui/internal/window`, the assistant in `ui/internal/accountwizard`
  (`jira.go`, `jira_flow.go`), the settings in `ui/internal/jiraaccount`,
  the comment mode in `ui/internal/compose`. The Windows client followed
  the same day ([windows-port.md §11.7](windows-port.md#117-jira-accounts-and-the-conversation-view)),
  a port of the Swift checked against the GTK behaviour.
- Conversation view: **decided** (2026-09-29) — selecting a folded
  conversation row (two or more members in the folder; a Jira folder is
  always grouped) shows every member stacked in the reading pane as
  native cards on a timeline (at first oldest first and scrolled to the
  newest; the order of today is below): each HTML body in a locked web view of its own, sized to
  its document by a script of the app in the view's own world
  (`macos-port.md` §5), never one composed document. The sanitiser
  keeps classes, ids and `<style>` selectors, so in one document a
  message's CSS could hide, restyle or forge the headers of the others,
  and without JavaScript there is no isolation to prevent it; in a card
  the headers are native text and the body one sanitiser output. Only
  the newest member that is not an event is marked read; a card is
  cheap (a body is fetched near the viewport, at most eight web views
  live). Rejected: one document with the cards' headers in it; the
  members expanded in the list instead (the GTK model, kept for member
  rows). The GTK port (2026-09-30) measures with a script in an isolated
  world: WebKitGTK cannot switch content script off alone, so a card's
  view has the JavaScript engine on with script markup off, which the CSP
  and the sanitiser back ([security.md §3.2](security.md#32-defences));
  a snapshot of the document was rejected, since a long newsletter would
  take hundreds of megabytes to measure. The GTK pane orders the
  conversation as Jira shows an issue, opened at its top: what opened it
  (the description, or the first message) folded to its header while
  more follows, then the rest newest first (the user's decision,
  2026-09-30: oldest first scrolled to the newest left the newest cut
  off while the cards above it grew); the macOS client shows the same
  order since that day (`ConversationLayout.displayOrder`), and the
  model keeps the oldest first in both. A double click on a card's
  header (the padding above it included, its buttons not) opens that
  message as a double click on its row in the list does: in a window of
  its own, a draft in the compose window (2026-10-02, all three clients;
  `Window.openMessage`, `ActionsController.openMessage`). Open: the
  Windows port, and a card's page under a dark appearance (the document
  keeps its light background).
- Document text in the MCP bridge, and its PDF engine: **decided**
  (2026-10-01) — `get_attachment` returns the text of PDF, DOCX and XLSX
  attachments, extracted by the bridge alone
  (`cmd/malachi-mcp/internal/extract`, which Go's `internal` rule keeps
  out of the daemon and the UI) and always in a child process of the
  bridge's own executable, one per document, with a memory watchdog and
  a deadline ([mcp.md](mcp.md), [security.md](security.md)); DOCX and XLSX
  with the standard library only. PDF: PDFium (`chromium/7961`, built
  without V8 and XFA) compiled to WebAssembly and embedded by
  `github.com/klippa-app/go-pdfium` v1.19.8 (`webassembly`, MIT), run by
  `github.com/tetratelabs/wazero` v1.12.0 (Apache-2.0): pure Go without
  cgo, the same tree on every platform (§6), with PDFium's bundled
  libraries (FreeType, HarfBuzz, ICU, libjpeg-turbo, OpenJPEG,
  Little CMS, zlib, abseil and others) listed in `THIRD-PARTY-NOTICES.md`. Pinned to
  v1.19.8, the last release for Go 1.25 (`backend/go.mod` says 1.25, and
  the rpm builds with Fedora's Go). PDFium is boxed even inside the
  worker: nothing of the host file system is mounted (go-pdfium mounts
  `/` by default), its linear memory stops at 256 MiB and grows without
  copying the whole of it again and again, wazero stops it at the
  document's deadline, and every document gets a runtime and a module
  instance of its own (`pdf_engine.go`). Measured on an Apple M4: a
  worker's cold start is 0.46 s, nearly all of it compiling the 5.2 MB
  module with four threads (0.72 s with two, which is why the worker runs
  four; no compiled code is cached on disk, where a writable directory
  would be code another process could replace); then about 110 pages/s
  of LibreOffice output and 47 of Chrome's (PDFium's own page analysis),
  at 330–400 MiB resident, most of it the compile. The spike's hostile
  files (Flate and text bombs, cycles, 2 million nested arrays, broken
  cross-references; 464 in all) all ended within 4.2 s and 0.7 GiB with
  text or a refusal. `malachi-mcp` grows by 9.3–9.7 MB per target
  (6.5–7.1 MB → 15.8–16.8 MB), a universal macOS binary by about 19 MB;
  under the hardened runtime it needs
  `com.apple.security.cs.allow-unsigned-executable-memory`, since wazero
  writes the code it compiles. Rejected: the pure-Go readers
  (`ledongthuc/pdf`, `dslipak/pdf`), which on the spike's corpus gave one
  table cell or glyph per line or words glued together, no text from form
  XObjects and no AES-256 or PDF 2.0, and on its hostile files infinite
  loops, fatal stack overflows and allocations of gigabytes, where PDFium
  kept Chrome's reading order, opened every encryption with an empty
  password and recovered text from 294 of 360 damaged files; PDFium
  through cgo (a C++ parser in a native process, a C toolchain per target
  and code per platform); poppler's `pdftotext` (GPL, and a program to
  ship); wazero's interpreter (about 16 times slower). Not included:
  OCR, form fields, annotations and embedded files; a password is never
  asked for.
- The board's case is a thread: **decided** (2026-10-01) — a case is one
  thread of an account (an issue on a `jira` account), never a single
  message or a cluster the program invents: the thread is what the user
  answers, what the reading pane and the conversation view already show,
  and what threading keeps stable (§3.4). The case has an id of its own,
  carried along when threads merge and adopted by the copies of a
  conversation another client moved, so the user's decisions outlive
  thread ids. Rejected: a per-message board (a long conversation would
  be many cards waiting for one answer).
- The board's rules live in the daemon; an assistant reaches the board
  only through the bridge: **decided** (2026-10-01) — the four states are
  computed by deterministic rules over headers, structure, folder roles,
  flags and the bulk classification (§3.7), never over the words of a
  message (one question mark in the user's own text excepted), so every
  client shows the same board, it works with no model at all, and a
  sender cannot talk a message onto it. A model only refines it, through
  the MCP bridge under its own flag, with notes the daemon checks and
  cleans; the daemon never talks to a model and holds no key for one.
  The owner's rulings on the rules: `them` only for a reply to someone who
  wrote in the thread or a question the user asked, a message of the
  user's shaped like a forward never; mail from a sender the user has
  never written to is `info.unknownSender`, its `Importance` ignored (the
  first run over a copy of a real store filled `you` with automated
  mail); Jira by assignee, reporter, earlier comment or
  watching, closed issues never; default windows 90/30/30/14 days.
  Amended 2026-10-08 (rules version 6): an own forward inside a thread
  passes like a note to self, so it neither decides nor closes a
  commitment; the user's own flag makes a case `hot` whoever wrote last;
  mail from an unknown sender addressed to the user in `To` is `you` with
  its own reason `you.newContact` (labelled *New contact* in the clients,
  `Importance` still ignored), unknown senders not in `To` stay
  `info.unknownSender`.
- A remind is woken by new mail and a returned remind is marked:
  **decided** (2026-10-08, the owner) — an inbound member stored after a
  remind was set ends the snooze (a reply from the other side is worth
  more than the date the user picked), and a remind that came due sets
  `remindedAt`, so the clients list the case first in its state with a
  *Reminded* badge until the user acts. Done by schema-less means, since
  0017 is frozen and a new migration would reach the owner's live data
  before the branch is merged (the marker in `done_seen`, §3.7), at the
  price that older reminds are not woken by mail. A state chosen by the
  user keeps its pin, and *Why is this here?* then says *Your decision
  keeps it on the board*. Rejected: a notification for a returned remind
  (the board is not a mail alert).
- The user's decision wins: **decided** (2026-10-01) — a state the user
  chose beats the assistant's, which beats the rules'; done, a remind and
  an open commitment keep a case the rules would drop; done reopens only
  on a new inbound message (by when the daemon stored it, never by a
  `Date` header or a copy of a message it already had). Nothing the board
  does sends, moves or deletes mail on its own: only the user's
  `board.archive` moves a case's inbox messages, `board.unflag` clears the
  flags that made a case `hot.flagged`, and `board.discardDraft` deletes
  its linked draft.
- A suggested reply is a local draft: **decided** (2026-10-02) — a draft
  linked to a case stays in the daemon's store (`drafts.local`), is
  edited and sent from the board, and does not reach the server's Drafts
  folder (but see the release rule below), where every client of the user (a phone included) would show an
  assistant's text with no sign of where it came from. A per-draft flag,
  because the only existing mechanism (an issue tracker's drafts) is per
  account. Set both when the draft is created (the bridge under
  `--reply-only` and `--triage-run`, so not even the first upload
  happens) and when it is linked (so a draft any other path made, an
  external triage session's included, ends up the same, its copy deleted);
  a race with an upload already under way is closed in
  `MarkDraftSynced`. Lifecycle: a linked draft keeps a live case (rules,
  prune) and a done case for the done retention; sending or deleting it
  lets the rules judge the case again. A suggested reply that loses its
  case without Send or Discard (a thread merge, its conversation gone for
  a day, the done retention over, the account removed with its local
  data kept) follows one rule (2026-10-02, the owner): edited by the user
  it becomes an ordinary draft in the Drafts folder, because typed text
  is never destroyed nor left invisible; untouched it is deleted, so an
  assistant's text the user never touched never reaches the server. The
  daemon knows which by `drafts.edited`, set by a save while the draft is
  linked (only the board's editor saves a linked draft; the bridge saves
  before the link) and by linking an ordinary draft (who wrote it cannot
  be told). Rejected for this: a time since unlinking (`unlinked_at`)
  with a sweep — it leaves typed text invisible for hours and still
  deletes it. A draft that was never linked is deleted after 6 hours
  without a save (nothing would ever show it). A Microsoft 365 copy that
  Outlook changed after the upload is never deleted by a link. Rejected: keeping suggested
  replies ordinary drafts (they would appear on every device), and a
  client-side "do not upload" (the daemon uploads, so only it can
  guarantee it).
- Verbatim quotes for deadlines and commitments: **decided** (2026-10-01)
  — a model's deadline or a promise of the user's is stored only with a
  quote the daemon finds, normalised alike, in the message (for a
  commitment in the user's own text of the user's own message), and with
  a date near that message. A model cannot invent a deadline the mail
  does not state, nor a sender plant a promise in the user's name; the
  clients always show the quote beside the date. Summaries, titles and
  tasks cannot be checked this way and are shown as the assistant's
  words.
- No API keys in the app; the user's own Claude Code runs triage:
  **decided** (2026-10-01) — the macOS app triages by starting the user's
  own Claude Code CLI locked down as the assistant panel (no built-in
  tools, nothing of the user's setup, no session on disk), with the
  bridge it bundles; the app never holds or asks for a credential, and
  the usage is the user's. The triage tier (`--allow-triage`) is separate
  from `--allow-modify` and `--allow-send` and enables nothing of them,
  because the queue hands out whole conversations at once while its tools
  write only local notes; the app's run never passes the other two. It
  is offered only with the assistant's *In App (experimental)* target and
  after a consent of its own, and remains experimental until Anthropic
  confirms the terms for running Claude Code from another product
  (*The panel in the app* in [mcp.md](mcp.md)).
- Automatic triage: **decided** (2026-10-01) — off by default; when the
  user turns it on, a run starts at most every `autoTriageMinutes` (30;
  the app offers 15 minutes to 3 hours) while cases wait, backs off
  after failures up to a day, and stops for the day at
  `autoTriageDailyCases` (60; the app offers 20, 60 or 150), counted by
  the daemon per local day; an automatic run gets no `create_draft`, since
  nobody watches it (unchanged now that a suggested reply stays local:
  2026-10-02, the default of the owner's open decision). The daemon
  only stores these preferences; the schedule is the client's.
- The board on macOS first: **decided** (2026-10-01, the owner's
  instruction, an exception to the rule that a UI change goes to all three
  clients) — the macOS client led with Swift-first logic
  (`MalachiCore/Board`). The GTK port followed on 2026-10-02: its Go model
  and texts are in `ui/internal/board`, triage in `ui/internal/boardtriage`,
  and suggested replies and inline editor lifecycle in
  `ui/internal/boardreply`. The GTK widgets mirror the three styles and
  embed the shared `compose.Pane`; live Claude and manual GTK checks
  remain to be verified. The Windows client has the Board as a full port
  (see `docs/windows-port.md` §11.8). The fix round of 2026-10-08 went into
  all three clients at once (Czech state names *Hoří / Čeká na vás / Čeká na ně / Pro
  informaci*, the English ones unchanged; the *Snoozed* navigation item; Archive with Undo; keys
  ⌘/Ctrl+1 and 2, E, D, R; the two-stage Escape; the board's own
  preferences). It is written, not yet verified, on GTK and Windows (see CLAUDE.md).
