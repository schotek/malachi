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
final class ComposeWindowController: NSWindowController, NSWindowDelegate, NSTextFieldDelegate, ToastHosting, NSPopoverDelegate {
    static let defaultSize = NSSize(width: 760, height: 640)
    static let minimumSize = NSSize(width: 360, height: 420)
    static let toolbarIdentifier = NSToolbar.Identifier("compose")

    let state: AppState
    unowned let manager: ComposeManager
    let params: ComposeParams
    let editor: any EditorView
    let draft: ComposeDraftController
    let toasts = ToastPresenter()

    let header = ComposeHeaderView()
    /// The issue and its visibility in place of `header` in comment mode
    /// (ComposeWindowController+Comment.swift); nil for an e-mail.
    private(set) lazy var commentHeader: CommentHeaderView? = params.comment.map { CommentHeaderView(comment: $0) }
    let formatToolbar = FormatToolbar()
    let chips = AttachmentChipsView()
    let statusLabel = NSTextField(labelWithString: "")
    private let plainHint = NSTextField(labelWithString: L10n.T("This message will be sent as plain text."))
    private let plainHintRow: NSView
    private let toolbarDelegate: ComposeToolbar

    /// The identities of the From row (`accounts`).
    private(set) var accounts: [Account] = []
    /// The identity the user picked in From (`chosenAccount`); until they
    /// do, `params.accountID` is what From shows, also after the account
    /// list arrives in place of the placeholder.
    private var chosenAccountID: AccountID?
    /// The attachments listed under the editor (`attachments`).
    var attachments: [DraftAttachment] = []
    /// The completion of the To, Cc and Bcc rows (`suggest`).
    private(set) var suggestions: [RecipientSuggestionsController] = []
    /// The formatting at the caret as last reported (for the Format menu).
    private(set) var editorState = EditorState()
    /// The editor content as of the last flush: a `changed` carrying the
    /// same content is the flush's own report, not an edit.
    private var lastFlushedHTML: String?

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
        self.params = params
        self.editor = editor
        let controller = manager.controller
        draft = ComposeDraftController(client: state.client, settings: state.settings) { [weak controller] in
            controller?.placeholder ?? true
        }
        toolbarDelegate = ComposeToolbar()
        // Whether Claude Code is there is looked up now (a few stat calls),
        // so the Assistant button reflects it from the start.
        state.assistant.refreshHandlers()
        toolbarDelegate.showsAssistant = state.assistant.canRunInApp

        plainHint.font = Typo.caption
        plainHint.textColor = Tint.secondary
        plainHint.alignment = .left
        plainHintRow = Self.inset(plainHint, top: 4, left: 12, bottom: 4, right: 12)
        plainHintRow.isHidden = composeRichText

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
        w.contentView = buildContent()
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        w.initialFirstResponder = isComment ? editor.view : header.toField.editor
        w.autorecalculatesKeyViewLoop = true
        chips.onRemove = { [weak self] id in
            self?.removeAttachment(id)
        }
        // The editor's callbacks first, as in compose.go: `ready` and
        // `state` may follow the load at any time.
        wireEditor()
        applyCommentMode()

        // Prefill before connecting change handlers so it does not count
        // as an edit.
        header.toField.stringValue = AddressList.format(params.to)
        header.ccField.stringValue = AddressList.format(params.cc)
        header.bccField.stringValue = AddressList.format(params.bcc)
        header.subjectField.stringValue = params.subject
        header.setCcBccVisible(cc: !params.cc.isEmpty, bcc: !params.bcc.isEmpty)
        updateTitle()
        draft.setOriginal(inReplyTo: params.inReplyTo, forwarding: params.forwarding, comment: params.comment)
        // A draft opened from the Drafts folder is the user's already: its
        // id and version make the saves updates, and closing never deletes it.
        draft.setOpened(draftID: params.draftID, version: params.version, replaces: params.replaces,
                        fromDrafts: params.kind == .edit)
        editor.load(bodyHTML: params.bodyHTML)
        setAccounts(controller.accounts, placeholder: controller.placeholder)
        // What the backend imported for the template (a quoted original's
        // pictures, a forwarded message's files): listed and shown now,
        // bound by the first save.
        setAttachments(params.attachments)

