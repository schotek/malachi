<!--
SPDX-FileCopyrightText: 2026 Vladislav Janeček
SPDX-License-Identifier: GPL-3.0-or-later
-->

# The Windows client

How the WinUI 3 client in `windows/` is designed and built, for people (and
agents) who change it. It is the Windows sibling of
[macos-port.md](macos-port.md) and follows the same model: a client of the
daemon's API, a mirror of the GTK UI, no mail logic of its own.

**Status: plan, 2026-09-27; implementation in progress on `feat/windows`.**
§0 records the decisions, §15 the work plan and its gates. As the phases
land, the sections turn from plan into description, the way macos-port.md
did; §17 keeps what is still open.

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
§16).

| Topic | Decision |
|---|---|
| Language, runtime | C# on .NET 10 (LTS), WinUI 3 on the Windows App SDK **2.5.x** (1.8 left servicing on 2026-09-24): the component packages `Microsoft.WindowsAppSDK.WinUI` and `.InteractiveExperiences` instead of the metapackage, which adds ~60 MB of AI libraries a mail client never uses (measured; the notification fix of §10 comes with it) |
| Windows versions | Windows 11 only (`TargetPlatformMinVersion` 10.0.22000.0) |
| Architectures | x64 and ARM64 (ARM64 is built here, run on real hardware before a release) |
| Packaging | **Unpackaged, self-contained**, per user. `make windows` assembles a folder like the macOS `.app`; the app registers itself in HKCU. Installer (Velopack + winget), code signing and the licence permission for Microsoft components come before the first public binary (§17). MSIX is out: its AppData virtualisation hides `config.toml` and the store and breaks the Claude registration, the same reason macOS has no App Sandbox |
| Entry points | `make windows`, `make run-windows`, `make test-windows`, from PowerShell and Git Bash (GNU make 4.4 from winget `ezwinports.make`); `windows/build.ps1` does the work |
| Stopping the daemon | `CTRL_BREAK_EVENT` through `AttachConsole`/`GenerateConsoleCtrlEvent` (Go maps it to SIGINT; verified clean exit in ~18 ms), `Kill` after 15 s. No backend change; GTK/macOS semantics kept (a daemon left behind by a crashed UI is adopted, never stopped) |
| Secrets | `malachi-credentials.exe`, the helper keyring over **Windows Credential Manager**, values above the 2560-byte blob limit split into hash-checked chunks |
| Main window | **GTK structure**: a command row per pane under a slim title bar that holds the search box; the primary menu behind a `…` button (no menu bar); GTK back navigation below 900/600 px; the status line across the whole bottom edge |
| Keyboard | Windows scheme: Ctrl+R Reply, Ctrl+Shift+R Reply All, Ctrl+Shift+F Forward, F5 Check for New Mail, Ctrl+F/Ctrl+E search; a setting `ctrl-r` (`reply`, default, or `refresh`) as macOS's `command-r`; otherwise GTK's keys, Ctrl+Q Quit and A/J/U/S/Delete included (no Outlook aliases: a pre-translate handler delivers every key even with a WebView2 focused, §11.5) |
| Attachment click | An **own previewer**: images, PDF and text in a locked-down WebView2 window (no network, no script, no temporary file); other types offer Open / Save As; programs are never opened |
| Windows additions | Notification-area icon while running in the background; context menus on messages and folders; a *Default apps* button in Preferences; dirty drafts saved on Quit |
| Unlisted links | Confirmed before opening, as on macOS (GTK opens them; see §6.4) |
| Dependencies | CsWin32, CommunityToolkit.WinUI Controls, CommunityToolkit.Mvvm, Microsoft.Extensions.Logging.Abstractions / TimeProvider.Testing; xUnit v3 for tests. Each justified in its commit (CLAUDE.md) |
| Backend changes | Four platform-neutral fixes on the branch, each its own commit (§14): the helper path check, the orphaned raw files after sending, `malachi-mcp --claude-desktop-config/--command`, and `.gitattributes` + portable Go tests + Windows CI |
| Settings store | *architecture*: `HKCU\Software\io.github.schotek.Malachi`, the gschema keys, change notification through `RegNotifyChangeKeyValue` (the counterpart of GSettings signals and macOS KVO) |
| Data | *architecture*: `%LOCALAPPDATA%\Malachi Mail\` for `config.toml`, `store.db`, logs, the WebView2 data and the open directory; the socket stays at the daemon's default outside AppData |
| Translations | *architecture*: `po/*.po` parsed at run time (no generator, no Python in the Windows build), GTK msgids as keys as on macOS |
| Tests | *architecture*: xUnit v3 on Microsoft.Testing.Platform; the Core tests run on any OS; the Go UI and Swift tests ported 1:1 |
| Preferences | *architecture*: named *Preferences* (the translated GTK msgid), no search field in v1 (as macOS) |

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

| What | Where |
|---|---|
| App folder (dev) | `build\windows\<arch>\Malachi Mail\` with `MalachiMail.exe`, `malachid.exe`, `malachi-mcp.exe`, `malachi-credentials.exe`, `locale\<lang>.po`, licences |
| Configuration | `%LOCALAPPDATA%\Malachi Mail\config.toml` (`--config`) |
| Mail store | `%LOCALAPPDATA%\Malachi Mail\store.db` (`--store`), lock `store.db.daemon.lock` |
| RPC socket | `%USERPROFILE%\.cache\malachi\run\rpc.sock`, the daemon's own default (`MALACHI_SOCKET` overrides). Outside AppData on purpose: `malachi-mcp` and `.mcp.json` work unchanged, and nothing under AppData is exposed to MSIX redirection (Claude Desktop is itself MSIX, and its children see a virtualised AppData) |
| RPC key | `rpc.sock.key` beside the socket; read afresh per connection with `FileShare.ReadWrite \| FileShare.Delete` |
| Daemon and app logs | `%LOCALAPPDATA%\Malachi Mail\logs\` (and the terminal under `make run-windows`) |
| WebView2 data | `%LOCALAPPDATA%\Malachi Mail\WebView2\` (InPrivate profiles; only browser-level state is written) |
| Attachments being opened | `%LOCALAPPDATA%\Malachi Mail\open\<random>\` (protected DACL, cleared at start and exit, entries older than an hour swept) |
| Preferences | `HKCU\Software\io.github.schotek.Malachi` |
| Passwords, sign-ins | Credential Manager, generic credentials `io.github.schotek.Malachi/<accountId>/<key>` |
| Launch at login | `HKCU\…\CurrentVersion\Run` value `Malachi Mail` = `"<exe>" --background`, `StartupApproved` respected |
| `mailto:` | `HKCU\Software\Classes\io.github.schotek.Malachi.mailto`, `HKCU\Software\Clients\Mail\Malachi Mail\Capabilities`, `HKCU\Software\RegisteredApplications` |
| Notifications | `AppNotificationManager`, AUMID `io.github.schotek.Malachi` |

Development overrides, as on macOS: `MALACHI_DAEMON` (path, or `none`),
`MALACHI_SOCKET`, `MALACHI_KEYRING`/`MALACHI_KEYRING_HELPER` (a preset
`MALACHI_KEYRING` wins over the bundled helper), `MALACHI_LOCALE_DIR`, and
one Windows-only variable, `MALACHI_DATA_DIR`, which replaces
`%LOCALAPPDATA%\Malachi Mail` for tests and agents. An agent running inside
Claude Desktop's process tree must use it: new files under AppData are
silently redirected into Claude's package store there, and so are writes to
HKCU (measured), so an agent that tests settings, `mailto:` registration,
launch at login or notifications starts the app **outside** that tree
(for example through WMI `Win32_Process.Create`).

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
  scripts/make-icons.ps1        docs/malachi_icon.png → .ico (crop as macos/Makefile)
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
```

| Project | May use | Holds |
|---|---|---|
| `Malachi.Core` | BCL, System.Text.Json (source generated), CommunityToolkit.Mvvm, Microsoft.Extensions.Logging.Abstractions | The typed API, the transport and handshake, the daemon supervisor's state machine, the pure logic ported from the Go UI and macOS (models, threads, folding, favourites, search, address parsing, quoting, wizard fields, HTML documents, the editor bridge, formatting, error texts), the controllers, the presentation classes macOS keeps in AppKit (§7.4), settings and i18n. `IsAotCompatible`. |
| `Malachi.Platform.Windows` | Core, CsWin32 | Process host (spawn, CTRL_BREAK, kill), key-file policy (owner SID, DACL), private directories (protected DACL), registry settings backend and its watcher, Mark of the Web (`IAttachmentExecute`), dangerous types (`AssocIsDangerous`), open directory, launcher, launch at login, `mailto:` registration, preferred languages, tray icon interop |
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
  WebView2 views of §6;
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
provider rows use the generic icon (U6); notification titles are capped
(U7); About carries the GTK fields (U8); there is no Help item (U9); the
unused gschema geometry keys are used (U10); the window is *Preferences*
(U11).

## 4. The API layer

`Malachi.Core/Api/` re-declares `backend/pkg/api` from api.md, one file per
area as `MalachiCore/API/`:

- every method is a static descriptor `RpcMethod<TParams, TResult>(name,
  timeout)` carrying the `JsonTypeInfo` of both types;
  `client.CallAsync(API.MessageList, params, ct)` is the only way to call
  one, so a typo cannot compile. The table is the static class `API`, the
  Swift name kept: a class `Api` in the namespace `Malachi.Core.Api` would
  be read as that namespace from every other `Malachi.Core.*` namespace. It
  has all 46 methods in the order of `api.AllMethods`, stubs included;
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
  struct over `int` with the 32 documented codes and a `Name`;
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
  `account.oauthStart`, 75 s per `account.oauthWait`.

`docs/api.md` and `backend/pkg/api` are not changed from `windows/`. A
feature that needs a new method goes backend → GTK → macOS → Windows.

## 5. Transport, handshake, daemon supervisor

**Transport.** `Socket(AddressFamily.Unix, SocketType.Stream,
ProtocolType.Unspecified)` with `UnixDomainSocketEndPoint`, newline-framed
JSON-RPC, one reader shared by the handshake and the read loop (the shape
of Go's `client.go`), a `TaskCompletionSource` per id created with
`RunContinuationsAsynchronously`, writes serialised, notifications and
state changes on unbounded channels in the daemon's order. 64 KiB line cap
during the handshake, 32 MiB after. Measured on this machine: connect plus
handshake 0.4 ms, a `system.info` round trip 0.05 ms.

**Handshake** (api.md §1.4): `system.hello` with a 32-byte client nonce;
compare `protocolVersion` first; then read `<socket>.key`; check the
daemon's proof in constant time (`HMACSHA256`,
`CryptographicOperations.FixedTimeEquals`); send `system.authenticate`. The
key is never cached, logged or kept. The test vectors of §1.4 and Go's
failure table (`backend/pkg/api/handshake_test.go`) are ported.

**Key file policy** (a listed deviation, the counterpart of macOS M27):
besides Go's checks (a regular file, not a reparse point, exactly 65 bytes,
the key format), the owner must be the current user and the DACL may grant
access to no one but the user, SYSTEM and Administrators; a NULL DACL is
refused. Before the first spawn the client creates the run directory
`%USERPROFILE%\.cache\malachi\run` with a protected DACL (user and SYSTEM).
The key is read with `FileShare.ReadWrite | FileShare.Delete`: a reader
without delete sharing makes the daemon's shutdown retry and leave the key
behind (measured).

**AF_UNIX on Windows** (all measured): the path limit is 107 UTF-8 bytes
(checked at start with a message naming `MALACHI_SOCKET`); a missing path
or a stale socket file is `ConnectionRefused`, a missing directory
`NetworkDown`; a full backlog fails at once with `NoBufferSpaceAvailable`
(counts as "a listener is there" for the probe); socket files are reparse
points; after a hard kill the daemon replaces the stale socket itself.

**Supervisor.** The state machine of `DaemonSupervisor.swift` in Core
(probe every 100 ms, start timeout 15 s, stop timeout 15 s, backoff 0 then
1 s doubling to 60 s, adopt foreign daemons), over an `IDaemonProcessHost`
from Platform.Windows: `UseShellExecute=false`, `CreateNoWindow`,
`CreateNewProcessGroup`, `ArgumentList` `--socket --config --store`,
stdout/stderr pumped to the log and, when the app was started from a
terminal, to it. Stop: under a process-wide lock, `AttachConsole(pid)`,
`GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid)`, `FreeConsole`, then
`Kill` after 15 s (a kill is crash-safe: the store is intact, the lock is
released, the next daemon replaces the socket and key). The app also stops
its daemon on `WM_ENDSESSION`.

**Console** (measured in phase B, from PowerShell, Git Bash in a pseudo
console and mintty, directly and through make). `Main`, before anything
touches `System.Console`, clears `HANDLE_FLAG_INHERIT` on the inherited
standard handles and calls `AttachConsole(ATTACH_PARENT_PROCESS)`. Started
from a terminal (`make run-windows`), the app is then attached: its log and
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
`build.ps1 run` starts the app with `Process.Start` (no redirection) and
waits; never `& exe` (returns at once) or `& exe | …` (on Ctrl+C PowerShell
kills the app and orphans the daemon, and the inherited pipe keeps
PowerShell waiting).

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

### 6.1 One environment

One `CoreWebView2Environment` for the app, user data folder
`…\Malachi Mail\WebView2`, `WEBVIEW2_*` variables cleared first:

- `AdditionalBrowserArguments = --host-resolver-rules="MAP * ~NOTFOUND"
  --proxy-server=127.0.0.1:1 --proxy-bypass-list=<-loopback>`: the resolver
  rule is the kill switch, the dead proxy an independent second barrier.
  Microsoft advises against flags in production; they are kept because
  they are the only way to meet "the view makes no connection", and a
  runtime that ignored them would still have the request gate below. The
  automated canary (§12) proves them on every runtime;
- `CustomSchemeRegistrations` assigned: `malachi-cid` and `cid` (secure, no
  authority) for pictures, `malachi-doc` (secure, with authority) for
  documents;
- `AreBrowserExtensionsEnabled=false`, `IsCustomCrashReportingEnabled=true`
  (renderer dumps can hold mail; they stay local);
- each view gets `IsInPrivateModeEnabled=true` with its own profile name
  (`viewer`, `editor`, `preview`) through
  `EnsureCoreWebView2Async(env, controllerOptions)`;
- the environment is created at start (it once took ~5 s); until it is
  ready, and if it cannot be created with these arguments or the runtime is
  missing, the reader shows the plain text with the existing hint. Fail
  closed.

### 6.2 The request gate (the content rule list of Windows)

Before the first navigation every view adds
`AddWebResourceRequestedFilter("*", All, SourceKinds.All)` and answers
everything with 403 except its current document (served exactly once,
`malachi-doc://<view>/<generation>-<nonce>`, with the CSP as a response
header **and** as a `<meta>`, and a fixed `<title>`: the hosting window's
title is readable by other processes) and its own picture scheme (with a
deferral; 404 once the view's generation moved on). `data:` never reaches
the gate.

### 6.3 Viewer (`MessageWebView`)

`IsScriptEnabled=false`, `IsWebMessageEnabled=false`,
`AreHostObjectsAllowed=false`, `AreDefaultScriptDialogsEnabled=false`,
`AreDevToolsEnabled=false`, `IsStatusBarEnabled=false`,
`IsReputationCheckingRequired=false`, autofill, password save, pinch,
swipe, zoom control and the error page off; `PreferredColorScheme=Light`,
white background. The document is the byte-identical port of
`htmlview.Document` with the GTK/macOS CSP `default-src 'none'; img-src
malachi-cid: data:; style-src 'unsafe-inline'`. `malachi-cid:` is the port
of `PartSchemeHandler` (`parsePartPath`, `message.part`, images only, never
SVG). Hover: `StatusBarTextChanged` still fires with the status bar off and
feeds the link label (capped at 512 characters). Links:
`NavigationStarting` allows only the pending document; anything else is
cancelled for the UI (the gate keeps it off the network) and the raw href
is read with `document.activeElement.getAttribute('href')` through
`ExecuteScriptAsync` (host scripts run with page script off) and handed
with the resolved URL to the port of `linkDecision`; `NewWindowRequested`
(middle, Ctrl, Shift click, `target=_blank`) the same. Downloads, external
schemes, frames, permissions and authentication are refused. The context
menu keeps Copy and Copy Link (every `ContextMenuTarget` property read in
`try`, `Handled` set in `finally`). Text zoom through CSS `zoom`.
`ProcessFailed` reloads the last body. One view per pane, reused.

### 6.4 Links

The port of macOS's `ActivatedLink`/`linkDecision`: `mailto:` opens the
composer; a masked link (the text shows another destination) asks *Open
This Link?*; a link the daemon did not list in `links[]` is **confirmed**
too (decided; macOS behaviour), because WebView2 hands out normalised URLs
and an exact match against the daemon's raw hrefs can fail. The research
found that the GTK check is likely bypassable with a non-canonical href;
that is a separate GTK/backend task (§14), not part of this port.

### 6.5 Editor (`ComposeWebView`)

`IsScriptEnabled=true` (required: with script off no listener fires),
`IsWebMessageEnabled=true`, `AreHostObjectsAllowed=false`, DevTools and
dialogs off. The document is served from `malachi-doc://editor/…` with the
CSP `default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:` as
header and meta, so every page script, handler and `javascript:` URL in
pasted or quoted HTML is blocked. The bridge is the GTK/macOS bridge with a
`chrome.webview.postMessage` channel, injected with
`AddScriptToExecuteOnDocumentCreatedAsync`, guarded by `window.top` and the
document URL, using `Document.prototype`/`EventTarget.prototype` accessors
captured at document start (a pasted `<img name="body">` clobbers `document.body`
otherwise; verified in Chromium). `WebMessageReceived` accepts only messages
whose `Source` is the current document and whose shape parses. `cid:` is the
port of `CIDSchemeHandler` (only ids in the window's `CIDRegistry`,
`checkInline`). File drops go through the bridge
(`postMessageWithAdditionalObjects` → `CoreWebView2File.Path` →
`attachment.import`). Flushes use macOS's sequence numbers **and** an
order-independent echo rule: WebView2 delivers the changed message before
the `ExecuteScriptAsync` result (15 of 15 trials), which on macOS very
likely leaves drafts dirty after every save. The bridge is a third copy
beside `ui/internal/editor/bridge.go` and the Swift one; a test compares it
with the Go copy modulo the documented deltas.

### 6.6 Previewer

The replacement for Quick Look and Sushi (decided): a reusable preview
window with its own hardened view (script off, `preview` profile, the gate,
downloads cancelled, the PDF toolbar's Save/Save As/Print/More hidden). The
bytes from `message.part` are served directly as `malachi-doc://preview/…`
with the sniffed type: images except SVG, `application/pdf`, `text/plain`;
HTML, SVG, XML and `.eml` are shown as source text. Nothing is written to
disk. Other types get a panel with the icon, name, size and type and *Open*
/ *Save As…*; executables get metadata only. Shell preview handlers are not
hosted: third-party handlers run in-process-adjacent code over hostile
files, and Windows itself stopped previewing internet files in Explorer in
October 2025 because previews leaked NTLM hashes.

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

### 7.3 Time

Every timer takes a `TimeProvider`: the mark-read delay, the 30 s autosave,
the 30 s sync fallback, the 60 s status refresh, the 5 s reconnect, the
300/150/400 ms debounces. Tests use `FakeTimeProvider.Advance`.

### 7.4 What moves out of the view layer

macOS keeps some logic in AppKit, untested. On Windows it lives in Core as
presentation classes with tests (a structural difference only, the
behaviour is macOS's): `NotificationHub`, `SignInRepair`,
`ReaderController` (showMessage with `bodyGen`, the 400 ms spinner),
`AccountsPageController`, `ComposeAttachmentsController`,
`SuggestionsController`, `Debouncer`, `FlushEcho`, `AvatarPalette`
(`g_str_hash % 14 + 1`, initials), `AttachmentOpener`, `LinkOpener`,
`NotificationPolicy`, `MessageWindowRegistry`.

### 7.5 Exposure to XAML

Observable state as `INotifyPropertyChanged` properties (CommunityToolkit.Mvvm
source generators, private setters), imperative outputs as events 1:1 with
the Swift `onX` callbacks, and lists as snapshots applied to
`ObservableCollection`s by a keyed diff (`KeyedListSync`) so a `ListView`
keeps its selection and scroll. `{x:Bind}` only. Dialogs are async hooks
(`IAlerts`), never a `ContentDialog` created by a controller.

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
write` do; tests use an in-memory backend.

Window geometry uses the gschema keys GTK declares but never writes
(`window-width`, `window-height`, `window-maximized`, `folder-pane-width`,
`message-list-width`). One Windows-only key: `ctrl-r` (`reply` default,
`refresh`), the counterpart of macOS's `command-r`. `launch-at-login` only
mirrors the Run key and `StartupApproved`, which are authoritative. As in
GTK, only presentation lives here; mail handling is the daemon's
(`config.get`/`config.set`).

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

The strings check (`Malachi.Conventions.Tests`, the counterpart of
`macos/scripts/check-strings.py`): Roslyn over `src/**/*.cs` and an XML
reader over `**/*.xaml` require every literal msgid of `L10n.T/N/C` and
`{l:T}` to be in `po/malachi.pot` with its context and plural pair, warn
about literals in WinUI text sinks (`Text`, `Content`, `Header`, `Title`,
`PlaceholderText`, `Label`, `ToolTip`, `AutomationProperties.Name`, …)
unless marked Windows-only, and fail on missing msgids. A coverage test
requires every msgid of `po/malachi.pot` to be referenced in `windows/` or
listed with a reason in `windows/parity-exclusions.txt`; another compares
the gschema's keys and defaults with the settings facade.

## 10. Platform services

**Keyring helper** (`malachi-credentials.exe`): the protocol of
`backend/internal/auth/helper` (argv `get|set|delete`, one JSON line on
stdin of at most 1 MiB, identifiers `[A-Za-z0-9._-]{1,128}`, `{"value":…}`
on stdout for `get`, exit 0 found/done, 2 not found, 1/3 as the Go side
defines, stderr capped and never carrying a value). `WinExe` subsystem (a
console window would flash on every call) and NativeAOT (one process per
operation). Generic credentials, `CRED_PERSIST_LOCAL_MACHINE` (secrets do
not roam), target `io.github.schotek.Malachi/<accountId>/<key>`, comment
`Malachi Mail: <accountId> (<key>)`, UTF-8 blobs. A value above 2560 bytes
is split into `#1..#n` chunks written first and chunk 0 last, carrying
`n`, the length and a SHA-256; a mismatch is a corrupt item
(`keyringError`), never a wrong token. The Go side's
`MALACHI_TEST_REAL_HELPER` round trip runs against it.

**Notifications and sound.** Measured in phase B on 2.5.1: in a
self-contained unpackaged app **no package set** makes
`AppNotificationManager.Register()` work; the metapackage, the WinUI
packages and WinUI plus the Runtime package all throw `0x8007007E`
(`Microsoft.WindowsAppRuntime.Insights.Resource.dll` missing; Register
writes its registry entries first, so toasts show but clicks are lost).
The fix is a build target that unpacks that one DLL (34 KB, same version)
from the Runtime package's framework MSIX
(`tools\MSIX\win10-<arch>\Microsoft.WindowsAppRuntime.2.msix`, fetched with
a `PackageDownload`) into the output; it goes once a Foundation release
with WindowsAppSDK PR #6725 ships, and `Register()` is re-tested on every
WinAppSDK bump. The `NotificationInvoked` handler is attached **before**
`Register()` (otherwise COM registers single-use and every click starts a
new process); `Register("Malachi Mail", <icon>)` with no explicit AUMID;
`Unregister()` on exit (a later click still cold-starts the app). A click
while running raises `NotificationInvoked`; a cold click starts the app
with `----AppNotificationActivated: -Embedding`, which the argument parser
ignores, and arrives through `GetActivatedEventArgs()` as kind
`AppNotification`. As `notify.go`: nothing while the main window is
the active window; title the sender's display name or *New message*, body
the subject or *(No subject)*, both capped at 200 bytes; group = account,
tag = `message-<id>`; a click shows the main window. The sound is its own
switch: `PlaySound("MailBeep", SND_ALIAS|SND_ASYNC|SND_NODEFAULT)`, the
user's *New Mail Notification* system sound (closer to GTK's
`message-new-email` than macOS's *Glass*), skipped in quiet hours; toasts
are muted.

**Background, tray, launch at login.** `DispatcherShutdownMode.OnExplicitShutdown`;
one main window for the process, hidden on close when *Run in background*
is on (otherwise the last visible window quits, the GApplication rule). While
hidden, a notification-area icon offers Open, New Message, Check for New
Mail and Quit: `Shell_NotifyIcon` through CsWin32 on a hidden top-level
`WS_EX_TOOLWINDOW` window (a message-only window misses the
`TaskbarCreated` broadcast), `NOTIFYICON_VERSION_4`, the icon taken from the
exe, re-added unconditionally on `TaskbarCreated`, and a native
`TrackPopupMenuEx` menu (a WinUI `MenuFlyout` opened from the tray lands
behind other windows, gets no keyboard and shows nothing while the owner is
hidden; measured). New icons land in the Windows 11 overflow. Every path
that shows the main window (tray, notification, redirected launch,
background start) calls `AppWindow.Show()`, `Activate()` and then
`SetForegroundWindow(hwnd)`: without the last, the window stays behind
(measured with `ForegroundLockTimeout` at its maximum); WinAppSDK's
`RedirectActivationToAsync` already grants the foreground right. Launch at
login is the Run value with `--background`, which starts hidden; a
`StartupApproved\Run` value whose first byte is odd means the user disabled
it in Windows Settings, which is shown as such, never overwritten.

**Single instance and activation.** A custom `Main`
(`DISABLE_XAML_GENERATED_MAIN`): register notifications, then
`AppInstance.FindOrRegisterForKey("io.github.schotek.Malachi")`; a second
launch calls `AllowSetForegroundWindow` and `RedirectActivationToAsync`
and exits. Kinds: launch (shows the window), `mailto:` (the composer only,
as GTK), a notification (the window), `--background` (nothing).

**`mailto:` and the default mail app.** The app writes its HKCU
registration at start when it is missing or stale (the app folder can
move), so it appears in *Settings → Apps → Default apps*; Windows does not
let an app make itself the default. The *Default apps* button in
Preferences (decided) opens `ms-settings:defaultapps?registeredAppUser=Malachi%20Mail`.
The ProgID carries `Application\ApplicationName` = *Malachi Mail* (without
it Windows lists the exe name), and the registration ends with
`SHChangeNotify(SHCNE_ASSOCCHANGED)` (verified with
`SHAssocEnumHandlersForProtocolByApplication`).
Links are parsed by the port of `compose.ParseMailto`.

**Attachments.** Opened files go to the open directory (a fresh random
subdirectory per file, `FileMode.CreateNew`) under a Windows-safe name
(reserved characters, device names including `COM¹`-style superscripts,
trailing dots and spaces, `:` streams, path length). Every file written out
of a message, opened or saved, gets the Mark of the Web through
`IAttachmentExecute` (`SetClientGuid`, `SetLocalPath`, `SetFileName`,
`Save()` on an STA thread; also the AV scan and policy), the counterpart of
the macOS quarantine attribute; if the zone cannot be read back, the file is
not opened (a saved file stays the user's). Never opened, only saved: the
GTK list, the macOS additions, Outlook's Level-1 list, `.rdp`,
`.appinstaller`, `.msix`, `.searchconnector-ms` and friends, anything
`AssocIsDangerous` or `CheckPolicy` flags, and disk images (`.iso`, `.img`,
`.vhd`, `.vhdx`: mounting them has been a Mark-of-the-Web bypass). Opening
anything else uses `ShellExecuteEx`/`Launcher`. `attachment.import` is only
ever given local paths the user picked.

**Sign-in.** The daemon owns the `127.0.0.1` listener; the app opens the
`https` URL with `Launcher.LaunchUriAsync`, nothing else.

**MCP registration** (Preferences → AI): `malachi-mcp.exe status|install|
uninstall --json` beside the app, 15 s timeout, output capped, the process
tree killed on timeout; the MSIX Claude Desktop's configuration path and the
app folder's bridge path are passed with the new flags (§14). Failures go
into the group description as in GTK.

## 11. UI

### 11.1 Main window

`ExtendsContentIntoTitleBar` with the WinUI `TitleBar` on Mica: icon and
*Malachi Mail*, the search box in the middle (Ctrl+F/Ctrl+E), and the back
and pane buttons in narrow layouts. Below it the three panes of
`window.blp` in a grid with two splitters (sidebar 200–320, list 280–460,
message ≥ 300; widths in the gschema keys, written only from a wide,
uncollapsed layout). Below 900 effective pixels the sidebar becomes an
overlay; below 600 list and message form one stack with Back (GTK). Command
rows per pane: sidebar (New Message, the primary menu `…`: New Message, Add
Account…, Preferences, About, Quit), list (folder title and counts, Check
for New Mail, the All/Unread/Flagged `SelectorBar`, hidden while
searching), message (Reply, Reply All, Forward, Trash, Junk, Archive, Star,
More). The status line runs across the bottom edge (26 px, spinner,
connection glyph, caption) and opens the per-account flyout. 1200×760,
minimum 360×294. The caption is *Folder – Malachi Mail*. The `TitleBar` is
set with `SetTitleBar`, `PreferredHeightOption=Tall`; the colour scheme sets
`RequestedTheme` on every window root **and**
`AppWindow.TitleBar.PreferredTheme` (the caption buttons ignore
`RequestedTheme`; measured), and a root without Mica gets
`ApplicationPageBackgroundThemeBrush`. The All/Unread/Flagged filter is the
in-box `SelectorBar` (the toolkit's `Segmented` items lack the UIA selection
pattern).

### 11.2 Sidebar and list

The sidebar is a flat `ListView` over the Core's entries (the GTK row model:
headings, indent 12 per depth, twisty, role glyph, unread badge, the star on
hover or selection, keyboard-reachable), with the status pages of GTK. The
message list is a virtualised `ListView` updated by key diff, rows after
`message_row.blp` (margins 8/3, avatars 40/28 with the libadwaita palette
ported, bold unread, count pill, date, unread dot, search highlights); it
pages itself at the end (macOS M6), *Load More* only to retry. Context menus
(decided) on messages and folders offer only existing actions.

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
draft?* on close. The account wizard is an owned modal window (a window
allows one `ContentDialog` at a time and the wizard nests the certificate
prompt), five pages as GTK/macOS. Preferences is a single-instance window
with Accounts (reorder by handle or Ctrl+Up/Ctrl+Down, a click selects the
row), General (startup, reading, deleting, notifications, the `ctrl-r`
choice, *Default apps*), Appearance and AI, built with `SettingsCard`s.

### 11.4 Banners, toasts, alerts

Banners are `InfoBar`s. Toasts are a per-window overlay (5 s default, queued,
Narrator notification). Alerts go through one `AlertService` that queues
`ContentDialog`s per window; button order follows `ContentDialog` (primary
left, Cancel right), defaults and close responses stay GTK's (*Save Draft*
is the default of the close question).

### 11.5 Keyboard

| Function | Windows | GTK |
|---|---|---|
| New Message / Preferences | Ctrl+N / Ctrl+, | same |
| Reply / Reply All / Forward | Ctrl+R (with `ctrl-r` = `reply`) / Ctrl+Shift+R / Ctrl+Shift+F | — |
| Check for New Mail | F5; Ctrl+R with `ctrl-r` = `refresh` | Ctrl+R |
| Search | Ctrl+F, Ctrl+E; Enter first result, Escape closes | Ctrl+F |
| Trash / Archive / Junk / Unread / Star | Delete / A / J / U / S, also with the message's WebView2 focused, never while a text input has focus (GTK `setTypingAccels`) | Delete / a / j / u / s |
| Quit | Ctrl+Q | Ctrl+Q |
| Close a secondary window | Escape, Ctrl+W | Escape |
| Send / Save draft / Bold, Italic, Underline | Ctrl+Enter / Ctrl+S / Ctrl+B, I, U | same |
| Reorder accounts | Ctrl+Up / Ctrl+Down | same |

Measured in phase B: while a WebView2 has focus, **no** XAML accelerator
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

## 12. Tests

- `Malachi.Core.Tests` (xUnit v3): every Go UI pure-logic test and every
  Swift test of `MalachiCoreTests` ported with the same shape (Theory for
  Swift's parameterised tests): models, threads, folding, favourites,
  folder tree, actions, attachments, accounts page, notification text,
  outbox, sync status, compose source, address list, prefill, mailto,
  suggestions, links, CID registry, editor bridge, wizard fields and
  results, sign-in, format, error texts, provider; the transport (framing,
  JSON-RPC, the §1.4 vectors, Go's handshake failure table, the client);
  API coding and notifications; settings, localisation (the Czech cases
  read `po/cs.po`), printf, plural rules, strftime; the controllers against
  the C# **FakeDaemon** (an in-process daemon on a real AF_UNIX socket with
  a short path, playing the handshake with all of macOS's modes) and
  **MailFixture**, with `FakeTimeProvider` and `IdleAsync`. They run on any
  OS with .NET 10.
- `Malachi.Platform.Windows.Tests`: the process host against
  `Malachi.Core.TestDaemon` (graceful stop, kill after the timeout, deaf
  daemon), the key-file policy (owner, DACL, reparse points), the registry
  backend and its watcher, file-name rules, Mark of the Web round trip.
- `Malachi.Credentials.Tests`: the protocol without the store; a real
  round trip (4 KiB and chunked values) on request
  (`MALACHI_CREDENTIALS_TEST=1`).
- `Malachi.Conventions.Tests`: strings, msgid and gschema coverage, SPDX
  headers of every file type, the manifest identity.
- The **network canary**: a test harness renders the `backend/testdata/mime`
  HTML corpus and the spike's hostile document in the real viewer, editor
  and previewer with loopback canaries and a NetLog, asserting zero
  connections, lookups, navigations, windows and downloads. It runs in
  `make test-windows` and on every WebView2 runtime bump.
- UI smoke tests with FlaUI (UIA3) for the main flows, against a local IMAP
  and SMTP test server (the go-imap and go-smtp servers the backend's own
  tests use).
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
the file version, the full string as `InformationalVersion`.

CI: `.github/workflows/windows.yml` on `windows-2025`: the Go job (`go vet`,
`go test` of `backend/`), and the client job (build, test, lint, package for
x64; cross-build and package for ARM64), zips and `.trx` uploaded.

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
3. **Raw files after sending**: the outbox and the Sent `APPEND` delete the
   raw message while their own handle is open, which Windows refuses, so
   every sent message's full copy stays in `messages\`. Close before
   delete; store renames and removes retry on sharing violations (the
   daemon's `retryFileOp` moved to a shared package); `core.Maintain`
   sweeps orphaned raw and `*.tmp` files.
4. **Portable backend tests**: separators, modes, JSON-escaped paths,
   closed handles, the FIFO and kill cases redesigned, so `go test ./...`
   is green on Windows and a Windows CI job can gate the daemon.
5. **`malachi-mcp status|install|uninstall`**: additive
   `--claude-desktop-config PATH` (the MSIX Claude Desktop reads
   `%LOCALAPPDATA%\Packages\Claude_…\LocalCache\Roaming\Claude\…`, which the
   bridge does not know) and `--command PATH` (what to register instead of
   `os.Executable()`); Windows rows in [mcp.md](mcp.md). The Windows client
   supplies the Windows knowledge; `backend/` keeps none.
6. The root **Makefile**: the Windows block, the `.exe` suffix, the three
   targets.
7. **`.github/workflows/windows.yml`**.

Proposed separately, not in this branch: canonical hrefs in the sanitiser
plus GTK confirming unlisted links (the likely masked-link bypass),
bridge DOM-clobbering hardening in GTK and macOS, the macOS flush-echo
order, portable names in `safename`, an own extension→content-type table,
a runtime D-Bus opt-out.

## 15. Work plan

Each phase is one orchestrated run: coding agents on disjoint areas, each
in its own git worktree and branch, each gated by the build and the tests
of its area; then the branches are merged into `feat/windows`, the full
gate runs, and a review stage (a parity review against the Swift and Go
sources, and a security review for the transport, WebView2, attachments
and the helper) must pass before the phase's commits stay.

| Phase | Work packages | Gate |
|---|---|---|
| **A** Repository groundwork | §14 items 1–6 | `go vet` + `go test ./...` of `backend/` on Windows; Linux test binaries run in WSL; `GOOS=darwin go vet` |
| **B** Scaffold and spikes | solution, props, packages, `.editorconfig`, projects, `build.ps1`, icons, manifest, make targets; spikes: the WinAppSDK 2.5 package set with `AppNotificationManager.Register` unpackaged, CommunityToolkit controls on 2.5, `TitleBar`, keyboard with a focused WebView2, `ContentDialog`/`Flyout` over WebView2 | `make windows`, `run-windows`, `test-windows` from PowerShell and Git Bash; a window opens |
| **C** Core foundation | C1 API layer; C2 i18n, text, settings; C3 Platform.Windows services; C4 `malachi-credentials`; then C5 transport and FakeDaemon; C6 supervisor, paths, bridge runner | all Core tests; the handshake against the real daemon; `account.add` with a password stored through the helper |
| **D** Core logic | D1 models; D2 compose, HTML (bridge), wizard; D3 connection, sync, message cache, mailbox controllers; D4 actions, compose, draft, wizard, preferences, MCP controllers; D5 the presentation classes of §7.4 | every ported Go and Swift test green; conventions tests green |
| **E** WinUI app | E1 shell (`Main`, lifecycle, integration, toasts, alerts, icons, theme, `{l:T}`); E2 WebView2 layer and the canary; E3 main window; E4 reader, windows, attachment actions, previewer; E5 compose; E6 wizard and preferences; E7 notifications, sound, tray, login, `mailto:` | release build without warnings; all tests; the canary; FlaUI smoke tests |
| **F** Verification and docs | end-to-end against local IMAP/SMTP servers; the parity matrix walked with evidence; security review; `windows/README.md`, this document, CLAUDE.md/AGENTS.md, README, architecture, security, mcp, releasing, LICENSING; CI | everything above, on a clean clone |

## 16. Research summary

The port was planned from nine research reports and a spike run of
2026-09-27 (macOS inventory in two parts, GTK parity, the backend on
Windows, WebView2 security, the Windows app model, build and
infrastructure). Measured, not read: the daemon and `malachi-mcp` build and
run on Windows unchanged for amd64 and arm64; AF_UNIX and the §1.4
handshake work from .NET (vectors match); CTRL_BREAK stops the daemon
cleanly; the WebView2 facts of §6; a hand-written unpackaged self-contained
WinUI 3 app builds with plain `dotnet build` in ~14 s for x64 and ARM64 and
starts in ~0.7 s to its first WebView2 navigation (~0.5 s with NativeAOT,
which is not used for the app in v1); publish needs `EnableMsixTooling`;
trimming needs source-generated JSON; the WinAppSDK 2.5.1 metapackage adds
~60 MB of AI libraries a mail client does not use. The macOS client mirrors
GTK faithfully: 502 of the 556 GTK msgids appear in its sources, and every
miss is explained.

## 17. Before a public release

- Installer: Velopack (per user, updates from GitHub Releases) and a winget
  manifest; the update stops the daemon gracefully first.
- Code signing: the owner's decision (SignPath Foundation, an OV
  certificate, or signing as an organisation; Azure Artifact Signing is not
  open to individuals in the EU).
- Licence: the Windows App SDK and WebView2 packages are under Microsoft's
  terms; an explicit GPLv3 §7 additional permission for Microsoft platform
  components in `LICENSING.md`, after a legal check, before the first
  public binary. Development and source are unaffected.
- ARM64 run on real hardware.
- Later tracks: MSIX (virtualisation disabled, an execution alias for
  `malachi-mcp`), Windows Web Account Manager as a daemon extension point,
  a taskbar unread badge.
