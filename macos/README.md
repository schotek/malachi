# Malachi Mail for macOS

The macOS client of Malachi Mail: a native Swift/AppKit application over
the `malachid` JSON-RPC socket, the third client of the daemon next to the
GTK UI and the MCP bridge. It mirrors the GTK UI, which is the primary one
and the template (CLAUDE.md rule 4): the same panes, the same behaviour,
the same strings, native widgets. Nothing here changes what the daemon
does; the one backend piece it needed, a platform-neutral keyring helper,
is an extension point of the daemon, not macOS code in it. How the client
is built and how it stays in step with the GTK UI is in
[docs/macos-port.md](../docs/macos-port.md).

**Status: the full mail UI of the GTK application.** Accounts (the setup
assistant with autodetection and the browser sign-in for Gmail and
Microsoft 365, editing, signing in again, pausing, reordering, removing), the
folder sidebar with favourites and folding, the message list (flat and
grouped by conversation, with the All / Unread / Flagged filter), the
reader with a locked-down WebKit view, attachments, message actions,
notifications with sound, compose with the rich-text editor, drafts
(kept in the Drafts folder and opened from it for editing), reply and
forward with the quoted original, `mailto:` links, the settings
window, launch at login, running in the background, the Assistant menu
that hands mail to Claude Desktop or Claude Code and the experimental
assistant panel that asks the user's own Claude Code inside the main
window, with the rewrite in the compose window and the search in your
own words ([AI agents](#ai-agents)), and the Czech translation generated
from `po/` at build time. What is missing is listed
under [Not on macOS, not yet](#not-on-macos-not-yet).

Licence: GPL-3.0-or-later (everything outside `backend/`). Every source file
starts with the SPDX header; in `Package.swift` it sits on lines 2–3 because
line 1 must be the `swift-tools-version` comment.

## Requirements

- macOS 14 or newer (`LSMinimumSystemVersion` and the package's platform).
- Xcode with a Swift 6 toolchain: the package is `swift-tools-version 6.0`
  with strict concurrency. The tree is built and tested with Xcode 27
  (Swift 6.4); `python3` from the Xcode command-line tools generates the
  string catalogues.
- Go 1.25 for the daemon and the MCP bridge, which go into the bundle.

## Build and run

From the repository root:

```sh
make macos        # builds build/malachid and build/malachi-mcp, then the Swift package,
                  # generates the .lproj catalogues from po/, renders the icon
                  # (docs/malachi_icon.png → Malachi.icns with sips and iconutil)
                  # and assembles "build/Malachi Mail.app" with malachid,
                  # malachi-mcp and malachi-keychain inside
make run-macos    # runs the bundled executable from the terminal: the app's and the
                  # daemon's logs stay visible, Ctrl+C reaches both
make test-macos   # generates the catalogues, then swift test (the Czech cases need them)
open "build/Malachi Mail.app"   # the Finder way: Dock icon, notifications, About panel
```

The three targets exist on Darwin only; on Linux they print a hint and
exit. Quick iteration without the bundle:

```sh
swift build --package-path macos                       # or open macos/Package.swift in Xcode
MALACHI_DAEMON=$PWD/build/malachid swift run --package-path macos MalachiMail
python3 -m unittest macos/scripts/test_po2strings.py   # the catalogue generator's own tests
```

Run that way there is no bundle, so there are no desktop notifications
and, without `MALACHI_LOCALE_DIR` pointing at `build/macos/locale`, no
translations. The keyring helper is found beside the executable in
`.build/` when `swift build` built it too; otherwise the daemon runs with
`MALACHI_KEYRING=none` (see below).

`git describe` without a tag yields a bare hash; the bundle then carries
`0.0.0` as its short version and the hash under `MalachiVersion`.

### Signing, and why the Keychain asks

The bundle and the three binaries inside it are signed with the identity
in `SIGN`, ad hoc (`-`) by default. That is enough for the machine it was
built on; another Mac would show Gatekeeper's "damaged" dialog, since
Developer ID signing and notarisation are not part of this phase.

Ad-hoc signing has one cost: every rebuild is a new code identity, and the
login keychain ties each stored password to the identity that created it.
So after a rebuild the first password read brings the Keychain's own
"malachi-keychain wants to use your confidential information" prompt;
answer *Always Allow* once per build and it stays quiet until the next
rebuild. A self-signed code-signing certificate made in Keychain Access
keeps the identity stable across rebuilds:

```sh
make macos SIGN='Malachi Dev'     # the certificate's common name
```

## Layout

```
macos/
  Package.swift                 SwiftPM, tools 6.0 (strict concurrency), macOS 14+; no
                                SwiftPM resources on purpose (the hand-assembled .app
                                carries them in Contents/Resources)
  Makefile                      catalogues, app bundle assembly, driven by the root Makefile
  Resources/Info.plist.in       template; make substitutes version, bundle id, languages
  scripts/po2strings.py         po/malachi.pot + po/*.po → <lang>.lproj (stdlib python3)
  Sources/MalachiCore/          no AppKit; everything here is covered by swift test
    Transport/                  RPCClient (actor over NWConnection, the connection handshake
                                in connect()), JSONRPC, LineFramer, UnixSocketProbe,
                                DaemonKey (the key file): the ui/internal/client of macOS
    Daemon/                     DaemonSupervisor (actor over Foundation.Process), Paths,
                                Version: the ui/internal/daemon of macOS
    API/                        backend/pkg/api re-declared: every method of docs/api.md
                                with its params, result and default timeout, the
                                notifications, the enums, the error codes, the
                                handshake's types and proofs (Auth.swift)
    Model/, Compose/, Wizard/,  the pure logic of the GTK UI ported 1:1 (window model,
    HTML/, Text/, Assistant/    threads, folding, favourites, search, address parsing, mailto:,
                                quoting, wizard fields and results, the viewer and editor
                                documents, formatting, error texts, the Assistant's prompts
                                and links, the panel's command line, stream events and
                                Markdown subset, the rewrite's and the search's prompts
                                and answers from ui/internal/assistant)
    Controllers/                @MainActor view models over the RPC client, tested against
                                an in-process fake daemon
    I18n/                       L10n (T/N/C), the catalogue loader, plural rules, strftime
    Settings/                   UserDefaults with the GSettings keys
    Platform/                   the open directory for attachments, RPC timeouts, the
                                bridge runner, Claude Code's locator and its process for
                                the assistant panel
  Sources/MalachiMail/          AppKit: App/ (delegate, menu bar, alerts, login item,
                                quitting and starting Claude Desktop), MainWindow/, Sidebar/,
                                MessageList/, MessageView/, Windows/, Actions/, Assistant/,
                                Attachments/, Compose/, WebViews/, Preferences/,
                                AccountWizard/ (the pages of the assistant; the browser
                                sign-in is OAuthPageController), Notifications/,
                                Appearance/, Shared/
  Sources/MalachiKeychain/      malachi-keychain, the daemon's keyring helper
  Tests/MalachiCoreTests/       the Go UI tests ported 1:1 plus the transport, controller
                                and localisation tests; Fixtures/ holds FakeDaemon and
                                MailFixture (a scripted daemon on a real unix socket)
  Tests/MalachiKeychainTests/   protocol parsing; a Keychain round trip on request
```

## How it runs the daemon

Like the GTK UI: `malachid` is looked for in `MALACHI_DAEMON` (a path;
`none` or empty switches the automatic start off), else beside the app's
executable (`Contents/MacOS/malachid`), else on `PATH`. If nothing answers
on the socket, the daemon is started with `--socket`, `--config` and
`--store`, and the app waits up to 15 s for the socket. A daemon that
already answers (`make run-backend`, a debugger) is used as is and never
stopped. On quit the app sends SIGTERM to the daemon it started and waits
up to 15 s (the daemon gives its syncers 10 s), then SIGKILL. A daemon that
exits is restarted: at once after a single exit, then with a backoff that
doubles from 1 s to 60 s. The daemon's log goes to the app's stderr, which
is why `make run-macos` is the way to watch it.

## Keyring

macOS has no Secret Service, so the daemon gets the app's own helper:
the supervisor starts `malachid` with `MALACHI_KEYRING=helper` and
`MALACHI_KEYRING_HELPER=<bundle>/Contents/MacOS/malachi-keychain`. The
daemon runs the helper once per operation, git-credential style
(`malachi-keychain get|set|delete`, one JSON line on stdin, the value on
stdout for `get`, the outcome in the exit status; the protocol is
documented in `backend/internal/auth/helper`). Values never travel in
arguments, files, the environment or logs.

The helper keeps one generic-password item per account and key in the
**login keychain**, service `io.github.schotek.Malachi`, account
`<accountId>/<key>`, labelled `Malachi Mail: <accountId> (password)` in
Keychain Access. No data-protection keychain and therefore no entitlement
or provisioning; the price is the access prompt after a rebuild described
above. A prompt that is dismissed, or a helper that fails, is a
`keyringError` in the app, never a fallback to plaintext.

A `MALACHI_KEYRING` already in the environment wins over the bundled
helper, so a developer can still point the daemon elsewhere; without a
helper beside the executable the daemon runs with `MALACHI_KEYRING=none`,
where adding an account with a password fails with `keyringError`.

Testing the real thing brings up the prompt, so both round trips run only
on request: `MALACHI_KEYCHAIN_TEST=1 swift test --package-path macos
--filter KeychainRoundTripTests` on the Swift side, and on the Go side
`MALACHI_TEST_REAL_HELPER="$PWD/build/Malachi Mail.app/Contents/MacOS/malachi-keychain"
go test ./internal/auth/helper -run TestRealHelper` from `backend/`.

## Disk space

Many Macs have small disks, so the daemon the app starts gets two
defaults of its own: `MALACHI_DEFAULT_COMPRESS_STORE=1` (stored mail is
kept zstd-compressed) and `MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS=30`
(attachments of 100 KiB and up of messages older than 30 days stay on the
mail server and are downloaded when you open them; the pictures a message
shows are always kept). The supervisor sets them unless the environment
has them already with a value (an empty one, no default to the daemon, is
replaced), whatever the keyring, and the daemon stores a default as the
preference the first time it applies it, for an existing store as well
([docs/api.md §4.8](../docs/api.md#48-config)). A stored preference
always wins: *Settings → General → Mail* changes both (*Keep Attachments
Offline For*, *Compress Stored Mail*), and a daemon started another way
later (`make run-backend`, which sets nothing) keeps what is stored. A
daemon the app adopts because it already answers gets no environment from
the app; it applies whatever it was started with, which changes nothing
stored. *Disk Space Used* in the same group shows the store's size, what
compression saves and how much of the attachments is on the server only.
A chip of an attachment on the server shows a cloud symbol; opening,
previewing or saving it downloads the message first (a spinner after
0.4 s), and so does a forward, which asks whether to go on without them
only when the download fails (a message too large to download is
forwarded at once). *Never Store Attachments*, in the same group, is off
until you switch it on (the app gives the daemon no default for it): then
no attachment of any size is stored (the pictures a message shows still
are when smaller than 100 KiB, and so are the messages the daemon never
reduces, such as drafts or signed and encrypted mail), *Keep Attachments
Offline For* is greyed out, and every chip keeps its cloud, since a
download goes into the daemon's memory only (30 minutes unused at most,
never past its quit). The larger pictures stay on the server: a bar above
the message counts them and offers *Download Pictures*, and a reply
downloads the ones it quotes by itself (going on without them should
that fail). The copies
written for opening or previewing
are removed when the app quits, with or without the switch, and at the
next start after a crash.

## Where things are

| What | Path |
|---|---|
| Configuration | `~/Library/Application Support/Malachi Mail/config.toml` |
| Mail store | `~/Library/Application Support/Malachi Mail/store.db` |
| Store lock | `~/Library/Application Support/Malachi Mail/store.db.daemon.lock`: held by the running daemon, released by the system with its process; a second daemon for the same store exits |
| RPC socket | `~/.cache/malachi/run/rpc.sock` (`MALACHI_SOCKET` overrides; `XDG_RUNTIME_DIR` / `XDG_CACHE_HOME` honoured) |
| RPC key | beside the socket, its path plus `.key` (`~/.cache/malachi/run/rpc.sock.key`): a new key at every daemon start, mode 0600, removed when the daemon stops cleanly; the app reads it for every connection and keeps nothing ([docs/api.md §1.4](../docs/api.md#14-handshake)) |
| Attachments being opened or previewed, or handed to Claude | `~/Library/Caches/Malachi Mail/open/` (private, emptied at start and exit, entries older than an hour swept) |
| Preferences | `defaults` domain `io.github.schotek.Malachi`, the GSettings keys (the Assistant's `assistant-menu`, `assistant-target`, `assistant-model`, `assistant-claude-path` and `assistant-consent` among them) plus `command-r` |
| The assistant panel's Claude Code | the user's own `claude` (the path in *Settings → AI*, else `~/.local/bin`, `~/.claude/local`, Homebrew, nvm, `~/.npm-global/bin`, the `PATH`), run in `~/Library/Caches/Malachi Mail/assistant` (empty, private); nothing of the conversation is written anywhere |
| Passwords, sign-ins | login keychain, service `io.github.schotek.Malachi` (`password`, or `oauth2.refresh_token` for a browser sign-in) |
| MCP bridge | `Contents/MacOS/malachi-mcp` in the bundle, `build/malachi-mcp` in a checkout |
| Keyring helper | `Contents/MacOS/malachi-keychain` |

The socket keeps the daemon's own default so that `malachi-mcp`, the
repository's `.mcp.json` and `make run-backend` agree with the app without
any environment variable. macOS limits a unix socket path to 103 bytes;
the app checks the path at start and logs a message naming `MALACHI_SOCKET`
instead of leaving the daemon's "invalid argument". There is no App
Sandbox: with one, the data would move into the app container and the
socket with it, which would break the MCP bridge's default.

## Localisation

The GTK catalogue in `po/` is the single source of truth. `make macos` (and
`make test-macos`) run `scripts/po2strings.py`, which turns
`po/malachi.pot` into `en.lproj` and every `po/<lang>.po` into
`<lang>.lproj` (`Localizable.strings` for singular entries,
`Localizable.stringsdict` for plurals) under `build/macos/locale/`; the
bundle carries them in `Contents/Resources`. Keys are the GTK msgids
verbatim, so a string is translated once, for both clients; printf formats
are converted to Foundation's (`%s` → `%@`), strftime date patterns are
mapped to `DateFormatter` patterns at run time. The language follows the
system and the per-app choice in System Settings → Language & Region; to
try Czech from the terminal:

```sh
defaults write io.github.schotek.Malachi AppleLanguages '(cs)'
defaults delete io.github.schotek.Malachi AppleLanguages    # back to the system language
```

Strings that exist only on macOS (the standard menus, the Keyboard
settings group) stay English; they are marked
`// macOS-only string` in the sources. A new language needs only its
`po/<lang>.po`, plus its plural categories in `po2strings.py` if the script
does not know the language yet (it refuses to guess).

## Differences from the GTK UI

The GTK UI is the template; the client deviates only where macOS
conventions demand it. Everything else is meant to be the same, down to
the strings and the confirmation dialogs.

| On macOS | Instead of (GTK) | Why |
|---|---|---|
| One unified toolbar across the three panes, split by tracking separators; the window title is the selected folder's name, the subtitle its counts ("12 unread of 1234"), as in Mail | Three header bars with pane titles; the counts are the subtitle of the list's header bar (`Adw.WindowTitle`) | The macOS window model; the menu bar duplicates every item |
| The status line is a bar across the whole bottom edge of the window, under all three panes (sync state, unsent messages, the connection; a click opens the popover with each account's state and action) | The status line at the bottom of the sidebar, with the same popover | It stays in sight when the sidebar is folded away, which a narrow window does by itself |
| A narrow window folds the sidebar (< 900 pt) and then the list (< 600 pt); *View → Show Sidebar* (⌃⌘S) and *Show Message List* (⌥⌘L) bring them back, and widening restores what folded by itself. The assistant panel, where it exists, folds first (once the panes beside it would get less than 900 pt), and the panes' breakpoints count the width the open panel leaves them | Breakpoints with back navigation between panes | Decided; there is no navigation stack in AppKit's split view |
| When the list pane is folded, its toolbar items merge into the message section | — | How tracking separators behave |
| Banners (backend, sign-in, certificate, draft, outbox), the remote-image and pictures bars and the account wizard's notice are rounded cards inset from the pane's edges, in a subtle system fill, with an SF Symbol (orange for a problem, grey for information) and the text in the regular weight; the bars' spinner takes the buttons' place at the end | `Adw.Banner`: an accent-tinted strip across the whole width with a bold title; the remote-image and pictures bars grey strips with the spinner at their start | The Mac's own notices |
| The message list pages itself: reaching its end, or rows too few to fill the pane, loads the next page, a small spinner at the foot while it loads; *Load More*, a standard small button, appears only to retry a page that failed | The flat *Load More* button under the list when the rows do not fill it (the scroll edge loads by itself as well) | The Mac's lists page themselves, as Mail's |
| The All / Unread / Flagged filter is a button in the list's section of the toolbar with a menu, its icon filled while the list is filtered, and the same three items in *View*, as in Mail | A toggle group above the list | The Mac's filter, as Mail's; the list keeps the row |
| No main menu button in the toolbar: New Message, Settings… and About are in the menu bar, and New Message opens the list's section, before the folder's name | The primary menu button in the sidebar's header bar, New Message at its start | The menu bar is the Mac's main menu |
| The sidebar is a native source list; account headings fold with the hover *Hide* / *Show* button | Custom rows with a fold arrow | Native look, decided |
| *Settings* (⌘,) has no search field | `Adw.PreferencesDialog` with search | Decided |
| Mail search is the search field at the toolbar's trailing end (*Edit → Find…*, ⌘F): a search is on while it holds text, and the Folder / Account / All Accounts scope bar appears over the list, as in Mail; the single-key shortcuts are refused by menu validation while the field has the keyboard | A search bar over the list (Ctrl+F, the search button) with the entry and the scope toggles; the shortcuts are lifted while the entry has the keyboard | The Mac's search, as Mail's |
| Accounts are reordered by dragging the handle or with ⌥⌘↑ / ⌥⌘↓ | ⌃↑ / ⌃↓ | ⌃↑ / ⌃↓ are Mission Control |
| ⌘R is a setting (*Settings → General → Keyboard*): *Reply* as in Mail (⌘R Reply, ⇧⌘R Reply All, ⇧⌘F Forward, ⇧⌘N Check for New Mail), or *Check for New Mail* as on Linux (⌘R refresh, ⌥⌘R / ⌥⇧⌘R / ⌥⇧⌘F for the replies) | Ctrl+R refreshes | Decided: a choice, default Mail's |
| Alerts follow `NSAlert`: *Cancel* on the right is the default (Return) and takes Escape, the destructive button has no shortcut; "Save changes to this draft?" keeps *Save Draft* on Return | GTK button order, suggested/destructive styling; the same default and close responses | AppKit convention |
| Files opened, previewed or saved from a message get the quarantine attribute (type e-mail attachment, agent Malachi Mail) | No attribute | Gatekeeper and the opening application treat them as downloads; a gain |
| The chip of an attachment on the mail server shows the server symbol (a cloud, `icloud.and.arrow.down`) after the chip's two segments, or the spinner there while the message downloads; the symbol is not clickable, the segments act as on any chip | The server icon, or the spinner, inside the chip's preview button, after the size and before the arrow, so a click on it previews like the rest of the button | A segment of `NSSegmentedControl` holds one image and one label, which the type icon and the name with the size already take |
| The new-mail sound is the system *Glass* sound | The sound theme's `message-new-email` | macOS has no such event |
| The *Keyboard Shortcuts* item is left out of the primary menu | Present | It never worked in the GTK UI either |
| The message list uses the system selection highlight | Rounded, themed rows | `NSTableView` |
| One WebKit view per pane, reused between messages | A view per message | Without network nothing persists; the document is replaced |
| The assistant's GNOME Online Accounts page has neither *Open Online Accounts* nor *Check Again*, and the identity page never shows *Signed In on This Computer*; the sign-in banner's *Open Online Accounts* for an account of GNOME Online Accounts opens *Settings* | Both, for accounts of GNOME Online Accounts; the banner's button opens *Online Accounts* in GNOME Settings | GNOME Online Accounts does not exist on macOS; without it the daemon offers its own browser sign-in instead, so the page is normally not reached; the Settings are where such an account is edited or removed |
| In *Settings → Accounts*, clicking a row selects it; *Enabled* is the switch alone | The row activates its switch (`SetActivatableWidget`) | ⌥⌘↑ / ⌥⌘↓ reorder the selected row, so a click must select |
| The buttons of the remote-images and pictures bars are not reached by Tab (`refusesFirstResponder`) | Focusable | The bars are transient; Tab moves through the message |
| WebKitGTK's feature switches of `html_view.blp` (smooth scrolling, media, WebGL, WebAudio, page cache, DNS prefetch, hyperlink auditing) have no `WKWebView` equivalent | Each switched off in the Blueprint | Covered by the CSP, the content rule list and the non-persistent data store: the document has no script, no network and nothing to store |
| The pane widths are kept in the app's own defaults keys (`main-sidebar-width`, `main-list-width`), written from a visible window with nothing collapsed, and the assistant panel's in `main-assistant-width`, written when its divider is dragged | `Adw.NavigationSplitView` fractions in GSettings | `NSSplitView`'s autosave restores before the window has its frame and records the panes at their minimums |
| The message header keeps 12 pt above the subject, the same as below the date | `margin-top: 24` above the subject, 12 below the date | Equal margins were asked for; the pane already sits below the toolbar |
| The account wizard's sheet has a Cancel button at the bottom left of every page (Escape) and no close control in its header; while the browser sign-in waits, the page's own *Cancel* stands alone (Escape still closes the sheet and cancels the sign-in) | Close button in the header bar | macOS sheets carry no window controls; Cancel is the convention, and two Cancel buttons on one page would be ambiguous |
| The daemon's key file (`rpc.sock.key`) is used only when it belongs to the user and grants nothing to group or others, besides being a regular file, not a link, of 65 bytes in the key format; otherwise the connection is refused (*Backend unavailable*, the reason in the log) | The Go clients (the GTK UI, `malachi-mcp`, `api.ReadKeyFile`) check the file's type, size and format, not its owner and mode | Defence in depth: the daemon writes the file 0600 in its private directory, so a key another user owns or could read was not written by it or has been exposed. The Go clients cannot check owner and mode the same way on every platform they build for (CLAUDE.md rule 4); on macOS it costs nothing |
| New and existing stores are compressed, and the large attachments of messages older than 30 days stay on the mail server until opened (*Settings* shows *Compress Stored Mail* on and *Keep Attachments Offline For* at *1 month*; the supervisor's `MALACHI_DEFAULT_*`, see [Disk space](#disk-space)) | Stored uncompressed, every attachment kept (the daemon's built-in defaults; *Everything*, compression off) | Many Macs have 256 GB disks; on Linux a file system such as btrfs compresses by itself. The same daemon, chosen at run time, no platform code |

The link under the pointer is shown at the bottom of the message view as
in GTK (a user script that runs with content JavaScript off), and a masked
link is confirmed before it opens; those are security features, not
deviations.

## Not on macOS, not yet

- **Microsoft 365 / Outlook.com** are added through the daemon's own
  sign-in in the browser (Graph) with the client Malachi Mail ships;
  organisations that restrict consent approve the app once.
- **Gmail needs an app password or an OAuth client of your own.** No
  Google client is shipped (docs/macos-port.md §12 says why): the
  assistant offers an app password (IMAP/SMTP), or signs in through the
  browser once a client ID is in
  `~/Library/Application Support/Malachi Mail/config.toml`
  (`[oauth2.google]`; how to register one is in the
  [root README](../README.md#oauth-clients-for-gmail-and-microsoft-365)). A sign-in the provider revokes shows
  the banner *Sign in to … again in your browser*, and *Settings →
  Accounts* offers *Sign In…* on the account.
- **Recipient completion** comes from the addresses you have written to
  only. The GTK UI also searches the system address books through
  Evolution Data Server; there is no equivalent here and the daemon
  degrades silently.
- **Distribution**: Developer ID signing and notarisation, an App
  Sandbox, a LaunchAgent for the daemon and an update mechanism are not
  there; the bundle is built for the machine it was built on
  ([docs/macos-port.md](../docs/macos-port.md) §12).

## Troubleshooting

- **Launch at Login is refused or reports an unknown developer.** An
  ad-hoc signed build has no stable identity for `SMAppService`; the
  switch reverts, a toast explains, and when macOS wants approval the app
  opens *System Settings → General → Login Items*. Sign with a stable
  identity (`SIGN=`) or test the setting on a bundle that stays put.
- **No desktop notifications.** They need the bundle: start the app with
  `open "build/Malachi Mail.app"` (or from the Finder) rather than
  `swift run`. `make run-macos` runs the bundled executable, which is
  enough; the first notification asks for permission. Nothing is shown
  while the main window is the key window, as in GTK.
- **The daemon should not be started by the app.** `MALACHI_DAEMON=none`;
  the window then shows the connection state until something answers on
  the socket (`make run-backend` in another terminal).
- **The status line says "Protocol mismatch: UI 2, backend 1".** An older
  `malachid` still answers on the socket, one from before the
  authenticated connections: typically `make run-backend` in another
  terminal from an older checkout, or a daemon left running by an older
  build. The app uses a running daemon and never stops somebody else's, so
  stop that one (Ctrl+C in its terminal, or `pkill -x malachid`); the app
  then starts its own at its next attempt, a few seconds later. A backend
  number higher than the UI's means the app is the older one: rebuild it.
- **"Backend unavailable" although malachid runs.** The connection was
  refused in the handshake; the log says why once (`make run-macos`
  shows it), for example that `rpc.sock.key` belongs to another user or
  grants group or others any access, or that the process on the socket
  did not prove it holds the key (a socket that is not your daemon's).
- **A different socket.** `MALACHI_SOCKET=/path/rpc.sock` for the app and
  the daemon it starts; `malachi-mcp` reads the same variable. Keep it
  under 103 bytes.
- **The Keychain asks again after every build.** Expected with ad-hoc
  signing; see [Signing](#signing-and-why-the-keychain-asks).
- **The daemon refuses to start with `MALACHI_KEYRING=helper`.** The
  helper path must be absolute and name an executable regular file; the
  error names `MALACHI_KEYRING_HELPER`. In the bundle that is automatic.
- **English although the system is Czech.** The catalogues are missing:
  build with `make macos`, not `swift build`; for `swift run` set
  `MALACHI_LOCALE_DIR=$PWD/build/macos/locale` after `make -C macos locale`.

## AI agents

While the app runs, the repository's `.mcp.json` works unchanged: Claude
Code spawns `build/malachi-mcp`, which connects to the same socket. Another
MCP client points at `Contents/MacOS/malachi-mcp` in the bundle. Tools,
flags and the security model are in [docs/mcp.md](../docs/mcp.md).

*Settings → AI → Register with Claude* puts the bundled bridge into the
MCP configuration of Claude Desktop and Claude Code on this Mac, or takes
it out again: the switch runs `malachi-mcp status`, `install` and
`uninstall` and shows what the bridge reports, so the app never edits
those files itself. The AI page exists in both UIs with the same strings,
so it is not a deviation. Outside the bundle (`swift run`) there is no
bridge beside the executable; the row is then insensitive and a toast
says so.

Claude Desktop reads its MCP servers only when it starts and, while it
runs, rewrites its configuration file from memory, undoing an entry
written or removed meanwhile ([docs/mcp.md](../docs/mcp.md)). Flipping
*Register with Claude* while Claude Desktop runs (and the bridge reports
it as installed) therefore asks *Restart Claude Desktop?* before anything
is written. *Restart Claude Desktop* asks it to quit (the ordinary quit
request, as from the Dock), waits until it has quit (at most 20 s), runs
`install` or `uninstall` and starts it again in the background; when it
did not quit in time a toast says so, and the change is written anyway
and stays pending. *Later* writes the change at once and keeps it
pending. While a change is pending, a *Claude Desktop* row under the
switch says it picks up the change when it restarts and offers
*Restart* (the same restart), and as soon as Claude Desktop quits by
itself the app writes the change once more, which then sticks, and the
row goes. Pending lasts for the app's run. Claude Code keeps the entry
while it runs and is left alone. GTK has no equivalent yet: macOS leads
here with the texts of `ui/internal/assistant` (`RestartTexts`), so it is
not a deviation.

The **Assistant** menu hands the selected mail to Claude on this Mac with
a prepared question, prefilled and unsent: it opens Claude Desktop
(`claude://`) or Claude Code in a terminal (`claude-cli://`), the user
reads the question, completes it and sends it there, and Claude reads the
mail itself through the registered bridge. The question carries only the
opaque ids of the account and the messages (a folded conversation's
members in the folder, newest first) or of the folder, never a subject, a
name or a file name. It is the sparkles button before *More Actions* in
the main window and in a message window, and *Message → Assistant* in the
menu bar: *Summarize*, *Draft a Reply…*, *Tasks and Deadlines*, *Ask
About This Message…*, *Summarize Unread in This Folder* (the main window
only), then *Open In* with *Claude Desktop* and *Claude Code*. The
Assistant exists only while *Register with Claude* is on: without the
bridge in any Claude client the button, the submenu and the attachment
item are gone, and the Assistant group of *Settings → AI* is insensitive
with its switch off, saying why (its preference is kept for when the
bridge is registered again). The menu always uses the app chosen under
*Open In*, never the other one instead. An app whose links nothing
handles cannot be chosen (Claude Code's handler exists once it has been
used in a terminal); while the chosen app cannot read the mail (not
installed, or the bridge not registered in it) the actions are disabled,
a disabled line above *Set Up the Assistant…* says why, and *Set Up the
Assistant…* opens *Settings → AI*. An attachment's menu has *Ask the
Assistant…* after *Open*, disabled while the chosen app is not
installed: the file is written as for *Open* (downloaded
first when it is only on the server, with the quarantine attribute) and
attached to a Cowork task in Claude Desktop, which asks you to confirm
it, or becomes Claude Code's working directory. *Settings → AI →
Assistant* has *Show the Assistant Menu* (`assistant-menu`) and *Open
In* (`assistant-target`, which the menu's choice changes too), whose
subtitle says why the chosen app cannot be used. The shared logic and every string are GTK's
(`ui/internal/assistant`, `po/`), and GTK has the same menu behind its ✦
button, so this is no deviation. The link formats and the
limits are in [docs/mcp.md](../docs/mcp.md#hand-off-from-the-app-the-assistant-menu).

The third choice under *Open In*, **In App (Experimental)**, keeps the
conversation in the app: the **assistant panel**, the main window's
inspector on the right. It runs the user's own Claude Code CLI (`claude
-p` with stream-json, one process per conversation, the bundled bridge as
its only MCP server and only the bridge's read and draft tools allowed;
the command line and why it is safe are in
[docs/mcp.md](../docs/mcp.md#the-panel-in-the-app-experimental)). Signing
in is Claude Code's alone: the app never sees a credential, it only asks
`claude auth status` whether Claude Code is signed in, and otherwise says
to run `claude` in Terminal and sign in. The choice is disabled while no
`claude` is found. The panel exists only while the Assistant does and In
App is chosen; then the toolbar ends with the inspector button after the
search field and *View → Show Assistant* / *Hide Assistant* toggles it.
It starts folded, and a narrow window folds it before the sidebar. From
the top: *Assistant* with *Claude Code · <model>* and *New Conversation*;
the context chip, which follows the list's selection until the
conversation's first question (*Selected message*, *Selected conversation
(n messages)*, a folded conversation's members fetched when asked; never
an Outbox message) or says *All mail*, and whose ✕ leaves the selection
out until it changes; from the first question on the conversation keeps
its context and the chip says what it is about (*Conversation about:*
and the subject, or *Conversation about n messages*, without the ✕);
*Summarize*, *Draft a Reply…* and *Tasks and Deadlines*, which then act
on the newest context of the conversation; while the selection is not
part of it, the bar *Another message is selected* with *New
Conversation* and *Add to Conversation* (the next question names the
added message to the model); the transcript (the questions, the
answers as plain text with a small Markdown subset drawn as fonts, never
HTML, a line with a spinner while a tool reads mail, *A draft is ready*
with *Open Draft*, which opens the draft only after `draft.list` has it,
errors with *Try Again*); the question field (Return sends, Shift-Return
starts a new line; one to five lines) with *Send*, or *Stop* while an
answer comes; and the line *Mail you ask about is sent to Claude under
your account*. The first question ever asks *Send Mail to Claude?* as a
sheet (the answer is kept in `assistant-consent`). A link in an answer
is opened only after *Open This Link?* has shown where it leads. With In
App chosen, the Assistant menus run in the panel: it unfolds, takes the
selection (from a message window: that message, with the main window
brought forward; a conversation that keeps its context gets it added
when it is not part of it) and *Summarize* or *Tasks and Deadlines* are sent at
once, while *Draft a Reply…*, *Ask About This Message…* and an
attachment's *Ask the Assistant…* wait for your words in the field (its
placeholder says what for; Escape or the ✕ above it drops the action);
*Summarize Unread in This Folder* asks about the folder selected in the
sidebar. An attachment can be asked about only when the bridge reads its
type (plain text, CSV, Markdown, calendar, JSON, PNG, JPEG, GIF, WebP).
*Settings → AI → Assistant* shows two more rows while In App is chosen:
*Claude Code* (its path, version and whether it is signed in, or that it
was not found) with *Choose…* for another `claude` (`assistant-claude-path`;
choosing the one found automatically goes back to looking), and *Model*
(*Sonnet*, the default, *Haiku* or *Opus*: `assistant-model`, used from
the next conversation). The conversation lives in memory only: *New
Conversation*, *Stop* (the next question starts a new one) or quitting the
app ends Claude Code. As with the menu, GTK has the same panel (its
conversation `ui/internal/assistantpanel`, the strings
`ui/internal/assistant`'s), so this is no deviation.

Two more uses of the same Claude Code exist while the panel could run
(the Assistant shown, In App chosen and `claude` found): one-shot requests
that read no mail, without the bridge and without any tool (one message
on stdin, the answer in the result; `AssistantRequest`), under the same
consent (*Send Mail to Claude?* on the window that asks, the first time
ever; `assistant-consent`) and the model of *Settings → AI*. The
**rewrite** is the compose window's ✦ *Assistant* button before the draft
menu: its popover works on the selection (*Rewrite Selection*) or, with
nothing selected, on your own text, which is what the editor holds above
the line the window put over the quoted original of a reply or a forward
(the whole text when there is no such line, as in a new message or a
draft reopened from Drafts; *Rewrite Your Text*). *More Polite*,
*Shorter*, *Fix Mistakes* and *Translate to English* ask at once, *Your
own instruction…* on Return; the answer streams in under *Rewriting…*
and is shown as plain text; *Replace* (the default button) puts it in
place of the passage and *Insert Below* after it, both as plain text
through the editor bridge, one edit that ⌘Z takes back, and the draft
counts as changed; *Discard*, Escape, a click elsewhere or closing the
window ends a running request. Only the passage and the instruction go
to Claude. The **search in your own words** is the search field's
magnifier menu, *Search in Your Own Words*, or ⌥↩ in the field: the
typed words go to Claude Code with the search syntax of `search.query`
(the answer in a JSON shape, `--json-schema`), the field says
*Converting the search…* and takes no typing meanwhile, and the query
that comes back replaces the words and is searched as if typed and
Return pressed, in the current scope (a list folded by a narrow window
unfolds); a failure is the toast *The search could not be converted: …*
and the words stay. Only the typed words go to Claude, no mail. The
editor bridge carries two macOS additions for the rewrite
(`rewriteTarget`, `rewriteApply`), and the compose window keeps the
attribution line it asked `draft.create` for (`ComposeParams.attribution`);
the GTK widgets follow this port.