        wireRows()
        wireToolbar()
        wireDraft()
        wireAssistant()
        escape = EscapeCloser.install(on: w)
        escape?.shouldClose = { [weak self] in
            guard let self else { return true }
            return !self.suggestions.contains { $0.isVisible } && !self.formatToolbar.isLinkPopoverShown
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Layout

    /// compose.blp `content`: the card, the formatting bar, the plain-text
    /// hint, the editor, the chips and the status line, top to bottom.
    private func buildContent() -> NSView {
        let editorBox = NSBox()
        editorBox.boxType = .custom
        editorBox.titlePosition = .noTitle
        editorBox.borderWidth = 0
        editorBox.cornerRadius = 0
        editorBox.fillColor = .textBackgroundColor
        editorBox.contentViewMargins = .zero
        editorBox.translatesAutoresizingMaskIntoConstraints = false
        editorBox.setContentHuggingPriority(.defaultLow, for: .vertical)
        editorBox.setContentCompressionResistancePriority(.defaultLow, for: .vertical)
        let editorView = editor.view
        editorView.translatesAutoresizingMaskIntoConstraints = false
        if let content = editorBox.contentView {
            content.addSubview(editorView)
            NSLayoutConstraint.activate([
                editorView.topAnchor.constraint(equalTo: content.topAnchor),
                editorView.bottomAnchor.constraint(equalTo: content.bottomAnchor),
                editorView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
                editorView.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            ])
        }

        statusLabel.font = Typo.caption
        statusLabel.textColor = Tint.secondary
        statusLabel.alignment = .left
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.maximumNumberOfLines = 1
        statusLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let statusRow = Self.inset(statusLabel, top: 4, left: 12, bottom: 4, right: 12)

        let root = FillStackView()
        root.spacing = 0
        for v in [
            Self.inset(commentHeader.map { $0 as NSView } ?? header, top: 12, left: 12, bottom: 6, right: 12),
            formatToolbar, plainHintRow, editorBox, chips, statusRow,
        ] {
            root.addArrangedSubview(v)
        }
        for v in root.arrangedSubviews where v !== editorBox {
            v.setContentHuggingPriority(.required, for: .vertical)
        }

        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(root)
        NSLayoutConstraint.activate([
            root.topAnchor.constraint(equalTo: container.topAnchor),
            root.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            root.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            root.trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ])
        toasts.install(over: container)
        return container
    }

    /// A view with margins around it (the Blueprint's margin-* properties).
    static func inset(_ v: NSView, top: CGFloat, left: CGFloat, bottom: CGFloat, right: CGFloat) -> NSView {
        let c = NSView()
        c.translatesAutoresizingMaskIntoConstraints = false
        v.translatesAutoresizingMaskIntoConstraints = false
        c.addSubview(v)
        NSLayoutConstraint.activate([
            v.topAnchor.constraint(equalTo: c.topAnchor, constant: top),
            c.bottomAnchor.constraint(equalTo: v.bottomAnchor, constant: bottom),
            v.leadingAnchor.constraint(equalTo: c.leadingAnchor, constant: left),
            c.trailingAnchor.constraint(equalTo: v.trailingAnchor, constant: right),
        ])
        return c
    }

    // MARK: Wiring

    private func wireEditor() {
        editor.onState = { [weak self] st in
            guard let self else { return }
            self.editorState = st
            self.formatToolbar.applyState(st)
        }
        editor.onChanged = { [weak self] in
            guard let self else { return }
            // The `changed` a flush produces reports what is being saved;
            // only other content is an edit (see `flushEditor`).
            if let flushed = self.lastFlushedHTML, flushed == self.editor.html() {
                return
            }
            self.lastFlushedHTML = nil
            self.draft.markDirty()
        }
        editor.onReady = { [weak self] in
            guard let self, self.params.kind != .new, self.params.kind != .edit else { return }
            self.editor.focusStart()
        }
        editor.onCrashed = { [weak self] in
            guard let self, !self.draft.draft.closed else { return }
            self.toast(L10n.T("The editor crashed; your last text was restored"))
            self.editor.load(bodyHTML: self.editor.html())
        }
        editor.onDropFiles = { [weak self] urls in
            guard let self else { return }
            for url in urls where url.isFileURL {
                self.importFile(path: url.path, name: url.lastPathComponent, inline: false, then: nil)
            }
        }
        // Pasted text that looks like Markdown: the daemon renders it
        // (draft.markdown); without an answer the text goes in as it is.
        let client = state.client
        editor.onPaste = { text, answer in
            Task { @MainActor in
                answer(await markdownPaste(text, client: client))
            }
        }
    }

    /// compose.go `wireRows`.
    private func wireRows() {
        let client = state.client
        for field in header.recipientFields {
            let s = RecipientSuggestionsController(field: field, client: client) { [weak self] in
                self?.account.id ?? ComposeController.placeholderAccounts[0].id
            }
            s.onChanged = { [weak self] in
                guard let self, !self.draft.draft.closed else { return }
                self.validateRow(field)
                self.draft.markDirty()
            }
            suggestions.append(s)
        }
        header.subjectField.delegate = self
        header.onFromChanged = { [weak self] in
            guard let self else { return }
            self.chosenAccountID = self.account.id
            self.draft.markDirty()
            // Another identity means other address books: what is shown
            // was asked on behalf of the previous one.
            for s in self.suggestions {
                s.hide()
            }
        }
        header.onShowCcBcc = { [weak self] in
            self?.header.setCcBccVisible(cc: true, bcc: true)
        }
    }

    private func wireToolbar() {
        formatToolbar.exec = { [weak self] command, argument in
            self?.editor.exec(command, argument)
        }
        formatToolbar.focusEditor = { [weak self] in
            self?.focusEditor()
        }
        formatToolbar.onInsertImage = { [weak self] in
            self?.insertImage(nil)
        }
    }

    private func wireDraft() {
        draft.form = self
        let alerts = state.alerts
        draft.confirmDiscard = { [weak self] heading, body, label in
            await alerts.confirmDestructive(on: self?.window, heading: heading, body: body, confirmLabel: label)
        }
        draft.saveDraftQuestion = { [weak self] in
            switch await alerts.saveDraftQuestion(on: self?.window) {
            case .save: return .save
            case .discard: return .discard
            case .cancel: return .cancel
            }
        }
        draft.onSent = { [weak self] text in
            self?.manager.controller.onSent?(text)
        }
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

    // MARK: Rows

    /// compose.go `validateRow`: whether a recipient row is free of
    /// unparsable tokens. The row shows them itself, as red badges.
    @discardableResult
    func validateRow(_ field: RecipientTokenField) -> Bool {
        field.resolved().invalid.isEmpty
    }

    /// compose.go `updateTitle`: the subject, or "New Message"; a comment
    /// names its issue (`Jira.commentTitle`).
    func updateTitle() {
        if let c = params.comment {
            window?.title = Jira.commentTitle(c.issue.key)
            return
        }
        let s = header.subjectField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        window?.title = s.isEmpty ? L10n.T("New Message") : s
    }

    /// The subject row (`subject.ConnectChanged`).
    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard (obj.object as? NSTextField) === header.subjectField else { return }
        updateTitle()
        draft.markDirty()
    }

    /// editor.GrabFocus: the keyboard back to the page.
    func focusEditor() {
        guard let window else { return }
        let v = editor.view
        if v.acceptsFirstResponder {
            window.makeFirstResponder(v)
        } else if let inner = Self.firstResponderCandidate(in: v) {
            window.makeFirstResponder(inner)
        }
    }

    private static func firstResponderCandidate(in v: NSView) -> NSView? {
        for s in v.subviews {
            if s.acceptsFirstResponder {
                return s
            }
            if let deeper = firstResponderCandidate(in: s) {
                return deeper
            }
        }
        return nil
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
        for s in suggestions {
            s.cleanup() // a pending search must not touch the rows after this
        }
        draft.cleanup()
        manager.remove(self)
    }
}

// MARK: - ComposeForm

extension ComposeWindowController: ComposeForm {
    /// compose.go `account`: the selected identity; a comment's is the
    /// issue's account (`commentAccount`).
    var account: Account {
        if let a = commentAccount {
            return a
        }
        let i = header.selectedAccountIndex
        if i >= 0, i < accounts.count {
            return accounts[i]
        }
        return accounts.first ?? ComposeController.placeholderAccounts[0]
    }

