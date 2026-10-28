# Windows (WinUI 3) screenshots — hand-over notes

Taken 2026-10-28 from branch `shots/windows` (based on `main` at
`49f0a376`). The code commits come first on the branch; this commit
holds only these pictures and notes. The sets follow docs/screenshots.md
§2 in English (`en/`) and Czech (`cs/`): 11 PNGs in each, lossless and
unscaled, 22 in all.

## System

- **Windows 11 Pro 26H2, build 26300.9711, ARM64**: a Parallels ARM
  virtual machine on a Mac (the registry's `ProductName` still says
  Windows 10 Pro, as it does on every Windows 11). The app photographed
  is the **native ARM64 build**, `build\windows\arm64\Malachi Mail\`
  (`windows\build.ps1 app` picks the host's architecture: `x64` in the
  task's text is the x64 machine of `CLAUDE.md`, this one is `arm64`).
- Toolchain: .NET SDK 10.0.403; Visual Studio 2026 Community 18.11.2
  with the ARM64 MSVC tools (the NativeAOT keyring helper linked);
  WebView2 runtime 156.0.4314.50. The machine had **no Git for Windows
  and no Go**: git 2.55.0 is the copy inside Visual Studio
  (`Common7\IDE\CommonExtensions\Microsoft\TeamFoundation\Team Explorer\Git`),
  put on `PATH` for the shell, and Go 1.27.3 was installed as the official
  `go1.27.3.windows-amd64.zip` (SHA-256 checked) unpacked into
  `%LOCALAPPDATA%\Programs\go`, user scope, nothing on the system `PATH`;
  `build.ps1` found it through `GO=…\go.exe`. It is the amd64 toolchain,
  so it runs under x64 emulation and the sample world's own binaries
  (`malachid`, `malachi-devmail`, `malachi-rpc` in `%TEMP%\malachi-shots\bin`)
  are amd64 and emulated too; the daemon and the bridge inside the app
  folder are cross-compiled arm64 by `build.ps1 app`.
- No sign-out and no change to the machine: the user's display language
  (Czech), region, scale, theme, preferences, registrations and real
  store were left alone; the app ran isolated (below).

## Display, scale, size

- One display, 3360×2100 physical pixels at **200 %** (192 dpi), so
  1680×1050 logical, the taskbar at the bottom; light theme, the default
  accent colour, Mica as the app has it.
- `MALACHI_START_SIZE=1440x900` sizes the window so that its **client
  area is 1440×900 logical = 2880×1800 px**; the window's own rectangle
  is 2906×1813 px (the invisible resize borders, 11 px left, right and
  below at this scale) and DWM's extended frame 2884×1802 px (the frame's
  one-pixel border lines on each side, through which the window behind
  showed, translucent). The picture is the frame intersected with the
  client area: **2880×1800 px**, the content and the one-logical-pixel top
  border line the app itself draws under its extended title bar; the side
  and bottom border lines and the resize borders are left out.
- `compose.png` is the compose window alone at its default size (760×640
  logical, the window's rectangle 1520×1280 px): its client area,
  **1494×1267 px**. It is moved to the same corner for its picture: the
  one-logical-pixel border line under a window's top edge is translucent,
  and over the main window the compose window's first two pixel rows
  showed that window's text (seen in a retake, zoomed in); at the
  screen's edge there is nothing behind it, as for the main window.
- The window stands in the top left corner of the work area, not in the
  centre: this Windows is **not activated** and draws its "Aktivujte
  Windows" watermark above the bottom right of the screen, over whatever
  window is there, and a centred 2880×1800 client area reached under it
  (the first English set had the watermark in nine pictures; both sets
  were retaken with the window in the corner, and none has it).
- The pointer was moved to the last pixel of the screen before each run
  (the driver does it); it is never in the picture, and nothing hovered.

## How the client was driven

`MALACHI_START`, ported into `Malachi.App` on this branch
(`MainWindow.DevStart.cs`, the parser and finders in Core's
`Presentation/DevelopmentStart.cs` with `DevelopmentStartTests`, the
capture in `Shell/WindowCapture.cs`; `docs/windows-port.md` §12), with
the step names and meaning of §3. `scripts/screenshots/windows.ps1` on
this branch starts the world (`start en|cs`: the daemon, the sample
servers and the accounts as the PowerShell block of §6, waiting for the
17 board cases) and runs the published app twice per language
(`shots en|cs OUTDIR`), with `MALACHI_START_INTERVAL=4`:

```
group=on;chat=off;folder=inbox;row=DevConf;shot=…\inbox.png;row=Quotation;chat=on;shot=…\chat.png;
chat=off;row=Q4 planning;shot=…\invitation.png;row=Design Weekly;shot=…\newsletter.png;
folder=Web;row=WEB-1;shot=…\jira.png;board:list:select;shot=…\board-list.png;
board:columns;shot=…\board-columns.png;calendar:week;shot=…\calendar-week.png;
mail;folder=inbox;row=DevConf;compose-open;shot=…\compose.png;quit

