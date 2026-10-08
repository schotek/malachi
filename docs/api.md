# Malachi Mail RPC API

This document is the contract between the backend daemon (`malachid`) and any
user interface. The Go types in `backend/pkg/api/` are the machine-readable
form of the same contract; **both change together, in one commit.**

Protocol version: **2** (`api.ProtocolVersion`). Bump on any incompatible
change and describe the change in the changelog section at the end.

## 1. Transport

| Aspect | Value |
|---|---|
| Protocol | JSON-RPC 2.0 |
| Socket | `$XDG_RUNTIME_DIR/malachi/rpc.sock` (mode 0600, directory 0700 where the platform has file modes) |
| Inside Flatpak (`FLATPAK_ID` set) | `$XDG_RUNTIME_DIR/app/$FLATPAK_ID/malachi/rpc.sock` (`api.SocketBase`) |
| Fallback without `XDG_RUNTIME_DIR` | `$XDG_CACHE_HOME/malachi/run/rpc.sock` |
| Override | `malachid --socket PATH`; UI honours `MALACHI_SOCKET` |
| Key | `<socket>.key` beside the socket (`api.KeyPath`), a new random key at every daemon start (§1.4) |
| Framing | newline-delimited JSON: one JSON object per line, terminated by `\n`. No `Content-Length` header. |
| Max message size | 32 MiB per line (server side) after authentication; before it 4 KiB in total (§1.4) |
| Direction | bidirectional on one connection: client → server *requests*, server → client *responses* and *notifications* |
| Parameters | always by name (a JSON object), never positional |
| Concurrency | after authentication the server may process requests from one connection concurrently and answer out of order; match by `id`. The two handshake requests are answered in order, one at a time |
| Multiple clients | allowed; each connection authenticates on its own; notifications go only to authenticated connections, to all of them |

Authentication: every connection proves, in both directions, knowledge of
a key the daemon makes at every start (§1.4). Whoever can read the key can
use the daemon; reaching the socket is not enough. The key file is
protected like the socket: mode 0600 in the 0700 directory where the
platform has file modes; on Windows, by the permissions it inherits from
its directory, which for the default path lies in the user's profile. Inside
Flatpak the socket lives in the application's own runtime dir,
`$XDG_RUNTIME_DIR/app/<app-id>`: the rest of the sandbox's runtime dir is a
private tmpfs per instance, and that directory is the one part every
instance (and the host) sees, so a UI started later still finds a daemon an
earlier instance left running.

Startup: before it touches its store or the socket, the daemon takes an
exclusive lock on the store (`<store>.daemon.lock` beside it, which the system
releases with the process, however it ends); a second daemon for the same
store exits with an error. The daemon replaces a stale socket file left by
a crash after checking that nothing answers on it. If another daemon is
alive, or a connection attempt to the socket times out (0.5 s), it exits
with an error rather than stealing the socket (a flood of connections
that makes that check fail can defeat it for a daemon of another store,
`docs/security.md` §8); anything at the path that is not a socket is never
removed. The daemon writes the key file
after binding the socket and before it accepts any connection, and
clients read the key only after the `system.hello` answer (§1.4), so "the
socket answers" stays a valid test that the daemon is ready. Nothing on
the desktop starts the daemon; the UI does (`ui/internal/daemon`), passing
`--socket` so both resolve the same path, and stops the one it started
when it quits. A client that is not the UI has to run `malachid` itself.

### 1.1 Request

```json
{"jsonrpc":"2.0","id":7,"method":"message.list","params":{"accountId":"acc_1","folderId":"f_inbox","page":{"limit":50}}}
```

`id` may be a number or string; it is echoed back unchanged. A request
without `id` is a client notification; the contract defines none, the
server ignores them (before authentication the connection is closed
instead, §1.4).

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

### 1.4 Handshake

Every connection starts with a handshake in which both ends prove that
they hold the daemon's connection key; nothing else is served before it
completes. The client speaks first and waits for each answer:

```jsonc
// client → daemon
{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"<64 hex digits>"}}
// daemon → client
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":2,"daemonNonce":"<64 hex digits>","daemonProof":"<64 hex digits>"}}
// client → daemon
{"jsonrpc":"2.0","id":2,"method":"system.authenticate","params":{"clientProof":"<64 hex digits>"}}
// daemon → client
{"jsonrpc":"2.0","id":2,"result":{}}
```

The connection is usable only after the last line. A nonce is 32 bytes
from a cryptographic random source, new for every connection and each
side; nonces and proofs travel as 64 lowercase hex digits.

**The key file.** At every start the daemon makes a key of 32 random bytes
and writes it to `<socket>.key` (`api.KeyPath`): exactly 65 bytes, the key
as 64 lowercase hex digits and `\n`. It writes the file atomically after
binding the socket and setting its mode, and before it accepts a
connection. It replaces only a missing file or a key-shaped regular file
(at most 65 bytes, each of them `0`–`9`, `a`–`f` or `\n`) and exits with an
error when anything else is in the way. Before it answers each
`system.hello` it writes the file again, under the same rule, when it is
missing or holds another key; something else in the way then is left
alone and logged, and the client fails with `keyUnavailable`. At shutdown
it removes the file, only while the file still holds its own key, before
it closes the listener. The key is never logged and never sent over the
socket.

**Proofs.** A proof is HMAC-SHA256 under the key of a 91-byte message:

`proof = HMAC-SHA256(key, "malachi-rpc-auth-v1" ‖ 0x00 ‖ role ‖ 0x00 ‖ clientNonce ‖ daemonNonce)`

The label and the role are ASCII, `‖` concatenates, the role is `"daemon"`
for `daemonProof` and `"client"` for `clientProof`, and the nonces are
their 32 raw bytes, not their hex. The role keeps a proof from being
reflected to the side that sent it.

**The client**, in this order:

1. sends `system.hello` as the first line of the connection;
2. compares `protocolVersion` with its own and stops on a difference,
   without reading the key;
3. reads the key file only after the `system.hello` answer, afresh for
   every connection (a restarted daemon has a new key), and accepts only a
   regular file — no link, directory, pipe or device — of exactly 65 bytes
   in the format above;
4. verifies `daemonProof` in constant time;
5. sends `system.authenticate` with its `clientProof`, and nothing else
   until that is answered.

It waits for each answer before it sends the next line, and on any failure
closes the connection without sending anything more.

**The daemon** answers the handshake in the transport, synchronously in the
connection's read loop; no backend code runs before authentication.

| State | The client sends | The daemon |
|---|---|---|
| new | `system.hello` with a valid `clientNonce` | answers with its nonce and proof; the state becomes *hello answered* |
| hello answered | `system.authenticate` with the right `clientProof` | answers `{}`; the state becomes *authenticated* |
| new, hello answered | any other JSON object whose `id` is not `null`: another method, a second `system.hello`, a malformed nonce, a wrong or malformed proof | answers error 1005 `unauthenticated` with that `id`, then closes the connection |
| new, hello answered | anything else: not JSON, a JSON array or another non-object, an object without `id` or with `"id": null` | closes the connection without an answer |
| authenticated | any request | answers as §4 says; `system.hello` and `system.authenticate` get `invalidRequest` ("already authenticated") and the connection stays usable |

Empty lines are skipped, before authentication as after it. Before
authentication a connection may send at most 4 KiB
(`api.MaxHandshakeBytes`), all lines together and empty ones included, and
must complete the handshake within 10 s of being accepted, or it is
closed. At most 32
connections are in the handshake at a time; further ones are accepted and
closed at once. No notification is sent to a connection before the
`system.authenticate` answer. Connecting and closing without sending a line
(a test whether a daemon is alive) is fine and not logged as an error.

**Protocol 1.** A daemon of protocol 1 does not know `system.hello` and
answers `methodNotFound`, possibly after notifications it broadcasts to
every connection (a client skips a few; the Go helper up to 8). The client
reports that the daemon speaks protocol 1 and never falls back to using
the connection unauthenticated. A client of protocol 1 gets
`unauthenticated` for its first call and is disconnected.

**Go clients** (the GTK UI, `malachi-mcp`) call
`api.ClientHandshake(ctx, conn, reader, keyPath)` right after connecting.
It bounds the exchange by `api.HandshakeTimeout` (5 s) and the context,
leaves whatever the daemon sent after the last answer in the caller's
reader, and fails with an `*api.HandshakeError` whose reason is
`protocolMismatch` (with the daemon's version), `keyUnavailable`,
`daemonUnproven`, `rejected` (with the error code), `malformed` or
`timedOut` (also when the context's deadline comes first), with the
context's error when the context is cancelled, or with a plain error when
the connection broke.

Test vectors (key, nonces and proofs are 32 bytes):

| Value | Hex |
|---|---|
| key | `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f` |
| `clientNonce` | `202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f` |
| `daemonNonce` | `404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f` |
| label `"malachi-rpc-auth-v1"` | `6d616c616368692d7270632d617574682d7631` |
| role `"daemon"` | `6461656d6f6e` |
| role `"client"` | `636c69656e74` |
| `daemonProof` | `04abc851d52b40dc687920756f15f42f44da2635331732bf02be0a0b01de2a1f` |
| `clientProof` | `024f86a00c241237f4556a27e83f8e41b13053bbcfd2980e029c300a5a2e84b2` |

`docs/security.md` §8 says what the handshake protects and what it does
not: it is not encryption, gives the messages after it no integrity of
their own, and does not protect against the user's own processes, which
can read the key file.

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
| 1005 | unauthenticated | the connection has not completed the handshake (§1.4), or it failed; the daemon closes the connection after this answer |
| 1100 | accountNotFound | |
| 1101 | folderNotFound | |
| 1102 | messageNotFound | |
| 1103 | threadNotFound | |
| 1104 | draftNotFound | |
| 1105 | attachmentNotFound | unknown id, another account's, or already bound to a different draft |
| 1106 | caseNotFound | no board case with that id (it left the board, its account is gone), or for `board.setCommitment` no such commitment (§4.13) |
| 1200 | authRequired | user interaction needed; a `notify.authRequired` was/will be sent |
| 1201 | authFailed | server rejected credentials |
| 1202 | keyringError | secret service unavailable |
| 1203 | oauthClientMissing | the backend's own sign-in needs an OAuth client id for the provider and none is configured (§4.1) |
| 1300 | offline | daemon is in offline mode |
| 1301 | networkError | connection failed |
| 1302 | serverError | IMAP/SMTP server returned an error |
| 1303 | tlsError | certificate or handshake problem; for IMAP/SMTP endpoints `data` = `TLSErrorData` (below) |
| 1304 | serverTimeout | |
| 1305 | messageGone | the mail server no longer has the message (another client deleted or moved it); the next sync removes the local copy |
| 1400 | storageError | SQLite failure |
| 1401 | migrationFailed | store schema could not be upgraded |
| 1500 | malformedMessage | MIME unparsable even leniently |
| 1501 | sanitizeFailed | sanitiser refused the body; **body is withheld**, never returned raw |
| 1502 | attachmentTooBig | over a documented limit; `data` = `{ "limit": bytes, "size": bytes }` |
| 1503 | partNotFound | `message.part` named a part the message does not have, or its content is no longer stored |
| 1504 | partNotDownloaded | the part's data is not stored on this device (`Attachment.remote`); `message.download` fetches it |
| 1505 | unsubscribeFailed | `message.unsubscribe` reached the sender's server, which refused the one-click request (any answer but 2xx, a redirect included), or the daemon refused to connect to the address of the URL (not a public one: loopback, private, link-local, a single-label or local-network name); `message` carries the reason and is not for display |
| 1506 | quoteNotFound | `board.annotate` or `board.commit` quoted text the daemon did not find verbatim where it must be; `data` = `{ "field": "due" \| "commitment" }` (§4.13) |

`tlsError` from an IMAP or SMTP endpoint (in `account.test` results and in a
`SyncState`) carries:

```jsonc
TLSErrorData {
  "reason": "untrusted|hostnameMismatch|expired|notYetValid|invalid|other|handshake|starttlsUnavailable|tlsRequired|pinMismatch",
  "certificate": CertificateInfo (opt),   // when the server presented one
  "expectedSha256": "…" (opt)             // pinMismatch only: the pinned fingerprint
}
CertificateInfo { "sha256": "64 lowercase hex digits of the DER certificate",
                  "subject": "…" (opt), "issuer": "…" (opt), "dnsNames": [] (opt),
                  "ipAddresses": [] (opt), "notBefore": time, "notAfter": time,
                  "selfSigned": bool }
```

`untrusted`: the issuer is not in the system trust store (a self-signed or
private certificate). `hostnameMismatch`: issued for another name.
`expired` / `notYetValid`: outside its validity. `invalid`: otherwise
unusable for a server. `other`: refused by the system verifier for a
reason it does not classify (e.g. macOS "not standards compliant").
`handshake`: the TLS protocol failed before a certificate was judged (no
`certificate`). `starttlsUnavailable`: STARTTLS is configured but not
offered or refused. `tlsRequired`: the server demands TLS before login.
`pinMismatch`: the endpoint pins a certificate
(`ServerConfig.certificateSha256`) and the server presented another one.
Every string of `certificate` is untrusted text from the server (control,
format and line/paragraph separator characters removed, ≤ 128 bytes;
lists ≤ 8 entries). Unknown
`reason` values are to be treated as `other`.

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
  "outbox": OutboxInfo (opt),
  "issue": MessageIssue (opt),
  "bulk": BulkInfo (opt)
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

`issue` is present only for a message of an issue-tracker account (kind
`jira`, §4.1): the issue the message belongs to, and what part of it the
message is. The fields of `IssueInfo` are flattened into the `issue`
object:

```jsonc
IssueInfo { "key": "ITSD-42", "url": "https://acme.atlassian.net/browse/ITSD-42",
            "summary": "Printer on the 2nd floor", "status": "In Progress",
            "statusCategory": "todo|inProgress|done" (opt), "type": "Bug" (opt),
            "priority": "High" (opt), "assignee": "Jana Dvořáková" (opt), "reporter": "…" (opt),
            "assignedToMe": true (opt), "watching": true (opt),
            "commentVisibilities": ["public", "internal"] (opt) }
MessageIssue = IssueInfo + { "item": "description|comment|event",
            "visibility": "public|internal" (opt), "changes": [IssueChange] (opt),
            "via": "…" (opt), "edited": true (opt), "mine": true (opt) }
IssueChange { "field": "status|assignee", "from": "To Do" (opt), "to": "In Progress" (opt) }
```

- An issue is a thread (§4.4). Its messages are `item: "description"`
  (the issue itself, its first message, with the description as the
  body), `"comment"` (one comment each) and `"event"` (a change of the
  status or the assignee, unless the account hides events). Every one has
  the subject `KEY: Summary`, so a client or tool that knows nothing of
  issues still shows something sensible. `from` is the author with the
  display name the site shows and an address under the reserved `.invalid`
  domain (a person on the site, never a mailbox to write to).
- `url` is `<siteUrl>/browse/<key>`, always `https:` or `http:`; `key` is
  the site's issue key. Every other string is untrusted display text from
  the site. `statusCategory` is an open enum: an unknown value is to be
  treated as absent. `assignee` absent means unassigned.
- An `event` message is stored read and never produces
  `notify.newMessage`; its `snippet` and text are language-neutral values,
  one line per change, `<from> → <to>` with `—` for an empty side ("To Do →
  In Progress", "— → Jana Dvořáková"). A client builds its own sentence
  from `changes` and skips a change of a `field` it does not know.
- `visibility` is set on a comment of a service-desk issue: `internal`
  comments are for the service-desk team only, `public` ones reach the
  customer. `commentVisibilities` lists what a new comment on the issue
  may be (`draft.create`, §4.5): both on a service-desk issue, absent
  elsewhere (public only).
- `via` is the display name of an integration that posted the comment on
  someone else's behalf (`jira.botNames`, §4.1): `from` is then the person
  the comment names as its author. `edited`: the comment was changed after
  it was posted; the message shows the current text. `mine`: the
  account's own user wrote the item on the site (never set together with
  `via`), so a client can tell the user's own comments apart.

`bulk` is present only for a message the daemon recognised as bulk mail
from its headers (rule version `"1"`, `internal/bulk`); absent for
personal mail, for every message of an `issue-tracker` account and for a
row not classified yet:

```jsonc
BulkInfo { "kind": "newsletter|list|automated",
           "listId": "golang-nuts.googlegroups.com" (opt),
           "domain": "news.example" (opt) }
```

- `list`: a discussion list (`List-Id` together with a `List-Post` that
  has a `mailto:` address). `newsletter`: otherwise, a `List-Unsubscribe`
  with at least one usable URI, or `Precedence: bulk` together with
  `List-Id`. `automated`: otherwise, `Auto-Submitted` other than `no`, or
  `Precedence` `bulk`, `junk` or `list`, or a field that bulk-sending
  services add to what they relay (`Feedback-ID`, `X-CSA-Complaints`,
  `X-MSFBL`, `X-SG-EID`, `X-Mailgun-Sid`, `X-SES-Outgoing`, `X-MC-User`,
  `X-Mandrill-User`, `X-PM-Message-Id`, `X-SFMC-Stack`; receipts, tickets,
  notifications; no unsubscribe offer). A client treats an unknown `kind`
  as absent.
- A URI of `List-Unsubscribe` is usable only when it is `https:` with a
  host, no credentials and ASCII only, or `mailto:` with an address; at
  most eight bracketed items of at most 2048 bytes are read, anything
  else (`http:`, `javascript:`, `data:`, broken brackets) is ignored.
- `listId` is the identifier inside `<…>` of `List-Id`, lower case, at
  most 255 bytes, without control or bidirectional characters; `domain`
  is the lower-case domain of the first `From` address. Both are text from
  the mail and are shown as plain text.
- The classification is made when the headers are known: from the header
  fields the IMAP sync fetches with the envelope, again when the body is
  ingested, and for rows stored earlier by a background pass. Listings
  (`message.list`, `thread.list`, `thread.get`, `search.query`) carry it;
  the attached message of `message.embedded` does not. `notify.newMessage`
  carries it when the row was classified when its envelope arrived (IMAP
  accounts); a Graph message, whose headers come with the body, does not
  until a listing.

### Message (message.get)

`MessageSummary` plus `cc`, `bcc`, `replyTo`, `rfcMessageId`, `inReplyTo`,
`references`, `attachments: [Attachment]`, `headers`, a curated
map of a few interesting headers (`List-Unsubscribe`, `Auto-Submitted`, …),
and `unsubscribe` (below). The raw header block is never returned.

```jsonc
UnsubscribeOffer { "method": "oneClick|mailto|url",
                   "target": "news.example" | "unsub@news.example",
                   "url": "https://…" (opt, method url only),
                   "unsubscribedAt": Time (opt) }
```

`unsubscribe` is set by `message.get` and `message.download` (the same
message) only, not by listings or `message.embedded`, when the message's headers
offer a usable method, and never for an `issue-tracker` account, for a
message in a folder of role `junk` or flagged `junk`. The method is chosen
from the headers: for a `list` the `mailto:` address, else one-click, else
the page; for anything else `oneClick` when there is an `https:` URI and
`List-Unsubscribe-Post` is `List-Unsubscribe=One-Click` (RFC 8058; case
and surrounding space ignored), else `mailto`, else `url`. `target` is
what a confirmation shows: the host of the URL, or the address. `url` is
the page to open, for `url` only. `unsubscribedAt` is set when the user
already unsubscribed from this list (`list:<List-Id>`) or sender
(`from:<address>`) of this account through `message.unsubscribe` with
`oneClick` or `mailto`; a page opened in the browser is never remembered.

```jsonc
Attachment { "partId": "2.1", "filename": "safe-name.pdf", "contentType": "application/pdf",
             "size": 12345, "inline": false, "contentId": "…",
             "remote": true }   // (opt) data kept on the mail server only
```

Filenames are sanitised by the backend (no path separators, no control or
bidi-control characters, length-capped).

`remote` is set when the part's data is not stored on this device: under
`attachmentOfflineDays` (§4.8) the large attachments of older messages stay
on the mail server, and under `neverStoreAttachments` every attachment the
HTML does not show. Name, type and size are still those of the original
part; `message.part` answers `partNotDownloaded` for it until
`message.download` has fetched the message. It appears only on messages
whose `bodyState` is `fetched`. A part not marked may still answer
`partNotDownloaded`: the message was reduced since it was listed, or its
stored file turned out to lack the part (after a crash), which marks it
`remote` from then on. Under `neverStoreAttachments` (§4.8) a part stays
`remote` after `message.download`: the daemon holds the downloaded message
in memory only, and serves the part from there while it lasts. That mode
also leaves on the server the pictures the HTML shows of 100 KiB and more
(`message.body` `remotePictures`).

### SyncState

```jsonc
{ "accountId": "acc_1", "status": "idle|syncing|offline|authRequired|error|disabled",
  "folderId": "f_inbox", "progress": 42, "lastSync": Time, "error": Error, "pendingOutbox": 0,
  "failedOutbox": 0 }
```

- `status`: `idle` (connected or between passes, no work), `syncing` (a pass
  is running; `folderId`/`progress` describe it), `offline` (the last attempt
  failed for a network or TLS reason; `error` set, for `tlsError` with
  `TLSErrorData` in `error.data` (§2); retrying with backoff),
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
- `failedOutbox`: outbox messages in state `failed` — delivery gave up and
  they wait in the outbox for `outbox.retry` or a delete. They count here,
  never in `pendingOutbox`; a `sent` message counts in neither. Both counts
  are filled for every account, a `disabled` one included.

## 4. Methods

Every method lists its `params` and `result` shapes. Fields marked *opt* may
be omitted. All methods that touch data take `accountId`.

### 4.0 system

#### `system.hello`
The first line of every connection (§1.4). The transport answers it, not
the backend.

- params: `{ "clientNonce": "64 lowercase hex digits" }`
- result: `{ "protocolVersion": 2, "daemonNonce": "64 lowercase hex digits", "daemonProof": "64 lowercase hex digits" }`
- errors: unauthenticated (not the connection's first non-empty line, or
  a malformed `clientNonce`; then the connection is closed),
  invalidRequest (after authentication)

`protocolVersion` is in this result in every protocol version, and the
method keeps its name and `clientNonce`, so a client can always tell which
protocol a daemon speaks before it reads the key. A daemon of protocol 1
answers `methodNotFound`.

#### `system.authenticate`
The second line of every connection (§1.4); once it is answered, the
connection is usable. The transport answers it, not the backend.

- params: `{ "clientProof": "64 lowercase hex digits" }`
- result: `{}`
- errors: unauthenticated (not right after an answered `system.hello`, or
  a wrong or malformed `clientProof`; then the connection is closed),
  invalidRequest (after authentication)

#### `system.info`
Health check and daemon details: usually a client's first call after the
handshake (§1.4); `protocolVersion` equals hello's.

- params: `{}`
- result: `{ "version": "0.1.0", "protocolVersion": 2, "pid": 4242, "storePath": "/home/u/.local/share/malachi/store.db" }`

#### `system.storage`
How much disk the mail store uses, for the preferences dialog; cheap enough
to poll every few seconds while it is open.

- params: `{}`
- result:

```jsonc
{
  "totalBytes": 734003200,               // databaseBytes + messageBytes + attachmentBytes
  "databaseBytes": 44470272,             // store.db with its -wal and -shm
  "messageBytes": 546700000,             // the stored raw messages as they are stored
  "messageUncompressedBytes": 909800000, // their content; equals messageBytes without compression
  "savedBytes": 363100000,               // what compression saves (the difference of the two)
  "attachmentBytes": 250000,             // the compose-side attachment store (§4.10)
  "remoteAttachmentBytes": 312000000,    // decoded size of attachments kept on the server only
  "messages": 3725,                      // messages with a stored raw file
  "compressedMessages": 3725,
  "partialMessages": 410,                // messages with attachments on the server only
  "conversion": "idle"                   // idle | running | noSpace
}
```

Byte counts are file lengths, not allocated blocks (a file system that
compresses by itself, such as btrfs, may use less). `conversion` is the
background pass that brings stored mail to the current `compressStore` and
`attachmentOfflineDays`: `running` while it has work left, `noSpace` when it
stopped because the disk is full (it tries again after any accepted
`config.set`, even one that changes nothing, or a restart of the daemon),
`idle` otherwise. It may say `running` for a while with nothing to do:
from the first start of a daemon of this version until the pass has gone
through the store once, and when what the pass works towards has moved (a
new day under an `attachmentOfflineDays` of N days) until the pass has
looked, which an idle daemon does every minute. Until that first pass has
accounted for the files an older daemon stored, their bytes are estimated
from the messages' sizes, as if uncompressed.

- errors: storageError

### 4.1 account

Accounts live in the daemon's store and are managed only through these
methods. `[[accounts]]` entries in `config.toml` are bootstrap defaults: at
start the daemon imports each one whose e-mail is not in the store and has
not been imported before (so an account removed through `account.remove`
stays removed); the daemon never writes `config.toml`. An entry's `id`
names the account's directory of stored messages, so the import skips,
with a warning in the log, an id that could leave that directory (a path
separator, `:`, `.`, `..`, a Windows device name), one ending in a dot or a
space, and one that differs from another account's id only in case or
Unicode normalisation (the same directory on macOS and Windows). `account.list`
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
Account { "id": "acc_1", "config": AccountConfig, "enabled": true, "state": SyncState,
          "capabilities": ["compose", "reply", "replyAll", "forward", "move", "delete"] }
AccountConfig {
  "name": "Work", "email": "me@example.org", "displayName": "Me" (opt),
  "kind": "imap|graph|jira" (opt, default imap),
  "imap": ServerConfig (imap only), "smtp": ServerConfig (imap only),
  "oauth2": OAuth2Config (opt; imap, or graph with source daemon),
  "graph": GraphConfig (graph only),
  "jira": JiraConfig (jira only),
  "syncIntervalSeconds": 300 (opt)
}
ServerConfig { "host": "imap.example.org", "port": 993, "security": "tls|starttls|none",
               "username": "me@example.org", "authMethod": "password|oauth2",
               "certificateSha256": "64 hex digits" (opt) }
OAuth2Config { "source": "goa|daemon" (opt), "goaAccountId": "account_1788683507_0" (with source goa),
               "provider": "google|office365|custom", "clientId" (opt), "tenantId" (opt),
               "authUrl" (opt), "tokenUrl" (opt), "scopes": [] (opt) }
GraphConfig  { "source": "goa|daemon", "goaAccountId": "account_1788512854_0" (with source goa) }
JiraConfig {
  "siteUrl": "https://acme.atlassian.net", "deployment": "cloud|datacenter",
  "cloudId": "UUID" (opt; cloud only), "login": "me@example.org" (cloud only),
  "spaces": [SpaceRef],                       // 1–200
  "offlineDays": 30 (opt; 0 = 30, at most 365),
  "onlyMine": false (opt), "hideEvents": false (opt),
  "disabledFolders": ["assignedToMe|watching|open"] (opt),
  "closedStatuses": [StatusRef] (opt),         // ≤ 64; empty = the statuses of the category done
  "notificationMail": "sync|hide|ignore" (opt, default sync),
  "notificationSenders": ["jira@example.org", "@example.org"] (opt),
  "botNames": ["…"] (opt), "metadataFilters": ["RE2 pattern"] (opt), "authorPrefixes": ["…"] (opt)
}
SpaceRef  { "id": "10001", "key": "ITSD", "name": "IT Service Desk" (opt) }
StatusRef { "id": "3", "name": "In Progress" (opt) }
```

`kind` selects the protocol behind the account. `imap` (the default when
absent) is a classic mailbox: IMAP for reading, SMTP for sending. `graph`
is a Microsoft 365 / Outlook.com mailbox accessed through the Microsoft
Graph API; it has no servers of its own, only a token source. With
`source: "goa"` the sign-in belongs to GNOME Online Accounts: the daemon
asks it for access tokens (`goaAccountId` is the GOA account id) and holds
them in memory only; refresh tokens never reach Malachi. A Graph account
sends and receives through Graph alone — no IMAP or SMTP is involved.

`jira` is an issue tracker read like mail: a Jira site, Atlassian Cloud
(`deployment: "cloud"`, REST API v3) or Data Center / Server
(`"datacenter"`, REST API v2). Its folders are the selected `spaces` (Jira
projects) and three fixed views of them (§4.2); every issue is a thread
whose messages are its description, its comments and the changes of its
status and assignee (`MessageSummary.issue`, §3). The account reads the
site only; the one thing it writes is a comment (`draft.create` `reply`,
§4.5). The token is `credentials.password`: on Cloud an API token, sent
with `login` (the Atlassian account's e-mail) as HTTP Basic
authentication, and through the Atlassian API gateway
(`https://api.atlassian.com/ex/jira/<cloudId>`, which scoped tokens need)
when the site refuses it and `cloudId` is known; on Data Center a personal
access token, sent as a Bearer token. `email` is the user's own address
(on Cloud normally `login`).

- `siteUrl` is the normalised form `account.detectSite` returns: `https`
  (`http` only for a loopback host, or a Data Center site the user typed
  with `http`), no user info, query or fragment, no trailing slash; a Data
  Center site may have a context path.
- `offlineDays` is the account's own window (the `offlineDays` preference,
  §4.8, does not apply): issues updated within it are kept, and open
  issues assigned to the user whatever their age (at most 500). On the
  first synchronisation the items created in the last 3 days are unread
  (except the user's own and events), older ones read; later, new items of
  others arrive unread.
- `onlyMine` keeps only the issues the user reports, is assigned to,
  watches or updated recently (and those a notification mail named), not
  every issue of the spaces. `hideEvents` leaves the status and assignee
  changes out of the threads. `disabledFolders` hides views.
- `closedStatuses` decide what the `open` view leaves out; empty means the
  statuses of the category done (`account.listSpaces` lists the site's
  statuses).
- `notificationMail` is what a notification e-mail of the site arriving in
  one of the user's mail accounts (`imap`, `graph`) does: `sync` (the
  default) makes the daemon synchronise its issue at once; `hide` does
  that too and hides the mail in its mail account — a display filter: the
  message leaves every listing, count, search and notification
  (`message.get` still returns it by id) but nothing about it changes on
  the mail server, not a flag, and it is shown again when the setting
  changes, the `jira` account is paused or removed, its space is
  deselected or its issue leaves the account; `ignore` does nothing. A
  mail counts as a notification when every address of its `From` matches
  `notificationSenders` (an address, or `@host` for any address of
  exactly that host, without regard to ASCII case; a display name never
  counts; empty means `@<site host>` on Cloud and nobody on Data Center,
  whose notifications match nothing until a sender is configured), and
  its subject holds, within its first 1024 bytes, an issue key in
  parentheses or square brackets (`[JIRA] (ITSD-42) Printer`,
  `IT Service Desk: Printer (ITSD-42)`; a space key of 2 to 10 capitals
  and digits, to the letter, and a number; a space whose key holds
  anything else is not recognised) whose space is a selected one; the
  first such key counts.
  For `hide` that issue must also be stored in the `jira` account: a
  message about an issue the account does not hold stays visible. The
  first enabled `jira` account in the accounts' order that knows a
  message takes it. The issue of a message that arrived within the last
  15 minutes is fetched before the message is announced (the daemon waits
  for it at most 5 seconds), so that a hidden message produces no
  `notify.newMessage`; mail older than the account's `offlineDays` asks
  for nothing, nor does mail older than what is stored of its issue, and
  an issue the site did not give is not asked for again for 10 minutes.
  With `hide`, mail that was stored before the account took it (before
  its first synchronisation, or before `hide`, the senders, the spaces or
  the window were set) is looked through, within the account's
  `offlineDays`, when the account's first synchronisation with that
  configuration ends and at once after `account.update`; the daemon
  judges what is hidden again every hour.
- `botNames` are the display names of integrations that post comments on
  someone else's behalf (the settings UI may suggest one): such a comment
  is shown as written by the person it names in its header, with `via`
  set (§3); `authorPrefixes` are words stripped from the start of that
  person's name (such as an organisation name the bot puts in front of
  it), matched as whole words ignoring case. `metadataFilters` are RE2
  patterns, each matched against a whole trimmed line of any comment;
  matching lines are removed unless that would leave the comment empty.
  Empty lists: nothing is re-attributed or removed. A change of any of
  them (or of `hideEvents`) rebuilds the stored messages in place, under
  their ids, on the next pass, which ends with `notify.messagesChanged`
  on the account (§5).

