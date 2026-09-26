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
    func forward(_ id: MessageID)
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
    /// attachments.go: Preview (a click on the chip; `source` is the chip
    /// the panel zooms out of), Open (never for executables), Save As…,
    /// Save All.
    func previewAttachment(_ attachment: Attachment, of summary: MessageSummary, from window: NSWindow?, source: NSView?)
    func openAttachment(_ attachment: Attachment, of summary: MessageSummary, from window: NSWindow?)
    func saveAttachment(_ attachment: Attachment, of summary: MessageSummary, from window: NSWindow?)
    /// Save All reports its end through `done`, so the button that
    /// started it stays disabled meanwhile (attachments.go `SetSensitive`).
    func saveAllAttachments(
        _ attachments: [Attachment], of summary: MessageSummary, from window: NSWindow?,
        done: @escaping @MainActor () -> Void)
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
