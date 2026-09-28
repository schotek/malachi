# Malachi Mail for Windows

The Windows client of Malachi Mail: a native C#/WinUI 3 application over the
`malachid` JSON-RPC socket, the fourth client of the daemon next to the GTK
UI, the macOS app and the MCP bridge. It mirrors the GTK UI, which is the
primary one and the template (CLAUDE.md rule 4); the macOS client is the
source it is ported from, because it already solved the same problem. The
daemon runs on Windows unchanged and does all the mail work. How the client
is designed, built and kept in step with the GTK UI, and the decisions
behind it, are in [docs/windows-port.md](../docs/windows-port.md).

**Status: in progress, phase E of docs/windows-port.md §15.** The
solution, its projects and packages, the build and test entry points
(`make windows`, `run-windows`, `test-windows`, `windows/build.ps1`), the
typed API layer, the transport and the daemon's supervisor, localisation
from `po/`, the settings (registry), the attachment-safety services, the
keyring helper `malachi-credentials.exe` and every ported controller of
the Go UI and macOS exist (phases A to D). Of the app, the shell stands
(phase E wave 1): it runs as a single instance, starts or adopts its
daemon, connects, and shows the connection, the status line and what the
mailbox loaded; it quits cleanly (drafts first, then the daemon it
started), from its window, Ctrl+Q or Ctrl+C in the terminal; the keyboard,
the dialogs, the toasts, the colour scheme and the strings check are in
place. The main window's panes stand too (phase E wave 2): the folder
sidebar with its Favourites, the message list (flat and by conversation,
search with its scope, paging), the command rows of the three panes with
their context menus, the status line with its flyout, and the narrow
layouts. The other screens (reader, compose, wizard, preferences), the
WebView2 layer and the platform services (notifications, the
notification-area icon, launch at login, `mailto:` registration) follow in
the rest of phase E. The rows below that describe them are the design those
steps implement.

Licence: GPL-3.0-or-later (everything outside `backend/`; `malachid.exe` and
`malachi-mcp.exe` in the app folder are AGPL-3.0-only, LICENSING.md). Every
source file starts with the SPDX header: C# files are checked by the
compiler (IDE0073 against `file_header_template` in `windows/.editorconfig`),
every other type by `Malachi.Conventions.Tests`, in its own comment syntax
(XML files carry it as a comment after the XML declaration; JSON and
Markdown carry none).

## Requirements

