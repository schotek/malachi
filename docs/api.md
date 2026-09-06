# Malachi Mail RPC API

This document is the contract between the backend daemon (`malachid`) and any
user interface. The Go types in `backend/pkg/api/` are the machine-readable
form of the same contract; **both change together, in one commit.**

Protocol version: **1** (`api.ProtocolVersion`). Bump on any incompatible
change and describe the change in the changelog section at the end.

## 1. Transport

| Aspect | Value |
|---|---|
| Protocol | JSON-RPC 2.0 |
| Socket | `$XDG_RUNTIME_DIR/malachi/rpc.sock` (mode 0600, directory 0700) |
| Fallback without `XDG_RUNTIME_DIR` | `$XDG_CACHE_HOME/malachi/run/rpc.sock` |
| Override | `malachid --socket PATH`; UI honours `MALACHI_SOCKET` |
| Framing | newline-delimited JSON: one JSON object per line, terminated by `\n`. No `Content-Length` header. |
| Max message size | 32 MiB per line (server side) |
| Direction | bidirectional on one connection: client → server *requests*, server → client *responses* and *notifications* |
| Parameters | always by name (a JSON object), never positional |
| Concurrency | the server may process requests from one connection concurrently and answer out of order; match by `id` |
| Multiple clients | allowed; notifications are broadcast to all |

Authentication: none beyond filesystem permissions. The socket is only
reachable by the owning user. Inside Flatpak both processes share the sandbox
runtime dir.

Startup: the daemon replaces a stale socket file left by a crash after
checking that nothing answers on it. If another daemon is alive it exits with
an error rather than stealing the socket.

### 1.1 Request

```json
{"jsonrpc":"2.0","id":7,"method":"message.list","params":{"accountId":"acc_1","folderId":"f_inbox","page":{"limit":50}}}
```

`id` may be a number or string; it is echoed back unchanged. A request
without `id` is a client notification; the contract defines none, the
server ignores them.

### 1.2 Response

```json
{"jsonrpc":"2.0","id":7,"result":{"messages":[…],"page":{"nextCursor":"…","total":1234}}}
{"jsonrpc":"2.0","id":7,"error":{"code":1100,"message":"account acc_1 not found"}}
```

Exactly one of `result` / `error` is present.

### 1.3 Notification (server → client)

```json
{"jsonrpc":"2.0","method":"notify.newMessage","params":{"accountId":"acc_1","folderId":"f_inbox","message":{…}}}
```

No `id`; the client must not reply.

## 2. Errors

`error.code` is an integer from a fixed enumeration (`api.ErrorCode`).
`error.message` is human-readable and **not** stable; do not match on it.
`error.data` is optional, method-specific structured detail.

| Code | Name | Meaning |
|---|---|---|
| -32700 | parseError | line was not valid JSON |
| -32600 | invalidRequest | missing `jsonrpc`/`method`, wrong version |
| -32601 | methodNotFound | |
| -32602 | invalidParams | params did not decode into the method's type |
| -32603 | internalError | unexpected failure; details are logged, not returned |
| 1000 | notImplemented | method is a stub |
| 1001 | invalidArgument | params decoded but are semantically wrong |
| 1002 | conflict | optimistic-concurrency failure (`draft.save`, `message.send`) |
| 1003 | cancelled | request cancelled by shutdown |
| 1004 | unavailable | daemon busy / shutting down; retry later |
| 1100 | accountNotFound | |
| 1101 | folderNotFound | |
| 1102 | messageNotFound | |
| 1103 | threadNotFound | |
| 1104 | draftNotFound | |
| 1105 | attachmentNotFound | unknown id, another account's, or already bound to a different draft |
| 1200 | authRequired | user interaction needed; a `notify.authRequired` was/will be sent |
| 1201 | authFailed | server rejected credentials |
| 1202 | keyringError | secret service unavailable |
| 1300 | offline | daemon is in offline mode |
| 1301 | networkError | connection failed |
| 1302 | serverError | IMAP/SMTP server returned an error |
| 1303 | tlsError | certificate or handshake problem |
| 1304 | serverTimeout | |
| 1400 | storageError | SQLite failure |
| 1401 | migrationFailed | store schema could not be upgraded |
| 1500 | malformedMessage | MIME unparsable even leniently |
| 1501 | sanitizeFailed | sanitiser refused the body; **body is withheld**, never returned raw |
| 1502 | attachmentTooBig | over a documented limit; `data` = `{ "limit": bytes, "size": bytes }` |
| 1503 | partNotFound | `message.part` named a part the message does not have, or its content is no longer stored |

Codes are never renumbered; new ones are appended within their group.

## 3. Common types

Identifiers are opaque strings. In particular `messageId` is a local store ID,
**not** the RFC 5322 `Message-ID` header (which is attacker-controlled and not
unique).

```jsonc
Page      { "cursor": "opaque", "limit": 50 }        // limit 0 = default 50, max 500
PageInfo  { "nextCursor": "opaque", "total": 1234 }  // nextCursor absent on last page; total -1 if unknown
Address   { "name": "Alice", "address": "alice@example.org" }
Flag      "seen" | "answered" | "flagged" | "draft" | "deleted" | "junk" | "forwarded"
SortOrder "dateDesc" (default) | "dateAsc"
MessageFilter "all" (default) | "unread" | "flagged"
Time      RFC 3339 string, UTC
```

### MessageSummary

```jsonc
{
  "id": "m_123", "accountId": "acc_1", "folderId": "f_inbox", "threadId": "t_9",
  "from": [Address], "to": [Address],
  "subject": "…", "date": "2026-09-02T10:00:00Z",
  "snippet": "plain text, ≤ ~200 chars, derived by the backend",
  "flags": ["seen"], "hasAttachments": false, "size": 4321,
  "outbox": OutboxInfo (opt)
}
```

`outbox` is present only for a message in the account's outbox folder
(role `outbox`, §4.2) and describes its delivery:

```jsonc
OutboxInfo { "state": "queued|sending|sent|failed", "attempts": 1,
             "nextAttemptAt": Time (opt), "error": Error (opt) }
```

- `queued`: waiting for the next attempt (`nextAttemptAt` set after a
  transient failure, absent when due now); `sending`: an SMTP session is
  running; `sent`: delivered, the copy in the Sent folder is pending;
  `failed`: a permanent failure, `outbox.retry` re-queues it.
- `error`: the last failure (a network, server, TLS, timeout or auth code
  from §2), absent before the first attempt and after a success.

### Message (message.get)

`MessageSummary` plus `cc`, `bcc`, `replyTo`, `rfcMessageId`, `inReplyTo`,
`references`, `attachments: [Attachment]`, and `headers`, a curated
map of a few interesting headers (`List-Unsubscribe`, `Auto-Submitted`, …).
The raw header block is never returned.

```jsonc
Attachment { "partId": "2.1", "filename": "safe-name.pdf", "contentType": "application/pdf",
             "size": 12345, "inline": false, "contentId": "…" }
```

Filenames are sanitised by the backend (no path separators, no control or
bidi-control characters, length-capped).

### SyncState

```jsonc
{ "accountId": "acc_1", "status": "idle|syncing|offline|authRequired|error|disabled",
  "folderId": "f_inbox", "progress": 42, "lastSync": Time, "error": Error, "pendingOutbox": 0 }
```

- `status`: `idle` (connected or between passes, no work), `syncing` (a pass
  is running; `folderId`/`progress` describe it), `offline` (the last attempt
  failed for a network reason; `error` set; retrying with backoff),
  `authRequired` (missing or refused credentials; a `notify.authRequired`
  was sent; nothing is retried until the account is updated), `error`
  (server or storage error; `error` set), `disabled` (paused, or no syncer).
