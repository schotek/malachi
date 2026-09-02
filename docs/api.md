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
| 1502 | attachmentTooBig | |

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

#### `account.list`
- params: `{}`
- result: `{ "accounts": [Account] }`

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

#### `account.add`
- params: `{ "config": AccountConfig, "credentials": { "password": "…" (opt) } }`
- result: `{ "accountId": "acc_2" }`
- errors: invalidArgument, keyringError, conflict (same e-mail already configured)

The password is written to the keyring and discarded. For `oauth2` no
credentials are passed; the backend starts the flow and emits
`notify.authRequired` with `authUrl`.

#### `account.remove`
- params: `{ "accountId", "deleteLocalData": bool }`
- result: `{}`

#### `account.test`
Connectivity test without persisting anything.

- params: same as `account.add`
- result: `{ "imap": EndpointTestResult, "smtp": EndpointTestResult }`

```jsonc
EndpointTestResult { "ok": true, "error": Error (opt), "capabilities": ["IDLE","CONDSTORE"] (opt), "latencyMs": 120 }
```

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

- params: `{ "accountId", "messageId", "remoteContent": "block" (default) | "allow" }`
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
returns the original HTML. `remoteContent: "allow"` is a per-call user
decision; the backend does not remember it (the UI may, per sender, later).

- errors: messageNotFound, sanitizeFailed (body withheld), malformedMessage

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

#### `draft.save`
- params: `{ "draft": Draft }`
- result: `{ "draftId": "d_1", "version": 2 }`
- errors: conflict (stored version ≠ supplied version), invalidArgument

```jsonc
Draft { "id": "d_1" (opt on first save), "accountId", "version": 1,
        "to": [Address], "cc": [Address] (opt), "bcc": [Address] (opt),
        "subject": "…", "textBody": "plain text",
        "inReplyTo": "m_123" (opt, local id), "forwarding": "m_124" (opt),
        "attachments": [ { "id", "filename", "contentType", "size" } ] (opt),
        "updatedAt": Time }
```

Optimistic concurrency: the client sends the version it last saw; the
backend increments on success. Compose bodies are plain text in protocol
version 1. HTML composition, if ever added, gets its own field and its own
inbound sanitisation.

#### `draft.list`
- params: `{ "accountId", "page": Page }`
- result: `{ "drafts": [Draft], "page": PageInfo }`

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

## 5. Notifications

| Method | params |
|---|---|
| `notify.newMessage` | `{ "accountId", "folderId", "message": MessageSummary }` |
| `notify.syncState` | `{ "state": SyncState }` |
| `notify.authRequired` | `{ "accountId", "reason": 1200\|1201\|1202, "message": "…", "authUrl": "https://…" (opt) }` |

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