    /// compose.go `self`.
    var selfAddress: Address {
        let a = account
        return Address(name: a.config.displayName, address: a.config.email)
    }

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) {
        var ok = true
        func parse(_ f: RecipientTokenField) -> [Address] {
            let (addresses, invalid) = f.resolved()
            if !invalid.isEmpty {
                ok = false
            }
            return addresses
        }
        let to = parse(header.toField)
        let cc = parse(header.ccField)
        let bcc = parse(header.bccField)
        return (to, cc, bcc, ok)
    }

    var subject: String { header.subjectField.stringValue }

    func editorHTML() -> String { editor.html() }

    func editorText() -> String { editor.text() }

    func flushEditor(_ done: @escaping @MainActor () -> Void) {
        editor.flush { [weak self] in
            self?.lastFlushedHTML = self?.editor.html()
            done()
        }
    }

    func setStatus(_ text: String) {
        statusLabel.stringValue = text
        statusLabel.toolTip = text.isEmpty ? nil : text
    }

    func toast(_ text: String) {
        toasts.show(text)
    }

    func setSendEnabled(_ enabled: Bool) {
        toolbarDelegate.sendButton.isEnabled = enabled
        window?.toolbar?.validateVisibleItems()
    }

    func closeWindow() {
        closing = true
        window?.close()
    }
}