- `folderId`: only while `syncing` a specific folder.
- `progress`: 0–100 within the current pass, -1 otherwise.
- `lastSync`: end of the last *successful* pass; absent before the first.
- `error`: the last failure, cleared by the next successful pass.
- `pendingOutbox`: outbox messages in state `queued` or `sending` (§4.3
  `message.send`). Sending is not a `status`: it runs beside the account's
  sync, and a `failed` message does not count.

## 4. Methods

Every method lists its `params` and `result` shapes. Fields marked *opt* may
be omitted. All methods that touch data take `accountId`.

### 4.0 system

#### `system.info`
Health check and version negotiation. The first call a UI makes.

- params: `{}`
- result: `{ "version": "0.1.0", "protocolVersion": 1, "pid": 4242, "storePath": "/home/u/.local/share/malachi/store.db" }`

### 4.1 account

Accounts live in the daemon's store and are managed only through these
methods. `[[accounts]]` entries in `config.toml` are bootstrap defaults: at
start the daemon imports each one whose e-mail is not in the store and has
not been imported before (so an account removed through `account.remove`
stays removed); the daemon never writes `config.toml`. `account.list`
returns accounts in display order: the order the user arranged with
`account.reorder`, creation order until then. Every mutation is followed by
`notify.accountsChanged`.

#### `account.list`
- params: `{}`
- result: `{ "accounts": [Account] }`

`state` is `disabled` for a paused account; otherwise the live `SyncState`
of the account's syncer (§3), `idle` with `progress: -1` and no `lastSync`
before the first pass. Identical to `sync.status`.

```jsonc
Account { "id": "acc_1", "config": AccountConfig, "enabled": true, "state": SyncState }
AccountConfig {
  "name": "Work", "email": "me@example.org", "displayName": "Me" (opt),
  "kind": "imap|graph" (opt, default imap),
  "imap": ServerConfig (imap only), "smtp": ServerConfig (imap only),
  "oauth2": OAuth2Config (opt, imap only),
  "graph": GraphConfig (graph only),
  "syncIntervalSeconds": 300 (opt)
}
ServerConfig { "host": "imap.example.org", "port": 993, "security": "tls|starttls|none",
               "username": "me@example.org", "authMethod": "password|oauth2" }
OAuth2Config { "source": "goa" (opt), "goaAccountId": "account_1788683507_0" (with source goa),
               "provider": "google|office365|custom", "clientId" (opt), "tenantId" (opt),
               "authUrl" (opt), "tokenUrl" (opt), "scopes": [] (opt) }
GraphConfig  { "source": "goa", "goaAccountId": "account_1788512854_0" }
```

`kind` selects the protocol behind the account. `imap` (the default when
absent) is a classic mailbox: IMAP for reading, SMTP for sending. `graph`
is a Microsoft 365 / Outlook.com mailbox accessed through the Microsoft
Graph API; it has no servers of its own, only a token source. With
`source: "goa"` the sign-in belongs to GNOME Online Accounts: the daemon
asks it for access tokens (`goaAccountId` is the GOA account id) and holds
them in memory only; refresh tokens never reach Malachi. A Graph account
sends and receives through Graph alone — no IMAP or SMTP is involved.

An `imap` account whose endpoints use `authMethod: "oauth2"` signs in with
an access token through SASL XOAUTH2 (OAUTHBEARER when that is all the
server offers). With `oauth2.source: "goa"` the token comes from GNOME
Online Accounts exactly as for a Graph account, and `provider` says whose
account it is — today `google`: Gmail and Google Workspace, with the
servers GNOME Online Accounts names (`account.linked` and
`account.discover` build the whole config). Without `source` the `oauth2`
block describes the backend's own authorisation flow, which is reserved
for desktops without GNOME Online Accounts and reports `notImplemented`.

Secrets are **never** part of `AccountConfig` and never returned.
`credentials.password` is write-only: it goes to the keyring and is never
logged, echoed or stored in the SQLite store. A `graph` account takes no
credentials at all.

#### `account.add`
- params: `{ "config": AccountConfig, "credentials": { "password": "…" (opt) } }`
- result: `{ "accountId": "acc_2" }`
- errors: invalidArgument, keyringError, conflict (same e-mail already configured, case-insensitive)

Validation (all failures are invalidArgument; free-text fields are trimmed):
- `name` required, `displayName` optional; both valid UTF-8, no CR/LF/NUL,
  at most 256 bytes;
- `email` a bare, syntactically valid address (no display name);
- `kind` absent, `imap` or `graph`;
- for `imap`: `imap` and `smtp` required, `graph` absent; `host` an IP
  literal or hostname of DNS labels (≤ 253 bytes), `port` 1–65535,
  `security` one of `tls|starttls|none` where `none` is accepted only for
  `localhost` or a loopback IP, `username` required (≤ 256 bytes, no
  control characters), `authMethod` one of `password|oauth2`;
- for `imap`: `oauth2` present exactly when an endpoint uses `oauth2`.
  With `source: "goa"`: `provider` `google`, a `goaAccountId` (letters,
  digits and `_`, ≤ 128 bytes), both endpoints `oauth2`, and none of
  `clientId`, `tenantId`, `authUrl`, `tokenUrl`, `scopes`. Without
  `source`: `provider` `office365|custom`, `custom` needs `https`
  `authUrl` and `tokenUrl`, at most 32 scopes without whitespace, no
  `goaAccountId`;
- for `graph`: `graph` required with `source: "goa"` and a `goaAccountId`
  (letters, digits and `_`, ≤ 128 bytes); `imap`, `smtp` and `oauth2`
  absent;
- `syncIntervalSeconds` 0 or ≥ 60;
- `credentials.password` only when an endpoint uses `password`.

The password is optional (an account without one ends in `authRequired`
once syncing exists). It is written to the system keyring
(`org.freedesktop.secrets`) and discarded; if the keyring refuses it nothing
is kept and `keyringError` is returned. That happens when no Secret Service
is running, when the user dismisses the unlock prompt, or when the daemon
runs with `MALACHI_KEYRING=none`. For `oauth2` no credentials are passed.
A `graph` account, and an `oauth2` account with `source: "goa"`, need no
keyring: adding them works with `MALACHI_KEYRING=none`, and a sign-in that
GNOME Online Accounts has lost surfaces as `authRequired` until the user
signs in again in the desktop's account settings. An `oauth2` account
without `source` would start the backend's own flow here; it is not
implemented.

#### `account.remove`
- params: `{ "accountId", "deleteLocalData": bool }`
- result: `{}`
- errors: invalidArgument, accountNotFound, storageError

The mail cache (folders, messages, raw files, operation log) is always
removed with the account; the syncer is stopped first.
`deleteLocalData: true` also deletes the account's drafts and attachments
(rows and files); `false` keeps them, orphaned, until a later phase defines
what happens to local data of a removed account. Keyring secrets are
deleted best-effort: an unavailable keyring never keeps the account alive.

#### `account.setEnabled`
- params: `{ "accountId", "enabled": bool }`
- result: `{}`
- errors: invalidArgument, accountNotFound, storageError

Pauses (`false`) or resumes (`true`) an account. A paused account keeps its
configuration and local data, is never synchronised and reports
`state.status = "disabled"`.

#### `account.reorder`
- params: `{ "accountIds": ["acc_2", "acc_1"] }`
- result: `{}`
- errors: invalidArgument (a duplicate id, or more ids than there are
  accounts), accountNotFound, storageError

