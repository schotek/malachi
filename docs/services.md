# Malachi Mail web services

The project's own HTTP services, which the **daemon** calls (never a UI,
never the MCP bridge). Their implementation, data and administration live
in a private repository and are deployed as a Cloudflare Worker; this
document is the public contract the open-source daemon is written and
tested against. Nothing in it is secret: the daemon's code shows every
address, field and key anyway.

Status: draft (2026-10-28). Nothing below is implemented yet; the plan is
[`plans/update-check.md`](plans/update-check.md).

## 1. Host, paths and versioning

Everything is served by one host, `https://admin.malachimail.app`, split
by path:

| Path | Who calls it | Authentication |
|---|---|---|
| `/api/v1/…` | the daemon of every installed copy | none; rate limits only (Cloudflare Access bypasses this path) |
| every other path | the owner in a browser | Cloudflare Access, and the Worker checks the Access token and `Origin` itself; never called by the app |

- The daemon calls only paths under `/api/`; an answer from any other path
  (a sign-in page of Access, a redirect) is not an API answer and is
  treated as a server failure (§2.5).
- Every API path carries a contract version, `/api/v1/…`. Copies of the app
  stay installed for years, so a path, once published, is answered for as
  long as any supported version calls it. An incompatible change is a new
  prefix (`/api/v2/…`), served beside the old one.
- Requests carry no cookies, no installation or user identifier and no
  query string. The `User-Agent` is `MalachiMail/<version>` (the daemon's
  `main.version`), nothing more.
- Only `https`. The daemon follows no redirect off the host it asked.
- Answers are JSON (`application/json; charset=utf-8`) unless said
  otherwise. Unknown fields are ignored by the daemon; a missing required
  field makes the answer unusable (§2.5).
- Errors: an HTTP status and, when the body is JSON, `{"error": "<code>"}`
  with a stable lowercase code (`rateLimited`, `tooLarge`, `invalid`,
  `unavailable`). The daemon never shows the server's text to the user.
  `429` and `503` may carry `Retry-After` (seconds), which the daemon
  honours, capped at one day.

## 2. Updates: `GET /api/v1/updates`

Tells the daemon which versions exist and where each package of a version
is. The daemon only **reports** a newer version; it never installs one
(`plans/update-check.md`).

### 2.1 Request

```
GET /api/v1/updates HTTP/1.1
Host: admin.malachimail.app
User-Agent: MalachiMail/0.1.2
Accept: application/json
```

The whole manifest is returned to every caller: the daemon does not say
which platform or package it is, and picks its own entry from the answer.

A mirror of the same bytes is attached to every GitHub release as the
asset `updates.json`, so
`https://github.com/schotek/malachi/releases/latest/download/updates.json`
is a fallback when the API cannot be reached (§2.6). Both carry the same
signed envelope; the daemon trusts neither host, only the signature.

### 2.2 The signed envelope

```jsonc
{
  "keyId": "2026-1",            // which embedded public key signed it
  "payload": "<base64 (RFC 4648 §4, with padding) of the manifest's UTF-8 JSON bytes>",
  "signature": "<base64 of the Ed25519 signature over the decoded payload bytes>"
}
```

- The signature is over the **decoded payload bytes exactly**, so there is
  no canonical JSON to agree on. The daemon verifies it before it parses
  the payload; an envelope that does not verify is discarded whole.
- The public keys are compiled into the daemon (`internal/update`, keyed by
  `keyId`). The private key never leaves the owner's machine: neither the
  Worker nor GitHub holds it, so taking over either cannot sign a
  manifest. A new key is added to the daemon a release before it is used;
  an old one is removed only after no supported version needs it.
- Size limits: envelope ≤ 256 KiB, decoded payload ≤ 192 KiB.

### 2.3 The manifest (payload)

```jsonc
{
  "schema": 1,
  "issuedAt": "2026-10-28T12:00:00Z",   // RFC 3339, UTC
  "releases": [ Release ],              // newest first, ≤ 32
  "notice": Notice (opt)
}

Release {
  "version": "0.1.3",                   // MAJOR.MINOR.PATCH, no "v", no suffix
  "publishedAt": "2026-10-28T10:00:00Z",
  "security": false,                    // true: the update fixes a security problem
  "withdrawn": false,                   // true: no longer offered (§2.4)
  "notes": { "en": "…", "cs": "…" },    // plain text, ≤ 4000 bytes each
  "infoUrl": "https://malachimail.app/download.html",   // https, host malachimail.app
  "packages": { "<channel>": Package }  // ≤ 16
}

Package {
  "url": "https://github.com/schotek/malachi/releases/download/v0.1.3/Malachi-Mail-0.1.3-x64-Setup.exe",
  "sha256": "<64 lowercase hex digits>",
  "size": 75497472                      // bytes
}

Notice {                               // a message to every user of some versions
  "id": "n-2026-11-02",                 // shown once per id
  "below": "0.1.3" (opt),               // only to versions lower than this
  "text": { "en": "…", "cs": "…" },     // plain text, ≤ 1000 bytes each
  "url": "https://malachimail.app/…" (opt)
}
```