// MARK: - ComposeWindowHandle

extension ComposeWindowController: ComposeWindowHandle {
    /// manager.go `FindDraft`: the same saved draft, or the same Drafts
    /// message taken over.
    func edits(_ d: Draft) -> Bool {
        if let id = d.id, draft.draft.draftID == id {
            return true
        }
        if let replaces = d.replaces, params.replaces == replaces {
            return true
        }
        return false
    }

    func present() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    /// compose.go `fromLocked`: From is fixed to `params.accountID` for a
    /// reply or a forward (a draft reopened from Drafts included).
    var fromLocked: Bool { params.inReplyTo != nil || params.forwarding != nil }

    /// compose.go `setAccounts`: fills the From row, keeping the selected
    /// identity the user picked when it is still listed; until they pick,
    /// the account the window was opened for. The row is only enabled with
    /// a choice, and never for a reply or a forward.
    func setAccounts(_ list: [Account], placeholder: Bool) {
        let selectedID = chosenAccountID ?? params.accountID
        accounts = list
        let labels = list.map { a -> String in
            var name = (a.config.displayName ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if name.isEmpty {
                name = a.config.name
            }
            // The account's own text, as plain text. GTK elides it at 30
            // characters (fromFactory's max-width-chars); the pop-up here
            // truncates to the width the row gives it (lineBreakMode).
            return formatAddress(Address(name: name, address: a.config.email))
        }
        let found = list.firstIndex { $0.id == selectedID }
        // A reply or a forward goes out from the account the original is
        // in (compose.go `fromLocked`): its quoted pictures and forwarded
        // files were copied into that account.
        header.setAccounts(labels: labels, selected: found ?? 0, enabled: list.count > 1 && !(fromLocked && found != nil))
        if placeholder, !isComment {
            setStatus(L10n.T("Using placeholder account"))
        }
    }
}

// MARK: - Actions (compose.blp `compose.*`, the Format menu)

extension ComposeWindowController: MalachiActions {
    @objc func sendMessage(_ sender: Any?) {
        draft.send()
    }

