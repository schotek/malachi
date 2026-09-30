# Malachi Mail for Windows

The Windows client of Malachi Mail: a native C#/WinUI 3 application over the
`malachid` JSON-RPC socket, the fourth client of the daemon next to the GTK
UI, the macOS app and the MCP bridge. It mirrors the GTK UI, which is the
primary one and the template (CLAUDE.md rule 4): the same panes, the same
behaviour, the same strings, native controls. The macOS client is the
source it was ported from, because it had already solved the same problem.
The daemon runs on Windows unchanged and does all the mail work; the few
backend fixes the port needed are platform-neutral. How the client is
built, how it stays in step with the GTK UI and the decisions behind it
are in [docs/windows-port.md](../docs/windows-port.md).

**Status: the full mail UI of the GTK application.** Accounts (the setup
assistant with autodetection and the browser sign-in for Gmail and
Microsoft 365, editing, signing in again, pausing, reordering, removing,
trusting a server's own certificate), the folder sidebar with favourites
and folding, the message list (flat and grouped by conversation, with the
All / Unread / Flagged filter, paging itself), search in the folder, the
account or every account, the reader with a locked-down WebView2 view,
message windows and attached messages, attachments with a previewer of
the app's own (those the daemon keeps on the mail server downloaded when
they are opened, saved or forwarded, and the pictures it keeps there on
request), message actions with context menus, notifications with the
system's new-mail sound, compose with the rich-text editor, drafts (kept
in the Drafts folder and opened from it for editing), reply and forward
with the quoted original, `mailto:` links and the *Default apps*
registration, the Preferences window, launch at login, running in the
background with a notification-area icon, Preferences → AI for the MCP
bridge, and the Czech translation read from `po/`. What is missing is
listed under [Not on Windows, not yet](#not-on-windows-not-yet).

Licence: GPL-3.0-or-later (everything outside `backend/`; `malachid.exe` and
`malachi-mcp.exe` in the app folder are AGPL-3.0-only, LICENSING.md). The
built app folder also carries Microsoft's Windows App SDK and WebView2
components, which are under Microsoft's terms: distributing it waits for
the owner's decision on a GPLv3 §7 permission for them (LICENSING.md,
docs/windows-port.md §17). Every source file starts with the SPDX header:
C# files are checked by the compiler (IDE0073 against
`file_header_template` in `windows/.editorconfig`), every other type by
`Malachi.Conventions.Tests`, in its own comment syntax (XML files carry it
as a comment after the XML declaration; JSON and Markdown carry none).

## Requirements