Sets the display order of `account.list`, which is the order a UI shows
accounts in (its sidebar, its account pickers). The listed accounts take the
head in the given order; accounts left out keep their relative order behind
them, so a client that has not seen a just-added account does not move it.
An empty list is accepted and changes nothing. Nothing about the accounts
themselves changes — this is not an account update — but the reorder is
persistent and is followed by `notify.accountsChanged`.

#### `account.update`
- params: `{ "accountId", "config": AccountConfig, "credentials": { "password": "…" (opt) } }`
- result: `{}`
- errors: invalidArgument (same rules as `account.add`), accountNotFound,
  conflict (another account already uses the e-mail), keyringError,
  storageError

Replaces the whole configuration; `enabled` is not touched. An empty
password keeps the stored one; a given password replaces it in the
keyring, and if the keyring refuses, the configuration is reverted so the
row and the keyring never disagree. Emits `notify.accountsChanged`.

#### `account.discover`
Suggests server settings for an address. Nothing is stored and nothing is
authenticated; the UI still asks for the password and should run
`account.test`.

- params: `{ "email": "me@example.org" }`
- result: `{ "config": AccountConfig (opt), "source": "goa|ispdb|autoconfig|srv|provider|guess|none", "providerName": "…" (opt) }`
- errors: invalidArgument (not a bare address)

Sources, from most to least trustworthy, each consulted only for what the
previous ones left open:

0. `goa`: the address is signed in through GNOME Online Accounts.
   `config` is complete and passes `account.add` as is, without a
   password: a `graph` account for Microsoft 365 (`providerName`
   `Microsoft 365`), an `imap` account with `oauth2` endpoints and
   `source: "goa"` for Google (`providerName` `Google`), with the servers
   GNOME Online Accounts names.
1. `ispdb`: Mozilla's autoconfig database at
   `https://autoconfig.thunderbird.net/v1.1/<domain>`. Only the domain is
   sent.
2. `autoconfig`: the provider's own document at
   `https://autoconfig.<domain>/mail/config-v1.1.xml` and
   `https://<domain>/.well-known/autoconfig/mail/config-v1.1.xml`. These
   receive the address, as the provider already knows it.
3. `srv`: RFC 6186 / RFC 8314 DNS records `_imaps`, `_imap`,
   `_submissions`, `_submission` (`_tcp`). The domain goes to the resolver.
4. `provider`: the domain is hosted by a provider whose sign-in belongs
   to GNOME Online Accounts. Microsoft 365 is recognised by an MX record
   under `mail.protection.outlook.com` or by Microsoft hosts in an
   autoconfig answer; Google by the `gmail.com` / `googlemail.com`
   domains, an MX record under `google.com`, or Google hosts in an
   autoconfig answer — and for Google this answer wins even over a
   password entry in the ISPDB, which would need an app password.
   `config` is then the account **without** `goaAccountId` (a `graph`
   account, or an `imap` one with `oauth2` endpoints): it does not pass
   `account.add`; the UI must have the user add the account in GNOME
   Online Accounts first and re-run discovery (or use `account.linked`).
5. `guess`: `imap.`/`mail.<domain>` on 993 (TLS) and 143 (STARTTLS),
   `smtp.`/`mail.<domain>` on 587 (STARTTLS) and 465 (TLS), verified by
   opening the connection under the transport policy without logging in.

When the two endpoints come from different sources, `source` reports the
weaker one. Autoconfig documents are capped at 256 KiB, parsed strictly,
plaintext socket types and OAuth2-only entries are skipped (for Microsoft
and Google hosts they turn into the `provider` answer), hosts and ports are
validated, and `%EMAILADDRESS%`/`%EMAILLOCALPART%`/`%EMAILDOMAIN%` are
substituted. An `imap` `config`, when present, passes `account.add`
validation with `authMethod: "password"` and the username prefilled (the
address unless the document says otherwise); `name` is the provider's
display name or the domain. `providerName` is display-only text from the
document. Whole lookup ≤ 20 s; internationalised domains are not handled
yet and yield `none`.

#### `account.test`
Connectivity test without persisting anything. Validates like `account.add`
(the same `invalidArgument` cases, including a password for an account
without a `password` endpoint), then probes the endpoints of the account
kind: `imap` and `smtp` concurrently, or the `graph` mailbox.

- params: same as `account.add`, plus `"accountId"` (opt): with it and an
  empty `credentials.password`, the stored password of that account is used
- result: `{ "imap": EndpointTestResult (imap), "smtp": EndpointTestResult (imap), "graph": EndpointTestResult (graph) }`
- errors: invalidArgument; with `accountId` and `password` endpoints:
  accountNotFound, authRequired (no stored password), keyringError. Each
  endpoint reports its own outcome

```jsonc
EndpointTestResult { "ok": true, "error": Error (opt), "capabilities": ["IDLE","CONDSTORE"] (opt), "latencyMs": 120 }
```

An IMAP/SMTP probe dials, secures the connection (TLS 1.2+, system trust
store, no override; STARTTLS is mandatory when configured), authenticates
with the password and disconnects. Per-endpoint `error.code` is one of
`authFailed` (credentials rejected), `tlsError` (certificate, handshake,
STARTTLS not offered, or the server demanding TLS before login),
`networkError` (unresolvable, refused, connection dropped), `serverTimeout`
(no answer in time), `serverError` (protocol error or no usable
authentication mechanism — for an `oauth2` endpoint, no XOAUTH2 and no
OAUTHBEARER). For `oauth2` endpoints with `source: "goa"` the access
token stands in for the password; a token problem is both endpoints'
outcome before anything is dialled: `authRequired` (sign in again in GNOME
Online Accounts), `unavailable` (no session bus or no GNOME Online
Accounts). An `oauth2` block without `source` reports `notImplemented`.
`capabilities` are the server's post-login IMAP CAPABILITY
list or the EHLO keywords the backend knows about, scrubbed to printable
ASCII, ≤ 64 entries; `latencyMs` is dial → ready (greeting read, STARTTLS
done). Budget: 10 s to connect, 20 s per endpoint. The password is used
for the connections only and never logged.

A Graph probe fetches a token from the account's source and opens the
mailbox. `error.code` is `authRequired` when the source has no valid
sign-in (sign in again in GNOME Online Accounts), `unavailable` when there
is no session bus or no GNOME Online Accounts, `invalidArgument` when the
signed-in mailbox is not `config.email`, otherwise the network/server
codes above; `capabilities` is `["graph"]`.

#### `account.linked`
Lists accounts other desktop services are signed in to and that Malachi
can use: the Microsoft 365 and Google accounts of GNOME Online Accounts
with mail enabled. Nothing is stored; a UI shows them as one-click choices
in the add-account flow and passes `config` to `account.add`.

- params: `{}`
- result: `{ "accounts": [LinkedAccount] }`
- errors: serverError (GNOME Online Accounts answered with an error).
  Without a session bus or GNOME Online Accounts the list is empty, not an
  error.

```jsonc
LinkedAccount { "provider": "microsoft365|google", "email": "me@contoso.com", "name": "Me" (opt),
                "goaAccountId": "account_1788512854_0", "configured": false, "attentionNeeded": false,
                "config": AccountConfig }
```

`config` is the account to add, complete and needing no credentials: a
`graph` account for `microsoft365`, an `imap` account with `oauth2`
endpoints and `source: "goa"` for `google`, with the servers and user
names GNOME Online Accounts reports (a Google account whose mail is
switched off there, or that names no IMAP/SMTP servers, is not listed).
`name` and `displayName` may be replaced before `account.add`.
`configured` says a Malachi account with that address exists already.
`attentionNeeded` mirrors GNOME Online Accounts: the service wants the
user to sign in again; adding the account still works, syncing will report
`authRequired` until then. `name` and `email` are untrusted text from the
service; `email` is always a bare valid address.