- Windows 11 (the app's minimum is 10.0.22000), x64 or ARM64.
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
- GNU make, optional: `winget install ezwinports.make`. Everything works
  without it through `windows\build.ps1`.
- Windows PowerShell 5.1, which every Windows has (`build.ps1` is written
  for PowerShell 7 as well).

Keep the clone's path short (see [Troubleshooting](#troubleshooting)).

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
| `icons` | renders `Malachi.ico` from `docs\malachi_icon.png`, cropped as `macos/Makefile` crops it (the build does this by itself) |
| `app` | `go`, then publishes and assembles `build\windows\<arch>\Malachi Mail\` (Release) |
| `test` | every test project, `.trx` reports in `build\windows\TestResults\` (Debug) |
| `run` | `app` for this machine, then `MalachiMail.exe` in this terminal until it quits (Ctrl+C quits it and the daemon it started) |
| `lint` | `dotnet format --verify-no-changes` and the conventions tests |
| `package` | `app`, then `build\windows\Malachi-Mail-<version>-<arch>.zip` |
| `clean` | removes `build\windows\` |

Options: `-Configuration Debug|Release`, `-Arch x64|arm64` (default: this
machine's), `-Version` (default: from git), `-BuildDir` (default: `build\`).
`run` always builds for this machine. Every `dotnet` command runs from
`windows\`: `global.json` there selects the SDK and Microsoft.Testing.Platform
for `dotnet test` (run elsewhere, `dotnet test` falls back to VSTest and
fails).

Visual Studio 2026: open `windows\Malachi.slnx`, pick the `x64` or `ARM64`
platform and run `Malachi.App` (unpackaged). Before the first run,
`make windows` or `build.ps1 app` puts `malachid.exe`, `malachi-mcp.exe` and
`malachi-credentials.exe` into `build\`, from where every build of the app
for this machine copies them beside `MalachiMail.exe`.

All build output goes to `build\windows\` (`UseArtifactsOutput`: `bin`, `obj`
and `publish` of every project under `build\windows\artifacts\`), never
beside the sources. Package versions are central (`Directory.Packages.props`),
each project's `packages.lock.json` is committed, and a CI build restores in
locked mode.

The app folder is self-contained (the .NET runtime and the Windows App SDK
travel with it, nothing is installed): about 215 MB for x64, the daemon and
the MCP bridge included. The Windows App SDK comes as its component packages
(`Microsoft.WindowsAppSDK.WinUI` and `.InteractiveExperiences`), not the
metapackage, whose AI and ML runtime would add some 60 MB; one DLL the
components lack, `Microsoft.WindowsAppRuntime.Insights.Resource.dll`, is
unpacked from the Runtime package's framework MSIX by the build
(docs/windows-port.md §10: without it notifications cannot register).

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
  Directory.Build.targets         locale\*.po beside the app, the icon, the daemon for F5,
                                  the SDK's own packages pinned
  Directory.Packages.props        every package version
  .editorconfig                   C# style; the SPDX header template (IDE0073)
  parity-exclusions.txt           the msgids of po/malachi.pot the client does not use, with
                                  the reason (the strings check's coverage)
  scripts/make-icons.ps1          docs\malachi_icon.png -> multi-size Malachi.ico
  src/
    Malachi.Core/                 net10.0: everything that needs neither WinUI nor P/Invoke
                                  (API, transport, supervisor, ported logic, controllers,
                                  settings, i18n); builds and is tested on any OS
    Malachi.Platform.Windows/     Windows services behind Core's interfaces (CsWin32)
    Malachi.App/                  WinUI 3 -> MalachiMail.exe; thin: windows, pages, XAML,
                                  the WebView2 layer. Program.cs (console, single instance,
                                  activation), App.xaml.cs (lifecycle, Quit), Shell/ (the
                                  composition root AppState, the Integration, alerts, toasts,
                                  window tracking and theme, the log), Commands/ (the command
                                  router, the WebView2 keys through the island's pre-translate
                                  source or a keyboard hook, the window commands), Controls/,
                                  Localization/ ({l:T}, mnemonics), Resources/ (icons, text styles),
                                  Platform/ (the platform services' entry points), Main/ (the
                                  main window's panes: sidebar, list and rows, the command rows,
                                  the status line)

                                  Platform/ (the platform services' entry points), Wizard/ (the
                                  account wizard), Preferences/ (the Preferences window)
    Malachi.Credentials/          malachi-credentials.exe, the daemon's keyring helper over
                                  Credential Manager (NativeAOT); depends on nothing else
  tests/
    Malachi.Core.Tests/           the Go UI and Swift tests ported, FakeDaemon, MailFixture
    Malachi.Core.TestDaemon/      a stand-in daemon for the supervisor tests
    Malachi.FakeBridge/           a scripted malachi-mcp for the MCP registration tests
    Malachi.Platform.Windows.Tests/
    Malachi.Credentials.Tests/
    Malachi.Conventions.Tests/    repository checks: SPDX headers, the gschema keys against
                                  the settings, the strings check and the msgid coverage
    Malachi.App.Canary/           the network canary over the WebView2 layer: the real viewer,
                                  editor and previewer against hostile documents and the raw
                                  MIME corpus, with loopback listeners and a NetLog
    Malachi.App.Canary.Host/      its WinUI host, compiling src/Malachi.App/WebViews
```

The dependency direction is `App -> Platform.Windows -> Core`, never back;
nothing imports the Go modules (the API is re-declared from
[docs/api.md](../docs/api.md), as on macOS). The tests are xUnit v3 on
Microsoft.Testing.Platform.

## Where things are

The design of docs/windows-port.md §1. The app uses all of them but the
WebView2 data, launch at login and the `mailto:` registration, which come
with the rest of phase E.

| What | Where |
|---|---|
| App folder (built here) | `build\windows\<arch>\Malachi Mail\`: `MalachiMail.exe`, `malachid.exe`, `malachi-mcp.exe`, `malachi-credentials.exe`, `locale\<lang>.po`, the licences |
| Configuration | `%LOCALAPPDATA%\Malachi Mail\config.toml` (`--config`) |
| Mail store | `%LOCALAPPDATA%\Malachi Mail\store.db` (`--store`), lock `store.db.daemon.lock` |
| RPC socket | `%USERPROFILE%\.cache\malachi\run\rpc.sock`, the daemon's own default (`MALACHI_SOCKET` overrides); outside AppData on purpose |
| RPC key | `rpc.sock.key` beside the socket, a new key at every daemon start, read afresh for every connection |
| Logs | `%LOCALAPPDATA%\Malachi Mail\logs\`: `MalachiMail.log` (the app, `MALACHI_LOG_LEVEL`) and `malachid.log` (the daemon), each rotated at 4 MiB; also the terminal under `make run-windows` |
| WebView2 data | `%LOCALAPPDATA%\Malachi Mail\WebView2\` |
| Attachments being opened | `%LOCALAPPDATA%\Malachi Mail\open\<random>\` (private, emptied at start and exit) |
| Preferences | `HKCU\Software\io.github.schotek.Malachi`, the gschema's keys plus `ctrl-r` |
| Passwords, sign-ins | Credential Manager, generic credentials `io.github.schotek.Malachi/<accountId>/<key>` |
| Launch at login | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, value `Malachi Mail` = `"<exe>" --background`; the user's switch in Windows Settings (`...\Explorer\StartupApproved\Run`) is respected, never overwritten |
| `mailto:` | `HKCU\Software\Classes\io.github.schotek.Malachi.mailto`, `HKCU\Software\Clients\Mail\Malachi Mail`, `HKCU\Software\RegisteredApplications`, written at start when missing or stale |
| Notifications | registered as *Malachi Mail* with `Assets\notification.png` beside the executable; Windows keys the registration by the executable's path (`HKCU\Software\Classes\AppUserModelId\<path>`) |

`MALACHI_DATA_DIR` replaces `%LOCALAPPDATA%\Malachi Mail` for tests and
agents; `MALACHI_DAEMON`, `MALACHI_SOCKET`, `MALACHI_KEYRING`,
`MALACHI_KEYRING_HELPER` and `MALACHI_LOCALE_DIR` work as on macOS.

## Differences from the GTK UI

The GTK UI is the template; the client deviates only where a Windows
convention wins, and every deviation is a row here (docs/windows-port.md §3).
These rows are decided (docs/windows-port.md §0 and §11) and land with the
phases that implement them.

| On Windows | Instead of (GTK) | Why |
|---|---|---|
| The search box sits in the middle of the title bar (Ctrl+F, Ctrl+E); Enter opens the first result, Escape closes it | A search bar over the message list (Ctrl+F, the search button) | Where Windows 11 apps keep search (Outlook, Explorer, Settings) |
| The status line (sync state, unsent messages, the connection; a click opens each account's state and action) runs across the whole bottom edge of the window | At the bottom of the sidebar, with the same popover | It stays in sight when a narrow window folds the sidebar away (as on macOS) |
| Windows keys: Ctrl+R Reply, Ctrl+Shift+R Reply All, Ctrl+Shift+F Forward, F5 Check for New Mail, Ctrl+E besides Ctrl+F for search; the setting `ctrl-r` (`reply` by default, or `refresh`) gives Ctrl+R to Check for New Mail instead, as macOS's `command-r`. GTK's other keys stay: Ctrl+Q Quit, Delete, A, J, U, S (also with the message view focused, never while typing), Escape; Ctrl+W also closes a secondary window | Ctrl+R Check for New Mail, no Reply/Forward keys, Escape | Ctrl+R is Reply in every Windows mail client and F5 is the Windows refresh key |
| Alerts are `ContentDialog`s: the primary button on the left, Cancel on the right; the defaults and close responses stay GTK's (*Save Draft* is the default of the close question) | GTK's button order | WinUI's dialog |
| Files opened or saved from a message get the Mark of the Web through `IAttachmentExecute` (which also runs the antivirus check and the attachment policy): the Restricted zone, or the Internet zone for a program saved with Save As (Restricted would make Attachment Services delete it). A file for opening is opened only after a check that passed and a zone that reads back, unless an administrator turned zone information off (`SaveZoneInformation=1`) or Attachment Services is missing; a saved file stays the user's | No mark | The counterpart of the macOS quarantine attribute: SmartScreen and Office's Protected View treat the files as downloads |
| A click on an attachment previews images, PDF and text in the app's own locked-down WebView2 window (no network, no script, nothing written to disk); other types offer Open and Save As…; programs are never opened | GNOME Sushi, the default application without it | Windows has no Quick Look or Sushi, and shell preview handlers run third-party code over hostile files |
| While the app runs in the background, a notification-area icon offers Open, New Message, Check for New Mail and Quit | No icon | A background app is invisible on Windows otherwise |
| Context menus on messages and folders, with the actions that exist elsewhere | None | Windows convention |
| At 900 effective pixels or less the sidebar folds into an overlay that the title bar's pane button opens; at 600 or less the list and the message are one stack, the title bar's back button returns to the list, and a click on the selected row shows it again | Collapsed split views that navigate between whole-window pages | Windows 11's own pane and back buttons in the title bar; the list stays in sight while the sidebar is open (docs/windows-port.md §11.1) |
| The primary menu `…` has New Message, Add Account…, Preferences, About Malachi Mail and Quit | New Message, Preferences, Keyboard Shortcuts, About Malachi Mail | Windows has no menu bar or application menu to add an account or quit from; GTK's Keyboard Shortcuts opens nothing (research 05 W4) |
| Left and Right fold an account's heading in the sidebar, as they fold a folder | Only a folder's; a heading folds with its arrow | A row's buttons are no tab stops in a Windows list, so the heading's arrow needs the keys |
| *Preferences* has a *Default apps* button that opens Settings → Apps → Default apps | None | Windows does not let an app make itself the default mail app; the app registers itself in HKCU at start |
| Quitting saves the unsaved changes of every message being written as drafts | The compose windows close; what was typed since the last automatic save is lost | Decided |
| A link the daemon did not list in `links[]` is confirmed before it opens, as on macOS | Opened | WebView2 hands out normalised URLs, so an exact match with the daemon's raw hrefs can fail (docs/windows-port.md §6.4) |
| The compose editor's context menu offers Undo, Redo, Cut, Copy, Paste, Paste as plain text and Select All (WebView2's own items, their labels the runtime's) | No context menu | WebView2's menu reduced to the editing commands; Windows users paste from it. Its navigation, printing, saving and inspection items are removed (docs/windows-port.md §6.5) |
| A compose window narrower than about 500 px shows Send with its icon alone (its name and tooltip stay) and no app icon in the title bar; the subject shortens with an ellipsis | The header bar keeps the window from getting narrower than Attach, the title, the Draft Menu and Send with its label | The window keeps its smallest size of 360 px, and the caption buttons take room GTK's header bar does not |
| In the compose window, *Text Colour* opens a colour picker in a flyout under its button (no transparency, opaque black at first); the colour is applied to the selection when the flyout closes with another colour | A colour dialog; the colour is applied when it is chosen there | WinUI has no colour dialog; a flyout keeps the compose window and the editor's selection in place (docs/windows-port.md §11.3) |
| The attachment previewer does not follow links in a PDF or a text | Sushi | Every navigation of the previewer is cancelled, as in the editor (docs/windows-port.md §6.6) |
| When a message's, an attachment's or the editor's web process dies, hangs (reported and not answering 5 s later) or takes the browser with it, the view shows the same document again once; when that document fails again, the reader shows the plain text, the previewer its panel, and the editor stays blank with its text kept for a save | GTK logs the terminated process and leaves the view blank; the compose window reloads the text on every report | WebView2 also reports hangs and loses the whole control with its browser, so the view must recover by itself; once per document, because a body that reliably kills the renderer (a Chromium or PDFium bug) would otherwise reload in a loop, writing a crash dump of the mail each time and giving an exploit unlimited retries (docs/windows-port.md §6.1) |
| The new-mail sound is the user's system sound for mail (`MailBeep`, *Desktop Mail Notification* in Control Panel → Sound) at the system sounds' volume, silent when none is set, skipped under Do Not Disturb, in a presentation, a full-screen program, the screen saver or a locked session; notifications themselves are silent | The sound theme's `message-new-email` | Windows' own event for it |
| *Preferences* has no search field | `Adw.PreferencesDialog` with search | Decided (as macOS) |
| The daemon's key file (`rpc.sock.key`) is opened as itself (a link or junction is refused, never followed) and used only when it is a file on disk (not a pipe or a device), the current user (or the token's default owner, as in an elevated run) owns it, and its DACL lets nobody but the user, SYSTEM, Administrators and OWNER RIGHTS read, write or append its data, change its DACL or take it (a NULL DACL is refused), besides being 65 bytes in the key format. The file inherits its directory's ACL, so a `MALACHI_SOCKET` directory must be private | The Go clients check the file's type, size and format | Defence in depth, the counterpart of macOS's owner and mode check (docs/windows-port.md §5) |
| The message list pages itself at its end; *Load More* appears only to retry a page that failed | The *Load More* button under the list | As macOS |
| In *Preferences → Accounts*, clicking a row selects it; *Enabled* is the switch alone | The row activates its switch | Ctrl+Up / Ctrl+Down reorder the selected row, so a click must select (as macOS) |
| The account wizard is a window of its own, modal over the window it was opened from (520×640): its title bar carries Back and the page's title; its close button, Escape and Ctrl+W cancel it (and a sign-in waiting in the browser). The pages slide in; the result rows' icons are green for success and red for an error | An `Adw.Dialog` over its parent with a header bar on each page | A window shows one `ContentDialog` at a time, and the wizard asks *Trust This Certificate?* in one of its own (docs/windows-port.md §11.3); the colours are macOS's |
| The wizard never shows the accounts of GNOME Online Accounts, and its GNOME Online Accounts page has neither *Open Online Accounts* nor *Check Again* | Both, for accounts GNOME Online Accounts holds | GNOME Online Accounts does not exist on Windows (as macOS); the daemon offers its own browser sign-in instead, so the page is normally not reached |
| *Preferences* is one window for the app with a navigation pane (Accounts, General, Appearance, AI), only its icons below 720 px; the rows are Windows settings cards; *General* has a *Keyboard* group (the `ctrl-r` choice) and a *Default Mail App* group; *Accounts* follows the accounts and their state while it is open | `Adw.PreferencesDialog` with a view switcher, built anew on every open | Windows Settings' form; the two groups are Windows' own (the keys above, the default mail app below); the window stays open beside the main window, where accounts change |
| *Launch at Login* is the Run value; when the user turned Malachi Mail off in Settings → Apps → Startup, the row says so and links there, and turning it on reports *Autostart was not granted* | The Background portal asks | Windows keeps that choice in Settings, where only the user changes it; the app never overwrites it |
| The window's caption names the selected folder: *Inbox – Malachi Mail* | *Malachi Mail* (the folder is the list's header) | The taskbar and Alt+Tab tell windows apart by their captions; macOS shows the folder as the window's title too |
| *About Malachi Mail* is a dialog with the name, icon, developer, version, licence, website and issue tracker; its description says *A native mail client.* | `Adw.AboutDialog` with *A native mail client for the GNOME desktop.* | GTK's text names GNOME; the fields are GTK's (research U8) |
| The window's size, maximised state and pane widths are kept in the gschema's keys (`window-width`, `window-height`, `window-maximized`, `folder-pane-width`, `message-list-width`), written only from a wide layout | Declared in the gschema, never written | The keys exist; the window opens where it was left |
| The headers of a message start 12 px below the top of the pane or window | 24 px (`margin-top` of the header box) | The command row above already sets them apart; as macOS (docs/windows-port.md §11.3) |
| A message window shows the subject in its title bar and its buttons in a row below it; the star button shows the state by its icon and label (Star, Unstar), not as a pressed button | The buttons in the header bar around the subject; a toggle button | The main window's structure (a command row per pane under the title bar) in every window; a pressed WinUI button is an accent block |
| Attachment chips show Windows' icon for the file's extension | The symbolic icon of the content type | What Explorer shows for the file; macOS shows the system's icon as well |
| An attachment chip is one split button: Tab reaches it once, Enter previews, F4 or Alt+Down opens its menu | The chip and its arrow are two buttons, each reached by Tab | WinUI's `SplitButton` |
| Headers taller than two thirds of the message pane or window (hundreds of attachments, every address unfolded) scroll on their own, and the body keeps the rest | The header box grows and pushes the body down | Email is hostile input: a message listing hundreds of parts would put its body and its last chips out of reach |
| While another program holds the clipboard open, Copy Address tries again for a moment, then a toast says the address could not be copied | Always copied | The Windows clipboard is shared: a clipboard manager or a remote desktop session can hold it open |
| The attachment previewer is one window titled with the file's name, with *Open* and *Save As…* in its title bar; Escape and Ctrl+W close it | Sushi's window with its *Open With* button | Where Windows apps keep a window's actions; Escape closes Sushi and Quick Look too |

## Troubleshooting

- **The XAML compiler fails (`MSB3073`, `XamlCompiler.exe` exited with code
  1, `MSB3106` for assemblies under the NuGet cache).** A path is longer
  than 260 characters: the XAML compiler is a .NET Framework tool without
  long-path support, and enabling long paths in Windows does not help it.
  Keep the clone's path short (`D:\src\malachi`) and the NuGet cache at its
  default (`%USERPROFILE%\.nuget\packages`).
- **Run from Claude Desktop, or from an agent it started, the app's data is
  somewhere else.** Claude Desktop is an MSIX package, and every process it
  starts sees a virtualised AppData: files created under `%APPDATA%` and
  `%LOCALAPPDATA%` land in Claude's package store, invisible to the same
  program started from Explorer. Set `MALACHI_DATA_DIR` to a directory
  outside AppData for such runs (the socket is outside AppData already).
- **"Running scripts is disabled on this system".** `build.ps1` is not
  signed; run it as `powershell -ExecutionPolicy Bypass -File windows\build.ps1`
  (make does), or allow local scripts for your user.
- **`malachi-credentials` does not publish (`Platform linker not found`,
  `link.exe`, `vswhere.exe` is not recognized).** NativeAOT links with the
  MSVC tools; install them as under [Requirements](#requirements).
  `build.ps1` puts the Visual Studio Installer directory on `PATH` for
  `vswhere.exe`, which Visual Studio 2026's `vcvarsall.bat` needs.
- **`dotnet test` says "Testing with VSTest target is no longer supported"
  or does not know `--solution`.** It ran outside `windows\`, without its
  `global.json`; use `build.ps1 test` or run it from `windows\`.
- **make from PowerShell runs `cmd.exe` recipes ("'sed' is not
  recognized").** Git for Windows is missing or elsewhere; the Makefile
  finds its `usr\bin` through `git --exec-path`, or set `GIT_USR_BIN`.
- **`build\malachid.exe is in use`.** A daemon started from `build\` is
  running (`make run-backend`); that copy is left as it is, and the app
  folder gets its own.
- **The network canary is skipped or fails.** `Malachi.App.Canary` starts
  its WinUI host beyond the edge of the screen, so it needs an interactive
  desktop session and the WebView2 runtime; without either its tests are
  skipped with that reason. `MALACHI_CANARY_KEEP=1` keeps each run's
  configuration, results and NetLog under `%TEMP%\malachi-canary-<id>\`
  for a look after a failure (docs/windows-port.md §12).
- **A lock file changed after a restore.** The lock files hold every
  package version, including the ones the SDK adds by itself, which are
  pinned in `Directory.Packages.props` (`MalachiSdkPackVersion`) so that a
  newer SDK does not change them. Commit a change only together with the
  package change that caused it.

## AI agents

The repository's `.mcp.json` starts `build/malachi-mcp`, which connects to
the daemon's socket; on Windows that binary is `build\malachi-mcp.exe`
(`make mcp`, `make windows` or `build.ps1 go`). Whether Claude Code on
Windows finds it under the name without `.exe` is not verified yet. Tools,
flags and the security model are in [docs/mcp.md](../docs/mcp.md).