`capabilities` lists what a client may offer for the account's messages
beyond reading them and setting flags; a client reads the list and never
derives it from `kind`:

| Capability | Meaning |
|---|---|
| `compose` | the account can send a new message, and is where a forward is written |
| `reply`, `replyAll` | its messages can be answered by e-mail (`draft.create`) |
| `forward` | its messages can be forwarded; a `jira` account's from a mail account (`draft.create` `messageAccountId`, §4.5) |
| `comment` | reply writes a comment to the issue (`draft.create` `reply` returns a comment draft); a client names the action Comment |
| `move`, `delete` | `message.move`, `message.delete` |
| `transition` | the status of its issues can be changed (`issue.transitions`, `issue.transition`, §4.12) |

`imap` and `graph` accounts have `["compose", "reply", "replyAll",
"forward", "move", "delete"]`; a `jira` account `["comment", "forward",
"transition"]`.
Flags (`message.flag`) are always allowed, and so is `message.delete` of
an account's outbox messages (cancelling a queued send or comment). Archiving and
marking as junk still depend on folders with those roles, which a `jira`
account does not have. The daemon always sends the list, `[]` when there
is nothing to offer; a client talking to an older daemon, which sends
none, assumes the list of the mail accounts.

An `imap` account whose endpoints use `authMethod: "oauth2"` signs in with
an access token through SASL XOAUTH2 (OAUTHBEARER when that is all the
server offers). With `oauth2.source: "goa"` the token comes from GNOME
Online Accounts exactly as for a Graph account, and `provider` says whose
account it is — today `google`: Gmail and Google Workspace, with the
servers GNOME Online Accounts names (`account.linked` and
`account.discover` build the whole config).

With `source: "daemon"` the backend runs the sign-in itself, for desktops
without GNOME Online Accounts or addresses not signed in there: an
authorization-code flow with PKCE started by `account.oauthStart`, the
user signing in in their browser, the refresh token kept in the system
keyring (`oauth2.refresh_token`) and access tokens in memory only.
`provider` is `google` on an `imap` account (Gmail through IMAP/SMTP with
SASL XOAUTH2; the servers are pinned to `imap.gmail.com` /
`smtp.gmail.com`, because the token, scoped for all of Gmail, goes to
whichever server the account names) or `office365` on a
`graph` account (Microsoft 365 and Outlook.com through Graph, the
`graph.source` being `daemon` too). The OAuth client is the one the
daemon is configured with (`config.toml` `[oauth2.google]` /
`[oauth2.microsoft]`, see the README); `clientId` and, for `office365`,
`tenantId` override it for one account. Without any client id the flow
cannot start and `oauthClientMissing` is returned. An `oauth2` block
without `source` (provider `custom` with `authUrl`, `tokenUrl`, `scopes`)
is reserved and reports `notImplemented`.

Secrets are **never** part of `AccountConfig` and never returned.
`credentials.password` is write-only: it goes to the keyring and is never
logged, echoed or stored in the SQLite store. `credentials.oauthSession`
names a completed `account.oauthStart` session whose tokens never leave
the backend. A `goa` account takes no credentials at all.

#### `account.add`
- params: `{ "config": AccountConfig, "credentials": { "password": "…" (opt), "oauthSession": "s_…" (opt) } }`
- result: `{ "accountId": "acc_2" }`
- errors: invalidArgument, keyringError, conflict (same e-mail already configured in the same
  realm, case-insensitive: every mail account shares one realm, a `jira` account's realm is its
  site, so it may have the address of a mailbox), oauthClientMissing (source `daemon` and no
  client id for the provider)

Validation (all failures are invalidArgument; free-text fields are trimmed):
- `name` required, `displayName` optional; both valid UTF-8, no CR/LF/NUL,
  at most 256 bytes;
- `email` a bare, syntactically valid address (no display name);
- `kind` absent, `imap`, `graph` or `jira`;
- for `imap`: `imap` and `smtp` required, `graph` and `jira` absent; `host` an IP
  literal or hostname of DNS labels (≤ 253 bytes), `port` 1–65535,
  `security` one of `tls|starttls|none` where `none` is accepted only for
  `localhost` or a loopback IP, `username` required (≤ 256 bytes, no
  control characters), `authMethod` one of `password|oauth2`,
  `certificateSha256` optional: a SHA-256 fingerprint (64 hex digits;
  colons, spaces and upper case are accepted and stored as 64 lowercase
  hex digits; an empty or blank value means no pin), only with
  `security` `tls|starttls` and `authMethod` `password`;
- for `imap`: `oauth2` present exactly when an endpoint uses `oauth2`.
  With `source: "goa"`: `provider` `google`, a `goaAccountId` (letters,
  digits and `_`, ≤ 128 bytes), both endpoints `oauth2`, and none of
  `clientId`, `tenantId`, `authUrl`, `tokenUrl`, `scopes`. With
  `source: "daemon"`: `provider` `google`, both endpoints `oauth2`, `imap`
  exactly `imap.gmail.com` port 993 `tls`, `smtp` exactly
  `smtp.gmail.com` with port 465 `tls` or 587 `starttls` (host names
  compared case-insensitively and stored lower-case), `username` on both
  the account's `email` (case-insensitively), no
  `goaAccountId`, `authUrl`, `tokenUrl`, `scopes`, `tenantId`; `clientId`
  optional (printable ASCII without spaces, ≤ 256 bytes). Without
  `source`: `provider` `office365|custom`, `custom` needs `https`
  `authUrl` and `tokenUrl`, at most 32 scopes without whitespace, no
  `goaAccountId`;
- for `graph`: `imap`, `smtp` and `jira` absent; with `graph.source: "goa"` a
  `goaAccountId` (letters, digits and `_`, ≤ 128 bytes) and no `oauth2`;
  with `graph.source: "daemon"` no `goaAccountId` and an `oauth2` block
  `{source: "daemon", provider: "office365"}` with optional `clientId`
  and `tenantId` (letters, digits, `.` and `-`, not starting with `.`,
  ≤ 64 bytes; default `common`);
- for `jira`: `jira` required, `imap`, `smtp`, `oauth2` and `graph`
  absent; `siteUrl` a URL that normalises as `account.detectSite` does
  (stored normalised), `https` for `cloud` unless its host is a loopback
  one; `deployment` `cloud` or `datacenter`; for `cloud` a `login` that is
  a bare address, for `datacenter` no `login`;
  `cloudId` empty or a UUID (stored lower-cased), `cloud` only; 1–200
  `spaces` (`api.MaxJiraSpaces`), each with a non-empty `id` and `key`, no
  `id` twice; at most 64 `closedStatuses` (`api.MaxJiraStatuses`), each
  with a non-empty `id`; the `id`, `key` and `name` of spaces and statuses
  at most 256 bytes of valid UTF-8 without control characters;
  `offlineDays` 0–365; `disabledFolders` of the three view codes, none
  twice; `notificationMail` absent, `sync`, `hide` or `ignore`;
  `notificationSenders` entries `addr@host` or `@host` (stored
  lower-cased); `notificationSenders`, `botNames`, `metadataFilters` and
  `authorPrefixes` at most 32 entries each (`api.MaxJiraListEntries`), each
  at most 512 bytes of valid UTF-8 without control characters, and every
  `metadataFilters` entry a valid RE2 pattern;
- `syncIntervalSeconds` 0 or ≥ 60;
- `credentials.password` only when an endpoint uses `password`, or for a
  `jira` account (its token);
  `credentials.oauthSession` only with source `daemon`, and it must name a
  completed session whose verified mailbox is the account's address (a
  sign-in that named no mailbox is refused), for the same provider and
  the client (id and tenant) the account resolves to now; a re-sign-in
  session (`account.oauthStart` with `accountId`) is accepted only by
  `account.update` / `account.test` of that same `accountId`.

The password is optional (an account without one ends in `authRequired`
once syncing exists). It is written to the system keyring
(`org.freedesktop.secrets`) and discarded; if the keyring refuses it nothing
is kept and `keyringError` is returned. That happens when no Secret Service
is running, when the user dismisses the unlock prompt, or when the daemon
runs with `MALACHI_KEYRING=none`. For `oauth2` no credentials are passed.
A `graph` account, and an `oauth2` account with `source: "goa"`, need no
keyring: adding them works with `MALACHI_KEYRING=none`, and a sign-in that
GNOME Online Accounts has lost surfaces as `authRequired` until the user
signs in again in the desktop's account settings.

An account with source `daemon` takes `credentials.oauthSession`: the
refresh token of that completed session is written to the keyring and the
session is consumed; if the keyring refuses, nothing is kept and
`keyringError` is returned. Added without a session — e.g. imported from
`config.toml` — the account starts in `authRequired` and the backend
opens a sign-in session for it at once (`notify.authRequired` with
`authUrl`, §5). Under `MALACHI_KEYRING=none` such an account can exist
(added without a session) but never signs in: its completed re-sign-in
fails with `keyringError`, the account stays in `authRequired`, and
`notify.authRequired` reports reason `keyringError` without a URL.