### 4.2 folder

#### `folder.list`
- params: `{ "accountId", "includeUnsubscribed": bool (opt) }`
- result: `{ "folders": [Folder] }`

```jsonc
Folder { "id": "f_1", "accountId", "parentId" (opt), "name": "Inbox", "path": "Inbox",
         "role": "none|inbox|sent|drafts|trash|junk|archive|all|outbox",
         "subscribed": true, "selectable": true, "unread": 3, "total": 120 }
```

Folder lists are not paginated: even large accounts have at most a few
thousand folders. `outbox` is a local pseudo-folder holding queued
messages: it exists once the first message was sent from the account, is
never synchronised with the server, has an empty server path and counts
every queued, sending, sent-pending and failed message in `total` (`unread`
is always 0). Clients typically show it only while `total` is non-zero.

- errors: invalidArgument (no `accountId`), accountNotFound, storageError

The list is what the daemon learned from the server's last `LIST` (with
special-use attributes when offered, name heuristics otherwise); `unread`
and `total` are counted from the local store, so they cover only messages
within the `offlineDays` window and lag the server by at most one sync.
Without `includeUnsubscribed`, unsubscribed folders are omitted except role
folders, which are always listed. Order: role folders first (inbox, drafts,
sent, archive, junk, trash, outbox), then the rest by `path`. Before the first
successful sync the list is empty (not an error).

#### `folder.subscribe`
- params: `{ "accountId", "folderId", "subscribed": bool }`
- result: `{}`
- errors: notImplemented

Not implemented yet: the sync engine synchronises every selectable folder
regardless of subscription, and a local-only flag would be overwritten by
the next `LIST`. Use `includeUnsubscribed` meanwhile.

### 4.3 message

#### `message.list`
- params: `{ "accountId", "folderId", "page": Page, "sort": SortOrder (opt),
  "filter": MessageFilter (opt), "unreadOnly": bool (opt, deprecated) }`
- result: `{ "messages": [MessageSummary], "page": PageInfo }`

- errors: invalidArgument (missing ids, unknown `sort`, unknown `filter`,
  bad cursor), accountNotFound, folderNotFound (unknown or another account's
  folder), storageError

`filter` narrows the listing to messages without the `seen` flag (`unread`)
or with the `flagged` flag (`flagged`). `unreadOnly` is the older spelling of
`filter: "unread"`; it is honoured only while `filter` is absent, so a client
written against either version works. The two filters overlap: a message can
be both read and flagged.

Cursor stability: a cursor encodes a (sort key, id) position and stays valid
across syncs; new messages inserted before the position are simply not seen
by an in-progress pagination. A cursor is bound to the `sort` it was issued
for. It does **not** encode `filter`, so a client that changes the filter must
start again from the first page rather than reuse the cursor it holds.
Clients refresh from the start on `notify.newMessage`. `page.total` is
the folder's local count after `filter`. Only messages within
the `offlineDays` window exist locally; `threadId` is empty until threading
exists.

#### `message.get`
- params: `{ "accountId", "messageId" }`
- result: `{ "message": Message }`
- errors: invalidArgument, accountNotFound, messageNotFound, storageError

`attachments` come from the server's `BODYSTRUCTURE` at header sync and are
refined from the parsed MIME once the body is downloaded; `headers` is the
curated subset parsed from the body (empty before it is downloaded).

#### `message.body`
**The only method that returns message content, and it returns only
sanitised content.**

- params: `{ "accountId", "messageId", "remoteContent": "block" | "allow" (opt, per-call override) }`
- result:

```jsonc
{
  "messageId": "m_123",
  "bodyState": "fetched",                // fetched | pending | tooBig | failed
  "hasHtml": true,
  "html": "<p>…sanitised…</p>",          // absent/empty when hasHtml is false or htmlWithheld
  "htmlWithheld": false,                 // (opt) true: the HTML part could not be shown safely
  "text": "plain text alternative, or text derived from html",
  "blocked": { "remoteImages": 3, "remoteStyles": 1, "remoteFonts": 0, "scripts": 1,
               "forms": 0, "eventHandlers": 2, "dangerousUrls": 0, "embeddedFrames": 0,
               "trackingPixels": 1 },
  "links": [ { "text": "Click here", "href": "https://real.destination/…" } ],
  "inlineParts": { "image001@…": "2.1" },
  "remoteContent": "block",              // the policy that was applied
  "sanitizerVersion": "1"
}
```

`html` is a fragment for the webview's `<body>`, not a document. It is
produced on demand from the raw message (no HTML is ever stored), so a
ruleset change takes effect at once and `sanitizerVersion` identifies the
rules that produced it.

Guarantees of `html` (enforced in `backend/internal/sanitize`, see
`docs/security.md`):

- no `<script>`, `<iframe>`, `<object>`, `<embed>`, `<form>` and controls,
  `<meta>`, `<link>`, `<base>`, `<svg>`, `<math>`, media elements; unknown
  elements are unwrapped, their text kept;
- no event-handler attributes; no `javascript:`, `vbscript:`, `data:` or
  unknown-scheme URLs, including obfuscated spellings;
- under `block`: no reference to any remote resource (images, CSS, fonts,
  media); each is counted in `blocked`. Under `allow`: `https:` images are
  fetched **by the daemon** (no cookies, no referrer, image bytes only,
  capped at 2 MiB each, 8 MiB and 32 images per message, 10 s in total) and
  inlined as `data:` URIs, so the webview never touches the network; an
  image that could not be fetched is dropped and counted as blocked. A
  tracking pixel (at most 2 px wide or high, or hidden) is never fetched
  under any policy and is counted in `trackingPixels`;
- CSS in `style=""` and `<style>` filtered through a property allow-list:
  no `url()`, `expression()`, `@import`, `@font-face`, `position: fixed` or
  `absolute`, hidden text, `content:`; `<style>` elements are hoisted to
  the top of the fragment; a `<body>`'s own colours and style move to a
  wrapping `<div class="malachi-body">`;
- links restricted to `http(s):` and `mailto:`, with `target` removed and
  `rel="noopener noreferrer"` added; the real target is listed in `links`;
- `cid:` references rewritten to
  `malachi-cid:<accountId>/<messageId>/<partId>`, only for parts the
  message has (`inlineParts` lists the surviving ones); the webview serves
  them through `message.part`;
- caps on input, output, nesting depth, node count, attribute count and
  CSS rules; sanitising the output again changes nothing.

There is **no** parameter, flag, environment variable or debug method that
returns the original HTML.

`remoteContent` in the **result** is the policy that was actually applied,
after the stored preference, the per-call override and the known-senders
list were resolved: `block` or `allow`, never `knownSenders`. A client
offers to load the images only under `block`. Under `allow` everything
loadable was loaded already, and what `blocked.remoteImages` still counts
are the references the sanitiser removes whatever the policy (CSS `url()`,
`srcset`, `background` attributes, plain `http:`) plus any download that
failed: asking again would change nothing.

`htmlWithheld` is set, with `html` empty and `text` still served, when the
message has an HTML part that cannot be shown: the sanitiser refused it (a
cap breach) or the raw message could not be read again. It is a state of
the result, not an error: the caller shows `text`. `sanitizeFailed` as an
error belongs to `draft.save`, where there is no text to fall back on.

`bodyState` says whether content exists at all: `pending` (the sync engine
has not downloaded the body yet; `text` empty), `tooBig` (over the daemon's
raw-message cap, never downloaded), `failed` (downloaded but unparsable),
`fetched`. Bodies are downloaded for every message within the `offlineDays`
window; there is no on-demand fetch.