- Windows 11 (the app's minimum is 10.0.22000), x64 or ARM64, with the
  WebView2 runtime, which Windows 11 includes.
- The .NET SDK 10.0.4xx (`windows/global.json`; a newer feature band is
  used when installed). Visual Studio is not needed to build the app.
- For `malachi-credentials.exe`, which is compiled with NativeAOT: the MSVC
  build tools of Visual Studio 2022 or 2026 or of its Build Tools (workload
  *Desktop development with C++*), for an ARM64 app on an x64 machine also
  the ARM64 build tools.
- Go 1.25 or newer for the daemon and the MCP bridge, which go into the app
  folder (`C:\Program Files\Go\bin\go.exe` is found without `PATH`; `GO`
  names another `go.exe`).
- Git for Windows: `git describe` gives the version, and its `sh.exe` runs the
  root Makefile's recipes.
- GNU make, optional: `winget install ezwinports.make` (4.4). Everything
  works without it through `windows\build.ps1`.
- Windows PowerShell 5.1, which every Windows has (`build.ps1` runs on
  PowerShell 7 as well).

Keep the clone's path short (see [Troubleshooting](#troubleshooting)). A
clone made before the repository had its `.gitattributes` holds CRLF
copies of LF files; check it out again once on a clean tree
(`git rm -r --cached -q . && git reset --hard`).

## Build and run

From the repository root, in PowerShell or Git Bash:

```sh
make windows        # builds build\malachid.exe and build\malachi-mcp.exe, then the
                    # solution; publishes the app and the keyring helper self-contained
                    # and assembles "build\windows\<arch>\Malachi Mail\" with MalachiMail.exe,
                    # malachid.exe, malachi-mcp.exe, malachi-credentials.exe, locale\*.po
                    # and the licences
make run-windows    # the same, then runs MalachiMail.exe in the terminal until it quits;
                    # its log and the daemon's appear there, and Ctrl+C quits the app,
                    # which stops the daemon it started (make then says "Error 512",
                    # its way of reporting the interrupt)
make test-windows   # every test project of the solution
```

A double click on `MalachiMail.exe` in the app folder runs it the Explorer
way: no terminal, the logs in their files, the app registered for
`mailto:` and notifications from where it lies.

The three targets exist on Windows only; elsewhere they print a hint and
exit. On Windows the root Makefile runs its recipes with the `sh.exe` of Git
for Windows (found through `git --exec-path`; `GIT_USR_BIN` names another
`usr\bin`), so PowerShell and Git Bash behave alike, and the Go binaries get
their `.exe`.

Without make, `windows\build.ps1` does the same and more:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 <target> [options]
```

| Target | Does |
|---|---|
| `version` | the version (`git describe` with the leading `v` cut, as the root Makefile) and the file version (`Major.Minor.Patch.CommitsSinceTag`) |
| `go` | `malachid.exe` and `malachi-mcp.exe` for `-Arch` into `build\windows\go\<arch>\` (`GOOS=windows`, `CGO_ENABLED=0`, `GOWORK=off`, `-trimpath`); for this machine's architecture also into `build\` |
| `build` | `dotnet build` of the solution (Debug) |
| `icons` | renders `Malachi.ico` from `docs\malachi_icon.png`, cropped as `macos/Makefile` crops it (the build renders it and the notification icon by itself) |
| `app` | `go`, then publishes and assembles `build\windows\<arch>\Malachi Mail\` (Release) and checks that nothing is missing from it |
| `test` | every test project, `.trx` reports in `build\windows\TestResults\` (Debug); the UI smoke tests drive the app folder `app` assembled and are skipped without it (docs/windows-port.md §12) |
| `run` | `app` for this machine, then `MalachiMail.exe` in this terminal until it quits (Ctrl+C quits it and the daemon it started) |
| `lint` | `dotnet format --verify-no-changes` and the conventions tests |
| `package` | `app`, then `build\windows\Malachi-Mail-<version>-<arch>.zip` (unsigned; not a release yet, docs/releasing.md) |
| `clean` | removes `build\windows\` |

Options: `-Configuration Debug|Release`, `-Arch x64|arm64` (default: this
machine's), `-Version` (default: from git), `-BuildDir` (default: `build\`).
`run` always builds for this machine. Every `dotnet` command runs from
`windows\`: `global.json` there selects the SDK and Microsoft.Testing.Platform
for `dotnet test` (run elsewhere, `dotnet test` falls back to VSTest and
fails). Quick iteration without the app folder:

```powershell
cd windows
dotnet build Malachi.slnx -p:Platform=x64
dotnet test --project tests\Malachi.Core.Tests\Malachi.Core.Tests.csproj
```

Visual Studio 2026: open `windows\Malachi.slnx`, pick the `x64` or `ARM64`
platform and run `Malachi.App` (unpackaged). Before the first run,
`make windows` or `build.ps1 app` puts `malachid.exe`, `malachi-mcp.exe` and
`malachi-credentials.exe` into `build\`, from where every build of the app
for this machine copies them beside `MalachiMail.exe`.

All build output goes to `build\windows\` (`UseArtifactsOutput`: `bin`, `obj`
and `publish` of every project under `build\windows\artifacts\`, a folder
per configuration and, for what is built for x64 or ARM64, per
architecture: `debug\`, `release_win-x64\`, `release_win-arm64\`), never
beside the sources, so one build tree takes both architectures. Warnings
are errors. Package versions are central
(`Directory.Packages.props`), each project's `packages.lock.json` is
committed, and a CI build (`CI=true`, or GitHub Actions) restores in
locked mode.

CI (`.github/workflows/windows.yml`, docs/windows-port.md §13) runs the
daemon's Go tests on Windows and `build.ps1 build`, `test`, `lint` and
`package` for x64, and `build` and `package` for ARM64, on the
`windows-2025` image; the zips and the `.trx` reports are the run's
artifacts. With `CI=true`, as there, every restore is locked and a stale
lock file fails the build.

The app folder is self-contained (the .NET runtime and the Windows App SDK
travel with it, nothing is installed): about 215 MB for x64, the daemon and
the MCP bridge included. The Windows App SDK comes as its component packages
(`Microsoft.WindowsAppSDK.WinUI` and `.InteractiveExperiences`), not the
metapackage, whose AI and ML runtime would add some 60 MB; one DLL the
components lack, `Microsoft.WindowsAppRuntime.Insights.Resource.dll`, is
unpacked from the Runtime package's framework MSIX by the build
(docs/windows-port.md §10: without it notifications cannot register).

ARM64: `build.ps1 app -Arch arm64` cross-builds on an x64 machine; the
keyring helper needs the MSVC ARM64 build tools for it. The ARM64 app has
not been run on real hardware yet.

## Layout

```
windows/
  README.md                       this file
  build.ps1                       the build entry point (above); Windows PowerShell 5.1 and 7
  Malachi.slnx                    the solution; x64 and ARM64 platforms
  global.json                     SDK 10.0.400 (roll forward to a later feature band),
                                  Microsoft.Testing.Platform for dotnet test
  nuget.config                    nuget.org only, with package source mapping
  Directory.Build.props           nullable, warnings as errors, analyzers, code style in the
                                  build, lock files, output under build\windows, versions
  Directory.Build.targets         locale\*.po beside the app, the icons, the daemon for F5,
                                  the SDK's own packages pinned
  Directory.Packages.props        every package version
  .editorconfig                   C# style; the SPDX header template (IDE0073)
  parity-exclusions.txt           the msgids of po/malachi.pot the client does not use, with
                                  the reason (the strings check's coverage)
  scripts/make-icons.ps1          docs\malachi_icon.png -> multi-size Malachi.ico and
                                  notification.png
  src/
    Malachi.Core/                 net10.0: everything that needs neither WinUI nor P/Invoke;
                                  builds and is tested on any OS
      Api/                        backend/pkg/api re-declared from docs/api.md: every method
                                  with its params, result and timeout, the notifications,
                                  the wire enums, the error codes, the handshake's proofs
      Transport/                  RpcClient (AF_UNIX, the handshake, framing), DaemonKey
      Daemon/                     DaemonSupervisor, Paths, the rotating logs
      Model/, Compose/, Html/,    the pure logic of the GTK UI and macOS ported 1:1
      Wizard/, Text/              (window model, threads, folding, favourites, search,
                                  addresses, mailto:, quoting, wizard fields and results,
                                  the viewer and editor documents and the bridge, formats)
      Controllers/                the controllers over the RPC client, tested against an
                                  in-process fake daemon
      Presentation/               what macOS keeps untested in AppKit: the view models of
                                  the panes, the reader, compose, the shell and the rules
                                  of the WebView2 layer, each with its tests
      I18n/, Settings/, Platform/ L10n over po/, the settings with the gschema's keys, the
                                  open directory, the never-open list, the bridge runner
    Malachi.Platform.Windows/     Windows services behind Core's interfaces (CsWin32): the
                                  daemon's process host and the console, the key-file
                                  policy, the registry settings, Mark of the Web and the
                                  file-type policy, the launcher, launch at login, the
                                  mailto: registration, notifications' arguments and quiet
                                  hours, the new-mail sound, the tray icon
    Malachi.App/                  WinUI 3 -> MalachiMail.exe; thin: Program.cs (console,
                                  single instance, activation), App.xaml.cs (lifecycle,
                                  Quit), Shell/ (the composition root AppState, the
                                  Integration, alerts, toasts, window tracking and theme,
                                  the log), Commands/ (the command router, the WebView2 keys),
                                  Controls/, Localization/ ({l:T}, mnemonics), Resources/,
                                  Platform/ (the platform services' entry points), Main/ (the
                                  main window's panes, command rows, status line), Reader/,
                                  Windows/ (message, attached-message and preview windows),
                                  Attachments/, Compose/, Wizard/, Preferences/, WebViews/
                                  (the hardened viewer, editor and previewer)
    Malachi.Credentials/          malachi-credentials.exe, the daemon's keyring helper over
                                  Credential Manager (NativeAOT); depends on nothing else
  tests/
    Malachi.Core.Tests/           the Go UI and Swift tests ported, FakeDaemon, MailFixture
    Malachi.Core.TestDaemon/      a stand-in daemon for the supervisor tests
    Malachi.FakeBridge/           a scripted malachi-mcp for the MCP registration tests
    Malachi.Platform.Windows.Tests/  the Windows services, some against the real malachid.exe
    Malachi.Credentials.Tests/    the helper's protocol; Credential Manager on request
    Malachi.Conventions.Tests/    repository checks: SPDX headers, the gschema keys against
                                  the settings, the strings check and the msgid coverage
    Malachi.App.Canary/           the network canary over the WebView2 layer: the real viewer,
                                  editor and previewer against hostile documents and the raw
                                  MIME corpus, with loopback listeners and a NetLog
    Malachi.App.Canary.Host/      its WinUI host, compiling src/Malachi.App/WebViews
    Malachi.App.UiTests/          UI smoke tests: the published app (build.ps1 app) driven
                                  through UI Automation on a data folder and a preferences
                                  key of its own; with MALACHI_DEVMAIL also against the
                                  local devmail server
    Malachi.FakeKeyring/          a keyring helper over a JSON file for the UI tests
```

The dependency direction is `App -> Platform.Windows -> Core`, never back;
nothing imports the Go modules (the API is re-declared from
[docs/api.md](../docs/api.md), as on macOS). The tests are xUnit v3 on
Microsoft.Testing.Platform: about 4,000 of them, two minutes for
`make test-windows` (docs/windows-port.md §12).

## How it runs the daemon

Like the GTK UI: `malachid.exe` is looked for in `MALACHI_DAEMON` (a path;
`none` or empty switches the automatic start off), else beside
`MalachiMail.exe`, else on `PATH`. If nothing answers on the socket, the
daemon is started with `--socket`, `--config` and `--store`, in a process
group of its own and without a console window, and the app waits up to
15 s for the socket. A daemon that already answers (`make run-backend`, a
debugger, one a crashed app left behind) is used as is and never stopped.
On Quit the app stops the daemon it started with `CTRL_BREAK_EVENT`, which
Go takes as an interrupt, waits up to 15 s (the daemon gives its syncers
10 s), then kills it; signing out of Windows stops it the same way. A
daemon that exits is restarted: at once after a single exit, then with a
backoff that doubles from 1 s to 60 s. Its output goes to
`logs\malachid.log` and, under `make run-windows`, to the terminal.

Before the first start the app creates the socket's directory
(`%USERPROFILE%\.cache\malachi\run`) with a DACL for the user and SYSTEM
alone: the daemon's `0600` means nothing on Windows, and the key file
beside the socket inherits the directory's permissions. The daemon also
gets `DBUS_SESSION_BUS_ADDRESS=disabled:`, so its optional Linux services
(Secret Service, GNOME Online Accounts, Evolution Data Server) give up at
once instead of looking for a session bus on every call.

The storage preferences start at the daemon's own defaults, as with the
GTK UI: the stored mail uncompressed and every attachment kept. The macOS
app gives its daemon other defaults (`MALACHI_DEFAULT_COMPRESS_STORE=1`,
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS=30`) because a Mac often has a
small disk; this app sets neither, so it is no deviation from GTK, and the
two variables, set in the app's environment, reach the daemon as they are.
*Preferences → General → Mail* changes them: *Keep Attachments Offline
For*, *Never Store Attachments* and *Compress Stored Mail*, with *Disk
Space Used* below them.

## Keyring

Windows has no Secret Service, so the daemon gets the app's own helper:
the app starts `malachid.exe` with `MALACHI_KEYRING=helper` and
`MALACHI_KEYRING_HELPER=<app folder>\malachi-credentials.exe`. The daemon
runs the helper once per operation, git-credential style
(`malachi-credentials get|set|delete`, one JSON line on stdin, the value on
stdout for `get`, the outcome in the exit status; the protocol is
documented in `backend/internal/auth/helper`). Values never travel in
arguments, files, the environment or logs.

The helper keeps one generic credential per account and key in
**Credential Manager** (*Control Panel → Credential Manager → Windows
Credentials*), target `io.github.schotek.Malachi/<accountId>/<key>`,
comment `Malachi Mail: <accountId> (<key>)`, local to the machine (not
roaming). A value longer than Credential Manager's 2560 bytes (a
Microsoft refresh token can be) is split into chunks `<target>#<n>`, each
value checked against the SHA-256 the helper wrote with it, so a torn or
edited item reads as corrupt (`keyringError`), never as a wrong token. An
item edited with `cmdkey` or the Credential Manager dialog is corrupt for
the same reason: sign in to the account again in the app. Runs of the
helper take turns through a named mutex of the session, because Credential
Manager loses updates when several processes use it at once.

A `MALACHI_KEYRING` already in the environment wins over the bundled
helper; without a helper beside the executable the daemon runs with
`MALACHI_KEYRING=none`, where adding an account with a password fails with
`keyringError`. The real round trips run only on request, since they write
to Credential Manager: `MALACHI_CREDENTIALS_TEST=1` for
`Malachi.Credentials.Tests`, and on the Go side
`MALACHI_TEST_REAL_HELPER="<app folder>\malachi-credentials.exe" go test ./internal/auth/helper -run TestRealHelper`
from `backend\`.

## Where things are

| What | Where |
|---|---|
| App folder (built here) | `build\windows\<arch>\Malachi Mail\`: `MalachiMail.exe`, `malachid.exe`, `malachi-mcp.exe`, `malachi-credentials.exe`, `locale\<lang>.po`, `Assets\`, the licences |
| Configuration | `%LOCALAPPDATA%\Malachi Mail\config.toml` (`--config`) |
| Mail store | `%LOCALAPPDATA%\Malachi Mail\store.db` (`--store`), lock `store.db.daemon.lock`: held by the running daemon, released by Windows with its process; a second daemon for the same store exits |
| RPC socket | `%USERPROFILE%\.cache\malachi\run\rpc.sock`, the daemon's own default (`MALACHI_SOCKET` overrides); outside AppData on purpose, so `malachi-mcp` and `.mcp.json` find it, and nothing under AppData is redirected for a process started by an MSIX app |
| RPC key | `rpc.sock.key` beside the socket: a new key at every daemon start, removed when it stops cleanly, read afresh for every connection and kept nowhere ([docs/api.md §1.4](../docs/api.md#14-handshake)) |
| Logs | `%LOCALAPPDATA%\Malachi Mail\logs\`: `MalachiMail.log` (the app, `MALACHI_LOG_LEVEL`) and `malachid.log` (the daemon), each rotated at 4 MiB; also the terminal under `make run-windows` |
| WebView2 data | `%LOCALAPPDATA%\Malachi Mail\WebView2\` (InPrivate profiles; only browser-level state is written; the crash dumps of dead renderers, which can hold a message or a draft, are deleted at start and exit) |
| Attachments being opened | `%LOCALAPPDATA%\Malachi Mail\open\<random>\` (private, emptied at start and exit, entries older than an hour swept) |
| Preferences | `HKCU\Software\io.github.schotek.Malachi` (`MALACHI_SETTINGS_KEY` names another key of that family), the gschema's keys plus `ctrl-r`; a `reg add` reaches the running app |
| Passwords, sign-ins | Credential Manager, generic credentials `io.github.schotek.Malachi/<accountId>/<key>` |
| Launch at login | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, value `Malachi Mail` = `"<exe>" --background`; the user's switch in Windows Settings (`...\Explorer\StartupApproved\Run`) is respected, never overwritten |
| `mailto:` | `HKCU\Software\Classes\io.github.schotek.Malachi.mailto`, `HKCU\Software\Clients\Mail\Malachi Mail`, `HKCU\Software\RegisteredApplications`, written at start when missing or stale, so the app is offered in *Settings → Apps → Default apps* |
| Notifications | registered as *Malachi Mail* with `Assets\notification.png` beside the executable; Windows keys the registration by the executable's path (`HKCU\Software\Classes\AppUserModelId\<path>`) |
| MCP bridge | `malachi-mcp.exe` in the app folder, `build\malachi-mcp.exe` in a checkout |
| Keyring helper | `malachi-credentials.exe` in the app folder |

`MALACHI_DATA_DIR` replaces `%LOCALAPPDATA%\Malachi Mail` for tests and
agents; a copy started with it leaves the user's `mailto:` registration
and Run value alone (what is asked for in its Preferences is still
written). `MALACHI_SETTINGS_KEY` names the preferences' key under
`HKCU\Software` instead of `io.github.schotek.Malachi`, for a test that
must not read or write the user's (the UI tests set
`io.github.schotek.Malachi.UiTests.<guid>`): only that name itself or it
followed by a dot and ASCII letters, digits, `.`, `_` or `-` is taken, and
any other value is ignored with a line in the log, so that a mistyped one
never writes into another program's key. `MALACHI_DAEMON`,
`MALACHI_SOCKET`, `MALACHI_KEYRING`, `MALACHI_KEYRING_HELPER` and
`MALACHI_LOCALE_DIR` work as on macOS. A
socket path may have at most 107 bytes (AF_UNIX on Windows); the app
checks it at start and says so, naming `MALACHI_SOCKET`.

## Localisation

The GTK catalogue in `po/` is the single source of truth. The build copies
`po/<lang>.po` for every language of `po/LINGUAS` to `locale\` beside the
executable, and the app parses them at start with the rules of the macOS
catalogue generator (fuzzy, obsolete and untranslated entries left out, a
translation whose printf directives differ from its msgid dropped). Keys
are the GTK msgids verbatim, in C# through `L10n.T/N/C` and in XAML through
`{l:T Msgid=…}`, so a string is translated once for all three desktop
clients; GTK's `_` mnemonics become access keys. The language follows the
Windows display languages (*Settings → Time & language → Language &
region*), matched by the base language; `MALACHI_LOCALE_DIR` points at
another directory of `.po` files (the repository's `po\` works).

Strings that exist only on Windows stay English, in a Czech UI too, until
the GTK UI has msgids for them: *Quit* in the primary menu and in the
notification-area icon's menu, with *Open Malachi Mail* there; in
*Preferences → General* the *Keyboard* group (its title, *Ctrl+R*, *F5
always checks for new mail*), the *Default Mail App* group (its title,
*Open e-mail links with Malachi Mail*, *Default apps* and what Windows
opens e-mail links with) and *Launch at Login*'s note that Windows
Settings turned it off, with its *Startup apps* link; *About*'s
description, licence line, *Website*, *Report an Issue* and *Close*; a few
toasts (an address that could not be copied, Windows Settings that could
not be opened) and accessible names (*Filter*, *Search Scope*, *Message
body*). They are marked `// Windows-only string` (or
`<!-- Windows-only string -->`) in the sources, and nothing is added to
`po/POTFILES`. `Malachi.Conventions.Tests` checks that every msgid the
sources use is in `po/malachi.pot`, with its context and plural, and that
every msgid of the template is used or listed with its reason in
`windows/parity-exclusions.txt`. A new language needs only its
`po/<lang>.po`, plus its plural rule in `Malachi.Core/I18n/PluralRules.cs`
when the table does not know the language yet.

