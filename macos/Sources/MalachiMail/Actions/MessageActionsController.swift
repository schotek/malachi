// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The AppKit half of the per-message actions: the `win.*` actions of the
/// main window (window.go `registerActions`, the star and compose buttons)
/// acting on the selection, and the `msg.*` actions of a message window
/// (message_window.go), the remote-images bar, the outbox banner, the
/// attachment chips and the HTML view acting on one message. Everything
/// that touches the model or the daemon is the `ActionsController`'s; this
/// only resolves the selection, the window a sheet goes on, and the links
/// and attachments that need the desktop (remote.go `openLink`,
/// attachments.go).
///
/// It installs itself as the controller's hooks; the application installs
/// it as `MessageWindows.delegate`, `MainWindowController.messageActions`
/// and `ListController.onMarkRead`'s target.
@MainActor
final class MessageActionsController: MessageActions, MessageActionDelegate {
    let state: AppState
    let actions: ActionsController
    let list: ListController
    let cache: MessageCache
    let windows: MessageWindows
    let attachments: AttachmentActions

    private let mainWindow: @MainActor () -> NSWindow?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "actions")

    /// - Parameters:
    ///   - mainWindow: the window the main window's confirmations go on;
    ///     nil falls back to an application-modal alert.
    init(
        state: AppState, actions: ActionsController, list: ListController, cache: MessageCache,
        windows: MessageWindows, mainWindow: @escaping @MainActor () -> NSWindow?
    ) {
        self.state = state
        self.actions = actions
        self.list = list
        self.cache = cache
        self.windows = windows
        self.mainWindow = mainWindow
        attachments = AttachmentActions(state: state, cache: cache)
        installHooks()
    }

    /// The controller's AppKit needs: the confirmation sheet, the compose
    /// window, the message windows and the views showing a star or an
    /// outbox banner.
    private func installHooks() {
        let alerts = state.alerts
        actions.confirm = { parent, heading, body, label in
            await alerts.confirmDestructive(on: parent as? NSWindow, heading: heading, body: body, confirmLabel: label)
        }
        actions.openCompose = { [weak self] params in
            guard let self, let open = self.state.hooks.openCompose else { return }
            open(params)
        }
        actions.raiseDraft = { [weak self] draft in
            self?.state.hooks.raiseDraft?(draft) ?? false
        }
        actions.openMessageWindow = { [weak self] summary in
            self?.windows.openMessage(summary)
        }
        actions.onWindowsClose = { [weak self] id in
            self?.windows.closeMessageWindow(id)
        }
        actions.onStarChanged = { [weak self] _, _ in
            guard let self else { return }
            self.windows.refreshStars()
            self.mainWindow()?.toolbar?.validateVisibleItems()
        }
        actions.onOutboxStateChanged = { [weak self] id in
            self?.windows.showOutboxState(id)
        }
    }

    // MARK: MessageActions (the selection; window.go `registerActions`)

    var flags: ActionFlags {
        actions.actionFlags(for: list.selectedRow)
    }

    /// Runs `fn` on the selected row's message (a conversation row's newest
    /// folder member; window.go `selectedMessage`).
    private func forSelected(_ fn: (MessageID) -> Void) {
        if let s = list.selectedRow?.message {
            fn(s.id)
        }
    }

    func reply() {
        forSelected { actions.openCompose(.reply, $0) }
    }

    func replyAll() {
        forSelected { actions.openCompose(.replyAll, $0) }
    }

    func forward() {
        forSelected { actions.openCompose(.forward, $0) }
    }

    func trash() {
        list.selectedIDs { [weak self] row, ids in
            guard let self else { return }
            self.actions.trash(ids, subject: self.list.rowSubject(row), parent: self.mainWindow())
        }
    }

    func junk() {
        list.selectedIDs { [weak self] row, ids in
            guard let self else { return }
            self.actions.junk(ids, subject: self.list.rowSubject(row), parent: self.mainWindow())
        }
    }

    func archive() {
        list.selectedIDs { [weak self] _, ids in
            self?.actions.archive(ids)
        }
    }

    /// On a conversation row the star acts on every member (`flagTarget`).
    func toggleFlag() {
        list.selectedIDs { [weak self] row, ids in
            guard let self else { return }
            self.actions.setFlagged(ids, self.list.flagTarget(row))
        }
    }

    func markRead() {
        list.selectedIDs { [weak self] _, ids in
            self?.actions.setSeen(ids, true)
        }
    }

    func markUnread() {
        list.selectedIDs { [weak self] _, ids in
            self?.actions.setSeen(ids, false)
        }
    }

    func loadImages() {
        forSelected { actions.loadImages($0) }
    }

    func trustSender() {
        forSelected { actions.trustSender($0) }
    }

    // MARK: MessageActionDelegate (one message; message_window.go `msg.*`)

    /// The current state of the message, not the summary the window was
    /// opened with (message_window.go `newMessageWindow` acts on the live
    /// row through the window's actions: the star and Mark as Read follow
    /// the model).
    func flags(for summary: MessageSummary) -> ActionFlags {
        actions.flags(for: actions.summary(summary.id) ?? summary)
    }

    func reply(_ id: MessageID) {
        actions.openCompose(.reply, id)
    }

    func replyAll(_ id: MessageID) {
        actions.openCompose(.replyAll, id)
    }

    func forward(_ id: MessageID) {
        actions.openCompose(.forward, id)
    }

    func trash(_ id: MessageID, from window: NSWindow?) {
        actions.trash(id, parent: window)
    }

    func junk(_ id: MessageID, from window: NSWindow?) {
        actions.junk(id, parent: window)
    }

    func archive(_ id: MessageID) {
        actions.archive([id])
    }

    func toggleFlag(_ id: MessageID) {
        actions.toggleFlagged(id)
    }

    func markRead(_ id: MessageID) {
        actions.markRead(id)
    }

    func markUnread(_ id: MessageID) {
        actions.markUnread(id)
    }

    func loadImages(_ id: MessageID) {
        actions.loadImages(id)
    }

    func trustSender(_ id: MessageID) {
        actions.trustSender(id)
    }

    func retryOutbox(_ id: MessageID) {
        actions.retryOutbox(id)
    }

    func isDraft(_ summary: MessageSummary) -> Bool {
        actions.mailbox.model.inDrafts(summary)
    }

    func editDraft(_ id: MessageID) {
        actions.openDraft(id)
    }

    // MARK: Links (remote.go `openLink`)

    /// A link the user activated in a message: mailto: opens a new message,
    /// http(s) is handed to the desktop, after a look at whether the link's
    /// text pretends to lead elsewhere. `href` is the attribute as written
    /// (`ActivatedLink.href`) and `links` are the body's links as the
    /// daemon listed them, text and real target side by side, matched
    /// exactly (`linkDecision`). Stricter than GTK in one respect: an
    /// http(s) link the list does not hold is never opened silently, the
    /// destination is shown first.
    func openLink(_ href: String, links: [Link], from window: NSWindow?) {
        switch linkDecision(href, links) {
        case .refused:
            return
        case .mailto(let target):
            openMailto(target)
        case .open(let target):
            launch(target, from: window)
        case .confirm(let text, let target):
            let alerts = state.alerts
            Task { @MainActor [weak self] in
                guard let self else { return }
                let open: Bool
                if text.isEmpty {
                    open = await self.openUnlistedLinkQuestion(on: window, href: target)
                } else {
                    // The text says one site, the target is another.
                    open = await alerts.openLinkQuestion(on: window, text: text, href: target)
                }
                if open {
                    self.launch(target, from: window)
                }
            }
        }
    }

    /// "Open This Link?" for a link the daemon did not list, so nothing is
    /// known about the text it wore: the destination alone. A sheet on
    /// `window`, or application-modal without one (as `AppAlerts` does).
    private func openUnlistedLinkQuestion(on window: NSWindow?, href: String) async -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = L10n.T("Open This Link?")
        // TRANSLATORS: %s is the link's real destination.
        alert.informativeText = L10n.T("This link leads to %s.", href) // macOS-only string
        let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
        cancel.keyEquivalent = "\u{1b}"
        let open = alert.addButton(withTitle: mn(L10n.T("_Open Link")))
        open.keyEquivalent = ""
        let response: NSApplication.ModalResponse
        if let window, window.isVisible, window.attachedSheet == nil {
            response = await alert.beginSheetModal(for: window)
        } else {
            response = alert.runModal()
        }
        return response == .alertSecondButtonReturn
    }

    /// A mailto: link as a new message (compose.ParseMailto) through the
    /// compose hook.
    private func openMailto(_ href: String) {
        let params: ComposeParams
        do {
            params = try parseMailto(href)
        } catch {
            log.debug("mailto link refused: \(String(describing: error), privacy: .public)")
            return
        }
        guard let open = state.hooks.openCompose else { return }
        open(params)
    }

    /// An address chip's New Message (addresses.go `chip`): a new message
    /// to that address, from the account of the message it sits on.
    func newMessage(to address: Address, account: AccountID) {
        guard let open = state.hooks.openCompose else { return }
        open(ComposeParams(kind: .new, accountID: account, to: [address]))
    }

    /// Opens `href` with the desktop's handler (remote.go `launchURI`).
    private func launch(_ href: String, from window: NSWindow?) {
        guard let url = URL(string: href) else {
            log.warning("open link: not a URL")
            // TRANSLATORS: %s is a technical error message.
            toast(L10n.T("The link could not be opened: %s", "invalid URL"), in: window) // macOS-only string (the technical detail)
            return
        }
        Task { @MainActor [weak self] in
            do {
                _ = try await NSWorkspace.shared.open(url, configuration: NSWorkspace.OpenConfiguration())
            } catch {
                guard let self else { return }
                // The error names the URL: mail content, private.
                let ns = error as NSError
                self.log.warning("open link: \(ns.domain, privacy: .public) \(ns.code, privacy: .public): \(String(describing: error), privacy: .private)")
                // TRANSLATORS: %s is a technical error message.
                self.toast(L10n.T("The link could not be opened: %s", error.localizedDescription), in: window)
            }
        }
    }

    // MARK: Attachments (attachments.go)

    func previewAttachment(_ attachment: Attachment, of summary: MessageSummary, from window: NSWindow?, source: NSView?) {
        attachments.preview(attachment, of: summary, from: window, source: source)
    }

    func openAttachment(_ attachment: Attachment, of summary: MessageSummary, from window: NSWindow?) {
        attachments.open(attachment, of: summary, from: window)
    }

    func saveAttachment(_ attachment: Attachment, of summary: MessageSummary, from window: NSWindow?) {
        attachments.saveAs(attachment, of: summary, from: window)
    }

    func saveAllAttachments(
        _ attachments: [Attachment], of summary: MessageSummary, from window: NSWindow?,
        done: @escaping @MainActor () -> Void
    ) {
        self.attachments.saveAll(attachments, of: summary, from: window, done: done)
    }

    // MARK: Helpers

    private func toast(_ text: String, in window: NSWindow?) {
        windowToast(text, in: window, or: state.toasts)
    }
}

/// A toast in the window where the click was, when that window has an
/// overlay of its own (a message window); otherwise through `fallback`,
/// wherever toasts go now (window.go `Toast`).
@MainActor
func windowToast(_ text: String, in window: NSWindow?, or fallback: any Toasts) {
    if let host = window?.windowController as? any ToastHosting {
        host.toasts.show(text)
    } else {
        fallback.show(text)
    }
}