Which policy applies: when `remoteContent` is omitted the stored preference
from `config.get` is used (`block` by default; `knownSenders` resolves to
`allow` only when every sender address of the message is on the `sender.list`
allow-list, otherwise `block`). Passing `remoteContent: "allow"` or `"block"`
overrides the preference for this one call and is not remembered;
`"knownSenders"` is not accepted per call (invalidArgument). Decrypted
content is always `block`, whatever the policy (see `docs/security.md` §5).

- errors: invalidArgument (bad `remoteContent`, missing ids), accountNotFound,
  messageNotFound, storageError. A refused or unreadable HTML part is not an
  error (`htmlWithheld`).

A call under `allow` may take several seconds while the daemon fetches the
images; a client should allow for that (30 s is a reasonable timeout) rather
than use its usual short one.

#### `message.part`
- params: `{ "accountId", "messageId", "partId" }`
- result: `{ "partId", "contentType", "filename", "size", "data" }` (`data`
  is base64; `filename` is sanitised as in `Attachment`)
- errors: invalidArgument (missing ids, `partId` not a part number such as
  `2` or `1.2`), accountNotFound, messageNotFound, partNotFound (no such
  leaf part — a multipart container cannot be fetched — or the message's
  content is no longer stored), attachmentTooBig (`data` would exceed
  `api.MaxAttachmentDataBytes`; `error.data` = `{ "limit": bytes }`),
  malformedMessage, storageError

The decoded content of one MIME part of a received message, addressed by
the `partId` that `Attachment` and `inlineParts` carry and that a
`malachi-cid:` URL in `html` ends with. The webview's scheme handler uses it
for inline images (and serves only `image/*` other than SVG from it);
saving an attachment will use it too. The part is read from the raw message
each time; nothing is cached.

#### `message.embedded`
- params: `{ "accountId", "messageId", "partId", "remoteContent": "block" | "allow" (opt) }`
- result: `{ "partId", "message": Message, "body": MessageBodyResult }`
- errors: invalidArgument (missing ids, `partId` not a part number, bad
  `remoteContent`, the part is not an attached message), accountNotFound,
  messageNotFound, partNotFound (as in `message.part`), attachmentTooBig
  (the part exceeds `api.MaxAttachmentDataBytes`), malformedMessage (the
  containing message or the attached one cannot be parsed), storageError

An attached message — a `message/rfc822` part, or a part named `*.eml`
(the file a mail client writes when a message is dragged into a new one) —
rendered as a message of its own, read-only. The part's bytes are read from
the containing message's raw file, parsed by the same parser and sanitised
by the same ruleset as any body; nothing about it is stored, and the parser
never recurses: an `.eml` inside the attached message stays a named
attachment. Nothing is parsed until a client asks.

`message` is what `message.get` would report for it: `from`, `to`, `cc`,
`subject`, `date`, `attachments`, `headers`. Its `id`, `accountId` and
`folderId` are those of the containing message (the attached one has no id
of its own) and `flags` is empty. `body` is what `message.body` would
return, with `bodyState` always `fetched`; every guarantee of `html` above
holds. Two differences follow from the part having no address of its own:
its `cid:` pictures cannot be served by URL, so the ones the HTML
references are inlined as `data:` URIs under the caps that apply to fetched
remote images (2 MiB each, 8 MiB and 32 images per message; the type is
sniffed from the bytes, never taken from the header) and left out of
`attachments` (`inlineParts` stays empty); and the attachments listed carry
no `partId`, since `message.part` serves the containing message's parts
only — a client shows them by name and cannot fetch them.

`remoteContent` resolves as for `message.body`, for the senders of the
*containing* message: the attached message's own `From` is forwarded
content, chosen by whoever attached it, and does not decide about loading
images. Under `allow` the call may take several seconds, as `message.body`.

#### `message.flag`
- params: `{ "accountId", "messageIds": [..], "set": [Flag] (opt), "clear": [Flag] (opt) }`
- result: `{}`
- errors: invalidArgument (empty `messageIds`, more than 1000, unknown flag,
  `deleted` in either list (use `message.delete`), a flag in both lists,
  nothing to change), accountNotFound, messageNotFound (any unknown id:
  nothing is changed), storageError

Local-first: the flags are updated in the store atomically for all ids
(all-or-nothing per call), an operation-log entry is queued and the syncer
pushes it; the result does not wait for the server. `folder.list` counters
reflect the change at once. A server-side conflict is resolved server-wins
on the next sync, after the queued change has been pushed.

#### `message.move`
- params: `{ "accountId", "messageIds": [..], "targetFolderId" }`
- result: `{}`
- errors: invalidArgument (ids as above, target not selectable),
  accountNotFound, folderNotFound (unknown target), messageNotFound,
  storageError

Local-first as above. Ids already in the target folder are ignored. The
moved message keeps its `id` (it is a local id, not the IMAP UID).

#### `message.delete`
- params: `{ "accountId", "messageIds": [..], "permanent": bool (opt) }`
- result: `{}`
- errors: invalidArgument, accountNotFound, folderNotFound (no folder with
  role `trash` while `permanent` is false), messageNotFound, storageError

With `permanent: false` (default) messages not already in the Trash role
folder are moved there (same rules as `message.move`); messages already in
Trash, or any message with `permanent: true`, are removed from the store at
once and expunged on the server by the syncer. All-or-nothing per call.

#### `message.send`
Builds the message from a saved draft and queues it into the outbox.

- params: `{ "accountId", "draftId", "version": 3 }`
- result: `{ "outboxId": "m_7" }` — the id of the queued message
- errors: accountNotFound, draftNotFound, conflict (version mismatch),
  invalidArgument (no recipients, or an invalid recipient address),
  attachmentTooBig (built message over `api.MaxOutgoingMessageBytes`,
  36 MiB; `data` = `{ "limit", "size" }`), storageError

The result only confirms enqueueing. Delivery is asynchronous and runs
beside the IMAP sync: the queued message is an ordinary message in the
account's outbox folder (§4.2) with `flags: ["seen"]` and an `outbox`
field (§3) that carries its state; `notify.syncState` is emitted whenever
`pendingOutbox` changes. A failed send stays in the outbox with `state:
"failed"` and the reason in `outbox.error`; it is never silently dropped.
Sending from a disabled account only queues; delivery starts when the
account is enabled.