## Differences from the GTK UI

The GTK UI is the template; the client deviates only where a Windows
convention wins, and every deviation is a row here (docs/windows-port.md
§3). Everything else is meant to be the same, down to the strings and the
confirmation dialogs.

| On Windows | Instead of (GTK) | Why |
|---|---|---|
| The search box sits in the middle of the title bar (Ctrl+F, Ctrl+E); the Folder / Account / All Accounts scope bar shows over the list while a search runs; Enter opens the first result, Escape closes the search | A search bar over the message list (Ctrl+F, the search button) with the entry and the scope toggles | Where Windows 11 apps keep search (Outlook, Explorer, Settings) |
| The status line (sync state, unsent messages, the connection; a click opens each account's state and action) runs across the whole bottom edge of the window | At the bottom of the sidebar, with the same popover | It stays in sight when a narrow window folds the sidebar away (as on macOS) |
| At 900 effective pixels or less the sidebar folds into an overlay that the title bar's pane button opens; at 600 or less the list and the message are one stack, the title bar's back button returns to the list, and a click on the selected row shows it again | Collapsed split views that navigate between whole-window pages | Windows 11's own pane and back buttons in the title bar; the list stays in sight while the sidebar is open (docs/windows-port.md §11.1) |
| The primary menu `…` has New Message, Add Account…, Preferences, About Malachi Mail and Quit | New Message, Preferences, Keyboard Shortcuts, About Malachi Mail | Windows has no menu bar or application menu to add an account or quit from; GTK's Keyboard Shortcuts opens nothing |
| The sidebar's New Message is an accent (filled) button | A plain header-bar button | Windows 11's style for a pane's primary action (Fluent's accent button); kept after the parity review |
| Windows keys: Ctrl+R Reply, Ctrl+Shift+R Reply All, Ctrl+Shift+F Forward, F5 Check for New Mail, Ctrl+E besides Ctrl+F for search; the setting `ctrl-r` (*Preferences → General → Keyboard*: `reply` by default, or `refresh`) gives Ctrl+R to Check for New Mail instead, as macOS's `command-r`. GTK's other keys stay: Ctrl+Q Quit, F10 the primary menu (while the sidebar shows it), Delete, A, J, U, S (also with the message view focused, never while typing), Escape; Ctrl+W also closes a secondary window | Ctrl+R Check for New Mail, no Reply/Forward keys, Escape | Ctrl+R is Reply in every Windows mail client and F5 is the Windows refresh key |
| Mnemonics are WinUI access keys: Alt shows their key tips on the menus and buttons that carry a GTK mnemonic; a dialog's buttons have none | Underlined mnemonics | WinUI's form of mnemonics; a `ContentDialog`'s buttons take no access keys |
| Alerts are `ContentDialog`s: the primary button on the left, Cancel on the right; the defaults and close responses stay GTK's (*Save Draft* is the default of the close question) | GTK's button order | WinUI's dialog |
| Banners (the backend, sign-in and certificate banners over the list, the outbox and draft banners over a message) are `InfoBar`s: a warning's or an information's icon and tint, the text in the regular weight, the button at the end | `Adw.Banner`: an accent-tinted strip with a bold title | WinUI's notice bar |
| Context menus on messages and folders, with the actions that exist elsewhere; a right click selects the row | None | Windows convention |
| Left and Right fold an account's heading in the sidebar, as they fold a folder | Only a folder's; a heading folds with its arrow | A row's buttons are no tab stops in a Windows list, so the heading's arrow needs the keys |
| The message list pages itself at its end; *Load More* appears only to retry a page that failed | The *Load More* button under the list | As macOS |
| The window's caption names the selected folder: *Inbox – Malachi Mail* | *Malachi Mail* (the folder is the list's header) | The taskbar and Alt+Tab tell windows apart by their captions; macOS shows the folder as the window's title too |
| The window's size and maximised state (`window-width`, `window-height`, `window-maximized`, the size the window restores to) are written when it closes or hides and at Quit; the pane widths are kept as well (`folder-pane-width`, `message-list-width`, written only from a wide layout) | The size and maximised state are bound to the window and written at every change; the pane-width keys are declared in the gschema, never written | A registry write for every step of a resize would be waste; the pane keys exist, and the window opens as it was left |
| The headers of a message start 12 px below the top of the pane or window | 24 px (`margin-top` of the header box) | The command row above already sets them apart; as macOS (docs/windows-port.md §11.3) |
| A message window shows the subject in its title bar and its buttons in a row below it | The buttons in the header bar around the subject | The main window's structure (a command row per pane under the title bar) in every window |
| The star, in the main window's command row and in a message window, is a flat toggle whose checked state is the filled star in the accent colour; its tooltip and name say Star or Unstar | A flat toggle button with the filled star, pressed (a darker background) while checked | A checked WinUI toggle button is a filled accent block; the accent-coloured star says the same without one |
| Headers taller than two thirds of the message pane or window (hundreds of attachments, every address unfolded) scroll on their own, and the body keeps the rest | The header box grows and pushes the body down | Email is hostile input: a message listing hundreds of parts would put its body and its last chips out of reach |
| While another program holds the clipboard open, Copy Address tries again for a moment, then a toast says the address could not be copied | Always copied | The Windows clipboard is shared: a clipboard manager or a remote desktop session can hold it open |
| Names, subjects, attachment names and the server's folder names are shown without bidi formatting characters (U+202A–U+202E, U+2066–U+2069) and with a space for each control character, in the list, the reader, the sidebar, window captions, search results, the status line, questions and notifications; a subject or name of nothing but characters that draw nothing (a bidi mark, U+200B, U+FEFF) shows its fallback, *(No subject)* or the address; in *Name &lt;address&gt;* (the chips' tooltips, the From list), in a conversation's participants and in *Folder – Malachi Mail* each name is isolated (U+2068 … U+2069). What is sent (a reply's To and Subject fields show it), and what Copy Address copies, stays as received | Shown as received | Mail text is hostile: an override in a name turned the address after it around, one in a subject drew `gnp.exe` as `exe.png`, and a control character stopped the notification; right-to-left names still read as written (docs/security.md §4; proposed for GTK and macOS) |
| A link the daemon did not list in `links[]` is confirmed before it opens, as on macOS | Opened | WebView2 hands out normalised URLs, so an exact match with the daemon's raw hrefs can fail (docs/windows-port.md §6.4) |
| A link whose text names a site is confirmed when Go's URL parser cannot tell the href's host, finds none, or finds userinfo (`https:// bank.example@evil.example/`), or when the address the browser gets is on another site; that address never carries userinfo, and the question names it (`https://evil.example/`). Every listed link with the clicked href, or with its canonical form, is judged. The daemon's text of the link is read cleaned (invisible characters out, NFKC, hosts in punycode without a trailing dot), every address in it counts (two slashes after a letter too: `https//bank.example`; a start the daemon's spaces split: `w ww.`), and one whose host cannot be read (`https://moje banka.example/login`, userinfo in the text) is confirmed, as is one word with a dot another script draws (`bankꓸexample`); a text that begins with an address and goes on in words names that address (`www.shop.example for details`); a single-label host is the site of no host under it (`https://cz/`). What CSS hides or markup draws right to left in the link is not in that text and is not seen. A link the launcher refuses is not offered and shows *The link could not be opened: invalid URL* | Opened when Go cannot parse the href or it has no host, when the text reads as an address only to a person (a soft hyphen, a space or a homoglyph in its host, a trailing dot, a backslash before the path, a colon another script draws or none), or when the first listed link with the href has a plain text; the question names the href as written (`https://bank.example@evil.example/`) | The browser reads what Go refuses and goes to the host after the `@`, and a reader reads what `hostOfText` does not: the security audit's and two adversarial reviews' bypasses of the masked-link question (docs/windows-port.md §6.4) |
| Attachment chips show Windows' icon for the file's extension | The symbolic icon of the content type | What Explorer shows for the file; macOS shows the system's icon as well |
| An attachment chip is one split button: Tab reaches it once, Enter previews, F4 or Alt+Down opens its menu | The chip and its arrow are two buttons, each reached by Tab | WinUI's `SplitButton` |
| A click on an attachment previews images, PDF and text in the app's own locked-down WebView2 window (no network, no script, nothing written to disk), one window titled with the file's name with *Open* and *Save As…* in its title bar, closed by Escape and Ctrl+W; it follows no link in a PDF or a text. Other types show a panel with Open and Save As…; programs are never opened | GNOME Sushi (its window with *Open With*), the default application without it | Windows has no Quick Look or Sushi, and shell preview handlers run third-party code over hostile files (docs/windows-port.md §6.6) |
| Files opened or saved from a message get the Mark of the Web through `IAttachmentExecute` (which also runs the antivirus check and the attachment policy): the Restricted zone, or the Internet zone for a program saved with Save As (Restricted would make Attachment Services delete it). A file for opening is opened only after a check that passed and a zone that reads back, unless an administrator turned zone information off (`SaveZoneInformation=1`) or Attachment Services is missing; a saved file stays the user's | No mark | The counterpart of the macOS quarantine attribute: SmartScreen and Office's Protected View treat the files as downloads |
| *Save All* leaves out every attachment that is never opened (programs, scripts, shortcuts, libraries, search connectors and the rest of that list, judged on the name and type the message lists, on those the daemon serves and on the name the file would get) and says so in a second toast after its summary: *2 attachments were not saved; save programs and scripts with Save As…*; when nothing else is left, it asks for no folder. *Save As…* still saves one such file, marked | Every attachment is saved | Explorer parses `.url`, `.lnk`, `.scf`, `.library-ms` and `.searchConnector-ms` files for their icon and location as soon as the folder is shown or the file selected, whatever their mark, and has leaked the user's NTLM hash to another host that way (CVE-2025-24054, exploited in 2025); one click on *Save All* for a mail with an invoice would plant such a file in Downloads (docs/windows-port.md §10) |
| When a message's, an attachment's or the editor's web process dies, hangs (reported and not answering 5 s later) or takes the browser with it, the view shows the same document again once; when that document fails again, the reader shows the plain text, the previewer its panel, and the editor stays blank with its text kept for a save | GTK logs the terminated process and leaves the view blank; the compose window reloads the text on every report | WebView2 also reports hangs and loses the whole control with its browser, so the view must recover by itself; once per document, because a body that reliably kills the renderer (a Chromium or PDFium bug) would otherwise reload in a loop, writing a crash dump of the mail each time and giving an exploit unlimited retries (docs/windows-port.md §6.1) |
| The compose editor's context menu offers Undo, Redo, Cut, Copy, Paste, Paste as plain text and Select All (WebView2's own items, their labels the runtime's) | No context menu | WebView2's menu reduced to the editing commands; Windows users paste from it. Its navigation, printing, saving and inspection items are removed (docs/windows-port.md §6.5) |
| The compose editor scrolls without animation, as the message view does | WebKit's smooth scrolling in the editor; the message view turns it off | WebView2's only switch for it is a browser argument, which holds for every view of the app (docs/windows-port.md §6.1) |
| A compose window narrower than about 500 px shows Send with its icon alone (its name and tooltip stay) and no app icon in the title bar; the subject shortens with an ellipsis | The header bar keeps the window from getting narrower than Attach, the title, the Draft Menu and Send with its label | The window keeps its smallest size of 360 px, and the caption buttons take room GTK's header bar does not |
| In the compose window, *Text Colour* opens a colour picker in a flyout under its button (no transparency, opaque black at first); the colour is applied to the selection when the flyout closes with another colour | A colour dialog; the colour is applied when it is chosen there | WinUI has no colour dialog; a flyout keeps the compose window and the editor's selection in place (docs/windows-port.md §11.3) |
| Quitting saves the unsaved changes of every message being written as drafts; only a draft that cannot be saved asks | The compose windows close; what was typed since the last automatic save is lost | Decided (docs/windows-port.md §0) |
| While the app runs in the background, a notification-area icon offers Open, New Message, Check for New Mail and Quit | No icon | A background app is invisible on Windows otherwise |
| The new-mail sound is the user's system sound for mail (`MailBeep`, *Desktop Mail Notification* in Control Panel → Sound) at the system sounds' volume, silent when none is set, skipped under Do Not Disturb, in a presentation, a full-screen program, the screen saver or a locked session; notifications themselves are silent | The sound theme's `message-new-email` | Windows' own event for it |
| The account wizard is a window of its own, modal over the window it was opened from (520×640): its title bar carries Back (also Alt+Left and the mouse's back button) and the page's title; its close button, Escape and Ctrl+W cancel it (and a sign-in waiting in the browser). The pages slide in; the result rows' icons are green for success and red for an error | An `Adw.Dialog` over its parent with a header bar on each page | A window shows one `ContentDialog` at a time, and the wizard asks *Trust This Certificate?* in one of its own (docs/windows-port.md §11.3); the colours are macOS's |
| The wizard never shows the accounts of GNOME Online Accounts, and its GNOME Online Accounts page has neither *Open Online Accounts* nor *Check Again*; the sign-in banner of an account that GNOME Online Accounts holds keeps its button *Open Online Accounts*, which opens *Preferences* | Both, for accounts GNOME Online Accounts holds; the banner's button opens *Online Accounts* in GNOME Settings | GNOME Online Accounts does not exist on Windows (as macOS, whose table has the same row); the daemon offers its own browser sign-in instead, so the page is normally not reached; an account of GNOME Online Accounts (a configuration brought from Linux) is practically never seen here, and the Preferences are where it is edited or removed |
| *Preferences* is one window for the app with a navigation pane (Accounts, General, Appearance, AI), only its icons below 720 px; the rows are Windows settings cards; *General* has, after GTK's groups (Startup, Reading, Deleting, Notifications, Mail), a *Keyboard* group (the `ctrl-r` choice) and a *Default Mail App* group; *Accounts* follows the accounts and their state while it is open | `Adw.PreferencesDialog` with a view switcher, built anew on every open | Windows Settings' form; the two groups are Windows' own; the window stays open beside the main window, where accounts change |
| *Preferences* has no search field | `Adw.PreferencesDialog` with search | Decided (as macOS) |
| *Preferences → General → Default Mail App* has a *Default apps* button that opens *Settings → Apps → Default apps* for Malachi Mail | None | Windows does not let an app make itself the default mail app; the app registers itself in HKCU at start, the user chooses it there |
| One launch or activation opens at most one compose window, for its first `mailto:` link; further links are logged by count and dropped | A compose window for every `mailto:` URI | The registration passes one link (`"%1"`); a caller that splits a quoted link into several arguments, or a command line of hundreds, would otherwise open a window and a WebView2 editor for each (docs/windows-port.md §10) |
| In *Preferences → Accounts*, clicking a row selects it; *Enabled* is the switch alone | The row activates its switch | Ctrl+Up / Ctrl+Down reorder the selected row, so a click must select (as macOS) |
| *Launch at Login* is the Run value; when the user turned Malachi Mail off in *Settings → Apps → Startup*, the row says so and links there, and turning it on reports *Autostart was not granted* | The Background portal asks | Windows keeps that choice in Settings, where only the user changes it; the app never overwrites it |
| *About Malachi Mail* is a dialog with the name, icon, developer, version, licence, website and issue tracker; its description says *A native mail client.* | `Adw.AboutDialog` with *A native mail client for the GNOME desktop.* | GTK's text names GNOME; the fields are GTK's |
| *Preferences → Accounts* shows a Google or Microsoft 365 account with the generic envelope glyph, as any other | The provider's GNOME Online Accounts icon (`goa-account-google`, `goa-account-ms365`) when the icon theme has it | Windows has no such icons, and the app ships no brand icons (docs/windows-port.md §3.1, U6) |
| A new-mail notification's title, the sender's name, is cut to 200 bytes with an ellipsis, as its body is | Only the body is capped | A display name is hostile input as much as a subject; as macOS (docs/windows-port.md §3.1, U7) |
| The daemon's key file (`rpc.sock.key`) is opened as itself (a link or junction is refused, never followed) and used only when it is a file on disk (not a pipe or a device), the current user (or the token's default owner, as in an elevated run) owns it, and its DACL lets nobody but the user, SYSTEM, Administrators and OWNER RIGHTS read, write or append its data, change its DACL or take it (a NULL DACL is refused), besides being 65 bytes in the key format; otherwise the connection is refused (*Backend unavailable*, the reason in the log). The file inherits its directory's ACL, so a `MALACHI_SOCKET` directory must be private | The Go clients check the file's type, size and format | Defence in depth, the counterpart of macOS's owner and mode check (docs/windows-port.md §5) |

