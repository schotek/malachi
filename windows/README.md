# Malachi Mail for Windows

The Windows client of Malachi Mail: a native C#/WinUI 3 application over the
`malachid` JSON-RPC socket, the fourth client of the daemon next to the GTK
UI, the macOS app and the MCP bridge. It mirrors the GTK UI, which is the
primary one and the template (CLAUDE.md rule 4); the macOS client is the
source it is ported from, because it already solved the same problem. The
daemon runs on Windows unchanged and does all the mail work. How the client
is designed, built and kept in step with the GTK UI, and the decisions
behind it, are in [docs/windows-port.md](../docs/windows-port.md).

**Status: in progress, scaffold (phase B of docs/windows-port.md §15).** The
solution, its projects and packages, the build and test entry points
(`make windows`, `run-windows`, `test-windows`, `windows/build.ps1`), the
application icon and an empty main window with its title bar exist. Nothing
talks to the daemon yet: the API layer, the transport, the supervisor, the
keyring helper and the ported logic follow in phases C and D, the user
interface in phase E. Everything below that describes the running app
(where things are, the differences from GTK) is the design the phases
implement.

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
make run-windows    # the same, then runs MalachiMail.exe from the terminal until its
                    # window closes; the app's output (and later the daemon's log) stays
                    # visible
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
| `run` | `app` for this machine, then `MalachiMail.exe` until its window closes |
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
travel with it, nothing is installed): about 270 MB today, of which the
Windows App SDK metapackage's AI and ML runtime takes some 60 MB until the
component set is chosen.

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
  scripts/make-icons.ps1          docs\malachi_icon.png -> multi-size Malachi.ico
  src/
    Malachi.Core/                 net10.0: everything that needs neither WinUI nor P/Invoke
                                  (API, transport, supervisor, ported logic, controllers,
                                  settings, i18n); builds and is tested on any OS
    Malachi.Platform.Windows/     Windows services behind Core's interfaces (CsWin32)
    Malachi.App/                  WinUI 3 -> MalachiMail.exe; thin: windows, pages, XAML,
                                  the WebView2 layer
    Malachi.Credentials/          malachi-credentials.exe, the daemon's keyring helper over
                                  Credential Manager (NativeAOT); depends on nothing else
  tests/
    Malachi.Core.Tests/           the Go UI and Swift tests ported, FakeDaemon, MailFixture
    Malachi.Core.TestDaemon/      a stand-in daemon for the supervisor tests
    Malachi.FakeBridge/           a scripted malachi-mcp for the MCP registration tests
    Malachi.Platform.Windows.Tests/
    Malachi.Credentials.Tests/
    Malachi.Conventions.Tests/    repository checks: SPDX headers (later strings, msgids,
                                  gschema keys)
