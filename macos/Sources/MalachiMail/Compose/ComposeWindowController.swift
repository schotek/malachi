// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// One compose window (compose.blp, ui/internal/compose/compose.go): the
/// toolbar with Attach, the draft menu and Send; the card of header fields;
/// the formatting bar; the editor; the attachment chips; the status line.
/// The draft's lifecycle (autosave, save, send, discard, the close
/// question) is `ComposeDraftController`, which reads the window through
/// `ComposeForm`; the account list arrives from the manager through
/// `ComposeWindowHandle`. The menu bar's compose and Format items reach the
/// controller through the responder chain (`MalachiActions`). Every string
/// shown from mail data is plain text.
///
/// With the assistant's In App target (ui/internal/assistant rewrite.go,
/// while `AssistantController.canRunInApp`) the toolbar has an Assistant
/// button before the draft menu: its popover (`ComposeRewriteViewController`)
/// rewrites the selection, or the user's own text above the quoted
/// original (what the editor holds before the attribution line of
/// `params.attribution`), with the user's Claude Code, and puts the answer
/// in its place or below it as plain text through the editor bridge, one
/// edit ⌘Z takes back. The first request ever asks for consent on this
/// window (the panel's sheet, `assistant-consent`). Closing the popover
/// or the window ends a running request.
@MainActor
final class ComposeWindowController: NSWindowController, NSWindowDelegate, ToastHosting, NSPopoverDelegate {
    static let defaultSize = NSSize(width: 760, height: 640)
    static let minimumSize = NSSize(width: 360, height: 420)
    static let toolbarIdentifier = NSToolbar.Identifier("compose")

    let state: AppState
    unowned let manager: ComposeManager
    /// The window's content: the fields, the editor, the draft
    /// (`ComposePane`, window layout). Owned as the content view controller.
    let pane: ComposePane
    var params: ComposeParams { pane.params }
    var editor: any EditorView { pane.editor }
    var draft: ComposeDraftController { pane.draft }
    /// The toast overlay over the content (the pane's).
    var toasts: ToastPresenter { pane.toasts }
    private let toolbarDelegate: ComposeToolbar

