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
Time      RFC 3339 string, UTC
```

### MessageSummary

```jsonc
{
  "id": "m_123", "accountId": "acc_1", "folderId": "f_inbox", "threadId": "t_9",
  "from": [Address], "to": [Address],
  "subject": "…", "date": "2026-09-02T10:00:00Z",
  "snippet": "plain text, ≤ ~200 chars, derived by the backend",
  "flags": ["seen"], "hasAttachments": false, "size": 4321
}
```

### Message (message.get)

`MessageSummary` plus `cc`, `bcc`, `replyTo`, `rfcMessageId`, `inReplyTo`,
`references`, `attachments: [Attachment]`, and `headers`, a curated
map of a few interesting headers (`List-Unsubscribe`, `Auto-Submitted`, …).
The raw header block is never returned.

```jsonc
Attachment { "partId": "2.1", "filename": "safe-name.pdf", "contentType": "application/pdf",
             "size": 12345, "inline": false, "contentId": "…" }
```

Filenames are sanitised by the backend (no path separators, no control
characters, length-capped).

### SyncState

```jsonc
{ "accountId": "acc_1", "status": "idle|syncing|offline|authRequired|error|disabled",
  "folderId": "f_inbox", "progress": 42, "lastSync": Time, "error": Error, "pendingOutbox": 0 }
```

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
returns accounts in creation order. Every mutation is followed by
`notify.accountsChanged`.

#### `account.list`
- params: `{}`
- result: `{ "accounts": [Account] }`

`state` is `disabled` for a paused account; otherwise, until the sync engine
exists, it is `idle` with `progress: -1`.

```jsonc
Account { "id": "acc_1", "config": AccountConfig, "enabled": true, "state": SyncState }
AccountConfig {
  "name": "Work", "email": "me@example.org", "displayName": "Me" (opt),
  "imap": ServerConfig, "smtp": ServerConfig,
  "oauth2": OAuth2Config (opt), "syncIntervalSeconds": 300 (opt)
}
ServerConfig { "host": "imap.example.org", "port": 993, "security": "tls|starttls|none",
               "username": "me@example.org", "authMethod": "password|oauth2" }
OAuth2Config { "provider": "office365|custom", "clientId" (opt), "tenantId" (opt),
               "authUrl" (opt), "tokenUrl" (opt), "scopes": [] (opt) }