```

The dependency direction is `App -> Platform.Windows -> Core`, never back;
nothing imports the Go modules (the API is re-declared from
[docs/api.md](../docs/api.md), as on macOS). The tests are xUnit v3 on
Microsoft.Testing.Platform.

## Where things are

The design of docs/windows-port.md §1; the app does not use them yet.

| What | Where |
|---|---|
| App folder (built here) | `build\windows\<arch>\Malachi Mail\`: `MalachiMail.exe`, `malachid.exe`, `malachi-mcp.exe`, `malachi-credentials.exe`, `locale\<lang>.po`, the licences |
| Configuration | `%LOCALAPPDATA%\Malachi Mail\config.toml` (`--config`) |
| Mail store | `%LOCALAPPDATA%\Malachi Mail\store.db` (`--store`), lock `store.db.daemon.lock` |
| RPC socket | `%USERPROFILE%\.cache\malachi\run\rpc.sock`, the daemon's own default (`MALACHI_SOCKET` overrides); outside AppData on purpose |
| RPC key | `rpc.sock.key` beside the socket, a new key at every daemon start, read afresh for every connection |
| Logs | `%LOCALAPPDATA%\Malachi Mail\logs\` (and the terminal under `make run-windows`) |
| WebView2 data | `%LOCALAPPDATA%\Malachi Mail\WebView2\` |
| Attachments being opened | `%LOCALAPPDATA%\Malachi Mail\open\<random>\` (private, emptied at start and exit) |
| Preferences | `HKCU\Software\io.github.schotek.Malachi`, the gschema's keys plus `ctrl-r` |
| Passwords, sign-ins | Credential Manager, generic credentials `io.github.schotek.Malachi/<accountId>/<key>` |
| Launch at login | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, value `Malachi Mail` |
| `mailto:` | `HKCU\Software\Classes\io.github.schotek.Malachi.mailto`, `HKCU\Software\Clients\Mail\Malachi Mail`, `HKCU\Software\RegisteredApplications` |

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
| Windows keys: Ctrl+R Reply, Ctrl+Shift+R Reply All, Ctrl+Shift+F Forward, F5 Check for New Mail; the setting `ctrl-r` (`reply` by default, or `refresh`) gives Ctrl+R to Check for New Mail instead, as macOS's `command-r`. Besides GTK's single keys (Delete, A, J, U, S), Ctrl+D, Ctrl+U, Insert and Ctrl+Q (Mark as Read) work, also with the message view focused; Quit is Ctrl+Shift+Q; Escape and Ctrl+W close a secondary window | Ctrl+R Check for New Mail, Ctrl+Q Quit, the single keys, Escape | Outlook's scheme is what Windows users know; bare letters do not reach the app from a focused WebView2 |
| Alerts are `ContentDialog`s: the primary button on the left, Cancel on the right; the defaults and close responses stay GTK's (*Save Draft* is the default of the close question) | GTK's button order | WinUI's dialog |
| Files opened or saved from a message get the Mark of the Web through `IAttachmentExecute` (which also runs the antivirus check and the attachment policy); a file whose zone cannot be read back is not opened | No mark | The counterpart of the macOS quarantine attribute: SmartScreen and Office's Protected View treat the files as downloads |
| A click on an attachment previews images, PDF and text in the app's own locked-down WebView2 window (no network, no script, nothing written to disk); other types offer Open and Save As…; programs are never opened | GNOME Sushi, the default application without it | Windows has no Quick Look or Sushi, and shell preview handlers run third-party code over hostile files |
| While the app runs in the background, a notification-area icon offers Open, New Message, Check for New Mail and Quit | No icon | A background app is invisible on Windows otherwise |
| Context menus on messages and folders, with the actions that exist elsewhere | None | Windows convention |
| *Preferences* has a *Default apps* button that opens Settings → Apps → Default apps | None | Windows does not let an app make itself the default mail app; the app registers itself in HKCU at start |
| Quitting saves the unsaved changes of every message being written as drafts | The compose windows close; what was typed since the last automatic save is lost | Decided |
| A link the daemon did not list in `links[]` is confirmed before it opens, as on macOS | Opened | WebView2 hands out normalised URLs, so an exact match with the daemon's raw hrefs can fail (docs/windows-port.md §6.4) |
| The new-mail sound is the user's *New Mail Notification* system sound, skipped in quiet hours | The sound theme's `message-new-email` | Windows' own event for it |
| *Preferences* has no search field | `Adw.PreferencesDialog` with search | Decided (as macOS) |
| The daemon's key file (`rpc.sock.key`) is used only when the current user owns it and its DACL grants access to nobody but the user, SYSTEM and Administrators (a NULL DACL is refused), besides being a regular file, not a reparse point, of 65 bytes in the key format | The Go clients check the file's type, size and format | Defence in depth, the counterpart of macOS's owner and mode check |
| The message list pages itself at its end; *Load More* appears only to retry a page that failed | The *Load More* button under the list | As macOS |
| In *Preferences → Accounts*, clicking a row selects it; *Enabled* is the switch alone | The row activates its switch | Ctrl+Up / Ctrl+Down reorder the selected row, so a click must select (as macOS) |
| The window's size, maximised state and pane widths are kept in the gschema's keys (`window-width`, `window-height`, `window-maximized`, `folder-pane-width`, `message-list-width`), written only from a wide layout | Declared in the gschema, never written | The keys exist; the window opens where it was left |

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
