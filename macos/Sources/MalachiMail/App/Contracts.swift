// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The seams between the application shell and the parts that plug into it
// (sidebar, list, reader, compose, wizard). The shell provides `Toasts` and
// `Alerts`; the message actions controller provides `MessageActions`.
// Everything here is main-actor: it is the UI talking to itself.

/// Transient messages over the message pane (Adw.ToastOverlay,
/// window.go `Toast`/`ToastFor`). Text only, never markup.
@MainActor
protocol Toasts: AnyObject {
    /// Shows `text` for the default 5 s.
    func show(_ text: String)
    /// Shows `text` for `seconds`; 0 keeps it until the next toast replaces it.
    func show(_ text: String, seconds: Int)
}

/// What the user answered to "Save changes to this draft?".
enum SaveDraftAnswer: Sendable, Equatable {
    case save, discard, cancel
}

/// The confirmation dialogs of the GTK UI (widget/rpc.go
/// `ConfirmDestructive`, compose/draft.go, window/remote.go), presented as
/// sheets on the window that owns the action, or application-modal when
/// there is none. Heading and body are plain text: the body is often
/// hostile input such as a subject.
@MainActor
protocol Alerts: AnyObject {
    /// Cancel / `confirmLabel` (destructive). True when confirmed.
    func confirmDestructive(on window: NSWindow?, heading: String, body: String, confirmLabel: String) async -> Bool

    /// `confirmDestructive` with a check box between the body and the
    /// buttons ("Also delete drafts and downloaded data").
    func confirmDestructiveExtra(
        on window: NSWindow?, heading: String, body: String, confirmLabel: String,
        extraLabel: String, extraDefault: Bool
    ) async -> (confirmed: Bool, extra: Bool)

    /// Save Draft (default) / Cancel / Discard (destructive).
    func saveDraftQuestion(on window: NSWindow?) async -> SaveDraftAnswer

    /// A question whose two answers are both safe: `confirmLabel` (the
    /// default: Return) / `declineLabel` (Escape). True for
    /// `confirmLabel`. "Restart Claude Desktop?" after a change of
    /// "Register with Claude" (`ClaudeDesktopController`).
    func confirm(on window: NSWindow?, heading: String, body: String, confirmLabel: String, declineLabel: String) async -> Bool

    /// "Open This Link?" for a link whose text says one site and whose
    /// target is another (`text` is the visible text, `href` the target).
    /// True when the user wants it opened.
    func openLinkQuestion(on window: NSWindow?, text: String, href: String) async -> Bool

    /// "Trust This Certificate?" (accountwizard trust.go,
    /// `ConfirmDestructiveExtra` with the certificate's details): Cancel /
    /// `confirmLabel` (destructive), the details between the body and the
    /// buttons as selectable plain text, the fingerprint monospaced. True
    /// when confirmed.
    func confirmTrustCertificate(
        on window: NSWindow?, heading: String, body: String, details: [CertificateDetail], confirmLabel: String
    ) async -> Bool
}

/// The per-message actions of the main window, acting on the current
/// selection like the GTK `win.*` actions (window/actions.go).
/// `MessageActionsController` implements it; the window controller
/// forwards its menu and toolbar actions here and validates them from
/// `flags`.
@MainActor
protocol MessageActions: AnyObject {
    /// What the selection allows right now (`messageActionState`).
    var flags: ActionFlags { get }

    func reply()
    func replyAll()
    func forward()
    /// Moves to Trash, or cancels the send of an outbox message.
    func trash()
    func junk()
    func archive()
    func toggleFlag()
    func markRead()
    func markUnread()
    func loadImages()
    func trustSender()

    /// The Assistant menu (ui/internal/assistant): a message action on the
    /// selection, a conversation row's folder members newest first or the
    /// one message, handed to Claude.
    func askAssistant(_ action: Assistant.Action)
    /// Summarize Unread in This Folder, for the folder selected in the
    /// sidebar.
    func summarizeUnread()
    /// A folder is selected and it is not an Outbox.
    var canSummarizeUnread: Bool { get }
}

/// The same actions addressed by message rather than by selection: what a
/// message window's toolbar (message_window.go `msg.*`), the remote-images
/// bar, the outbox banner, the attachment chips and the HTML view call.
/// `MessageActionsController` implements it; the message views consume it.
/// Every method decides for itself whether the message still exists.
@MainActor
protocol MessageActionDelegate: AnyObject {
    /// What the message allows right now (actions.go `messageActionState`
    /// for a single message; window-local actions).
    func flags(for summary: MessageSummary) -> ActionFlags