Channels (the package a build was installed from; the build tells its
daemon which one, `plans/update-check.md` §3):

| Channel | Package |
|---|---|
| `macos-dmg` | `Malachi-Mail-<v>-universal.dmg` |
| `windows-setup-x64`, `windows-setup-arm64` | `Malachi-Mail-<v>-<arch>-Setup.exe` |
| `windows-zip-x64`, `windows-zip-arm64` | `Malachi-Mail-<v>-<arch>.zip` |
| `linux-deb-amd64`, `linux-deb-arm64` | `malachi_<v>-1_<arch>.deb` |
| `linux-rpm-x86_64`, `linux-rpm-aarch64` | `malachi-<v>-1.fc42.<arch>.rpm` |
| `linux-flatpak-x86_64`, `linux-flatpak-aarch64` | `malachi-<v>-<arch>.flatpak` |

Unknown channels are ignored. Rules the daemon enforces on the payload
(any violation discards the whole manifest, §2.5):

- `schema` is `1`. A higher schema is not an error: the daemon keeps what
  it had and reports nothing new (a later version will read it).
- `issuedAt` is not older than the `issuedAt` of the last manifest this
  daemon accepted, and not more than one day in the future (protection
  against a replayed older manifest, which would hide a fix).
- Every `url` is `https` on `github.com` or `malachimail.app`; `infoUrl`
  and `Notice.url` on `malachimail.app`. No credentials, port or fragment.
- `version` strings parse as three decimal numbers without leading zeros;
  versions are unique.
- Every string is cleaned like other untrusted text before it reaches a
  client (control, bidi, format and invisible characters removed, line
  breaks kept, NFC) and is shown as plain text only, never as markup.

### 2.4 What the daemon makes of it

- **Available update**: the highest `version` that is newer than the
  daemon's own, not `withdrawn`, and has a package for the daemon's
  channel. A release without that channel's package is invisible to that
  channel; that is how a release is published for one platform first (the
  Windows packages are uploaded later by hand, `releasing.md` §7).
- `security` is true when it or any skipped-over newer release (newer than
  the running version, not withdrawn) is a security release.
- A build whose version is not a plain `MAJOR.MINOR.PATCH` (a development
  build, `0.1.2-3-gabc1234`) or that has no channel does not check at all.
- `withdrawn` hides a release that a daemon has not yet offered; one
  already shown as available disappears at the next check.

### 2.5 Failures

Network failure, a status other than 200, an envelope over the limits,
a bad signature, an unknown `keyId`, a payload that breaks §2.3: the
daemon keeps the last accepted manifest, records the failure class
(`network`, `server`, `signature`, `format`) and tries again at the next
scheduled check. None of this is shown to the user beyond a line in
*About* when they asked for a check themselves.

### 2.6 Server behaviour

- `Cache-Control: public, max-age=300`; `ETag` of the envelope bytes; the
  daemon sends `If-None-Match` and treats `304` as "unchanged".
- The served envelope is exactly the bytes the owner uploaded; the server
  never re-encodes it.
- Fallback order of the daemon: the API, then the GitHub asset, each tried
  once per check.

## 3. Feedback: `POST /api/v1/feedback` (sketch, not yet scheduled)

Reserved. The intended shape, so that the server and daemon agree when it
is built:

```jsonc
// request, Content-Type: application/json, ≤ 32 KiB
{
  "schema": 1,
  "kind": "bug|idea|other",
  "text": "…",                       // plain text, 1–8000 bytes
  "contact": "user@example.org" (opt),
  "app": { "version": "0.1.3", "channel": "macos-dmg", "os": "macOS 27.0", "locale": "cs" },
  "diagnostics": { … } (opt)         // only what the user saw and ticked
}
// 201 { "id": "fb_…" }
```

The user sees exactly what is sent before it goes; nothing from mail,
accounts or unredacted logs is ever attached. The server stores no IP
address. Abuse is limited by rate limits per IP at the edge and the size
limits; there is no secret in the app to authenticate with.

## 4. Changelog

- **v1** (draft, 2026-10-28): `GET /api/v1/updates`; `POST /api/v1/feedback`
  reserved. One host, `admin.malachimail.app`, the API under `/api/`
  (the owner's decision, 2026-10-28; first drafted as `api.malachimail.app`).