The link under the pointer is shown at the bottom of the message view as
in GTK, and a masked link is confirmed before it opens; those are security
features, not deviations.

## Not on Windows, not yet

- **Gmail needs an app password or an OAuth client of your own.** No
  Google client is shipped (the [root README](../README.md#oauth-clients-for-gmail-and-microsoft-365)
  says why and how to register one): the assistant offers an app password
  (IMAP/SMTP), or signs in through the browser once a client ID is in
  `%LOCALAPPDATA%\Malachi Mail\config.toml` (`[oauth2.google]`).
  **Microsoft 365 / Outlook.com** sign in through the browser with the
  client Malachi Mail ships; organisations that restrict consent approve
  the app once.
- **Recipient completion** comes from the addresses you have written to
  only. The GTK UI also searches the system address books through
  Evolution Data Server; there is no equivalent here and the daemon
  degrades silently.
- **Jira accounts and the conversation view** are not ported yet. The
  daemon has them (`kind: jira`, [docs/api.md §4.1](../docs/api.md#41-account),
  [docs/architecture.md §3.6](../docs/architecture.md#36-issue-tracker-accounts-kind-jira)),
  and the macOS client came first, the GTK UI followed ([macos/README.md](../macos/README.md#swift-first-where-the-gtk-ui-mirrors-it)):
  the port takes the Go reference of the pure logic (`ui/internal/jira`,
  `ui/internal/capabilities`, `ui/internal/conversation`) and the Swift
  (`MalachiCore/Jira`, `Model/Capabilities.swift`,
  `Model/Conversation.swift`, `JiraWizardController`,
  `JiraAccountController`, `ConversationController`, the sized mode of
  `MessageWebView`) into `Malachi.Core` and `Malachi.App`. Until then the
  client knows nothing of such an account: what the daemon lists of it
  reads as mail (the subject of every message is `KEY: Summary`), the
  actions it offers on it the daemon refuses (`invalidArgument`, an
  account without the capability), and it is not the place to add or
  edit one. A folded conversation row shows its newest member, as in
  GTK. The msgids are listed in `parity-exclusions.txt`
  under "Jira account: macOS first" and "Conversation view: macOS first";
  the port removes them from there as it uses them.
- **Distribution**: the app folder is unsigned and has no installer or
  updater; ARM64 has not run on real hardware; the Microsoft components'
  licence permission is the owner's decision; CI builds the zips for both
  architectures as run artefacts, not release assets ([docs/windows-port.md §17](../docs/windows-port.md#17-before-a-public-release),
  [docs/releasing.md](../docs/releasing.md)).

## Troubleshooting

- **The XAML compiler fails (`MSB3073`, `XamlCompiler.exe` exited with code
  1, `MSB3106` for assemblies under the NuGet cache).** A path is longer
  than 260 characters: the XAML compiler is a .NET Framework tool without
  long-path support, and enabling long paths in Windows does not help it.
  Keep the clone's path short (`D:\src\malachi`) and the NuGet cache at its
  default (`%USERPROFILE%\.nuget\packages`).
- **Run from Claude Desktop, or from an agent it started, the app's data
  and settings are somewhere else.** Claude Desktop is an MSIX package, and
  every process it starts sees a virtualised AppData and HKCU: files
  created under `%APPDATA%` and `%LOCALAPPDATA%` and values written to HKCU
  (the preferences, launch at login, the `mailto:` and notification
  registrations) land in Claude's package store, invisible to the same
  program started from Explorer. Set `MALACHI_DATA_DIR` to a directory
  outside AppData for such runs (the socket is outside AppData already,
  and a short `MALACHI_SOCKET` under `%TEMP%` keeps a test apart from the
  real daemon, as `MALACHI_SETTINGS_KEY` keeps its preferences apart);
  test what must reach the real registry from a process started outside
  Claude's tree.
- **The message shows as plain text with "could not be shown safely", the
  previewer shows only a panel, the editor stays blank.** WebView2 could
  not start: the WebView2 runtime is missing or broken (Windows 11 ships
  it; *Settings → Apps* lists it as *Microsoft Edge WebView2 Runtime*, and
  Microsoft's Evergreen installer repairs it), or the runtime is too old
  for a setting the views require. The views fail closed; the app log
  says why.
- **No desktop notifications, or a click on one does nothing.** Nothing is
  shown while the main window is the active window, as in GTK. Otherwise
  check *Settings → System → Notifications* (Malachi Mail on, Do Not
  Disturb off: under it the toast goes silently to the notification
  centre), start the app from outside Claude Desktop's process tree
  (above), and look for `Register` in `MalachiMail.log`: an app folder not
  assembled by the build lacks
  `Microsoft.WindowsAppRuntime.Insights.Resource.dll`, and then toasts may
  show but their clicks are lost.
- **The status line says "Protocol mismatch: UI 2, backend 1".** An older
  `malachid.exe` still answers on the socket, one from before the
  authenticated connections: typically `make run-backend` in another
  terminal from an older checkout, or a daemon left running by an older
  build. The app uses a running daemon and never stops somebody else's, so
  stop that one (Ctrl+C in its terminal, or end `malachid.exe` in Task
  Manager); the app then starts its own at its next attempt, a few seconds
  later. A backend number higher than the UI's means the app is the older
  one: rebuild it.
- **"Backend unavailable" although malachid.exe runs.** The connection was
  refused in the handshake; `MalachiMail.log` (or the terminal under
  `make run-windows`) says why once. Usually the key file's policy: with
  `MALACHI_SOCKET` in a directory outside your profile (`D:\…`, where
  Authenticated Users may modify by default) the key inherits that ACL and
  the app refuses it as *accessible to other users*; use a directory of
  your own (under `%USERPROFILE%` or `%TEMP%`), or make it private. *belongs
  to another user* means the key was written by another account, and a
  failed proof means the process on the socket is not your daemon.
- **A different socket.** `MALACHI_SOCKET=C:\path\rpc.sock` for the app and
  the daemon it starts; `malachi-mcp` reads the same variable. Keep it
  under 107 bytes.
- **"Running scripts is disabled on this system".** `build.ps1` is not
  signed; run it as `powershell -ExecutionPolicy Bypass -File windows\build.ps1`
  (make does), or allow local scripts for your user.
- **`malachi-credentials` does not publish (`Platform linker not found`,
  `link.exe`, `vswhere.exe` is not recognized).** NativeAOT links with the
  MSVC tools; install them as under [Requirements](#requirements) (for
  `-Arch arm64` on an x64 machine the ARM64 build tools too, the
  component `Microsoft.VisualStudio.Component.VC.Tools.ARM64`). The app
  itself is published by then; `build.ps1` says which tools it lacks and
  that the app folder is incomplete without the helper. It puts the Visual
  Studio Installer directory on `PATH` for `vswhere.exe`, which Visual
  Studio 2026's `vcvarsall.bat` needs.
- **`dotnet test` says "Testing with VSTest target is no longer supported"
  or does not know `--solution`.** It ran outside `windows\`, without its
  `global.json`; use `build.ps1 test` or run it from `windows\`.
- **make from PowerShell runs `cmd.exe` recipes ("'sed' is not
  recognized").** Git for Windows is missing or elsewhere; the Makefile
  finds its `usr\bin` through `git --exec-path`, or set `GIT_USR_BIN`.
- **`error CS8012` (a referenced assembly targets a different processor).**
  A publish of the app compiles `Malachi.Core` and
  `Malachi.Platform.Windows` for its architecture, so every project built
  for x64 or ARM64 has output folders of its own for that architecture
  (`build\windows\artifacts\bin\<project>\release_win-x64\` and
  `release_win-arm64\`, `Directory.Build.props`), and `build.ps1 app -Arch
  x64` and `-Arch arm64` follow each other in one build tree. When the two
  shared a folder, the second took the first one's libraries as up to
  date and failed with CS8012. Should it come back (a build given an
  `ArtifactsPivots` of its own), `build.ps1 clean` clears the stale
  libraries.
- **`build\malachid.exe is in use`.** A daemon started from `build\` is
  running (`make run-backend`); that copy is left as it is, and the app
  folder gets its own.
- **The network canary is skipped or fails.** `Malachi.App.Canary` starts
  its WinUI host beyond the edge of the screen, so it needs an interactive
  desktop session and the WebView2 runtime; without either, and on a CI
  runner (`GITHUB_ACTIONS=true`) unless `MALACHI_CANARY=1`, its tests are
  skipped with that reason. `MALACHI_CANARY_KEEP=1` keeps each run's
  configuration, results and NetLog under `%TEMP%\malachi-canary-<id>\`
  for a look after a failure (docs/windows-port.md §12).
- **A lock file changed after a restore.** The lock files hold every
  package version, including the ones the SDK adds by itself, which are
  pinned in `Directory.Packages.props` (`MalachiSdkPackVersion`) so that a
  newer SDK does not change them. Commit a change only together with the
  package change that caused it.
- **English although Windows is Czech.** The app follows the Windows
  display language, not the regional format; and it needs `locale\cs.po`
  beside `MalachiMail.exe`, which every build of the app puts there
  (`MALACHI_LOCALE_DIR` names another directory of `.po` files).

## AI agents

While the app runs, the repository's `.mcp.json` works against its daemon:
Claude Code spawns `build/malachi-mcp`, which is `build\malachi-mcp.exe` on
Windows (`make mcp`, `make windows` or `build.ps1 go`), and it connects to
the same socket. Whether Claude Code on Windows finds the `.exe` under the
name without the extension has not been verified yet; a local-scope entry
naming `build\malachi-mcp.exe` works either way. Another MCP client points
at `malachi-mcp.exe` in the app folder. Tools, flags and the security
model are in [docs/mcp.md](../docs/mcp.md).

*Preferences → AI → Register with Claude* puts the app folder's
`malachi-mcp.exe` into the MCP configuration of Claude Desktop and Claude
Code on this computer, or takes it out again: the switch runs
`malachi-mcp status`, `install` and `uninstall` and shows what the bridge
reports, so the app never edits those files itself. The app passes the
bridge's canonical path (`--command`), and for the packaged (MSIX) Claude
Desktop, which keeps its configuration inside its package, that file's
path (`--claude-desktop-config`; [docs/mcp.md](../docs/mcp.md)).
Restart Claude Desktop afterwards. The AI page exists in all three desktop
UIs with the same strings, so it is not a deviation; without a bridge
beside the executable, or when its status fails, the page says so in the
group's description, as in GTK.