    func reply(_ id: MessageID)
    func replyAll(_ id: MessageID)
    /// compose_open.go `openComposeFrom`: `window` receives "Forward
    /// Without Attachments?" when the download of the attachments fails.
    func forward(_ id: MessageID, from window: NSWindow?)
    /// Moves to Trash, or cancels the send of an outbox message
    /// (`window` receives the confirmation sheet).
    func trash(_ id: MessageID, from window: NSWindow?)
    func junk(_ id: MessageID, from window: NSWindow?)
    func archive(_ id: MessageID)
    func toggleFlag(_ id: MessageID)
    func markRead(_ id: MessageID)
    func markUnread(_ id: MessageID)
    /// remote.go `loadRemoteImages` / `trustSender`: the views re-render
    /// through the message cache's `onLoaded`.
    func loadImages(_ id: MessageID)
    func trustSender(_ id: MessageID)
    /// remote.go `downloadPictures`: the pictures bar's Download Pictures;
    /// a failure toasts in `window` (the one the click came from).
    func downloadPictures(_ id: MessageID, from window: NSWindow?)
    /// outbox.go `retryOutbox`.
    func retryOutbox(_ id: MessageID)
    /// model.go `inDrafts`: the message lies in its account's Drafts folder
    /// (the pane shows the draft banner).
    func isDraft(_ summary: MessageSummary) -> Bool
    /// drafts.go `openDraft`: the draft banner's Edit button.
    func editDraft(_ id: MessageID)
    /// remote.go `openLink`: allow-list, mailto → compose, masked-link
    /// alert on `window`, then the browser.
    func openLink(_ href: String, links: [Link], from window: NSWindow?)
    /// addresses.go: an address chip's New Message, written from
    /// `account` (that of the message the chip sits on).
    func newMessage(to address: Address, account: AccountID)
    /// attachments.go: Preview (a click on the chip; `source` finds the
    /// chip of the part actually fetched, for the panel to zoom out of,
    /// once the file is ready: the chips are drawn again while a download
    /// runs), Open (never for executables), Save As…, Save All. `remote`
    /// is what the chip showed (`partState`): the part is on the mail
    /// server, so the message is downloaded first; for Save All, that any
    /// of them is (`anyRemote`), and the message is downloaded once.
    func previewAttachment(
        _ attachment: Attachment, of summary: MessageSummary, remote: Bool, from window: NSWindow?,
        source: @escaping @MainActor (_ partId: String) -> NSView?)
    func openAttachment(_ attachment: Attachment, of summary: MessageSummary, remote: Bool, from window: NSWindow?)
    func saveAttachment(_ attachment: Attachment, of summary: MessageSummary, remote: Bool, from window: NSWindow?)
    /// While it runs the message's Save All buttons stay disabled
    /// (`MessageCache.isSavingAll`, attachments.go `savingAll`).
    func saveAllAttachments(_ attachments: [Attachment], of summary: MessageSummary, remote: Bool, from window: NSWindow?)
    /// The Assistant menu of a message window: a message action on that
    /// one message (ui/internal/assistant).
    func askAssistant(_ action: Assistant.Action, about summary: MessageSummary, from window: NSWindow?)
    /// A chip's "Ask the Assistant…": the file handed to Claude, downloaded
    /// first when `remote`.
    func askAssistant(about attachment: Attachment, of summary: MessageSummary, remote: Bool, from window: NSWindow?)
}

/// The formatted-text editor of a compose window (ui/internal/editor
/// `Editor`), backed by a WKWebView with content JavaScript off and the
/// bridge script from `bridgeJS`. `ComposeEditorView` implements it; the
/// compose window consumes it. `html()` and
/// `text()` return the page's last `changed` message: call `flush` before
/// reading them for a save.
@MainActor
protocol EditorView: AnyObject {
    /// The widget to place into the compose window's editor slot.
    var view: NSView { get }
    /// Whether the page reported `ready` for the current document.
    var isReady: Bool { get }

    /// editor.Load: renders `editorDocument(body:)` around `bodyHTML`
    /// (already escaped/sanitised by its producer) and resets `isReady`.
    func load(bodyHTML: String)
    /// editor.HTML: the body's inner HTML as last reported.
    func html() -> String
    /// editor.Text: the body's inner text as last reported.
    func text() -> String
    /// editor.Flush: asks the page for its current content and calls
    /// `done` after the `changed` message arrived (or right away when the
    /// page is not ready).
    func flush(_ done: @escaping @MainActor () -> Void)
    /// editor.Exec: runs an editing command (`bold`, `formatBlock` with
    /// `h1`, `createLink`, `foreColor`, `insertImage`, `justifyLeft`,
    /// `insertUnorderedList`, `removeFormat`, `unlink`, …).
    func exec(_ command: String, _ argument: String?)
    /// editor.FocusStart: caret to the start of the body (replies).
    func focusStart()
    /// The assistant's rewrite (a macOS addition to the bridge,
    /// ui/internal/assistant): notes the passage to rewrite, the selection
    /// when it holds more than white space, otherwise the user's own text
    /// before the `div` holding `attribution` (the whole body when "" or
    /// not found), and hands it over; nil when the page is not ready or
    /// the script failed.
    func rewriteTarget(attribution: String, _ done: @escaping @MainActor (RewriteTarget?) -> Void)
    /// Puts `text` in place of the passage `rewriteTarget` noted, or below
    /// it, as plain text (`rewriteInsertion`): one edit the page's undo
    /// takes back, reported as a change like typing.
    func applyRewrite(_ text: String, below: Bool)

    /// editor.RegisterCID / RegisterFetcher / UnregisterCID for the
    /// `cid:` scheme of this process (shared registry).
    func registerCID(_ id: String, path: String, contentType: String)
    func registerCIDFetcher(_ id: String, fetch: @escaping CIDFetcher)
    func unregisterCID(_ id: String)

    /// The page finished loading and the bridge is up.
    var onReady: (@MainActor () -> Void)? { get set }
    /// The content changed (after `html()`/`text()` were updated).
    var onChanged: (@MainActor () -> Void)? { get set }
    /// The formatting at the caret changed.
    var onState: (@MainActor (EditorState) -> Void)? { get set }
    /// Files dropped onto the editor (the window imports them as
    /// attachments; WebKit never inserts `file:` URLs).
    var onDropFiles: (@MainActor ([URL]) -> Void)? { get set }
    /// The web content process died; the window may reload the last
    /// content on request.
    var onCrashed: (@MainActor () -> Void)? { get set }
}
