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

Two things exist here before they exist in the GTK UI, on purpose
([docs/architecture.md §7](../docs/architecture.md#7-open-decisions),
"macOS first for Jira"): **Jira accounts** (`kind: jira`, [docs/api.md
§4.1](../docs/api.md#41-account): the assistant, the JIRA heading in the
sidebar with the Assigned to Me / Watching / Open views above the spaces,
the always-grouped list with status pills and event rows, the issue card
over a message, Comment in place of Reply with the comment window,
forwarding an issue's message from a mail account, the account's settings
sheet with the spaces, the views, the notification mail and the bot
comments) and the **conversation view** (a folded conversation row shows
the whole conversation stacked in the reading pane, for mail and Jira
alike). Their pure logic is a Go reference the GTK UI will use
(`ui/internal/jira`, `ui/internal/capabilities`,
`ui/internal/conversation`); see [Swift-first](#swift-first-what-the-gtk-ui-still-has-to-mirror).

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
    HTML/, Text/                threads, folding, favourites, search, address parsing, mailto:,
                                quoting, wizard fields and results, the viewer and editor
                                documents, formatting, error texts); Model/ also holds the
                                conversation view's model and layout (Conversation.swift,
                                ConversationLayout.swift, the port of ui/internal/conversation),
                                the capabilities rules (Capabilities.swift, of
                                ui/internal/capabilities) and the Jira parts of the reading
                                pane, the list and the accounts page (IssueReading, ChipPlan,
                                MailModel+Jira)
    Jira/                       the port of ui/internal/jira: the texts and view models of
                                the assistant, the sidebar, the list, the issue card, the
                                comment window and the account settings (JiraWizard, JiraView,
                                JiraCompose, JiraSettings, JiraURL, JiraPattern: the RE2
                                check of a filter, after Go's regexp/syntax)
    Controllers/                @MainActor view models over the RPC client, tested against
                                an in-process fake daemon (JiraWizardController,
                                JiraAccountController and ConversationController among them)
    I18n/                       L10n (T/N/C), the catalogue loader, plural rules, strftime
    Settings/                   UserDefaults with the GSettings keys
    Platform/                   the open directory for attachments, RPC timeouts
  Sources/MalachiMail/          AppKit: App/ (delegate, menu bar, alerts, login item;
                                Integration+Jira and Integration+Conversation wire the two
                                features), MainWindow/ (ActionPresentation: what the
                                capabilities make of the toolbar and the menus),
                                Sidebar/, MessageList/, MessageView/ (the single-message
                                pane, IssueCardView, and the conversation view:
                                ReadingPaneViewController swaps between them,
                                ConversationViewController, ConversationCardView,
                                ConversationCardHeader, ConversationEventRow,
                                ConversationRow, MessageParts shared with the pane),
                                Windows/ (MessageDisplay: the fan-out to a view showing
                                several messages), Actions/, Attachments/, Compose/
                                (CommentHeaderView and ComposeWindowController+Comment:
                                the comment mode), WebViews/ (MessageWebView has the sized
                                mode of a conversation card), Preferences/ (JiraAccount/:
                                the settings sheet of a Jira account), AccountWizard/ (the
                                pages of the assistant; the browser sign-in is
                                OAuthPageController; Jira/: the Jira assistant's sheet and
                                pages), Notifications/, Appearance/, Shared/ (IssuePill)
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
| A test copy | `MALACHI_DATA_DIR=<dir>` puts the configuration, the store and the messages there instead, for the app and the daemon it starts; with its own `MALACHI_SOCKET` a test build runs beside the everyday one on a copy of the store (a migration is forward-only, so a branch is tried on a copy first) |
| Store lock | `~/Library/Application Support/Malachi Mail/store.db.daemon.lock`: held by the running daemon, released by the system with its process; a second daemon for the same store exits |
| RPC socket | `~/.cache/malachi/run/rpc.sock` (`MALACHI_SOCKET` overrides; `XDG_RUNTIME_DIR` / `XDG_CACHE_HOME` honoured) |
| RPC key | beside the socket, its path plus `.key` (`~/.cache/malachi/run/rpc.sock.key`): a new key at every daemon start, mode 0600, removed when the daemon stops cleanly; the app reads it for every connection and keeps nothing ([docs/api.md §1.4](../docs/api.md#14-handshake)) |
| Attachments being opened or previewed | `~/Library/Caches/Malachi Mail/open/` (private, emptied at start and exit, entries older than an hour swept) |
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
| The pane widths are kept in the app's own defaults keys (`main-sidebar-width`, `main-list-width`), written from a visible window with nothing collapsed | `Adw.NavigationSplitView` fractions in GSettings | `NSSplitView`'s autosave restores before the window has its frame and records the panes at their minimums |
| The message header keeps 12 pt above the subject, the same as below the date | `margin-top: 24` above the subject, 12 below the date | Equal margins were asked for; the pane already sits below the toolbar |
| The account wizard's sheet has a Cancel button at the bottom left of every page (Escape) and no close control in its header; while the browser sign-in waits, the page's own *Cancel* stands alone (Escape still closes the sheet and cancels the sign-in) | Close button in the header bar | macOS sheets carry no window controls; Cancel is the convention, and two Cancel buttons on one page would be ambiguous |
| The daemon's key file (`rpc.sock.key`) is used only when it belongs to the user and grants nothing to group or others, besides being a regular file, not a link, of 65 bytes in the key format; otherwise the connection is refused (*Backend unavailable*, the reason in the log) | The Go clients (the GTK UI, `malachi-mcp`, `api.ReadKeyFile`) check the file's type, size and format, not its owner and mode | Defence in depth: the daemon writes the file 0600 in its private directory, so a key another user owns or could read was not written by it or has been exposed. The Go clients cannot check owner and mode the same way on every platform they build for (CLAUDE.md rule 4); on macOS it costs nothing |
| New and existing stores are compressed, and the large attachments of messages older than 30 days stay on the mail server until opened (*Settings* shows *Compress Stored Mail* on and *Keep Attachments Offline For* at *1 month*; the supervisor's `MALACHI_DEFAULT_*`, see [Disk space](#disk-space)) | Stored uncompressed, every attachment kept (the daemon's built-in defaults; *Everything*, compression off) | Many Macs have 256 GB disks; on Linux a file system such as btrfs compresses by itself. The same daemon, chosen at run time, no platform code |
| The Jira assistant (*File → Add Jira Account…*, or the *+* pull-down in *Settings → Accounts*) is a sheet of the mail assistant's size with the pages site → credentials → spaces, Back and the page title in a 44 pt header, Cancel at the bottom left (`AccountWizard/Jira/`, `JiraWizardController`); editing an account opens it on the credentials page to replace the token | Not in the GTK UI yet: the pages, the texts and the rules are the Go reference `ui/internal/jira/wizard.go` | The same sheet as the mail assistant's; the GTK dialog follows the same pages |
| The settings of a Jira account are a sheet (*Settings → Accounts*, the edit button on its row; *Edit Account…* from a banner): one scrolling page with the site (read only, *Replace Token…*), the spaces, the synchronisation, the views with the closed statuses, the notification e-mails and the bot comments, Cancel and Save below (`Preferences/JiraAccount/`, `JiraAccountController`) | Not in the GTK UI yet: the form, its checks and its texts are `ui/internal/jira/settings.go` | A mail account is edited in the assistant, which builds its pages from `imap` and `smtp`; a Jira account has neither, so every "edit account" route asks `accountEditor` first |
| The heading of a Jira account in the sidebar carries a small "JIRA" capsule after its name (`accountHeaderBadge`, `SidebarHeaderCellView`); its row in *Settings → Accounts* a ticket symbol and the site's host under the name; the views (Assigned to Me, Watching, Open) sit above the spaces with `folder.badge.gearshape` | Not in the GTK UI yet (`ui/internal/jira` `KindBadge`, `VirtualRank`, `VirtualFolderTitle`) | Decided; a brand name, never translated |
| A folded conversation row (two or more members in the folder; a Jira folder is always grouped) shows the whole conversation in the reading pane: native cards on a timeline, oldest first, scrolled to the newest, each HTML body in a locked web view of its own sized to its document (at most eight alive), a Jira conversation with its issue card on top and its status and assignee changes as compact rows; only the newest member that is not an event is marked read; Space and ⇧Space in the list page through it (`MessageView/Conversation*`, `ConversationController`) | A conversation row shows, and marks read, its newest member (`window.go`); the members are expanded in the list | Decided ([docs/architecture.md §7](../docs/architecture.md#7-open-decisions), "Conversation view"); the pure model is the Go reference `ui/internal/conversation`, the security of the per-card views in [docs/security.md §3.2](../docs/security.md#32-defences) |
| `MALACHI_DATA_DIR` names the data directory (`config.toml`, `store.db`, the messages) instead of `~/Library/Application Support/Malachi Mail`, for the app and the daemon it starts (`Daemon/Paths.swift`) | `--config` / `--store` flags of the daemon, XDG directories | The Windows client's override, taken over so that a test build runs beside the everyday one on a copy of the store (pair it with its own `MALACHI_SOCKET`) |

The link under the pointer is shown at the bottom of the message view as
in GTK (a user script that runs with content JavaScript off), and a masked
link is confirmed before it opens; those are security features, not
deviations.

## Swift-first: what the GTK UI still has to mirror

For the Jira accounts and the conversation view the order of
[docs/macos-port.md §10](../docs/macos-port.md#10-adding-a-feature-keeping-the-parity)
is reversed: the backend and this client came first, the GTK widgets and
the Windows client follow. So that the port has something to diff against,
the pure logic exists as Go packages the GTK UI will use as they are
(with an adapter over `ui/internal/i18n` for the `Translator`), and their
tests are the reference the Swift tests port:

- `ui/internal/jira` — the texts and view models of the assistant
  (`wizard.go`), the sidebar, the list rows, the issue card and the event
  lines (`jira.go`), the comment window (`compose.go`) and the account
  settings with its checks (`settings.go`); the port is
  `MalachiCore/Jira/`;
- `ui/internal/capabilities` — which message actions an account offers
  (`Account.capabilities`); the port is `MalachiCore/Model/Capabilities.swift`;
- `ui/internal/conversation` — the items of a stacked conversation, the
  member marked read, the scroll target, the truncated row; the port is
  `MalachiCore/Model/Conversation.swift`.

The msgids of those packages are in `po/POTFILES`, `po/malachi.pot` and
`po/cs.po` already (appended by hand; `make po` in the Toolbx renumbers
them), and in `windows/parity-exclusions.txt` under "Jira account: macOS
first" and "Conversation view: macOS first" until the Windows client uses
them.

What has no Go counterpart yet is marked `Swift-first` in its comment,
with the Go file it belongs in; the GTK port mirrors it there:

| Swift | Mirror in |
|---|---|
| `Model/FolderTree.swift`: `sortSiblings` (the views' rank after the roles), `folderIcon`, `accountHeaderBadge`, `folderTitle` (the views' names), `accountLabel` (the site's host for an unnamed Jira account) | `ui/internal/window/model.go`, `folders.go` |
| `Model/AccountsPage.swift`: `accountRowTitle`, `accountRowSubtitle`, `accountEditor` (which editor a kind opens) | `ui/internal/window/accounts_page.go` |
| `Model/ActionRules.swift`: `ActionFlags.comment` and `.unsupported` from the capabilities; `MainWindow/ActionPresentation.swift` (Reply relabelled Comment with `text.bubble`, unsupported items disabled in the menus and hidden or disabled in the toolbar) | `ui/internal/window/actions.go` `setMessageActionsSensitive` |
| `Model/MailModel+Jira.swift`: `alwaysGrouped` (a Jira folder lists threads whatever the setting), events never unread; `RowMessage.issue` / `RowThread.issue` (`Jira.rowIssue`) | `ui/internal/window/thread_model.go`, `window.go` |
| `Model/IssueReading.swift`, `MessageView/IssueCardView.swift`: the issue card over the headers, the summary as the subject, an event shown from `changes` without a body | `ui/internal/window/message_view.go` |
| `Model/NotificationText.swift`, `Model/SyncStatus.swift`: the Jira cases of the notification text and the status line | `ui/internal/window/notify.go`, `sync.go` |
| `Controllers/MailboxController.swift` `handleMessagesChanged` (`notify.messagesChanged`: the folders read again, the message cache emptied for the account, the shown folder re-fetched and listed again) | `ui/internal/window/notify.go` |
| `Controllers/JiraWizardController.swift`, `Controllers/JiraAccountController.swift`: the flows over `account.detectSite`, `account.listSpaces`, `account.add` / `update` | `ui/internal/accountwizard` (a Jira flow), `ui/internal/window/preferences.go` |
| `Controllers/ConversationController.swift`, `Model/ConversationLayout.swift`, `MessageView/Conversation*.swift`, the sized mode of `WebViews/MessageWebView.swift` | `ui/internal/window/window.go` (`onMessageRowSelected`), `ui/internal/htmlview` (a height measurement: an isolated-world script with JavaScript on for that world only, or a snapshot; [docs/security.md §3.2](../docs/security.md#32-defences)) |
| `Controllers/IssueActionsController.swift`, `Shared/IssueStatusPill.swift`, `Shared/IssueTransitionMenu.swift`, `App/ChangeStatusMenus.swift`: the status pill of the issue card as the menu of the transitions the site allows (`issue.transitions` / `issue.transition`), the same list under *Change Status* in the Message menu and More Actions; the items and texts are `ui/internal/jira/transitions.go` | `ui/internal/window/message_view.go`, `actions.go` |
| `Compose/CommentHeaderView.swift`, `Compose/ComposeWindowController+Comment.swift`: the comment mode of the compose window (no recipients, subject, attachments or Save Draft; the visibility choice on a service-desk request) | `ui/internal/compose` |

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