Recipients (`to` + `cc` + `bcc`) must be non-empty and valid; an empty
subject or body is allowed. The message is built from the *stored* draft
at call time and the draft is removed together with the call: its
attachments move with the outbox message. A plain-text draft goes out as
`text/plain` (UTF-8, quoted-printable); a rich-text one as
`multipart/alternative` with the derived text first and the stored
(sanitised) HTML second, the HTML wrapped in a `multipart/related` together
with the draft's `inline` attachments (`Content-ID`, `Content-Disposition:
inline`) when it has any; files add a `multipart/mixed` around the whole
body (base64, sanitised file names). The outbox copy's `attachments` carry
the part numbers of that tree, so `message.part` works on sent mail too.
Headers: `From` is
always the account's `displayName <email>`, `To` and `Cc` from the draft,
never `Bcc` (Bcc recipients exist only in the SMTP envelope), `Subject`,
`Date`, a generated `Message-ID` under the account's domain, `MIME-Version`,
`User-Agent`, and `In-Reply-To`/`References` when the draft's `inReplyTo`
names a stored message. Every header value is stripped of control
characters before it is written.

Delivery: one SMTP session per attempt through the account's `smtp`
endpoint with the stored password. Transient failures (network, TLS,
timeouts, 4xx replies) are retried with backoff from 1 minute up to 4 hours;
a 5xx reply after MAIL, RCPT or DATA, a message over the server's `SIZE`
limit, or a server without a usable AUTH mechanism is permanent
(`failed`). A refused password or a missing keyring secret defers every
queued message of the account and sends `notify.authRequired`; editing the
account (`account.update`) retries at once. After a successful delivery the
recipients are recorded as known senders with source `sent` (§4.9) and,
when the account has a folder with role `sent`, the message is uploaded
there with `\Seen` by the IMAP syncer and that folder is synchronised;
until then `outbox.state` is `sent`. Without a Sent folder the local copy
is dropped after delivery (servers such as Gmail or Office 365 file the
copy themselves).

Outbox messages: `message.flag` and `message.move` reject them with
invalidArgument; `message.delete` cancels the send and removes the message
permanently whatever `permanent` says (no Trash), and returns conflict while
the message is `sending`. `message.get` and `message.body` work as for any
message.

#### `outbox.retry`
Re-queues an outbox message for an immediate attempt.

- params: `{ "accountId", "messageId" }`
- result: `{}`
- errors: invalidArgument (message already delivered, `state: "sent"`),
  accountNotFound, messageNotFound (not an outbox message of this account),
  conflict (message is `sending`), storageError

Works for `failed` and `queued` messages alike (a queued message waiting for
its backoff is attempted at once).

### 4.4 thread

#### `thread.list`
- params: `{ "accountId", "folderId", "page": Page, "sort": SortOrder (opt) }`
- result: `{ "threads": [ThreadSummary], "page": PageInfo }`

```jsonc
ThreadSummary { "id": "t_9", "accountId", "subject": "normalised (Re:/Fwd: stripped)",
                "participants": [Address], "messageCount": 4, "unreadCount": 1,
                "latestDate": Time, "snippet": "…", "flags": ["flagged"],
                "hasAttachments": true, "folderIds": ["f_inbox","f_sent"] }
```

A thread is listed in a folder if at least one member is in that folder.

#### `thread.get`
- params: `{ "accountId", "threadId" }`
- result: `{ "thread": ThreadSummary, "messages": [MessageSummary] }` (chronological)

### 4.5 draft

Drafts are local until sent; they are not synchronised to the IMAP Drafts
folder in this phase. The backend owns every derived field: it sanitises
`htmlBody` on the way **in**, derives `textBody` from it, assigns attachment
metadata and sets `updatedAt`.

```jsonc
Draft { "id": "d_1" (absent on first save), "accountId", "version": 1,
        "to": [Address], "cc": [Address] (opt), "bcc": [Address] (opt),
        "subject": "…",
        "textBody": "plain text",
        "htmlBody": "<p>…</p>" (opt; rich text),
        "inReplyTo": "m_123" (opt, local id), "forwarding": "m_124" (opt),
        "attachments": [DraftAttachment] (opt),
        "updatedAt": Time }
DraftAttachment { "id": "att_…", "filename": "safe-name.pdf", "contentType": "application/pdf",
                  "size": 12345, "inline": false, "contentId": "…@malachi.local" (opt) }
```

Bodies:

- `htmlBody` empty → plain-text message; `textBody` is stored as typed
  (CRLF normalised to LF).
- `htmlBody` non-empty → multipart/alternative. The value sent is the
  editor's HTML and is treated as hostile (pasted web content). It passes
  through the same sanitiser as incoming mail, in compose mode: scripts,
  forms, event handlers, frames, CSS outside the allow-list, every remote
  reference and every `data:` URL are removed (`block` policy, no per-call
  override); `<img src>` survives only as `cid:<contentId>` of an
  attachment listed in `attachments` with `inline: true`. `textBody` in
  params is **ignored**; the backend derives the plain-text alternative
  from the sanitised HTML. Only the sanitiser's output is stored, returned
  by `draft.list` and sent.
- Limits (`api.MaxDraft*`): `textBody` and `htmlBody` ≤ 1 MiB each,
  `subject` ≤ 1024 bytes, ≤ 500 recipients, ≤ 100 attachments, attachments
  ≤ 25 MiB in total. Subject and address names must not contain CR, LF or
  NUL; all strings must be valid UTF-8.

The UI editor keeps its own live copy of the HTML; the backend's copy is
the one that is sent. `draft.save` therefore echoes what it stored
(`htmlBody`, `textBody`) and what it removed (`blocked`) so the UI can be
honest about removals. Reopening a draft always yields the sanitised form.

#### `draft.save`
- params: `{ "draft": Draft }`
- result: `{ "draftId": "d_1", "version": 2, "textBody": "…", "htmlBody": "…" (opt),
             "blocked": BlockedContent, "attachments": [DraftAttachment] (opt) }`
- errors: invalidArgument (limits, bad address, CR/LF in header fields,
  both `inReplyTo` and `forwarding`), conflict (stored version ≠ supplied
  version), draftNotFound (`id` given but unknown), attachmentNotFound
  (listed attachment unknown, of another account, or bound to another
  draft), attachmentTooBig (sum over 25 MiB), sanitizeFailed (nothing is
  stored), storageError

Optimistic concurrency: with `id` absent the draft is created and
`version` is ignored (result `version` = 1). With `id` present the supplied
`version` must equal the stored one; the result is `version + 1`.

Attachments: only `attachments[].id` is read. Listed attachments become
bound to this draft in the given order; attachments previously bound but no
longer listed are released (kept for 24 h by the orphan sweep, see §4.10).
An `inline` attachment whose `contentId` is not referenced from the
*sanitised* `htmlBody` is released as well: deleting the picture from the
body drops it. A plain-text draft cannot keep inline attachments.

A `draft.save` whose `htmlBody` the sanitiser refuses (a cap breach) fails
with sanitizeFailed and leaves the draft unchanged; unlike `message.body`
there is no text to fall back on, because the text alternative is derived
from the sanitised HTML.

#### `draft.list`
- params: `{ "accountId", "page": Page }`
- result: `{ "drafts": [Draft], "page": PageInfo }` (newest `updatedAt` first; full bodies)

#### `draft.delete`
- params: `{ "accountId", "draftId" }`
- result: `{}` (deleting an unknown draft is not an error). Bound
  attachments are deleted with it.

#### `draft.create`
Returns an **unsaved** template (`id` empty, `version` 0) with everything a
compose window needs pre-filled by the backend: for `reply`/`replyAll` the
recipients computed from `Reply-To`/`From`/`To`/`CC` minus the account's
own addresses, a `Re:` subject, the original quoted in both `htmlBody`
(sanitised, `<blockquote type="cite">`) and `textBody` (`> ` prefixed) and
`inReplyTo` set; for `forward` a `Fwd:` subject, the quoted body,
`forwarding` set and the original's attachments imported (unbound, swept
after 24 h if never saved); for `new` with `mailto` the parsed URI. Nothing
is persisted. Reply and forward logic lives here so that every UI behaves
the same.

- params: `{ "accountId", "mode": "new" | "reply" | "replyAll" | "forward",
             "messageId" (opt; required unless mode is new), "mailto": "mailto:…" (opt, new only) }`
- result: `{ "draft": Draft }`
- errors: invalidArgument, messageNotFound, sanitizeFailed, notImplemented
  (until the message store exists)

### 4.6 search

#### `search.query`
- params: `{ "accountId" (opt, empty = all accounts), "folderId" (opt), "query": "…", "page": Page }`
- result: `{ "results": [SearchResult], "page": PageInfo }`

```jsonc
SearchResult { "message": MessageSummary, "snippet": "plain text excerpt",
               "ranges": [ { "start": 12, "end": 18 } ], "score": 0.83 }