```

Secrets are **never** part of `AccountConfig` and never returned.
`credentials.password` is write-only: it goes to the keyring and is never
logged, echoed or stored in the SQLite store.

#### `account.add`
- params: `{ "config": AccountConfig, "credentials": { "password": "…" (opt) } }`
- result: `{ "accountId": "acc_2" }`
- errors: invalidArgument, keyringError, conflict (same e-mail already configured, case-insensitive)

Validation (all failures are invalidArgument; free-text fields are trimmed):
- `name` required, `displayName` optional; both valid UTF-8, no CR/LF/NUL,
  at most 256 bytes;
- `email` a bare, syntactically valid address (no display name);
- `imap`/`smtp`: `host` an IP literal or hostname of DNS labels (≤ 253
  bytes), `port` 1–65535, `security` one of `tls|starttls|none` where `none`
  is accepted only for `localhost` or a loopback IP, `username` required
  (≤ 256 bytes, no control characters), `authMethod` one of `password|oauth2`;
- `oauth2` present exactly when an endpoint uses `oauth2`; `provider`
  `office365|custom`, `custom` needs `https` `authUrl` and `tokenUrl`; at
  most 32 scopes without whitespace;
- `syncIntervalSeconds` 0 or ≥ 60;
- `credentials.password` only when an endpoint uses `password`.

The password is optional (an account without one ends in `authRequired`
once syncing exists). It is written to the system keyring
(`org.freedesktop.secrets`) and discarded; if the keyring refuses it nothing
is kept and `keyringError` is returned. That happens when no Secret Service
is running, when the user dismisses the unlock prompt, or when the daemon
runs with `MALACHI_KEYRING=none`. For `oauth2` no credentials are passed;
the backend starts the flow and emits `notify.authRequired` with `authUrl`.

#### `account.remove`
- params: `{ "accountId", "deleteLocalData": bool }`
- result: `{}`
- errors: invalidArgument, accountNotFound, storageError

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

#### `account.test`
Connectivity test without persisting anything. Validates like `account.add`
(the same `invalidArgument` cases, including a password for an account
without a `password` endpoint), then probes both endpoints concurrently.

- params: same as `account.add`
- result: `{ "imap": EndpointTestResult, "smtp": EndpointTestResult }`
- errors: invalidArgument only; each endpoint reports its own outcome

```jsonc
EndpointTestResult { "ok": true, "error": Error (opt), "capabilities": ["IDLE","CONDSTORE"] (opt), "latencyMs": 120 }
```

A probe dials, secures the connection (TLS 1.2+, system trust store, no
override; STARTTLS is mandatory when configured), authenticates with the
password and disconnects. Per-endpoint `error.code` is one of `authFailed`
(credentials rejected), `tlsError` (certificate, handshake, STARTTLS not
offered, or the server demanding TLS before login), `networkError`
(unresolvable, refused, connection dropped), `serverTimeout` (no answer in
time), `serverError` (protocol error or no usable authentication
mechanism), `notImplemented` (an `oauth2` endpoint, until OAuth2 lands).
`capabilities` are the server's post-login IMAP CAPABILITY list or the EHLO
keywords the backend knows about, scrubbed to printable ASCII, ≤ 64 entries;
`latencyMs` is dial → ready (greeting read, STARTTLS done). Budget: 10 s
to connect, 20 s per endpoint. The password is used for the connections
only and never logged.

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
thousand folders. `outbox` is a local pseudo-folder holding queued messages.

#### `folder.subscribe`
- params: `{ "accountId", "folderId", "subscribed": bool }`
- result: `{}`

### 4.3 message

#### `message.list`
- params: `{ "accountId", "folderId", "page": Page, "sort": SortOrder (opt), "unreadOnly": bool (opt) }`
- result: `{ "messages": [MessageSummary], "page": PageInfo }`

Cursor stability: a cursor encodes a (sort key, id) position and stays valid
across syncs; new messages inserted before the position are simply not seen
by an in-progress pagination. Clients refresh from the start on
`notify.newMessage`.

#### `message.get`
- params: `{ "accountId", "messageId" }`
- result: `{ "message": Message }`

#### `message.body`
**The only method that returns message content, and it returns only
sanitised content.**

- params: `{ "accountId", "messageId", "remoteContent": "block" | "allow" (opt, per-call override) }`
- result:

```jsonc
{
  "messageId": "m_123",
  "hasHtml": true,
  "html": "<div>…sanitised…</div>",      // absent/empty when hasHtml is false
  "text": "plain text alternative, or text derived from html",
  "blocked": { "remoteImages": 3, "remoteStyles": 1, "remoteFonts": 0, "scripts": 1,
               "forms": 0, "eventHandlers": 2, "dangerousUrls": 0, "embeddedFrames": 0,
               "trackingPixels": 1 },
  "links": [ { "text": "Click here", "href": "https://real.destination/…" } ],
  "inlineParts": { "image001@…": "2.1" },
  "sanitizerVersion": "1"
}
```

Guarantees of `html` (enforced in `backend/internal/sanitize`, see
`docs/security.md`):

- no `<script>`, `<iframe>`, `<object>`, `<embed>`, `<form>`, `<meta>`, `<link>`, `<base>`;
- no event-handler attributes; no `javascript:`, `vbscript:`, `data:text/html` URLs;
- under `block`: no reference to any remote resource (images, CSS, fonts,
  media). Under `allow`: `https:` images only, everything else still removed;
- inline CSS filtered through a property allow-list; no `url()`, `@import`, `position: fixed`;
- links restricted to `http(s):` and `mailto:`; the real target is listed in `links`;
- `cid:` references rewritten to `malachi-cid:<partId>` and only for parts that exist;
- output size- and depth-capped.

There is **no** parameter, flag, environment variable or debug method that
returns the original HTML.

Which policy applies: when `remoteContent` is omitted the stored preference
from `config.get` is used (`block` by default; `knownSenders` resolves to
`allow` only when every sender address of the message is on the `sender.list`
allow-list, otherwise `block`). Passing `remoteContent: "allow"` or `"block"`
overrides the preference for this one call and is not remembered;
`"knownSenders"` is not accepted per call (invalidArgument). Decrypted
content is always `block`, whatever the policy (see `docs/security.md` §5).

- errors: messageNotFound, sanitizeFailed (body withheld), malformedMessage,
  invalidArgument (bad `remoteContent`)

#### `message.flag`
- params: `{ "accountId", "messageIds": [..], "set": [Flag] (opt), "clear": [Flag] (opt) }`
- result: `{}`

Applied locally at once, pushed to the server asynchronously.

#### `message.move`
- params: `{ "accountId", "messageIds": [..], "targetFolderId" }`
- result: `{}`

#### `message.delete`
- params: `{ "accountId", "messageIds": [..], "permanent": bool (opt) }`
- result: `{}`

Default moves to the Trash role folder; `permanent` expunges.

#### `message.send`
Queues a saved draft into the outbox.

- params: `{ "accountId", "draftId", "version": 3 }`
- result: `{ "outboxId": "m_out_7" }`
- errors: draftNotFound, conflict (version mismatch), invalidArgument (no recipients)

Delivery is asynchronous; progress and failures arrive through
`notify.syncState` (folder role `outbox`, `pendingOutbox`). A failed send
stays in the outbox; it is never silently dropped.

Recipients (`to` + `cc` + `bcc`) must be non-empty; an empty subject or body
is allowed. The message is built from the *stored* draft: text/plain alone,
or multipart/alternative (text/plain + text/html) wrapped in
multipart/related when inline attachments are referenced. On success the
draft is removed and its attachments move with the outbox message. Until
the SMTP phase this method returns notImplemented.

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

Transitional: while `internal/sanitize` is a stub, every `draft.save` with
a non-empty `htmlBody` fails with sanitizeFailed and the draft is left
unchanged. Plain-text drafts work.

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

#### `sync.trigger`
- params: `{ "accountId" (opt), "folderId" (opt), "full": bool (opt) }`
- result: `{}` (returns immediately; progress via `notify.syncState`)

### 4.8 config

Daemon-owned preferences: options that affect mail handling and therefore
belong to the backend, not to the UI's own settings store. Precedence of
values: set through `config.set` (persisted in the store), else
`config.toml` (`[sync] interval_seconds`), else the built-in default.

```jsonc
Preferences {
  "syncIntervalSeconds": 300,   // 0 = manual sync only; otherwise >= 60
  "remoteContent": "block" | "knownSenders" | "allow"
}
```

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

## 5. Notifications

| Method | params |
|---|---|
| `notify.newMessage` | `{ "accountId", "folderId", "message": MessageSummary }` |
| `notify.syncState` | `{ "state": SyncState }` |
| `notify.authRequired` | `{ "accountId", "reason": 1200\|1201\|1202, "message": "…", "authUrl": "https://…" (opt) }` |
| `notify.accountsChanged` | `{}` |

`notify.accountsChanged` is sent after `account.add`, `account.remove` and
`account.setEnabled` to every client, including the caller; it carries no
payload and clients re-run `account.list`.

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
- **1** (2026-09-03, add-account wizard): keyring implemented over
  `org.freedesktop.secrets` (`account.add` stores the password);
  `account.test` implemented with per-endpoint outcomes.