    private var escape: EscapeCloser?
    /// The controller decided the window may close: `windowShouldClose`
    /// stops asking.
    private var closing = false
    /// The close question is up; a second close request waits for it.
    private var closeQuestionPending = false
    /// The assistant's rewrite (the In App target): its controller, made
    /// on first use, and the popover while it is shown.
    private var rewrite: ComposeRewriteController?
    private var rewritePopover: NSPopover?
    /// The Assistant button was clicked and the popover is on its way
    /// (consent, the editor's passage).
    private var openingRewrite = false
    private var assistantToken: AssistantController.Token?
    private var assistantTargetToken: Settings.ChangeToken?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "compose")

    /// - Parameters:
    ///   - state: the application state (client, settings, alerts, toasts).
    ///   - manager: the manager that opened the window; told when it closes.
    ///   - params: what the window is prefilled with.
    ///   - editor: the formatted-text editor to put into the editor slot.
    init(state: AppState, manager: ComposeManager, params: ComposeParams, editor: any EditorView) {
        self.state = state
        self.manager = manager
        pane = ComposePane(state: state, accounts: manager.controller, params: params, editor: editor)
        toolbarDelegate = ComposeToolbar()
        // Whether Claude Code is there is looked up now (a few stat calls),
        // so the Assistant button reflects it from the start.
        state.assistant.refreshHandlers()
        toolbarDelegate.showsAssistant = state.assistant.canRunInApp

        let w = NSWindow(
            contentRect: NSRect(origin: .zero, size: Self.defaultSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false
        )
        w.title = L10n.T("New Message")
        w.minSize = Self.minimumSize
        w.toolbarStyle = .unified
        w.tabbingMode = .disallowed
        w.isReleasedWhenClosed = false
        super.init(window: w)

        w.delegate = self
        w.toolbar = toolbarDelegate.makeToolbar()
        w.contentViewController = pane
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        w.initialFirstResponder = pane.initialResponder
        w.autorecalculatesKeyViewLoop = true
        applyCommentMode()
        pane.host = self
        updateTitle()

        wireAssistant()
        escape = EscapeCloser.install(on: w)
        escape?.shouldClose = { [weak self] in
            guard let self else { return true }
            return self.pane.popupsHidden
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// compose.go `updateTitle`: the pane's title (the subject, "New
    /// Message", or the comment's issue).
    func updateTitle() {
        window?.title = pane.titleText
    }

    /// editor.GrabFocus: the keyboard back to the page.
    func focusEditor() {
        pane.focusEditor()
    }

    // MARK: Assistant

    /// The Assistant button follows `canRunInApp`: the Assistant shown, In
    /// App chosen and Claude Code found.
    private func wireAssistant() {
        assistantToken = state.assistant.onChange { [weak self] in
            self?.updateAssistant()
        }
        assistantTargetToken = state.settings.onChange(.assistantTarget) { [weak self] in
            self?.updateAssistant()
        }
    }

    private func updateAssistant() {
        let available = state.assistant.canRunInApp
        if let toolbar = window?.toolbar {
            toolbarDelegate.setAssistant(visible: available, in: toolbar)
        }
        if !available {
            rewritePopover?.performClose(nil)
        }
    }

    /// The toolbar's Assistant button: the rewrite's popover, or it closes.
    /// The first request ever asks for consent first, so that the sheet
    /// does not close the popover under it.
    @objc func showAssistantRewrite(_ sender: Any?) {
        if let p = rewritePopover, p.isShown {
            p.performClose(sender)
            return
        }
        guard state.assistant.canRunInApp, !openingRewrite, !closing else { return }
        openingRewrite = true
        Task { @MainActor [weak self] in
            guard let self else { return }
            if !self.state.settings.assistantConsent {
                guard await self.askConsent() else {
                    self.openingRewrite = false
                    return
                }
                self.state.settings.assistantConsent = true
            }
            self.editor.rewriteTarget(attribution: self.params.attribution) { [weak self] target in
                guard let self else { return }
                self.openingRewrite = false
                self.presentRewrite(target ?? RewriteTarget(selected: false, text: ""))
            }
        }
    }

    /// "Send Mail to Claude?" on this window (the panel's consent).
    private func askConsent() async -> Bool {
        let t = Assistant.panelTexts()
        return await state.alerts.confirm(
            on: window, heading: t.consentHeading, body: t.consentBody, confirmLabel: t.allow, declineLabel: t.cancel)
    }

    private func rewriteController() -> ComposeRewriteController {
        if let rewrite {
            return rewrite
        }
        let request = AssistantRequest(settings: state.settings, locator: state.claudeCode)
        request.consent = { [weak self] in
            await self?.askConsent() ?? false
        }
        let c = ComposeRewriteController(request: request)
        rewrite = c
        return c
    }

    private func presentRewrite(_ target: RewriteTarget) {
        guard state.assistant.canRunInApp, !closing, let window, window.isVisible else { return }
        let controller = rewriteController()
        controller.cancel()
        let vc = ComposeRewriteViewController(controller: controller, target: target)
        vc.onClose = { [weak self] in
            self?.rewritePopover?.performClose(nil)
        }
        vc.onApply = { [weak self] text, below in
            self?.applyRewrite(text, below: below)
        }
        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentViewController = vc
        popover.delegate = self
        rewritePopover = popover
        if window.toolbar?.isVisible == true, let item = toolbarDelegate.assistantItem, item.isVisible {
            popover.show(relativeTo: item)
        } else {
            let anchor = editor.view
            popover.show(relativeTo: NSRect(x: anchor.bounds.midX, y: anchor.bounds.minY, width: 1, height: 1), of: anchor, preferredEdge: .maxY)
        }
    }

    /// Replace or Insert Below: the popover goes, the editor takes the
    /// keyboard (so that ⌘Z reaches its undo) and the answer as plain text.
    private func applyRewrite(_ text: String, below: Bool) {
        rewritePopover?.performClose(nil)
        focusEditor()
        editor.applyRewrite(text, below: below)
    }

    /// The popover went (Discard, a click elsewhere, Escape, an apply):
    /// a running request ends.
    func popoverDidClose(_ notification: Foundation.Notification) {
        guard let popover = notification.object as? NSPopover, popover === rewritePopover else { return }
        rewritePopover = nil
        rewrite?.cancel()
    }

    // MARK: NSWindowDelegate

    /// closeRequest: the window goes at once when nothing is at stake;
    /// otherwise the draft controller asks and the window closes later
    /// when allowed.
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if closing {
            return true
        }
        if draft.canCloseWithoutAsking {
            draft.cleanup()
            closing = true
            return true
        }
        guard !closeQuestionPending else { return false }
        closeQuestionPending = true
        Task { [weak self] in
            guard let self else { return }
            let allowed = await self.draft.closeRequest()
            self.closeQuestionPending = false
            if allowed {
                self.closing = true
                self.window?.close()
            }
        }
        return false
    }

    func windowDidBecomeKey(_ notification: Foundation.Notification) {
        state.toasts.presenter = toasts
    }

    /// cleanup: the window really closes.
    func windowWillClose(_ notification: Foundation.Notification) {
        closing = true
        escape?.uninstall()
        escape = nil
        assistantToken?.cancel()
        assistantToken = nil
        assistantTargetToken?.cancel()
        assistantTargetToken = nil
        rewritePopover?.close()
        rewritePopover = nil
        rewrite?.cancel()
        pane.cleanup()
        manager.remove(self)
    }
}

// MARK: - ComposePaneHost

extension ComposeWindowController: ComposePaneHost {
    var paneWindow: NSWindow? { window }

    func paneTitleChanged(_ pane: ComposePane) {
        updateTitle()
    }

    /// The toolbar's Send (`ComposeForm.setSendEnabled`).
    func paneSendEnabledChanged(_ pane: ComposePane, _ enabled: Bool) {
        toolbarDelegate.sendButton.isEnabled = enabled
        window?.toolbar?.validateVisibleItems()
    }

    func paneToast(_ text: String) {
        toasts.show(text)
    }

    /// `ComposeForm.closeWindow`: the controller decided, the window goes
    /// without asking.
    func paneDidEnd(_ pane: ComposePane, _ end: ComposePane.End) {
        closing = true
        window?.close()
    }

    func paneHeightChanged(_ pane: ComposePane) {}
}

// MARK: - ComposeWindowHandle

extension ComposeWindowController: ComposeWindowHandle {
    func setAccounts(_ list: [Account], placeholder: Bool) {
        pane.setAccounts(list, placeholder: placeholder)
    }

    func toast(_ text: String) {
        pane.toast(text)
    }

    /// manager.go `FindDraft`.
    func edits(_ d: Draft) -> Bool {
        pane.edits(d)
    }

    func present() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }
}

