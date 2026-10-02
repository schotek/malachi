<!--
SPDX-FileCopyrightText: 2026 Vladislav Janeček
SPDX-License-Identifier: GPL-3.0-or-later
-->

# The Windows client

How the WinUI 3 client in `windows/` is built, for people (and agents) who
change it. What it does and how to build it is in
[windows/README.md](../windows/README.md); why there is a native client per
platform at all is in [architecture.md §6](architecture.md#6-platform). It
is the Windows sibling of [macos-port.md](macos-port.md) and follows the
same model: a client of the daemon's API, a mirror of the GTK UI, no mail
logic of its own.

**Status: the full mail UI of the GTK application, built on
`feat/windows` (2026-09-27 and 28), with the Assistant (§11.6) and the
Jira accounts and conversation view (§11.7, `feat/jira-windows`,
2026-09-30) since.** §0 records the decisions, each with
where it is built; §15 the phases the port was built in and their gates;
§16 the research and the measurements it started from; §17 what is still
open before a public release. Where a section says *measured* or
*verified*, it was run on the development machine (Windows 11 Pro 26100
x64), not taken from documentation.

The one sentence that governs everything below, unchanged from macOS: **the
GTK UI is the template, and the daemon is the only place logic lives.** The
macOS client is the primary *source* of the port, because it already
solved the same problem (a native client over the socket, the Go UI's logic
ported 1:1 with its tests); the GTK UI is what the port is *validated*
against. Where macOS deviates from GTK for Mac reasons, Windows goes back
to GTK unless a Windows convention wins, and every such choice is a row in
the deviation table of `windows/README.md`.

## 0. Decisions

Decided by the owner on 2026-09-27 unless marked *architecture* (decided in
the port with the research of the same day; the reports are summarised in
§16). The last column says where each decision is built; every row is,
except for the Windows CI job of the backend row and what §17 lists.

| Topic | Decision | Built |
|---|---|---|
| Language, runtime | C# on .NET 10 (LTS), WinUI 3 on the Windows App SDK **2.5.x** (1.8 left servicing on 2026-09-24): the component packages `Microsoft.WindowsAppSDK.WinUI` and `.InteractiveExperiences` instead of the metapackage, which adds ~60 MB of AI libraries a mail client never uses (measured; the notification fix of §10 comes with it) | Built: `windows/Directory.Packages.props` (WinUI 2.3.9, InteractiveExperiences 2.1.9, the 2.5.1 Runtime package downloaded for one DLL), §2, §13 |
| Windows versions | Windows 11 only (`TargetPlatformMinVersion` 10.0.22000.0) | Built: `Malachi.App.csproj` |
| Architectures | x64 and ARM64 | Built: `build.ps1 -Arch x64\|arm64`, the solution's two platforms. x64 is built and run here; for ARM64 the app, the daemon and the bridge cross-build (2026-09-28), the NativeAOT keyring helper needs the MSVC ARM64 build tools, and nothing ARM64 has run yet (§17) |
| Packaging | **Unpackaged, self-contained**, per user. `make windows` assembles a folder like the macOS `.app`; the app registers itself in HKCU. Installer (Velopack + winget), code signing and the licence permission for Microsoft components come before the first public binary (§17). MSIX is out: its AppData virtualisation hides `config.toml` and the store and breaks the Claude registration, the same reason macOS has no App Sandbox | Built: `build.ps1 app` and `package` (a zip of the folder), the HKCU registrations of §10; the installer, signing and the licence permission are §17 |
| Entry points | `make windows`, `make run-windows`, `make test-windows`, from PowerShell and Git Bash (GNU make 4.4 from winget `ezwinports.make`); `windows/build.ps1` does the work | Built: the root `Makefile`, `windows/build.ps1` (§13), verified from both shells |
| Stopping the daemon | `CTRL_BREAK_EVENT` through `AttachConsole`/`GenerateConsoleCtrlEvent` (Go maps it to SIGINT; verified clean exit in ~18 ms), `Kill` after 15 s. No backend change; GTK/macOS semantics kept (a daemon left behind by a crashed UI is adopted, never stopped) | Built: `Malachi.Platform.Windows` `Process/` and `Console/`, §5 |
| Secrets | `malachi-credentials.exe`, the helper keyring over **Windows Credential Manager**, values above the 2560-byte blob limit split into hash-checked chunks | Built: `Malachi.Credentials`, §10 |
| Main window | **GTK structure**: a command row per pane under a slim title bar that holds the search box; the primary menu behind a `…` button (no menu bar); GTK back navigation at 900/600 px or less; the status line across the whole bottom edge | Built: `MainWindow`, `Malachi.App/Main`, §11.1 |
| Keyboard | Windows scheme: Ctrl+R Reply, Ctrl+Shift+R Reply All, Ctrl+Shift+F Forward, F5 Check for New Mail, Ctrl+F/Ctrl+E search; a setting `ctrl-r` (`reply`, default, or `refresh`) as macOS's `command-r`; otherwise GTK's keys, Ctrl+Q Quit and A/J/U/S/Delete included (no Outlook aliases: a pre-translate handler delivers every key even with a WebView2 focused, §11.5) | Built: `ShortcutMap` (Core), `Malachi.App/Commands`, §11.5 |
| Attachment click | An **own previewer**: images, PDF and text in a locked-down WebView2 window (no network, no script, no temporary file); other types offer Open / Save As; programs are never opened | Built: `PreviewWindow`, `PreviewWebView`, §6.6 |
| Windows additions | Notification-area icon while running in the background; context menus on messages and folders; a *Default apps* button in Preferences; dirty drafts saved on Quit | Built: §10 (tray, *Default apps*, Quit), §11.2 (context menus) |
| Unlisted links | Confirmed before opening, as on macOS (GTK opens them; see §6.4) | Built: `LinkDecision`, `LinkOpener`, §6.4 |
| Dependencies | CsWin32, CommunityToolkit.WinUI Controls, CommunityToolkit.Mvvm, Microsoft.Extensions.Logging.Abstractions / TimeProvider.Testing; xUnit v3 for tests. Each justified in its commit (CLAUDE.md) | Built: `Directory.Packages.props`; besides these the Windows SDK build tools and, for the strings check (§9), Roslyn (`Microsoft.CodeAnalysis.CSharp`). Of the toolkit's controls the app uses SettingsControls and Sizers (Segmented, unused once the filter became a `SelectorBar`, §11.1, is no longer referenced) |
| Backend changes | Four platform-neutral fixes on the branch, each its own commit (§14): the helper path check, the stored files that Windows will not rename or remove while open (after sending, and in main's raw store), `malachi-mcp --claude-desktop-config/--command`, and `.gitattributes` + portable Go tests + Windows CI | Built, except the Windows CI workflow (§13, §17) |
| Settings store | *architecture*: `HKCU\Software\io.github.schotek.Malachi`, the gschema keys, change notification through `RegNotifyChangeKeyValue` (the counterpart of GSettings signals and macOS KVO) | Built: `SettingsStore` (Core), `RegistrySettingsBackend`, §8 |
| Data | *architecture*: `%LOCALAPPDATA%\Malachi Mail\` for `config.toml`, `store.db`, logs, the WebView2 data and the open directory; the socket stays at the daemon's default outside AppData | Built: `Paths` (Core), §1 |
| Translations | *architecture*: `po/*.po` parsed at run time (no generator, no Python in the Windows build), GTK msgids as keys as on macOS | Built: `Malachi.Core/I18n`, `{l:T}`, §9 |
| Tests | *architecture*: xUnit v3 on Microsoft.Testing.Platform; the Core tests run on any OS; the Go UI and Swift tests ported 1:1 | Built: the six test projects of §12 and their four helpers |
| Preferences | *architecture*: named *Preferences* (the translated GTK msgid), no search field in v1 (as macOS) | Built: `PreferencesWindow`, §11.3 |

## 1. Process model

```
┌───────────────────────────────────────────┐                    ┌───────────────────────┐
│  build\windows\<arch>\Malachi Mail\       │                    │  malachid.exe (Go)    │
│    MalachiMail.exe  (WinUI 3)             │ ◄─ JSON-RPC 2.0 ─► │  all the logic        │
│                                           │    AF_UNIX socket  │                       │
│    starts ─► malachid.exe ────────────────┤                    │  --socket --config    │
│      CREATE_NO_WINDOW | NEW_PROCESS_GROUP │                    │  --store              │
│      --socket --config --store            │                    │  MALACHI_KEYRING=     │
│      MALACHI_KEYRING=helper               │                    │    helper             │
│      MALACHI_KEYRING_HELPER=…\malachi-credentials.exe          └──────────┬────────────┘
│      DBUS_SESSION_BUS_ADDRESS=disabled:   │                               │
│    stops ─► CTRL_BREAK, Kill after 15 s   │                               │
│                                           │                               │
│    malachi-credentials.exe ◄── one process per get/set/delete ────────────┘
│        (Credential Manager, CredWriteW/CredReadW/CredDeleteW)
│    malachi-mcp.exe ◄── spawned by an MCP client, same socket
└───────────────────────────────────────────┘
```

The app does what `ui/internal/daemon` and `DaemonSupervisor.swift` do:
it looks for `malachid.exe` (`MALACHI_DAEMON`, then beside its executable,
then `PATH`), probes the socket, starts the daemon when nothing answers,
waits up to 15 s for the socket, restarts it after an exit with a backoff
from 1 s doubling to 60 s, and stops the daemon it started when the
application quits. A daemon that already answers is adopted and never
stopped. `DBUS_SESSION_BUS_ADDRESS=disabled:` keeps the Secret Service,
GOA and EDS paths from searching `PATH` for `dbus-launch` on every call
(~10 ms per `contact.search` keystroke, measured).

The app starts in its own folder (Explorer, a shortcut and `make
run-windows` all make it the working directory) and leaves it for the
user's profile before anything else (`Program.LeaveLaunchDirectory`):
every process the app starts inherits its working directory, and one that
outlives it (Claude Desktop, started by its restart around *Register with
Claude* or by a hand-off, a browser opened for a link, a viewer opened
for an attachment)
would keep the folder from being removed or updated as long as it runs.
Measured: a Claude Desktop the app had started kept `build\windows\x64\Malachi Mail`
until its own exit, and `build.ps1 app` could not replace it. The
`MALACHI_*` variables that name a path and were given relative are made
absolute against the launch folder first. `build.ps1` empties the app
folder rather than removing it, for what an older build started.

One file of the folder is run by other programs for as long as they
like: Claude Desktop and Claude Code start the registered
`malachi-mcp.exe` and keep it until they quit (measured: two bridge
processes of a Claude Desktop the app had restarted, and `build.ps1 app`
failed with "access to the path … malachi-mcp.exe is denied" with half
the folder removed). Windows neither deletes nor overwrites the file of a
running program, but it renames it, so `build.ps1` moves a file in use
into `build\windows\replaced\` (the same volume, as a rename needs), says
so, and removes it in a later build once it is free; the program that
runs it gets the new bridge at its next start. The same holds for
`build\malachi-mcp.exe`, which `.mcp.json` names, and for a daemon of
`make run-backend`. The app, its daemon or the helper still running from
the app folder stop the build before anything is removed.

| What | Where |
|---|---|
| App folder (dev) | `build\windows\<arch>\Malachi Mail\` with `MalachiMail.exe`, `malachid.exe`, `malachi-mcp.exe`, `malachi-credentials.exe`, `locale\<lang>.po`, licences |
| Configuration | `%LOCALAPPDATA%\Malachi Mail\config.toml` (`--config`) |
| Mail store | `%LOCALAPPDATA%\Malachi Mail\store.db` (`--store`), lock `store.db.daemon.lock` |
| RPC socket | `%USERPROFILE%\.cache\malachi\run\rpc.sock`, the daemon's own default (`MALACHI_SOCKET` overrides). Outside AppData on purpose: `malachi-mcp` and `.mcp.json` work unchanged, and nothing under AppData is exposed to MSIX redirection (Claude Desktop is itself MSIX, and its children see a virtualised AppData) |
| RPC key | `rpc.sock.key` beside the socket; read afresh per connection with `FileShare.ReadWrite \| FileShare.Delete` |
| Daemon and app logs | `%LOCALAPPDATA%\Malachi Mail\logs\` (and the terminal under `make run-windows`) |
| WebView2 data | `%LOCALAPPDATA%\Malachi Mail\WebView2\` (InPrivate profiles; only browser-level state is written; the renderers' crash dumps removed at start and exit, §6.1) |
| Attachments being opened | `%LOCALAPPDATA%\Malachi Mail\open\<random>\` (protected DACL, cleared at start and exit, entries older than an hour swept; the removal refuses any other path, §10) |
| Preferences | `HKCU\Software\io.github.schotek.Malachi` |
| Passwords, sign-ins | Credential Manager, generic credentials `io.github.schotek.Malachi/<accountId>/<key>` |
| Launch at login | `HKCU\…\CurrentVersion\Run` value `Malachi Mail` = `"<exe>" --background`, `StartupApproved` respected |
| `mailto:` | `HKCU\Software\Classes\io.github.schotek.Malachi.mailto`, `HKCU\Software\Clients\Mail\Malachi Mail\Capabilities`, `HKCU\Software\RegisteredApplications` |
| Notifications | `AppNotificationManager.Register("Malachi Mail", Assets\notification.png)`, no explicit AUMID: Windows keys the registration by the executable's path (`HKCU\Software\Classes\AppUserModelId\<path>`) |

Development overrides, as on macOS: `MALACHI_DAEMON` (path, or `none`),
`MALACHI_SOCKET`, `MALACHI_KEYRING`/`MALACHI_KEYRING_HELPER` (a preset
`MALACHI_KEYRING` wins over the bundled helper), `MALACHI_LOCALE_DIR`, the
daemon's `MALACHI_DEFAULT_COMPRESS_STORE` and
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS` (the app sets neither, unlike the
macOS app, §5; set in its environment they reach the daemon), and
two Windows-only variables: `MALACHI_DATA_DIR`, which replaces
`%LOCALAPPDATA%\Malachi Mail` for tests and agents, and
`MALACHI_SETTINGS_KEY`, which gives a test a preferences key of its own
(§8). An agent running inside
Claude Desktop's process tree must use the first: new files under AppData are
silently redirected into Claude's package store there, and so are writes to
HKCU (measured), so an agent that tests settings, `mailto:` registration,
launch at login or notifications starts the app **outside** that tree
(for example through WMI `Win32_Process.Create`). With `MALACHI_DATA_DIR`
set, the app also leaves the user's registrations alone at start: it
neither writes the `mailto:` registration nor points a Run value at itself
(`Registration/SelfRegistration`), because such a copy usually runs from a
worktree or temporary folder that is deleted later, and a `mailto:` handler
or Run value pointing there would break for a user who chose Malachi Mail.
What the user asks for in Preferences is still written. A copy that a
click on a notification starts is started by COM, without the variable,
so a test of such a cold start removes the registration afterwards.

## 2. Solution and module boundaries

```
windows/
  README.md                     what it does, how to build, the deviation table
  build.ps1                     version|go|build|icons|app|test|run|lint|package|clean (PowerShell 5.1 and 7)
  Malachi.slnx                  x64 and ARM64 configurations
  global.json                   SDK 10.0.400 (rollForward latestFeature), test runner Microsoft.Testing.Platform
  nuget.config                  nuget.org only, package source mapping
  Directory.Build.props         nullable, warnings as errors, code style in build, artifacts under build\windows
  Directory.Build.targets       locale copy, daemon copy for F5, icons
  Directory.Packages.props      central package versions, lock files
  .editorconfig                 C# style, the SPDX header rule (IDE0073)
  parity-exclusions.txt         the msgids of po/malachi.pot the client does not use, with reasons (§9)
  scripts/make-icons.ps1        docs/malachi_icon.png → .ico and the notification icon (crop as macos/Makefile)
  src/
    Malachi.Core/               net10.0, no WinUI, no P/Invoke: everything testable anywhere
    Malachi.Platform.Windows/   net10.0-windows: Windows services behind Core interfaces (CsWin32)
    Malachi.App/                WinUI 3 → MalachiMail.exe
    Malachi.Credentials/        → malachi-credentials.exe, NativeAOT, depends on nothing else
  tests/
    Malachi.Core.Tests/         the Go UI and Swift tests ported, FakeDaemon, MailFixture
    Malachi.Core.TestDaemon/    a stand-in daemon (listen, slow, exit, deaf) for supervisor tests
    Malachi.FakeBridge/         a scripted malachi-mcp for the MCP registration tests
    Malachi.Platform.Windows.Tests/
    Malachi.Credentials.Tests/
    Malachi.Conventions.Tests/  msgid and gschema coverage, the strings check, SPDX headers
    Malachi.App.Canary/         the network canary (§12) over the WebView2 layer
    Malachi.App.Canary.Host/    its WinUI host, compiling src/Malachi.App/WebViews
    Malachi.App.UiTests/        UI smoke tests over the published app (UI Automation), devmail opt-in
    Malachi.FakeKeyring/        a keyring helper over a JSON file for the UI tests
```

| Project | May use | Holds |
|---|---|---|
| `Malachi.Core` | BCL, System.Text.Json (source generated), CommunityToolkit.Mvvm, Microsoft.Extensions.Logging.Abstractions | The typed API, the transport and handshake, the daemon supervisor's state machine, the pure logic ported from the Go UI and macOS (models, threads, folding, favourites, search, address parsing, quoting, wizard fields, HTML documents, the editor bridge, formatting, error texts), the controllers, the presentation classes macOS keeps in AppKit (§7.4), settings and i18n. `IsAotCompatible`. |
| `Malachi.Platform.Windows` | Core, CsWin32 | Process host (spawn, CTRL_BREAK, kill), key-file policy (owner SID, DACL), private directories (protected DACL; the open directory's logic, the never-open list and Windows-safe names are in `Malachi.Core.Platform`), registry settings backend and its watcher, Mark of the Web (`IAttachmentExecute`), the file-type policy (`AssocIsDangerous`), ANSI best-fit look-alikes, launcher, launch at login, `mailto:` registration, preferred languages, tray icon interop |
| `Malachi.App` | WinUI 3, WebView2, CommunityToolkit.WinUI Controls, Core, Platform.Windows | Windows, pages, XAML, the WebView2 layer, command wiring. Thin: it renders what a controller holds and sends clicks back |
| `Malachi.Credentials` | BCL, CsWin32 | The keyring helper; independent of everything else |

The dependency direction is `App → Platform.Windows → Core`, never back.
Nothing imports the Go modules: the API is re-declared from
[api.md](api.md), as on macOS.

## 3. The parity principle

The reference for every screen is its Blueprint in `ui/data/ui/*.blp` and
the Go file behind it; the Swift file of the same screen is the porting
source. "Mirror" means what it means in [macos-port.md §3](macos-port.md#3-the-parity-principle):

- every element of the Blueprint has a counterpart in the same order and
  place; sizes and margins come from the Blueprints and `ui/internal/style`;
- every behaviour of the Go UI is ported 1:1 (actions, confirmations,
  optimistic changes with revert, timeouts, error texts, generation
  counters, `closed`/`op` guards, the mark-as-read delay, the autosave
  delay), with the Go names where they are meaningful;
- every file starts with the SPDX header and then names what it ports:
  `// Port of macos/Sources/…/X.swift; GTK: ui/internal/…/y.go (funcName)`;
- every user-visible string goes through `L10n.T/N/C` with the GTK msgid as
  the key (§9); a string with no GTK counterpart is marked
  `// Windows-only string` and stays English;
- the pure-logic Go tests and the Swift tests are ported to xUnit suites of
  the same shape (§12);
- mail data is hostile input: `TextBlock.Text`/`TextBox.Text` only, never
  a XAML or RTF parser over anything from a message; HTML only in the
  WebView2 views of §6; and what the chrome shows of it (names, subjects,
  a server's folder names, captions, notifications, attachment names)
  passes `Text.DisplayText` first: no bidi formatting or control character
  reaches the screen, a subject or name that draws nothing takes its
  fallback, and a name composed with other text is isolated (U+2068 …
  U+2069), so it cannot reorder the address after it ([security.md §4](security.md#4-message-parsing-mime);
  Windows-only, a row of the deviation table);
- where Windows differs, the row goes into the deviation table of
  `windows/README.md` and the code says why. A new deviation never lands
  silently.

### 3.1 Conventions for the C# code

- **Files and names.** One type per file, the file named after it; folders
  and namespaces mirror the Swift folders (`Malachi.Core.Api`,
  `.Transport`, `.Daemon`, `.Model`, `.Compose`, `.Html`, `.Wizard`,
  `.Controllers`, `.Presentation`, `.I18n`, `.Settings`, `.Text`,
  `.Platform`), file-scoped namespaces. Swift type names are kept; members
  become PascalCase with the same words (`reconnectNow` →
  `ReconnectNow`), so a reader can diff the two. The header is the two SPDX
  lines, a blank line, then the port note.
- **Data.** API records are `sealed record`s with `init` members,
  `required` where Swift is non-optional, lists as `IReadOnlyList<T>`
  defaulting to `[]` (never `ImmutableArray<T>` in records: its equality is
  by reference). Optimistic changes are `with` expressions. Models that
  Swift keeps as mutable structs become classes with explicit `Clone()`
  where a snapshot is taken.
- **JSON.** System.Text.Json source generation only (one
  `JsonSerializerContext` per contract, its type list grouped by area: the
  generator refuses `[JsonSerializable]` on several partial declarations);
  no reflection-based serialisation, no `dynamic`.
- **Async.** `Task`/`ValueTask`; cancellation through `CancellationToken`.
  Transport and platform code use `ConfigureAwait(false)`; controllers and
  presentation classes never do (§7.1). Fire-and-forget only through
  `Perform` (§7.2).
- **Time.** `TimeProvider` injected everywhere; no `DateTime.Now`,
  `Task.Delay` or timers without the provider.
- **Logging.** `ILogger<T>`; method names, codes and ids only. Mail
  content, addresses, keys, nonces, proofs and tokens are never logged
  (wrap payload-like values in `Sensitive<T>`, which renders `<private>` in
  Release).
- **Strings.** `L10n.T/N/C` with the GTK msgid verbatim, formats through
  the Go-printf formatter, never concatenation; `// Windows-only string`
  where there is no msgid.
- **Errors.** Exceptions carry the exact Go/Swift texts wherever a test or
  a user-visible mapping compares them.
- **Tests.** xUnit v3, one test class per Swift suite and Go test file with
  the same name, `[Theory]` for parameterised cases, no sleeps
  (`FakeTimeProvider`, `IdleAsync`).
- **Platform code.** Only in `Malachi.Platform.Windows` and `Malachi.App`;
  P/Invoke and COM through CsWin32 (`NativeMethods.txt`), never hand-written
  where CsWin32 can generate it.

The eleven macOS deviations that its table does not list (the parity
report's U1–U11) are resolved for Windows as follows: unlisted links are
confirmed (U1, decided); the AI page reports bridge failures in the group
description as GTK does (U2, U3); a cold `mailto:` launch opens only the
composer as GTK does (U4); the sidebar star is keyboard-reachable (U5);
provider rows use the generic icon (U6) and notification titles are capped
(U7), both rows of the deviation table; About carries the GTK fields (U8);
there is no Help item (U9); the gschema's geometry keys are used, the
pane widths, which GTK leaves unused, as well (U10); the window is
*Preferences* (U11).

## 4. The API layer

`Malachi.Core/Api/` re-declares `backend/pkg/api` from api.md, one file per
area as `MalachiCore/API/`:

- every method is a static descriptor `RpcMethod<TParams, TResult>(name,
  timeout)` carrying the `JsonTypeInfo` of both types;
  `client.CallAsync(API.MessageList, params, ct)` is the only way to call
  one, so a typo cannot compile. The table is the static class `API`, the
  Swift name kept: a class `Api` in the namespace `Malachi.Core.Api` would
  be read as that namespace from every other `Malachi.Core.*` namespace. It
  has all 48 methods in the order of `api.AllMethods`, stubs included;
  `system.hello` and `system.authenticate` are sent only by the handshake;
- records are sealed and immutable (`init`, lists as `IReadOnlyList`),
  changed with `with`: the optimistic reverts of the controllers rely on
  snapshots that nothing aliases. Swift's names are kept, with one forced
  exception: `Address.address` is `Address.Email` (C# allows no member
  named like its type);
- JSON is System.Text.Json with one source-generated context,
  `ApiJsonContext` (trimming and AOT stay possible; a NativeAOT build shows
  no warning): property names verbatim via `[JsonPropertyName]`, nulls
  omitted on write, relaxed escaping (the default inflates HTML about
  sixfold; `JsonCoding.WriterOptions` for a writer the transport owns), a
  null-as-empty converter for arrays (Go's nil slices), C# `required`
  where Swift is non-optional with the same lenient exceptions (and a
  missing `error.message` read as empty, as Go's client does), a null in a
  member that is not nullable refused as Swift refuses it, and an RFC 3339
  converter that requires a zone, writes `Z` and clamps Go's year 0 instead
  of failing a whole list. Source generation reads an absent init-only
  member as its type's default, not the property's initializer, so every
  list and string that must never be null coalesces in its `init`;
- wire enums (`FolderRole`, `Flag`, `SyncStatus`, …, 22 of them) are
  `readonly record struct`s over the wire string with `const` members, so
  an unknown value from a newer daemon decodes; `ErrorCode` is a record
  struct over `int` with the 34 documented codes and a `Name`;
  `RpcError.Data` survives as a cloned `JsonElement`. The tests compare the
  method table, the error codes, every wire enum's values and every
  record's members with `backend/pkg/api` as it is in the tree;
- the protocol version is compared in the handshake before the key file is
  read, and again with `system.info` (`ConnectionController`), exactly as
  on macOS;
- timeouts are the GTK UI's (`RpcTimeouts`): 5 s default, 5 s for the whole
  handshake, 3 s `system.info`, 60 s `message.part` and `attachment.get`,
  30 s `message.body` (always: a stored `allow` or a known sender may
  resolve to `allow`), `message.embedded`, `draft.create`, `draft.open`,
  `account.add`/`update`, 15 s `account.discover`, 45 s `account.test`, 10 s
  `account.oauthStart`, 75 s per `account.oauthWait`, 5 min
  `message.download` (the daemon's budget is 4 minutes, and it finishes a
  download its caller gave up on).

`docs/api.md` and `backend/pkg/api` are not changed from `windows/`. A
feature that needs a new method goes backend → GTK → macOS → Windows.

## 5. Transport, handshake, daemon supervisor

**Transport** (`Malachi.Core/Transport`, `RpcClient`).
`Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified)`
with `UnixDomainSocketEndPoint`, newline-framed JSON-RPC, one reader
shared by the handshake and the read loop (the shape
of Go's `client.go`; what the handshake read beyond its last answer goes
to the `LineFramer` once the state is Connected), a `TaskCompletionSource`
per id created with `RunContinuationsAsynchronously`, writes serialised and
never cut short, notifications and state changes on unbounded channels in
the daemon's order (and as events, raised under the client's lock). The
dial probes first (nothing listening fails at once) and gives up after
5 s; a call's timeout is its descriptor's, on the `TimeProvider`, and the
caller's cancellation is an `OperationCanceledException`. 64 KiB line cap
during the handshake, 32 MiB after (as Swift's framer, the cap bounds what
waits for its newline). Measured on this machine: connect plus
handshake 0.4 ms, a `system.info` round trip 0.05 ms.

**Handshake** (api.md §1.4): `system.hello` with a 32-byte client nonce;
compare `protocolVersion` first; then read `<socket>.key`; check the
daemon's proof in constant time (`HMACSHA256`,
`CryptographicOperations.FixedTimeEquals`); send `system.authenticate`. The
key is never cached, logged or kept. The test vectors of §1.4 and Go's
failure table (`backend/pkg/api/handshake_test.go`) are ported.

**Key file policy** (a listed deviation, the counterpart of macOS M27):
Go's checks (a regular file, not a reparse point, exactly 65 bytes, the key
format) are `DaemonKey`'s in Core; `IKeyFilePolicy` adds the platform's.
`WindowsKeyFilePolicy` opens the file with `FILE_FLAG_OPEN_REPARSE_POINT`
(a link or junction is refused as itself, never followed), refuses on
opening what `GetFileType` does not call a disk file (a pipe, `NUL`),
requires the owner to be the current user (or the token's default owner,
which an elevated run gives its files), and lets the DACL give the data
(read, write, append, directly or through generic rights) or `WRITE_DAC` /
`WRITE_OWNER` to no one but the user, SYSTEM, Administrators and OWNER
RIGHTS; a NULL DACL is refused. The reasons are macOS's texts, in Swift's
order: what the file is, then whose it is and who may read it, then its
size and content; whatever a policy throws is `keyUnavailable`, as every
error of `api.ReadKeyFile` is. `RpcClient` takes the policy as a required
argument: the app passes `WindowsKeyFilePolicy`, tests and other systems
`PortableKeyFilePolicy.Instance` (Go's rule), so no composition root loses
the check by leaving it out. The daemon's 0600 means nothing on Windows:
the key file inherits its directory's ACL. A `MALACHI_SOCKET` directory
must therefore be private, as the run directory is, or the client refuses
the key as accessible to other users (outside the profile, say `D:\…`,
Authenticated Users may modify by default). Before the
first spawn the client creates the run directory
`%USERPROFILE%\.cache\malachi\run` with a protected DACL (user and SYSTEM).
The key is read with `FileShare.ReadWrite | FileShare.Delete`: a reader
without delete sharing makes the daemon's shutdown retry and leave the key
behind (measured); a sharing violation (another process holding the file
exclusively) is retried four times within about 300 ms on the
`TimeProvider`. The key lives in a buffer zeroed after use.

**AF_UNIX on Windows** (all measured): the path limit is 107 UTF-8 bytes
(checked at start with a message naming `MALACHI_SOCKET`); a missing path
or a stale socket file is `ConnectionRefused`, a missing directory
`NetworkDown`; a non-blocking connect to a listener answers `WouldBlock`
(in progress), and a full backlog fails at once with
`NoBufferSpaceAvailable` (both count as "a listener is there" for the
probe, `UnixSocketProbe`); socket files are reparse points; after a hard
kill the daemon replaces the stale socket itself. `File.Move` with
overwrite onto a file that a reader holds, even with delete sharing, is
refused; deleting it is not.

**Supervisor.** The state machine of `DaemonSupervisor.swift` in Core
(`Malachi.Core.Daemon`: `DaemonSupervisor`, `Paths`, `DaemonLaunch`; probe
every 100 ms, start timeout 15 s, stop timeout 15 s, backoff 0 then 1 s
doubling to 60 s, adopt foreign daemons and never stop them), over an
`IDaemonProcessHost`; `BeginStopping` stops restarts at once for the console
handler (CTRL_CLOSE below). `EnsureAsync` and `StopAsync` leave the
caller's thread before they do anything (the Swift actor hop): the probe's
connect, the run directory's DACL and `CreateProcess` never run on the UI
thread. The Windows host
(`Malachi.Platform.Windows.Processes.DaemonProcessHost`) does not use
`Process.Start`, which hands every inheritable handle of the app to the
child: `CreateProcessW` with `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` passes
exactly NUL as stdin and one pipe as stdout and stderr (the daemon's lines
stay in order), `CREATE_NEW_PROCESS_GROUP`, `CREATE_NO_WINDOW` only when the
app has no console, the arguments `--socket --config --store` quoted as the C
runtimes parse them. NUL and the pipe's write end are inheritable from
their creation to their close after `CreateProcessW`, and the MCP bridge
is started with `Process.Start` (§10), which hands its child every
inheritable handle of the app: a bridge started in that window would hold
the daemon's output pipe, so the pipe would not end with the daemon, and
its exit would be seen only when the wait for its last lines gives up,
`DrainGrace` (2 s) late (measured). Both starts therefore take one lock,
Core's `SpawnGate` (`Malachi.Core.Platform`, no Windows API): the daemon's
for that window, inside the console gate, the bridge's around
`Process.Start`; each is held for the synchronous start alone. The
launcher's `ShellExecuteEx` (§10)
takes no gate: it hands its child no handle of the app (measured: an
inheritable pipe that `Process.Start` without the shell passes on does not
arrive), and it can wait on the shell's dialogs, which must never hold up
the daemon's start. The environment is the app's plus
`MALACHI_KEYRING=helper`/`MALACHI_KEYRING_HELPER` (or `none` without the
helper) and `DBUS_SESSION_BUS_ADDRESS=disabled:`, each left alone when
already set, and nothing else: the macOS supervisor also gives its daemon
`MALACHI_DEFAULT_COMPRESS_STORE=1` and
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS=30`, because a Mac often has a
small disk, while this one, as the GTK UI, sets no storage default (decided):
the daemon's own defaults apply (no compression, every attachment kept)
until the user changes them in *Preferences → General*, and such a variable
already in the app's environment reaches the daemon as it is. Being GTK's
behaviour, that is no row of the deviation table. The lines go to `logs\malachid.log` (rotated at 4 MiB, two
predecessors kept) and, when the app is attached to a terminal, to it. Before
every start the run directory is made private (`Paths.EnsureSocketDirectory`;
a directory named by `MALACHI_SOCKET` is only created when missing, never
changed). Stop: the three paths of Console below, under the process-wide
console lock, then `Kill` after 15 s (a kill is crash-safe: the store is
intact, the lock is released, the next daemon replaces the socket and key).
The app also stops its daemon on `WM_ENDSESSION`. The namespaces are
`Processes` and `Consoles`: one named `Process` or `Console` would hide
`System.Diagnostics.Process` or `System.Console` in every
`Malachi.Platform.Windows` namespace.

**Console** (measured in the spikes of §16, from PowerShell, Git Bash in a
pseudo console and mintty, directly and through make; replayed by
`ConsoleAttachmentTests` with the test binary as terminal and app). `Main`,
before anything touches `System.Console`, calls
`ConsoleAttachment.Initialize`, which clears `HANDLE_FLAG_INHERIT` on the
inherited standard handles and calls
`AttachConsole(ATTACH_PARENT_PROCESS)`. Started from a terminal
(`make run-windows`), the app is then attached: its log and
the daemon's reach the terminal, and Ctrl+C arrives in the app's
`SetConsoleCtrlHandler` routine, which quits gracefully. The daemon is
started with `CreateNewProcessGroup` (so the terminal's Ctrl+C spares it),
`CreateNoWindow` only when the app has no console (otherwise a console-less
parent gets a new terminal window), stdin closed, stdout and stderr pumped
to the log and, when attached, the terminal. Stop, under a process-wide
lock: if the daemon shares the app's console (`GetConsoleProcessList`), a
direct `GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid)`; if the app is
attached but the daemon is not, `FreeConsole`, `AttachConsole(pid)`,
CTRL_BREAK, `FreeConsole`, `AttachConsole(ATTACH_PARENT_PROCESS)`; if the app
has no console, `AttachConsole(pid)`, `SetConsoleCtrlHandler(NULL, TRUE)`,
CTRL_BREAK, `FreeConsole`; `Kill` after 15 s. On CTRL_CLOSE (the terminal
tab closes) the daemon stops by itself and the app does not restart it.
`build.ps1 run` starts the app with `Process.Start` (no redirection)
and waits for it in a loop, up to 20 s more in `finally` (INPUT-SPIKES.md
§4.3); never `& exe` (returns at once) or `& exe | …` (on Ctrl+C PowerShell
kills the app and orphans the daemon, and the inherited pipe keeps
PowerShell waiting). The app's handler (`Program.OnConsoleControl`) turns
Ctrl+C and Ctrl+Break into Quit on the UI thread, and CTRL_CLOSE (and the
never-delivered LOGOFF/SHUTDOWN) into a session end: `BeginStopping` at
once on the handler's thread, then the way out without drafts or
questions (`QuitSequence`, §10), the handler released by
`ShutdownCompleted` just before `Application.Exit`. The app's own log goes
to `logs\MalachiMail.log` and the terminal (`Shell/AppLog`, the level from
`MALACHI_LOG_LEVEL` as in `ui/main.go`).

## 6. The WebView2 security layer

Layer 2 of [security.md §3.2](security.md#32-defences)/§3.3, re-established
for WebView2. Everything in this section was **measured** on this machine
(WebView2 runtime 153), not taken from documentation; the spike report is
summarised in §16. What it established:

- `NavigationStarting.Cancel` **does not stop the request** (the GET, form
  data included, still reached a canary server);
- the CSP and a `WebResourceRequested` filter together still leak:
  `<link rel=preconnect>` opens TCP that the filter never sees,
  `<link rel=prerender>` sends a full GET past both, hover and mousedown
  over a link preconnect, dns-prefetch resolves;
- `--dns-prefetch-disable` does nothing; `--host-resolver-rules="MAP *
  ~NOTFOUND"` stops **everything**, IP literals included, and also
  WebView2's own calls (`config.edge.skype.com`) and SmartScreen;
- by default SmartScreen POSTs every clicked link to Microsoft, even one
  the host cancels (`IsReputationCheckingRequired=false` stops it);
- with `IsScriptEnabled=false`, host scripts run once but no listener,
  timer or host message ever fires; with script on and a CSP without
  `script-src`, page scripts, `on*` handlers and `javascript:` are
  blocked while an injected bridge works fully;
- `NavigateToString` refuses more than 1,572,834 UTF-8 bytes; a document
  served through a custom scheme with a CSP response header loads 5.4 MB
  in ~100 ms;
- `CustomSchemeRegistrations` must be **assigned** (the getter returns a
  copy; `Add` is a silent no-op), and `TreatAsSecure` is an `int` in the
  C# projection;
- WebView2 has no isolated script world: an injected bridge shares the
  page's world.

The code is `Malachi.App/WebViews` (`WebViewEnvironment`, `HardenedWebView`
and the four views over it: the viewer, a conversation card's, the editor
and the previewer) and its pure rules in `Malachi.Core.Presentation`
(`RequestGate`, `ResponseHeaders`, `NavigationPolicy`, `LinkProbe`,
`ContextMenuPolicy`, `RendererRecovery`, `HoverLabel`, `ViewerZoom`,
`CardSize`, `PreviewContent`, `PreviewDocument`, `PreviewPanel`,
`EditorKeys`), which have their tests. The views are built in code, not XAML, and use nothing
else of the app, so the network canary (§12) compiles the same files into
its host. A view initialises when it is first loaded into a window and is
closed with `Close()` when its window goes; `CoreWebViewInitialized` says
it has a control (again after a failed process replaced it), `Unavailable`
that it will show nothing.

### 6.1 One environment

One `CoreWebView2Environment` for the app (`WebViewEnvironment`), user data
folder `<data dir>\WebView2`, every `WEBVIEW2_*` variable of the process
cleared first (the loader appends `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` to
the app's; an administrator's WebView2 policy in the registry stays
authoritative):

- `AdditionalBrowserArguments = --host-resolver-rules="MAP * ~NOTFOUND"
  --proxy-server=127.0.0.1:1 --proxy-bypass-list=<-loopback>`: the resolver
  rule is the kill switch, the dead proxy an independent second barrier.
  Microsoft advises against flags in production; they are kept because
  they are the only way to meet "the view makes no connection", and a
  runtime that ignored them would still have the request gate below. The
  automated canary (§12) proves them on every runtime. The rule maps the
  proxy's own address too, so WebView2's background requests
  (`config.edge.skype.com` at once, and `edge.microsoft.com`'s component
  updater about a minute after the browser started) fail at the proxy's
  name, before any socket; the
  one socket the NetLog shows is Chromium's IPv6 reachability probe, a UDP
  connect to a Microsoft address that fails at once and sends nothing;
- `--disable-smooth-scrolling` as well: GTK's viewer turns WebKit's scroll
  animation off (`html_view.blp`: "wheel steps land at once"), and WebView2
  has no setting for it but this browser argument, which holds for every
  view of the environment, so the editor and the previewer scroll the same
  way (GTK's editor keeps WebKit's default; a listed deviation);
- `CustomSchemeRegistrations` assigned as a new list: `malachi-cid` and
  `cid` (secure, no authority) for pictures, `malachi-doc` (secure, with
  authority) for documents;
- `AreBrowserExtensionsEnabled=false`, `IsCustomCrashReportingEnabled=true`
  (renderer dumps can hold mail; they stay local),
  `AllowSingleSignOnUsingOSPrimaryAccount=false`,
  `ExclusiveUserDataFolderAccess=true`;
- the renderers' crash dumps do not stay either: the browser's Crashpad
  handler writes one per dead renderer into
  `<user data folder>\EBWebView\Crashpad\reports` (and `attachments`), a
  renderer's memory holds the message it showed or the draft being written,
  nothing uploads or ever deletes them, and a hostile body can crash a
  renderer once per document. Core's `CrashDumps.Sweep` empties both
  folders (never following a link or junction on the way, the database's
  own files left) before the environment is first created, off the UI
  thread, and `WebViewEnvironment.SweepCrashDumps` again at Quit and at the
  session's end; best effort, logged by count, an entry still in use stays
  for the next sweep (verified with planted reports: removed at exit and
  at the next start, before the environment);
- each view gets `IsInPrivateModeEnabled=true` with its own profile name
  (`viewer`, `editor`, `preview`) through
  `EnsureCoreWebView2Async(env, controllerOptions)`, checked on the profile
  afterwards;
- the shell calls `WebViewEnvironment.Start(<data dir>\WebView2)` at start
  (the first creation once took ~5 s); the first view creates it otherwise.
  A view waits for it; if the runtime is missing
  (`GetAvailableBrowserVersionString`), the environment cannot be created
  with these arguments, or any setting below cannot be applied (a runtime too
  old for it), the view loads nothing and raises `Unavailable`: the reader
  shows the plain text with the existing hint, the editor reports its
  failure, the previewer shows its panel. Fail closed. A failure is not
  remembered: the next document tries again. A document whose own
  navigation fails (`Navigate` throws, or `NavigationCompleted` reports an
  error that no process report explains within a second) raises
  `Unavailable` the same way.
- A failed process is handled as `RendererRecovery` (Core) says, once per
  document (a hash of the bytes it is served from): a dead renderer
  (`RenderProcessExited`) shows the document again in the same control; a
  dead browser process (`BrowserProcessExited`) retires the environment and
  shows it again in a new control; a hang (`RenderProcessUnresponsive`,
  which Chromium's hang monitor reports about 15 s after unanswered input
  and repeats only for more input, measured) is acted on only when the
  renderer has not answered a host script 5 s later, or is reported again,
  and then gets a new control, which ends the hung renderer. When the same
  document fails again, whoever loaded it again (the editor's window
  reloads its text after `Crashed`), the view gives up: it drops the
  document and raises `Unavailable` (the reader shows the plain text, the
  previewer its panel, the editor stays blank and keeps its text).
  Neither reference reloads by itself (GTK only logs, macOS reloads on the
  next `load(body:)`), and an unbounded reload would let a body that
  reliably kills the renderer (a Chromium or PDFium bug) loop for as long
  as it is shown, writing a crash dump of the mail each time and giving an
  exploit unlimited retries; a listed deviation. The canary's recovery run
  crashes each view's renderer (DevTools `Page.crash`) and hangs the
  viewer's (§12).
  Other kinds (a frame's renderer, the GPU and utility processes) are left
  to WebView2.
- Known limit of the recovery: Chromium starts the renderer of a document
  shown again after a crash at idle priority until the page commits. On a
  machine whose every core is busy, an idle process gets no CPU at all
  (measured while the other test assemblies ran), so that reload can stall
  until Chromium's commit timeout of 30 s, and the view then gives up as
  for a document that failed to load (the reader shows the plain text with
  its hint). The app leaves Chromium's priorities alone. The canary does
  not see it because its host puts itself, and so the browser and all its
  processes, into a job object that pins the priority class to normal
  (§12), which is what keeps its recovery run from stalling under the
  same load.

`WebViewEnvironment.LoggerFactory` is set by the shell; the views log kinds,
statuses and counts, never a URL or any other content.

### 6.2 The request gate (the content rule list of Windows)

Before the first navigation every view adds
`AddWebResourceRequestedFilter("*", All, SourceKinds.All)` and answers every
request, with a response of the requesting view's own environment
(`sender.Environment`; an unanswered request would go on to the network
stack), with what `RequestGate` decides: its current document once
(`malachi-doc://<view>/<generation>-<nonce>`, 128 random bits of nonce, the
bytes forgotten when served), with the CSP as a response header **and** as a
`<meta>`, `nosniff`, `no-store` and `no-referrer` (`ResponseHeaders`); its
own picture scheme only (the viewer `malachi-cid:`, the editor `cid:`, the
previewer none), each picture with a deferral and answered 404 once the
view's generation moved on (WebView2 cannot withdraw a request, as WebKit's
`stop` does); 403 for everything else, the document a second time and
`data:` included (which WebView2 does not route here; a runtime that did
would break pictures rather than open a rule). Every document has a fixed
title (`FixedTitle`): WinUI draws a WebView2 through a top-level
`Chrome_WidgetWin_1` window of the browser process over the control, titled
with the document's title (*Malachi Mail – [InPrivate]*, measured by the
canary), which other processes can read. The viewer's, editor's and
previewer's own documents carry it as their first `<title>`; a picture
served as the document would be titled with its URL and size, and a PDF
with its own `/Title`, which is content of the mail (measured), so
`DocumentTitleChanged` puts the fixed title back with a host script, and a
PDF is embedded in a page of the previewer's own (§6.6). The gate serves
that page's one embedded resource (`<document>/content`) once, after the
page, and the frame navigation to it is the one a view allows
(`NavigationPolicy.Frame`).

### 6.3 Viewer (`MessageWebView`)

`IsScriptEnabled=false`, `IsWebMessageEnabled=false`,
`AreHostObjectsAllowed=false`, `AreDefaultScriptDialogsEnabled=false`,
`AreDevToolsEnabled=false`, `IsStatusBarEnabled=false`,
`IsReputationCheckingRequired=false`, `AreBrowserAcceleratorKeysEnabled=false`,
autofill, password save, pinch, swipe, zoom control, the non-client region
and the error page off (every view has these, `HardenedWebView.Harden`);
`PreferredColorScheme=Light`, white background; a dark desktop leaves the
message on its white canvas, as GTK and macOS do. The document is the port
of `htmlview.Document` (`ViewerDocument`) with the GTK/macOS CSP
`default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'`.
`malachi-cid:` is the port of `PartSchemeHandler` (`parsePartPath`,
`message.part` through `PartFetcher`, which the reader sets with
`UseCache(MessageCache)`; images only, never SVG; a type that is not
`token/token`, a CR or LF in it included, is answered 404 rather than put
into the header block, `ResponseHeaders.Picture`, as the editor's `cid:`).
The same body is not reloaded (macOS `loadedBody`), except when its
pictures kept on the mail server were downloaded: the body asked for again
carries the same HTML, whose `malachi-cid:` URLs now have something to
serve, so `ReaderController.HtmlReloadRequested` has the view load it once
more (`Load(body, reload: true)`, macOS `picturesArrived`; GTK loads every
render). A picture `message.part` answers partNotDownloaded for (the daemon
let go of the copy it held under `neverStoreAttachments`) is answered 404
and handed to the cache (`MessageCache.FetchPartAsync`, remote.go
`pictureFailed`), which asks for the body once more so that its count
brings the pictures bar back. `Clear()` loads an empty one.

Hover: `StatusBarTextChanged` still fires with the status bar off; its text
is capped at 512 characters (`HoverLabel.Cap`, macOS) and shown in the
view's own label at the bottom left, cut in the middle to GTK's 80
characters (`HoverLabel.Display`), as plain text; `HoveredLink` and
`HoveredLinkChanged` expose it.

Links (`NavigationPolicy`, `LinkProbe`): `NavigationStarting` allows only
the pending document, once, not as a redirect; anything else is cancelled for
the view (the gate keeps its request off the network). A user-initiated
navigation (as GTK's `IsUserGesture`; WebView2 calls a click, a script
click and a host `Navigate` user-initiated and a meta refresh not,
measured) to an http, https or mailto target may be a link: a host script
(`ExecuteScriptAsync` runs with page script off) reads the focused element
through the prototypes' own accessors, which a named element of the page
cannot shadow, and the attribute of the link it is in is taken only when
that link resolves to exactly the navigation's URL. A form submit arrives as
a user navigation too, and the focus then is on its button, not a link;
a meta refresh is never probed, so a refresh to the link the focus stayed on
after an earlier, cancelled click is not taken for it: nothing is handed
on. `NewWindowRequested` (middle, Ctrl, Shift click, `target=_blank`)
opens nothing; a user's request is a link activation as in GTK, confirmed
with the URL alone when the focus did not follow and no form control made
it. The result is `LinkActivated(ActivatedLink)`, which the reader decides
with `LinkDecision.For` (§6.4) and opens, confirms through its alerts or
composes. Downloads, external schemes, frames, permissions, authentication,
client certificates, certificate errors, screen capture and Save As are
refused. The context menu keeps Copy and Copy Link (`ContextMenuPolicy`;
separators only between kept groups; every item read in `try`, `Handled`
set in `finally`, a menu that could not be reduced is not shown). Text zoom
(the `text-zoom` setting, `Zoom`) is CSS `zoom` on the document's root, set
as the document is served and by a host script when the setting changes
(`ViewerZoom`); the WinUI control has no `ZoomFactor`. A failed process
shows the body again once (§6.1); the same body failing again raises
`Unavailable`, and the reader shows the plain text. One view per pane,
reused. Automation name *Message*.

### 6.4 Links

The port of macOS's `ActivatedLink`/`linkDecision`: `mailto:` opens the
composer; a masked link (the text shows another destination) asks *Open
This Link?*; a link the daemon did not list in `links[]` is **confirmed**
too (decided; macOS behaviour), because WebView2 hands out normalised URLs
and an exact match against the daemon's raw hrefs can fail. The attribute
read from the page decides, as on macOS, unless it leads somewhere else
than the navigation; without it (`ActivatedLink.Raw` null) the resolved URL
is compared with Chromium's canonical form of every listed href
(`ChromiumUrl`, computed only where certain, null otherwise), and in a body
that carries a masked link every such click is confirmed, since a listed
href whose canonical form is not certain could be the one clicked. The research
found that the GTK check is likely bypassable with a non-canonical href;
that is a separate GTK/backend task (§14), not part of this port.

The security audit of the finished client found that bypass, with shapes
of its own: an href that spells the bank in a userinfo Go's `net/url`
refuses (a space, `%`, `[`, `^`, `|`, `{`, `"`, a soft hyphen, `。`:
`https:// www.bank.example@evil.example/`) or leaves the authority empty
(`https:///evil.example/`) has no host for GTK's `Masked`, which then
opens without a question, while Chromium and `System.Uri` read it and go
to the host after the `@`. The Windows check is therefore stricter than
GTK's (a row of the deviation table): under a text that names a host,
`Links.IsMasked` **fails closed** where Go cannot tell the href's host,
finds none, or finds userinfo at all; and a listed link opens without the
question only when the address the launcher hands the browser
(`ILauncher.LinkTarget`) has a host on the site the text names
(`Links.LeadsElsewhere`), so the decision judges what is opened, not how
one parser reads the href (`LinkDecision.For` takes the launcher's
function). That address never carries userinfo (`Launcher.WebLinkTarget`
drops it: a mail link never needs credentials, and the browser would hide
them anyway), so *The link is shown as “%s” but leads to %s.* names the
host the browser goes to first, where GTK names the href as written. A
link the launcher refuses (the backslash and empty-authority shapes) is
decided as masked and then not offered, as there is nothing it could
open; the click shows GTK's toast for a link that could not be opened
(*The link could not be opened: %s*, with macOS's detail `invalid URL`)
instead of doing nothing visible, and a refused link under a plain text
never reaches the launcher either. `LinkDecisionTests`, `LinkOpenerTests`
and, over the real launcher, `LauncherTests` hold the audit's shapes; the
network canary (§12) clicks each in the real viewer and checks that it
reaches the reader with its attribute as written and resolves to the host
after the `@` (the backslash shape to the bank: Chromium ends the
authority there), the input the decision is tested with.

An adversarial review of that fix found two more ways past the question.
One href listed under two texts: `For(string)` judged the first listed
link with the href, so an empty anchor before the one that wears the
bank's address, both on evil.example, opened it. Both paths now share one
rule (`LinkDecision.Judge`): every listed link the activation matches is
judged, the first whose text misleads is quoted, and the link opens only
when none does; the canary clicks the second of such a pair and checks
that its report carries exactly the first one's attribute. And the text
side failed open: GTK's `hostOfText` reads no host, so the link opens,
for texts a reader takes for the bank's address (a space the daemon puts
around an inline element, `https://www.moje banka .example/login`; a soft
hyphen, U+200B, U+2060 or U+FEFF in the host; a trailing dot; a Cyrillic
homoglyph; an RLO, NBSP or `。` before the path; a backslash, fullwidth
solidus or `%2F` as the separator; `//www.mojebanka.example/login`), and
reads evil.example for `https://www.mojebanka.example@evil.example/login`.
`Links.HostsOfText` reads the daemon's text of the link cleaned and
normalised: format characters (Cf:
the soft hyphen, the zero-width characters, the bidi controls) and the
other default-ignorable code points go, the rest is read in NFKC with
`。` as a dot, hosts are compared as the launcher hands them to the
browser (IDNA, punycode, lower case) and without a trailing dot; a text
that reads as an address (a scheme with its colon and a slash, http and
https without one, two slashes, `www.`) whose host cannot be read (a
space inside it, userinfo, an escape, a port that is no number, no host
name) names a host no link leads to, so it asks, with the text quoted as
the mail wrote it; and every address in the text counts, one after words
(`Log in at https://…`) or after a hidden first one included. `SameSite`
no longer takes a single-label host for the site of the hosts under it
(`www.mojebanka.cz` over `https://cz/` asks); a multi-label public suffix
(`co.uk`) is still a parent site, as there is no public-suffix list.
Plain words and empty texts name no host and open as before; `mailto:`
stays exempt. `HtmlLinksTests` holds every shape and the ordinary texts
that must not ask (a bare host over its own https URL, a path, upper
case, an IDN text over its punycode href, a subdomain href). GTK
(`htmlview.Masked`, `linkTextFor`) and macOS (`Links.swift`,
`linkDecision`) keep these bypasses until they get the same rules or the
backend's canonical hrefs.

A second review of those rules found more. Two slashes after a letter or
a mark began no address, so a colon another script draws as a letter or a
mark (U+02D0, which the scheme took in before its colon test saw it,
U+02D1, U+A4FD, U+0903, U+0A83) or no colon at all (`https//mojebanka.example/login`)
named no host, and the link to evil.example opened. Two slashes now begin
an address wherever they stand, and the scheme ends at a colon lookalike
before a letter could take it in (the list gained the other four and the
Syriac, runic and Ethiopic colons); that also closes a scheme address that
markup draws right to left (`<bdo dir=rtl>`, `unicode-bidi:
bidi-override`), whose listed text `nigol/elpmaxe.aknabejom//:sptth` has
`:sptth` after its slashes, a host that cannot be read. The price is that
`Tips//Tricks` and a bare `shop.example//sale` ask. A text that begins
with an address and goes on in words asked over its own link, since a
space in such a text made the host unreadable (`www.shop.example for
details`, `https://www.shop.example Shop now`, `www.shop.example Logo`
with the alt the daemon appends); now a space there ends the host unless
the host visibly goes on after it (`HostGoesOn`: the next word begins with
a dot, has one before its first slash or ends with one, or the word before
the space ends with one; a dot between digits or after another dot, as in
a date or an ellipsis, is no host's), which keeps `https://www.moje banka
.example/login`, `https://mojebanka .example` and a hidden first address
before `mojebanka.example/login` asking, and costs `www. shop .example`
and `www. shop.example` (from `www.<b>shop</b>.example` and
`<span>www.</span>shop.example`) a question over their own link. A start
of an address the daemon's spaces split (`<b>w</b>ww.`, `https<b>:/</b>/`,
listed as `w ww.` and `https :/ /`) is read as drawn, without them
(`SpacedPrefix`; of every text with one or two such spaces in the bank's
address, 842 of 2956 opened over evil.example before, none now). A text
that holds no address is read whole as a host, as in GTK, now from its
first letter or digit (a mark before it is no part of it) and past a word
and a colon (`Login:mojebanka.example/login`); a word with a dot another
script draws between its labels (U+A4F8, U+06D4, U+0701, U+0702, U+A60E,
not the middle dots of Catalan and Japanese) or more than one dot at its
end (`mojebanka.example…`) names a host that cannot be read, and `_` is
taken in a host's labels as browsers take it (`https://shop_name.example/`
over itself no longer asks). With the attribute, the decision now
also judges every listed link whose canonical form is the navigation's,
as it did without the attribute: hrefs that differ only in case, a default
port or escaping (`HTTPS://EVIL.EXAMPLE/dup` beside `https://evil.example/dup`)
lead to one address, and the attribute may be another anchor's.

A third review found that the `HostGoesOn` refinement had dropped a next
word that is a dot alone: `https://www.halifax.co<span>.</span>uk/login`,
listed as `https://www.halifax.co . uk/login` and drawn as the bank's
.co.uk, read as `www.halifax.co` and opened over a link there, which had
asked before. A next word that begins with a dot, or with a dot another
script draws, now continues the host whatever follows the dot, unless the
dot begins an ellipsis. Split slashes after a letter or a mark
(`httpsঃ<b>/</b>/`, listed as `httpsঃ/ /`, with a Bengali visarga for
the colon; a `<bdo>` address whose slashes are split, `…/ / :sptth`) are
read as the two glued slashes are; the double and triple solidus
operators (U+2AFD, U+2AFB) count as two and three slashes, U+1735 and
U+31D3 as slashes; a one-word text takes a colon another script draws
before its host (`Web∶mojebanka.example`); and two dots in a row end a
host as they end a sentence (`www.shop.example… Shop now` over its own
link opens).

What the client judges is the daemon's `links[].text`, not what the view
draws, and some shapes stay beyond it. They open without the question,
as known limits (`HtmlLinksTests.TheKnownLimitsStillOpen` holds them):
text that CSS hides or clips inside the link (`<span
style="font-size:0">x</span>mojebanka.example/login`, a `text-indent` that
clips `https://evil.example/xxx` off `https://evil.example/xxxhttps://mojebanka.example/login`,
hidden padding that pushes the shown address past the daemon's 200-rune
cap); a bare host with a path that markup draws right to left
(`nigol/elpmaxe.aknabejom`, shown as `mojebanka.example/login`; without a
path the reversed host still reads as one and asks); a host an inline
element splits right after a host of the link's own site
(`https://evil.example<b>moje</b>banka.example`, drawn as
`https://evil.examplemojebanka.example`, listed as `https://evil.example
moje banka.example`), which reads as that first host, since the space
cannot be told from one before words; a host whose last label an inline
element splits (`https://www.natwest.co<span>m</span>/login`, listed as
`https://www.natwest.co m /login` and drawn as the bank's .com over a link
to `www.natwest.co`), which reads as `www.natwest.co` for the same reason
(it asked before the rule that lets `www.shop.example for details` open,
and no rule found tells the two apart without asking over newsletters:
`HtmlLinksTests.AHostSplitInItsLastLabelStillOpens`); a one-word text whose
labels a middle or raised dot parts (U+00B7, U+0660, U+0F0B, U+10FB,
U+1427, U+16EB, U+2E31, U+2E33, U+A92F, U+ABEB), left out of the dot
lookalikes for Catalan `l·l`, the Japanese `・` between words and the
scripts that write such dots between syllables; and a host without a scheme or
`www.` after words (`Log in at mojebanka.example`, `https://evil.example
→ mojebanka.example`), which GTK does not read either and whose reading
would ask over every file name and abbreviation in prose. An underscore
host over itself (`https://shop_name.example/`) opens with the viewer's
attribute but asks without it (a middle click the page reports only by
its resolved URL), since the port of Chromium's canonicalisation refuses
`_` in a host: that fails closed. Reading on inside a path for a further address
would not help: a clipped prefix ends with `/` or `?` as easily, and such
an address is also what archive and redirect links show
(`https://web.archive.org/web/2020/https://example.com/`), which would ask.
Closing them needs the daemon (proposed, §14): a link text without a space
between inline elements that touch; a flag when it cut the text at its
cap, or the last address of the text kept rather than the first 200 runes;
the text of nodes that CSS hides (`display:none`, `visibility:hidden`,
`font-size:0`, a transparent colour, `text-indent` or overflow clipping)
left out or flagged; and a `<bdo>` or a `direction`/`unicode-bidi`
override inside a link reported, or the link's text given in the order it
is drawn. GTK's `hostOfText` and macOS's `Links.swift` also open the
text shapes this section closes: the colon lookalikes and the missing
colon, the reversed scheme address, the split prefix, the lookalike dots
and extra dots, `Login:` or a mark before a bare host. GTK matches the
URL WebKit resolved (`action.Request().URI()`) against the listed hrefs
and takes the first equal one, so of anchors whose hrefs differ only in
case or escaping the one written as WebKit resolves it speaks for all;
macOS reads the clicked anchor's own attribute, which avoids that shape
but not the one href listed twice.

### 6.5 Editor (`ComposeWebView`)

`IsScriptEnabled=true` (required: with script off no listener fires),
`IsWebMessageEnabled=true`, `AreHostObjectsAllowed=false`, DevTools,
dialogs and browser keys off. The document is `EditorDocument`, served from
`malachi-doc://editor/…` with the CSP `default-src 'none'; style-src
'unsafe-inline'; img-src cid: data:` as header and meta, so every page
script, handler and `javascript:` URL in pasted or quoted HTML is blocked
(the canary loads its hostile document and the corpus with scripts into
it). The bridge (`EditorBridge.Script`) is the GTK/macOS bridge with a
`chrome.webview.postMessage` channel, injected with
`AddScriptToExecuteOnDocumentCreatedAsync` before the first navigation,
guarded by `window.top` and the document URL, using
`Document.prototype`/`EventTarget.prototype` accessors captured at document
start (a pasted `<img name="body">` clobbers `document.body` otherwise;
verified in Chromium), and, for the assistant's rewrite, the `Node.prototype`
getters `parentNode`, `previousSibling`, `nodeType` and `childNodes`: its
walk up and back from the attribution's `div` may meet a `<form>`, whose
named controls override its own properties as named elements do the
document's (reasoned from the HTML specification, not measured). `WebMessageReceived` accepts only strings whose
`Source` is the current document and whose shape parses; they go to Core's
`EditorChannel` (`Channel`: `Ready`, `Changed`, `StateChanged`,
`KeyPressed`, `Html`, `Text`, the rewrite's passage from its
`rewrite` message, and `PasteRequested` for GTK's `paste` of plain text
that looks like Markdown, which the compose window answers with
`Pasted(id, html)` after `draft.markdown`, or null for the text as it is). Host to page is `ExecuteScriptAsync`:
`Flush(done)` (the flush script, its `seq` handed back to the channel),
`Exec(command, argument)`, `FocusStart()`, and the assistant's rewrite
(`RewriteTarget(attribution, done)`, whose passage comes back as GTK's
posted `rewrite` message rather than as the script's value, so the bridge
keeps GTK's shape; `ApplyRewrite(text, below)`, one step the page's undo
takes back). A rewrite never hangs: a bridge that does not run, a new
document, a dead renderer and a failed evaluation answer with the empty
passage. `cid:` is the port of
`CIDSchemeHandler` (only ids in the window's `CidRegistry`, a registered
file read off the UI thread when it is a regular file within the cap, a
fetcher bounded by `FetchTimeout`, then `checkInline`). File drops go
through the bridge (`postMessageWithAdditionalObjects` →
`CoreWebView2File.Path` → `FilesDropped`, which the compose window hands to
`attachment.import`); the page never sees them. WinUI's WebView2 hands a
drop from the shell to its host instead (measured), where the compose
window's editor slot takes it (§11.3). Every navigation but the
pending document is cancelled, links of a quoted original included; new
windows are refused. The context menu keeps Undo, Redo, Cut, Copy, Paste,
Paste as plain text and Select All (`ContextMenuPolicy`; GTK and macOS have
none, a listed deviation). A dead renderer, and a document that could not
be loaded, raise `Crashed` (the latter once until a document loads, as
macOS's `reportedUnavailable`): the compose window shows its toast and
loads `Html` again. That reload is the document's one (§6.1): when the same
text fails again the view stays blank, raises only `Unavailable` and keeps
`Html` for a save, so the window's reload cannot loop.

Flushes use macOS's sequence numbers **and** an order-independent echo rule:
WebView2 delivers the changed message before the `ExecuteScriptAsync`
result (15 of 15 trials), which on macOS very likely leaves drafts dirty
after every save. Saving on Quit (§0) flushes outside a save, which GTK and
macOS never do, so the echo gets two baselines: a flush when the editor
becomes ready records how the page serialises the loaded body
(`ComposeDraftController.EditorReady`), and Quit's flush records content
that comes back unchanged; its waits are bounded, so a hung renderer makes
Quit ask rather than wait. The bridge is a third copy beside
`ui/internal/editor/bridge.go` and the Swift one; a test compares it with
the Go copy modulo the documented deltas.

The compose window wires the view as GTK wires `editor.Editor`: its
`IComposeForm` answers `EditorHtml`/`EditorText`/`FlushEditor` with `Html`,
`Text` and `Flush`; `Channel.Ready` and `Channel.Changed` go to the draft
controller's `EditorReady` and `EditorChanged`, `Channel.StateChanged` to the
format bar, `Channel.KeyPressed` (`escape`, `link`) to the window's Escape
and Insert Link, `FilesDropped` to `attachment.import`, `Crashed` to the
editor-failure toast and a `Load(Html)`. Keys (§11.5): while the editor has
focus every key passes the window's pre-translate handler before Chromium;
the router lets the keys of `EditorKeys.BridgeHandles` through (Ctrl+B, I,
U with or without Shift, Ctrl+K, Escape: the bridge formats, prevents
Ctrl+Shift+I's Tab and posts Ctrl+K and Escape back) and acts on and
swallows its own (Ctrl+Enter, Ctrl+S, Ctrl+W, Ctrl+Q, …); everything else
reaches the page as typing.

### 6.6 Previewer

The replacement for Quick Look and Sushi (decided): the content of a
reusable preview window (`PreviewWebView`; the window, its title and *Open*
/ *Save As…* are the reader's) with its own hardened view (script off,
`preview` profile, the gate, downloads cancelled, the PDF toolbar's Save,
Save As, Print, Full screen and More settings hidden). The bytes from
`message.part` are served from memory as `malachi-doc://preview/…` with the
type `PreviewContent` sniffed: pictures by their signature (PNG, JPEG, GIF,
WebP, BMP, ICO, AVIF; never by the claim alone, never SVG), PDF by `%PDF-`
within the first KiB (shown by WebView2's viewer in an `<embed>` of a page
of the previewer's own, `PreviewDocument.PdfPage`, whose CSP admits exactly
that one URL as object and frame: `'self'` does not match a custom scheme's
origin; served as the document, the PDF's `/Title` would name the window),
text in the previewer's own escaped document
(`PreviewDocument.Text`, a `<pre>` under `default-src 'none'`), which is
also how HTML, SVG, XML and messages (`.eml`) are shown: as their source.
The claimed charset is honoured, a byte-order mark wins, UTF-8 when valid,
Windows-1252 otherwise; bytes with a NUL are not text. Nothing is written
to disk. Links in a PDF or a text are not followed (every navigation is
cancelled, as in the editor). Other types, everything the platform would
run (`DangerousTypes`, and `FileTypePolicy` when the reader sets
`TypePolicy`), and everything when the view is unavailable (an attachment
whose renderer, PDFium's included, died again after it was shown again
once, §6.1) get a panel
(`PreviewPanel`): the icon Windows has for the extension, the name, the
size and the type name. `ShellFileTypes` asks by the extension alone
(`AssocQueryString` for the name, `SHGetFileInfo` with
`SHGFI_USEFILEATTRIBUTES` and the system image list for a 256-pixel icon,
which also knows packaged apps' icons), so no file exists and no icon handler
reads an attachment; `SHGetFileInfo` is the one hand-written P/Invoke of
the client, because CsWin32 generates it only for a specific architecture
(its structure is packed on 32-bit Windows, which the app does not ship
for). Shell preview handlers are not hosted: third-party handlers run
in-process-adjacent code over hostile files, and Windows itself stopped
previewing internet files in Explorer in October 2025 because previews
leaked NTLM hashes.

The window is `Malachi.App/Windows/PreviewWindow` with
`Attachments/AttachmentPreview`: one window, made on the first click on a
chip, its content swapped by the next while it is open, gone when closed.
Nothing changes on screen while the part is fetched (`message.part`, through
`AttachmentOpener.PreviewAsync`, after `message.download` for a part kept on
the mail server, while the chips show their spinner); a failure is GTK's
toast where the chip was. A program's bytes are not even fetched: its panel
shows what the chip lists. The title bar carries the attachment's name and
*Open* (disabled for a program) and *Save As…*, which act as the chip's
menu does on the part the daemon served (a download on Microsoft 365 may
renumber it); Escape and Ctrl+W close it (`WindowKind.Other`), as Escape
closes Sushi and Quick Look.

### 6.7 Conversation card (`CardWebView`)

The port of GTK's `htmlview.Card` (`card.go`, `size.go`) and the card half
of macOS's `MessageWebView`: the HTML body of one card of the conversation
view (§11.7), in a view as tall as its document, stacked with the other
cards in one scrolling column. It is a viewer (`WebViewKind.Viewer`: the
`viewer` profile, the gate, `malachi-cid:` through `PartFetcher`, links
through `LinkProbe` and `LinkDecision`, the reduced context menu, the hover
text for the pane's one label, the text zoom as CSS), and everything of
§6.3 holds, page script off included. Its document is
`ViewerDocument.CompactDocument`, GTK's `CompactDocument` (the column's
padding cut to the card's; drift-tested against `htmlview`); never one
document of the whole conversation, where one message's CSS could reach
the headers of another ([security.md §3.2](security.md#32-defences)).

The one difference from GTK's card is how the height is known. GTK's card
runs a measuring script of the application in an isolated world of the
page, with a `ResizeObserver` and every picture's load reporting the
height. WebView2 has no isolated world, and with page script off no
listener of an injected script fires (measured, §6 above), so the host
measures instead: `CardSize.Script`, run with `ExecuteScriptAsync` (host
scripts run with page script off) 40 ms after the last of the causes that
may change the height: the document's load (its `NavigationCompleted`,
when the pictures it asked for were answered), a picture served after it,
a change of the view's width or of the text zoom, and a change of its
height alone (a report within 100 ms of that says so, as `sizeScript`'s
viewport flag). The script reads where the column ends, overflow included,
only through the prototypes' own accessors, so a named element of the
message cannot stand in for `getElementById` or a size, and changes
nothing; its height is in CSS pixels of the viewport, the zoom included,
capped at `CardSize.MaxReported`. A report for a document the view no
longer shows (a view handed from one card to another, a newer document) is
dropped. The card's `WebHeightGovernor` (GTK's `webHeightGovernor`) sizes
the view to it up to 4000 px, beyond which the card scrolls inside, and
freezes a document that grows with every step the view grows (three
reports in a row caused by the view's own growth) until its document,
width or zoom changes.

Views are pooled by the pane (`ConversationLayout.LiveCards`: the cards
within two screens of the viewport, at most eight web views, two kept idle;
`Reset` drops the callbacks and the document of a view handed on, so a
report of the old document never reaches the next card, and `Release`
closes it). The wheel over a card whose document fits goes on to the
column (GTK's `forwardScroll`; the XAML sees the wheel over a WebView2,
visual hosting, and the card forwards it with `handledEventsToo`). A
failed renderer shows the body again once (§6.1); a card whose view gives
up shows the plain text. The canary runs a card view as well: the hostile
document, its links and hover, a document of known height measured at two
zooms, and the recovery of its renderer (§12).

## 7. Concurrency

### 7.1 Controllers are UI-thread-affine

The controllers are `@MainActor` classes on macOS; on Windows they run on
the UI thread's `DispatcherQueueSynchronizationContext` (a single-thread
context in tests), so continuations after `await` come back to it and the
generation counters of the Go UI (`listGen`, `bodyGen`, `foldersGen`,
`ConnectionController.generation`), the op counters and the `closed` flags
work without locks, as they do there. No `ConfigureAwait(false)` in
controllers; `VerifyAccess` in debug builds.

### 7.2 The trap of a literal port

A Swift `Task { }` always defers; a C# `async` method runs synchronously up
to its first incomplete `await`. Ported literally, a transport that fails
fast leaves `ConnectionController.reconnectNow`'s attempt handle set for
ever and `MessageCache.fetch` drops its waiters (reproduced). Every
fire-and-forget path therefore goes through one `Perform` helper that
starts with `await Task.Yield()`, registers the task with a pending-work
tracker, passes the controller's lifetime token and drops the result when
the controller is closed. Tests wait on `IdleAsync()` (the tracker, the
fake daemon's in-flight count and the drained UI queue), never on sleeps.

In the code (`Malachi.Core/Controllers/Infrastructure`): `ControllerScope`
holds a controller's (or a group's, the mailbox with its halves) thread
(`ThreadAffinity`, `VerifyAccess` and a present `SynchronizationContext`
checked in debug builds), `IsClosed`, `Lifetime` token and `PendingWork`;
`Perform` hands an `Outcome<T>` (Swift's `Result`) to the UI thread, `Run`
starts tracked work, `RunDetached` loops and waits on the clock (a tracked
wait on a `FakeTimeProvider` would hold `IdleAsync` for ever), all
yield-first. The tests' side is `Fixtures/Quiescence.IdleAsync`,
`TestUIContext`, `FakeDaemon` and `MailFixture`.

`Close` goes one step further than Swift's `closed` flag: it cancels
`Lifetime`, so a call that is not written yet is never sent, even one that
`Perform` started in the same UI turn right before the close (its work has
not run). That suits reads. A mutation fired on the way out, which Swift
and GTK send all the same (ComposeDraftController's discard: `draft.delete`,
then the window closes; `discardNow`'s `attachment.remove`), goes through
`PerformPastClose`: tracked and yield-first too, but its call does not pass
`Lifetime`, and its outcome is dropped once the scope is closed. Work
started by `Run` or `RunDetached` catches its routine failures itself, as
Swift's `connectOnce` turns a failed dial into a state; what escapes is a
bug, logged at error level and kept (the last 64) for the tests.

### 7.3 Time

Every timer takes a `TimeProvider`: the mark-read delay, the 30 s autosave,
the 30 s sync fallback, the 60 s status refresh, the 5 s reconnect, the 5 s
disk-space refresh of the preferences, the 300/150/400 ms debounces. Tests
use `FakeTimeProvider.Advance`.

### 7.4 What moves out of the view layer

macOS keeps some logic in AppKit, untested. On Windows it lives in Core as
presentation classes with tests (a structural difference only, the
behaviour is macOS's): `NotificationHub`, `SignInRepair`,
`ReaderController` (showMessage with `bodyGen`, the 400 ms spinner),
`AccountsPageController`, `ComposeAttachmentsController`,
`SuggestionsController`, `Debouncer`, `FlushEcho`, `AvatarPalette`
(`g_str_hash % 14 + 1`, initials), `AttachmentOpener`, `LinkOpener`,
`NotificationPolicy`, `MessageWindowRegistry`. The reader's come with
`AddressHeader` (the From/To/Cc lines and their fold), `AttachmentChip`,
`ChipText` (the chips' ellipses by characters) and `MessageActionRouter`
(macOS `MessageActionsController`: the selection's and one message's
commands). What these classes show of a message goes through
`Text.DisplayText` (§3): `Format.DisplayName`, `FormatAddress` and
`FormatParticipants` clean and isolate names,
`LoadedMessageText.SubjectText` cleans the subject for the list, the
reader, the captions and the questions, `NotificationText` both lines
of a toast, and `FolderTree.FolderTitle` a server's name for a folder,
which `ListHeading.Caption`, the status line and a search result's origin
isolate. What is sent to the daemon stays as received: the quote
headers of `Prefill` take `Format.NameAsReceived` and `AddressAsReceived`,
GTK's forms, and Copy Address copies the address itself. The rules of the
WebView2 layer, which macOS keeps in its web views, live there too
(`Malachi.Core.Presentation`, §6).

The shell's are in `Malachi.Core/Presentation`: the
`NotificationHub` above; `ActivationRequest` and `CommandLine` (what a
launch or a redirected second launch asks, §10); `WindowLifetime` (the
GApplication rule for the one main window); `QuitSequence` (Quit, §10);
`ShortcutMap` with `KeyChord`, `ShortcutCommand`, `ShortcutContext` and
`WindowKind` (the key map of §11.5); `ToastPresenter` (Adw.ToastOverlay's
queue on the `TimeProvider`); `DialogScheduler` (one `ContentDialog` at a
time per window); `Mnemonic` (`mn()` plus the access key); `IconGlyphs`
(GTK icon names to Segoe Fluent Icons glyphs, each checked against the
font; a test finds every icon name of `ui/` in the table).

The main window's: `AvatarPalette` and `AvatarColours`
(above); `PaneLayout` with `PaneMode` and `PaneWidths` (window.blp's
breakpoints and ranges, where the folded layouts navigate, when the widths
are kept); `SidebarRow` and `SidebarRowKind` (a sidebar row of folders.go
and favourites.go); `MessageRow` and `RowAppearance` (a list row of
message_row.go); `StatusPopover` and `StatusPopoverRow` (the flyout of
status.go). The rows are the view models the `ListView`s keep (§7.5).

### 7.5 Exposure to XAML

Observable state as `INotifyPropertyChanged` properties (CommunityToolkit.Mvvm
source generators, private setters), imperative outputs as events 1:1 with
the Swift `onX` callbacks, and lists as snapshots applied to
`ObservableCollection`s by a keyed diff (`KeyedListSync`) so a `ListView`
keeps its containers and scroll. WinUI and UWP have been reported to turn
a `Move` into a removal and an insertion, which may deselect the moved row:
a list controller re-applies its selection by key after a sync that moved
entries (the main window's lists select their key after every apply, with
their own handler suppressed; §11.2). `{x:Bind}` only, and never on a
property that code sets too: a one-time binding runs when the control
loads, after its constructor, and puts its value back over what code set
there (a message window's star showed the empty star over a flagged
message; the compose bar's alignment glyph is set in code only for the
same reason). Dialogs are async hooks (`IAlerts`), never a `ContentDialog`
created by a controller.

A Swift callback cannot throw; a C# handler can, and inside a controller
its exception would leave a state change half done or end a loop that
lives as long as the controller (the only reader of the client's states,
the reconnect loop, the status line's redraw). Controllers therefore raise
their events through `ControllerEvents.Raise` (each handler in its own
`try`, as `RpcClient.Raise` does for the transport) and guard the steps
of those loops and their `PropertyChanged` notifications with
`ControllerEvents.Guard`; what a handler throws is reported by the
`PendingWork` (logged at error level, and failing the tests' `IdleAsync`).

How the lists reach the view, as the mailbox does it: `MailboxController` publishes the
sidebar as `Entries` (keyed by `SidebarKey.Of`) with the highlighted row as
`SelectedEntryKey`, and its list half `ListController` publishes `Rows`
(keyed by `ListRow.Key`) with `SelectedKey`. The key is the source of
truth, and it is current whenever a snapshot is announced:

- The rows arrive with `RowsChanged` (and its `SelectionHint`) and, in flat
  mode, with `RowsRefreshed` (a flag changed: mark-as-read, about a second
  after every selection); the sidebar with `EntriesChanged` and
  `BadgesChanged` (a count moved: every mark-as-read in the selected
  folder). The sidebar's highlight moves on its own with `SelectionChanged`.
- A changed row (a flag, a badge, a conversation's counts) is a new record
  under the same key. The view keeps row view models and applies every
  snapshot with the view overload of `KeyedListSync.Apply`
  (`create`/`update`), which updates them in place. The record overload
  would `Replace` the row, and a WinUI selector treats that as a removal
  plus an insertion, dropping the selection. It is only for lists without
  a selection.
- After every apply the view selects `SelectedKey` or `SelectedEntryKey`
  where its `ListView`'s selection differs (a moved row may have lost it),
  with its own selection handler suppressed while it applies and selects.
  A deselection the collection caused must never go back as `Select(null)`,
  which would clear the reader. A click goes back as
  `SelectFolder`/`Select`.

The paging of the list and the search box's pause, which macOS keeps in
AppKit, are in Core (`MailboxController.Paging.cs`, `SearchFieldChanged`):
the view reports its layout through `ViewportChanged` after every change
of the rows' extent or the pane's size and on every scroll, and shows
*Load More* only while `LoadMoreRetry` says so. The footer
(`LoadMoreState`) follows the model under a status page too, so the
emptied list of another listing never asks for the previous listing's
next page.

## 8. Settings

`Malachi.Core/Settings` is the typed facade with the gschema's keys,
defaults and ranges (`data/io.github.schotek.Malachi.gschema.xml`): numeric
keys clamped (`mark-read-delay` 0–60, `text-zoom` 50–200), bad enum strings
fall back to the default, an unchanged write fires nothing, handlers fire
synchronously on the UI thread in registration order (the
`savingCollapse`/`savingFavourites` echo guards depend on it), lists are
copies. The backend is `HKCU\Software\io.github.schotek.Malachi` (DWORD for
`b`/`i`, REG_SZ for enums, REG_MULTI_SZ for `as`) with a
`RegNotifyChangeKeyValue` watcher that diffs and raises per-key handlers,
so a `reg add` reaches the running app as `gsettings set` and `defaults
write` do; tests use an in-memory backend. `MALACHI_SETTINGS_KEY` names
another key under `HKCU\Software` for a test that runs the real app (the
UI smoke tests, §12, give each app they start
`io.github.schotek.Malachi.UiTests.<guid>` and delete it afterwards): it is
read with the other paths (`Paths.Resolve`, beside `MALACHI_DATA_DIR` and
`MALACHI_SOCKET`) and taken only when it is `io.github.schotek.Malachi`
itself or that name followed by a dot and ASCII letters, digits, `.`, `_`
or `-`, compared exactly. Any other value is ignored with a warning in the
log and the preferences stay in the app's own key, so a mistyped variable
can never point the app's writes at another program's key or a parent of
one; nothing else moves with it (the Run value, the `mailto:` registration
and the notification registration are `MALACHI_DATA_DIR`'s and §10's).

Window geometry uses the gschema keys: `window-width`, `window-height`
and `window-maximized` as GTK, which binds them to the main window's
default size and maximized state (`ui/internal/window/geometry.go`), so the
size is the one the window restores to, also while it is maximised (here
the placement's normal rectangle, `GetWindowPlacement`), written when the
window closes or hides and at Quit where GTK writes every change; and
`folder-pane-width` and `message-list-width`, which GTK declares but never
writes (a row of the deviation table). One Windows-only key: `ctrl-r`
(`reply` default, `refresh`), the counterpart of macOS's `command-r`.
`launch-at-login` only mirrors the Run key and `StartupApproved`, which are
authoritative. As in GTK, only presentation lives here; mail handling is
the daemon's (`config.get`/`config.set`).

## 9. Localisation

`po/` is the single source of truth. The build copies `po/<lang>.po` for
every entry of `po/LINGUAS` to `locale\` beside the executable; Core parses
them at start with po2strings' semantics (header, obsolete, fuzzy and
untranslated entries skipped; plurals only when complete; context keyed
`ctxt\u0004msgid`; `nplurals` checked against the plural table; a
translation whose printf directives do not match its msgid is dropped at
load). `L10n.T/N/C` as on macOS; a Go-printf formatter (`%[N$][flags][width]
[.prec]s|d|f|%`, invariant culture) that never throws; the plural table
ported from `PluralRules.swift`; the strftime msgids of `widget/format.go`
mapped to .NET custom formats at run time (a single-letter .NET pattern is
a standard format and ICU-style `''` drops the apostrophe: both handled and
tested). XAML uses a markup extension `{l:T Msgid, Context=…}`; GTK `_`
mnemonics become `AccessKey`s. The language follows the Windows display
language (matched by base language, English always available);
`MALACHI_LOCALE_DIR` points elsewhere for development. `.resw` is not used.
Windows-only strings stay English (`// Windows-only string`, `<!--
Windows-only string -->`); nothing enters `po/POTFILES`.

The strings check (`Malachi.Conventions.Tests/Strings`, the counterpart of
`macos/scripts/check-strings.py`): Roslyn (`Microsoft.CodeAnalysis.CSharp`)
over `windows/src/**/*.cs` and an XML reader over `windows/src/**/*.xaml`
require every literal msgid of `L10n.T/N/C` and `{l:T}` to be in
`po/malachi.pot` with its context and plural pair (a call marked
`// Windows-only string` may miss, as check-strings.py accepts
"macOS-only string"), and fail on a msgid built at run time. They warn,
as a skipped test that lists them, about literals in WinUI text sinks
(`Text`, `Content`, `Header`, `Title`, `PlaceholderText`, `Label`,
`ToolTipService.ToolTip`, `AutomationProperties.Name`, the text inside a
`TextBlock`, …) and about literals formatted into a translated sentence,
unless marked Windows-only: in C# a comment on the line or right above the
statement (or initializer entry), or `// Windows-only strings` earlier in
the same block; in XAML a comment right before the element or an
enclosing one. A coverage test requires every msgid of `po/malachi.pot`
to be a string literal of `windows/src` (or a `{l:T}` msgid) or listed
with a reason in `windows/parity-exclusions.txt` (one msgid per line with
C escapes and `\004` between a context and its msgid, a tab, the reason;
seeded from research 05 Appendix A: GNOME, GOA, portal, Flatpak, gsound,
gschema and desktop metadata), and every exclusion to be a template msgid
the sources do not use (a stale or a now used exclusion fails). The
coverage holds for the whole template: every msgid is used or excluded.
A gap (a msgid that GTK work and `make po` added, say) is a failure:
`CoverageEnforced` in `StringsCheckTests` is `true` now that every screen
exists (set back to `false`, a gap is a skip that lists the missing
msgids, and `MALACHI_MSGID_COVERAGE=strict` still fails it). Another test
compares the gschema's keys and defaults with the settings facade.

## 10. Platform services

**Keyring helper** (`malachi-credentials.exe`): the protocol of
`backend/internal/auth/helper` (argv `get|set|delete`, one JSON line on
stdin of at most 1 MiB, identifiers `[A-Za-z0-9._-]{1,128}`, `{"value":…}`
on stdout for `get`, exit 0 found/done, 2 not found, 1/3 as the Go side
defines, stderr capped and never carrying a value). `WinExe` subsystem (a
console window would flash on every call) and NativeAOT (one process per
operation). Generic credentials, `CRED_PERSIST_LOCAL_MACHINE` (secrets do
not roam), target `io.github.schotek.Malachi/<accountId>/<key>`, comment
`Malachi Mail: <accountId> (<key>)`, UTF-8 blobs. Every value `get` hands
over matches a SHA-256 the helper wrote with it; a missing chunk or any
mismatch is a corrupt item (`keyringError`), never a wrong token. An item
that holds the value itself carries the SHA-256 of its blob in the
attribute `Malachi_SHA256`. `cmdkey` and the Credential Manager dialogs
store a password as UTF-16LE without it (and an edit that keeps it no
longer matches), so such an item is corrupt, not a wrong password: unlike
an edit in Keychain Access, an edit there is not honoured, and the account
is signed in again in the app. A value above 2560 bytes is split into
chunks `<target>#<number>` written first and the main item last, whose
header (`0xFF`, which no UTF-8 value begins with, the version, the slot,
`n`, the length and the SHA-256) stands in for the value. The chunks
alternate between two slots, `#1..#16` and `#17..#32`: `set` writes the
slot the current header does not name, then the header, then removes
every other chunk (found with `CredEnumerateW`), so a `set` that fails or
is killed leaves the previous value readable, as `SecItemUpdate` does, and
nothing for good; `delete` removes them all. A value may take at most 16
chunks (40 KiB) and its `get` answer at most the 64 KiB the daemon keeps;
a longer one is refused. Credential Manager loses updates when several
processes use it at once, even when all but one only read (measured: a
write that reads back as missing, a deleted item that returns), while the
calls of one process are consistent; the daemon may run several helpers at
once, so every run holds the session's named mutex
`Local\io.github.schotek.Malachi.credentials` (10 s wait) around its store
operation. Another program using Credential Manager at the same moment can
still make a `set` or `delete` go missing; the item then reads as its
previous value or as corrupt, never as a mix. The value stays bytes and
never becomes a string, so every buffer that held it is zeroed. The Go
side's `MALACHI_TEST_REAL_HELPER` round trip runs against it.

**Notifications and sound.** Measured on 2.5.1 (§16): in a
self-contained unpackaged app **no package set** makes
`AppNotificationManager.Register()` work; the metapackage, the WinUI
packages and WinUI plus the Runtime package all throw `0x8007007E`
(`Microsoft.WindowsAppRuntime.Insights.Resource.dll` missing; Register
writes its registry entries first, so toasts show but clicks are lost).
The fix is a build target that unpacks that one DLL (34 KB, same version)
from the Runtime package's framework MSIX
(`tools\MSIX\win10-<arch>\Microsoft.WindowsAppRuntime.2.msix`, fetched with
a `PackageDownload`) into the output (`MalachiInsightsResource` in
`Malachi.App.csproj`; `build.ps1 app` checks the DLL is in the app
folder); it goes once a Foundation release
with WindowsAppSDK PR #6725 ships, and `Register()` is re-tested on every
WinAppSDK bump (the published app, run outside Claude Desktop's process
tree, registered in 70 ms and unregistered cleanly). The
`NotificationInvoked` handler is attached **before**
`Register()` (otherwise COM registers single-use and every click starts a
new process); `Register("Malachi Mail", <icon>)` with no explicit AUMID;
`Unregister()` on exit (a later click still cold-starts the app). A click
while running raises `NotificationInvoked`; a cold click starts the app
with `----AppNotificationActivated: -Embedding`, which the argument parser
ignores, and arrives through `GetActivatedEventArgs()` as kind
`AppNotification`. Either way the toast's arguments (`account`,
`message`) open the message in its own window
(`ActivationRequest.FromNotification`, `Integration.OpenNotifiedMessage`,
GTK `app.open-message` in `window/notify_open.go`): the summary from the
list or the cache, else `message.get` (`MessageCache.LookUp`), marked read
as a double-click does; a click before the connection waits for it, one
that started the app shows the main window too, and a message the daemon
no longer has shows the main window instead. As `notify.go`: nothing while the main window is
the active window (`AppState.IsMainWindowActive`: shown and holding the
activation, false while another window of the app or another application
has it, as GTK's `IsActive()`; Core's `WindowActivation` keeps it apart
from the last active window, which gets the toasts); title the sender's
display name or *New message*, body the subject or *(No subject)*, both
capped at 200 bytes; group = account,
tag = `message-<id>`; a click shows the main window. The sound is its own
switch: `PlaySound("MailBeep", SND_ALIAS|SND_ASYNC|SND_NODEFAULT|SND_SYSTEM)`,
the user's system sound for mail (*Desktop Mail Notification* in Control
Panel → Sound; closer to GTK's `message-new-email` than macOS's *Glass*),
silent when the user set none, in the volume mixer's *System Sounds*
session (muting system sounds mutes it, as GTK's event sound follows the
event role), and skipped while Windows asks for quiet; toasts are muted.
In the code, the rules are Core's (`Malachi.Core.Presentation`):
`NotificationHub`, the port of the macOS hub (every daemon notification
decoded and handed to the handlers of its kind, a handler's failure
reported instead of stopping the others), and `NotificationPolicy` over
`IDesktopNotifier` and `INewMailSound`; `DesktopNotification` cuts a tag
or group longer than the 64 characters a toast allows to its start and a
hash. `Malachi.Platform.Windows` has `Notifications/` (`NotificationArguments`,
the account and message a click carries back; `QuietHours`: Do Not
Disturb, that is `ToastNotificationManager.GetDefault().NotificationMode`
other than `Unrestricted`, whether switched on by hand, on a schedule or by
its rules for games and full-screen apps, and the
`SHQueryUserNotificationState` answers for a presentation, a full-screen
program or Direct3D game, the screen saver, a locked or another user's
session, and quiet time, the first hour after a new user's first sign-in;
`QUNS_APP`, a Store app in front, is not quiet, and neither is what Windows
cannot say) and `Sound/NewMailSound`; the app's
`Platform/NotificationService` is the only code over `AppNotificationManager`.
Verified outside Claude's process tree: the toast shows the name and icon
(`Assets\notification.png`, rendered by `make-icons.ps1` with the `.ico`),
a sender's `<…>` and a subject's markup as plain text, and a click reaches
the running app on its UI thread and cold-starts an exited one (kind
`AppNotification`) with the ids intact, `;`, `=` and `%` included.
Notifications fail open: CsWinRT turns a failed HRESULT into several
exception types, and whatever `IsSupported`, `Register`, `Show` or
`Unregister` throws is logged, so `InitializeEarly` and `Stop` never throw
(verified: without the Insights DLL `Register` throws a `COMException`
0x8007007E, with an icon Windows cannot read a `FileNotFoundException`
0x80070002; the app starts either way).