#### `account.remove`
- params: `{ "accountId", "deleteLocalData": bool }`
- result: `{}`
- errors: invalidArgument, accountNotFound, storageError

The mail cache (folders, messages, raw files, operation log) is always
removed with the account; the syncer is stopped first. A `jira` account
takes its issues with it, and the notification mails it hid in mail
accounts (`notificationMail: "hide"`) are shown again
(`notify.messagesChanged`).
`deleteLocalData: true` also deletes the account's drafts and attachments
(rows and files); `false` keeps them, orphaned, until a later phase defines
what happens to local data of a removed account. Keyring secrets are
deleted best-effort: an unavailable keyring never keeps the account alive.
A waiting `account.oauthStart` sign-in of the account is cancelled.

#### `account.setEnabled`
- params: `{ "accountId", "enabled": bool }`
- result: `{}`
- errors: invalidArgument, accountNotFound, storageError

Pauses (`false`) or resumes (`true`) an account. A paused account keeps its
configuration and local data, is never synchronised and reports
`state.status = "disabled"`. A paused `jira` account shows the
notification mails it hid in the mail accounts (`notificationMail:
"hide"`), and takes no notification mail; resumed, it hides them again
(`notify.messagesChanged`).

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
- params: `{ "accountId", "config": AccountConfig, "credentials": { "password": "…" (opt), "oauthSession": "s_…" (opt) } }`
- result: `{}`
- errors: invalidArgument (same rules as `account.add`), accountNotFound,
  conflict (another account of the same realm already uses the e-mail),
  keyringError, oauthClientMissing, storageError

Replaces the whole configuration; `enabled` is not touched. An empty
password keeps the stored one — except for a `jira` account whose site
(its realm) changes, an account that becomes a `jira` one, and a `jira`
account that becomes one with a `password` endpoint: a secret is never
sent to a site it was not given for, so such a change needs
`credentials.password` (invalidArgument without it). A given password
replaces it in the
keyring, and if the keyring refuses, the configuration is reverted so the
row and the keyring never disagree. `credentials.oauthSession` replaces
the stored refresh token the same way; if the keyring refuses, the session
is kept so the UI can retry. A configuration that leaves source `daemon`
deletes the stored refresh token (best effort); a given session or such a
change cancels a waiting re-sign-in of the account. Without a session, a
`daemon` account whose normalised address, provider or effective client
(`clientId` / `tenantId` against the configured one; no tenant counts as
`common`) changes loses its stored sign-in the same way: the refresh
token is deleted, a waiting re-sign-in is cancelled and a new one opened
for the new configuration, so the engines' `notify.authRequired` carries
its `authUrl` (§5); moving an account to source `daemon` without a
session opens one likewise. A `jira` account's notification mail follows
its new configuration (`notificationMail`, `notificationSenders`,
`spaces`, `offlineDays`): what it no longer hides is shown, what it hides
now is hidden (`notify.messagesChanged`). Emits `notify.accountsChanged`.

#### `account.discover`
Suggests server settings for an address. Nothing is stored and nothing is
authenticated; the UI still asks for the password and should run
`account.test`.

- params: `{ "email": "me@example.org" }`
- result: `{ "config": AccountConfig (opt), "source": "goa|ispdb|autoconfig|srv|provider|guess|none", "providerName": "…" (opt), "alternatives": [AccountConfig] (opt) }`
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
4. `provider`: the domain is hosted by a provider that signs in with
   OAuth2. Microsoft 365 is recognised by an MX record under
   `mail.protection.outlook.com` or by Microsoft hosts in an autoconfig
   answer; Google by the `gmail.com` / `googlemail.com` domains, an MX
   record under `google.com`, or Google hosts in an autoconfig answer —
   and for Google this answer wins even over a password entry in the
   ISPDB. When GNOME Online Accounts is running, `config` is the account
   **without** `goaAccountId` (a `graph` account, or an `imap` one with
   `oauth2` endpoints): it does not pass `account.add`; the UI may have
   the user add the account in GNOME Online Accounts and re-run discovery
   (or use `account.linked`). Without GNOME Online Accounts `config` is
   the backend's own sign-in (source `daemon`, see `account.oauthStart`).
   `alternatives` then lists, in this order, the own sign-in (when it is
   not `config` already) and, for Google, the IMAP/SMTP account with
   `authMethod: "password"` for an app password (`imap.gmail.com:993`,
   `smtp.gmail.com:465`, username the address). The own sign-in is listed
   even when no OAuth client is configured; `account.oauthStart` then
   answers `oauthClientMissing`.
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
kind: `imap` and `smtp` concurrently, the `graph` mailbox, or the `jira`
site.

- params: same as `account.add`, plus `"accountId"` (opt): with it and an
  empty `credentials.password`, the stored password of that account is
  used; for source `daemon` the token of `credentials.oauthSession`
  (read, not consumed) or, with `accountId`, the stored sign-in
- result: `{ "imap": EndpointTestResult (imap), "smtp": EndpointTestResult (imap), "graph": EndpointTestResult (graph), "jira": EndpointTestResult (jira) }`
- errors: invalidArgument (also a `jira` account's `accountId` of another
  site); with `accountId` and `password` endpoints (or a `jira`
  account): accountNotFound, authRequired (no stored password),
  keyringError. Each
  endpoint reports its own outcome; for source `daemon` an unknown
  `accountId` is reported per endpoint as `accountNotFound`

```jsonc
EndpointTestResult { "ok": true, "error": Error (opt), "capabilities": ["IDLE","CONDSTORE"] (opt), "latencyMs": 120 }
```

An IMAP/SMTP probe dials, secures the connection (TLS 1.2+; STARTTLS is
mandatory when configured) and verifies the server's certificate against
the system trust store and the host name — or, when the endpoint pins one
(`certificateSha256`), accepts exactly that certificate and nothing else,
whatever its issuer, name or validity — authenticates with the password
and disconnects. Per-endpoint `error.code` is one of
`authFailed` (credentials rejected), `tlsError` (certificate, handshake,
STARTTLS not offered, or the server demanding TLS before login; `data`
says which and describes the certificate, §2, so a UI can show it and let
the user pin it after an explicit confirmation),
`networkError` (unresolvable, refused, connection dropped), `serverTimeout`
(no answer in time), `serverError` (protocol error or no usable
authentication mechanism — for an `oauth2` endpoint, no XOAUTH2 and no
OAUTHBEARER). For `oauth2` endpoints with `source: "goa"` the access
token stands in for the password; a token problem is both endpoints'
outcome before anything is dialled: `authRequired` (sign in again in GNOME
Online Accounts), `unavailable` (no session bus or no GNOME Online
Accounts). With `source: "daemon"` the token comes from the session or
the stored refresh token: `authRequired` when there is neither (or the
provider revoked it), `oauthClientMissing` when no client is configured.
An `oauth2` block without `source` reports `notImplemented`.
`capabilities` are the server's post-login IMAP CAPABILITY
list or the EHLO keywords the backend knows about, scrubbed to printable
ASCII, ≤ 64 entries; `latencyMs` is dial → ready (greeting read, STARTTLS
done). Budget: 10 s to connect, 20 s per endpoint. The password is used
for the connections only and never logged.

A Graph probe fetches a token from the account's source and opens the
mailbox. `error.code` is `authRequired` when the source has no valid
sign-in (sign in again in GNOME Online Accounts, or through
`account.oauthStart` for source `daemon`), `unavailable` when there is no
session bus or no GNOME Online Accounts, `invalidArgument` when the
signed-in mailbox is not `config.email`, otherwise the network/server
codes above; `capabilities` is `["graph"]`.

A Jira probe signs in to the site with the token (with `accountId` and an
empty `credentials.password`, the account's stored one, used only when
that account is a `jira` account of the same site: invalidArgument
otherwise) and reads the signed-in user. `error.code` is `authFailed` (the site refused the token,
on Cloud also through the API gateway), `authRequired` (no token given and
no `accountId`), otherwise the network, TLS and server codes above
(`serverError` also for a site that is not Jira);
`capabilities` is `["cloud"]` or `["datacenter"]`, plus `"gateway"` when
the site was reached through the Atlassian API gateway.

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
`configured` says a Malachi mail account with that address exists already
(a `jira` account with the same address does not count).
`attentionNeeded` mirrors GNOME Online Accounts: the service wants the
user to sign in again; adding the account still works, syncing will report
`authRequired` until then. `name` and `email` are untrusted text from the
service; `email` is always a bare valid address.

#### `account.oauthStart`
Begins the backend's own sign-in (source `daemon`): for a new account
(`config`, e.g. an entry of `account.discover`) or to sign an existing
`daemon` account in again (`accountId`). The backend creates a PKCE
verifier and a random `state`, opens a listener on `127.0.0.1` on a free
port and returns the provider's authorisation URL. **The UI opens
`authUrl` in the user's browser**; the backend never launches one. When
the provider redirects back, the backend exchanges the code, checks that
the signed-in mailbox is `config.email` (Google: the `email` of the ID
token, which must carry `email_verified: true`; Microsoft: Graph `/me`'s
`mail`, else its `userPrincipalName`), answers the browser with a short
page and closes the listener.

- params: `{ "config": AccountConfig (opt), "accountId": "acc_1" (opt), "browserPage": OAuthBrowserPage (opt) }` — exactly one of `config` and `accountId`
- result: `{ "sessionId": "s_…", "authUrl": "https://accounts.google.com/…", "expiresAt": "2026-09-25T10:10:00Z" }`
- errors: invalidArgument (neither or both of `config`/`accountId`, a
  config that is not source `daemon` or fails `account.add` validation,
  an `accountId` whose account does not use the own sign-in),
  accountNotFound, oauthClientMissing, unavailable (too many sign-ins
  under way, at most 8)

```jsonc
OAuthBrowserPage { "successTitle": "Signed in" (opt), "successText": "You can close this tab." (opt),
                   "failureTitle": "Sign-in failed" (opt), "failureText": "Return to Malachi Mail." (opt) }
```

The page texts come from the UI in the user's language: plain text, at
most 200 characters each, escaped by the backend (no markup, no script,
`Cache-Control: no-store`); omitted texts leave a page with a symbol and,
on failure, the provider's technical error name only. A session lives
10 minutes (`expiresAt`); the listener accepts one callback with the
expected `state` and nothing else (requests naming another `Host` than
`127.0.0.1:<port>`, or a wrong `state`, are refused without ending the
session). For `accountId` at most one session exists per account: a
second call returns the pending one (with the new page texts, if any are
given) while it is still for the account's provider, client and address,
and replaces it otherwise. A completed re-sign-in stores the new refresh
token, restarts the account's syncer and sender, and is followed by
`notify.syncState`; if the account was changed to another address or
client, or removed, meanwhile, nothing is stored and the session fails.

#### `account.oauthWait`
Waits for a session to finish.

- params: `{ "sessionId": "s_…" }`
- result: `{ "status": "pending|complete", "config": AccountConfig (with complete) }`
- errors: invalidArgument (unknown session), cancelled (the user denied
  access in the browser, or `account.oauthCancel`), serverTimeout (the
  session expired), authFailed (the provider refused the code or the
  callback was malformed), invalidArgument with `data.signedInAs` (the
  browser signed in to another mailbox), networkError, tlsError,
  serverError; for a re-sign-in also keyringError (the keyring can never
  hold the token, `MALACHI_KEYRING=none`), invalidArgument (the account
  changed during the sign-in) and accountNotFound (it was removed)

Blocks at most 60 s and answers `pending` when the browser has not come
back yet; the UI calls again. `complete` carries the config to pass to
`account.test` and `account.add` together with `credentials.oauthSession`
(for a re-sign-in the backend has stored the token already and `config`
is the account's). A finished session's outcome stays readable until the
session expires or is consumed.

#### `account.oauthCancel`
- params: `{ "sessionId": "s_…" }`
- result: `{}`
- errors: none (an unknown session is ignored)

A pending session: closes the listener and ends a waiting
`account.oauthWait` with `cancelled`. A completed session is discarded:
its tokens leave the backend's memory and it can no longer be passed as
`credentials.oauthSession` (a re-sign-in's token is stored already). A
failed, cancelled or expired session is left as it is.

#### `account.detectSite`
Finds out what kind of issue-tracker site a URL names, for the
add-account flow of a `jira` account. Nothing is stored and nothing is
authenticated.

- params: `{ "url": "acme.atlassian.net" }` — a host name or a full URL; `https` is assumed
- result: `{ "kind": "jira", "siteUrl": "https://acme.atlassian.net", "deployment": "cloud|datacenter",
  "cloudId": "UUID" (opt), "title": "…" (opt), "version": "…" (opt) }`
- errors: invalidArgument (not a usable URL), networkError, tlsError,
  serverTimeout, serverError (the site answered, but it is not Jira)

The daemon asks the site anonymously what it is (its server info and, for
Atlassian Cloud, its tenant info), following redirects to `https` only.
`siteUrl` is the normalised URL a `JiraConfig` needs (§4.1), `cloudId` the
id of a Cloud site, which enables the API gateway route. `title` and
`version` are untrusted display text from the site; a Data Center site
that refuses anonymous requests is still recognised, without them. A
client allows the call 15 s.

#### `account.listSpaces`
Signs in to the site of a `jira` configuration and lists what the
account settings choose from: the spaces, the site's statuses (for
`closedStatuses`) and the user the token belongs to. Nothing is stored.

- params: `{ "accountId" (opt), "config": AccountConfig, "credentials": { "password": "…" (opt) },
  "counts": bool (opt) }`
- result: `{ "user": SiteUser, "spaces": [Space], "statuses": [IssueStatus] }`
- errors: invalidArgument (`config` is not of kind `jira`, its
  connection fields fail the `account.add` rules, or `accountId` names an
  account of another site), authRequired (no token
  given and no `accountId`), authFailed (the site refused the token),
  keyringError, accountNotFound, networkError, tlsError, serverTimeout,
  serverError

```jsonc
SiteUser    { "name": "Jana Dvořáková", "email": "jana@example.org" (opt) }
Space       { "id": "10001", "key": "ITSD", "name": "IT Service Desk", "serviceDesk": true (opt),
              "issues": 120 }                        // -1 = not counted
IssueStatus { "id": "3", "name": "In Progress", "category": "todo|inProgress|done" }
```

Of `config` only the connection is validated, by the `account.add`
rules: `kind` `jira` and nothing of the other kinds, `siteUrl`,
`deployment`, `login`, `cloudId` and `offlineDays`. `name`, `email` and
`jira.spaces` may be missing: the call is how the spaces are chosen, and
how a Data Center wizard learns the address. With `accountId` and an
empty `credentials.password` the stored token of that account is used, so
an existing account's settings can be edited without typing it again;
only when that account is a `jira` account of the same site (its realm),
invalidArgument otherwise — a token never goes to another site.
`spaces` are the spaces the user may browse, by name, at most 1000;
`serviceDesk` marks a service-desk space (its comments may be internal).
With `counts` every space's `issues` is an estimate of its issues updated
within `jira.offlineDays` (30 when 0), otherwise -1. `category` of a
status is an open enum, as in §3. Every name is untrusted display text
from the site; `user.email` is absent when the site does not reveal it (a
Data Center wizard then asks for the address). A client allows the call
45 s.

### 4.2 folder

#### `folder.list`
- params: `{ "accountId", "includeUnsubscribed": bool (opt) }`
- result: `{ "folders": [Folder] }`

```jsonc
Folder { "id": "f_1", "accountId", "parentId" (opt), "name": "Inbox", "path": "Inbox",
         "role": "none|inbox|sent|drafts|trash|junk|archive|all|outbox",
         "subscribed": true, "selectable": true, "synced": true, "unread": 3, "total": 120,
         "virtual": "assignedToMe|watching|open" (opt) }
```

Folder lists are not paginated: even large accounts have at most a few
thousand folders. `synced: false` marks a folder the daemon lists and
accepts moves into but never downloads: a server's `\All` folder, which
holds every message the other folders already hold. On Gmail that is All
Mail, and there it carries the `archive` role (unless the server marks a
folder `\Archive` itself), since moving a message out of the inbox into
All Mail is what Gmail calls archiving; `unread` and `total` are 0 and
`message.list` on it is empty. Gmail's Important and Starred (`\Important`,
`\Flagged`) are views of the same messages and are not listed at all.
`outbox` is a local pseudo-folder holding queued
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
successful sync the list is empty (not an error). `unread` and `total`
never count hidden messages (a notification mail hidden by a `jira`
account, §4.1).

A `jira` account lists its views first — `assignedToMe` (issues assigned
to the user), `watching` (issues the user watches) and `open` (issues not
in a closed status), those of `jira.disabledFolders` left out — and then
one folder per selected space, by name; every one has role `none`. A view
carries `virtual` with its code, and a client names it by that code (its
`name` is an English fallback). Its messages are copies of the messages in
the space folders, restricted to the issues in view, with ids of their
own and the same `threadId`; flags set on one copy apply to all of them
(`message.flag`, §4.3). Views cover the selected spaces only. Once a
comment is queued the account has the outbox folder (role `outbox`) like
any other (§4.3 `message.send`).

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
Clients refresh from the start on `notify.newMessage` and
`notify.messagesChanged`. `page.total` is the folder's local count after
`filter`. Only messages within the `offlineDays` window exist locally. A
hidden message (a notification mail a `jira` account hides, §4.1) is not
listed; `message.get` still returns it by id. `threadId` names the
conversation the message belongs to: an opaque id, assigned when the
message is stored, the same for every member across the account's
folders (Microsoft Graph accounts carry the server's conversation id,
other accounts a locally computed one; see `docs/architecture.md` §3.4). It is empty only for a
message stored by an older daemon that has not been linked yet, and it
can change when two partial conversations turn out to be one (the larger
keeps its id).

#### `message.get`
- params: `{ "accountId", "messageId" }`
- result: `{ "message": Message }`
- errors: invalidArgument, accountNotFound, messageNotFound, storageError

`attachments` come from the server's `BODYSTRUCTURE` at header sync and are
refined from the parsed MIME once the body is downloaded; `headers` is the
curated subset parsed from the body (empty before it is downloaded). An
attachment marked `remote` is on the mail server only (§3).

#### `message.body`
**The only method that returns message content, and it returns only
sanitised content.**

- params: `{ "accountId", "messageId", "remoteContent": "block" | "allow" (opt, per-call override), "trimQuoted": true (opt) }`
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
  "remotePictures": 0,                   // (opt) pictures of inlineParts kept on the server only
  "remoteContent": "block",              // the policy that was applied
  "sanitizerVersion": "1",
  "quotedTrimmed": true                  // (opt) trimQuoted cut a quoted history off
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