// MARK: - Actions (compose.blp `compose.*`, the Format menu)

// While a view of the content has the keyboard, the responder chain reaches
// the pane (the content view controller) before the window; while the
// window itself is first responder it does not, so every compose and Format
// action and its validation is forwarded to the pane here.

extension ComposeWindowController: MalachiActions {
    @objc func sendMessage(_ sender: Any?) { pane.sendMessage(sender) }
    @objc func saveDraft(_ sender: Any?) { pane.saveDraft(sender) }
    @objc func discardDraft(_ sender: Any?) { pane.discardDraft(sender) }
    @objc func attachFiles(_ sender: Any?) { pane.attachFiles(sender) }
    @objc func insertImage(_ sender: Any?) { pane.insertImage(sender) }
    @objc func formatBold(_ sender: Any?) { pane.formatBold(sender) }
    @objc func formatItalic(_ sender: Any?) { pane.formatItalic(sender) }
    @objc func formatUnderline(_ sender: Any?) { pane.formatUnderline(sender) }
    @objc func formatParagraph(_ sender: Any?) { pane.formatParagraph(sender) }
    @objc func formatHeading1(_ sender: Any?) { pane.formatHeading1(sender) }
    @objc func formatHeading2(_ sender: Any?) { pane.formatHeading2(sender) }
    @objc func formatHeading3(_ sender: Any?) { pane.formatHeading3(sender) }
    @objc func alignLeft(_ sender: Any?) { pane.alignLeft(sender) }
    @objc func alignCenter(_ sender: Any?) { pane.alignCenter(sender) }
    @objc func alignRight(_ sender: Any?) { pane.alignRight(sender) }
    @objc func bulletedList(_ sender: Any?) { pane.bulletedList(sender) }
    @objc func numberedList(_ sender: Any?) { pane.numberedList(sender) }
    @objc func quoteBlock(_ sender: Any?) { pane.quoteBlock(sender) }
    @objc func insertLink(_ sender: Any?) { pane.insertLink(sender) }
    @objc func clearFormatting(_ sender: Any?) { pane.clearFormatting(sender) }
}

extension ComposeWindowController: NSUserInterfaceValidations {
    /// The pane's (Send off while sending, the Format check marks, comment
    /// mode); everything else of the window (the Assistant button) on.
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        pane.validateUserInterfaceItem(item)
    }
}

extension ComposeWindowController: NSToolbarItemValidation {
    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        pane.validateToolbarItem(item)
    }
}

// MARK: - Toolbar

/// The header bar of compose.blp as a unified toolbar: Attach, the draft
/// menu and Send. Items act through the responder chain. With the
/// assistant's In App target the Assistant button (ui/internal/assistant;
/// GTK compose.blp `rewrite_button`) sits before the draft menu while
/// `showsAssistant`.
@MainActor
private final class ComposeToolbar: NSObject, NSToolbarDelegate {
    enum ID {
        static let attach = NSToolbarItem.Identifier("composeAttach")
        static let assistant = NSToolbarItem.Identifier("composeAssistant")
        static let draftMenu = NSToolbarItem.Identifier("composeDraftMenu")
        static let send = NSToolbarItem.Identifier("composeSend")
    }

