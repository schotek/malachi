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
window, launch at login, running in the background, and the Czech
translation generated from `po/` at build time. What is missing is listed
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
    Transport/                  RPCClient (actor over NWConnection), JSONRPC, LineFramer,
                                UnixSocketProbe: the ui/internal/client of macOS
    Daemon/                     DaemonSupervisor (actor over Foundation.Process), Paths,
                                Version: the ui/internal/daemon of macOS
    API/                        backend/pkg/api re-declared: every method of docs/api.md
                                with its params, result and default timeout, the
                                notifications, the enums, the error codes
    Model/, Compose/, Wizard/,  the pure logic of the GTK UI ported 1:1 (window model,
    HTML/, Text/                threads, folding, favourites, address parsing, mailto:,
                                quoting, wizard fields and results, the viewer and editor
                                documents, formatting, error texts)
    Controllers/                @MainActor view models over the RPC client, tested against
                                an in-process fake daemon
    I18n/                       L10n (T/N/C), the catalogue loader, plural rules, strftime
    Settings/                   UserDefaults with the GSettings keys
    Platform/                   the open directory for attachments, RPC timeouts
  Sources/MalachiMail/          AppKit: App/ (delegate, menu bar, alerts, login item),
                                MainWindow/, Sidebar/, MessageList/, MessageView/, Windows/,
                                Actions/, Attachments/, Compose/, WebViews/, Preferences/,
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

## Where things are

| What | Path |
|---|---|
| Configuration | `~/Library/Application Support/Malachi Mail/config.toml` |
| Mail store | `~/Library/Application Support/Malachi Mail/store.db` |
| RPC socket | `~/.cache/malachi/run/rpc.sock` (`MALACHI_SOCKET` overrides; `XDG_RUNTIME_DIR` / `XDG_CACHE_HOME` honoured) |
| Attachments being opened | `~/Library/Caches/Malachi Mail/open/` (private, emptied at start and exit, entries older than an hour swept) |
| Preferences | `defaults` domain `io.github.schotek.Malachi`, the GSettings keys plus `command-r` |
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
| A narrow window folds the sidebar (< 900 pt) and then the list (< 600 pt); *View → Show Sidebar* (⌃⌘S) and *Show Message List* (⌥⌘L) bring them back, and widening restores what folded by itself | Breakpoints with back navigation between panes | Decided; there is no navigation stack in AppKit's split view |
| When the list pane is folded, its toolbar items merge into the message section | — | How tracking separators behave |
| Banners (backend, sign-in, certificate, draft, outbox), the remote-image bar and the account wizard's notice are rounded cards inset from the pane's edges, in a subtle system fill, with an SF Symbol (orange for a problem, grey for information) and the text in the regular weight | `Adw.Banner`: an accent-tinted strip across the whole width with a bold title; the remote-image bar a grey strip | The Mac's own notices |
| The message list pages itself: reaching its end, or rows too few to fill the pane, loads the next page, a small spinner at the foot while it loads; *Load More*, a standard small button, appears only to retry a page that failed | The flat *Load More* button under the list when the rows do not fill it (the scroll edge loads by itself as well) | The Mac's lists page themselves, as Mail's |
| The All / Unread / Flagged filter is a button in the list's section of the toolbar with a menu, its icon filled while the list is filtered, and the same three items in *View*, as in Mail | A toggle group above the list | The Mac's filter, as Mail's; the list keeps the row |
| No main menu button in the toolbar: New Message, Settings… and About are in the menu bar, and New Message opens the list's section, before the folder's name | The primary menu button in the sidebar's header bar, New Message at its start | The menu bar is the Mac's main menu |
| The sidebar is a native source list; account headings fold with the hover *Hide* / *Show* button | Custom rows with a fold arrow | Native look, decided |
| *Settings* (⌘,) has no search field | `Adw.PreferencesDialog` with search | Decided |
| Accounts are reordered by dragging the handle or with ⌥⌘↑ / ⌥⌘↓ | ⌃↑ / ⌃↓ | ⌃↑ / ⌃↓ are Mission Control |
| ⌘R is a setting (*Settings → General → Keyboard*): *Reply* as in Mail (⌘R Reply, ⇧⌘R Reply All, ⇧⌘F Forward, ⇧⌘N Check for New Mail), or *Check for New Mail* as on Linux (⌘R refresh, ⌥⌘R / ⌥⇧⌘R / ⌥⇧⌘F for the replies) | Ctrl+R refreshes | Decided: a choice, default Mail's |
| Alerts follow `NSAlert`: *Cancel* on the right is the default (Return) and takes Escape, the destructive button has no shortcut; "Save changes to this draft?" keeps *Save Draft* on Return | GTK button order, suggested/destructive styling; the same default and close responses | AppKit convention |
| Files opened or saved from a message get the quarantine attribute (type e-mail attachment, agent Malachi Mail) | No attribute | Gatekeeper and the opening application treat them as downloads; a gain |
| The new-mail sound is the system *Glass* sound | The sound theme's `message-new-email` | macOS has no such event |
| The *Keyboard Shortcuts* item is left out of the primary menu | Present | It never worked in the GTK UI either |
| The message list uses the system selection highlight | Rounded, themed rows | `NSTableView` |
| One WebKit view per pane, reused between messages | A view per message | Without network nothing persists; the document is replaced |
| The assistant's GNOME Online Accounts page has neither *Open Online Accounts* nor *Check Again*, and the identity page never shows *Signed In on This Computer*; the sign-in banner's *Open Online Accounts* for an account of GNOME Online Accounts opens *Settings* | Both, for accounts of GNOME Online Accounts; the banner's button opens *Online Accounts* in GNOME Settings | GNOME Online Accounts does not exist on macOS; without it the daemon offers its own browser sign-in instead, so the page is normally not reached; the Settings are where such an account is edited or removed |
| In *Settings → Accounts*, clicking a row selects it; *Enabled* is the switch alone | The row activates its switch (`SetActivatableWidget`) | ⌥⌘↑ / ⌥⌘↓ reorder the selected row, so a click must select |
| The buttons of the remote-images bar are not reached by Tab (`refusesFirstResponder`) | Focusable | The bar is transient; Tab moves through the message |
| WebKitGTK's feature switches of `html_view.blp` (smooth scrolling, media, WebGL, WebAudio, page cache, DNS prefetch, hyperlink auditing) have no `WKWebView` equivalent | Each switched off in the Blueprint | Covered by the CSP, the content rule list and the non-persistent data store: the document has no script, no network and nothing to store |
| The pane widths are kept in the app's own defaults keys (`main-sidebar-width`, `main-list-width`), written from a visible window with nothing collapsed | `Adw.NavigationSplitView` fractions in GSettings | `NSSplitView`'s autosave restores before the window has its frame and records the panes at their minimums |
| The message header keeps 12 pt above the subject, the same as below the date | `margin-top: 24` above the subject, 12 below the date | Equal margins were asked for; the pane already sits below the toolbar |
| The account wizard's sheet has a Cancel button at the bottom left of every page (Escape) and no close control in its header; while the browser sign-in waits, the page's own *Cancel* stands alone (Escape still closes the sheet and cancels the sign-in) | Close button in the header bar | macOS sheets carry no window controls; Cancel is the convention, and two Cancel buttons on one page would be ambiguous |

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
- **Search** is not implemented anywhere yet: the daemon answers
  `notImplemented`, so *Edit → Find…* is disabled and the toolbar's search
  item stays out of the default set.
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