Outdated notifications are withdrawn, as in GTK and macOS (notify.go
`withdrawNotifications`, 2026-09-28): a message that arrives read shows
nothing, and `NotificationPolicy.Deliver` says whether it asked for a
toast, which `PlatformServices` hands to the mailbox
(`PlatformContext.NotificationShown`,
`MailboxController.RecordNotification`). The mailbox keeps the last 50
notified messages with their folder (`NotifiedMessages`, the port of
`notified.go`; the oldest pushed out is withdrawn) and withdraws them when
the actions read, trash, archive or junk them (on the daemon's success),
after each sync pass of the account when `message.get` says the message
was read, left its folder or is gone (one check per account at a time, an
answer that tells nothing ends it until the next pass), for the selected
folder when the main window becomes active or a folder is selected in it,
and for accounts removed or paused. `PlatformServices.WithdrawNotifications`
turns the messages into their tags (`DesktopNotification.TagOf`, the same
as the toast's) and `NotificationService.Withdraw` removes each with
`AppNotificationManager.RemoveByTagAsync`; a tag without a toast is no
error. The main window's activation is handled after the window tracker
took it (`Integration.WireNotifications`, through the UI queue), so the
window counts as active.

**Background, tray, launch at login.** `DispatcherShutdownMode.OnExplicitShutdown`;
one main window for the process, hidden on close when *Run in background*
is on (otherwise the last visible window quits, the GApplication rule). While
hidden, a notification-area icon offers Open, New Message, Check for New
Mail and Quit (`Malachi.Platform.Windows` `Tray/`, the app's
`Platform/BackgroundTray`): `Shell_NotifyIcon` on a hidden top-level
`WS_EX_TOOLWINDOW` window (a message-only window misses the
`TaskbarCreated` broadcast), `NOTIFYICON_VERSION_4`, the icon taken from the
exe with `SHDefExtractIcon` at the small-icon size of the taskbar's DPI
(`ExtractIconEx` and `LoadIconMetric` use the process's system DPI, which
can differ from the taskbar's once the user changed the scale without
signing out, and `LoadIconMetric` needs the Common Controls 6 manifest), re-added unconditionally, at the
new taskbar's DPI, on `TaskbarCreated`, and a native
`TrackPopupMenuEx` menu (a WinUI `MenuFlyout` opened from the tray lands
behind other windows, gets no keyboard and shows nothing while the owner is
hidden; measured). CsWin32 refuses `Shell_NotifyIcon` and its structures in
an Any CPU library (PInvoke005: x86 packs them differently), so
`Tray/NotifyIconInterop` declares their 64-bit layouts, checked against
the SDK's sizes. The labels are GTK's msgids where GTK has the action
(`_New Message` with its mnemonic as the access key, `Check for New Mail`);
*Open Malachi Mail* and *Quit* are Windows-only strings. The commands are
queued to the UI thread, so they run after the menu has returned. New icons
land in the Windows 11 overflow. Every path
that shows the main window (tray, notification, redirected launch,
background start) calls `AppWindow.Show()`, `Activate()` and then
`SetForegroundWindow(hwnd)`: without the last, the window stays behind
(measured with `ForegroundLockTimeout` at its maximum); WinAppSDK's
`RedirectActivationToAsync` already grants the foreground right. Shown
again after `AppWindow.Hide()`, the window came back with the keyboard on
its caption's input window instead of the XAML island, so no key worked
until a click, and its first show after `--background` put WinUI's first
focus in the search box (measured in phase F); every show therefore gives
the island the keyboard (`InputFocusController.TrySetFocus`, XAML restoring
the element that had it) and falls back to the sidebar's first tab stop
(`MainWindow.TakeKeyboard`). A `--background` start shows the icon at
once, since the window it never shows raises no visibility change. Launch at
login is the Run value with `--background`, which starts hidden; a
`StartupApproved\Run` value whose first byte is odd means the user disabled
it in Windows Settings, which is shown as such, never overwritten
(`Startup/LaunchAtLogin`: turning it on then writes the Run value and
answers `RequiresApproval`, which Preferences reports with GTK's *Autostart
was not granted* and can follow with Settings → Apps → Startup). The
`launch-at-login` key only mirrors the status, at start and whenever
Preferences opens; a Run value naming an executable that no longer exists
(the app folder moved) is pointed at the running one at start. In
Preferences → General the row reads the status whenever the page comes up
or its window is activated; where the user turned the entry off in Windows
Settings it says so under the row (*Turned off in Windows Settings*,
Windows-only) with a *Startup apps* link, and turning it on in the app
writes the value and answers *Autostart was not granted*, the switch
going back. Verified outside Claude's process tree: the Run value written
and removed, the mirror key following, the disabled state and its toast;
the values the test wrote were removed afterwards.

**Single instance and activation.** A custom `Main`
(`DISABLE_XAML_GENERATED_MAIN`, `Malachi.App/Program.cs`): the console
(§5), the log, the notifications' hook (`PlatformServices.InitializeEarly`:
the handler, then `Register()`), then
`AppInstance.FindOrRegisterForKey("io.github.schotek.Malachi")`; a second
launch redirects with `RedirectActivationToAsync` (which grants the first
instance the foreground right itself, so no `AllowSetForegroundWindow`),
pumping COM with `CoWaitForMultipleObjects` meanwhile, and exits. The wait
has no time limit, as in the Windows App SDK's own pattern: a busy first
instance takes the activation once it is free, but aborts inside the
Windows App SDK when it gets to a redirect whose launch has already ended
(measured with a suspended first instance), so giving up after a timeout
would crash the app the user already has. The wait also ends when the
first instance's process ends, which leaves a redirect pending forever
(measured); that launch then exits with code 1. Its lines go to the same
app log, each written at the file's end (`RotatingLogFile`), so the first
instance's later lines do not overwrite them. The
first instance hands every activation, its own and each redirect's
(`AppInstance.Activated`, a worker thread), to `App.Activate` on the UI
thread (`ActivationRequest`, Core). An unpackaged launch carries its whole
command line (split as the C runtime splits it). Kinds: launch (shows the
window), `mailto:` (the composer only, as GTK; a cold `mailto:` shows no
main window, and one that no compose window can take yet brings the main
window up instead; only the activation's first link opens one, the others
are logged by count with the unknown arguments, since the ProgID passes
one link and a caller that splits a quoted link into several arguments
would otherwise open a window, with its WebView2 editor, for every
piece), a notification (its message's window; the main window too on a
start), `--background` (nothing;
the first launch holds the app without a window, GTK's service hold),
`----AppNotificationActivated:` and `-Embedding` ignored, anything else
logged by count and ignored.

**Lifecycle and Quit** (`App.xaml.cs`, `Shell/`). One main window for the
process, made at start and shown unless the activation says otherwise;
`AppWindow.Closing` is always cancelled and the window hides; with *Run in
Background* off, and once no other window is open and no background hold
is left, that quits (`WindowLifetime`, the GApplication rule; the other
windows count from `WindowTracker.Track` until they close). Quit
(`QuitSequence`): `ComposeController.SaveForQuitAsync`, then the close
question of each window it returned (`IComposeWindowHandle.CloseForQuitAsync`,
an additive member whose default keeps the window, so a window that could
not save and asks nothing never loses its draft to a Quit); a Cancel
abandons the Quit. Then the point of no return (the supervisor starts
nothing any more, the window geometry is kept, `PlatformServices.Stop`,
every window hides and no activation shows one again, the open directory
is emptied), `ConnectionController.StopAsync` (the daemon this app
started is stopped, never one it adopted), `AppState.Dispose`
(controllers, the open directory once more, settings), the WebView2 crash
dumps removed (§6.1), `Application.Exit`. `WM_ENDSESSION` (a subclass
of the main window's procedure) empties the open directory, stops the
daemon before it returns, empties the open directory again and removes the
crash dumps, and then quits as a session end. The open directory goes
before the daemon's stop, which may take 15 s, as GTK's shutdown and the
macOS app remove it first: a quit that never completes leaves nothing
behind. The platform services get their
moments through `Malachi.App/Platform/PlatformServices` (`InitializeEarly`,
`Start`, `NewMessage` before the list, `MainWindowVisibilityChanged`,
`Stop`, `Shutdown`).

**`mailto:` and the default mail app.** The app writes its HKCU
registration at start when it is missing or stale (the app folder can
move), so it appears in *Settings → Apps → Default apps*; Windows does not
let an app make itself the default. The *Default apps* button in
Preferences (decided) opens `ms-settings:defaultapps?registeredAppUser=Malachi%20Mail`.
The ProgID carries `Application\ApplicationName` = *Malachi Mail* (without
it Windows lists the exe name), and the registration ends with
`SHChangeNotify(SHCNE_ASSOCCHANGED)` (verified with
`SHAssocEnumHandlersForProtocolByApplication`, which lists *Malachi Mail*
after the registration and no more after `Unregister`).
Links are parsed by the port of `compose.ParseMailto`. The code:
`Malachi.Platform.Windows` `Registration/` (`MailtoRegistration`, rewritten
when any value is missing or differs, `Unregister` for an uninstaller,
`IsDefault` through `AssocQueryString`; `SystemSettings` for the two
Settings pages).

**The app's side** (`Malachi.App/Platform/PlatformServices`), called by the
shell in this order: `InitializeEarly(onActivated)` first in `Main` (the
notification handler, then `Register`), `NotificationActivationFrom` on
`GetActivatedEventArgs()` for a cold start by a click (and on a redirected
activation), `Start(PlatformContext)` on the UI thread once the
`NotificationHub` is attached to the connection and before the mailbox adds
its own `notify.newMessage` handler (GTK notifies first, then updates the
list), `SetRunningInBackground(bool)` whenever the main window is hidden in
the background or shown again, and `Stop()` on the way out. A click arrives
on the UI thread (held until `Start` when it comes earlier). Preferences
takes `LaunchAtLogin`, `Mailto` and `OpenDefaultApps` from it.

**Attachments.** Opened files go to the open directory (a fresh random
subdirectory per file, `FileMode.CreateNew`; the directory emptied at
start, before the daemon's stop at Quit and once more at the end, whatever
the preferences say; `OpenDir.RemoveAll` removes nothing but a fully
qualified `<data directory>\open` that is no device path and not directly
under the root of a drive or share, and logs a refusal, the counterpart of
GTK's `purgeOpenDir` and macOS's `removeAll`, which also name the parent,
`malachi` or `Malachi Mail`: here the parent is the data directory,
which `MALACHI_DATA_DIR` may name) under a Windows-safe name
(reserved characters and their best-fit look-alikes: those of code page
1252 and every look-alike of `"` always, those of this machine's ANSI code
page as well, so no `"` comes back to split an ANSI program's command
line; device names including `COM¹`-style superscripts, trailing dots and
spaces, `:` streams, path length, a cut to length never leaving an
extension the name did not have). Every file written out
of a message, opened or saved, gets the Mark of the Web through
`IAttachmentExecute` (`SetClientGuid`, `SetLocalPath`, `SetFileName`,
`Save()` on an STA thread; also the AV scan and policy), the counterpart of
the macOS quarantine attribute; the file is judged by its own name, the
path's last component. If the zone cannot be read back, the file is
not opened (a saved file stays the user's), unless an administrator turned
zone information off (`SaveZoneInformation=1`), whose choice that is. The
zone is Microsoft's e-mail client guidance: no `SetSource`, so Restricted
sites (`ZoneId=4`, measured). That zone's policy blocks what
`AssocIsDangerous` names, and `Save()` then deletes the file (measured), so
a saved file of such a type gets `SetSource("about:internet")` instead
(`ZoneId=3`, Chromium's choice): it stays, scanned, and running it goes
through SmartScreen and the security prompt. Where Attachment Services
cannot be created (the class is missing), the stream is written directly
and the file opens as it would without the service; where `Save()` fails,
the stream is written directly too, and a file for opening stays shut: a
verdict (antivirus, policy) or a check that failed without one never
opens. `IAttachmentExecute` is CsWin32's built-in COM interop, which a
trimmed publish would have to replace with its source-generated COM.
Never opened, only saved: the
GTK list, the macOS additions, Outlook's Level-1 list, `.rdp`,
`.appinstaller`, `.msix`, `.ppkg`, `.searchconnector-ms` and friends, anything
`AssocIsDangerous` or `CheckPolicy` flags, disk images (`.iso`, `.img`,
`.vhd`, `.vhdx`: mounting them has been a Mark-of-the-Web bypass), OneNote's
`.one` and `.onepkg` (a file embedded in a page runs when the picture laid
over it is double-clicked, past one warning, which delivered malware in
2023), Windows Contacts' `.contact` and `.wab` (a click on a crafted
contact's link can run a program, a report Microsoft declined to service;
an address book is imported by the same `wab.exe`), and Access's
formats since 2007 (`.accdb`, `.accde`, `.accdr`, `.accda`, `.accdu`,
`.accdt`, `.accdc`, and the web app reference `.accdw`, whose web apps are
retired but which is still registered to Access). Outlook's list and the
shell name the Jet-era Access types (`.mdb`, `.mde`, `.mda`, `.mdt`,
`.mdz`, `.ade`, `.adp`), the database itself included, but none of their
successors, which run the same VBA and macros when they open, so the
successors are listed too; none of these is flagged by `AssocIsDangerous`,
even with Office installed (measured). A
vCard (`.vcf`) still opens: it is an everyday attachment, and GTK and macOS
open it. Opening
anything else uses `ShellExecuteEx` (through `Process.Start` on an STA
thread, zone checks on, the shell's dialogs owned by the window) and *Open
With…* `SHOpenWithDialog` (this once, never the default); only files on a
local drive, never a share (nor a drive mapped to one), a link or a
stream. A link of a message goes to the browser escaped, with its host
as DNS gets it and without userinfo (`ILauncher.LinkTarget`), which is
what the reader judges its text against and what its confirmation shows
(§6.4). No exception of these services names the path of a file written
out of a message, which carries the attachment's name. `attachment.import` is only
ever given local paths the user picked. The code: `Malachi.Core.Platform`
(`DangerousTypes`, `WindowsFileNames`, `OpenDir`, the interfaces) and
`Malachi.Platform.Windows` `Attachments/`, `Files/`, `Launch/`. The flows
are Core's `AttachmentOpener` (§7.4): Open refuses a program with GTK's
toast (*Programs and scripts are not opened directly; save the file and
decide yourself.*), judged on what the message lists, again on the name and
type the daemon served and on the name the file got, writes into the open
directory, marks (`AttachmentUse.Open`) and opens only when
`ZoneMark.MayOpen`; Save As and Save All write where the user chose, never
overwriting in Save All (" (2)"), mark (`AttachmentUse.Save`), and count a
file the check removed as not saved. A part kept on the mail server
(`Attachment.remote`, or any part of a body not downloaded yet) comes
through `IReaderCache.PartDataAsync` (download.go `partData`): the message
is downloaded first (`message.download`, one call per message however many
actions ask, `MessageCache.DownloadAsync`), the part is looked up again in
the message the download answered with (`AttachmentChips.PartAfterDownload`:
the same id while it is the same file, else the only part with its name and
type, else *The attachment no longer exists* and nothing fetched under the
old id), and a part the chip showed stored that the daemon answers
partNotDownloaded for gets one download and one retry, never a loop
(`Download.WithDownloadAsync`). Save All downloads once, after the folder is
chosen, when any of its parts is on the server; a failed download writes
nothing and says why (*Saving the attachments failed: …*). Save All leaves
out what is never opened (a listed deviation): Explorer parses a `.url`,
`.lnk`, `.scf`, `.library-ms` or `.searchConnector-ms` file for its icon
and location as soon as the folder is shown, whatever its mark, and has
sent the user's NTLM hash to another host that way (CVE-2025-24054). It
is judged on what the message lists (such a part is not fetched), on the
name and type the daemon served, and on the free name the file would get;
after GTK's
summary of what it tried, a second, Windows-only toast says how many it
left out and that Save As saves one (*N attachments were not saved; save
programs and scripts with Save As…*), and when nothing else is left no
folder is asked for. Save As of a single file is the explicit way and
stays as it is. The pickers are the Windows App SDK's
`FileSavePicker` and `FolderPicker`, owned by the window of the click
(`Malachi.App/Attachments/AttachmentPickers`), titled *Save Attachment* and
*Save Attachments*, starting in Downloads; the save picker offers the
attachment's own extension as its file type.

**Sign-in.** The daemon owns the `127.0.0.1` listener; the app opens the
`https` URL through the launcher (`ILauncher.OpenUrlAsync`: `ShellExecuteEx`,
https only), nothing else. The account wizard's
browser page shows `WizardController`'s `OAuthView` (the prompt with the
provider's button, the wait with *Open the Browser Again* and *Cancel*, and
*No Sign-In Client Configured* with *Use an App Password Instead*); the
controller calls `account.oauthStart` and `account.oauthWait` and hands the
address to the launcher with the wizard's window as owner. Closing the
wizard (its close button, Escape, Ctrl+W) closes the controller, which
cancels a session the browser still has (`account.oauthCancel`). Verified
with a Google address and no client configured (the unavailable page and
the app-password path); a Microsoft address's prompt was not pressed,
since it opens the user's browser.

**MCP registration** (Preferences → AI): `malachi-mcp.exe status|install|
uninstall --json` beside the app, 15 s timeout, output capped (1 MiB per
stream, both drained at once), the process tree killed on timeout, the
drains given up 500 ms after the exit (`Malachi.Core.Platform.BridgeRunner`;
a kill reads as status -1, a crash as its NTSTATUS, `ExitStatus.Describe`;
started at the `SpawnGate` of §5, so it never inherits the handles the
daemon's start makes inheritable);
the MSIX Claude Desktop's configuration path and the app folder's bridge
path are passed with the new flags (§14) on every call: `--command` with
the bridge's canonical path (absolute, short names expanded, each component
as the file system spells it, so another spelling of the app folder does
not read as someone else's registration), `--claude-desktop-config` while
`%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc` exists
(`ClaudeDesktopPackage`). A missing bridge and a failed status go into the
group description as in GTK (a later status that answers puts the page's
own text back); a failed install or uninstall is a toast. A status ends
with the page; an install or uninstall runs to its end
(`McpRegistrationController`). The bridge starts its reason with its own
name (`malachi-mcp: no Claude app found …`), which is skipped before the
comparison with *no Claude app found*: GTK and macOS compare the whole
line and never show their sentence for it (a fix there is separate). The
AI page asks for the status whenever it comes up; verified
against the bundled bridge with `USERPROFILE`, `APPDATA` and
`LOCALAPPDATA` in a temporary folder (registered in both files, removed
again, and the toast with neither Claude app).

**The Assistant** (`ui/internal/assistant`; [mcp.md](mcp.md), *Hand-off
from the app* and *The panel in the app*; [security.md §10.1](security.md#101-the-assistant)),
built on `feat/assistant-menu` (2026-09-29) as a port of the macOS client.
Core has the Go package's pure half as `Malachi.Core.Assistants` (the
static class `Assistant` in partial files per Swift file: texts, prompts,
links, the command line, the child's environment, the stream-json events
read as Go reads JSON, the Markdown subset, the rewrite's and the search's
prompts and answers; byte offsets into UTF-8 and Go's character classes as
in Swift's port; the namespace is plural because a namespace `Assistant`
would hide the class from every other `Malachi.Core` namespace) and the
controllers of Swift: `AssistantController` (the link handlers are the
shell's default ProgIDs of `claude` and `claude-cli`, `AssocQueryString`;
the bridge's registration its own status), `ClaudeCodeLocator`,
`ClaudeCodeProcess`, `AssistantPanelController`, `AssistantRequest`,
`ComposeRewriteController`, `SearchConversion` and `ClaudeDesktopController`,
with the Swift and Go tests against a stand-in `claude.exe`
(`tests/Malachi.FakeClaude`, scripted by a JSON file beside it: turns,
stderr, exits, a process that never ends). What is Windows' own:

- **Claude Code is `claude.exe` only** (decided): the native installer's
  `%USERPROFILE%\.local\bin\claude.exe`, then every directory of the
  `PATH`, after the path of *Preferences → AI*; npm's `claude.cmd` would
  run through `cmd.exe`, whose parsing of a command line cannot carry the
  JSON arguments (`--mcp-config`, `--json-schema`) safely. Paths are
  cleaned in pure string code (drive-absolute only; UNC, relative and
  forbidden characters refused).
- **The process** (`ClaudeCodeProcess`): `Process` at the `SpawnGate`,
  no window, the three pipes as raw bytes, the environment of
  `Assistant.ChildEnv` (what a Windows program needs to start, matched
  without case, and a `PATH` of claude's folder and the system's; no
  `CLAUDE*`, `ANTHROPIC*` or `MALACHI_*`), the working directory
  `%LOCALAPPDATA%\Malachi Mail\assistant` made private by
  `PrivateDirectory`. There is no SIGTERM: ending a conversation closes
  stdin (`claude -p` ends at the end of its input) and kills the process
  tree, the bridge Claude Code started included, 2 s later; the end is
  reported once, after every event. Measured with Claude Code 2.1.72 on
  this machine: the command line's flags exist and `system/init` comes in
  that environment (the machine's own login had expired, so no answer came:
  `claude auth status` still said `loggedIn: true`, and Claude Code retried
  the API eleven times before it said "Failed to authenticate. API Error:
  401"; 2.1.285 asks for a new token first, clears the dead one and says
  "Not logged in" at once).
- **The sign-in** (`ClaudeCodeLocator.SignInAsync`): Claude Code's sign-in
  is its own, apart from Claude Desktop's (`%USERPROFILE%\.claude\.credentials.json`),
  so a user of Claude Desktop alone has none. *Sign In…* runs `claude.exe
  auth login` through the locator's runner with `Assistant.ChildEnvironment`
  in the private directory and waits up to ten minutes: Claude Code opens
  the browser through the shell and stores the sign-in itself (measured
  with 2.1.285: it waits with its stdin closed, prints the address to open
  by hand, which is neither shown nor logged, and honours `BROWSER`, which
  the child's environment does not carry). Out of time, cancelled (*Stop*,
  a second sign-in, Quit) it is killed with its process tree. One sign-in
  at a time for the panel and *Preferences → AI*, which both follow
  `SigningInChanged`.
- **Claude Desktop** is the MSIX package `Claude_pzs8sxrjxfjjc`
  (`Malachi.Platform.Windows.Claude.ClaudeDesktopApp`): it runs while a
  `claude.exe` of that package family runs (`GetPackageFamilyName`; the
  name alone would match Claude Code, and the package's `RuntimeBroker`
  outlives it). Closing its window only hides it in the notification area,
  so the restart asks the root `claude.exe` of each instance to quit the
  way Windows does at sign-out (the Restart Manager's shutdown without
  force: `WM_QUERYENDSESSION` and `WM_ENDSESSION`), waits up to 45 s for
  the processes (measured with GitHub Desktop, another Electron app: gone
  after 20 s, and once only after `RmShutdown` had given up at its own
  30 s), and starts it again by its application user model id
  (`IApplicationActivationManager`). While a change is pending the app
  waits for its processes to go and then writes the change once more
  (macOS hears of the termination from `NSWorkspace`). The quit was never
  run against Claude Desktop in development: this port was written in a
  Claude Code session that runs inside Claude Desktop.
- **The links** go through `ILauncher.OpenAssistantLinkAsync`, which takes
  only the three forms `Assistant.Link` and `FileLink` build (the
  characters `encodeURIComponent` keeps and the query's separators), at
  most 32 000 characters (Windows' limit of a command line, which the
  handler's must fit). An attachment for Claude is written as Open writes
  it (`AttachmentOpener.WriteForHandOffAsync`: the open directory, the Mark
  of the Web, never a program).

## 11. UI

### 11.1 Main window

`ExtendsContentIntoTitleBar` with the WinUI `TitleBar` on Mica: icon and
*Malachi Mail*, the search box in the middle (Ctrl+F/Ctrl+E), and the back
and pane buttons in narrow layouts. Below it the three panes of
`window.blp` in a grid with two splitters (sidebar 200–320, list 280–460,
message ≥ 300; widths in the gschema keys, written only from a wide,
uncollapsed layout). At 900 effective pixels or less the sidebar becomes an
overlay; at 600 or less list and message form one stack with Back (GTK). Command
rows per pane: sidebar (New Message, the primary menu `…`: New Message, Add
Account…, Preferences, About, Quit), list (folder title and counts, Check
for New Mail, the All/Unread/Flagged `SelectorBar`, hidden while
searching), message (Reply, Reply All, Forward; Archive, Trash; Star, ✦,
More). The status line runs across the bottom edge (26 px, spinner,
connection glyph, caption) and opens the per-account flyout. 1200×760,
minimum 360×294. The caption is *Folder – Malachi Mail*, the folder's name
cleaned and isolated (`ListHeading.Caption`, §3). The `TitleBar` is
set with `SetTitleBar`, `PreferredHeightOption=Tall`; the colour scheme sets
`RequestedTheme` on every window root **and**
`AppWindow.TitleBar.PreferredTheme` (the caption buttons ignore
`RequestedTheme`; measured), and a root without Mica gets
`ApplicationPageBackgroundThemeBrush`. The All/Unread/Flagged filter is the
in-box `SelectorBar` (the toolkit's `Segmented` items lack the UIA selection
pattern).

The window itself (`MainWindow.xaml`): the title bar, the search
box (`MainWindow.Search`, focused by the Search command), the size and the
maximised state in the gschema keys (the minimum through
`WM_GETMINMAXINFO`). Every other window is tracked by
`WindowTracker.Track(window, kind, root, toasts)` right after it is made:
the colour scheme on its root and caption buttons, its `WindowCommands`
and `CommandRouter`, its toast overlay as the router's target while it is
active, and its part in the app's life. A window without Mica gives its
root `Background="{ThemeResource ApplicationPageBackgroundThemeBrush}"`.

The panes (`MainWindow.xaml`, `MainWindow.Panes.cs`, `Malachi.App/Main`)
are the window's own controls, wired to the Integration's controllers in
`MainWindow.Attach`; the reader's region (`MainWindow.Reader`) sits under
the message page's header bar and the
toast overlay. outer_split is a `SplitView`: the sidebar (`SidebarPane`) is
its pane, inline while the window is wider than 900 effective pixels, an
overlay (opaque) opened by the title bar's pane button at 900 or less
(window.blp's `max-width` conditions, so 900 itself folds), closed by a
folder chosen with a click or Enter (not by the arrow keys, which move the
selection), a click outside or Escape. inner_split is the grid's two
columns: at 600 or less they are one stack, choosing a message shows it
(and, beyond GTK, a click on the row already selected, which a context
menu or the back button leaves selected; the right click itself keeps the
list), the title bar's back button returns to the list with the keyboard
on the selected row (as does the Search command, search.go
`startSearch`), and a folder chosen shows its list rather than the
emptied message page a GTK stack keeps. Core's
`PaneLayout` decides all of this and the widths: the stored ones clamped to
window.blp's ranges, the list narrowed first so that the message keeps 300.
The sidebar is dragged with the toolkit's `PropertySizer` on the
`SplitView`'s `OpenPaneLength`, the list with its `GridSplitter`, pointer
only (GTK has no handle, macOS no keyboard one: not tab stops, and hidden
from UI Automation, where the toolkit's own name does not resolve in this
package set); the widths go to the gschema keys when a drag ends and with
the window's geometry, only from the wide layout. The title bar itself is
no tab stop (its buttons and the search box are), so WinUI's own first
focus of the window would land in the search box, where the single-key
shortcuts type; `MainWindow.Panes` hands that one focus to the sidebar's
first tab stop (GTK's first focusable widget, New Message), and a click or
Ctrl+F that comes first stays the user's. The command rows are 44
px under the tall title bar: the sidebar's accent New Message and the
primary menu `…` (`_New Message`, `_Add Account…`, `_Preferences`, `_About
Malachi Mail` and a Windows-only *Quit*, with the keys beside them; GTK's
dead `_Keyboard Shortcuts` is left out), the list's Check for New Mail and
title with its counts (`MessageListPane`, no search toggle: the box is in
the title bar), the message page's Reply, Reply All, Forward and, packed
from the end as in the Blueprint, Archive and Trash, a gap, then the Star
toggle, the Assistant and More Actions (`message_menu_model`, which holds
Mark as Junk; `MessageCommandBar`). Every button runs
a `WindowCommands` command (the per-message ones are `SelectionActions`,
the port of the selection half of `MessageActionsController.swift`) and is
enabled while it is; the star and the trash follow the flags (Star or
Unstar, Move to Trash or Cancel Sending). The star is a flat toggle whose
checked state is the filled star in the accent colour, as in a message
window; its glyph is set in code only, since a one-time `x:Bind` of it
runs when the control loads and would put the empty star back over a
flagged message's (it did in a message window). A control is not given the
command's `XamlUICommand` itself: assigned to `Button.Command` it replaces
the button's content with the command's empty label (measured: the header
icons came out blank), so `Main/CommandBinding` wires the click and the
enabled state. The status line (`StatusBarView`) is the bar across the
bottom (named by its text, or by its Sync Status tooltip while it is
empty) with the flyout of status.go (`StatusPopover` in Core: rows rebuilt
only when the accounts change, the buttons changed only with the action,
the unsent row leading to the outbox; the actions run after the flyout
closed). The banners' buttons and the flyout's actions are
`Main/AccountRepair` (Integration.swift's `authBannerButton`,
`signInAgain`, `statusAction`): the edit wizard through the new
`AppHooks.EditAccount` hook (§11.3), the browser for the daemon's own
sign-in, the preferences otherwise.

### 11.2 Sidebar and list

The sidebar is a flat `ListView` over the Core's entries (the GTK row model:
headings, indent 12 per depth, twisty, role glyph, unread badge, the star on
hover or selection, keyboard-reachable), with the status pages of GTK. The
message list is a virtualised `ListView` updated by key diff, rows after
`message_row.blp` (margins 8/3, avatars 40/28 with the libadwaita palette
ported, bold unread, count pill, date, unread dot, search highlights); it
pages itself at the end (as macOS), *Load More* only to retry. Context menus
(§0) on messages and folders offer only existing actions.

In the code (`Malachi.App/Main`, the view models in
`Malachi.Core/Presentation` with their tests): the sidebar's rows are
`SidebarRow`s (the rows of folders.go: headings 3 px lower with the fold
arrow, which the Favourites heading keeps as an invisible place; folders
12 in from their heading, which GTK's rows get from `Adw.ActionRow`'s
start padding (`SidebarRow.FolderInset`; the first port left it out and
drew an account's folders flush with its name), and 12 more per level,
the arrow's column only in a nested account, the
role glyph, the name and, for a pinned folder with two or more accounts,
whose it is, the badge, the star), shown by `FolderRowView` and applied by
key; the selection is the mailbox's `SelectedEntryKey`, a heading or a
container never takes it, Left and Right fold a folder with children and,
beyond GTK, an account heading (whose arrow is no tab stop in a row). The
star waits for the pointer, the selection or the keyboard (a 150 ms fade),
stays in sight in the tree for a pinned folder, and is reached with Tab
from its row (U5; verified). The context menu of a folder or heading has
Add to / Remove from Favourites and Expand / Collapse. The message rows are
`MessageRow`s (message_row.go's `SetMessage`, `SetThread` and `applyLead`:
the start margin 6, 2 in a grouped list, a member indented by the avatar
and the gap or 24; 8 or 3 above and below; the avatar 40 or 28; the
participants and the count pill of a conversation; the attachment and star
glyphs, the origin of a search result, the date through Core's `Format`,
the unread dot; sender and subject semibold while unread; the preview line
by the setting, always in search, its matched words as bold runs; the
members of an unfolded conversation on a 3 % tint; the hairline under every
row but the last), shown by `MessageRowView` with `Avatar` (Core's
`AvatarPalette`: `g_str_hash % 14 + 1`, the libadwaita gradients, the
initials; the monochrome variant). A row's accessible name is what it
shows, with Unread, Flagged and Attachment (msgids GTK has for the filter
and the attachment chip) after the subject where its icons say so. Double
click and Enter activate (a conversation folds, a draft goes to
`draft.open`, a message to its message window, §11.3), Left and Right
fold, a right click selects the row (without the navigation of a folded
window) and opens the header's actions (Reply, Reply All, Forward; Mark as
Unread, Mark as Read, Star or Unstar; Archive, Mark as Junk, Move to Trash
or Cancel Sending). The filter and the scope bar are `SelectorBar`s, whose
template's list is what UI Automation announces: it carries the bar's name
(Windows-only *Filter* and *Search Scope*; the bar itself is Raw, or a
named bar would be announced as a group around the list). The end of a
search, and the back button, ask for the keyboard on the list: the request
waits until rows are shown and their containers realised (the folder's
rows come with the daemon's reply), puts it on the selected row or the
first without selecting it, and lapses when the user selects a row or
takes the keyboard elsewhere, when a search starts again, or when the list
ends on a status page. The sidebar star's fade keeps its target beside it,
since the opacity reads the running fade's value (a star could stay lit
after a quick pass of the pointer), and lets go when it ends. The view
reports the viewport after every change of the rows' extent or the pane's
size and on every scroll (the list's `ScrollViewer` exists only once its
template was applied, which a list under a status page has not had: it is
looked up again with every size change). Verified against devmail: mark-as-read, S, U, A (the neighbour
takes the selection), J and Delete (their confirmations), a conversation
unfolded and folded with its member selected, a search in every account
with the origins and bold matches, Enter to its first result and Escape
back to the folder with the list focused (in all 22 runs that kept the
foreground, with and without a result selected, one with no results; A,
J, U, S and Delete then act on the list, and do not while the box is
typed into), a right click at 500 px keeping the list, a page past the
first, and Load More appearing only after a page request timed out (the
daemon suspended), then paging on. WinUI keeps the selection through the
in-place updates of the view overload and restores it where the controller
moves it.

### 11.3 Reader, windows, compose, wizard, preferences

One `MessageView` control in three modes (pane, window, embedded): InfoBars
for the outbox and draft, the remote-image bar (Tab-reachable, GTK), headers
in a clamp (900, sides 24, top 12), address chips with their flyout,
attachment chips as split buttons (click previews; View, Open, Save As…),
Save All, the plain body as selectable text with zoom and optional
monospace, or the viewer of §6.3. Message windows (820×620, one per
message, closed with the message) and attached-message windows as on macOS.
Compose (760×640): From, To/Cc/Bcc with the recipient popup of
`suggest.go` (not `AutoSuggestBox`: no preselection, no Tab accept), the
format toolbar, the editor of §6.5, attachment chips, *Save changes to this
draft?* on close. The window forwards the editor's Ready and Changed to
`EditorReady` and `EditorChanged` of its draft controller and, once it
really closes (after `Cleanup`), hands itself to
`ComposeController.Remove`, as GTK's cleanup calls `Manager.remove`;
Quit runs `ComposeController.SaveForQuitAsync` and asks the close
question only of the windows it returns. The account wizard is an owned modal window (a window
allows one `ContentDialog` at a time and the wizard nests the certificate
prompt), five pages as GTK/macOS. Preferences is a single-instance window
with Accounts (reorder by handle or Ctrl+Up/Ctrl+Down, a click selects the
row), General (startup, reading, deleting, notifications and the daemon's
mail settings in GTK's order, the storage rows included: *Keep Attachments
Offline For*, *Never Store Attachments* and *Compress Stored Mail* shown
once `config.get` reports them, *Disk Space Used* from `system.storage`;
then Windows' own groups: the `ctrl-r` choice, *Default apps*),
Appearance and AI, built with `SettingsCard`s.

The reader (`Malachi.App/Reader`, `Windows`, `Attachments`):
`Reader/MessageView` is the one view, its state Core's
`ReaderController` (§7.4), in the pane (`Reader/ReaderHub` puts it into the
main window's Reader region and gives the main window's per-message
commands their handlers, the selection's actions through
`MessageActionRouter`), in `Windows/MessageWindow` and in
`Windows/EmbeddedMessageWindow`. From the top, as `window.blp`'s
`message_page`: in a message window the command row (Reply, Reply All,
Forward; Archive, Trash; Star, ✦, More with `message_menu_model`),
40-pixel `AppBarButton`s bound to the window's `WindowCommands`, the star
and the trash button showing the flags (Star/Unstar, Move to Trash/Cancel
Sending); the star is an `AppBarToggleButton` with the main window's look
(the filled star in the accent colour, no accent block: theme resources
of the view), its click runs the command and then shows the flags, and
its glyph is set in code only (§11.1); in the pane the main window's
`MessageCommandBar` above the reader's region is that row (§11.1), level
with the other panes' header rows as window.blp's message header bar is,
and the view shows none of its own; the outbox and draft
`InfoBar`s (Retry, Edit); the remote-image bar, a bar of its own with Load
Images and Always From This Sender as buttons that take no focus on a click
but are reached by Tab; then the No Message Selected and No Accounts pages
(the pane, faded in) or the message: the headers in a clamp of 900 (sides
24, top 12), the address chips as pill buttons whose `MenuFlyout` names the
address and offers Copy Address and New Message, "+N more", the attachment
chips as `SplitButton`s (the click previews, an attached message opens in
its window; View, Open, Save As… from the arrow, or from the keyboard with
F4 or Alt+Down) with Save All, the hint and a separator; a part kept on the
mail server shows the server glyph after its size, dimmed like GTK's
`dim-label` and with *On the server only; it is downloaded when you open
it* as its tooltip and name, or a `ProgressRing` once the message's
download has run for 400 ms (`MessageCache.ChipsChanged`, the registry's
`RefreshChips`); its chip stays enabled and every action on it downloads
first. Under the remote-image bar, the pictures bar says how many pictures
of the HTML are on the mail server only (*N pictures of this message are on
the server only*, `neverStoreAttachments`) with *Download Pictures*, which
downloads the message and asks for its body again (under `allow` when the
remote images are shown, so they stay), a `ProgressRing` in its place
meanwhile; a failure is a toast in the window of the click and the bar
offers the pictures again. An attached message's window has no pictures
bar: its pictures arrive inlined. The headers scroll on their own once
they would take more than two thirds of the page (a
hostile message listing hundreds of parts, every address unfolded), so the
body and the last chips stay in reach; another message starts them at
their top. Below, the body as a selectable `TextBlock` in the same clamp
(a pane-wide `Grid` inside the `ScrollViewer` centres it, as the headers
are: a scroller lays its content out from the left) with the text-zoom
and monospace settings, the 32-pixel `ProgressRing` after 400 ms, or the
viewer, made on first use and fed only `ReaderController.Html`. A body the
viewer gave up on stays plain text with the hint for that message. The
chips' looks are styles of the view's resources, so their theme brushes
follow the window's colour scheme; a chip that cannot be used is disabled
and shows its reason on a wrapper, since a disabled WinUI control shows no
tooltip (none while `message.body` has not answered and the reason is
still empty). A chip's icon is the shell's for its extension, and one set
of chips looks up at most 24 extensions it has not seen (Core's
`IconLookups`; the rest get the generic glyph), since the lookup runs on
the UI thread. Save All is disabled while its run lasts, from the folder
picker to its last toast, by message (`AttachmentOpener.IsSavingAll`),
so neither a re-rendered button nor the same message in another window
starts a second run. A forward of a message with parts on the mail server
(or a body not downloaded yet, or one the cache does not hold) downloads it
before `draft.create`; when that fails, *Forward Without Attachments?* asks
over the window the forward came from (the main window when that one has
closed meanwhile), unless asking would change nothing: no daemon, one
without `message.download`, or a message over its cap. A reply downloads
first only when the body on display counts pictures on the server, and goes
on without a word when that fails; the compose window says once how many
parts of the original `draft.create` left out (*N attachments of the
original could not be attached*). Copy Address writes to the Windows
clipboard, which
another program may hold open (`CLIPBRD_E_CANT_OPEN`): it is tried five
times 50 ms apart, then a toast says the address could not be copied (a
Windows-only string) and the log has the error's kind only. Message windows (820×620, 360×294 at least) show the subject as the
title, have their own toast overlay and `CommandRouter` (the per-message
keys, Escape and Ctrl+W), give the body the first focus, and close with
their attached messages' windows when the message leaves its folder
(`MessageWindowRegistry`, which also fans out what the cache and the actions
learn to every view). An attached message's window has the containing
message's subject as its subtitle and no command row; Load Images renders
the part again with remote images allowed. Links go through `LinkOpener`
(§6.4); the question is `IAlerts.OpenLinkQuestionAsync`, with the
destination alone for an unlisted link. The folder is `Windows/`, the
namespace `Malachi.App.MessageWindows`: a namespace `Malachi.App.Windows`
would hide the `Windows.*` namespaces from every file of the app.

The compose window (`Malachi.App/Compose`):
`ComposeManager` makes the windows `ComposeController` asks for
(`Integration.InstallComposeWindows`, before the first activation, so a
cold `mailto:` opens its composer at once) and cascades them 32 px from a
first one centred on the active window's display. `ComposeWindow` is
compose.blp top to bottom: its header bar as the window's tall `TitleBar` on
Mica (Attach at the start; the subject, or *New Message*, as a `TextBlock`
in the bar's middle column, shortened with an ellipsis, since the
`TitleBar.Title` column never shrinks and a long subject pushed Send under
the caption buttons; the Draft Menu and the accented Send, tooltip
*Send (Ctrl+Enter)*, at the end; below about 500 px, where the title would
get less than a few characters, Send shows only its icon and the app icon
goes, from Core's `HeaderBarFit` over the wide layout's measures, and the
bar is asked to recompute its drag regions), then under the toast overlay
`ComposeHeader` (the card: one 30 px line per field, the labels in one
column, flat fields whose own template lets the invalid state be a style
with red text and underline), `FormatToolbar` (flat buttons that never take
the focus, the checked toggles in the accent colour, the link popover whose
entry turns red for a refused link, the text colour as a `ColorPicker` in a
flyout sent when it closes with another colour), the plain-text hint, the
`ComposeWebView` in the editor slot (the view background, no lines of its
own) and `AttachmentChipsView` (six chips to a line, `ChipWrapPanel`; each
chip a UIA group named after its file, `ChipGroup`, so that Narrator says
whose *Remove* it is), and the status line at the bottom. 760×640, at
least 360×420 (`OverlappedPresenter.PreferredMinimumWidth/Height` at the
window's scale). The logic is Core's: the draft controller, and the
presentation classes `ComposeAttachmentsController` (imports, removal, the
list the backend kept, the `cid:` registrations, the chips),
`SuggestionsController` (one per recipient row; the 150 ms pause, the
generation, the keys), `ComposeHeaderRules` (the From row, the Cc/Bcc
button, the title) and `FormatBarState` (applyState and the commands). The
recipient popup (`RecipientSuggestions`) is a `Popup` constrained to the
window under the row, as wide as it, whose 36 px rows never take the focus;
the row's `PreviewKeyDown` hands it Down, Up, Enter, Tab and Escape. UI
Automation does not find the popup, and the keyboard stays in the row, so
the row raises a polite notification with the selected suggestion (name and
address) when the popup opens and when the selection moves to another one.
Keys: the window's `CommandRouter` runs Ctrl+Enter, Ctrl+S, and Escape and
Ctrl+W (the close request, not while a popup of the window is open), and lets
`EditorKeys.BridgeHandles` through to the page (Ctrl+Shift+I is italic
there, as in GTK); the bridge posts Escape (the close request) and Ctrl+K
(the link popover). The editor's WebView2 is marked
`KeyboardRouting.IsEditor` and named *Message body* (a Windows-only string;
GTK names it nothing), again after a failed browser process replaced it.
The open dialogs are the shell's `IFileOpenDialog`, owned by the window, on
an STA thread of their own, with GTK's titles and the named *Images*
filter; an item without a file-system path is refused with *Only local
files can be attached*, where a local path is drive-absolute or a share's,
never the `\\?\` and `\\.\` namespaces. Measured: WinUI's WebView2 hands a
shell drop of files to its host, not to its page, so the editor slot takes
it (`AllowDrop`, the storage items' paths); the bridge's drop path of §6.5
stays for a runtime that hands it on. Closing (the caption, Alt+F4, Escape,
Ctrl+W) is draft.go's close request through `AlertService`; as Swift's
`windowShouldClose`, `AppWindow.Closing` lets a close with nothing at stake
go on and cancels only to ask, the window closing after the answer (never
`Close()` inside `Closing`); the question is asked once, a second request
waits for its answer, and one that ends at once leaves nothing behind. Quit
saves without asking and only a failed save asks (`CloseForQuitAsync`).
Measured: a window closed while inactive (its caption button invoked
through UI Automation, a composer closing itself after Send) reports an
activation after `Closed`; `WindowTracker` ignores it, since the stale last
active window without an `AppWindow` made the next composer's placement
throw, and the app ended (`0xc000027b`). A window the factory cannot make
is logged by `ComposeController.Open` and leaves nothing open (a window made
but not shown closes, so it holds no app); one that cannot be placed opens
where Windows puts it. Three fixes of what GTK and macOS leave as it is:
the placeholder account's
status goes when the real accounts arrive; a template's inline picture is
fetched from the account the window has when the editor asks for it
(read when it is registered, the first window of a run asked with the
placeholder identity and got `attachmentNotFound`), and while the From row
still lists the placeholder the window's own calls use the account it was
opened for; and a composer made before the connection (a cold `mailto:`)
asks for the accounts again once connected. Verified against devmail with
real input: a message with bold text, a Cc, a picked and a dropped
attachment and an inserted picture autosaved (and uploaded to the server's
Drafts), sent with Ctrl+Enter from the editor and delivered; recipient
suggestions after that send (Enter and Tab accept, Escape hides); reply and
forward through `draft.create` with the quoted original, its inline picture
and the forwarded file; a draft reopened through `draft.open` and raised
when opened again; the close question; Quit saving a dirty draft without
asking; a cold `mailto:` showing only its composer, and the app ending with
it. Again after the review of the header bar and the close: a subject of
117 characters at 760, 470 and 360 px (Send, Minimize, Maximize and Close
apart, the title a drag region); the caption's Close invoked through UI
Automation on a composer with nothing at stake, then a `mailto:` composer,
four times without a failure; the notification of a suggestion as a UIA
client receives it; the chips as named groups.

The wizard and the Preferences (`Malachi.App/Wizard`,
`Malachi.App/Preferences`): the wizard
(`AccountWizardWindow`) is owned by the window it opens from
(`GWLP_HWNDPARENT`) with a modal dialog `OverlappedPresenter`, 520×640
effective pixels with its title bar, centred on its owner. A modal window
disables its owner, and Windows gives the activation to the next enabled
window when the active one goes away (measured: another app, the main
window instead of the Preferences, or none), so the wizard enables its
owner again and activates it in `Closed`, as a Win32 dialog hands back its
owner; every way out (Escape, Ctrl+W, Alt+F4, the close button, a save)
returns the keyboard to the control that opened it (measured from the
Preferences and from the main window). It is restored whenever something
minimises or maximises it (UIA still exposes the caption buttons the
dialog presenter hides). Its title bar is
the WinUI `TitleBar` with Back (`WizardController.CanGoBack`, also
Alt+Left and the mouse's back button, as `Adw.NavigationView`) and the
visible page's title; the `TitleBar` names the window after its title
when it loads and when the title changes, so the window's own name is set
back to the wizard's (*Add Account*, *Edit Account*, *Sign In*) for
Alt+Tab and UIA. Below it a `Frame` navigates to a host page per
wizard page, sliding in from the right for a later page and from the left
for an earlier one (`WizardController.Rank`), while the five pages are
made once and keep what was typed; the `Frame` reports a navigation
before the page is in the tree, so a page takes the focus once it is
loaded. The pages are `UserControl`s over the
controller's events; the identity and Servers rows are `SettingsCard`s,
a flagged row's title turns the critical colour (the GTK `error` class)
and the test's rows show green or red icons. Those colours are
`{ThemeResource}` setters of styles in `Preferences/SettingsStyles.xaml`,
resolved in the theme of the element's window: a brush looked up in
`Application.Resources` has Windows' theme, not the colour scheme a window
root's `RequestedTheme` carries (measured: `#C42B1C` on a dark card,
2.5:1), and does not follow a change. For Narrator the result rows are
named by their title and described (`HelpText`) by their result, the
Servers groups are named by their headings, and the results announce
their title and focus the suggested button. The
status pages are `Wizard/StatusPage` (Adw.StatusPage's margins). The
certificate confirmation is the shell's ContentDialog on the wizard's
window. Entry points: `AccountWizardWindow.Show` (add, edit, sign in again,
ask for the password), and the hooks `AddAccount`, `EditAccount` and
`OpenPreferences` (`Preferences/PreferencesEntryPoints`). Preferences
(`PreferencesWindow.Show`, one instance) is a Mica window with a
`NavigationView` pane (its labels from 720 px up, only its icons and the
button that opens it below: `PaneDisplayMode="Auto"`, never hidden) and a
page column clamped to 600 px, down to 480 px wide; its settings are
bound two-way (`Preferences/SettingBindings`), the Mail group is
`MailPreferencesController` with `StorageUsageController` for its Disk
Space Used row (`system.storage` when the window opens, after every change
the daemon confirmed and every 5 s on the `TimeProvider` while the window
is open, one call at a time; hidden for a daemon without the method; its
texts `StorageUsage.StorageTexts`), the AI page `McpRegistrationController`, the
Accounts page Core's `AccountsPageController` (§7.4) rendered with
`KeyedListSync`; a row too wide for the page puts its status and its
controls on a second line (`Preferences/AccountRowPanel`), as a
`SettingsCard` wraps. The window stays open beside the main window, where
accounts can be added and edited and change their state, so the page
follows `notify.accountsChanged` (a reload, after an order being saved)
and `notify.syncState` (the row's status), which GTK's dialog, built on
every open, does without. A drag takes its row out of the collection and
puts it back: that deselection is not sent to the controller. A `ListView` takes Ctrl+Up and Ctrl+Down for its own
focus before the window's accelerators (measured), so the list asks the
controller first in its `PreviewKeyDown`; the window's `MoveUp` and
`MoveDown` commands cover the rest. A `ScrollViewer` places a direct child
that has a `MaxWidth` of its own off centre (measured), so every clamped
column sits in a `Grid`.

### 11.4 Banners, toasts, alerts

Banners are `InfoBar`s. Toasts are a per-window overlay (5 s default, queued,
Narrator notification). Alerts go through one `AlertService` that queues
`ContentDialog`s per window; button order follows `ContentDialog` (primary
left, Cancel right), defaults and close responses stay GTK's (*Save Draft*
is the default of the close question).

In the code: `Controls/ToastHost` over Core's `ToastPresenter` (a capsule 42 px
high, 24 px above the bottom, one line with the whole text as its tooltip,
a 0.2 s fade, a click dismisses; `AutomationPeer.RaiseNotificationEvent`),
and `Shell/ToastRouter` for the application's toasts (the active window's
overlay, else the main window's). `Shell/IAlerts` is Contracts.swift's
`Alerts` (destructive, destructive with a check box, the close question,
*Open This Link?*, *Trust This Certificate?* with its details grid) plus
About; `AlertService.ConfirmHook()` is the `ConfirmDestructive` the
controllers take. A dialog for a hidden window goes on the main window,
shown first. Mnemonics: `{l:T}` returns the msgid with its `_`, and
`Localization/MnemonicLabel.Text` turns it into the control's text and
`AccessKey` (a `ContentDialog`'s buttons take none: stripped).

### 11.5 Keyboard

| Function | Windows | GTK |
|---|---|---|
| New Message / Preferences | Ctrl+N / Ctrl+, | same |
| Reply / Reply All / Forward | Ctrl+R (with `ctrl-r` = `reply`) / Ctrl+Shift+R / Ctrl+Shift+F | — |
| Check for New Mail | F5; Ctrl+R with `ctrl-r` = `refresh` | Ctrl+R |
| Search | Ctrl+F, Ctrl+E; Enter first result, Escape closes | Ctrl+F |
| Trash / Archive / Junk / Unread / Star | Delete / A / J / U / S, also with the message's WebView2 focused, never while a text input has focus (GTK `setTypingAccels`) | Delete / a / j / u / s |
| Quit | Ctrl+Q | Ctrl+Q |
| The primary menu | F10 in the main window, also from a text input or the message view, while the sidebar shows the menu's button | F10 (`primary: true`), while the button is mapped |
| Close a secondary window | Escape, Ctrl+W | Escape |
| An attachment chip's menu (View, Open, Save As…) | F4 or Alt+Down on the chip (`SplitButton`); Enter or Space previews | Tab to the chip's arrow, then Enter |
| Send / Save draft / Bold, Italic, Underline | Ctrl+Enter / Ctrl+S / Ctrl+B, I, U | same |
| Reorder accounts | Ctrl+Up / Ctrl+Down | same |

Measured (§16): while a WebView2 has focus, **no** XAML accelerator
and no XAML key event fires (27 keys, real input, viewer and editor), but
every key reaches the window's `InputPreTranslateKeyboardSource`
(`GetForIsland(XamlRoot.ContentIsland)`, `SetPreTranslateHandler`) exactly
once, on the UI thread, and can be swallowed there. Each window therefore
has one command router, fed by XAML `KeyboardAccelerator`s while XAML has
focus and by the pre-translate handler while a WebView2 has focus (acting on
`WM_KEYDOWN` of the `Chrome_WidgetWin_0` focus window and swallowing the
matching `WM_KEYUP`); a UI-thread `WH_KEYBOARD` hook is the fallback. In the
editor, Ctrl+B/I/U, Ctrl+K and Escape stay in the bridge (`preventDefault`,
as in GTK and macOS), Ctrl+Shift+I is prevented there too (Chromium types a
Tab for it); window keys (Ctrl+Enter, Ctrl+S, Ctrl+W, Quit) go to the
router. `AreBrowserAcceleratorKeysEnabled=false` really suppresses reload,
find, print and DevTools for real input (CDP-injected keys bypass it, so
keyboard UI tests use real input). Single-letter keys are gated whenever a
text input has focus: in a `TextBox` they would fire and type at once.
Overlays (`ContentDialog`, flyouts, `TeachingTip`, `InfoBar`, popups) draw
above the WebView2 with no airspace workaround; the viewer and editor are
never hosted in a raw HWND controller.

In the code (`Commands/`): every tracked window has a `WindowCommands` (named
`XamlUICommand`s: the application's, the per-message ones enabled from
`Flags`, an `ActionFlags` the screen sets, Check for New Mail, Search,
the primary menu, Close, Send, Save Draft, the reordering) and a
`CommandRouter` that runs them for Core's `ShortcutMap` (the `ctrl-r`
setting read at each key). The
XAML side is `KeyboardAccelerator`s on the window's root (placement
hidden), plus the root's `PreviewKeyDown` for Ctrl+Q, which a `TextBox`
consumes before any accelerator; the WebView2 side is
`PreTranslateKeyboard` (the COM interop of the spike, built-in COM, the
app being untrimmed), or, where an island gives no pre-translate source,
`ThreadKeyboardHook` (the measured `WH_KEYBOARD` fallback: thread-local,
acting only while the thread's focus window is a `Chrome_WidgetWin_0`
inside its window): a mapped key down of the `Chrome_WidgetWin_0` focus
window is swallowed and its command runs after the message, the key up of
that press is swallowed too when it comes to the same focus window (Core's
`SwallowedKeys`: a command that moves the focus sends it elsewhere, and
the next press of the key starts afresh), an auto-repeat runs nothing
more, and the browser's own keys are swallowed even when they run
nothing. The compose
editor's WebView2 carries `KeyboardRouting.IsEditor="True"`, so that its
single keys type and Escape stays with its bridge. No key runs anything
while one of the window's dialogs is up. Verified with real input: Ctrl+F
puts the focus in the search box, letters then type there, and Ctrl+Q
quits from it; Ctrl+Comma and Ctrl+N run their commands from there. The
WebView2 side was driven the same way, through the pre-translate source
and, forced, through the hook, on a WebView2 put in the main window for
the test before the reader existed: a letter reaches the page down and up;
F5, Ctrl+Comma, Ctrl+N and A run their commands and the page sees none of
them (F5 no reload); Ctrl+F moves the focus to the search box before its
key up, and the next F typed in the page arrives down and up; Ctrl+Q
quits. On the real editor the compose window's keys were verified with
real input (§11.3: a message typed with bold text and sent with
Ctrl+Enter from the editor).
In phase F the same was done on the real reader (§12, the end-to-end
walk): with an HTML message's viewer focused by a click, Ctrl+R opens the
reply, S and U toggle the star and the unread state, A archives (the
neighbour takes the selection), J and Delete ask their questions. F10
(GtkWindow's key for the primary MenuButton, `gtk_window_activate_menubar`,
which passes over a button that is not mapped) is the main window's
`MainMenu` command: it opens the primary menu under its button while the
sidebar is shown, inline or as the open overlay, and does nothing while
the sidebar is folded away. Verified with real input: F10 with the list
focused and with an HTML message's viewer focused (the Win32 focus on
`Chrome_WidgetWin_0`) opens the menu with the keyboard on its first item,
Escape closes it and gives the keyboard back to the list or the viewer; in
the open overlay it opens there; with the sidebar folded it opens nothing
and leaves no menu mode behind (Down still moves in the list). The
automated UI tests (§12) use UI Automation's patterns only, never
synthetic keys (they run beside other windows and never need the
foreground).

### 11.6 The Assistant

The ✦ button of the message pane's command bar (`MessageCommandBar`,
window.blp `assistant_button`) carries the Assistant menu
(`Assistants/AssistantMenu.cs`, a `MenuFlyout` built again each time it
opens, as GTK's model is: the four message actions, *Summarize Unread in
This Folder* while the folder has unread mail, *Open In* with the three
targets as radio items, and, while the chosen Claude app cannot take the
request, its problem and *Set Up…*, which opens *Preferences → AI*). The
same menu sits in a message window's command bar, without *Summarize
Unread*. The attachment chip's menu has *Ask Claude about This
Attachment*. Every action goes through `AssistantActions` (the port of
GTK's `assistant.go` and macOS `AssistantActions.swift`): a hand-off
builds the link in Core and opens it with `ILauncher.OpenAssistantLinkAsync`,
In App runs in the panel.

The panel (`Assistants/AssistantPanel.xaml`, the port of macOS
`AssistantPanelViewController`) is the pane of a second `SplitView`
(`AssistantSplit`, pane on the right) around the three panes, with its
toggle beside the ✦ button while In App is chosen. Wider than
`PaneLayout.AssistantBreakpoint` (1180 px) it is a pane of its own beside
the others, narrower an overlay that a click outside or Escape closes;
its width is 28 % of the window's between 280 and 480 px (GTK's
`Adw.OverlaySplitView` with the same numbers; not resized by dragging).
Its rows are a header with *New Conversation*, the context chip (its ✕
removes the context), the quick actions, the bar shown when the list
selects another message than the conversation's (*New Conversation* or
*Add to Conversation*), the transcript (user, answer, activity, draft,
error and note rows in a `StackPanel`, kept in step with the controller's
`Items` by its `Changed` indexes), the waiting action with its ✕, the input (a `TextBox`: Enter sends, Shift+Enter and
Alt+Enter start a new line, Escape drops a waiting action) with
Send/Stop, and the footer. An answer is Core's Markdown subset rendered by
`AnswerRenderer` into a `RichTextBlock` (paragraphs, headings as bold
runs, lists with a hanging indent, code in the monospace font, links as
`Hyperlink`s whose click goes to the reader's link opener with no listed
link, so *Open This Link?* names the destination); nothing of an answer is
ever markup. The consent question (`assistant-consent`) is a
`ContentDialog` on the window that asks (`IAlerts.ConfirmAsync`).

The compose window's header has the rewrite's ✦ button while the one-shot
requests can run (`ComposeWindow.Rewrite.cs`): its flyout has the four
rewrites, an instruction field (Enter sends; with no words, Enter takes
the answer), the answer as plain text in a read-only box, and *Discard*,
*Insert Below* and *Replace*; the editor's passage and the answer go
through the bridge (§6.5). The main window's search box has the ✦ of
*Search in Your Own Words* and Alt+Enter (`MainWindow.OwnWords.cs`); while
the words are converted the box is disabled and says so. *Preferences →
AI* (`AiPage`) has GTK's rows: *Register with Claude* (with Claude
Desktop's restart question), *Assistant Menu*, *Open In*, and for In App
*Claude Code* (the path found or chosen, its version and whether it is
signed in, *Choose…*, whose file dialog takes a `claude.exe`, and in front
of it what the row offers: *Sign In…* while Claude Code says it is signed
out, *Get Claude Code…* while there is none) and *Model*. The panel's
error line has the same two buttons beside *Try Again*.

An open inline panel takes its width from the three panes, which are laid
out for what it leaves (Core's `PaneLayout.Resize` gets the window's width
less the panel's, so the sidebar folds below 900 px of it), and the list
narrows, not below its minimum, so that the message pane keeps the width
its buttons need (`PaneLayout.Widths` with the command bar's measured
width): GTK's breakpoints follow the window and its panes' minimum widths
keep the header bar whole, where WinUI would cut off the last buttons.

Walked through on 2026-09-29 with the published app on its own data,
socket, preferences key, fake keyring and home folder, against devmail and
a scripted stand-in `claude.exe` (Malachi.FakeClaude, set as the path in
*Preferences → AI*): registering with Claude Code in that home folder,
*Open In → In App*, the Claude Code row (path, version, signed in), the ✦
menu (the four actions, *Summarize Unread*, *Open In*), the consent
question, the panel inline and as an overlay, *Summarize* with the
activity row and the streamed answer rendered (heading, emphasis, list,
code, a link and a literal `<script>`), the link's *Open This Link?*
naming the destination, the bar for another selected message and *Add to
Conversation*, a typed question stopped with *Stop*, an attachment's *Ask
the Assistant…* waiting for words (enabled only for the types the bridge
can read), the rewrite of a new message (a preset, the preview while it
streams, *Replace*, one Ctrl+Z back) and of a reply (a custom instruction
over the text above the quote only, *Insert Below* above the
attribution), and the search in your own words (the box read-only while
converting, the query searched). The child's command line, environment,
working directory and stdin were read back from the stand-in's records.
Not walked: the hand-off links (they would open the Claude apps of the
machine the session ran in), Claude Desktop's restart (the same reason),
and a real Claude Code, whose login on the development machine had
expired.

The sign-in was walked through the same way on 2026-09-30, with a stand-in
whose `auth status` says signed out until its `auth login` has run for
four seconds: the *Claude Code* row (*Not signed in*, *Sign In…*, then
*Waiting for the sign-in in your browser…*, then *Signed in* and no
button), the panel (*Claude Code is not signed in* with *Sign In…*, the
waiting line with *Stop*, then the answer to the question asked before),
and without any Claude Code both the row and the panel's line with *Get
Claude Code…* (beside *Try Again* in the panel). The sign-in's working
directory and environment were read back from the stand-in's records. Not
walked: *Get Claude Code…* itself (it would open the browser of the
machine the session ran in) and the sign-in of a real Claude Code, which
takes the owner's answer in the browser.

### 11.7 Jira accounts and the conversation view

Ported on `feat/jira-windows` (2026-09-30) after the macOS client (which
had them first) and the GTK UI (which mirrors it, and is the reference for
the behaviour). The pure logic is Core's, each file a port of its Go
reference and of the Swift with the tests of both: `IssueTrackers/`
(`ui/internal/jira` and `ui/internal/capabilities`: the texts and view
models of the assistant, the sidebar, the list rows, the issue card and
its transitions, the event lines, the comment window and the account
settings with its checks), `Model/Conversation*` and
`Model/ConversationLayout*` (`ui/internal/conversation` and GTK's
`conversation_layout.go`: the items, the order, the member marked read,
the live cards, the rails, the height governor), and the controllers
`JiraWizardController`, `JiraAccountController`, `IssueActionsController`
(the transitions menu and the busy state of an issue, one for the app),
`ConversationController` and the comment mode of `ComposeController`,
tested against the fake daemon. The app is thin over them:

- **Adding and editing.** *Add Jira Account…* is in the primary menu `…`,
  in the *+* menu of *Preferences → Accounts* and on the empty window
  (`AddJiraAccountButton`); it opens `Wizard/JiraWizardWindow`, an owned
  modal window like the account wizard (`ModalDialog`, shared by both)
  with the pages site, credentials and spaces. Every route that edits an
  account asks `AccountsPage.EditorOf` first (GTK's `accountEditor`): a
  Jira account opens `Preferences/JiraAccountWindow`, one scrolling page
  over `JiraAccountController` whose closed statuses and lists are
  `JiraStatusPicker` and `JiraListEditor` (theme brushes from XAML, so a
  window's own theme reaches them); *Replace Token…* opens the assistant
  in its edit mode over it.
- **Sidebar and list.** The account's heading carries the JIRA capsule,
  the views (Assigned to Me, Watching, Open) sit above the spaces, a Jira
  folder is always grouped, rows show the issue key and the status pill,
  event rows are never unread; the compose button is off while no account
  writes mail (`Integration.CanComposeNew`), the tray's New Message too.
- **Reading.** `Reader/IssueCardView` over the headers of a Jira message
  (its key a link only to the account's own site, the pill a
  `DropDownButton` with *Change Status* while the account transitions and
  the spinner while one runs; `IssueActionsController` fans the result out
  to every card that shows the issue). *Change Status* is in the command
  bar's and the list's menus and acts on the card of the pane, of a
  message window, or of the conversation shown. Reply is *Comment* on an
  account that comments (`ActionRules`), and the compose window opened for
  it is in its comment mode (`Compose/ComposeWindow.Comment.cs`): the
  issue's key and summary instead of the header fields, *Reply to
  Customer* / *Internal Note* on a service-desk request, the restricted
  formatting bar, nothing attached, no Save Draft, pinned to the issue's
  account (`ComposeController.CommentAccount`).
- **The conversation view** (`Reader/Conversation/`). Selecting a folded
  conversation row of the grouped list (`ListRow.ShowsConversation`; not
  while a search shows its flat results) shows the whole conversation in
  the reading pane (`ReaderHub`: `ReaderController.LeaveForConversation`
  lets go of the single message, `ConversationController.Show` of the
  row); any other row clears it. `ConversationView` lays out Core's
  `ConversationModel`: the issue card once on top, then
  `ConversationLayout.DisplayOrder` (what opened the conversation, folded
  to its header and a preview while more follows, then the rest newest
  first, the row of older members left out at the bottom), each member a
  `ConversationCard` on the timeline of `ConversationRow` (the sender's
  avatar, the user's own in the accent colour; the dots of the issue's
  status and assignee changes, `ConversationEventRow`). A card has the
  fold arrow, the unread dot, the sender, the recipients' disclosure, the
  Jira badges and the hover buttons (Reply or Comment, Reply All,
  Forward; also shown while one has the keyboard focus; a double click
  on the header outside its buttons opens the message in a window of its
  own, a draft in the compose window, `ActionsController.OpenMessage`),
  and below the
  single-message pane's own recipients, chips (`MessageChips`, shared with
  `MessageView` and its `ChipStyles.xaml`), hint and bars. Bodies are
  fetched only for the cards near the viewport (`NeedsBody`); an HTML body
  is a `CardWebView` (§6.7) from the pane's pool, a plain one a
  `TextBlock`. Where heights settle (bodies arriving, views measuring),
  the item being read stays in place through the `ScrollViewer`'s own
  anchoring (every row an anchor candidate, the anchor at the viewport's
  top) where GTK moves the adjustment back itself; at the top the view
  stays at the top. The conversation follows the list: members that
  arrive, change or go are merged (`ListController.ThreadMembersChanged`,
  `ConversationChanged` after `notify.messagesChanged`). Only the newest
  member that is not an event is marked read. The list keeps the
  keyboard: Space and Shift+Space page through the conversation
  (`MessageListPane.PageConversation`).

Walked through on 2026-09-30 with the published app on the walk's own
data, socket, preferences key and fake keyring, against a fake Jira Data
Center site on 127.0.0.1 (a copy of `backend/internal/jira/jiratest`
listening on a real port, seeded with a service desk and two ordinary
spaces, with a control endpoint that makes another user comment, write an
internal note, change a status or assign) and devmail: the assistant
(detection, the personal access token, a wrong token's refusal, the
spaces with their counts), the sidebar and the views, the list with its
pills, the issue card and *Change Status* from the pane, from a message
window and from a conversation (the transitions that need fields in Jira
disabled with the reason, the event row and the toast after), comments
from the command bar and from a card's hover button (an internal note and
a reply to the customer, posted with their visibility), a comment, a
status change and an assignment made on the site arriving in the shown
conversation or taking the issue out of *Assigned to Me*, the settings
window (the events switched off and the conversation rebuilt without
them, *Replace Token…*), the conversation view of mail threads (plain and
HTML cards, folding, recipients, the wheel over both kinds of card) and
Space paging. Two faults found there are fixed: the conversation stopped
following its members because the issue cards were compared through the
API's JSON context, which does not know them, and a reload kept a
conversation's members when only its issue had moved (a status change
with the events off), in all three clients (`sameShape`). Not walked: a
real Jira Cloud site (the owner's, with a token they enter).

## 12. Tests

`make test-windows` (`build.ps1 test`) runs six test projects, 5,944
tests in about a minute and a half on the development machine
(2026-09-30): 5,067 in `Malachi.Core.Tests`, 651 in
`Malachi.Platform.Windows.Tests`, 159 in `Malachi.Credentials.Tests`, 27
in `Malachi.Conventions.Tests`, 28 in the canary and 12 UI tests (4 of
them opt-in). The canary and the UI tests share the desktop when they run
side by side: once in a while a UI test's primary menu closes before its
item is found (seen once on 2026-09-30, `AboutShowsTheVersion`), and a
second run passes. The tests that need a
built `malachid.exe` skip without one
(`make windows` or `build.ps1 go` builds it, `MALACHI_TEST_MALACHID`
names another), and the Credential Manager round trips run only on
request. The `.trx` reports land in `build\windows\TestResults\`.

- `Malachi.Core.Tests` (xUnit v3): every Go UI pure-logic test and every
  Swift test of `MalachiCoreTests` ported with the same shape (Theory for
  Swift's parameterised tests): models, threads, folding, favourites,
  folder tree, actions, attachments, accounts page, notification text,
  outbox, sync status, compose source, address list, prefill, mailto,
  suggestions, links, CID registry, editor bridge, wizard fields and
  results, sign-in, format, error texts, provider; the transport (framing,
  JSON-RPC, the §1.4 vectors, Go's handshake failure table, the client);
  API coding and notifications; settings, localisation (the Czech cases
  read `po/cs.po`), printf, plural rules, strftime; the WebView2
  crash-dump sweep (links on the way never followed, a dump in use kept);
  the Assistant's pure package (Go's `ui/internal/assistant` cases and
  Swift's) and its process, locator, one-shot requests and panel against
  a stand-in `claude.exe` (`tests/Malachi.FakeClaude`: a small program
  copied into a fresh folder beside the JSON script of its turns, which
  records its command line, environment, working directory and stdin);
  the controllers against
  the C# **FakeDaemon** (an in-process daemon on a real AF_UNIX socket with
  a short path, playing the handshake with all of macOS's modes) and
  **MailFixture**, with `FakeTimeProvider` and `IdleAsync`. They run on any
  OS with .NET 10.
- `Malachi.Platform.Windows.Tests`: the process host against
  `Malachi.Core.TestDaemon` (graceful stop, kill after the timeout, deaf
  daemon), the key-file policy (owner, DACL, reparse points), the registry
  backend and its watcher, file-name rules, Mark of the Web round trip,
  the notification-area icon on a hidden window (added, re-added after a
  simulated Explorer restart, removed), launch at login and the `mailto:`
  registration under a test key of their own
  (`HKCU\Software\io.github.schotek.Malachi.Tests.<guid>`, one key per
  test and no parent they share), the command
  line, the notification arguments, quiet hours and the sound; the
  handshake, a call and a notification against the real `malachid.exe`,
  its clean stop in a private run directory, a crashed daemon replaced,
  and the console roles of §5. What only
  the real shell shows (a toast and its click, the handler list, the Run
  key, the icon's menu) is checked by hand from outside Claude's process
  tree (§1) and cleaned up afterwards.
- `Malachi.Credentials.Tests`: the protocol without the store; a real
  round trip (4 KiB and chunked values, `cmdkey`'s UTF-16 items) on request
  (`MALACHI_CREDENTIALS_TEST=1`), one test at a time under the helper's
  session lock.
- `Malachi.Conventions.Tests`: the strings check and the msgid coverage
  (§9), the gschema's keys against the settings facade, the SPDX headers
  of every file type under `windows/` (C#, XAML, the MSBuild files, the
  solution, the manifests, PowerShell, and `.js`, `.css`, `.html`, `.xml`,
  `.config` and `.resw` should they appear).
- The **network canary** (`Malachi.App.Canary`, with its WinUI host
  `Malachi.App.Canary.Host`, which compiles `src/Malachi.App/WebViews`
  itself): the host holds the real viewer, a conversation card's view,
  the editor and the previewer in the
  app's environment, in a window beyond the edge of the screen that never
  takes the focus, and plays the spike's hostile document (every vector
  with its own loopback listener in the test process, DNS-only host names,
  UNC paths), its active twin (hover, press, a link with `ping`, a form,
  `target=_blank`, a middle click, `mailto:`, `download`, a UNC link, the
  security audit's masked links, an empty anchor before one that wears the
  bank's address on the same href, a meta refresh; pointer input through the
  DevTools protocol), the
  previewer's HTML, SVG, PDF (its link clicked, its open action), picture
  and text, and every HTML part of `backend/testdata/mime` **raw**, without
  the sanitiser, in the viewer and the editor. Chromium writes a NetLog
  (`--log-net-log`, an option only the canary sets); reading it fails when
  the runtime lacks an event or source type the reader relies on, so a
  renamed event cannot make the checks pass by never matching. The test
  asserts that no listener was reached; no name was looked up (resolver
  requests were made, every one mapped to `~notfound`, no resolver job,
  and no DNS canary name appears anywhere in the log); no TCP connection
  was attempted and every UDP connect failed; no URL request was started
  but WebView2's own background ones (runtime 153: `config.edge.skype.com`,
  and the component updater's check at `edge.microsoft.com`, which comes
  about a minute after the browser started and so only in a run slowed
  down by a busy machine), each started by the browser (the NetLog's
  initiator, which the control run shows is the page's for a page's
  request) to a host and path the test names,
  which checks the gate, the CSP and SmartScreen apart from the resolver
  rule that would hide what passed them (what only that rule stops, by
  design, is a cancelled navigation's speculative preconnect, which starts
  no URL request); nothing navigated but the views' own documents; a click
  is user-initiated and a meta refresh not (what `NavigationPolicy` relies
  on); no window opened (the four windows WebView2 draws the views in
  aside, titled *Malachi Mail* throughout, also while the previewer shows a
  PDF whose metadata has a title of its own); nothing downloaded; the gate
  answered 403 to everything not the view's own; and that clicks reached
  the reader as links (forms and refreshes not), each masked link once,
  with its attribute as written, resolved to the host after its `@` (but
  for the backslash, which ends Chromium's authority first; §6.4); and,
  as checks of the
  views themselves, that the editor's bridge types, formats and flushes
  under its CSP, a dropped file arrives as a path, the viewer zooms, and
  the card view measures a document of a known height at two zooms
  (§6.7).
  A control run of the same document in a WebView2 without protection
  (its reach beyond the machine cut off) must reach the canaries (the
  preconnect and the prerender among them) and show connections, URL
  requests to the canaries and the DNS canaries' names in its NetLog, so
  the harness is known to see leaks. The UNC vectors are played but not
  observed: Chromium refuses `file:` from a web page in either run (the
  link becomes `about:blank#blocked`, the pictures are never requested);
  their WebDAV form (`file://127.0.0.1@<port>/…`) would reach its listener
  through the WebClient service if a runtime ever opened one, while the
  SMB form (port 445, the NTLM leak) stays outside what the canary sees.
  A recovery run crashes each view's renderer twice under one document
  (DevTools `Page.crash`, once while a document loads) and hangs the
  viewer's once (a host script that never ends): each document is shown
  again exactly once, the second crash raises `Unavailable` with no
  further load (the editor's `Crashed` once), and the hung renderer gets a
  new control once its report went 5 s unanswered (§6.1); a load stopped
  half-way (a failed navigation with no process to blame) raises
  `Unavailable` a second later. The host puts itself, and so the browser
  and all its processes, in a job that pins the priority class to normal:
  Chromium starts the renderer of a page shown again after a crash at idle
  priority until it commits, and while the other test assemblies keep
  every core busy an idle process gets no CPU at all (measured), so the
  reload stalled until Chromium's 30 s commit timeout (the app keeps that
  limit, §6.1). The three runs
  go side by side in about 50 s; without a desktop session or the WebView2
  runtime, and on a CI runner (`GITHUB_ACTIONS=true`) unless
  `MALACHI_CANARY=1`, the tests are skipped with that reason (§13);
  `MALACHI_CANARY_KEEP=1` keeps the runs' files, and each run's
  `results.json.progress` shows how far a run that never finished got. It runs in `make test-windows` and on
  every WebView2 runtime bump.
- The **UI smoke tests** (`Malachi.App.UiTests`, xUnit v3 over the Windows
  Desktop framework's own UI Automation client, `UIAutomationClient` and
  `UIAutomationTypes` through `UseWPF`; no FlaUI, no package) start the
  published app, the folder `build.ps1 app` assembled
  (`build\windows\<arch>\Malachi Mail\`, or the one `MALACHI_UITEST_APP`
  names), as a user would, with a temporary folder of their own for the
  data (`MALACHI_DATA_DIR`) and the socket (`MALACHI_SOCKET`, a short path),
  a registry key of their own per app for the preferences
  (`MALACHI_SETTINGS_KEY=io.github.schotek.Malachi.UiTests.<guid>`, §8,
  deleted once that app is gone, also after a failure),
  the bundled daemon and `Malachi.FakeKeyring`, a keyring helper over a JSON
  file in that folder (`MALACHI_FAKE_KEYRING_FILE`): no password reaches
  Credential Manager. The app's terminal log is its stderr, a pipe the test
  reads. They find every element by AutomationId (the names follow the
  user's language) and use UI Automation's patterns only, never synthetic
  input, so they need no foreground. Checked on an empty data folder: the
  main window with its New Message and primary menu and, once the daemon
  answered, its No Accounts page; the sidebar's New Message opens a
  composer (To, Subject, the editor) that closes without a question; the
  primary menu's Preferences (its navigation), Add Account… (the wizard on
  its identity page, modal, the main window enabled again after it) and
  About (the executable's version); the No Accounts page's Add Account…;
  and Quit from the primary menu: the app exits with 0, the daemon it
  started exits with 0, and the socket and its key are gone; and the
  preferences' key: a `window-maximized` the session wrote into its key
  before the start shows as a maximised main window, and the key is gone
  after the session. The classes
  share one collection without parallelism (the app is one instance per
  executable); one app serves the window checks, one the Quit, one the
  preferences; about 30 s. **Opt-in**, `MALACHI_DEVMAIL=<folder with devmail.exe>` (the local
  IMAP and SMTP server of the port's research, outside the repository)
  adds a suite against a mail server: devmail started on free ports of
  127.0.0.1 and seeded with `backend/testdata/mime`, the account added over
  the app's socket as a client adds one (`account.add` through Core's
  `RpcClient`), then the Inbox listed, a message selected and shown in the
  reader (its subject and sender), Reply opening a composer the daemon
  prepared (`Re:` and the sender; the quote itself is inside WebView2,
  which UI Automation does not reach), and a message sent from a composer
  arriving in the server's INBOX with its copy in Sent; about 45 s with the
  smoke tests. Skipped with the reason: not Windows, no interactive
  desktop, the app not built, or `MalachiMail.exe` of that folder running
  already (a test's launch would only activate it). The user's
  `HKCU\Software\io.github.schotek.Malachi` is neither read nor written
  (a Quit writes the window geometry back into the session's key). Not
  isolated: the notification registration, which Windows keys by the
  executable's path (`HKCU\Software\Classes\AppUserModelId\<path>`, §10)
  and which every start of the app folder writes, a test's as a user's.
  They run in `build.ps1 test` with the rest of the
  solution (a stale app folder tests the old app: `build.ps1 app` first),
  or alone:
  `dotnet test --project tests\Malachi.App.UiTests\Malachi.App.UiTests.csproj -p:Platform=x64`
  from `windows\`.
- **End to end, by hand** (phase F1, 2026-09-28, against devmail with real
  input where keys matter; the app from a temporary data folder): New
  Message from the sidebar, Ctrl+N and the primary menu; Reply from the
  command row, Reply All and Forward from the context menu (right click
  and Shift+F10), Reply All (with its Cc) from a message window's row,
  Ctrl+R, Ctrl+Shift+R and Ctrl+Shift+F from the list, Ctrl+Shift+F in a
  message window and Ctrl+R with the viewer focused, each composer
  addressed and titled by the daemon (`draft.create`; the quote looked at
  in a reply); a draft opened by the Edit banner, Enter
  (raising the composer already editing it) and a double click; the primary
  menu's Add Account… (and a whole account added through the wizard),
  Preferences (also Ctrl+Comma), About and Quit, labelled with the
  Windows-only *Quit* (no msgid in `po/malachi.pot` fits); the No Accounts
  page's Add Account… (fixed); the sign-in banner's and the status
  flyout's Edit Account… with a changed server password (the wizard asking
  for it, the banner gone after the save); a second launch with a
  `mailto:` URI opening only a composer, also with the main window hidden;
  F5 and the status line; search from Ctrl+F and Ctrl+E with the three
  scopes, Enter to the first result and Escape back; Trash, Archive, Junk
  (their questions) and the Star toggle from the command row, Mark as
  Unread and Unstar from the context menu, and A, J, U, S and Delete with
  the viewer focused, not while typing in the search box; an attachment
  previewed, opened (the open directory's copy in zone 4; a `.log` has no
  association: *Open With*) and saved (zone 4); a message to
  test@example.test sent with Ctrl+Enter, delivered and in Sent (it shows
  in the Inbox after F5: the daemon's known missed EXISTS after its own
  APPEND, devmail README); autosave to the Drafts folder on the server and
  the close question; Quit with a dirty composer saving it without asking
  and stopping the daemon (socket and key gone; on a machine starved of
  CPU the daemon once took 74 s over a `draft.save`, the save timed out
  and Quit asked the close question instead, whose Save Draft then saved
  it as a new draft and quit, as `draft.go` does); Run in Background (the
  window hides, the icon's Open, New Message, Check for New Mail and Quit,
  a second launch shows the window), a `--background` start (fixed: no
  icon) and the keyboard after the window came back (fixed). Not
  confirmed: a notification's click. Run outside Claude Desktop's process
  tree (§1) with the main window hidden, the app registered (its
  `AppUserModelId` and activator keys appeared and were removed afterwards)
  and took the new message, but no banner could be found to click: UI
  Automation in that session did not see a toast that Windows PowerShell
  posted as a control either, so the check needs a person's eyes. The
  click's own path is phase E's (verified there with the notification
  service in a probe) and ends in the same activation as a second launch,
  which was walked. The certificate banner was not walked either (devmail
  has no TLS; its button is the same `EditAccount` hook as the sign-in
  banner's).
- The Go side on Windows: `go vet ./...` and `go test ./...` of `backend/`
  green (§14), the helper's real round trip with `malachi-credentials.exe`.

## 13. Build and CI

`make windows` builds `build\malachid.exe` and `build\malachi-mcp.exe` (the
root Makefile's Go targets, with the `.exe` suffix on Windows) and delegates
to `windows/build.ps1 app`; `run-windows` and `test-windows` likewise. On
Windows the root Makefile runs its POSIX recipes with the `sh` of Git for
Windows, from PowerShell as from Git Bash (verified); elsewhere the three
targets print a hint, like the macOS ones on Linux. `build.ps1` builds the
Go binaries per architecture (`GOOS=windows`, `CGO_ENABLED=0`,
`GOWORK=off`, `-trimpath`, `-ldflags -X main.version`), publishes the
solution self-contained with `EnableMsixTooling=true` (without it publish
drops the `.pri` and the app dies with `0xC000027B`), renders the icon,
copies the catalogues and licences, and assembles
`build\windows\<arch>\Malachi Mail\`. Every `dotnet` command runs from
`windows/` (global.json selects the test platform). The XAML compiler is a
.NET Framework tool without long-path support: keep the clone path short.
Version: `git describe` as in the Makefile; `Major.Minor.Patch.Commits` for
the file version, the full string as `InformationalVersion`. `build.ps1
package` zips the app folder as `build\windows\Malachi-Mail-<version>-<arch>.zip`
(entries with `/` separators, so every unzip tool reads them); the zip is
unsigned and is not a release artefact yet (§17, [releasing.md](releasing.md)).

`build.ps1 lint` is `dotnet format --verify-no-changes` over the solution
and the conventions tests. Warnings are errors in every project
(`Directory.Build.props`, code style in the build), so a Release build
without warnings is part of the build itself.

**CI** is `.github/workflows/windows.yml`, on the `windows-2025` runner
image: Windows Server 2025 with Git, the WebView2 runtime and Visual
Studio 2022, whose C++ workload includes the MSVC build tools for x64 and
ARM64 (`Microsoft.VisualStudio.Component.VC.Tools.ARM64`, in the image's
toolset as of 2026-09-28), which is what the NativeAOT keyring helper links
with. The label is named, not `windows-latest`, so the move to the next
Windows Server is an edit of the workflow; the image's tools (Visual
Studio, the WebView2 runtime) are still updated with it every week or two.
The workflow runs on pushes to `main`, on `v*` tags, by hand
(`workflow_dispatch`) and on pull requests that touch `windows/`,
`backend/`, `po/`, `docs/malachi_icon.png`, the Makefile or the workflow,
or what the tests read outside `windows/`: the GTK UI's sources (the drift
tests compare the timeouts, the editor bridge, the viewer's document and
the icon names with them), the gschema, `docs/api.md`, the licences that
go into the app folder, `.gitattributes`. Its jobs:

| Job | Steps | Uploads (30 days) |
|---|---|---|
| `go` | `go vet ./...` and `go test -count=1 ./...` in `backend/` with `CGO_ENABLED=0` and `GOWORK=off`, as the daemon is built | |
| `client` | `dotnet restore Malachi.slnx -p:Platform=x64 --locked-mode`; `build.ps1 build -Arch x64` (Debug; every warning is an error through `TreatWarningsAsErrors`); `build.ps1 go`, so that the tests against the real daemon run, as under `make test-windows`; `build.ps1 test`; a job summary of every project's counts and of every skipped test with its reason; `build.ps1 lint`; `build.ps1 package -Arch x64` | `malachi-windows-x64` (the zip), `windows-test-results` (the `.trx` files, also when a test failed) |
| `arm64` | the locked restore and `build.ps1 build` for ARM64, then `build.ps1 package -Arch arm64`: Go cross-compiles, the app is published for `win-arm64`, the helper is NativeAOT cross-linked with the image's ARM64 tools. Nothing ARM64 runs on the x64 runner, so there are no tests. A job of its own, though one build tree takes both architectures one after the other (every project built for an architecture has output folders of its own, `Directory.Build.props`): it runs beside `client` rather than after it, has its own NuGet cache, and a failed ARM64 link leaves the x64 results apart | `malachi-windows-arm64` |
| `release` | on a `v*` tag, once the three jobs pass, and only when the repository variable `WINDOWS_RELEASE_ZIPS` is `true`: the zips attached to the tag's release, a draft created when there is none, as the Flatpak, Debian and RPM workflows do. Unset, the job is skipped and the release gets no Windows zips (below) | |

Every client step is a `build.ps1` target started as make starts it
(`powershell -NoProfile -ExecutionPolicy Bypass -File build.ps1 …`), so CI
builds what a desktop builds; pwsh judges a step by the exit status of its
last program, so each step runs one. Go is the minor version of
`backend/go.mod`'s `go` directive at its newest patch release (`go 1.25.0`
→ the newest 1.25.x, as the Linux workflows' `'1.25'`): setup-go's
`go-version-file` would install 1.25.0 itself, without the security fixes
the zips must carry. The .NET SDK comes from `windows/global.json` through
`setup-dotnet`. The NuGet packages folder is cached per architecture, keyed
by the lock files and `Directory.Packages.props`; the runner sets `CI=true`,
which makes every restore locked (`Directory.Build.props`), and the
explicit `--locked-mode` restore fails first, naming the project, when a
lock file does not match.

What CI leaves out, and says so: the network canary skips itself on a
runner (`GITHUB_ACTIONS=true`) with the reason that it needs a desktop
session with the WebView2 runtime; a hosted runner may have a desktop, but
its Server image, graphics and runtime change with the image, so a verdict
there would be about the runner. A manual run with `canary` set passes
`MALACHI_CANARY=1` to try it anyway (not yet tried on the hosted image).
The Credential Manager round trip needs `MALACHI_CREDENTIALS_TEST=1`; tests
that find no taskbar or may not create symbolic links skip themselves with
the reason; the job summary lists every skip, and a notice on the run
points to it. Nothing ARM64 runs (§17). The UI smoke tests (§12) skip
themselves with the reason that the app is not built: the test step runs
before the package step assembles the app folder, and whether the hosted
image's session can drive a WinUI window through UI Automation is not
tried yet. The Linux packaging workflows ignore pushes that change
nothing but `windows/`, `macos/` or this workflow; tags always build
everything (GitHub does not apply path filters to tags).

The jobs run the tests as they are, without retries. The workflow's
first run on GitHub (2026-09-28, the merge of the Windows client into
`main`) built, tested and packaged the client for both architectures (the
ARM64 helper's NativeAOT link included) and failed only in `backend/`,
on a runner about three times slower than the development machine, in
two tests, for two causes. `internal/imap`
`TestClientSideWindowAcrossBatches` synced its 2,500 messages in 19 s
against a 15 s bound; `TestCallsBeforeHandshakeAreRejected` and `pkg/api`
`TestHandshakeTimesOut` (a 150 ms context that ended before
`system.hello` was written) had failed the same way locally under load.
These bounds now wait for what the test expects rather than time it: the
IMAP and Graph harnesses up to 60 s (the Graph one after
`TestTokenProblemsAndRecovery` outran its 10 s in a full local run), the
RPC tests up to 30 s (`waitLimit`), the
handshake test with a 1 s context that `HandshakeTimeout` must not beat;
a passing run returns at once. So that a longer wait cannot let the
pre-auth timeout do a close the test attributes to garbage, the byte
budget or the cap, the RPC test server gives a connection an hour to
authenticate. `internal/rpc` `TestPendingConnectionsAreCapped` (a refused
connection still open after 5 s) was not the runner's speed but Windows's
AF_UNIX: a connection the server closes within about a millisecond of
accepting it may never show the close to a peer that only reads (10 of
2,000 such closes in a probe, none once the peer wrote first or the
server waited a millisecond), and the daemon closes a connection over its
cap at once. A client writes `system.hello` first and sees the close, so
the daemon is unchanged; the test's refused connection now sends hello
too (the old form failed 18 runs of 300 locally, the new none, and a cap
switched off in `server.go` fails it at once on the hello's answer). One
more race surfaced in the run after that: `internal/core`
`TestEndToEndAttachmentsOnDemand` expunged the message on the server while
its syncer still ran, whose IDLE could remove the message locally before
`message.download` asked for it (`messageNotFound` for the
`messageGone` the test checks); the syncer now stops first. And
`TestSystemStorage` asked for a running conversion right after compression
was switched on while its loop ran on a 1 ms tick, which had sometimes
converted the store's one message already; it now requires running until
every message is converted, idle only once all are.

The client's tests that a busy machine failed the same way no longer
depend on its speed, and check what they checked: the bridge runner's
timeout and cancellation pass on a fake clock, and a run that ends while
the stand-in would still sleep is one whose process was killed
(`BridgeRunnerTests`, which bounded a run by 3 s of wall clock, the
stand-in's start included); the attachments test finds the picture by
its name, whichever of the two imports in flight answers first
(`ComposeAttachmentsControllerTests.RemovingForgetsTheFileAndTellsTheBackend`);
the dropped connection's test stops the UI thread in the turn that starts
the attempt, where a test thread held up by the machine let the
connection be handled first
(`ConnectionControllerTests.SystemInfoOfADroppedConnectionIsDropped`,
`infoFailed` where `unavailable` was expected; with the test's thread
paused 300 ms after the start, the old form failed 20 runs of 20, the
new one none); and the tests against the real `malachid.exe`
(`RealDaemonTests`, `ConsoleAttachmentTests.AnAttachedAppStopsTheRealDaemonCleanly`,
`DaemonProcessHostTests.TheRealDaemonStopsCleanlyInAPrivateRunDirectory`,
which failed together once while other builds kept the machine busy)
give it three times the app's limits, 45 s to listen and 15 s for the
first handshake (`RealDaemon.StartLimit` and `HandshakeLimit`), since only
the start of a real process has to be waited for. The network canary,
which CI skips, names the background request its browser makes about a
minute after it started (§12), which a run slowed down by a busy machine
lasts long enough to meet, waits up to 120 s for its browser to end
(more than 60 s once with every core busy and the solution being rebuilt
beside it), and waits for each click that must reach the reader as a
link to do so before the next (a fixed 500 ms let a masked link's click
count for the next one). With a busy loop per logical processor, and in
half the runs the solution rebuilt in a loop beside them, three of six
runs of `build.ps1 test` failed in the canary before these changes;
three runs with all of them, the rebuild loop included, passed
(2026-09-28).

The zips are test builds until §17 is done (no signature, no installer,
the licence permission for the Microsoft components not yet in
`LICENSING.md`), so a tag keeps them as the run's artifacts and attaches
nothing to its release: the `release` job runs only when the repository
variable `WINDOWS_RELEASE_ZIPS` is `true`, which nothing sets by
default. The owner sets it (*Settings → Secrets and variables → Actions →
Variables*, or `gh variable set WINDOWS_RELEASE_ZIPS --body true`) once
the permission is in `LICENSING.md` and the workflow signs the
executables; [releasing.md §7](releasing.md#7-windows) has what that
means for a release. Every step of the three jobs was run locally in
order from a fresh build tree, on Windows 11 x64 with `CI=true` and
`GITHUB_ACTIONS=true`, before the workflow was committed; the one step
that could not pass there is the ARM64 helper's NativeAOT link, for want
of the MSVC ARM64 build tools on that machine; on the hosted image it
links (first run, 2026-09-28: `malachi-windows-x64` 87 MiB and
`malachi-windows-arm64` 83 MiB as run artefacts).

## 14. Backend and repository changes

Each its own commit on `feat/windows`, platform-neutral, no build tags:

1. **`.gitattributes`** (`* text=auto eol=lf`, `**/testdata/** -text`,
   binaries, CRLF for `.cmd`/`.bat`), committed alone. Windows clones are
   re-checked-out once (`git rm -r --cached -q . && git reset --hard` on a
   clean tree; never `git add --renormalize` on an old CRLF worktree, which
   would commit CRLF into 48 LF fixtures). Fixes gofmt on 371 CRLF files,
   CRLF copies of LF test fixtures and CRLF-embedded migrations.
2. **Keyring helper path check** (blocker): `helper.checkPath` tests Unix
   execute bits, which Go never reports on Windows, so
   `MALACHI_KEYRING=helper` refuses every helper and the daemon exits.
   `exec.LookPath` decides instead (execute bits on Unix, the extension on
   Windows); the helper tests stop using `syscall.Kill`.
3. **Stored files on Windows**: the raw store (`internal/store/raw.go`,
   `raw_maint.go`, from main) relies on POSIX, where a file that is open
   can be renamed over or removed; Windows refuses both while any handle
   of it is open (Go opens files without delete sharing), the process's
   own included. Without platform code:
   - nothing renames over or removes a file its own call path still has
     open: the outbox worker and the Sent `APPEND` close before they drop
     the copy, `ingest.Strip` before it commits the skeleton over the
     file it read, the conversion before it removes its source, and a
     commit closes the staged file before renaming it into place;
   - every rename and removal of a message's file retries for about
     1.3 s (`internal/fsretry`, once the daemon's `retryFileOp`) while
     holding the message's names lock, so the readers that have the file
     open (`message.body`, `message.part`, `draft.create`) finish and no
     new one opens it; deletions of many files share an `fsretry.Batch`,
     and the attachment files get the same (the merge of main had
     dropped it);
   - a reader that holds the file for longer makes the operation
     `store.ErrBusy` (the store counts its open readers per message, at
     every attempt, so that one that lets go just after the last attempt
     still counts), the file as it was: the conversion and the sweep count
     the message busy and come back to it, a deletion leaves the file to
     the sweep, a commit undoes phase A's widening of the remote parts,
     the attachment step passes the message over to its next pass, logged,
     not as a failure, and `message.download` answers `unavailable`, to be
     tried again;
   - a commit whose rename fails for any cause, a reader the store does
     not count included (another program, a virus scanner), undoes phase A
     as well, since a failed rename leaves the stored file whole; only the
     error is then not busy (the attachment step tries the message once
     more before it keeps it whole). Not so when the new file is gone from
     its temporary name after the failure: a rename that went through but
     reported a failure (a reply lost on a network file system) is retried,
     and the retry finds nothing to rename, so the new file may be in
     place and phase A stays, on the safe side. An account deleted while a reader
     holds one of its files keeps its directory until the sweep, which
     removes it whole: `store.DeleteAccount` records it in `meta` until it
     is gone, since the sweep otherwise leaves a directory with files of an
     account it does not know to the other store that may share
     `messages/`. Of a message left with both variants (a replacement
     whose removal of the other was busy) a reader takes the newer, the one
     the sweep keeps, so a codec switched back before the sweep serves no
     replaced content;
   - a failure that lasts but could be a handle's refusal (a directory
     without write permission, `EACCES`) now takes the retries, about
     1.3 s, on every system, holding the message's names lock, so its
     readers wait with it. Rule 4 leaves no other way: the refusal a
     handle causes has no portable error value (a sharing violation, or
     access denied, which a lasting failure also is), and telling the
     systems apart would take a build tag or a platform check. It is
     acceptable because such a failure is rare and already an error that
     the operation reports, the wait is bounded, and a deletion of many
     files gives the retries up after the first failure that outlasts
     them (`fsretry.Batch`), so it costs one wait, not one per file. What
     can never be a refusal returns at the first attempt: a missing file,
     and a read-only file system, a full disk or quota, a rename across
     file systems, a name that is not the directory or file it is taken
     for (`EROFS`, `ENOSPC`, `EDQUOT`, `EXDEV`, `ENOTDIR`, `EISDIR`,
     portable `syscall` values that Unix systems report; Windows reports
     its own codes for these, which are retried with the rest);
   - `os.SameFile` of an `os.Lstat` result reads the file's identity
     lazily, by path, on Windows: the conversion's check that a writer
     replaced neither file reads both identities at once;
   - a file is flushed through a handle that may write, which
     `FlushFileBuffers` requires.
   On Linux and macOS these calls succeed at the first attempt unless a
   lasting failure refuses them (above); nothing else changes there but
   when a handle is closed, which of two variants a reader takes, and the
   sweep's removal of a deleted account's directory that a crash left.
4. **Portable backend tests**: separators, modes (`permOf`), JSON-escaped
   paths, closed handles, the FIFO and kill cases redesigned, a staging
   area broken by a file in its place rather than a read-only directory
   (Windows ignores a directory's read-only attribute when files are
   created in it), so `go test ./...` is green on Windows and a Windows CI
   job can gate the daemon. A race in the raw maintenance loop that ran a
   restarted step twice, which Windows's timers made frequent, is fixed in
   the loop.
5. **`malachi-mcp status|install|uninstall`**: additive
   `--claude-desktop-config PATH` (the MSIX Claude Desktop reads
   `%LOCALAPPDATA%\Packages\Claude_…\LocalCache\Roaming\Claude\…`, which the
   bridge does not know) and `--command PATH` (what to register instead of
   `os.Executable()`); Windows rows in [mcp.md](mcp.md). The Windows client
   supplies the Windows knowledge; `backend/` keeps none.
6. The root **Makefile**: the Windows block, the `.exe` suffix, the three
   targets.
7. **`.github/workflows/windows.yml`**: the backend's `go vet` and
   `go test` on Windows beside the client's jobs (§13); the Linux
   packaging workflows skip pushes that change only `windows/`, `macos/`
   or that workflow.

Proposed separately, not in this branch: canonical hrefs in the sanitiser
plus GTK confirming unlisted links (the likely masked-link bypass), the
Windows masked-link rules in GTK and macOS (every listed link with the
clicked href judged, the text cleaned, normalised and read as §6.4
reads it), a link text in the sanitiser that does not put a space
between inline elements, flags when it cut the text at its cap and
leaves out (or flags) what CSS hides, and a report of a bidi override
inside a link (§6.4's known limits),
bridge DOM-clobbering hardening in GTK and macOS, the macOS flush-echo
order, portable names in `safename`, an own extension→content-type table,
a runtime D-Bus opt-out, the same display-text rule in GTK and macOS
(bidi formatting and control characters out of the names, subjects and
folder names they show, a name isolated from the address after it;
security.md §4), and clean subjects and display names in `draft.create`
(a reply's To and Subject fields show, and send, the received text, an
override included; only the daemon can change what is sent).

## 15. How it was built

The client was built on `feat/windows` in six phases, all done. Each phase
was one orchestrated run: coding agents on disjoint areas, each in its own
git worktree and branch, each gated by the build and the tests of its
area; then the branches were merged into `feat/windows`, the full gate
ran, and a review stage (a parity review against the Swift and Go
sources, and a security review for the transport, WebView2, attachments
and the helper) had to pass before the phase's commits stayed. The fixes
those reviews asked for are the `fix(windows):` commits after each
phase's `feat(windows):` ones.

| Phase | Work packages | Gate | State |
|---|---|---|---|
| **A** Repository groundwork | §14 items 1–6 | `go vet` + `go test ./...` of `backend/` on Windows; Linux test binaries run in WSL; `GOOS=darwin go vet` | Done |
| **B** Scaffold and spikes | solution, props, packages, `.editorconfig`, projects, `build.ps1`, icons, manifest, make targets; spikes: the WinAppSDK 2.5 package set with `AppNotificationManager.Register` unpackaged, CommunityToolkit controls on 2.5, `TitleBar`, keyboard with a focused WebView2, `ContentDialog`/`Flyout` over WebView2 | `make windows`, `run-windows`, `test-windows` from PowerShell and Git Bash; a window opens | Done |
| **C** Core foundation | C1 API layer; C2 i18n, text, settings; C3 Platform.Windows services; C4 `malachi-credentials`; then C5 transport and FakeDaemon; C6 supervisor, paths, bridge runner | all Core tests; the handshake against the real daemon; `account.add` with a password stored through the helper | Done |
| **D** Core logic | D1 models; D2 compose, HTML (bridge), wizard; D3 connection, sync, message cache, mailbox controllers; D4 actions, compose, draft, wizard, preferences, MCP controllers | every ported Go and Swift test green; conventions tests green | Done |
| **E** WinUI app | wave 1: E1 shell (`Main` with the console, notifications and single instance; lifecycle and Quit; integration; toasts, alerts, icons, theme, `{l:T}`; the command router; the package set of §10), E2 WebView2 layer and the canary, E7 platform services (tray, launch at login, `mailto:` registration, notifications and sound); wave 2: E3 main window, E4 reader, message and attached-message windows, attachment actions and the previewer, E5 compose, E6 wizard and preferences. Each screen's agent also wrote the presentation classes of §7.4 it needed, in Core with tests | release build without warnings; all tests; the canary; FlaUI smoke tests; a run against the local test mail server | Done; the UI checked by hand through UI Automation, the smoke tests written in phase F (without FlaUI, §12) |
| **F** Verification and docs | end-to-end against local IMAP/SMTP servers and the UI smoke tests; the parity matrix walked with evidence; security review; `windows/README.md`, this document, CLAUDE.md/AGENTS.md, README, architecture, security, mcp, releasing, LICENSING; CI | everything above, on a clean clone | Done; CI checked locally step by step, then green on GitHub for the client at its first run (§13) |

## 16. Research summary

The port was planned from nine research reports of 2026-09-27 (the macOS
client's core infrastructure and its logic, its AppKit shell in two parts,
the GTK UI's inventory with the parity matrix, the backend on Windows,
WebView2 security with attachment safety and credential storage, the
Windows app model, build and infrastructure) and three spike runs of the
same day: WinUI 3, WebView2 and AF_UNIX; the app model on Windows App SDK
2.5.1 (notifications, single instance, activation, the tray, launch at
login, `mailto:`); and the keyboard with a focused WebView2, overlays over
it and the console. The reports and spikes are working material outside
the repository; what they established is in the sections above, where
they are cited as *measured*.

Measured, not read: the daemon and `malachi-mcp` build and run on Windows
unchanged for amd64 and arm64, once the four backend fixes of §14 were in;
AF_UNIX and the §1.4 handshake work from .NET (vectors match); CTRL_BREAK
stops the daemon cleanly; the WebView2 facts of §6; a hand-written
unpackaged self-contained WinUI 3 app builds with plain `dotnet build` in
~14 s for x64 and ARM64 and starts in ~0.7 s to its first WebView2
navigation (~0.5 s with NativeAOT, which the app does not use);
publish needs `EnableMsixTooling`; trimming needs source-generated JSON;
the WinAppSDK 2.5.1 metapackage adds ~60 MB of AI libraries a mail client
does not use; `AppNotificationManager.Register()` fails unpackaged without
the Insights resource DLL (§10); no XAML key handler sees a key while a
WebView2 has focus, but the island's pre-translate source does (§11.5).
The macOS client mirrors GTK faithfully: 502 of the 556 GTK msgids appear
in its sources, and every miss is explained; those 54 seeded
`windows/parity-exclusions.txt`.

The parity report's matrix (every GTK feature against its macOS mirror and
its Windows counterpart) and its validation list are what the Windows
client was checked against: the msgid coverage and the gschema tests
(§9, §12), every matrix row the same or a row of the deviation table in
`windows/README.md`, the actions, the keys and the dialogs, and the manual
scenarios (a notification suppressed while the window is active, the sound
without notifications, a click on a toast, a hidden launch at login, Run
in Background, a cold `mailto:`, a masked link, an executable attachment,
Save All's names, reordering accounts, the AI page's failures, the
daemon's graceful stop), recorded in §10 and §11.

## 17. Before a public release

The client is complete as a mail client; what it lacks is distribution.
Today `make windows` gives a self-contained, unsigned folder that needs
nothing installed but the WebView2 runtime Windows 11 comes with, and
`build.ps1 package` zips it. Open, in the order a first public binary
needs them:

- **Licence.** The Windows App SDK and WebView2 packages, which the app
  folder carries, are under Microsoft's terms, not the GPL. Distributing
  the built client needs an explicit GPLv3 §7 additional permission for
  those Microsoft platform components in `LICENSING.md`. That is the
  owner's decision, after a legal check, and comes before the first public
  binary; development and the source are unaffected.
- **Code signing.** The owner's decision (SignPath Foundation, an OV
  certificate, or signing as an organisation; Azure Artifact Signing is not
  open to individuals in the EU). Until then SmartScreen warns about the
  unsigned executables.
- **Installer.** Velopack (per user, updates from GitHub Releases) and a
  winget manifest; an update stops the daemon gracefully first and must
  cope with a `malachi-mcp.exe` that Claude Desktop or Claude Code still
  runs (§1: it can be renamed, not replaced), and an
  uninstall removes what the app registered in HKCU (the `mailto:`
  registration, for which `MailtoRegistration.Unregister` exists, the Run
  value and the notification registration).
- **ARM64.** `build.ps1 app -Arch arm64` cross-builds the app, the
  daemon and the bridge; the keyring helper links only where the MSVC
  ARM64 build tools are installed, which the development machine lacks,
  so no ARM64 app folder has been assembled and none has run. It needs a
  build with those tools and a run on real ARM64 hardware.
- **The UI smoke tests on CI.** `.github/workflows/windows.yml` (§13)
  runs on the hosted runner (first run 2026-09-28: the client built,
  tested and zipped for both architectures, the ARM64 helper linked with
  the image's MSVC ARM64 tools); whether the UI smoke tests (§12) can
  drive the app in the runner's session is open (the test step runs
  before the app folder exists, so they skip).
- **A notification's click** checked by a person (§12: UI Automation did
  not see the toast).

Later tracks: MSIX (virtualisation disabled, an execution alias for
`malachi-mcp`), Windows Web Account Manager as a daemon extension point, a
taskbar unread badge, and the separate proposals at the end of §14.

The Jira accounts and the conversation view of the reading pane, where
the client trailed the daemon's API until 2026-09-30, are ported (§11.7,
the card's view §6.7); of them only a walk against a real Jira Cloud site
is open, which takes the owner's own token and an issue they name for the
comments.