```

Query syntax (parsed by the backend, compiled to parameterised FTS5):
free words (AND), `"quoted phrase"`, `from:`, `to:`, `subject:`,
`has:attachment`, `is:unread`, `is:flagged`, `before:YYYY-MM-DD`,
`after:YYYY-MM-DD`, `in:<folder path>`. Unknown prefixes are treated as
plain words. Snippets are plain text with byte ranges; never HTML.

### 4.7 sync

#### `sync.status`
- params: `{ "accountId" (opt) }`
- result: `{ "accounts": [SyncState] }`
- errors: accountNotFound (a given `accountId` must exist), storageError

Empty `accountId` = every account in `account.list` order, paused ones
included with `status: "disabled"`. Identical to `Account.state`.

#### `sync.trigger`
- params: `{ "accountId" (opt), "folderId" (opt), "full": bool (opt) }`
- result: `{}` (returns immediately; progress via `notify.syncState`)
- errors: invalidArgument (`folderId` without `accountId`), accountNotFound,
  folderNotFound

A paused account is skipped silently, so "sync everything" never fails
because one account is paused. Triggers coalesce: a trigger during a running
pass schedules one more pass, not several. `full: true` ignores the
per-folder change detection so every selectable folder is walked and its
flags re-read; it does not discard local data (only a server-side
UIDVALIDITY change does). Queued local operations are pushed first.

### 4.8 config

Daemon-owned preferences: options that affect mail handling and therefore
belong to the backend, not to the UI's own settings store. Precedence of
values: set through `config.set` (persisted in the store), else
`config.toml` (`[sync] interval_seconds`), else the built-in default.

```jsonc
Preferences {
  "syncIntervalSeconds": 300,   // 0 = manual sync only; otherwise >= 60
  "remoteContent": "block" | "knownSenders" | "allow",
  "offlineDays": 30             // 0 = keep everything; otherwise 1..3650
}
```

`offlineDays` bounds the local mail cache: headers *and* bodies of messages
whose server date is within the last N days are synchronised; older messages
are not stored locally at all (they stay on the server and disappear from
`message.list` as they age out). Shrinking the window prunes on the next
pass, growing it backfills silently (no `notify.newMessage`). Changing it,
or the interval, wakes every syncer. Precedence: `config.set`, else
`config.toml` `[sync] offline_days`, else 30. Because `config.set` is
read-modify-write, a client must echo the value it got from `config.get`.

#### `config.get`
- params: `{}`
- result: `{ "preferences": Preferences }`

#### `config.set`
- params: `{ "preferences": Preferences }` (the whole set; read-modify-write)
- result: `{ "preferences": Preferences }` (effective values)
- errors: invalidArgument (interval below 60 and not 0, unknown policy),
  storageError

### 4.9 sender

The known-senders allow-list behind the `knownSenders` remote-content
policy. Entries are added automatically for recipients of mail the user
sends (`"source": "sent"`) and explicitly by the user (`"source": "user"`).
The list is **never** populated from incoming `From` headers, which are
attacker-controlled; matching is on the bare address, case-insensitively,
and a display name never counts.

```jsonc
KnownSender { "address": "alice@example.org", "source": "sent" | "user", "addedAt": "2026-09-02T…Z" }
```

#### `sender.list`
- params: `{}`
- result: `{ "senders": [KnownSender] }`

#### `sender.add`
- params: `{ "address": "alice@example.org" }` (a `Name <addr>` mailbox is accepted; only the address is stored)
- result: `{}`
- errors: invalidArgument (unparsable address), storageError

#### `sender.remove`
- params: `{ "address" }`
- result: `{}` (removing an unknown address is not an error)

### 4.10 attachment

The compose-side attachment store. Files are copied into
`<data dir>/attachments/<id>` (0600 in a 0700 directory, next to
`store.db`) at import; metadata lives in the store. An imported attachment
belongs to an account, not yet to a draft; `draft.save` binds it. Unbound
attachments older than 24 h are deleted by a sweep at daemon start and
hourly, so an import that never made it into a save does not leak disk.

#### `attachment.import`
- params: `{ "accountId", "path": "/abs/file" (opt), "data": base64 (opt),
             "filename": "…" (opt), "inline": bool (opt) }` — exactly one of `path` / `data`
- result: `{ "attachment": DraftAttachment }`
- errors: invalidArgument (relative path, not a regular file, empty file,
  neither or both of `path`/`data`, `data` without `filename`, `inline`
  with a non-image type), attachmentTooBig (file over 25 MiB; `data` over
  16 MiB), storageError

`path` comes from the FileChooser portal; the daemon shares the sandbox and
reads it directly. Symlinks are followed; the target must be a regular file
(directories, FIFOs and devices are rejected without blocking). The file is
copied immediately, so the portal grant may lapse afterwards. `data` is for
clipboard or drag content that has no path (standard base64 on the wire).
`contentType` is detected from the content and only falls back to the
extension when sniffing is inconclusive; the client cannot set it.
`filename` is sanitised like received attachment names (`docs/security.md`
§4) and defaults to the basename of `path`. With `inline: true` (images
only) a `contentId` is assigned; the HTML must reference the image exactly
as `<img src="cid:<contentId>">`.

#### `attachment.remove`
- params: `{ "accountId", "attachmentId" }`
- result: `{}` (removing an unknown id is not an error). If the attachment
  was bound to a draft it disappears from that draft; the draft's `version`
  is not changed.

### 4.11 contact

Recipient completion for the compose window. Suggestions come from two
sources, merged and ranked by the backend: addresses the user has written
to (`"source": "sent"` — collected from outgoing mail after delivery, To,
Cc and Bcc alike, and once from the Sent folders already in the store;
**never** from incoming `From` headers, which are attacker-controlled), and
the system address books of the sending account (`"source": "addressBook"`,
read from Evolution Data Server over D-Bus; on Microsoft 365 that includes
the organisation directory and recent people). `name` and `book` are
untrusted display text.

```jsonc
Contact { "name": "Alice Example" (opt), "address": "alice@example.org",
          "source": "sent" | "addressBook", "book": "Contacts" (opt) }