`remotePictures` counts the pictures of `inlineParts` that are kept on the
mail server only and not available on this device now: under
`neverStoreAttachments` (§4.8) a picture the HTML shows of 100 KiB or more
is not stored. Their `malachi-cid:` URLs stay in `html`, but `message.part`
answers `partNotDownloaded` for them, so a client offers to download them;
after `message.download` (which under `neverStoreAttachments` holds the
message in the daemon's memory, and otherwise stores it whole) it asks for
the body again, and the count is 0 while the pictures are available. A
picture the stored file turned out to lack (after a crash) counts too.
Showing a message never makes the daemon contact the mail server.

`trimQuoted` asks for the body without the quoted history of a reply, as
Gmail's "trimmed content" does: the client shows the result with a button
(•••) under it and asks again without `trimQuoted` to show the whole
message. `quotedTrimmed` is `true` only when something was actually cut
(absent otherwise); without `trimQuoted` the result is exactly what it
would be without the parameter. The view runs without JavaScript, so the
cut is the daemon's: the sanitiser removes the history from the parsed
tree before its walk (`backend/internal/sanitize/quote.go`), so the
trimmed `html` carries every guarantee above, and `blocked`, `links`,
`inlineParts` and `remotePictures` describe the trimmed body (under
`allow` only its images are fetched). `text` is cut to match: the text
alternative cut by the plain-text rules when they find the quote in it,
otherwise the text rendering of the trimmed `html`. A message without
HTML, or whose HTML is withheld, has its `text` cut by the plain-text
rules. The detection is conservative (when in doubt, everything is shown)
and takes the first of these markers, in document order, that qualifies:

- a nested quote, cut only when nothing visible follows it at any level
  (a reply written below or between quoted passages is never trimmed):
  Gmail's `class="gmail_quote"` / `gmail_quote_container`, a
  `<blockquote type="cite">` (Apple Mail, Thunderbird), any `<blockquote>`
  right after an attribution line; the attribution line before it
  ("On … wrote:", "Dne … napsal(a):", "Am … schrieb …:", Gmail's
  `gmail_attr`, Thunderbird's `moz-cite-prefix`) goes with it;
- the start of a history, which runs to the end of the body: Outlook on
  the web's `#divRplyFwdMsg`, `#appendonsend`,
  `#mail-editor-reference-message-container`; Outlook's separator (`<hr>`
  or a `<div>` with a top border) followed by a header block whose bold
  labels name From, then Sent or Date, and To or Subject (English, Czech,
  Slovak, German, French, Spanish, Italian, Dutch, Polish and more); a
  `-----Original Message-----` line or a translation of it;
- in plain text: a `-----Original Message-----` line, a line of
  underscores followed by such a header block, such a header block on
  its own (Outlook's text alternative of an HTML reply: at least three
  `Label: value` lines in a row at the start of their lines, after a
  blank line whose nearest text above does not end with a colon — Apple
  Mail's `Begin forwarded message:` stays whole —, From first and with a
  value, a Sent or Date with a digit, a To or Subject, closed by a blank
  line with the quoted message below), or an attribution line followed
  by nothing but `>`-quoted and blank lines.

Empty elements, line breaks and separators right before the cut go with
it. Nothing is cut when the cut part shows nothing, or when nothing would
be left to show above it: a forward that is only the forwarded message
stays whole, and so does a bare quote. The trimming is not part of the
ruleset `sanitizerVersion` names (the whole body is the same with or
without it). `draft.create` quotes the whole body and the MCP bridge
reads it whole.

`bodyState` says whether content exists at all: `pending` (the sync engine
has not downloaded the body yet; `text` empty), `tooBig` (over the daemon's
raw-message cap, never downloaded), `failed` (downloaded but unparsable),
`fetched`. Bodies are downloaded for every message within the `offlineDays`
window. Under `attachmentOfflineDays` (§4.8) the large attachments of older
messages stay on the server (`Attachment.remote`); `message.download`
fetches them, and a body still `pending`, when the user asks for them.

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
than use its usual short one. That includes a call without `remoteContent`:
the stored preference or a known sender may resolve it to `allow`, and the
client cannot tell in advance.

#### `message.part`
- params: `{ "accountId", "messageId", "partId" }`
- result: `{ "partId", "contentType", "filename", "size", "data" }` (`data`
  is base64; `filename` is sanitised as in `Attachment`)
- errors: invalidArgument (missing ids, `partId` not a part number such as
  `2` or `1.2`), accountNotFound, messageNotFound, partNotFound (no such
  leaf part — a multipart container cannot be fetched — or the message's
  content is no longer stored), partNotDownloaded (the part is `remote`:
  call `message.download` first), attachmentTooBig (`data` would exceed
  `api.MaxAttachmentDataBytes`; `error.data` = `{ "limit": bytes }`),
  malformedMessage, storageError

The decoded content of one MIME part of a received message, addressed by
the `partId` that `Attachment` and `inlineParts` carry and that a
`malachi-cid:` URL in `html` ends with. The webview's scheme handler uses it
for inline images (and serves only `image/*` other than SVG from it);
opening, previewing and saving an attachment use it too. The part is read
from the raw message each time; nothing is cached. It never contacts the
mail server: a `remote` part, including a picture of `inlineParts` that
`message.body` counts in `remotePictures`, answers `partNotDownloaded` —
unless `message.download` put the message into the daemon's memory under
`neverStoreAttachments` (§4.8), which serves it until the copy is
dropped.

#### `message.embedded`
- params: `{ "accountId", "messageId", "partId", "remoteContent": "block" | "allow" (opt) }`
- result: `{ "partId", "message": Message, "body": MessageBodyResult }`
- errors: invalidArgument (missing ids, `partId` not a part number, bad
  `remoteContent`, the part is not an attached message), accountNotFound,
  messageNotFound, partNotFound (as in `message.part`), partNotDownloaded
  (the attached message is `remote`: call `message.download` first),
  attachmentTooBig (the part exceeds `api.MaxAttachmentDataBytes`),
  malformedMessage (the containing message or the attached one cannot be
  parsed), storageError

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

#### `message.download`
- params: `{ "accountId", "messageId" }`
- result: `{ "message": Message }` (as `message.get` reports it afterwards)
- errors: invalidArgument, accountNotFound, messageNotFound, unavailable
  (the account is paused, or a local move of the message has not reached
  the server yet: retry after the next sync; or the message changed twice
  while the download was being stored, the stored file was being read for
  longer than the daemon waits to replace it (Windows), or the server
  refused it for the moment, IMAP `[UNAVAILABLE]`, `[INUSE]` or `[LIMIT]`:
  try again),
  messageGone (the server no longer has it: its mailbox or UID does not
  exist, or its UIDVALIDITY changed; any other refusal is serverError),
  attachmentTooBig (over the daemon's raw-message cap, `bodyState: tooBig`,
  or announced over it by the server, which is then not read; a `pending`
  body found over it is marked `tooBig`),
  offline, networkError (also a transfer that broke off), serverError
  (also a download that is not the stored message, and under
  `neverStoreAttachments` one that lacks a part the stored message keeps
  on the server), tlsError,
  serverTimeout (not done within 4 minutes), authRequired, authFailed,
  keyringError, malformedMessage (a `pending` body that does not parse is
  marked `failed`), storageError, cancelled (the caller gave up while the
  download goes on, or the daemon is stopping)

Makes the whole message available locally: it downloads the message again
from the account's mail server and stores it complete, so that no
attachment is `remote` any more and `message.part`, `message.embedded`,
Save All and a forward (`draft.create`) work on it. A body that is still
`pending` is downloaded the same way. It only reads the mail server (IMAP
`EXAMINE` and `BODY.PEEK`; nothing is marked read) and only on a user action
— never because content asks, and never from the webview's `malachi-cid:`
requests: a picture kept on the server (`message.body` `remotePictures`)
is fetched only when the user asks for the message's pictures.

On a `jira` account there is no stored message on a server to download:
the daemon reads the item from the site again and builds the message the
way the sync does, so the result replaces what the client shows;
`messageGone` when the site no longer has the item (deleted, or its issue
out of reach), and the next sync removes it.

A message with nothing missing answers at once, without contacting the
server; `failed` messages answer at once too. Calls for the same message
share one download, and an account runs at most two at a time. The
download finishes even when the caller gives up (the call is idempotent),
within the daemon's budget of 4 minutes; a client should wait at least
5 minutes. A downloaded message stays complete for 7 days before the
background pass may keep its attachments on the server again. On
Microsoft 365 accounts the server rebuilds the message, so part ids may
change: a client replaces the message it shows with the result.

Under `neverStoreAttachments` (§4.8) nothing is written to the store: the
downloaded message is held in the daemon's memory (at most 256 MiB for all
such messages, the least recently used dropped first, each dropped after
30 minutes unused, all of them when the daemon quits or the preference is
switched off, and an account's when it is removed or paused). A message
already held answers at once, without contacting the server, and counts as
used. A download is held only when it has every part the stored message
keeps on the server (a Microsoft 365 message rebuilt with such a part
named or typed anew is not): otherwise the call is `serverError` and the
next call downloads again. The preference applies as it is when the
message arrives from the server; a message stored whole because it was
switched on meanwhile loses its attachments in the background (§4.8).
The result still marks the attachments `remote`; `message.part`,
`message.embedded`, `draft.create` and `draft.open` take them from memory
while the copy lasts, and answer as for any `remote` part once it is gone,
so a client calls `message.download` again. That holds in the Drafts folder
too: a reduced message moved there is held, not stored whole. A body still
`pending` is stored as usual but without its attachments, and the whole
message is held in memory too; one the preference stores whole anyway
(§4.8: Drafts, Outbox, signed or encrypted, …) is stored whole and not
held.

#### `message.unsubscribe`
- params: `{ "accountId", "messageId", "method": "mailto" (opt) }`
- result: `{ "outcome": "unsubscribed|queued|openUrl|unverified",
  "url": "https://…" (opt, `openUrl` only), "mailto": "address" (opt,
  `unverified` only), "unsubscribedAt": Time (opt) }`
- errors: invalidArgument (an `issue-tracker` account, a message in the
  junk folder or flagged `junk`, a message that offers nothing usable, a
  `method` other than `mailto`, `mailto` for a message without a
  `mailto:` address),
  accountNotFound, messageNotFound, unsubscribeFailed (1505), networkError
  (the sender's server could not be reached), storageError; and the errors
  of `message.download` when the message has to be fetched first
  (messageGone, unavailable, …), conflict (another `message.unsubscribe`
  for the same list or sender of the account is running)

Acts on the unsubscribe offer of `message.get`. The client sends only the
message: the daemon reads the headers again from the stored message and
never takes a URL or address from the caller; `method`, when given, can
only be `mailto`: the message's `mailto:` alternative, which a client asks
for after the user confirmed an `unverified` outcome (empty = the offer's
own method). It is never automatic; a client asks the user first (what is sent, and to whom,
`target`), and never calls it for a `url` offer, which it opens itself.

- `url`: nothing is sent, nothing is remembered; `outcome: "openUrl"` with
  the page in `url` (https only). Only this method ever yields `openUrl`.
- `oneClick`: the request is verified first, by the rule of the account's
  kind. **IMAP and Gmail accounts:** the daemon needs the whole message (it
  downloads it like `message.download` when it is not stored whole; under
  `neverStoreAttachments` from the copy it holds in memory) and verifies
  its DKIM signatures (at most five, 10 s for the DNS lookups): a
  signature counts only when it is valid, its `d=` domain belongs to the
  same organisation (public suffix + 1) as the `From` domain, and it
  signs `From`, `List-Unsubscribe` and `List-Unsubscribe-Post`.
  **Microsoft 365 (Graph) accounts:** Exchange serves the message rebuilt,
  so its signatures no longer verify here; the request counts as verified
  when the topmost `Authentication-Results` field (the one Exchange
  prepends; any below it is ignored) has `dkim=pass` with a `header.d` of
  the `From` domain's organisation and the message has a `DKIM-Signature`
  with that same `d=` whose `h=` lists `From`, `List-Unsubscribe` and
  `List-Unsubscribe-Post`; only the headers are needed. For either kind a
  message with more than one `From`, `List-Unsubscribe` or
  `List-Unsubscribe-Post` field is not verified. Without verification
  nothing is sent and the result is `outcome: "unverified"` with, when the
  message also has a `mailto:` address, that address in `mailto`; **the
  one-click URL is never returned** (a one-click endpoint need not answer
  a browser's GET). The client may then ask the user and call again with
  `method: "mailto"`. When verified the daemon POSTs
  `List-Unsubscribe=One-Click` (`application/x-www-form-urlencoded`) to
  the https URI and answers `outcome: "unsubscribed"` with
  `unsubscribedAt` after a 2xx status. Redirects are not followed (a 3xx
  status is a failure), the request has a 15 s timeout, no cookies, and it
  refuses to connect to loopback, private, link-local, unspecified,
  multicast or carrier-grade-NAT addresses (`docs/security.md` §7.2). Any
  other status is unsubscribeFailed, a connection that could not be made
  networkError.
- `mailto` (the offer's method, or `method: "mailto"`): the daemon queues a plain-text message in the outbox of the
  account the message arrived in, from that account's address, to the
  first address of the `mailto:` URI with its `subject` (default
  `unsubscribe`) and `body`; no draft is left behind, and it is delivered
  and copied to Sent like any outbox message. `outcome: "queued"` with
  `unsubscribedAt`.

A repeat for the same list or sender within 60 seconds of an
unsubscription is answered with that outcome (`unsubscribed` for
`oneClick`, `queued` for `mailto`) and its `unsubscribedAt`, without a
second request or mail. Before the request the daemon also refuses a URL
whose host is a literal address it does not connect to, a single-label
name, or one ending in `.local`, `.localhost`, `.internal` or
`.home.arpa`, so that a configured proxy cannot be used to reach them.

`unsubscribedAt` is remembered (`unsubscribed` and `queued` only) for the
list or sender; `message.get` then reports it in the offer.

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
on the next sync, after the queued change has been pushed. On a `jira`
account the flags are local only (the site has no read state): nothing is
sent to the site, and the copies of the message in the account's other
folders (§4.2) take the same flags.

#### `message.move`
- params: `{ "accountId", "messageIds": [..], "targetFolderId" }`
- result: `{}`
- errors: invalidArgument (ids as above, target not selectable, an account
  without the `move` capability, §4.1), accountNotFound, folderNotFound
  (unknown target), messageNotFound, storageError

Local-first as above. Ids already in the target folder are ignored. The
moved message keeps its `id` (it is a local id, not the IMAP UID) — except
into a folder with `synced: false`: the server gets the move as usual,
but locally the message is gone at once, the way an archived message
leaves a Gmail inbox; it is not listed again until the server returns it
to a synchronised folder. Moving a message out of the Drafts folder
deletes the draft it is the copy of (§4.5).

#### `message.delete`
- params: `{ "accountId", "messageIds": [..], "permanent": bool (opt) }`
- result: `{}`
- errors: invalidArgument (also an account without the `delete`
  capability, §4.1, unless every id is an outbox message), accountNotFound,
  folderNotFound (no folder with role `trash` while `permanent` is false),
  messageNotFound, storageError

With `permanent: false` (default) messages not already in the Trash role
folder are moved there (same rules as `message.move`); messages already in
Trash, or any message with `permanent: true`, are removed from the store at
once and expunged on the server by the syncer. All-or-nothing per call.
On Gmail, where an expunge only removes a label and the message stays in
All Mail, a message outside the Trash and Spam is moved to the Trash and
expunged there. Deleting (or moving) a message of the Drafts folder
deletes the draft it is the copy of (§4.5).

#### `message.send`
Builds the message from a saved draft and queues it into the outbox.

- params: `{ "accountId", "draftId", "version": 3 }`
- result: `{ "outboxId": "m_7" }` — the id of the queued message
- errors: accountNotFound, draftNotFound, conflict (version mismatch),
  invalidArgument (no recipients, or an invalid recipient address; a
  comment draft with recipients or attachments, whose issue is no longer
  stored, whose visibility the issue does not allow, or whose body is
  empty or longer than 32 767 characters in the site's format — see
  below),
  attachmentTooBig (built message over `api.MaxOutgoingMessageBytes`,
  36 MiB; `data` = `{ "limit", "size" }`), storageError

The result only confirms enqueueing. Delivery is asynchronous and runs
beside the IMAP sync: the queued message is an ordinary message in the
account's outbox folder (§4.2) with `flags: ["seen"]` and an `outbox`
field (§3) that carries its state; `notify.syncState` is emitted whenever
`pendingOutbox` or `failedOutbox` changes. A failed send stays in the
outbox with `state: "failed"` and the reason in `outbox.error`, and counts
in `failedOutbox`; it is never silently dropped.
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
names a stored message (or, when that message has left the store, from the
headers the draft kept when it was saved or taken over with `replaces`).
Every header value is stripped of control characters before it is
written. The draft's copy in the Drafts folder (§4.5) is deleted with it.

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

A comment draft (`Draft.comment`, §4.5) of a `jira` account is not an
e-mail: it has no recipients and no attachments, and it is queued like any
message but delivered by posting it as a comment to its issue with its
visibility. The queued message is `From` the user as the site names them
(else the account's `displayName`) at the account's `email`, its subject
is the issue's, and while it waits it is a member of the issue's thread
(§4.4). Its body is converted to the site's own format — the Atlassian
Document Format on Cloud, wiki markup on Data Center — from the sanitised
HTML (or the text of a plain-text draft): paragraphs, line breaks, bold,
italic, underline, strike-through, code, code blocks, headings, lists,
quotes and rules are kept, links only to `http`, `https` and `mailto`
targets, pictures are dropped and everything else becomes its text; the
user's text is never read as the site's markup. `message.send` refuses a
comment that is empty after the conversion or longer than 32 767
characters (Jira's limit; on Cloud the document's JSON counts). An
`internal` comment carries Jira Service Management's `sd.public.comment`
property; every comment carries `io.github.schotek.malachi.outbox` with the
outbox message's id, by which a retry after an attempt whose answer was
lost finds the comment the site took, instead of posting it twice. The
outbox rules above apply (states, retries with backoff, `failed` with the
reason, `outbox.retry`, `message.delete` to cancel): a refused token defers
the account's queue and sends `notify.authRequired`; the site's answers
400, 403, 404, 413 and any other 4xx fail for good; network failures,
timeouts, 429 and 5xx are retried. After the post the daemon refreshes the
issue and waits for it (at most 30 seconds), so the comment is normally in
the issue's thread (read, not announced) by the time the outbox message
goes; no Sent copy is kept, and neither known senders nor recipient
completion learn anything from a comment.

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

Threads group the messages of one account into conversations. The daemon
assigns every stored message a `threadId` as it arrives: from
`References`, `In-Reply-To` and shared `Message-ID`s for IMAP accounts
(the `References` field is fetched with the envelope, so the id is known
before the body is), from the server's conversation id for Microsoft Graph
accounts. Ids are opaque, never shared between accounts, and stable while
a conversation lives: a reply joins the existing conversation under its
id, and only two partial conversations found to be one merge under the
larger one's id (a client holding the old id gets `threadNotFound` and
lists again). A message without a `threadId` was
stored by an older daemon and not linked yet; a client shows it on its
own. The rules and caps are in `docs/architecture.md` §3.4.

Threads are computed per account and **displayed per folder**: a thread is
listed in a folder when at least one member is in that folder, and every
field of the `ThreadSummary` a folder listing returns describes the
members **in that folder** (a conversation with two messages in the inbox
and one in Sent has `messageCount: 2` in the inbox), except `folderIds`,
which always names every folder of the account with a member.

On a `jira` account a thread is one issue (§3): its id is the issue's,
its members are the description, the comments and the events, and the
`ThreadSummary` carries `issue` (`IssueInfo`, without the per-message
fields). `latest` may be an event; a client shows it from
`latest.issue.changes`. Hidden messages (§4.1 `notificationMail`) are no
member of any thread listing.

#### `thread.list`
- params: `{ "accountId", "folderId", "page": Page, "sort": SortOrder (opt),
  "filter": MessageFilter (opt) }`
- result: `{ "threads": [ThreadSummary], "page": PageInfo }`
- errors: invalidArgument (missing ids, unknown `sort` or `filter`, bad
  cursor), accountNotFound, folderNotFound, storageError

```jsonc
ThreadSummary { "id": "t_9", "accountId": "acc_1",
                "subject": "Lunch",                 // the latest member's, Re:/Fwd: stripped
                "participants": [Address],         // distinct senders, newest first, ≤ 8
                "messageCount": 3, "unreadCount": 1,
                "latestDate": Time,
                "latest": MessageSummary,          // the newest member, in full
                "snippet": "…",                    // of the latest member
                "flags": ["flagged", "seen"],      // union over the members
                "hasAttachments": true,
                "folderIds": ["f_inbox", "f_sent"],
                "issue": IssueInfo (opt),          // a jira account's thread
                "sentCount": 1 }                   // the user's replies in Sent the folder lacks
```

- Order: by the date of the latest member in the folder, `dateDesc` by
  default, ties by thread id; a new reply moves its thread to the top.
- `latest` is the member `latestDate`, `subject` and `snippet` come from,
  as `message.list` would return it (with `outbox` in the outbox folder),
  so a client shows a conversation without a `thread.get` round trip.
- `subject` is that member's subject with reply and forward markers
  removed (`Re:`, `Fwd:`, `AW:`, `Re[2]:` and the like; the raw subject
  when nothing is left), so a thread does not rename itself as replies
  arrive.
- `participants`: the distinct `from` addresses of the members, newest
  first, compared case-insensitively by address, the display name as the
  newest member that carries one wrote it; at most
  `api.MaxThreadParticipants` (8), taken from the newest 64 members.
- `flags`: every flag some member carries. Read state comes from
  `unreadCount`; `seen` here only says that some member was read.
- `sentCount`: how many members of the thread in the account's folders of
  role `sent` the folder lacks, so that a client shows a conversation of
  one message and the user's reply to it as a conversation. A sent member
  whose `Message-ID` header a member of the folder carries (a Bcc to
  oneself, a Gmail label) is the folder's; members sharing a `Message-ID`
  count once, a member without one counts on its own; hidden messages do
  not count. It is part of no other field (`messageCount`, `unreadCount`,
  `participants`, … are the folder's). Always 0 in a folder of role
  `sent`, in the outbox, in a `jira` account (it has no sent folder) and in
  the account-wide summary of `thread.get`.
- `filter`: `unread` keeps threads with an unread member in the folder,
  `flagged` those with a flagged member; `page.total` counts threads after
  the filter.
- Cursors follow §4.3: bound to `sort`, not to `filter`, stable across
  syncs. A cursor from `message.list` is rejected here (invalidArgument)
  and vice versa.

#### `thread.get`
- params: `{ "accountId", "threadId", "folderId" (opt), "withSent": bool (opt) }`
- result: `{ "thread": ThreadSummary, "messages": [MessageSummary],
  "sent": [MessageSummary] (opt) }`
- errors: invalidArgument, accountNotFound, folderNotFound (a `folderId`
  that is not the account's), threadNotFound (no member in the account,
  or none in `folderId` when given), storageError

`messages` are oldest first (`date`, then `id`). With `folderId` only the
members in that folder are returned and `thread` is aggregated over them,
exactly as `thread.list` of that folder reports it; without it every
member of the account is returned and `thread` covers them all
(`folderIds` is the same either way), each message once: the copies in
the views of a `jira` account (§4.2) are left out. At most
`api.MaxThreadMessages` (500) members are returned, the newest;
`messageCount` still counts them all. A member in the outbox folder carries `outbox` as in `message.list`.

`withSent` (with `folderId`; ignored without it) adds `sent`: the members
`thread.sentCount` counts — the user's replies in the account's sent
folders that the folder lacks, compared by `Message-ID` as well as by id,
one per `Message-ID` — oldest first, at most `api.MaxThreadMessages`, the
newest, each as `message.list` of its own folder would return it
(`folderId` is the sent folder's). A client places them in the
conversation by date; they are not members of the folder, and nothing of
`thread` describes them. `sent` is absent when there are none, without
`withSent`, in a sent folder, the outbox and a `jira` account. Nothing
announces a change of them on its own: a client showing them asks again
when `notify.newMessage` or a change in a sent folder names the thread.

Actions stay per message: `message.flag`, `message.move` and
`message.delete` take the `messageIds` of the members a client wants to
touch; there are no thread-level mutations and no thread notification.
`notify.newMessage` carries the new message's `threadId`, so a client
showing conversations merges the arrival into the thread row it shows.

### 4.5 draft

Drafts live in the store and, once saved, get a copy in the account's
folder with role `drafts` (below), unless they are local. The backend owns every derived field: it
sanitises `htmlBody` on the way **in**, derives `textBody` from it, assigns
attachment metadata and sets `updatedAt`.

```jsonc
Draft { "id": "d_1" (absent on first save), "accountId", "version": 1,
        "to": [Address], "cc": [Address] (opt), "bcc": [Address] (opt),
        "subject": "…",
        "textBody": "plain text",
        "htmlBody": "<p>…</p>" (opt; rich text),
        "inReplyTo": "m_123" (opt, local id), "forwarding": "m_124" (opt),
        "attachments": [DraftAttachment] (opt),
        "replaces": "m_125" (opt; draft.open → draft.save only),
        "comment": DraftComment (opt; a comment draft of a jira account),
        "local": true (opt; kept on this device, never in the Drafts folder),
        "updatedAt": Time }
DraftAttachment { "id": "att_…", "filename": "safe-name.pdf", "contentType": "application/pdf",
                  "size": 12345, "inline": false, "contentId": "…@malachi.local" (opt) }
DraftComment { "issue": IssueInfo, "visibility": "public|internal" }   // "" = public
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

A comment draft is a draft of a `jira` account that `draft.create`
`reply` made: `comment` names the issue it goes to (as the daemon last
synchronised it) and its `visibility`, `""` or `public` for everyone who
sees the issue, `internal` for the service-desk team only (allowed only
when `issue.commentVisibilities` has it). The issue is the one of the
message `inReplyTo` names, which every draft of a `jira` account must
have; in `draft.save` only `comment.visibility` is read of `comment`, and
the subject becomes the issue's (`KEY: Summary`). It has no recipients,
no attachments, no `forwarding` and no `replaces`, and it stays local: no
copy goes to a Drafts folder. `draft.list` returns `comment` with the issue
(empty when the issue is no longer stored). `message.send` posts it
(§4.3).

**Local drafts.** A draft with `local: true` stays in the daemon's store:
no syncer uploads it to the Drafts folder, no other client sees it, and it
leaves the device only when it is sent (`message.send`: the outbox, the
Sent copy, as any draft). It is a board case's suggested reply (§4.13).
`draft.save` reads `local` on the first save only (no `id`); later saves
keep the stored value, so a client can neither set nor clear it on an
existing draft, and `local` together with `replaces`, or `replaces` on a
local draft, is invalidArgument. Linking a draft to a case
(`board.setDraft`, `board.annotate` `draftId`) makes it local and deletes
a copy it already has in the Drafts folder (like `draft.delete` deletes
one) — except a Microsoft 365 copy that was changed on the server after
the upload (Outlook edits drafts in place): that one stays in the Drafts
folder as the user's own draft, no longer tied to this one. An upload
that was under way when the draft became local deletes the copy it made.
The MCP bridge makes its drafts local when it was started for the board
(`--reply-only`, `--triage-run`; `docs/mcp.md`). Nothing else creates a
local draft.

A local draft reaches the mail server only when the user sends it, with
one exception. The daemon remembers whether the user may have written in
it: a draft saved while a case links it (the board's own editor), or one
that was an ordinary draft when it was linked, counts as **edited**.
When a linked draft loses its case without `message.send` or
`board.discardDraft` — a thread merge that keeps the other case's reply,
the case deleted because its conversation was gone for a day, the end of
a done case's 30 days, the account removed with its local data kept — an
edited draft becomes an ordinary draft of the account (`local` false: it
is uploaded to the Drafts folder and the user finds it there), and an
untouched suggestion is deleted (`draft.delete`). Typed text is never
destroyed, and an untouched suggestion never reaches the server. In results
`local` is also true for every draft of a `jira` account, whose drafts
never reach a server folder.

The UI editor keeps its own live copy of the HTML; the backend's copy is
the one that is sent. `draft.save` therefore echoes what it stored
(`htmlBody`, `textBody`) and what it removed (`blocked`) so the UI can be
honest about removals. Reopening a draft always yields the sanitised form.

#### `draft.save`
- params: `{ "draft": Draft }`
- result: `{ "draftId": "d_1", "version": 2, "textBody": "…", "htmlBody": "…" (opt),
             "blocked": BlockedContent, "attachments": [DraftAttachment] (opt) }`
- errors: invalidArgument (limits, bad address, CR/LF in header fields,
  both `inReplyTo` and `forwarding`; `local` with `replaces`, or
  `replaces` on a local draft; on a comment draft recipients,
  attachments, `forwarding`, `replaces` or a `visibility` the issue does
  not allow; a draft of a `jira` account whose `inReplyTo` is missing or
  names no stored message of an issue of the account), conflict (stored version ≠ supplied
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

**The copy in the Drafts folder.** A draft whose newest version has
rested for 30 seconds (no `draft.save` since) is stored in the account's
folder with role `drafts` by the syncer: over IMAP an `APPEND` with
`\Draft` and `\Seen`, over Microsoft Graph `POST me/messages` with the
MIME message. The message is the one `message.send` would build, plus a
`Bcc` header, under a **fresh `Message-ID` for every upload**; the copy it
replaces is then deleted like a `message.delete` with `permanent: true`,
so every version ends up as exactly one message and the Drafts folder
shows the draft to every client. A copy that Microsoft Graph reports as
changed after the upload (Outlook edits drafts in place) is not deleted;
both stay. Uploads are retried with backoff; after 8 refused attempts a
draft waits for its next save. Without a Drafts folder a draft stays
on this device. A local draft is not uploaded, and it never holds a
copy. `version` is never touched by the upload. `draft.delete` and
`message.send` delete the copy too; a `message.move` or `message.delete`
of the copy (Trash included) deletes the draft it belongs to, whose
attachments are released rather than deleted, so that a compose window
still editing it saves it again as a new draft (its next `draft.save`
answers draftNotFound). Drafts saved by a daemon older than this
behaviour are not uploaded until they are saved again.

A message of the Drafts folder is opened for editing with `draft.open`.
`draft.save` with `replaces` (the id of such a message, only ever set by
`draft.open`) links the draft to it: the draft's first upload replaces
that message instead of leaving a second copy. A draft that already holds
the message is deleted in the same transaction when everything it has is
on the server (its attachments released); when it still has changes to
upload, `replaces` is ignored and both are kept. `replaces` naming a
message outside a Drafts folder of the account is invalidArgument, an
unknown one messageNotFound.

#### `draft.list`
- params: `{ "accountId", "page": Page }`
- result: `{ "drafts": [Draft], "page": PageInfo }` (newest `updatedAt` first; full bodies)

#### `draft.get`
- params: `{ "accountId", "draftId" }`
- result: `{ "draft": Draft }` — the stored draft exactly as one item of
  `draft.list` (sanitised compose HTML, `comment` on a comment draft,
  `local`), for a client that opens one draft by id (the board's editor of
  a suggested reply) without paging the list.
- errors: invalidArgument (`accountId` or `draftId` missing), draftNotFound
  (no such draft in the account), storageError

#### `draft.delete`
- params: `{ "accountId", "draftId" }`
- result: `{}` (deleting an unknown draft is not an error). Bound
  attachments are deleted with it, and its copy in the Drafts folder is
  removed from the store at once and deleted on the server by the syncer.

#### `draft.create`
Returns an **unsaved** template (`id` empty, `version` 0) with everything a
compose window needs pre-filled by the backend. Reply and forward logic
lives here so that every UI behaves the same; the one thing the client
supplies is the line above the quote, because it has a language and the
backend has none.

- params: `{ "accountId", "mode": "new" | "reply" | "replyAll" | "forward",
             "messageId" (opt; required unless mode is new),
             "mailto": "mailto:…" (opt, new only),
             "attribution": "On …, X wrote:" (opt; reply and forward),
             "messageAccountId" (opt; forward only) }`
- result: `{ "draft": Draft, "quoted": "html" | "text" | "none",
             "blocked": BlockedContent, "skipped": [Attachment] (opt) }`
- errors: invalidArgument (mode, a missing `messageId`, `mailto` or
  `messageId` with the wrong mode, an attribution over its caps or with
  control characters, a mode the account's capabilities do not allow — a
  forward needs `compose` of `accountId` and `forward` of the message's
  account —, `messageAccountId` with another mode than `forward`, a
  message of a `jira` account whose issue is not stored),
  accountNotFound (also `messageAccountId`),
  messageNotFound, storageError (a copy of a part could not be stored;
  nothing is left behind)

Per mode:

- `reply`: `to` is the original's `Reply-To`, else its `From`, without the
  account's own addresses; a message of one's own is answered to its `To`
  (again without oneself), a note to oneself to oneself. `subject` is
  `Re:` + the original's subject stripped of every `Re:`/`Fwd:`/`Fw:`/
  `AW:`/`WG:` marker (so the prefix never stacks; the marker is never
  translated, other clients only recognise the English form). `inReplyTo`
  is set; the threading headers are resolved at send time.
- `replyAll`: as `reply`, plus the original's `To` and `CC` as `cc`,
  without oneself and without whoever is already in `to`. Recipients are
  de-duplicated case-insensitively; an address a draft could not carry
  (unparsable) is dropped, a display name that would break a header is
  dropped from its address, so the first `draft.save` cannot fail on them.
- `forward`: no recipients, `Fwd:` + the stripped subject, `forwarding`
  set, and every part of the original that is not its body imported into
  the attachment store (see below).
- `new`: an empty draft, or the `mailto:` URI parsed — `to` (the path and
  `to=`), `cc`, `bcc`, `subject`, `body`; nothing else is interpreted,
  unusable addresses are dropped, the body is escaped into `htmlBody`.

On a `jira` account (capability `comment`) `reply` is the only mode: it
returns a comment draft of the message's issue (`comment` set, visibility
`""`, the subject `KEY: Summary`, `inReplyTo` the message, no recipients,
an empty body, `quoted: "none"`; `attribution` is not used); `new`,
`replyAll` and `forward` are invalidArgument there. A message of a `jira`
account is forwarded by e-mail from a mail account instead: `accountId` is
the mail account (capability `compose`), `messageId` the message and
`messageAccountId` its `jira` account (capability `forward`). The draft is
the mail account's, built as any forward (`Fwd:`, the quote, the parts
copied into the mail account's attachment store, `forwarding` naming the
message), and it is sent by e-mail like any other. `messageAccountId` may
name any other account of the user whose messages can be forwarded, a
mailbox too. Without it (or equal to `accountId`) the message is looked up
in `accountId`.

The quote. With `quoted: "html"` the original's HTML was sanitised **in
compose mode** — exactly what `draft.save` will do to it — and placed
under an empty paragraph for the answer: a reply as
`<p><br/></p><div>attribution</div><blockquote type="cite">…</blockquote>`,
a forward as `<p><br/></p><div>attribution</div>…`. `textBody` is the
text rendering of that: quoted lines carry `> `, nested quotes `> > `.
Because the result is the sanitiser's own output, sending it back
unchanged in `draft.save` stores it byte for byte and reports nothing
`blocked`. Compose mode removes from the original what it removes from
any draft — remote images (the `block` policy, no override), scripts,
forms, event handlers, `<style>` blocks and `data:` images — and the
result's `blocked` counts the removals the way `draft.save` would, so the
client can say so once. The original's inline pictures
(`cid:` references to its own parts) are **copied into the attachment
store** as inline attachments under fresh `contentId`s and the quote
rewritten to them; they are listed in `draft.attachments`, unbound until
the client sends their ids back in `draft.save` (the orphan sweep of §4.10
applies), and readable through `attachment.get` for display. A reference
to a part that is not a picture (or an SVG) is dropped from a reply and
becomes a regular attachment of a forward. A forward imports every other
part as well, `message/rfc822` as `.eml`. Caps: one part ≤ 16 MiB, 25 MiB
in total, 32 pictures, 100 attachments; a part over a cap, unreadable, or
otherwise not taken is listed in `skipped` with the metadata `message.get`
reports for it, never an error. A `remote` part (§3) is imported only from
the copy `message.download` holds in memory under `neverStoreAttachments`
(§4.3), under the metadata `message.get` reports for it; otherwise it is
listed in `skipped` with `remote: true`, and a client that wants the
original's files calls `message.download` first.

`attribution` is plain text, lines separated by LF (CRLF accepted), at
most `api.MaxDraftAttributionBytes` (2048) and
`api.MaxDraftAttributionLines` (16), no control characters other than
tab; the backend escapes it. Empty means no line above the quote.

Degradation, never a failure: a message whose HTML the sanitiser refuses
(over its caps) or whose raw file is gone is quoted from its stored text
inside the same cite block (`quoted: "text"`); should the sanitiser refuse
even that, the draft is plain text with `> ` lines and no `htmlBody`
(still `"text"`). A body that was never downloaded, and mode `new`, quote
nothing (`"none"`). `sanitizeFailed` is never returned by this call.

#### `draft.open`
Opens a message of the account's Drafts folder as a draft to edit.
Nothing is persisted but the attachments it imports.

- params: `{ "accountId", "messageId" }`
- result: `{ "draft": Draft, "blocked": BlockedContent, "skipped": [Attachment] (opt) }`
- errors: invalidArgument (the message is not in a folder with role
  `drafts`, or its content is not stored: `tooBig`, `failed`),
  accountNotFound, messageNotFound, unavailable (the body is not
  downloaded yet; retry later), storageError

When the message is the copy of a saved draft (its `Message-ID`, or its
server identity, is the one the draft's last upload got), the saved draft
is returned as `draft.list` has it (`id`, `version`; `blocked` empty).
The same holds for an older copy, and for any copy while the draft has
changes the server has not seen yet.

Otherwise — a draft another client wrote, or a newer copy of a saved draft
that another client stored after Malachi's last upload — the draft is built
from the message like a `draft.create` forward, but without a quote around
it: `to`, `cc`, `bcc` and `subject` from its header, its HTML sanitised in
compose mode with its pictures copied into the attachment store under new
`contentId`s and every other part imported as an attachment (the caps and
`skipped` of `draft.create`, including its rule for `remote` parts; the
Drafts folder has such parts only when a message reduced elsewhere was
moved into it), or its text when there is no usable
HTML;
`inReplyTo` names the stored message its `In-Reply-To` identifies, when
there is one (the header itself is kept for sending either way). The draft
is unsaved (`id` empty, `version` 0) and its attachments unbound — except
for the newer copy of a saved draft, which carries that draft's `id` and
`version`, so that its first `draft.save` goes through the version check.
`replaces` is set to `messageId` **only when nothing was lost**: nothing
`skipped`, nothing `blocked`, the message parsed completely and its HTML
kept. Without it, saving the draft leaves the message where it is next to
the new copy.

#### `draft.markdown`

```jsonc
→ { "text": "# Agenda\n- **one**\n- two" }
← { "markdown": true, "html": "<h1>Agenda</h1><ul><li><strong>one</strong></li>…</ul>" }
← { "markdown": false }
```

Text pasted into the compose editor (a message or a comment), rendered as
HTML when it reads as Markdown. The editors call it for a paste of plain
text that looks like Markdown, with no rich HTML on the clipboard, and
insert `html` in place of the text; with `markdown: false`, any error or a
daemon without the method (`methodNotFound`) they paste the text as it is.
Nothing is stored.

- Markdown is CommonMark with GitHub tables, strikethrough and bare-URL
  links; every line break of the text is a line break of the HTML. The
  text reads as Markdown when it has a heading, a list, emphasis, code, a
  quote, a link, an image, a rule, a table or strikethrough; an indented
  block or a bare address alone does not count.
- Raw HTML in the text stays text, an image becomes a link to its
  address, and tables, code and quotes carry inline styles. `html` is the
  sanitiser's output in compose mode, as for `draft.save` (§4.5), and
  `draft.save` sanitises it again with the rest of the editor's HTML.
- Text nested deeper than 32 levels, or whose HTML the sanitiser refuses,
  gives `markdown: false`.
- `text` ≤ 1 MiB (`api.MaxDraftBodyBytes`) and valid UTF-8, otherwise
  `invalidArgument`.

### 4.6 search

#### `search.query`
- params: `{ "accountId" (opt), "folderId" (opt, needs accountId), "query": "…", "page": Page }`
- result: `{ "results": [SearchResult], "page": PageInfo }`
- errors: invalidArgument (empty or blank `query`, longer than
  `api.MaxSearchQueryBytes` = 1024 bytes, more than `api.MaxSearchTerms` =
  32 terms and filters, `folderId` without `accountId`, bad cursor),
  accountNotFound, folderNotFound, storageError

```jsonc
SearchResult { "message": MessageSummary, "snippet": "…posílám přílohy k faktuře…",
               "ranges": [ { "start": 13, "end": 22 } ], "score": 0 }
```

Searches the local store: only messages within the `offlineDays` window
exist there (§4.8), and a message's text becomes searchable once its body
has been downloaded (before that its headers are; a `tooBig` message only
ever by its headers). Rows stored before the index existed are indexed in
the background after the upgrade.

Scope: `folderId` searches that folder; `accountId` alone every folder of
the account except the Trash and Junk roles; neither, every **enabled**
account the same way (a paused account is searched only when named). An
`in:` filter names the folders itself and reaches Trash and Junk too. The
views of a `jira` account (§4.2) hold copies of messages of its space
folders and are searched only when the scope names them (`folderId` or
`in:`), so a message is found once. A hidden message (§4.1
`notificationMail`) is never found.

Query syntax (parsed by the backend, compiled to a parameterised FTS5
expression; nothing typed is ever operator syntax):

- free words, all of which must match (AND). Every word matches as a
  **prefix**, and case and diacritics are ignored: `priloh` finds
  "Přílohy". A word the tokenizer splits (`e-mail`, `jan@firma`) matches
  its parts in order. Words without a letter or digit are ignored;
- `"quoted phrase"` (also `„…“` and `“…”`): whole words in that order;
- `from:`, `to:` (To, Cc and Bcc), `subject:` restrict the next word or
  quoted phrase to that part; free words also match the attachment names
  and the body;
- `has:attachment`, `is:unread`, `is:flagged`;
- `before:YYYY-MM-DD` (exclusive), `after:YYYY-MM-DD` (inclusive), days in
  the daemon's local time;
- `in:<folder>`: a role keyword (`inbox`, `sent`, `drafts`, `trash`,
  `junk` or `spam`, `archive`, `outbox`) or a folder's path or name,
  case-insensitive; several `in:` mean any of them.

Anything else (an unknown prefix, `has:x`, a date that does not exist, a
prefix without a value) is searched as plain words; a query of nothing
but ignored words returns no results.

Results are ordered by `date`, then `id`, newest first; the cursor
belongs to `search.query` and does not encode the query, so a client
restarts from the first page when the query or scope changes.
`page.total` is the exact number of matches up to `api.MaxSearchTotal`
(1000), and -1 beyond it. A message stored in several folders (Gmail
labels) is one result per copy. `score` is reserved and always 0.

`snippet` is plain text, never HTML: up to 200 characters of the body
around the first match of the free words and phrases, whitespace
collapsed, control and invisible formatting characters (bidi overrides,
zero-width spaces) removed, `…` where it was cut; `ranges` are the byte
ranges of the matched words in it, sorted, not overlapping, on UTF-8
boundaries. When the body has no such match (the query matched the
subject or the people only) `snippet` is the message's summary snippet
and `ranges` is absent. `message` carries `outbox` as in `message.list`.

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
flags re-read; on a `graph` account it additionally discards the folders'
delta cursors and re-enumerates the retention window; on a `jira` account
it enumerates the issues of the account's window again instead of asking
for the ones updated since the last pass. It does not discard local data
(only a server-side UIDVALIDITY change does). Queued local operations are
pushed first.

A `graph` account keeps its delta cursors across daemon restarts: a restart
resumes every folder where the last pass left off instead of re-enumerating
it. A folder no pass has enumerated for a week is read from scratch anyway,
which repairs drift the delta stream cannot report.

### 4.8 config

Daemon-owned preferences: options that affect mail handling and therefore
belong to the backend, not to the UI's own settings store. Precedence of
values: set through `config.set` (persisted in the store), else
`config.toml` (`[sync] interval_seconds`), else the built-in default.

```jsonc
Preferences {
  "syncIntervalSeconds": 300,   // 0 = manual sync only; otherwise >= 60
  "remoteContent": "block" | "knownSenders" | "allow",
  "offlineDays": 30,            // 0 = keep everything; otherwise 1..3650
  "compressStore": true,        // (opt in config.set) store raw messages zstd-compressed
  "attachmentOfflineDays": 30,  // (opt in config.set) 0 = all; 1..3650 days; -1 = small ones only
  "neverStoreAttachments": false // (opt in config.set) store no attachment, no picture of 100 KiB+
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

`compressStore` stores each raw message as one zstd frame instead of the
bytes as received (the file name, not its content, tells which). Changing
it converts the stored mail in the background, in both directions
(`system.storage.conversion`); reading a message stays byte-exact.

`attachmentOfflineDays` decides which attachments are stored locally.
Always stored: the text and HTML bodies, every part the HTML shows through
`cid:`, and every part smaller than `api.LargeAttachmentMinBytes`
(100 KiB). The larger ones are stored for messages received in the last N
days (1..3650), for all messages (0) or for none (-1); the others stay on
the mail server, marked `remote` (§3), and `message.download` fetches them
on request. It matters only where it is stricter than `offlineDays`. The
Drafts and Outbox folders, signed and encrypted messages and messages
without a copy on the server are always stored whole, and so is a message
whose reduced copy the daemon cannot verify to show the same. Tightening it
takes effect in the background; loosening it applies to mail downloaded
from then on, and older attachments are fetched when the user opens them.

`neverStoreAttachments` stores no attachment at all, whatever its size or
the message's age; it overrides `attachmentOfflineDays`. The text and HTML
bodies and the pictures the HTML shows through `cid:` that are smaller than
`api.LargeAttachmentMinBytes` (100 KiB) are still stored; larger pictures
stay on the server and the message shows them once the user downloads them
(`message.body` `remotePictures`). Also stored whole are the messages
`attachmentOfflineDays` never reduces (Drafts, Outbox,
signed or encrypted, no copy on the server, a MIME structure the daemon
could not parse to the end, a reduced copy the daemon cannot verify).
Switching it on removes the stored attachments in the background: also
those downloaded on request before, the small ones of the messages
`attachmentOfflineDays` reduced, and those of a message a sync or a
download was storing as it was switched on; a background pass every day
removes those of messages that came to be stored whole since (a message
moved out of Drafts, say). While it is on, `message.download`
keeps the attachments it fetches in the daemon's memory only (§4.3) and
nothing is kept once the daemon quits. Switching it off applies
to mail downloaded from then on, as loosening `attachmentOfflineDays`
does. It has no default from the environment.

These three were added after the others, so they follow different rules:
in `config.set` an **absent** one is left unchanged (an older client drops
fields it does not know), while `config.get` and the result of
`config.set` always carry all of them. Precedence: `config.set`, else the
environment of the process that starts the daemon
(`MALACHI_DEFAULT_COMPRESS_STORE` = a boolean as Go's `strconv.ParseBool`
reads it: `1`, `t`, `T`, `true`, `True`, `TRUE`, `0`, `f`, `F`, `false`,
`False` or `FALSE`; `MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS` = a valid
value; an empty variable counts as unset; the macOS app sets `1` and `30`,
other clients nothing), else `false` and `0`. A default
from the environment is stored as the preference the first time the daemon
applies it, so a daemon started without the variables later does not
change it back; an invalid value is logged and ignored.

#### `config.get`
- params: `{}`
- result: `{ "preferences": Preferences }`

#### `config.set`
- params: `{ "preferences": Preferences }` (the whole set; read-modify-write;
  an absent `compressStore`, `attachmentOfflineDays` or
  `neverStoreAttachments` is left unchanged)
- result: `{ "preferences": Preferences }` (the effective values, every
  field set)
- errors: invalidArgument (interval below 60 and not 0, unknown policy,
  `offlineDays` or `attachmentOfflineDays` out of range), storageError

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

#### `attachment.get`
- params: `{ "accountId", "attachmentId" }`
- result: `{ "attachmentId", "filename", "contentType", "size", "data": base64 }`
- errors: invalidArgument, attachmentNotFound (unknown id, or another
  account's), attachmentTooBig (over 16 MiB, `data` = `{ "limit", "size" }`),
  storageError

Reads an attachment of the store back: what a compose editor shows for a
`cid:` reference it did not mint itself — the pictures `draft.create`
copied out of a quoted original. The row is looked up before the file, so
an id that is not the account's never reaches the file system. The
payload cap is the one of `message.part`; the UI never needs more, as
only pictures are shown this way.

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

### 4.12 issue

The issues of an issue-tracker account (`kind: jira`, §4.1), named by any
message of the issue: the message's thread is the issue. Both methods need
the account's `transition` capability (§4.1); on any other account they
answer `invalidArgument`. They go to the site at once (the daemon's own
client of the account, so the same token and route as the sync), unlike
the local-first message operations.

```jsonc
IssueTransition { "id": "31", "name": "Start Progress", "to": "In Progress",
                  "toCategory": "todo" | "inProgress" | "done" (opt),
                  "needsInput": true (opt) }
```

`name` and `to` are untrusted display text from the site: the
transition's name as the site's own status menu shows it, and the name of
the status it leads to. `needsInput` says the transition opens a screen on
the site or has fields that must be filled in (Jira's `hasScreen`, or a
field with `required`): the daemon cannot perform it, and a client lists
it disabled with a hint that it needs fields on the site.

#### `issue.transitions`
- params: `{ "accountId", "messageId" }`
- result: `{ "issue": IssueInfo, "transitions": [IssueTransition] }` —
  `issue` as the daemon last synchronised it (§3); `transitions` never
  null, in the site's order, at most `api.MaxIssueTransitions` (100),
  those the site marks unavailable to the user left out
- errors: invalidArgument (an account without the capability, a message
  of no issue, an empty id), accountNotFound, messageNotFound (the
  message), messageGone (the site no longer shows the issue, or hides it
  from the user), authFailed (the site refused the token), serverError
  (the site's answer, its message in `error.message`), serverTimeout,
  networkError, tlsError, keyringError, storageError

#### `issue.transition`
- params: `{ "accountId", "messageId", "transitionId" }` — the id of a
  transition from `issue.transitions` without `needsInput`
- result: `{ "issue": IssueInfo }` — the issue after the daemon refreshed
  it from the site, so its `status` is the new one and the event row of
  the change (§3, `issue.item: "event"`, the user's own: read, never
  announced) is stored; when the refresh did not finish within 30 s, the
  issue as last synchronised (the transition was performed all the same
  and the next pass brings the change)
- errors: those of `issue.transitions`, and invalidArgument for a
  transition the issue does not offer or one with `needsInput` (checked
  against the site's current list before anything is changed); a
  transition the site refuses after all (a 400, such as a validator of
  the workflow) is serverError with the site's cleaned message

The daemon lists the transitions again before performing one, so a
transition that stopped being offered since the client listed them is
refused rather than sent. Clients allow 20 s for `issue.transitions` and
45 s for `issue.transition`.

### 4.13 board

The board sorts the user's conversations and issues by what is owed. A
**case** is one thread of an account (§4.4): a mail conversation, or an
issue of a `jira` account. Every case is in one of four **states**:

| State | Meaning |
|---|---|
| `hot` | needs the user now |
| `you` | waits for the user's answer |
| `them` | the user waits for someone else |
| `info` | nothing to do; for reading |

Three parties can set a state, and the first one present wins:

1. the **user** (`userState`, `board.setState`);
2. an **assistant** (`annotation.state`), only while the board's
   `assistant` preference is on and the annotation is not `stale`;
3. the daemon's **rules** (`ruleState`, always present, with the code of
   the rule in `ruleReason`).

The rules read only headers, structure, folder roles, flags, the bulk
classification (§3 `bulk`) and the fields of an issue, never the words
of a message (one exception: a question mark in the user's own text,
`them.asked`). Which members count: not hidden, not in a folder of role
`trash`, `junk` or `drafts`, not bulk mail, not waiting for the bulk
classification (it counts once classified) and no `jira` event. A member is the user's own
(**mine**) only when it is in a folder of role `sent` or `outbox`, never
because its `From` names the user. Copies of one message (one
`Message-ID`) count once: when a copy is mine the message is the user's;
otherwise the copy the daemon stored first stands for it, and a later
copy (another client's move, or a forged duplicate) changes none of what
the rules read (sender, recipients, `Importance`, arrival, bulk class).

A **note to self** is a message of the user's (mine) whose recipients,
`To`, `Cc` and `Bcc` together, are at least one and every one an address
of one of the user's accounts (each account's own address and the senders
of its sent folder, compared without case and surrounding space); a
mixture of the user's and anybody else's addresses is no note, and
neither is a message without recipients. A note still counts (it is in
`messageCount`, its flag counts for `hot.flagged`, an answer to it is
`you.repliedToYou`), but the rules pass over it: the state is decided by
the other members as if it were not there, and the case's `date`,
`subject`, `snippet` and `latestMessageId` are those of the newest member
that is not a note, so the windows count from it. A reply the user sent
only to another of their own addresses therefore leaves the
correspondent's mail before it on the board.

A message of the user's **shaped like a forward** (below, "Own text and
forwards") is passed over the same way: among the user's newest messages
after the newest inbound member that counts (at most 10, the ones whose
text the daemon reads), a forward counts but decides nothing, so
forwarding a conversation to someone else neither ends its case nor
makes a new one, and it answers none of the user's commitments. The
newest member that counts and is neither a note nor such a forward is
the **deciding member**: the state is decided by it, and `date`,
`subject`, `snippet` and `latestMessageId` are its. A thread of nothing
but notes and forwards is no case.

A thread is a case when its deciding member is inbound, or when the
user flagged a member that counts (`hot.flagged`, whoever wrote the
deciding member). When the deciding member is mine, it is otherwise a
case only when the rules say `them` (`them.replied`, `them.asked`); a
reply of the user's to someone who did not write in the thread, or one
that asks nothing, is no case. While an inbound member of the thread waits for
the bulk classification (new mail, or all mail after the classification
rules changed) the daemon decides nothing and the thread keeps the case
it had. A user state, a remind, a future deadline, an open commitment or
a linked suggested reply (`draft`, while the draft exists) keeps a case
the rules would drop (`ruleReason: "kept"`): neither unstarring the
case nor answering it from another client leaves a suggested reply
without its case. Cases are
computed in the background as messages are stored, moved, flagged,
deleted or merged.

**Known senders.** The user is asked to act only on mail from people
they have written to: a sender is **known** when its `From`, or an
address of its `Reply-To`, is in `To` or `Cc` of a message in a folder of
role `sent` or `outbox` of any of the user's enabled mail accounts (or of
a message of the user's in the same thread). A message from an unknown
sender with the user in its `To` is a **new contact**
(`you.newContact`: it waits for the user, but is told apart from mail of
people the user writes to); from an unknown sender without the user in
`To` (only in `Cc`, or not addressed) it is `info.unknownSender`. An
unknown sender's `Importance` never makes a case `hot`; an answer to one
of the user's messages (`you.repliedToYou`) and the user's own flag
(`hot.flagged`) count whoever sent it. Every header involved can be forged; the rule sorts
mail, it does not authenticate it.

**Merged threads.** The case id stays when threads merge, `threadId`
may change. When both threads had a case, the case of the thread that
absorbs the other survives with its id (the other id then answers
caseNotFound) and the user's decisions carry over: the user state set
later, done only when both were done (at the later time), else the
earlier remind of either, every commitment, the surviving case's
annotation (the other's when it had none), and the draft link (the
other's when it had none, or when its own draft no longer exists; when
both link a draft that exists, the surviving case keeps its own and the
other draft loses its case: when the user edited it, it becomes an
ordinary draft of the account, uploaded to its Drafts folder, so nothing
typed is lost; an untouched suggestion is deleted, §4.5). The annotation
is stale after a merge (its members changed).

**Moved threads.** A thread whose messages another client moves keeps
its case and the user's decisions: while its members are gone for a
moment (one folder synced before the other) the case stays off the board,
and the copies that come back, also as a thread of their own, take it
over with its id. A case whose thread has had no member for a day is
deleted; its suggested reply loses its case (below). A copy of a message the case already had when it was marked
done does not reopen it.

`ruleReason` codes (an open set: a client shows a generic text for a code
it does not know; a code is never reused for another meaning):

| Code | State | Rule |
|---|---|---|
| `hot.important` | hot | deciding member inbound, the user in its `To`, a known sender, and its own header says `Importance: high` or `X-Priority` 1 or 2 |
| `hot.flagged` | hot | the user flagged a member that counts (any copy the rules read), whoever wrote the deciding member |
| `you.addressed` | you | deciding member inbound, the user in its `To` and a known sender |
| `you.newContact` | you | deciding member inbound, the user in its `To`, from a sender the user has never written to (not known); its `Importance` does not count |
| `you.repliedToYou` | you | deciding member inbound and it answers (`In-Reply-To`) a message of the user's, whoever sent it |
| `them.replied` | them | deciding member the user's reply to an earlier inbound member, with one of its senders (`From` or `Reply-To`) in `To` |
| `them.asked` | them | no inbound member counts, and one of the user's newest 10 messages that is not a forward, to someone else, asks a question (a question mark in its own text) |
| `info.ccOnly` | info | inbound; the user only in `Cc` |
| `info.notAddressed` | info | inbound; the user in neither `To` nor `Cc` (a list, a `Bcc`) |
| `info.unknownSender` | info | inbound from a sender the user has never written to (not known), the user not in its `To` (only in `Cc`, or not addressed) |
| `info.yourNote` | info | inbound from one of the user's addresses (of any of their accounts), every recipient one of them |
| `jira.yourComment` | them | the issue's last item that is not an event is the user's |
| `jira.assigned` / `jira.reporter` / `jira.commented` | you | someone else's item on an issue assigned to, reported by, or commented on before by the user |
| `jira.watching` | info | an issue the user only watches |
| `kept` | (last) | the rules no longer make it a case; something above keeps it |

For an inbound deciding member the rules are tried in the order
`hot.flagged`, `info.yourNote`, `hot.important` (known sender only),
`you.repliedToYou`, `you.addressed` (known sender), `you.newContact`
(unknown sender, the user in `To`), `info.unknownSender` (unknown
sender, the user not in `To`), `info.ccOnly`, `info.notAddressed`
(`ccOnly` and `notAddressed` are thus left to known senders); for a
deciding member of the user's, `hot.flagged`, `them.replied`,
`them.asked`. A message whose `From` names the user, in the user's
inbox, is inbound like any other.

**Own text and forwards.** `them.asked` and a commitment's quote read
the user's **own text** of a message: for a message with HTML, the text
of its HTML part with the quoted history cut off as `message.body`
`trimQuoted` does; otherwise its plain text with the quoted history cut
off (as `trimQuoted` does for text). On top of that every `>`-quoted line
is dropped, the text is cut at an attribution line (`On … wrote:` and its
translations) whose quote below is not `>`-quoted (always, for text taken
from HTML), at the signature separator (`"-- "`), and a text of more
than 100,000 lines, or one that starts with a quoted history, has none.
In doubt there is less own text, never more. A question mark counts
outside URLs and addresses, and after one as its sentence punctuation
(`… to anna@example.cz?`). A message of the user's is shaped like a
forward when its subject carries a forward marker (`Fwd:`, `FW:`, `WG:`,
`TR:`, `ENC:`, `RV:`, `PD:`), it has an attached message
(`message/rfc822`), its text starts with a quoted history, or it answers
nothing (no `In-Reply-To` or `References`) and has less than 300 bytes of
its own above a quoted history. Such a message is passed over (above); a
forward older than the user's 10 newest messages after the newest
inbound member is judged without its text (only its subject and an
attached message tell it).

The rules have a version (`RulesVersion` in the daemon, now 6); a new
version makes the daemon judge every thread of the longest window again
in the background (`ready` false meanwhile), keeping the user's states,
done, reminds, annotations and commitments.

**Jira.** An issue in the `done` status category, or in one of the
account's `closedStatuses`, is no case.

**Windows.** A case stays on the board for a number of days from its
`date` that depends on the state in effect (`windows`, below: 90 for `hot`, 30 for
`you` and `them`, 14 for `info` by default); a user state, a remind (also
one that came due, while its `remindedAt` lasts: until the user acts on
the case or an inbound member that counts arrives), a
future deadline or an open commitment keeps it regardless. An
annotation's deadline keeps or lists a case only while the `assistant`
preference is on. A done case is pruned once its 30 days have passed,
whatever its user state.

```jsonc
BoardCase {
  "id": "c_<32 hex>",                 // stable; per account
  "accountId": "acc_1", "threadId": "t_9",
  "ruleState": "hot" | "you" | "them" | "info",
  "ruleReason": "you.addressed",      // see the table
  "userState": "them" (opt),          // the user's choice; absent = automatic
  "annotation": BoardAnnotation (opt),
  "visibility": "live" | "done" | "snoozed",
  "doneAt": Time (opt),               // with visibility "done"
  "remindAt": Time (opt),             // with visibility "snoozed", in the future
  "remindedAt": Time (opt),           // with visibility "live": a remind came due and the user has not acted since
  "subject": "Lunch",                 // the newest member's, Re:/Fwd: stripped; an issue's "KEY: Summary"
  "person": Address,                  // the other party
  "date": Time,                       // arrival of the newest member that counts
  "snippet": "…", "unread": true, "hasAttachments": false,
  "messageCount": 3,                  // members that count
  "replyMessageId": "m_5", "replyFolderId": "f_inbox",
  "latestMessageId": "m_7",
  "issue": { "key": "ITSD-42", "status": "In Progress",
             "statusCategory": "inProgress" (opt) } (opt),   // a jira account's case
  "canArchive": true,
  "draft": { "draftId": "d_1", "text": "…", "updated": Time } (opt),
  "version": 12
}
BoardAnnotation {
  "state": "them" (opt),              // absent: the assistant left the state to the rules
  "title": "…", "summary": "…", "why": "…", "tasks": ["…"],
  "due": BoardDue (opt),
  "source": "…",                      // names the assistant
  "at": Time,
  "stale": true (opt)
}
BoardDue { "at": Time, "quote": "…", "messageId": "m_3" }
BoardCommitment { "id": "k_1", "caseId": "c_…", "accountId": "acc_1",
                  "messageId": "m_4",             // the user's own message
                  "text": "…", "quote": "…", "due": Time (opt),
                  "state": "open" | "done" | "closed",
                  "closedReason": "replied" | "done" (opt),   // with "closed"
                  "at": Time }
BoardMessage { "id": "m_3", "folderId": "f_inbox", "from": Address, "date": Time,
               "mine": false, "text": "…", "trimmed": true (opt) }
```

- `person`: the sender of the newest inbound member that counts, else the
  first `To` recipient of the user's newest member that is not one of the
  user's addresses. `subject` and `person` are cleaned (control, bidi and
  other invisible characters removed, one line) but keep their URLs:
  they are what the sender wrote, never a link.
- `date`: when the newest member that counts arrived (its internal date,
  else its `Date` header, else when the daemon stored it), never later
  than when the daemon stored it nor than now.
- `replyMessageId` is the member a reply answers (`draft.create` with
  `mode: "reply"`): the newest inbound member that counts, else the newest
  member that counts; on a `jira` account that reply is a comment.
  `latestMessageId` is the newest member that counts.
- `visibility`: `done` after `board.setDone` until an inbound member that
  counts arrives later (by the time the daemon stored it, not by its
  `Date` header, so neither a forged date nor a backfill of old mail
  reopens a case); `snoozed` until `remindAt`, then `live` again with
  `remindAt` gone and `remindedAt` set; a remind that came due keeps the
  case listed (also past its window) as long as `remindedAt` lasts: until
  the user acts on the case or an inbound member that counts arrives
  (below). Marking a case done clears its remind; setting a remind
  clears done. **New mail ends a remind early**: an inbound member that
  counts, which the daemon stored after the remind was set and which was
  not a member then (a copy of a message the case already had, moved by
  another client, does not count, as for done), makes a snoozed case
  `live` again at once, with `remindAt` gone and `remindedAt` not set
  (the remind did not come due). Only a remind set by a daemon with this
  rule ends early: an older remind, or one a merge carried over from a
  case without the marker, waits for its time.
- `remindedAt`: when a remind came due (the `remindAt` it had); present
  while the case is `live` after it, until the user acts on the case
  (`board.setState`, `board.setDone`, `board.remind` — also with null —,
  `board.archive`, `board.unflag`) or an inbound member that counts
  arrives. A client lists such a case first in its state, marked as
  reminded; it is no system notification.
- `canArchive`: `board.archive` would move messages — the account has the
  `move` capability (§4.1) and a folder of role `archive`, and a member is
  in the folder of role `inbox`.
- `draft`: the suggested reply linked to the case, while that draft
  exists: linked by the user (`board.setDraft`) or by an annotation
  (`board.annotate` `draftId`); `text` is its plain text, at most
  `api.MaxBoardDraftTextBytes` (4000). The link is the case's, not the
  annotation's: it needs no annotation, survives a stale annotation and a
  later one without a draft, and is listed whatever the `assistant`
  preference. A case links at most one draft; the link ends with
  `board.discardDraft`, and shows no draft once the draft is sent or
  deleted or its account is removed. Drafts are never sent by the board;
  the user edits and sends them.

  A linked draft is **local** (§4.5): linking makes it so, deletes a copy
  it already had in the Drafts folder (as `draft.delete` does; a
  Microsoft 365 copy changed in Outlook since the upload stays, §4.5), and
  from then on no syncer uploads it. It reaches the mail server only when
  the user sends it (`message.send`, with the usual Sent copy), or — if
  the user edited it — when its case goes without Send or Discard, as one
  of the user's ordinary drafts; an untouched suggestion never does (§4.5).
  Its lifecycle on the board: saving it bumps the case's `version` and
  marks it edited; it keeps a live case on the board (above) and from the
  prune; a done case with a suggested reply stays listed among the done
  for the done case's 30 days, and then loses its reply; sending,
  deleting or discarding it lets the rules judge the case again. When its
  case goes — deleted because its conversation was gone for a day, the
  surviving case of a thread merge keeping its own reply (above), the
  account removed with its local data kept — or a done case's 30 days
  end, an edited reply becomes an ordinary draft and an untouched one is
  deleted, at once. A local draft that was never linked (an assistant's
  draft whose link was refused, or never asked for) is deleted once it
  has not been saved for 6 hours. Drafts linked before migration 0018
  were ordinary drafts: 0018 made them local, 0019 counts every local
  draft as edited, and a copy one of
  them had in the Drafts folder is deleted by the daemon (at the next sync
  pass of the account and in its hourly upkeep; on Microsoft 365 unless it
  was changed in Outlook since the upload).
- `version` changes whenever anything of the case changes, the members
  `board.get` returns included; a client caches `board.get` by `(id,
  version)`.
- `BoardMessage.text`: the message's own text — its quoted history cut
  off as `message.body` with `trimQuoted` cuts it: the stored plain text
  cut by the plain-text rules when they find the quote in it, else, when
  the sanitiser cut a quoted history off the HTML part, the text of the
  trimmed HTML, else the stored text whole (nothing is cut when the quote
  does not run to the end, nothing of the message's own would be left
  above it, or the trimming gives up; a pure forward is shown whole) —
  with the signature (from the RFC 3676 separator `"\n-- \n"`) cut off,
  cleaned (no control, bidi or other invisible characters), at most
  `api.MaxBoardMessageTextBytes` (8000: an ordinary mail whole, so the
  detail shows it in full); `trimmed` says something was cut off. Never
  HTML. `mine` as above. One `board.get` thus carries at most 50 × 8000
  bytes of message text (400 kB; under 2.4 MB of JSON even if every
  character is escaped). `board.queue` keeps its own, smaller caps and
  reads the stored plain text only (the plain-text rules, never the
  HTML). The daemon derives the own text of at most 8 HTML members while
  `board.get` waits and the rest in the background, so the text of an
  older HTML member may stay untrimmed until the case next changes.

**Annotations are text an assistant wrote.** `annotation` (`title`,
`summary`, `why`, `tasks`, `due.quote`, `source`) and a commitment's
`text` were written by an AI assistant that read the user's mail, which
may have tried to steer it. The daemon cleans them (control, bidi and
other invisible — default-ignorable — characters removed, except ZWJ
and ZWNJ between two other kept characters that are not whitespace, and
an emoji's presentation selector; URLs — `scheme://…` and
`www.…` — removed; whitespace collapsed in one-line fields) and enforces the
limits below, but cannot make them true. A client shows them **only as
plain text**, never as markup or links, always marked as the assistant's
(never as the daemon's or the user's words), shows `due.quote` and a
commitment's `quote` next to the date they support, and never acts on
them by itself. No annotation sends, moves or deletes anything.

`stale`: a member was added, removed or got its body since the
annotation was made — the case's input key changed (`board.queue`
`inputKey`: a key over the ids and body states of the members that
count; a new version of the rules alone does not change it). A client then uses none of it (no state, title,
summary, why, tasks or deadline) and may say that the notes are
outdated; `draft` stays. `board.queue` offers the case again.

| Field | Limit (UTF-8 bytes, after cleaning) |
|---|---|
| `title` | one line, ≤ 300 (`api.MaxBoardTitleBytes`); `""` = show the subject |
| `why` | one line, ≤ 400 |
| `summary` | a block (line breaks kept), ≤ 2000 |
| `tasks` | ≤ 10 lines of ≤ 300 each |
| commitment `text` | one line, ≤ 300 |
| `source` | one line, ≤ 64 |
| `quote` | 10–300, at least 10 characters that are not spaces |

**Quotes.** A deadline (`annotation.due`) and a commitment carry a quote
the daemon checks before it stores anything: the quote and the message's
text are normalised alike (valid UTF-8, the cleaning above, each URL
replaced by one placeholder so that removing it never joins the words
around it, Unicode NFC, typographic quotes as ASCII, every run of
whitespace one space) and the quote must be an exact substring. A
deadline's quote must be in the stored plain text of `due.messageId`
(all of it, a quoted history included), a member of the case that
counts; a commitment's quote must be in the user's **own** text
of `messageId` (a member that is mine; own text as above), so the other
party's words never count as the user's promise. A deadline or a commitment's `due` must lie between one day
before and 400 days after that message arrived (as `date` above: its
internal date, else its `Date` header, never later than when the daemon stored
it, so a forged `Date` cannot move the range). Otherwise the call fails
with quoteNotFound (`data.field`: `due` or `commitment`) or
invalidArgument (the date, the limits).

**Commitments** are what the user promised in their own messages. One
closes by itself (`closed`) when the user writes a message in the thread
newer than any they had written when it was recorded (`closedReason:
"replied"`; a commitment recorded on an older message stays open until
the user writes again) or the case is marked done (`"done"`); the user
ticks it off with `board.setCommitment`.

**Triage and runs.** An assistant annotates through the MCP bridge
(`docs/mcp.md`, `--allow-triage`): `board.queue` hands it cases with
their text, `board.annotate` and `board.commit` store what it found. The
daemon itself never talks to an assistant. A client that starts a run
(the user's Triage button, or its own automatic schedule under the
`autoTriage*` preferences) records it with `board.runStart` and
`board.runEnd` and passes the `runId` to the bridge, which passes it on;
the daemon counts each annotate and commit call in that run (accepted:
`annotated` / commitments; refused with quoteNotFound, conflict or
invalidArgument: rejected). A call without a `runId`, or with one the
daemon does not know, counts in an implicit run with trigger `external`
per `source` and day. Runs are kept for 90 days.

**Preferences** (`board.preferences`, `board.setPreferences`; not part of
`Preferences`, §4.8):

```jsonc
BoardPreferences {
  "enabled": true,                     // the daemon computes the board
  "assistant": false,                  // annotations count, board.queue hands out text
  "windows": { "hot": 90, "you": 30, "them": 30, "info": 14 },   // days, 1..365
  "triageAccounts": [],                // [] = every enabled mail account (imap, graph)
  "autoTriage": false,                 // the client runs triage on its own schedule
  "autoTriageMinutes": 30,             // least time between automatic runs, 5..1440
  "autoTriageDailyCases": 60           // cases automatic runs annotate per local day, 0..1000
}
```

The values above are the defaults. `assistant` is turned on by a client
only after the user agreed to have mail read by the assistant. A `jira`
account is triaged only when `triageAccounts` names it. The daemon only
stores `autoTriage`, `autoTriageMinutes` and `autoTriageDailyCases`; the
client's schedule reads them. While `enabled` is false the daemon
computes nothing, `board.list` returns no cases, and every other board
method but the preferences answers invalidArgument; the user's decisions
(states, done, reminds, annotations) are kept for when it is turned on
again.

#### `board.list`
- params: `{ "accountIds": ["acc_1"] (opt) }` — absent or empty = every
  enabled account
- result: `{ "cases": [BoardCase], "commitments": [BoardCommitment],
  "enabled": bool, "assistant": bool, "triage": BoardTriage,
  "ready": bool, "truncated": true (opt) }`
- errors: accountNotFound, storageError

```jsonc
BoardTriage { "lastRun": { "at": Time,                 // when it started
                           "endedAt": Time (opt),      // absent while it runs
                           "trigger": "manual" | "auto" | "external",
                           "source": "…", "annotated": 4,
                           "error": "cancelled" | "timeout" | "signedOut" | "failed" (opt) } (opt),
              "annotatedTodayAuto": 12,   // by automatic runs started today (local day)
              "queue": 3,                 // live cases board.queue would offer; 0 with the assistant off
              "usage24h": { "inputTokens": 1200, "outputTokens": 340,
                            "cacheCreationInputTokens": 0, "cacheReadInputTokens": 9000,
                            "lowerBound": true (opt),   // a run summed reported only a lower bound
                            "runs": 2 } (opt) }  // runs ended in the last 24 h that reported usage
```

`cases`: live, done and snoozed cases of enabled accounts (a paused
account's are left out; a done case for 30 days after it was marked
done), newest `date` first, at most
`api.MaxBoardCases` (1000, the newest; `truncated` then). Never null.
`commitments`: the open commitments of the live cases listed, never
null. `ready` is false until the daemon has evaluated every thread once
since the board was enabled or its rules changed (after the upgrade that
brings the board this takes a minute or two in the background); until
then `cases` may be partial and a client says so. `lastRun` is a run
still open if there is one (the newest by start), else the run with the
latest activity (its end; an external run's latest call), of any
trigger; its `error` is a class, never free text. `usage24h` sums the
token usage (`board.runEnd`) of the runs that ended within the 24 hours
before `board.list` answered (the daemon's clock, by each run's end) and
carry usage; `runs` counts them (≥ 1), and `lowerBound` is true when
any of them reported only a lower bound (a client then says "at
least"). Absent when no run in that window
has usage: runs of clients that did not report it, external runs and
runs the daemon ended itself have none. The value is computed when
`board.list` answers; it shrinks as runs age out of the window without a
`notify.boardChanged`.

#### `board.get`
- params: `{ "caseId" }`
- result: `{ "case": BoardCase, "messages": [BoardMessage] }` — the
  members that count, the newest `api.MaxBoardMessages` (50), oldest
  first; never null
- errors: invalidArgument (an empty id), caseNotFound, storageError

#### `board.setState`
- params: `{ "caseId", "state": "hot" | "you" | "them" | "info" | null }`
  — null (or absent) returns the case to automatic
- result: `{ "case": BoardCase }` — clears `remindedAt`
- errors: invalidArgument (an unknown state), caseNotFound, storageError

#### `board.setDone`
- params: `{ "caseId", "done": bool }`
- result: `{ "case": BoardCase }` — done also clears a remind and closes
  the case's open commitments (`closedReason: "done"`); `false` makes it
  live again; either clears `remindedAt`
- errors: caseNotFound, storageError

#### `board.remind`
- params: `{ "caseId", "until": Time | null }` — in the future and at most
  a year ahead (`api.MaxBoardRemind`); null (or absent) ends the remind
- result: `{ "case": BoardCase }` — `snoozed` until `until`, done and
  `remindedAt` cleared; when the time comes the case is live again with
  `remindedAt` and `notify.boardChanged` says so; an inbound member that
  counts, stored after the call, ends the remind early (`visibility`
  above)
- errors: invalidArgument (the past, more than a year), caseNotFound,
  storageError

#### `board.archive`
- params: `{ "caseId" }`
- result: `{ "archived": 2, "noArchive": true (opt), "case": BoardCase,
  "moved": [ { "messageId": "m_3", "fromFolderId": "f_inbox" } ] (opt) }`
- errors: invalidArgument (the board disabled, no `caseId`), caseNotFound
  (also when the case's account is gone), messageNotFound (a member left
  or was deleted while the move was prepared; nothing moved, the case is
  not marked done), storageError

Moves the members in the folder of role `inbox` to the folder of role
`archive` the way `message.move` does (local first, through the
operation log), then marks the case done. An account without the `move`
capability or without an archive folder only marks it done and answers
`noArchive: true` with `archived: 0`. It clears `remindedAt`. `moved`
names every message moved and the folder it was moved from, so a client
can undo: `message.move` of those messages back to their folders, then
`board.setDone` with `done: false` (with `noArchive`, only the latter).
`moved` is absent when nothing moved, and also when the move cannot be
undone locally: an archive folder the daemon does not synchronise
(`Folder.synced` false, Gmail's All Mail) takes the messages off the
local store (`message.move`), so there is nothing to move back, and a
client offers no Undo (with `noArchive` it may offer one that only
clears done).

#### `board.unflag`
- params: `{ "caseId" }`
- result: `{ "case": BoardCase, "unflagged": 2 }`
- errors: invalidArgument (the board disabled, no `caseId`), caseNotFound
  (also when the case's account is gone), messageNotFound (a member was
  deleted while the change was prepared; nothing changed), storageError

Takes the star away from a case that is `hot.flagged` (whoever wrote
last; a client offers it for such a case that is not done): clears the
`flagged` flag of every copy whose flag the rules read — every flagged
copy of a member that counts, or that waits for its bulk
classification, in whatever folder, as the rules merge copies by
`Message-ID` — the way `message.flag` does (local first, through the
operation log; on a `jira` account on the item's local copies). A flagged
copy in the trash, junk or Drafts, a hidden one, one in the outbox, or a
copy of a message that does not count (bulk mail) keeps its flag. A
`jira` item's copies in the virtual folders (`assignedToMe`, `watching`,
`open`) are not read by the rules, but they are the same item: their
flag is cleared with it, as `message.flag` does on a `jira` account.
Why a method: `board.get` shows only the newest members, each
`Message-ID` once, so a client cannot know every flagged copy.
`unflagged` counts the flagged copies the rules read that the call
cleared (a `jira` item's virtual copies not counted); 0 (nothing
flagged) is no error. Should the call fail part way (a storage error
after the copies the rules read were cleared, while a `jira` item's
virtual copies were not yet), the error is returned with what was done
kept, as with `message.flag`: calling it again finishes it.
`case` is the case as stored when the flags were cleared; the rules
judge it again right after, and `notify.boardChanged` follows with what
it became (another state, the same state with `ruleReason: "kept"` when a
user state, a suggested reply or the like keeps it, or no case). It
clears `remindedAt`.

```jsonc
// → { "caseId": "c_0f3…" }
// ← { "case": { "id": "c_0f3…", "ruleState": "hot", "ruleReason": "hot.flagged", …, "version": 9 }, "unflagged": 2 }
```

#### `board.discardDraft`
- params: `{ "caseId" }`
- result: `{ "case": BoardCase }` — without `draft`
- errors: caseNotFound, storageError

Deletes the linked draft as `draft.delete` does and removes the link; a
case without a draft is answered as it is.

#### `board.setDraft`
- params: `{ "caseId", "draftId" }`
- result: `{ "case": BoardCase }` — with `draft`
- errors: caseNotFound; invalidArgument (the board disabled, an empty
  `caseId` or `draftId`, a draft that does not exist, is not a draft of
  the case's account or does not reply to a member of the case — the
  checks of `board.annotate`'s `draftId`); conflict (the case already
  links another draft that still exists: discard it first); storageError

Links a draft as the case's suggested reply on the user's request (the
client's Suggest Reply). Needs neither an annotation nor the `assistant`
preference. The draft becomes local (§4.5): a copy it had in the Drafts
folder is deleted on the server (unless Outlook changed it since), and
it is not uploaded again unless it loses its case after the user edited
it. A draft that was an ordinary draft when linked counts as edited
(§4.5): the daemon cannot tell who wrote it. Linking
the draft the case already links answers the case unchanged. A link to a draft that no longer exists is replaced. Followed
by `notify.boardChanged` when the case changed.

```jsonc
// → { "caseId": "c_0f3…", "draftId": "d_42" }
// ← { "case": { "id": "c_0f3…", …, "draft": { "draftId": "d_42", "text": "Monday works.", "updated": Time }, "version": 13 } }
```

#### `board.queue`
- params: `{ "accountIds": [] (opt), "caseIds": [] (opt), "limit": 3 (opt) }`
  — `accountIds` empty = every triage account, others are ignored;
  `caseIds` restricts to those cases (the others are skipped); `limit` 0
  = 3, at most `api.MaxBoardQueueLimit` (5)
- result: `{ "items": [BoardQueueItem], "remaining": 7 }`
- errors: invalidArgument (the assistant preference off, the board
  disabled, `limit` out of range), storageError

```jsonc
BoardQueueItem { "caseId": "c_…", "accountId": "acc_1",
                 "inputKey": "<32 hex>",           // the members that count and their body states; pass back to board.annotate / board.commit
                 "ruleState": "you", "ruleReason": "you.addressed",
                 "userState": "them" (opt),
                 "subject": "…", "replyMessageId": "m_5",
                 "issue": { … } (opt),
                 "own": ["me@example.org"],        // the user's addresses on the account
                 "hasDraft": true (opt),           // the case already links a draft: a draftId passed to board.annotate is not linked
                 "commitments": [BoardCommitment] (opt), // open and done ones already recorded, oldest first, at most 10
                 "messages": [{ "messageId": "m_5", "from": Address,
                                "to": [Address] (opt), "cc": [Address] (opt),
                                "date": Time, "mine": false,
                                "text": "…", "truncated": true (opt) }] }
```

The live cases of triage accounts without an annotation or with a stale
one, newest `date` first; `remaining` counts the others the queue would
offer. Per item the newest `api.MaxBoardQueueMessages` (8) members that
count, oldest first, each `text` the stored plain text with the quoted
history the plain-text rules find and the signature cut off, cleaned (as
`BoardMessage.text`, but never from the HTML part), at most 3000
bytes and 12 KiB for the item together (`truncated` when cut), so one
call carries at most about 60 KiB. This is the only method that hands mail
text to an assistant, and only while `assistant` is on.

#### `board.annotate`
- params: `{ "caseId", "inputKey", "runId" (opt), "state" (opt),
  "title" (opt), "summary" (opt), "why" (opt), "tasks": [] (opt),
  "due": BoardDue (opt), "draftId" (opt), "source" }`
- result: `{ "case": BoardCase, "draftNotLinked": true (opt) }`
- errors: caseNotFound; conflict (`inputKey` is not the case's current
  one: it changed since `board.queue`); quoteNotFound (`data.field:
  "due"`); invalidArgument (the assistant off, the case's account not a
  triage account, an unknown state, a field over its limit, a `due`
  outside its range or `due.messageId` not a member of the case that
  counts, a
  `draftId` that is not a draft of the case's account replying to a
  member of the case, an empty `source`); storageError

Replaces the case's annotation as a whole (fields left out are empty).
The daemon computes the case's key again inside the same transaction, so
an annotation of members that changed in between is refused.

The case's draft link is not part of the annotation: without `draftId`
the link stays as it is, and `draftId` is linked only when the case
links no other draft that still exists (`hasDraft` in `board.queue`). A
link the user made with `board.setDraft`, or an earlier annotation's, is
never replaced by an unattended run: the annotation is stored, the draft
passed stays unlinked, and the result says `draftNotLinked: true` (a
local draft that was never linked is deleted after 6 hours without a
save; an ordinary one stays among the account's drafts). Passing the draft already linked is
no refusal. A draft it links becomes local as with `board.setDraft`.

#### `board.commit`
- params: `{ "caseId", "inputKey", "runId" (opt), "messageId", "text",
  "quote", "due": Time (opt), "source" }` — `messageId` a member of the
  case that is the user's own
- result: `{ "commitment": BoardCommitment, "existing": true (opt) }`
- errors: caseNotFound, conflict, quoteNotFound (`data.field:
  "commitment"`), invalidArgument (the assistant off, the account not a
  triage account, a message that is not the user's or not in the case,
  limits, `due` out of range), storageError

A commitment already recorded for the same case, message and quote (in
any state; quotes compared after the normalisation of the verbatim check,
one containing the other counts as the same) is returned with `existing:
true` instead of a second one: its text and state stay, nothing is
counted in the run, and only an open or done one without a deadline takes
`due`. Duplicates recorded before this rule are merged once by the
daemon's board upkeep (the oldest kept, done when any of them was).
Recording a commitment raises the case's `version`.

#### `board.setCommitment`
- params: `{ "commitmentId", "done": bool }` — `false` reopens a done or
  closed commitment
- result: `{ "commitment": BoardCommitment }` — raises the case's
  `version`
- errors: caseNotFound (no such commitment, or its case is gone),
  storageError

#### `board.preferences`
- params: `{}`
- result: `{ "preferences": BoardPreferences }`
- errors: storageError

#### `board.setPreferences`
- params: `{ "preferences": BoardPreferences }` — every field; a client
  sends back what `board.preferences` gave it with its changes
- result: `{ "preferences": BoardPreferences }` — as stored
- errors: invalidArgument (a window, `autoTriageMinutes` or
  `autoTriageDailyCases` out of range, a malformed account id in
  `triageAccounts` — empty, over 128 bytes, or other than printable ASCII
  without spaces — or more than 1000 of them), storageError

An id in `triageAccounts` of an account that no longer exists is dropped
quietly, on writing and on reading (`board.preferences`, `board.list`),
so a client that sends back what it read is never refused for an
account removed meanwhile. A list whose accounts are all gone stays as
it is: an empty list would mean every enabled mail account, and the
user chose fewer; triage then reads no account until the user names
others or empties the list.

Changing `windows` or `enabled` makes the daemon evaluate the board
again; `notify.boardChanged` follows.

#### `board.runStart`
- params: `{ "trigger": "manual" | "auto", "source" }` — `external` is
  the daemon's own (calls without a run)
- result: `{ "runId" }`
- errors: invalidArgument (another trigger, an empty or over-long
  `source`), storageError

#### `board.runEnd`
- params: `{ "runId", "error": "cancelled" | "timeout" | "signedOut" |
  "failed" (opt), "usage": BoardUsage (opt) }` — `error` absent =
  success; any other value is stored as `failed`, never as text
- result: `{}`
- errors: invalidArgument (an unknown run id, a negative `usage`
  counter), invalidParams (a `usage` counter that is not a JSON integer
  within 64 bits, like any mistyped parameter), storageError

```jsonc
BoardUsage { "inputTokens": 1200, "outputTokens": 340,
             "cacheCreationInputTokens": 0, "cacheReadInputTokens": 9000,
             "lowerBound": true (opt) }   // only a lower bound of the run's usage
```

`usage` is the run's token usage as the client's assistant reported it
(Claude Code's `usage` of the run); absent = unknown, and the run keeps
none (a counter absent from a given `usage` is 0). It is stored with the
run when this call ends it; each counter above `api.MaxBoardUsageTokens`
(10^12) is stored as that. `lowerBound` says the counters are only a
lower bound: the client stopped the run, it timed out, or the client
gave up waiting for the assistant's final report; it is stored with the
usage and carried into `usage24h`. Ending a run that already ended changes
nothing, its `usage` included; so does ending an external run. A run
left open (the client stopped) is ended by the daemon with `failed`, and
without usage, at its next start or after two hours. Every call that
succeeds is followed by `notify.boardChanged`, so clients learn the new
`usage24h`.

## 5. Notifications

| Method | params |
|---|---|
| `notify.newMessage` | `{ "accountId", "folderId", "message": MessageSummary }` |
| `notify.syncState` | `{ "state": SyncState }` |
| `notify.authRequired` | `{ "accountId", "reason": 1200\|1201\|1202, "message": "…", "authUrl": "https://…" (opt) }` |
| `notify.accountsChanged` | `{}` |
| `notify.messagesChanged` | `{ "accountId", "folderIds": ["f_1"] (opt) }` |
| `notify.boardChanged` | `{ "accountIds": ["acc_1"] (opt) }` |

Notifications are sent only to connections that completed the handshake
(§1.4).

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
message is not new mail). The `message` carries `threadId`, so a client
showing conversations (§4.4) merges the arrival into its thread row
instead of listing again.

On a `jira` account `notify.newMessage` is sent for a description or a
comment by someone else that arrives after the account's first
synchronisation, once, from the space folder (not for the copies in the
views, §4.2); never for an event, for the user's own items, or on the
first synchronisation. A mail hidden as a notification of an
issue-tracker site (§4.1 `notificationMail: "hide"`) produces none.

`notify.messagesChanged` is sent when messages of an account changed
without arriving or leaving. Three cases. Messages were hidden or shown
again: notification mails a `jira` account hides once their issue is
stored, and shows again when its `notificationMail`, its senders or its
spaces change, the issue leaves it, or the account is paused or removed;
`accountId` is then the mail account the messages are in, not the `jira`
account. Or messages of a `jira` account changed in place, keeping their
ids: a pass rebuilt stored items with other rendering settings (bot
names, metadata filters, `hideEvents`), a comment was edited or
re-attributed (its `from` and `date` too), an issue was renamed (every
message of its thread retitled); `accountId` is then the `jira` account,
and the pass sends it once, when it ends. Or the background pass over
existing mail classified messages as bulk mail (`MessageSummary.bulk`,
after migration 0016 or a new rule version): sent once per account with
such messages, when the pass ends, without `folderIds`. In every case
`folderIds` names
the folders concerned (absent: any folder of the account); a client
showing one drops what it cached of their messages (bodies, headers,
`message.get` results), lists it again (`message.list` or `thread.list`)
and re-reads the counts (`folder.list`), which the daemon has recounted
by then; what it displays of them (a reading pane, a conversation's
cards) it fetches again. Changes within 250 ms are gathered into one
notification per account; a change that takes longer sends several.

`notify.boardChanged` is sent when what `board.list` returns changed for
the accounts named (absent: any account): cases computed, changed or
dropped (new mail, a move, a flag, a merge), a decision of the user (also
to the client that made it), an annotation or commitment, a remind that
came due, a change of the board preferences, a triage run started or
ended. At most one per second; it carries no case, clients run
`board.list` again (and `board.get` of a case they show whose `version`
changed). A client that does not know it ignores it.

`notify.syncState` is sent immediately on every change of `status`,
`folderId`, `error`, `lastSync`, `pendingOutbox` or `failedOutbox`, and for
progress-only changes at most every 500 ms per account (the last value is
always delivered). Clients must not assume every intermediate `progress`
value.

`notify.authRequired` with `authUrl` means an OAuth2 flow is waiting: an
account with source `daemon` whose refresh token the provider no longer
accepts (or that has none, or lost it because `account.update` changed
its address or client). The backend opened the sign-in session itself;
the UI opens the URL in the user's browser (the GTK UI through
`gtk.URILauncher`: the OpenURI portal inside Flatpak, the desktop's
default handler otherwise; the macOS UI through `NSWorkspace`), the
backend's loopback listener completes the flow and follows up with
`notify.syncState`. The URL is valid for 10 minutes; a UI acting on it
later calls `account.oauthStart` with the `accountId` for a fresh one.
With reason `authFailed` the URL is included only when such a session is
already waiting. Reason `keyringError` never carries a URL: for a
`daemon` account it also follows a completed re-sign-in whose token the
keyring refused — kept in memory until the daemon stops when the keyring
failed this time (the account works now; the next `notify.syncState` may
follow), or not kept at all under `MALACHI_KEYRING=none` (the account
stays in `authRequired`).

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
- The handshake (§1.4) is part of the contract: the key file, the proofs
  and the rules before authentication change only with `protocolVersion`.
  `system.hello` keeps its name, its `clientNonce` and the
  `protocolVersion` member of its result in every version, so every
  client can read every daemon's version; `methodNotFound` for
  `system.hello` means protocol 1.
- `protocolVersion` has nothing to do with the application's release
  version (`system.info` reports both, and `docs/releasing.md` explains
  which is which). The two ship together in one Flatpak, so a mismatch
  means someone is running a daemon left over from an older install; the
  client compares the `protocolVersion` of the `system.hello` result,
  before it authenticates, never the release version.

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
- **1** (2026-09-06, compatible addition, unsynchronised folders): `Folder`
  gained `synced`; a `\All` folder is listed but never downloaded, on
  Gmail as the `archive` role, and `message.move` into it drops the local
  copy. `\Important` and `\Flagged` folders are no longer listed.
- **1** (2026-09-08, compatible change, thread ids): `MessageSummary.threadId`
  is now set for every message, in `message.list`, `message.get` and
  `notify.newMessage` alike: conversations are linked as messages are
  stored, per account, from `In-Reply-To`, `References` and shared
  `Message-ID`s, Graph accounts keep the server's conversation id. A store
  from an older daemon is linked in the background after the upgrade.
  `folder.subscribe` and `search.query` remain `notImplemented`.
- **1** (2026-09-08, compatible addition, conversation threading):
  `thread.list` and `thread.get` implemented — threads are computed per
  account and listed per folder with the aggregates taken over the members
  in that folder (§4.4); `ThreadListParams` gained `filter` (as
  `message.list`), `ThreadGetParams` gained `folderId`, `ThreadSummary`
  gained `latest` (the newest member in full); new limits
  `api.MaxThreadMessages` (500) and `api.MaxThreadParticipants` (8); thread
  cursors are bound to `thread.list`. IMAP fetches `References` with the
  envelope, so a conversation is known before the body is. Only
  `folder.subscribe` and `search.query` remain `notImplemented`.
- **1** (2026-09-08, compatible addition, reply and forward quoting):
  `draft.create` implemented — recipients, the `Re:`/`Fwd:` subject, and
  the original quoted as its sanitised HTML (compose mode, so the first
  `draft.save` is the identity) with its inline pictures copied into the
  attachment store under new `contentId`s; new params field
  `attribution` (the client's line above the quote, plain text) with
  limits `api.MaxDraftAttributionBytes` / `api.MaxDraftAttributionLines`;
  the result gained `quoted` (`html` | `text` | `none`), `blocked` and
  `skipped`, and no longer returns `sanitizeFailed`. New `attachment.get`
  for reading a stored attachment back. The text alternative the backend
  derives from HTML (`draft.save`, `message.body`) now renders
  `<blockquote>` with `> ` on every line; the HTML is unchanged, so the
  sanitiser version stays `"1"`. Only `folder.subscribe` and
  `search.query` remain `notImplemented`.
- **1** (2026-09-25, compatible addition, own OAuth2 flow): new source
  value `daemon` for `OAuth2Config.source` (Google IMAP/SMTP) and
  `GraphConfig.source` (Microsoft 365 Graph, with an `oauth2` block);
  `credentials.oauthSession`; `account.discover` `alternatives` (the own
  sign-in and, for Google, an app-password IMAP/SMTP account) and a
  `daemon` primary `config` without GNOME Online Accounts; new
  `account.oauthStart`, `account.oauthWait`, `account.oauthCancel`; new
  error code 1203 `oauthClientMissing`; `notify.authRequired` now carries
  `authUrl` for `daemon` accounts.
- **1** (2026-09-26, compatible addition, drafts on the server): saved
  drafts get a copy in the account's Drafts folder, replaced on every
  upload and deleted with `draft.delete` and `message.send`; a move or
  delete of such a copy deletes its draft. New `draft.open`; new
  `Draft.replaces`. A permanent delete on Gmail goes through the Trash.
- **1** (2026-09-26, compatible addition, pinned server certificates):
  `ServerConfig.certificateSha256` (an IMAP/SMTP endpoint accepts exactly
  that certificate; not with `security: none` or `authMethod: oauth2`);
  `tlsError` from IMAP/SMTP endpoints carries `TLSErrorData` (`reason`,
  `certificate`, `expectedSha256`) in `error.data`, in `account.test`
  results and in `SyncState.error`.
- **1** (2026-09-27, compatible addition, status line): new
  `SyncState.failedOutbox`, the account's outbox messages in state
  `failed` (they never counted in `pendingOutbox`), in `sync.status`,
  `account.list` and `notify.syncState`; a change of it alone sends
  `notify.syncState` immediately (§5).
- **1** (2026-09-26, compatible addition, search): `search.query` is
  implemented over a full-text index of the local store: scopes, Trash and
  Junk left out of account-wide searches, prefix matching without
  diacritics, date order, the `errors` line, the limits
  `api.MaxSearchQueryBytes`, `api.MaxSearchTerms` and `api.MaxSearchTotal`
  (§4.6). Only `folder.subscribe` remains `notImplemented`.
- **2** (2026-09-27, incompatible change, authenticated connections):
  every connection must complete a handshake before anything else (§1.4):
  new `system.hello` and `system.authenticate` prove on both sides, with
  HMAC-SHA256 over two nonces, knowledge of the 32-byte key the daemon
  writes for each run to `<socket>.key`. New error code 1005
  `unauthenticated`, sent before the daemon closes a connection that did
  anything else. Before authentication a connection may send at most
  4 KiB within 10 s, at most 32 such connections are served at a time,
  and none gets notifications; afterwards `system.hello` and
  `system.authenticate` answer `invalidRequest`. `protocolVersion` is also
  in the `system.hello` result, which a daemon of protocol 1 answers with
  `methodNotFound`; a client of protocol 1 gets `unauthenticated` on its
  first call.
- **2** (2026-09-27, compatible addition, compressed store and attachments
  on demand): new preferences `compressStore` and `attachmentOfflineDays`
  (absent in `config.set` = unchanged; defaults from
  `MALACHI_DEFAULT_COMPRESS_STORE` / `MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS`,
  stored on first use); the `config.set` result carries the effective
  values; new `Attachment.remote` and limit `api.LargeAttachmentMinBytes`;
  new `message.download` and `system.storage`; new error codes 1305
  `messageGone` and 1504 `partNotDownloaded`; `draft.create` and
  `draft.open` list `remote` parts in `skipped`.
- **2** (2026-09-27, compatible addition, attachments never stored): new
  preference `neverStoreAttachments` (no attachment stored, nor a picture
  the HTML shows of 100 KiB or more); under it `message.download` keeps the
  downloaded message in the daemon's memory only and the parts stay
  `remote`, served from memory while the copy lasts; new `message.body`
  field `remotePictures`.
- **2** (2026-09-29, compatible addition: issue-tracker accounts): new
  account kind `jira` (Jira Cloud and Data Center) with
  `AccountConfig.jira` (`JiraConfig`, limits `api.MaxJira*`) and its token
  in `credentials.password`; a `jira` account's folders are its selected
  spaces and the views `assignedToMe`, `watching` and `open`
  (`Folder.virtual`), every issue is a thread (`ThreadSummary.issue`) of
  its description, comments and status or assignee changes
  (`MessageSummary.issue`); addresses are unique per realm, so a `jira`
  account may share a mailbox's address. New `Account.capabilities` (nil
  from an older daemon means the mail list); `message.move` and
  `message.delete` answer invalidArgument for an account without the
  capability (a delete of outbox messages only needs none). New
  `account.detectSite` and `account.listSpaces`;
  `account.test` result gained `jira`. Comment drafts (`Draft.comment`,
  `draft.create` `reply` on a `jira` account, posted by `message.send`),
  forwarding a message of another account, a `jira` message from a mail
  account (`DraftCreateParams.messageAccountId`). Notification mails of the site
  may trigger a sync of their issue or be hidden in their mail account
  (`jira.notificationMail`): hidden messages leave listings, counts and
  search; new `notify.messagesChanged`, also sent by a `jira` account whose
  stored messages a pass rebuilt in place. `thread.get` without `folderId`
  and account-wide `search.query` leave out the views' copies.
- **2** (2026-09-30, compatible addition: issue status transitions): new
  capability `transition` (a `jira` account now has `["comment",
  "forward", "transition"]`) and new `issue.transitions` /
  `issue.transition` (§4.12): the transitions the site offers on the issue
  of a message, those needing input on the site marked, and one performed
  by its id followed by a refresh of the issue; new limit
  `api.MaxIssueTransitions`; no new error codes (a transition the site
  refuses is serverError, an issue it no longer shows messageGone).
- **2** (2026-10-01, compatible addition: Markdown pasted into compose):
  new `draft.markdown` (§4.5), text pasted into the compose editor rendered
  as sanitised HTML when it reads as Markdown; no new error codes.
- **2** (2026-10-01, compatible addition: the user's replies in a
  conversation): `ThreadSummary.sentCount` (`thread.list`, `thread.get`
  with `folderId`) counts the members in the account's sent folders that
  the folder lacks; `thread.get` gained `withSent`, which returns them as
  `sent`, outside the folder's members and aggregates; no new error codes.
- **2** (2026-10-01, compatible addition: trimmed quoted history):
  `message.body` gained `trimQuoted`, which cuts the quoted history of a
  reply off the body, and the result `quotedTrimmed`, set only when
  something was cut; `html`, `text`, `blocked`, `links`, `inlineParts`
  and `remotePictures` then describe the trimmed body. Without the
  parameter the result is unchanged; no new error codes.
- **2** (2026-10-01, compatible addition: bulk mail and unsubscribing):
  `MessageSummary.bulk` (`BulkInfo`: `newsletter`, `list` or `automated`,
  the `List-Id` and the sender's domain), classified by the daemon from
  `List-Id`, `List-Post`, `List-Unsubscribe`, `Precedence` and
  `Auto-Submitted` (`List-Post` is now among the curated `headers`);
  `Message.unsubscribe` (`UnsubscribeOffer`) in `message.get`; new
  `message.unsubscribe` (§4.3), a one-click POST after a check (the
  daemon's own DKIM verification, or for a Graph account Exchange's
  `Authentication-Results`), a `mailto:` request queued in the outbox, or a
  page for the client to open; an unverified one-click request sends
  nothing and answers `outcome: "unverified"` with the message's `mailto`
  address, never the one-click URL, and the client may repeat the call with
  `method: "mailto"`; new error code 1505 `unsubscribeFailed`.
  `ProtocolVersion` stays 2.
- **2** (2026-10-01, compatible addition: the board): new §4.13 —
  `board.list`, `board.get`, `board.setState`, `board.setDone`,
  `board.remind`, `board.archive`, `board.discardDraft`, `board.setDraft`,
  `board.queue`, `board.annotate`, `board.commit`, `board.setCommitment`,
  `board.preferences`, `board.setPreferences`, `board.runStart` and
  `board.runEnd`: cases of four states computed by the daemon's rules,
  the user's decisions (a suggested reply linked on the user's request
  among them), annotations and commitments of an assistant
  checked against verbatim quotes, triage runs, and the board's own
  preferences outside `Preferences`; new notification
  `notify.boardChanged`; new error codes 1106 `caseNotFound` and 1506
  `quoteNotFound`. `ProtocolVersion` stays 2.
- **2** (2026-10-02, compatible addition: suggested replies on the
  board): `Draft.local` (§4.5) — a draft kept on this device, not
  uploaded to the Drafts folder; `draft.save` reads it on the first save
  only; linking a draft to a case (`board.setDraft`, `board.annotate`
  `draftId`) makes it local and deletes its copy in the Drafts folder; a
  linked draft keeps a live case (`ruleReason: "kept"`) and the case from
  the prune; a reply that loses its case becomes an ordinary draft when
  the user edited it and is deleted when untouched. New `draft.get` (one draft as `draft.list` lists it) and
  `board.unflag` (clears the flags behind `hot.flagged`). No new error
  codes; `ProtocolVersion` stays 2.
- **2** (2026-10-08, compatible addition: board fixes, §4.13):
  `BoardCase.remindedAt` (a remind came due and the user has not acted
  since) and new mail ending a remind early; `BoardArchiveResult.moved`
  (each archived message and its former folder, for undo);
  `BoardUsage.lowerBound` in `board.runEnd` and in `usage24h`; new
  `ruleReason` `you.newContact` (an unknown sender with the user in
  `To`; `info.unknownSender` is now an unknown sender without the user
  in `To`); `hot.flagged` whoever wrote last; the user's forwards in a
  thread passed over like notes to self (rules version 6). No new
  methods or error codes; `ProtocolVersion` stays 2.