group=off;chat=off;folder=sent;row=Quotation;shot=…\crm-saved.png;
folder=inbox;row=Request for a quote;crm-save;shot=…\crm-dialog.png;quit
```

These are the two runs of §3 with `folder=inbox` at the start of the
first, as the Linux set has it. In Czech the three subjects were
`Nabídka`, `Porada` and `Poptávka`. Each language was a fresh world
(`windows.ps1 start cs`, the Czech set, then `windows.ps1 start en`, the
English set).

- `row=` selects through the list controller (`ListController.Select`,
  what a click does: the mark-as-read timer runs) and scrolls the row into
  view; for a conversation row it then waits for the thread's members
  (`thread.get`) and asks again when the fetch failed (once in a retake
  it ran out of its 5 s and `chat.png` showed one bubble and the
  "timed out" toast); `folder=` selects through the mailbox controller and then waits
  for the list to show the folder, so that the next `row=` cannot match
  a row of the folder before; a listing that failed is asked for again
  as *Try Again* does (once in a retake `message.list` of the Inbox ran
  out of the client's 5 s on this emulated machine while the daemon's log
  shows it idle, and the scene came out as *Messages Unavailable*).
- `board:list:select` loads every listed case's conversation
  (`board.get`) and selects the one with the most text (the quotation
  thread); `board:columns` keeps it, as a case the user picked.
- `shot=` waits for the reading pane's web view to finish loading (a cold
  WebView2 took more than the interval on the first HTML body of a run:
  `HardenedWebView.IsLoading`, `MessageView.DevelopmentBodyShown`) and for
  the *Save to eWay-CRM* dialog's lookup to answer (`crm.lookup` through
  the emulated daemon and sample server took more than the interval in
  one retake, which photographed the spinner), brings
  the window to the front, waits 0.3 s for the activation to give the
  focus back, re-focuses the focused control as a pointer would (XAML
  draws a keyboard focus ring when the last input it saw was a key, and
  the input state can come along with the foreground: retakes had the
  ring around the *List* style button and the *Mail* mode button), doing
  so until the focused control reports pointer focus (the step's line
  says which control and what state it found), waits 0.5 s more and
  copies the
  client area **from the screen** (`BitBlt` with
  `CAPTUREBLT`), which is what the user sees: Mica, the WebView2s, the
  `ContentDialog`. The `PrintWindow` path exists for a window that could
  not take the foreground and was not needed: every shot's line says
  `(screen)`.
- `quit` closes the open dialog (`crm-save`) and quits as the menu does.

Client environment of each run (the driver sets it; nothing else of the
desktop reaches the app):

- `MALACHI_DAEMON=none`, `MALACHI_SOCKET=%TEMP%\malachi-shots\rpc.sock`
  (the stack's daemon; the directory has an ACL of its own, the user and
  SYSTEM, since the key file inherits it)
- `MALACHI_DATA_DIR=%TEMP%\malachi-shots\appdata`, wiped before every
  run (so every run starts WebView2 cold)
- `MALACHI_SETTINGS_KEY=io.github.schotek.Malachi.shots`, deleted after
  the runs
- `MALACHI_CALENDAR_SAMPLES=1`
- `MALACHI_LANGUAGES=en-GB` or `cs-CZ`
- `MALACHI_START`, `MALACHI_START_SIZE=1440x900`, `MALACHI_START_INTERVAL=4`
- The daemon ran with `MALACHI_KEYRING=helper`,
  `MALACHI_KEYRING_HELPER=…\malachi-devmail.exe`,
  `MALACHI_TIME_ZONE=Europe/Prague`, `MALACHI_LOG_LEVEL=info`; the app
  takes the zone of the system, Central Europe Standard Time, which is
  Europe/Prague.

The real store, the real preferences
(`HKCU\Software\io.github.schotek.Malachi`) and the user's registrations
were never touched.

## Language

The machine's display language is Czech and was not changed (a sign-out
would have been needed for it, and for English). Instead the development
override of this branch, `MALACHI_LANGUAGES` (`docs/windows-port.md` §9),
named the display languages for the isolated instance: `en-GB` for the
English set, `cs-CZ` for the Czech. Its first tag is also the regional
format, so the English set shows dates as 28/10/2026 and Wed, 28 Oct
2026, the Czech set as 28. 10. 2026 and st 28. 10. 2026, with Czech day
and month names in the calendar. The catalogue is `locale\cs.po` beside
the executable for Czech; English is the source language.

## Checked

I opened all 22 pictures:

- Every main-window picture is 2880×1800 px, the compose window 1494×1267
  px; light appearance, the default accent.
- All text is drawn: no missing glyphs, no placeholder icons (Czech
  diacritics included).
- Every reading pane shows its body, including the WebView2 bodies (the
  DevConf table, the newsletter, the invitation's message, the Jira
  comments as cards), the invitation card, the newsletter's bulk strip
  with Unsubscribe and the blocked-image strip, the issue card with its
  status pill.
- No pointer, toast, notification or other window, and no "backend
  unavailable" or sync banner; the status line reads "Up to date" /
  "Aktuální".
- The sidebar shows the mail account (IMAP badge) with its folders and
  the Jira account with its views and spaces; the CRM account is not in
  Mail, as §2 says (it shows in the CRM bar and the dialog).
- The Inbox shows 17 messages in both sets, 5 of them unread as the
  sample set has them ("5 unread of 17" / "5 nepřečtených z 17"): each
  set was taken over a world started afresh for it.
- The language and the formats are right in each set.

## Differences from §2, and what could not be reproduced

1. **`chat.png` shows the end of the conversation.** The chat opens at
   its end, as the client does, and the four bubbles need more than the
   pane's height at 1440×900: the picture has the last two bubbles whole
   (Petr's, Jana's on the right), the end of Jana's first reply above
   them, and the quick-reply bar under them. Petr's first message is
   above the visible part. The client was not changed for the picture.
2. **The selected case in `board-list.png`** is in *Waiting for Them*,
   and whether the list is scrolled to it varies between runs (the
   client's own behaviour after a programmatic selection): the English
   set has it at the bottom of the visible list, the Czech set has it
   below, as on Linux.
   The detail shows the case, *Why is this here?* and its four
   messages; `board-columns.png` shows the same case in the sliding
   panel over the columns.
3. **`calendar-week.png`** has English calendar and event names in both
   sets: the built-in calendar samples (`MALACHI_CALENDAR_SAMPLES`) are
   not translated, as on Linux. Everything around them is Czech in the
   Czech set.
4. **`compose.png`**: the To field has the keyboard focus; its caret
   blinks, so it is in the picture or not by chance.
5. **The unread count of the Inbox changes between scenes** (5, then 4,
   3 of 17): each scene marks the message it selects as read, as a click
   would, and the second run of a language continues on the first run's
   world.
6. **The window's caption** on Windows shows "Malachi Mail" in the title
   bar; the folder is in the window's title for the taskbar only
   (`docs/windows-port.md` §11.1), so no picture has a folder in its
   title bar.
7. **`crm-dialog.png`**: the *Save to eWay-CRM* dialog is a
   `ContentDialog` inside the main window (§4), dimming the window
   behind it, as the client does.

## Found and fixed on the way

- The runs are not to be touched: the window takes the foreground before
  every picture, so whatever is typed meanwhile lands in the client. One
  retake had a message typed into the chat beside it; its first letter,
  a bare `A`, is *Archive* in Mail and moved the selected newsletter to
  the Archive (the Inbox showed 16), the rest went elsewhere. Both sets
  were taken again with the machine left alone, as §3 asks for the Mac.

- `scripts/screenshots/windows.ps1`, on this branch, went through three
  fixes of its own before the sets were right: the app's output read by
  tasks instead of script-block event handlers (which have no runspace
  and ended the shell, leaving the app running), the Czech subjects
  spelled with character codes (Windows PowerShell reads a script without
  a BOM in the ANSI code page, so `Nabídka` matched no row), and the
  servers stopped by path with a wait for the port (a start over a world
  left behind synced the old server's flags: the Czech set once showed
  3 unread instead of 5).
- `windows\build.ps1 test` on this ARM64 machine found two things that
  are fixed on the branch (`test(windows): …`): the canary tests looked
  for a win-x64 host (the solution builds them as Any CPU and maps the
  host to the machine's platform, so an ARM64 machine has only a
  win-arm64 host; every canary test failed with "the canary host was not
  built"), and `AssistantPanelLogicTests.ChildEnvironment`, which the
  bridge fix of 2026-10-26 (`4cb7cc9f`) left expecting no
  `MCP_CONNECTION_NONBLOCKING=false` while the code sets it; it failed
  on `main` since then.

## Verification (§6 of the task)

- `windows\build.ps1 app` published the arm64 app (Release) with the
  branch's code; the pictures are of that build.
- `windows\build.ps1 lint` is green (`dotnet format --verify-no-changes`
  and the 27 conventions tests, SPDX headers and the strings check
  included; no new msgid).
- `windows\build.ps1 test` (every project, Debug, arm64): 8,781 tests,
  8,707 passed, 42 skipped, 32 failed before the fixes above. The UI
  smoke tests (the published app over its own data) all passed, the
  Credentials and Conventions tests too. The failures:
  - 28 canary tests, "the canary host was not built": fixed as above;
    run again alone (`dotnet test --project tests\Malachi.App.Canary\…`),
    26 of 28 pass and 2 fail, `NoWindowOpened` and
    `ActivatedLinksReachTheReaderAndNothingElse`, both on the
    `chat-bubble` view alone: the host's log has no `chat-bubble-blank`
    new-window event and no `chat-bubble-nav` link event, while the
    viewer, the card and the board card have all of theirs and the
    chat-bubble view reported ready. Not investigated further; the
    canary host here is the win-arm64 build on an emulating machine, so
    this may be the host, not the view.
  - `Malachi.Platform.Windows.Tests`:
    `FileTypePolicyTests.TheShellsOwnVerdict(".settingcontent-ms")`, the
    shell's verdict on this machine, which `CLAUDE.md` lists as a known
    environmental failure that predates the port.
  - `Malachi.Core.Tests`: `AssistantPanelLogicTests.ChildEnvironment`,
    fixed as above and green since; and
    `BridgeRunnerTests.TheEnvironmentAndTheDirectoryAreTheCallers` and
    `ClaudeCodeProcessTests.EnvironmentAndDirectory`, which start the
    stand-in claude with a custom environment and compare what it saw
    with what was given: on Windows 11 ARM64 the system adds
    `PROCESSOR_ARCHITECTURE` to the child's environment, so the child
    sees one variable more. That is this host, not the code; the tests
    were left as they are.
- The new Core tests (`DevelopmentStartTests`,
  `EnvironmentPreferredLanguagesTests`, 32 tests) pass.
- Nothing was built through `windows.yml`.