```

#### `contact.search`
- params: `{ "accountId", "query", "limit": 10 (opt, max 50) }`
- result: `{ "contacts": [Contact] }` — best match first; the same address
  from both sources is one entry, reported as the address book's
- errors: invalidArgument (empty query, over 256 bytes, control
  characters), accountNotFound, storageError

Ranking: a whole-address match, then an address prefix, a word of the name,
anywhere in the name, anywhere in the address; a collected address is
lifted by how often and how recently it was written to. The address books
searched are those of the account's own collection in Evolution Data
Server — matched on the GNOME Online Accounts id of a Microsoft 365
account, else on the collection's e-mail identity. An account with no such
collection, a desktop without a session bus or without Evolution Data
Server, and an address book that fails or times out all leave the
address-book part simply empty, never an error. A query is at least one
character; clients wait for two before asking.

## 5. Notifications

| Method | params |
|---|---|
| `notify.newMessage` | `{ "accountId", "folderId", "message": MessageSummary }` |
| `notify.syncState` | `{ "state": SyncState }` |
| `notify.authRequired` | `{ "accountId", "reason": 1200\|1201\|1202, "message": "…", "authUrl": "https://…" (opt) }` |
| `notify.accountsChanged` | `{}` |

`notify.accountsChanged` is sent after `account.add`, `account.remove`,
`account.setEnabled` and `account.update` to every client, including the
caller; it carries no payload and clients re-run `account.list`.

`notify.newMessage` is sent once per message that arrives *after* a folder's
initial synchronisation finished, and only once its body is stored (the
`message` is a complete `MessageSummary` with `snippet`). The first download
of an account, and a backfill after `offlineDays` grew, never produce it;
clients refresh from `folder.list`/`message.list` when `notify.syncState`
leaves `syncing` instead. Folders with role `sent`, `drafts`, `trash`,
`junk` and `outbox` never produce it either (a copy of the user's own sent
message is not new mail).

`notify.syncState` is sent immediately on every change of `status`,
`folderId`, `error`, `lastSync` or `pendingOutbox`, and for progress-only
changes at most every 500 ms per account (the last value is always
delivered). Clients must not assume every intermediate `progress` value.

`notify.authRequired` with `authUrl` means an OAuth2 flow is waiting. The UI
opens the URL through the OpenURI portal; the backend's loopback listener
completes the flow and follows up with `notify.syncState`.

Notifications are best-effort: a slow client that cannot keep up may miss
some. Clients must be able to resynchronise their view via `sync.status`,
`folder.list` and `message.list`.

## 6. Versioning rules

- Adding an optional request field, a response field, an error code, a
  notification, or a method: compatible; keep `protocolVersion`.
- Removing or renaming anything, changing a type, making a field required:
  incompatible; bump `protocolVersion`, update the client, document below.
- Servers ignore unknown request fields; clients ignore unknown response
  fields.
- `protocolVersion` has nothing to do with the application's release
  version (`system.info` reports both, and `docs/releasing.md` explains
  which is which). The two ship together in one Flatpak, so a mismatch
  means someone is running a daemon left over from an older install; the
  client compares `protocolVersion`, never the release version.

## 7. Changelog

- **1** (2026-09-02): initial contract. All methods except `system.info` are
  stubs returning `notImplemented`.
- **1** (2026-09-02, compatible addition): `config.get`, `config.set`,
  `sender.list`, `sender.add`, `sender.remove`; new stored remote-content
  policy value `knownSenders`; `message.body` `remoteContent` is now an
  optional per-call override of the stored preference.
- **1** (2026-09-02, compatible addition, compose): `Draft.htmlBody`
  (sanitised on the way in; `textBody` derived by the backend when set),
  `DraftAttachment.inline`/`contentId`, `draft.save` result now echoes
  `textBody`, `htmlBody`, `blocked`, `attachments`; new `draft.delete`,
  `draft.create` (stub), `attachment.import`, `attachment.remove`; new
  error code 1105 `attachmentNotFound`; limits `api.MaxDraft*` /
  `api.MaxAttachment*` documented in §4.5 and §4.10.
- **1** (2026-09-03, compatible addition, accounts): account registry
  implemented (`account.list`, `account.add`, `account.remove`); new
  `account.setEnabled`; new `notify.accountsChanged`; validation rules and
  the `config.toml` bootstrap import documented in §4.1.
- **1** (2026-09-03, compatible addition, add-account wizard): keyring
  implemented over `org.freedesktop.secrets` (`account.add` stores the
  password); `account.test` implemented with per-endpoint outcomes; new
  `account.discover` (ISPDB, provider autoconfig, DNS SRV, verified
  guesses).
- **1** (2026-09-04, compatible addition, account editing): new
  `account.update`; `account.test` accepts `accountId` to reuse the stored
  password.
- **1** (2026-09-04, compatible addition, IMAP reading): `folder.list`,
  `message.list`, `message.get`, `message.body` (text only while the
  sanitiser is a stub; new `bodyState` field), `message.flag`,
  `message.move`, `message.delete` (local-first with an operation log),
  `sync.status`, `sync.trigger` implemented; `notify.newMessage` and
  `notify.syncState` emitted with the rules in §5; new preference
  `offlineDays`; `SyncState` field semantics and live `account.list` state
  documented. `folder.subscribe`, `thread.*`, `search.query` and
  `message.send` remain `notImplemented`.
- **1** (2026-09-04, compatible addition, sending): `message.send`
  implemented (text/plain phase; outbox folder with role `outbox`, retry
  with backoff, Sent copy via IMAP); new `outbox.retry`; new optional
  `MessageSummary.outbox` (`OutboxInfo`); `SyncState.pendingOutbox` is
  live; `notify.newMessage` is no longer sent for sent/drafts/trash/junk/
  outbox folders; new limit `api.MaxOutgoingMessageBytes`.
  `folder.subscribe`, `thread.*` and `search.query` remain
  `notImplemented`.
- **1** (2026-09-04, compatible addition, Microsoft Graph accounts): new
  `AccountConfig.kind` (`imap`, the default, or `graph`) and
  `AccountConfig.graph` (`GraphConfig`, token source `goa` = GNOME Online
  Accounts); `imap`/`smtp` are now optional and absent for `graph`
  accounts (readers must not assume them); `account.test` result gained
  `graph` and its `imap`/`smtp` entries are present only for `imap`
  accounts; new `account.linked`; `account.discover` sources `goa` and
  `provider`. Graph accounts synchronise through delta queries (polled:
  the inbox every minute, every folder at the sync interval), push local
  changes through the Graph API and send through `sendMail`, which files
  the Sent copy itself; the same `SyncState`, notifications, outbox and
  retention rules apply.
- **1** (2026-09-04, compatible addition, account ordering): new
  `account.reorder`; `account.list` now returns accounts in the order the
  user arranged (creation order until they do), which the preferences
  dialog sets by dragging rows.
- **1** (2026-09-05, compatible addition, applied remote-content policy):
  `message.body` result gained `remoteContent`, the policy that was applied
  (`block` or `allow`). Without it a client could not tell "blocked, ask
  again to load" from "loaded, and these references can never be loaded",
  and offered a button that changed nothing.
- **1** (2026-09-05, compatible addition, HTML rendering): the sanitiser is
  implemented (`sanitizerVersion` `"1"`), so `message.body` now returns
  `html`, `blocked`, `links` and `inlineParts` for messages with an HTML
  part; new response field `htmlWithheld`; `cid:` references are rewritten
  to `malachi-cid:<accountId>/<messageId>/<partId>`; under `allow` the
  daemon fetches and inlines remote images instead of leaving `https:`
  references in place; new `message.part`; new error code 1503
  `partNotFound`. `draft.save` accepts `htmlBody`.
- **1** (2026-09-05, compatible addition, attached messages): new
  `message.embedded` renders a `message/rfc822` (or `*.eml`) part as a
  read-only message of its own — headers and sanitised body, its `cid:`
  pictures inlined as `data:` URIs, its attachments listed without
  `partId`.
- **1** (2026-09-06, compatible addition, message-list filter): `message.list`
  gained `filter` (`all` | `unread` | `flagged`), which the client's
  segmented switch above the list uses; `page.total` counts the folder
  after it. `unreadOnly` is deprecated in favour of `filter: "unread"` and
  is honoured only while `filter` is absent. Cursors do not encode the
  filter, so changing it means starting from the first page.
- **1** (2026-09-06, compatible addition, recipient completion): new
  `contact.search` over addresses the user wrote to (collected after each
  delivery and once from the Sent folders; never from incoming `From`) and
  the system address books of the sending account, read from Evolution
  Data Server; the address-book part is empty, not an error, wherever the
  service is missing.
- **1** (2026-09-06, compatible addition, Gmail): `oauth2` endpoints are
  implemented for tokens from GNOME Online Accounts — `OAuth2Config`
  gained `source: "goa"` and `goaAccountId`, `provider` the value
  `google`; the daemon signs in to IMAP and SMTP with SASL XOAUTH2.
  `account.linked` lists Google accounts (`provider: "google"`) and
  carries the ready `config` for every provider; `account.discover`
  answers a Google address with `goa` or the `provider` hint, never a
  password entry; `account.test` probes `oauth2` endpoints instead of
  reporting `notImplemented`. The backend's own OAuth2 flow (an `oauth2`
  block without `source`) stays reserved.
