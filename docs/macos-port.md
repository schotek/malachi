<!--
SPDX-FileCopyrightText: 2026 Vladislav Janeček
SPDX-License-Identifier: GPL-3.0-or-later
-->

# The macOS client

How the Swift/AppKit client in `macos/` is built, for people (and agents)
who change it. What it does and how to build it is in
[macos/README.md](../macos/README.md); why there is a native client per
platform at all is in [architecture.md §6](architecture.md#6-platform).
§12 keeps what the port was measured to take and what is still open.

The one sentence that governs everything below: **the GTK UI is the
template, and the daemon is the only place logic lives.** The macOS client
is a mirror of `ui/`, not a second design, and it holds no mail knowledge
the GTK client does not hold either.

## 1. Process model

```
┌──────────────────────────────┐                     ┌────────────────────┐
│  Malachi Mail.app            │                     │  malachid (Go)     │
│  Contents/MacOS/MalachiMail  │ ◄── JSON-RPC 2.0 ─► │  all the logic     │
│                              │     unix socket     │                    │
│  starts ──► Contents/MacOS/malachid ───────────────┤  --config --store  │
│             --socket --config --store              │  MALACHI_KEYRING=  │
│             MALACHI_KEYRING=helper                 │    helper          │
│             MALACHI_KEYRING_HELPER=…/malachi-keychain                   │
│                                                    └────────┬───────────┘
│  Contents/MacOS/malachi-keychain ◄── one process per get/set/delete ────┘
│      (login keychain, Security.framework)
│  Contents/MacOS/malachi-mcp  ◄── spawned by an MCP client, same socket
└──────────────────────────────┘
```

The app does what the GTK UI does (`ui/internal/daemon`,
[architecture.md §2](architecture.md#2-transport)): it looks for
`malachid` (`MALACHI_DAEMON`, then beside its executable, then `PATH`),
probes the socket, starts the daemon when nothing answers, waits for the
socket, restarts it after an exit with a backoff, and stops the daemon it
started when the application quits. The daemon gets macOS paths for the
configuration and the store (`~/Library/Application Support/Malachi
Mail/`) as flags and keeps its own default socket path, so `malachi-mcp`
and `.mcp.json` work unchanged (`MalachiCore/Daemon/Paths.swift`,
`DaemonSupervisor.swift`). `MALACHI_DATA_DIR` replaces that directory
(the Windows client's override, [windows-port.md §1](windows-port.md#1-process-model)),
so a test build with its own `MALACHI_SOCKET` runs beside the everyday
one on a copy of the store.

Like the GTK UI (`SweepOpenedAttachments`), the app removes the directory
it writes attachments to for opening and previewing
(`MalachiCore/Platform/OpenDir.swift`, `~/Library/Caches/Malachi
Mail/open`) when it starts and when it quits, whatever the preferences
say, so nothing opened outlives the session, which is also what *Never
Store Attachments* promises. At quit it goes before the daemon is stopped,
which can take 15 s, and once more in `applicationWillTerminate` for a
file whose write was still in flight; `OpenDir.removeAll` refuses any path
but an absolute one ending in `Malachi Mail/open`, as `purgeOpenDir` does
in Go, and a failure is logged.

Every connection to the socket is authenticated before it is used
([api.md §1.4](api.md#14-handshake)), by `RPCClient.connect()` itself,
the Swift port of `api.ClientHandshake`: between Network.framework's
`.ready` and `.connected` it sends `system.hello`, compares the protocol
version before it reads anything else, reads the key file
`<socket>.key` afresh (a restarted daemon has a new key), checks the
daemon's proof in constant time (CryptoKit's HMAC-SHA256), and sends
`system.authenticate`; only its answer makes the connection `.connected`.
The key is never cached, logged or kept in the actor's state. The key file
is read by `Transport/DaemonKey.swift`, which is stricter than the Go
clients on purpose: besides a regular file (no link, pipe or device) of
exactly 65 bytes in the key format, it must belong to the user and grant
nothing to group or others, which is how the daemon writes it (0600). The
Go clients cannot check owner and mode the same way on every platform
they build for (CLAUDE.md rule 4); here it costs nothing, so it is a
listed deviation ([macos/README.md](../macos/README.md#differences-from-the-gtk-ui)).

The daemon's keyring on macOS is the helper keyring
(`backend/internal/auth/helper`, [security.md §6](security.md#6-credentials)):
`MALACHI_KEYRING=helper` and `MALACHI_KEYRING_HELPER` pointing at the
bundled `malachi-keychain`, set by `DaemonSupervisor.environment` unless
`MALACHI_KEYRING` is already in the environment. Before that decision, and
whatever it is, the same function sets the daemon's storage defaults,
`MALACHI_DEFAULT_COMPRESS_STORE=1` and
`MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS=30`, each unless the environment
has it with a value ([api.md §4.8](api.md#48-config)): the store is compressed and the
large attachments of messages older than 30 days stay on the mail server.
`neverStoreAttachments` has no such default; only Settings switches it on.
The daemon stores a default as the preference the first time it applies
it, so Settings overrides them and a daemon started otherwise keeps them;
it is the second run-time extension point of the daemon the client uses,
chosen by the environment, never a build tag, and on Linux nothing sets it
(a listed deviation, [macos/README.md](../macos/README.md#disk-space)). A
daemon the app adopts gets no environment from it. The helper is a separate
executable target (`MalachiKeychain`) with no dependency on the rest of
the package: `Request.swift` is the protocol (parsed and validated without
touching the Keychain, so it is testable without prompts),
`Keychain.swift` the Security-framework half, `main.swift` the glue. It
prints only the protocol: the value on stdout as the answer to `get`, a
diagnostic on stderr, never data.

## 2. Package and module boundaries

`macos/Package.swift` (tools 6.0, strict concurrency, macOS 14, no SwiftPM
resources) has three targets and their tests:

| Target | May import | Holds |
|---|---|---|
| `MalachiCore` | Foundation, Network, CryptoKit (the handshake's HMAC-SHA256), Darwin (the socket probe, the key file, the supervisor's signals), UniformTypeIdentifiers, os | The typed API, the transport with its handshake, the daemon supervisor, the pure logic ported from the Go UI (models, threads, folding, favourites, address parsing, quoting, wizard fields, HTML documents, formatting, error texts, the Assistant's prompts and links in `Assistant/`, the port of `ui/internal/assistant`), the `@MainActor` controllers, settings, i18n, the open directory. **No AppKit, no WebKit.** Everything here is covered by `swift test`. |
| `MalachiMail` | AppKit, WebKit, UserNotifications, ServiceManagement, `MalachiCore` | Windows, views, the menu bar, the toolbar, the WebKit wrappers, the platform services. Thin: it renders what a controller holds and sends clicks back. |
| `MalachiKeychain` | Foundation, Security | `malachi-keychain`, the keyring helper. Independent of the other two. |

The dependency direction is `MalachiMail → MalachiCore`, never back, and
nothing imports the Go modules: the API types are re-declared in
`MalachiCore/API/` from `docs/api.md`. That is the same rule as for the GTK
UI ("UI smí z `backend/` importovat pouze `pkg/api`"), one step further
out: the contract is the socket and the document, not a Go package.

Within `MalachiCore`, `Controllers/` is the layer that talks to the
daemon: `ConnectionController` (the reconnect loop and the protocol
check), `MailboxController` (accounts, folders, the list and its threads),
`SyncController` (the status line from `sync.status`, `notify.syncState`
and the connection, the rows of its popover, the sign-in and certificate
banners), `MessageCache` (`message.get`/`message.body`/
`message.part` with a bounded cache, and `message.download` for the
attachments kept on the mail server: one per message, the chips' spinner
after 0.4 s; the pictures bar's download asks for the body again),
`ActionsController` (flags, moves,
trash, archive, junk, outbox retry, remote images, pictures kept on the
server, trusted senders, reply/forward through `draft.create`, a forward
downloading the attachments first, a reply the pictures it quotes),
`ComposeController` and
`ComposeDraftController` (recipients, autosave, send, discard),
`WizardController` (discover → the browser sign-in or a password →
test → add/update; a refused server certificate offers trust through
`CertTrust`, the port of `ui/internal/certtrust`, and the pin rides along
in the endpoint fields until the host or port changes),
`MailPreferencesController` (`config.get`/`config.set`),
`StorageUsageController` (`system.storage` every 5 s while Settings is
open),
`MCPRegistrationController` (runs the bundled `malachi-mcp status` /
`install` / `uninstall --json` through `BridgeRunner` for Settings → AI;
the page shows the application's last status at once and follows every
newer one, so neither switch shows "off" only because a check has not
answered, and a failed check is repeated after 1, 2 and 4 s, where GTK
waits for the page to come up again),
`AssistantController` (what the Assistant menu may use: the Claude apps'
link handlers through an injected LaunchServices lookup and the bridge's
registration from `malachi-mcp status --json`, run by an
`MCPRegistrationController` of its own without toasts; held by
`AppState`, refreshed at launch and whenever an Assistant menu opens,
and updated by the AI page's switch),
`ClaudeDesktopController` (Claude Desktop rewrites its configuration
from memory while it runs, so a registration written then is lost: the
AI page offers to restart it, quit through the injected platform, write,
start again, and a change left for *Later* is written again when Claude
Desktop quits by itself; the AppKit side is `ClaudeDesktopService`),
`AssistantPanelController` (the in-app panel of phase B: consent, the
signed-in check, one `ClaudeCodeProcess` per conversation fed by stdin
and read as stream-json through `LineFramer`, the transcript items,
pending actions, drafts offered after `openSavedDraft` finds them;
`ClaudeCodeLocator` finds `claude` where a Finder-launched app's `PATH`
does not reach),
`JiraWizardController` (the Jira assistant: `account.detectSite` →
the token → `account.listSpaces`, which doubles as the sign-in test →
the spaces, the offline window and *Only Issues Involving Me* →
`account.add`; in its edit mode `account.update` with a new token),
`JiraAccountController` (the settings sheet of a Jira account:
`account.listSpaces` with the stored token for the spaces and statuses,
the form of `ui/internal/jira/settings.go`, `account.update` with empty
credentials, which keeps the token), `IssueActionsController` (the
status menu of an issue: `issue.transitions` when the menu opens,
`issue.transition` for the chosen item, stale replies dropped, one
transition per issue at a time, the returned issue applied to the card
at once) and `ConversationController` (the
conversation shown for a folded conversation row: the members from the
list controller's folder-scoped `thread.get`, the bodies through
`MessageCache` near the viewport only, the one member marked read, the
cards kept in step as members arrive or go; the port of
`ui/internal/conversation`). `MailboxController.handleMessagesChanged`
takes `notify.messagesChanged`: the account's folders read again, the
cache's entries of the account dropped, the shown folder re-fetched and
listed again.
Each is a `@MainActor` class over an injected `RPCClient` (or a process
runner) and a `toast` sink, tested against a scripted daemon or a fake
bridge script (§9), with no view in sight.

`MalachiMail/App/Contracts.swift` and `Integration.swift` are the seams:
the protocols the shell offers (`Toasts`, `Alerts` with its
confirmations, `confirmTrustCertificate` among them, `MessageActions`,
`EditorView`) and the one place where the sidebar, list, reader, actions,
compose, wizard and notifications are wired to the main window and the
app state.

## 3. The parity principle

The reference for every screen is its Blueprint in `ui/data/ui/*.blp` and
the Go file behind it (`ui/internal/window`, `compose`, `accountwizard`,
`widget`, `editor`, `htmlview`). "Mirror" means:

- every element of the Blueprint has a counterpart in the same order and
  the same place; sizes and margins from the Blueprints and
  `ui/internal/style` become Auto Layout constants; the source files name
  the Blueprint and the Go function they port in their header comment;
- every behaviour of the Go UI (actions, confirmations, optimistic changes
  with revert, timeouts, error texts, generation counters, `closed`/`op`
  guards, the mark-as-read delay, the autosave delay) is ported 1:1, with
  the same names where the Go names are meaningful, so a reader can diff
  the two;
- every user-visible string goes through `L10n.T/N/C` with the **GTK
  msgid as the key**, so `po/` translates both clients at once (§8); a
  string with no GTK counterpart is marked `// macOS-only string` and
  stays English;
- the pure-logic Go tests (`*_test.go` of `window`, `widget`, `compose`,
  `accountwizard`, `editor`, `htmlview`) are ported 1:1 to Swift Testing
  suites of the same shape (§9);
- mail data is hostile input here as there: only `stringValue` /
  `NSTextView.string`, never `NSAttributedString(html:)` or RTF over
  anything from a message; HTML only in the WebKit views of §5.

Where macOS conventions win, the deviation is deliberate, small and
listed in the table in [macos/README.md](../macos/README.md#differences-from-the-gtk-ui)
(the unified toolbar with the folder's counts as the window subtitle, pane
folding without back navigation, the status bar across the bottom of the
window, Settings without search, ⌥⌘↑/↓ for reordering, the ⌘R setting,
`NSAlert` button order, the quarantine attribute on attachments, the
*Glass* sound, the owner and mode checks on the daemon's key file). A new
deviation goes into that table, not silently into the code.

The main window has two modes, Mail and Board (2026-10-01, Swift-first,
on the owner's instruction; the GTK port followed on 2026-10-02, while
the Windows port is still owed). A two-segment control at the leading side
of every toolbar and the first items of the View menu switch them; the
mode is not saved. A hidden split view still reserves its sidebar section
in the unified toolbar, so in Board the Mail split's view is taken out of
the window's view hierarchy (`MainContentViewController.setMode`); its
controller and all mail state (folder, selection, scroll positions, reader,
search, the assistant's transcript) stay alive and the view is put back on
return (`MainWindowController+Mode.swift`, the pure rules in
`MalachiCore/Board/Board.swift`: `Board.Command.allows` disables the mail
actions in Board, `viewsMail` keeps a window showing the board from
counting as looking at a folder for notifications). The window swaps
`window.toolbar` by mode and style, and a re-installed
`.sidebarTrackingSeparator` does not bind to its split again after the view
has left and re-entered the window: after every toolbar install the item is
removed and inserted again (measured; the mode switch starts at the same x
in Mail and in every board style).

The board ([architecture.md §3.7](architecture.md#37-the-board), [api.md
§4.13](api.md#413-board)) in layers:

- **Source.** `MalachiCore/Board/BoardSource.swift` is the seam: a
  snapshot of cases, accounts and commitments, `onChange`, and the user's
  writes (state, done, remind, archive, a commitment ticked off, a
  discarded draft, loading a case's conversation).
  `DaemonBoardSource.swift` is the daemon's: `board.list` and
  `account.list`, `board.get` when a case is selected (cached by case and
  version), the writes laid over the data at once and taken back with a
  toast when the daemon refuses them, `notify.boardChanged`,
  `notify.accountsChanged` and the connection state fed by the
  application's fan-out, one list in flight with a debounce, a back-off
  after a failed list, phases `loading`, `preparing` (`ready: false`),
  `ready`, `off`, `unavailable`, `failed`, `unsupported` (an older
  daemon). `InMemoryBoardSource.dummy(samples:)` over `BoardSamples.swift`
  is the invented data for the tests and `MALACHI_BOARD_SAMPLES`.
- **Model and controller.** `BoardCase.swift` (the API's case in the
  board's terms: the state in effect and who decided it, notes only when
  the assistant preference is on and the notes are current, every string
  cleaned and capped again by `cleanLine` / `cleanBlock`), `BoardView.swift`
  (`Board.view(snapshot, viewState, now:)` builds every view model: the
  four states, the filters, the three styles, the detail, the empty and
  notice texts), `BoardRemind.swift` (the remind presets),
  `BoardText.swift` and `BoardTriageText.swift` (the texts), and
  `Controllers/BoardController.swift` (the view state: style, filters,
  selection; writes go to the source). The window owns its source and
  controller (`MainWindowController.boardSource`, `board`): the source is
  made on the first entry into Board, or at launch while automatic triage
  wants the board's data, and runs until the application quits.
- **Triage** (owned by `AppState`, one for the application):
  `BoardPreferencesController` (`board.preferences` and
  `board.setPreferences`; each write reads afresh and lays only its own
  change on top), `BoardTriageController` (the run: consent, the request
  through `AssistantRequest` with the bridge's triage tools,
  `board.runStart` / `board.runEnd`, progress from the stream events) and
  `BoardAutoTriageScheduler` over the pure rule `Board.AutoTriage.decide`
  (`BoardAutoTriage.swift`); `Assistant/AssistantTriage.swift` has the
  command line, the tools per trigger, the limits (40 cases, 15 minutes)
  and the prompt; [mcp.md](mcp.md), *The board's triage run in the app*.
  `MainWindowController+Triage.swift` shows it: the board toolbars'
  Triage item, the status strip's line, a toast when a manual run ends.
- **Suggest Reply** (owned by `AppState`, one request for the
  application): `BoardReplyController` runs Claude Code once for one case
  (`Assistant/AssistantSuggestReply.swift`: the bridge under
  `--reply-only <message>`, three tools, the prompt and the message) and
  links the draft named by the `create_draft` result with
  `board.setDraft`, deleting it when the link fails, on Stop, the timeout
  or quit; `Board/BoardSuggestReply.swift` has the pure rules (offered,
  the control's view) and `BoardSuggestReplyText.swift` the texts
  ([mcp.md](mcp.md), *A suggested reply on the board*). The detail shows
  it with `BoardSuggestReplyControl` in the suggested reply's place.
  *Settings → AI → Board* (`AIPaneViewController`) has the consent, the
  automatic switch with its interval and daily cap, and a status row.
- **AppKit** (`MalachiMail/Board/`). `BoardPageViewController` hosts three
  styles (List, Columns, Today; `MainWindowController+Board.swift` and the
  View menu), an empty state, a sliding detail panel and toasts; only it
  sets `BoardController.onChange` and fans changes out (`apply(_:)`). The
  List style is a split view of its own (`BoardListViewController`:
  navigation as a full-height sidebar, list, detail; resizable; the
  navigation folds below 900 pt of page width, the inline detail below
  640 pt, where the sliding panel takes over). Columns
  (`BoardColumnsViewController`, four tables of cards, the commitments
  under the first) and Today (`BoardTodayViewController`). The detail
  (`BoardDetailViewController`) shows the state, the "why" box (with
  *Unstar* beside it when the case is hot because of a star), the
  assistant's title, deadline with its quote, summary and tasks under the
  assistant's mark, the suggested reply, and the conversation as cards
  (`BoardConversationBlock`, `BoardMessageCardView`): the newest open,
  older ones folded to a three-line preview of the excerpt `board.get`
  gave. An open card asks `message.body` for its message (through
  `MessageCache.fetchBody`, so `trimQuoted` unless Mail revealed the
  quoted text of that entry) and shows sanitised HTML in the web view of
  Mail's conversation cards (`MessageWebView`, sized mode: the same
  configuration, CSP, `malachi-cid:` handler, link script and height
  rules; the white page under it, `WebPaperView`, in both appearances);
  links go through Mail's `openLink`, the block being a `MessageDisplay`
  registered with `MessageWindows`. Anything else (no HTML part, HTML
  withheld, a body not stored, a failure, a message gone, the samples)
  keeps the excerpt. The rules are Core's `Board.ConversationCards`: at
  most 4 live web views (the inline editor is a fifth), the least
  recently opened card folding back first, never the newest; the block is
  outside the detail's rebuild and changes cards only when the case or its
  members (id and excerpt) change; a card whose height changes above the
  viewport moves the scroll position by as much (`compensatedTop`).
  `BoardActions` is what can be done with a case from the toolbar, the
  panel's action bar and the context menus (`BoardCaseMenu`): Done,
  Remind… (Later Today, Tomorrow, Next Week), Archive, Unstar
  (`BoardController.unflag`, `board.unflag`; offered by
  `Board.canUnstar` / `Detail.canUnstar`: rule reason `hot.flagged`, not
  done), and Reply and Show in Mail, which the application installs
  (`App/Integration+Board.swift`: the compose window, the message selected
  in Mail or in its own window). Every string from a case goes through
  `stringValue`. There is no Open Draft on the board.
- **The inline suggested reply** (2026-10-02). A suggested reply is a
  local draft the case links (`Board.Case.draft`, never in the Drafts
  folder, [api.md §4.13](api.md#413-board)); the detail edits it in place.
  The compose window's content is `Compose/ComposePane.swift` (an
  `NSViewController` with the header, the format bar, the editor, the
  chips, the draft controller and the compose and Format actions;
  `ComposeWindowController` keeps the window, its toolbar, the close
  question, Escape and the rewrite, and forwards the actions to it); the
  board uses it as `.inline` with owner `.board`: no From row, the editor
  in its sized mode (the document's height clamped by Core's
  `EditorHeight`, 160 pt up to `min(480, 0.6 × the detail's visible
  height)`, then it scrolls inside; while it fits, the wheel scrolls the
  detail), a footer `[Attach] status … [Discard] [Send]`.
  `ComposeDraftController` with `DraftOwner.board` never deletes on close,
  keeps a conflict in place (`draft.get`, our text wins) and reports a
  draft deleted elsewhere (`onLost`); `settle()` waits for a send under
  way, flushes and saves while dirty, `finish()` is that and the clean-up. `BoardReplyEditorController` (Core) loads the selected
  case's draft with `draft.get`, keyed by case, account and draft, so the
  board listing the case again after every autosave never reloads it.
  The rules for the panes are Core's `Controllers/BoardReplyPanes.swift`
  (tested with fake panes and over the real draft controller,
  `BoardReplyPanesTests`); `Board/BoardReplyEditorHost.swift`, owned by
  the page and shared by the List's detail and the panel's, only makes the
  `ComposePane`s, moves their views and forwards their ends. There is
  **one live pane per window**, its view in the detail that shows the case
  (it moves between them on a style switch). Nothing typed and no Send's
  outcome is lost silently: a pane that leaves sight (another selection, a
  closed panel, the case leaving the board, Mail mode, a closed window
  through the page's `viewDidDisappear` and `NSWindow.willCloseNotification`,
  quit) and a pane whose case the board shows with another draft or none
  (not proof the draft went) is **saved first** (`settle()`, no clean-up)
  and closed only once that succeeded; only the draft controller's
  `draftNotFound` (`onLost`) abandons it, with *The suggested reply was
  removed elsewhere.* A pane whose save failed is kept, **whatever their
  number**, saves again with back-off (5 s up to 2 min) and is shown again
  when its case is, with the note *This reply could not be saved yet;
  Malachi Mail keeps trying.*; one the user comes back to while it saves
  stays theirs (the save's end does not close it). A **sending** pane is
  never saved or closed before the send answered: success toasts the
  confirmation (*Message queued for sending*, or the Jira comment's) and
  ends the draft (`ended(key)`) wherever the user is; failure leaves the
  draft controller's toast and, out of sight, *Your reply “…” was not
  sent; it is still on the board.*, and the pane is kept (at most three of
  these clean ones). Quit (`AppDelegate`: the replies first, within
  `BoardReplyController.endWait`, then the triage and the connection)
  asks *Quit without saving a reply?* (*Quit Anyway* / Cancel) when one
  could not be saved or sent; Cancel stops nothing. With owner `.board`
  the draft controller **never calls `draft.save` without a real change**
  (the daemon takes every save of a linked suggestion as the user's edit):
  `editorReady()` learns with one flush how the editor itself writes the
  draft, and `settle()` compares two reads of the editor around its flush,
  never the stored HTML with the editor's rendering of it. The inline
  pane's account is always `params.accountID`, never the placeholder.
  Discard asks the draft controller's question and deletes exactly the
  draft it edits (`BoardController.discardDraft(_:draft:account:)` as
  `discardStored`: `board.discardDraft` while the case links it, else
  `draft.delete`; a refusal keeps the pane and its text); the case then
  offers Suggest Reply again, and after Send or Discard the keyboard goes
  to the state pill. The detail's column is `upper` (header to tasks),
  `replySlot` and `lower` (the conversation): the rebuild on a changed
  detail touches only `upper` and `lower`, so the editor's caret and
  keyboard survive every refresh; the slot shows the pane, a loading
  row, a failure note with Try Again, Suggest Reply, or for the samples
  their static block. While the editor grows with the keyboard in it, the
  detail scrolls to keep the pane's Send row in sight. The inline Send has
  **no key equivalent** (a button's ⌘↩ would fire from anywhere in the
  window): ⌘↩ and ⌘S are the menu's Send and Save Draft, which reach the
  pane through the responder chain only while the keyboard is inside it
  (from anywhere else nothing handles them and they are disabled). Tab
  goes To → Cc/Bcc → Subject → editor; Escape in the pane closes its
  suggestions or link popover first, then the panel. Reply on a case with
  a suggested reply selects it and puts the keyboard into its editor
  instead of opening a compose window.
- **Toolbars** (`BoardToolbar`). List: mode switch, sidebar toggle, then
  in the list column's section the style switch at its leading edge and
  the account filter and Triage at its trailing edge, a tracking separator
  on the list/detail divider, then Done (or Move Back to Board), Remind…,
  Archive and Reply at the trailing edge, no window title; the detail has
  no action bar there, its actions are toolbar items (`boardDone`,
  `boardRemind`, `boardArchive`, `boardReply` in `App/Actions.swift`).
  Columns and Today: mode switch and style switch (both navigational),
  title with subtitle, flexible space, account filter, Triage; the detail
  slides over as a panel with its own action bar. Toolbar sections cannot
  overflow individually, so the list column keeps a minimum width that
  holds its three items; in a narrow window Triage is the first item to
  give way to the overflow menu. Triage is taken out of the toolbars while
  it is not offered (`TriageView.offered`). Row and card tooltips are left
  out, because AppKit shows the tooltips of views under an overlapping
  sibling through the panel.

The board's texts have their msgids in the Go package `ui/internal/board`
(including `text.go`, `triage.go` and `reply.go`, translated in `po/`), which
`Board.Text` looks up with key = msgid like every other text (§8); what
stays a `// macOS-only string` there is English. The GTK port also supplies
the Go model and view logic in that package, the triage controllers in
`ui/internal/boardtriage`, and the suggested reply and editor lifecycle in
`ui/internal/boardreply`. Its widgets live in `ui/internal/window/board*.go`
and `board_page.blp`, with the shared `compose.Pane` for inline drafts.
It also mirrors `Board.ConversationCards`, using Mail's existing
`htmlview.Card` for one sanitised `message.body` answer per open card,
with the same four-view limit and plain-text excerpt fallback.
Live Claude runs and a manual GTK pass remain to be verified.

Development aids, neither of them a feature, both only from an isolated
instance (`MALACHI_DATA_DIR` and its own `MALACHI_SOCKET`, §1), never over
a real store, since migration 0017 cannot be undone:

- `MALACHI_BOARD_SAMPLES=1` shows the invented sample cases instead of the
  daemon's board (read once at launch); Reply, Show in Mail, Unstar and
  Triage then only say that the preview cannot do it, and the suggested
  replies are the static block of earlier versions (no draft to edit).
- `MALACHI_START` (`MainWindowController+DevStart.swift`): steps separated
  by `;` (`mail`, `board:list`, `board:list:nav-off`, `board:columns`,
  `board:today`, `size=WxH`, and `quit` as a last step) put the window
  through those modes and styles after start-up (`compose` opens and
  closes a blank compose window; `reply-pane`, with the samples, has the
  real `BoardReplyEditorHost` show a blank, never saved pane in the
  List's reply slot, types into it and prints its heights, its place in
  the detail and where Send goes), `MALACHI_START_SIZE` sets
  its size, `MALACHI_START_INTERVAL` the seconds per step (3) and
  `MALACHI_START_TRACE` logs every resize; after each step the toolbar
  items', the dividers' and the board's and triage's state go to stderr,
  so a layout can be checked without clicking.

## 4. The API layer

`MalachiCore/API/` re-declares `backend/pkg/api` (`API.swift` is the
method table of `methods.go`; the other files follow `types.go` by area).
Rules that keep it honest against a daemon it did not ship with:

- every method is a type conforming to `RPCMethod` (`Params`, `Result`,
  `name`, a default `timeout`), and `RPCClient.call(API.MessageList.self,
  params)` is the only way to call one, so a typo in a method name cannot
  compile; stubs the daemon answers with `notImplemented`
  (`folder.subscribe`) are declared too, so the table is the whole
  contract;
- property names are the JSON names verbatim (no `CodingKeys`); Go
  `omitempty` is `Optional`; a Go nil slice arrives as `null` and is
  wrapped `@NullAsEmpty` so a missing or null array is `[]`, never a
  decoding error;
- wire enums (`FolderRole`, `Flag`, `SyncStatus`, …) are extensible
  structs, so a value from a newer daemon decodes instead of failing the
  whole result; `ErrorCode` is `RawRepresentable<Int>` with the documented
  constants and a `name`, and `RPCError.data` survives (for
  `attachmentTooBig`'s `limit`/`size`);
- `API.protocolVersion` is compared with the `system.hello` answer of
  every connection, in the handshake and before the key file is read
  (`RPCClient`; a daemon of protocol 1 answers `methodNotFound`, which is
  a mismatch with 1), and again with `system.info` as a defence
  (`ConnectionController`). A mismatch is a state the window shows, not
  something to work around: the status line names both versions and is no
  button, the backend banner stays hidden (a daemon runs), nothing is
  loaded, and the 5 s retry loop keeps the state up instead of flashing
  *Connecting…* until an attempt ends otherwise; a connection ends it at
  once (*Connecting…* until `system.info` answers, as GTK drops it at
  Connected). Every other refused
  handshake is *Backend unavailable* with the banner, logged at error
  level once per distinct reason until the next connection. No new
  strings: both are the GTK msgids;
- `system.hello` and `system.authenticate` are in the method table like
  every method (50, in the order of `api.AllMethods`; `account.detectSite`
  and `account.listSpaces` came with the Jira accounts), but only
  `RPCClient.connect()` sends them; `ErrorCode.unauthenticated` (1005) is
  what the daemon answers anything else before the handshake;
  `API.allNotifications` is `api.AllNotifications` in its order
  (`newMessage`, `syncState`, `authRequired`, `accountsChanged`,
  `messagesChanged`), and `APICodingTests.methodTableMatchesGo` holds
  both lists against the Go ones;
- the Jira types (`API/Jira.swift`: `JiraConfig` with every Go field,
  since `Codable` drops unknown keys and an `account.update` from the
  settings would otherwise erase them; `IssueInfo`, `MessageIssue` with
  the flattened `IssueInfo`, `Space`, `IssueStatus`, `DraftComment`,
  `MessagesChangedNotification`) follow the rules above:
  `Account.capabilities` is `[Capability]?`, not `@NullAsEmpty`, because
  a missing list means the mail default while an empty one means none;
- timeouts are the GTK UI's (`Platform/RPCTimeouts.swift`): 5 s by
  default, 5 s for the whole handshake (`handshake`, api.HandshakeTimeout),
  3 s for `system.info`, 60 s for `message.part` and
  `attachment.get`, 30 s for `message.body` under `allow`,
  `message.embedded`, `draft.create`, `account.add`/`update`, 15 s for
  `account.discover`, 45 s for `account.test`, 10 s for
  `account.oauthStart` and 75 s for each `account.oauthWait` (the daemon
  answers `pending` after a minute and the wizard asks again), 300 s for
  `message.download` (`download`: the daemon's budget is 4 minutes, and
  it finishes a download its caller gave up on), 15 s for
  `account.detectSite` and 45 s for `account.listSpaces` (the daemon
  signs in, lists and counts within 40 s), the values api.md names for
  the clients.

`docs/api.md` and `backend/pkg/api` are not changed from here. A feature
that needs a new method is added to the daemon and the document first
(CLAUDE.md rule 5), then to the GTK UI, then here.

## 5. The WebKit security layer

HTML from a message is rendered by `WebViews/MessageWebView.swift`, HTML
being composed by `WebViews/ComposeWebView.swift`. Both are **layer 2** of
[security.md §3.2](security.md#32-defences) and §3.3, re-established for
WebKit on macOS, since none of WebKitGTK's settings carry over. The
backend's sanitiser (layer 1) is what makes the content safe; these views
are what keep a sanitiser bug from becoming a compromise, and they are
never a reason to relax layer 1. The header comment of each file walks
through §3.2 / §3.3 bullet by bullet with the property that implements it;
in short:

| Property | Viewer (`MessageWebView`) | Editor (`ComposeWebView`) |
|---|---|---|
| Content JavaScript | off (`allowsContentJavaScript = false`) | off; the bridge is a `WKUserScript`, which runs anyway and the CSP does not govern |
| Storage | `WKWebsiteDataStore.nonPersistent()` | same |
| CSP | `default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'` as a `<meta>` in the document (`HTML/ViewerDocument.swift`), identical to GTK; a WKWebView has no default policy, so the document is the only carrier | `default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:` (`HTML/EditorDocument.swift`) |
| Network | a proxy nothing answers on (`127.0.0.1:1`) for whatever might slip past the CSP, **plus a content rule list** that blocks every load except `malachi-cid:`, `data:` and `about:blank`; no body is loaded before the list is compiled and installed, and none at all when it cannot be compiled (`ContentRules`: two stores tried, a failure is never cached, the reader falls back to the plain text with the hint) | same, allowing `cid:` instead of `malachi-cid:`; the editor reports a failure instead of loading |
| Navigation | only the initial `about:blank` load of the main frame (the document is loaded with `baseURL: nil`); a link activation is cancelled and handed to `onLink` when `allowedLink` accepts it (the actions layer confirms a masked link); everything else cancelled; `createWebViewWith` returns nil; drops refused | only the initial load; every other navigation, including a clicked link in a quoted original, cancelled; file drops taken away from WebKit and handed to attachment import |
| Pictures | `PartSchemeHandler` for `malachi-cid:<account>/<message>/<part>`: stateless, `parsePartPath` then `message.part`, images only, never SVG | `CIDSchemeHandler` for `cid:<id>`: only ids the window registered in `CIDRegistry` (files it picked, the backend's copies through `attachment.get`), every picture through `checkInline` (never SVG, within the cap) |
| Context menu | Copy and Copy Link only | none |
| Hover | the link under the pointer in a status label, from a user script | — |

The content rule list is the one thing layer 2 has here that the GTK
viewer does not: a `<link rel="preconnect">` opens a TCP connection
without a request, which neither the CSP nor the proxy setting catches
(found by a network canary during the port). The identifiers
(`io.github.schotek.Malachi.viewer.1`, `.editor.1`) are bumped with the
rules, since the store keeps the compiled list by them.

The two schemes are different on purpose: a displayed message can never
address compose attachments, and a composed draft cannot name a received
message's parts. One view per pane is reused between messages (without
network nothing persists, and the document is replaced whole).

The conversation view (`MessageView/ConversationViewController.swift`,
a folded conversation row selected) stacks the members of a conversation
as native cards and gives every HTML card a `MessageWebView` of its own
in its **sized mode** (`init(cache:zoom:sized: true)`): the same
configuration, the same content rule list, content JavaScript off, and
the document still `viewerDocument(body:compact:)` around one sanitiser
output, the column's padding cut to the card's. What the mode adds is a
second user script of the app's own (`sizeScript`), in the same
`.defaultClient` world as the viewer script, that only reads the layout:
a `ResizeObserver` on the document and on the column reports the
document's height in CSS pixels through a third message handler (`size`)
whenever it changes and when a picture finishes loading, and
`WebHeightGovernor` (`MalachiCore/Model/ConversationLayout.swift`, pure
and tested) turns it into the view's height at the page zoom: capped at
4000 pt (beyond it the card scrolls inside), and frozen after three
growths in a row that the view's own growth caused (`100vh`,
`height: 100%`: the script marks a report that followed a change of the
view's height alone), until the document, the width or the zoom
changes. The scroll wheel goes on to the conversation's scroll view while
the document fits, the link under the pointer to the pane's one status
label (`onHover`), and the card re-measures on zoom. Cards are cheap:
`ConversationLayout.live` fetches a body only within two screens of the
viewport and keeps at most eight web views alive, the nearest first; the
others keep the height they last had without a view. Why not one composed
document of the whole conversation, and what a card may and may not do,
is in [security.md §3.2](security.md#32-defences) (layer 2 on macOS).

The [security review checklist](security.md#12-review-checklist-for-prs-touching-content-handling)
applies to changes in `WebViews/`, `MessageView/`, `Compose/`,
`Attachments/`, `MalachiKeychain` and `backend/internal/auth/helper`.

## 6. Concurrency

- `RPCClient` is an actor over `NWConnection`: it owns the connection,
  matches responses by id, and publishes `notifications` and `states` as
  `AsyncStream`s in the daemon's order (a `newMessage` never overtakes
  the `syncState` that follows it). Each stream has one consumer, the
  `ConnectionController`, which forwards on the main actor.
- The handshake runs inside `connect()` while `state` is still
  `.connecting`, so `call` refuses with `.notConnected` until it is done;
  its two requests take a private path that checks the connection and its
  generation. The actor is reentrant: `close()`, a failed connection or a
  second `connect()` can run at any `await`, so the generation is compared
  after every one of them and nothing is sent on a connection that was
  torn down. Until the `system.authenticate` answer the bytes go to the
  handshake alone, read line by line (at most 64 KiB a line): up to 8
  notifications before the `system.hello` answer are dropped, one between
  the two answers breaks the handshake, and whatever came after the last
  answer is held and handed to the framer right after `.connected`, so
  those notifications arrive after it and in order. The whole exchange is
  bounded by `RPCTimeouts.handshake` (injectable for tests): past it no
  more bytes are taken and nothing more is sent, as on a Go connection
  past its deadline. A refusal is a `HandshakeError`, kept apart from
  `ClientError`, and tears the connection down; a connection that breaks
  during the handshake is a plain `ClientError`, like any other.
- Everything that touches a view or a model is `@MainActor`: the
  controllers in `MalachiCore/Controllers/`, every AppKit class, the
  WebKit delegates (main-actor isolated in the Swift overlay). An RPC is a
  `Task` started on the main actor, so the continuation after `await` is
  on the main actor too, and the generation counters of the Go UI
  (`listGen`, `bodyGen`, `foldersGen`) work without locks: a late answer
  compares its generation and is dropped.
- Optimistic changes are applied, their inverse kept, and reverted in the
  error path with a toast, as in `actions.go`. Windows keep `closed` flags
  and cancel their tasks when they close, so nothing renders into a
  window that is gone.
- The keyring helper, the attachment writes and the file reads that could
  block run off the main actor (`Task.detached`, `nonisolated` helpers);
  the daemon itself is where anything slow belongs.
- Swift 6 strict concurrency is on and the build is expected to be free
  of warnings; `@preconcurrency` imports need a reason in a comment.

## 7. Settings

`MalachiCore/Settings/Settings.swift` is `UserDefaults.standard` (domain
`io.github.schotek.Malachi`) behind typed properties. The keys and defaults
are those of `data/io.github.schotek.Malachi.gschema.xml`
(`launch-at-login`, `run-in-background`, `mark-read-delay`,
`confirm-delete`, `desktop-notifications`, `notification-sound`,
`color-scheme`, `message-list-density`, `show-preview-line`,
`group-by-conversation`, `show-avatars`, `monochrome-avatars`,
`monospace-plain-text`, `text-zoom`, `collapsed-folders`,
`collapsed-accounts`, `favourite-folders`, `assistant-menu`,
`assistant-target`, the latter read through `Assistant.parseTarget`, so an
unknown nick is Claude Desktop) plus one macOS-only key,
`command-r` (`reply`, the default, or `refresh`; §3). The board adds
`board-default-style` (`list`, the default, `columns` or `today`; Settings
→ General → Board), the style of its first show after launch, later shows
keeping the user's last one. Numeric keys are
clamped to the schema's ranges, bad enum strings fall back to the default,
and a change fires its handlers through KVO on `UserDefaults`, so a
`defaults write` from outside reaches the running app exactly as a second
GTK window sharing the profile would. `launch-at-login` only mirrors
`SMAppService`, which is authoritative. As in GTK, only presentation
options live here; anything that affects mail handling (check interval,
remote content, retention) is the daemon's, through `config.get`/`config.set`.

The window frames (`Main`, `Settings`) and the pane widths
(`main-sidebar-width`, `main-list-width`, and the assistant panel's
`main-assistant-width`) are AppKit state in the same domain, not
settings.

## 8. Localisation

`macos/scripts/po2strings.py` (stdlib Python, run by `make macos` and
`make test-macos`) turns `po/malachi.pot` into `en.lproj` and each
`po/<lang>.po` into `<lang>.lproj`, with `Localizable.strings` for
singular entries and `Localizable.stringsdict` for plurals, under
`build/macos/locale/`; the Makefile copies them into
`Contents/Resources`. Keys are msgids verbatim; a context entry is keyed
`<ctxt>\u{4}<msgid>` (gettext's convention); a plural entry is keyed by its
singular msgid; printf formats become Foundation's (`%s` → `%@`, `%d` →
`%ld`, positional forms likewise) unless the entry is `no-c-format` (the
strftime date patterns); fuzzy, obsolete and untranslated entries are left
out so the runtime falls back to the msgid. The plural categories per
language are a table in the script (`PLURAL_CATEGORIES`) that
`I18n/PluralRules.swift` mirrors; an unknown language is an error, not a
guess.

`I18n/Localization.swift` is the gettext shim: `L10n.T(msgid)`,
`T(msgid, args…)`, `N(singular, plural, n)`, `C(context, msgid)`. The
`Catalogue` is loaded once at start from the first root that has any
`.lproj`: `Bundle.main.resourceURL`, then `MALACHI_LOCALE_DIR`, else
English; the language is `Bundle.preferredLocalizations`, which honours
the per-app language in System Settings. The plural form is picked by
`PluralRules`, not by Foundation, so no resource bundle is needed and
`Bundle.module` is never used (a hand-assembled `.app` cannot carry it).
`I18n/Strftime.swift` maps the strftime msgids of `widget/format.go` to
`DateFormatter` patterns at run time, so a translator changes a date
format in one place for both clients.

## 9. Tests

`make test-macos` runs both test targets with the generated catalogues in
`MALACHI_LOCALE_DIR`, so the Czech cases run.

- `Tests/MalachiCoreTests/` (Swift Testing): the ports of the Go UI tests
  (`MailModelTests`, `ThreadModelTests`, `CollapseStateTests`,
  `FavouriteStateTests`, `FolderTreeTests`, `ActionHelpersTests`,
  `AttachmentsTests`, `DownloadTests`, `AccountsPageTests`, `NotificationTextTests`,
  `NotifiedMessagesTests`, `OutboxTests`, `SyncStatusTests`,
  `ComposeSourceTests`,
  `AddressListTests`, `PrefillTests`, `MailtoTests`, `SuggestTests`,
  `BlockedSummaryTests`, `HTMLLinksTests`, `CIDRegistryTests`,
  `EditorBridgeTests`, `WizardFieldsTests`, `WizardResultsTests`,
  `SignInTests`, `FormatTests`, `RPCErrorTextTests`, `ProviderTests`,
  `AssistantTests` from `ui/internal/assistant/assistant_test.go`, whose
  translator case is `AssistantTranslationTests` against the generated
  Czech catalogue), the
  transport
  (`FramingTests`, `JSONRPCTests`, `RPCClientTests`, `SupervisorTests`,
  `AuthTests` with the test vectors of api.md §1.4 and `DaemonKeyTests`,
  and `HandshakeTests`, the failure table of
  `backend/pkg/api/handshake_test.go` with Go's outcomes, the check that
  no failure text shows a key, a nonce or a proof, and Go's error texts),
  the API coding (`APICodingTests`, `NotificationDecodeTests`), the
  settings and i18n (`SettingsTests`, `LocalizationTests`,
  `GettextFormatTests`, `PluralRulesTests`, `StrftimeTests`) and the
  controllers (`ConnectionControllerTests`, `MailboxControllerFoldersTests`,
  `MailboxControllerListTests`, `MessageCacheTests`, `SyncControllerTests`,
  `ActionsControllerTests`, `ComposeControllerTests`, `DraftStateTests`,
  `WizardControllerTests`, `MailPreferencesTests`, `StorageUsageTests`,
  `MCPRegistrationTests` with a `#!/bin/sh` fake bridge,
  `AssistantControllerTests` with the same kind of bridge and a scripted
  link-handler lookup, `ClaudeDesktopControllerTests` with a fake
  platform and bridge).
  The Jira accounts and the conversation view add the ports of the Go
  reference tests (`JiraTests`, `JiraWizardTests`, `JiraComposeTests`,
  `JiraSettingsTests`, `JiraPatternTests` for `ui/internal/jira`;
  `CapabilitiesTests` for `ui/internal/capabilities`; `ConversationTests`
  for `ui/internal/conversation`), the Swift-first logic (`JiraListTests`,
  `JiraReaderTests`, `JiraActionRulesTests`, `JiraAccountsTests`,
  `ConversationLayoutTests` with the height governor, `BoardTests` for the
  window modes, `BoardModelTests` and `BoardDaemonModelTests` for the
  board's view models, `BoardRemindTests`, `BoardAutoTriageTests` and
  `BoardTriageViewTests` for the schedule's rule and the triage's view,
  `AssistantTriageTests` for its command line, `APICodingTests+Board` for
  the API types), the board's controllers and source
  (`BoardControllerTests`, `DaemonBoardSourceTests`,
  `BoardPreferencesControllerTests`, `BoardTriageControllerTests`), the controllers
  (`JiraWizardControllerTests`, `JiraAccountControllerTests`,
  `JiraComposeControllerTests`, `JiraActionsTests`,
  `IssueActionsControllerTests` (`JiraTransitionsTests` for
  `ui/internal/jira/transitions.go`), `ConversationControllerTests`,
  `MessagesChangedTests`) and the Czech cases (`JiraTranslationTests`,
  `JiraSettingsTranslationTests`).
- `Tests/MalachiCoreTests/Fixtures/`: `FakeDaemon` is an in-process
  `malachid` on a real unix socket speaking the same newline-delimited
  JSON-RPC, with per-method handlers. It plays the daemon's side of the
  handshake: `start()` writes a fresh key beside the socket (0600), it
  answers `system.hello` and `system.authenticate` itself, in order, and
  records them in `handshakes`, never in `calls` (so a count of calls
  sees only what came after); before a connection is authenticated it
  refuses anything else with 1005 and closes, and it notifies
  authenticated connections only. `setHandshake(_:)` makes it another
  daemon (`oldDaemon`, `protocolVersion(n)`, `wrongProof`,
  `rejectClient`, `silent`, `malformedProof`) or a script (`raw`, the
  exact bytes to write for each answer, or none, and whether to close
  after it, as the Go test's scripted daemon); a connection keeps the
  mode it was accepted in. Two more knobs send notifications before the
  `system.hello` answer or with the `system.authenticate` answer,
  `restart()` / `rotateKey()` give it a new key, and `secrets` lists every
  key, nonce and proof it used. `MailFixture` scripts it with
  accounts, folders, messages, threads, bodies and parts, records what was
  asked, and fails, delays or pushes notifications on request. Controller
  tests run against it, never against a real daemon.
- `Tests/MalachiKeychainTests/`: the helper protocol (parsing, exit
  statuses, the identifier rule) without the Keychain; a real round trip
  through the login keychain only with `MALACHI_KEYCHAIN_TEST=1`, because
  it can prompt. On the Go side `backend/internal/auth/helper` tests the
  daemon's half against a fake helper (the test binary itself), and
  `MALACHI_TEST_REAL_HELPER=<path>` drives the built `malachi-keychain`.
- `python3 -m unittest macos/scripts/test_po2strings.py` covers the
  catalogue generator against the real `po/malachi.pot`.

There is no `MalachiMailTests` target: the AppKit classes are thin and the
logic they would test lives in `Controllers/`.

## 10. Adding a feature, keeping the parity

1. **Backend first.** If the daemon has to learn something, that lands in
   `backend/` with `docs/api.md` in the same commit (CLAUDE.md rule 5).
   Nothing in `macos/` may work around a missing method.
2. **GTK second.** The Blueprint and the Go code are the template: change
   `ui/` and its tests, and add every new string to `po/` (`make po`).
3. **Mirror third.** Port the Blueprint change to the AppKit view and the
   Go change to the controller or model port, with the same msgids, the
   same behaviour and the ported test. Name the Go file and function in
   the header comment, as the existing files do.
4. **If macOS has to differ**, add the row to the deviation table in
   `macos/README.md` and say why in the code.
5. **Before handing over**: `make macos` (release, no warnings),
   `make test-macos`, the Go tests of `internal/auth/helper` when the
   helper changed, an SPDX header on every new file (GPL-3.0-or-later in
   `macos/`, AGPL-3.0-only in `backend/`), no `print` of mail data, no
   `try!` or `!` on decoded data, no `Bundle.module`, no
   `NSAttributedString(html:)`. Anything that touches content handling
   goes through the [security checklist](security.md#12-review-checklist-for-prs-touching-content-handling).

For an agent, the reading list of a change is the `.blp` file and the Go
file it names, `docs/api.md` for every method it calls, and
[security.md](security.md) §3 for anything near HTML or attachments. The
`.blp` files are the reference: when the Swift view and the Blueprint
disagree and no deviation explains it, the Swift view is wrong.

## 11. Build

`make macos` at the root builds `build/malachid` and `build/malachi-mcp`
(the Go targets) and delegates to `macos/Makefile` with `BUILD_DIR`,
`VERSION` and `APP_ID`; `make run-macos` and `make test-macos` delegate
likewise. In `macos/Makefile`, `build` is `swift build -c release`,
`locale` runs the catalogue generator, and `app` assembles `build/Malachi
Mail.app`: `MalachiMail` and `malachi-keychain` from SwiftPM's bin path,
`malachid` and `malachi-mcp` from `build/`, the `.lproj` directories,
`Info.plist` rendered from `Resources/Info.plist.in` (version, bundle id,
`CFBundleLocalizations`, the `mailto:` URL type) and checked with
`plutil`, the licences, then `codesign` of each binary (with a fixed
identifier) and of the bundle with `SIGN` (ad hoc by default; the README
says what that costs at the Keychain and how a self-signed identity
avoids it). `make macos-dmg` builds the same for arm64 and x86_64 (`ARCHS`,
the Go binaries joined by `lipo` in the root Makefile's `macos-go`) into a
disk image, `make macos-notarize` has Apple notarise it; with a Developer
ID every binary gets the hardened runtime and the bundle
`Resources/MalachiMail.entitlements`, and `malachi-mcp` alone, in every
build, `Resources/malachi-mcp.entitlements`
([releasing.md §8](releasing.md#8-macos), CI in
`.github/workflows/macos.yml`). No `.xcodeproj` is kept; Xcode opens
`Package.swift`.

## 12. What the port took, and what is still open

This document began as the exploration of whether a native macOS client
was feasible, measured against the tree of 2026-09-07. The conclusions
that still matter, updated to what was built:

**The backend needed one extension point, not a fork.** The daemon and
its tests already compiled on macOS with no build tags; the only
Linux-bound pieces sat behind interfaces. Of the three, the keyring
(`auth.Keyring`) got its platform-neutral implementation, the helper
keyring of §1; the XDG paths are handed to the daemon as flags by the
app; the address books (`contacts.Directory`, Evolution Data Server) are
absent and the daemon degrades silently, so recipient completion runs on
the collected addresses alone. A second, smaller one followed with the
compressed store (2026-09-27): the run-time defaults of two preferences
(`MALACHI_DEFAULT_*`, §1), which the app sets for its daemon because Macs
often have small disks. No `//go:build darwin` exists anywhere,
which is what CLAUDE.md rule 4 asks for. One Go test still fails on
macOS and is unrelated to the client: the timing-sensitive
`TestWorkerAuthFailureDefersQueue`. `TestAttachmentImportMetadata` failed
too until it pinned the type it relies on: `internal/core/attachments.go`
asks the host MIME database about an extension, and macOS has no `.md`,
so a Markdown attachment still goes out as `text/plain` there (a
content-type table of our own would fix that on every platform).

**GNOME Online Accounts is no longer the limit; distribution is.** At
first Gmail and Microsoft 365 could not be added here: their tokens came
only from GNOME Online Accounts (Graph tokens, Gmail's XOAUTH2 tokens,
the accounts `account.linked` lists), and macOS has no equivalent. Since
2026-09-25 the daemon runs the sign-in itself (source `daemon`,
[api.md §4.1](api.md#41-account), [security.md §6](security.md#6-credentials)):
authorization code with PKCE, a one-shot listener on `127.0.0.1`, the
refresh token in the keyring, which on macOS is the helper keyring of §1
(key `oauth2.refresh_token`). Without GNOME Online Accounts,
`account.discover` answers a Google or Microsoft 365 address with that
sign-in as the primary config, and the assistant takes the same way as
the GTK one on a desktop without GNOME: *Sign In with Google* opens the
provider's page in the browser (`NSWorkspace`, https only), the wizard
waits on `account.oauthWait`, tests the account with the session and adds
it (`OAuthPageController` over `WizardController`). A revoked sign-in is
the banner *Sign in to … again in your browser* and *Sign In…* in
*Settings → Accounts*; for Gmail an app password over IMAP/SMTP remains
the fallback. The flow needed no build tag: it is platform-neutral in the
daemon, and GNOME Online Accounts stays the preferred source where it
runs.

What remains is not code but distribution. The daemon ships the
project's Microsoft registration, so Microsoft 365 and Outlook.com sign in
out of the box; it is not publisher-verified, so organisations that
restrict user consent approve it once. For Gmail no client is shipped: it
would need Google's OAuth verification and, for the restricted mail scope,
an annual third-party security assessment (CASA) before users outside a
test list can sign in. Gmail on macOS therefore uses an app password or a
client of the user's own in
`~/Library/Application Support/Malachi Mail/config.toml` (see the
[README](../README.md#oauth-clients-for-gmail-and-microsoft-365)). Who would own that is the open question
of the original exploration, and it is the same on a Linux desktop
without GNOME Online Accounts.

**Jira accounts came to macOS first (2026-09-29).** For the
issue-tracker accounts ([api.md §4.1](api.md#41-account),
[architecture.md §3.6](architecture.md#36-issue-tracker-accounts-kind-jira))
and the conversation view the order of §10 was reversed, since the user's
Jira lives on the Mac: the daemon first as always, then a Go reference of
the pure UI logic (`ui/internal/jira`, `ui/internal/capabilities`,
`ui/internal/conversation`, tested on the Mac without GTK), ported 1:1
into `MalachiCore` (`Jira/`, `Model/Capabilities.swift`,
`Model/Conversation.swift`) with the AppKit views on top, and the GTK
widgets and the Windows client later. The parity rules of §3 hold in
reverse: the msgids are in `po/` already (appended by hand, `make po`
renumbers them) and on the Windows side in `parity-exclusions.txt`;
every Swift function without a Go counterpart is marked `Swift-first`
with the Go file it belongs in, and
[macos/README.md](../macos/README.md#swift-first-where-the-gtk-ui-mirrors-it)
lists them for the port; the deviations (the assistant and the settings
as sheets, the JIRA capsule, the conversation view with its per-card web
views, `MALACHI_DATA_DIR`) are in its table. The daemon needed nothing
platform-specific: `MALACHI_DATA_DIR` is the app's reading of its own
paths, and the test copies the feature was verified on ran beside the
everyday store with their own socket. The GTK UI followed on
2026-09-30: it uses the Go reference as it is and mirrors the
Swift-first functions where
[macos/README.md](../macos/README.md#swift-first-where-the-gtk-ui-mirrors-it)
said, so the two clients are at parity again and the order of §10
holds for what comes next. The Windows client followed the same day
([windows-port.md §11.7](windows-port.md#117-jira-accounts-and-the-conversation-view)).

**Distribution is partly in place.** CI builds a universal DMG, signed
with a Developer ID and notarised once the owner's Apple Developer Program
membership and the repository's secrets exist (until then ad hoc,
[releasing.md §8](releasing.md#8-macos)). Not done: App Sandbox entitlements (note
that a sandbox moves the data into the app container and the socket with
it, which the MCP bridge's default path does not survive); `malachid` as
a LaunchAgent (today the app starts and stops it, like the GTK UI); an
update mechanism (Sparkle, or the App Store, which reopens the licence
question).

**Repository and licence, decided 2026-09-24.** The client lives in this
repository under `macos/`, so the API contract and the clients move
together in one commit; rule 4 was reworded the same day so that other
platforms are separate clients of the daemon's API rather than branches
of the GTK code. The client is GPL-3.0-or-later like everything outside
`backend/`; `backend/` stays AGPL-3.0-only, the boundary case
[LICENSING.md](../LICENSING.md) anticipates with the commercial core
licence. App Store distribution, if it ever matters, reopens this.

**The Assistant menu came to macOS first (2026-09-29).** It hands the
selected mail to Claude Desktop or Claude Code through their `claude:` and
`claude-cli:` links ([mcp.md](mcp.md#hand-off-from-the-app-the-assistant-menu)).
GTK has its logic and msgids (`ui/internal/assistant`) but not the
widgets yet, Windows only the settings keys; once GTK has a Blueprint for
it, that becomes the reference as everywhere else. The hand-off itself
depends on the Claude apps' link handlers, which only a manual test with
Claude Desktop and Claude Code installed covers.