    /// Not in comment mode: no Drafts folder keeps a comment.
    @objc func saveDraft(_ sender: Any?) {
        guard !isComment else { return }
        draft.save(reason: .explicit)
    }

    @objc func discardDraft(_ sender: Any?) {
        draft.discard()
    }

    @objc func formatBold(_ sender: Any?) {
        editor.exec("bold", nil)
    }

    @objc func formatItalic(_ sender: Any?) {
        editor.exec("italic", nil)
    }

    @objc func formatUnderline(_ sender: Any?) {
        editor.exec("underline", nil)
    }

    @objc func formatParagraph(_ sender: Any?) {
        formatToolbar.setBlock("p")
    }

    @objc func formatHeading1(_ sender: Any?) {
        formatToolbar.setBlock("h1")
    }

    @objc func formatHeading2(_ sender: Any?) {
        formatToolbar.setBlock("h2")
    }

    @objc func formatHeading3(_ sender: Any?) {
        formatToolbar.setBlock("h3")
    }

    @objc func alignLeft(_ sender: Any?) {
        formatToolbar.setAlign("left")
    }

    @objc func alignCenter(_ sender: Any?) {
        formatToolbar.setAlign("center")
    }

    @objc func alignRight(_ sender: Any?) {
        formatToolbar.setAlign("right")
    }

    @objc func bulletedList(_ sender: Any?) {
        editor.exec("insertUnorderedList", nil)
    }

    @objc func numberedList(_ sender: Any?) {
        editor.exec("insertOrderedList", nil)
    }

    @objc func quoteBlock(_ sender: Any?) {
        formatToolbar.toggleQuote()
    }

    @objc func insertLink(_ sender: Any?) {
        formatToolbar.showLinkPopover()
    }

    @objc func clearFormatting(_ sender: Any?) {
        formatToolbar.clearFormatting()
    }
}

extension ComposeWindowController: NSUserInterfaceValidations {
    /// Send is off while sending; the Format items carry check marks for
    /// the formatting at the caret.
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        if isComment, !Self.commentAllows(action) {
            return false
        }
        let st = editorState
        let block: String
        switch st.block {
        case "h1", "h2", "h3", "blockquote": block = st.block
        default: block = "p"
        }
        let align = (st.align == "center" || st.align == "right") ? st.align : "left"
        var checked: Bool?
        switch action {
        case Action.sendMessage:
            return !draft.draft.sending
        case Action.formatBold: checked = st.bold
        case Action.formatItalic: checked = st.italic
        case Action.formatUnderline: checked = st.underline
        case Action.bulletedList: checked = st.ul
        case Action.numberedList: checked = st.ol
        case Action.quoteBlock: checked = block == "blockquote"
        case Action.formatParagraph: checked = block == "p"
        case Action.formatHeading1: checked = block == "h1"
        case Action.formatHeading2: checked = block == "h2"
        case Action.formatHeading3: checked = block == "h3"
        case Action.alignLeft: checked = align == "left"
        case Action.alignCenter: checked = align == "center"
        case Action.alignRight: checked = align == "right"
        default:
            break
        }
        if let checked, let menuItem = item as? NSMenuItem {
            menuItem.state = checked ? .on : .off
        }
        return true
    }
}

extension ComposeWindowController: NSToolbarItemValidation {
    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        item.action == Action.sendMessage ? !draft.draft.sending : true
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