    static let items: [NSToolbarItem.Identifier] = [ID.attach, .flexibleSpace, ID.assistant, ID.draftMenu, ID.send]

    /// Whether the Assistant button is in the toolbar
    /// (`AssistantController.canRunInApp`).
    var showsAssistant = false
    /// The Assistant button, once made (the rewrite's popover points at it).
    private(set) var assistantItem: NSToolbarItem?

    /// Puts the Assistant button into `toolbar` (right before the draft
    /// menu) or takes it out.
    func setAssistant(visible: Bool, in toolbar: NSToolbar) {
        showsAssistant = visible
        let current = toolbar.items.firstIndex { $0.itemIdentifier == ID.assistant }
        if visible {
            guard current == nil else { return }
            let at = toolbar.items.firstIndex { $0.itemIdentifier == ID.draftMenu } ?? toolbar.items.count
            toolbar.insertItem(withItemIdentifier: ID.assistant, at: at)
        } else if let current {
            toolbar.removeItem(at: current)
        }
    }

    /// `send_button`: the suggested action, ⌘↩.
    let sendButton: NSButton

    override init() {
        sendButton = NSButton(title: mn(L10n.T("_Send")), image: Icon.symbol("paperplane.fill", size: .toolbar), target: nil, action: Action.sendMessage)
        super.init()
        sendButton.bezelStyle = .rounded
        sendButton.imagePosition = .imageLeading
        sendButton.imageScaling = .scaleProportionallyDown
        sendButton.bezelColor = .controlAccentColor
        sendButton.keyEquivalent = "\r"
        sendButton.keyEquivalentModifierMask = .command
        sendButton.toolTip = mn(L10n.T("_Send")) + " (⌘↩)"
        sendButton.setAccessibilityLabel(mn(L10n.T("_Send")))
    }

    func makeToolbar() -> NSToolbar {
        let tb = NSToolbar(identifier: ComposeWindowController.toolbarIdentifier)
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        return tb
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        showsAssistant ? Self.items : Self.items.filter { $0 != ID.assistant }
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        Self.items
    }

    func toolbar(
        _ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool
    ) -> NSToolbarItem? {
        switch id {
        case ID.attach:
            let it = NSToolbarItem(itemIdentifier: id)
            it.image = Icon.image("mail-attachment", size: .toolbar)
            it.label = L10n.T("Attach Files")
            it.paletteLabel = it.label
            it.toolTip = it.label
            it.isBordered = true
            it.target = nil
            it.action = Action.attachFiles
            return it
        case ID.assistant:
            // The assistant's rewrite (ui/internal/assistant, In App).
            if let assistantItem {
                return assistantItem
            }
            let label = Assistant.texts().assistant
            let it = NSToolbarItem(itemIdentifier: id)
            it.image = Icon.symbol("sparkles", size: .toolbar, description: label)
            it.label = label
            it.paletteLabel = label
            it.toolTip = label
            it.isBordered = true
            it.target = nil
            it.action = #selector(ComposeWindowController.showAssistantRewrite(_:))
            assistantItem = it
            return it
        case ID.draftMenu:
            let it = NSMenuToolbarItem(itemIdentifier: id)
            it.image = Icon.moreActions
            it.label = L10n.T("Draft Menu")
            it.paletteLabel = it.label
            it.toolTip = it.label
            it.showsIndicator = false
            it.isBordered = true
            it.menu = draftMenu()
            return it
        case ID.send:
            let it = NSToolbarItem(itemIdentifier: id)
            it.view = sendButton
            it.label = mn(L10n.T("_Send"))
            it.paletteLabel = it.label
            it.visibilityPriority = .high
            return it
        default:
            return nil
        }
    }

    /// compose.blp `compose_menu`.
    private func draftMenu() -> NSMenu {
        let m = NSMenu()
        m.addItem(withTitle: mn(L10n.T("_Save Draft")), action: Action.saveDraft, keyEquivalent: "s")
        m.addItem(withTitle: mn(L10n.T("Attach _Files…")), action: Action.attachFiles, keyEquivalent: "")
        m.addItem(withTitle: mn(L10n.T("Insert _Image…")), action: Action.insertImage, keyEquivalent: "")
        m.addItem(.separator())
        m.addItem(withTitle: mn(L10n.T("_Discard")), action: Action.discardDraft, keyEquivalent: "")
        return m
    }
}
