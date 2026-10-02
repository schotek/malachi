// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// What hosts a `ComposePane`: the compose window (`ComposeWindowController`)
/// or, inline, the board's case detail. The pane tells it of what the
/// window around it shows (the title, Send), of toasts and of its end;
/// everything else (the draft, the fields, the editor) is the pane's.
@MainActor
protocol ComposePaneHost: AnyObject {
    /// The window open panels and alerts go on (nil: the pane's own).
    var paneWindow: NSWindow? { get }
    /// `ComposePane.titleText` changed (the subject was edited).
    func paneTitleChanged(_ pane: ComposePane)
    /// The draft controller enabled or disabled Send (`ComposeForm.setSendEnabled`).
    func paneSendEnabledChanged(_ pane: ComposePane, _ enabled: Bool)
    /// A short message for the user (`ComposeForm.toast`).
    func paneToast(_ text: String)
    /// The draft controller decided the form goes (`ComposeForm.closeWindow`),
    /// or, for the board, that the draft was deleted elsewhere (`.lost`).
    func paneDidEnd(_ pane: ComposePane, _ end: ComposePane.End)
    /// Inline layout: the editor took another height.
    func paneHeightChanged(_ pane: ComposePane)
}

/// The content of a compose window (compose.blp, ui/internal/compose/compose.go):
/// the card of header fields, the formatting bar, the editor, the
/// attachment chips and the status line, the draft behind them
/// (`ComposeDraftController` over `ComposeForm`) and the compose and Format
/// actions of the menu bar, which reach the pane through the responder
/// chain (`MalachiActions`). Every string shown from mail data is plain
/// text.
///
/// Two layouts (`Options.Layout`): `.window` is the compose window's
/// content as it always was (the window keeps its toolbar, its title, the
/// close question, Escape and the assistant's rewrite, and forwards the
/// actions to the pane when it is first responder itself); `.inline` is a
/// reply edited in place in the board's case detail: no From row (the
/// reply is from-locked; the detail shows the account), the editor in its
/// sized mode (it takes the height of its document between a minimum and a
/// cap, then scrolls inside), and a footer row of its own with Attach, the
/// status line, Discard and Send. The inline Send has no key equivalent: a
/// button's ⌘↩ would fire from anywhere in the main window; ⌘↩ reaches the
/// pane through the menu's Send only while the keyboard is inside it.
@MainActor
final class ComposePane: NSViewController, NSTextFieldDelegate {
    struct Options {
        enum Layout {
            /// The compose window's content.
            case window
            /// Inline in the board's case detail: sized editor, footer row,
            /// no From row.
            case inline
        }

        var layout: Layout
        /// Who keeps the draft (`ComposeDraftController.owner`).
        var owner: DraftOwner

        init(layout: Layout = .window, owner: DraftOwner = .window) {
            self.layout = layout
            self.owner = owner
        }
    }

    /// How the pane ended (`ComposePaneHost.paneDidEnd`).
    enum End: Equatable {
        /// The message (or comment) was queued; the text is the
        /// confirmation ("Message queued for sending").
        case sent(String)
        /// The user discarded it.
        case discarded
        /// It closed otherwise (nothing at stake, or after the close
        /// question).
        case closed
        /// `.board`: the draft was deleted elsewhere (`ComposeDraftController.onLost`).
        case lost
    }

    let state: AppState
    /// The compose manager's controller: the account list (`accounts`,
    /// `placeholder`, `knownAccounts`) and, for a window, `onSent`.
    let composer: ComposeController
    let params: ComposeParams
    let editor: any EditorView
    let options: Options
    let draft: ComposeDraftController
    /// The toast overlay of the window layout (installed over the content);
    /// inline toasts go to the host.
    let toasts = ToastPresenter()
    weak var host: (any ComposePaneHost)?

    let header: ComposeHeaderView
    /// The issue and its visibility in place of `header` in comment mode
    /// (ComposePane+Comment.swift); nil for an e-mail.
    private(set) lazy var commentHeader: CommentHeaderView? = params.comment.map { CommentHeaderView(comment: $0) }
    let formatToolbar = FormatToolbar()
    let chips = AttachmentChipsView()
    let statusLabel = NSTextField(labelWithString: "")
    private let plainHint = NSTextField(labelWithString: L10n.T("This message will be sent as plain text."))
    private let plainHintRow: NSView

    /// Inline layout: the footer's buttons.
    private(set) var attachButton: NSButton?
    private(set) var discardButton: NSButton?
    private(set) var sendButton: NSButton?
    /// Inline layout: the editor's height (sized mode), and the document
    /// height the editor last reported (nil until it did).
    private var editorHeight: NSLayoutConstraint?
    private(set) var contentHeight: CGFloat?

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
    /// The confirmation the draft controller gave with a send, for
    /// `End.sent`.
    private var sentText: String?
    /// The pane ended (`paneDidEnd` was called once).
    private(set) var ended = false
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "compose")

    /// - Parameters:
    ///   - state: the application state (client, settings, alerts).
    ///   - accounts: the compose manager's controller (the From list, the
    ///     placeholder, every known account for a comment).
    ///   - params: what the pane is prefilled with.
    ///   - editor: the formatted-text editor to put into the editor slot
    ///     (`ComposeEditorView(sized: true)` for the inline layout).
    ///   - options: the layout and who keeps the draft.
    init(state: AppState, accounts controller: ComposeController, params: ComposeParams, editor: any EditorView,
         options: Options = Options())
    {
        self.state = state
        composer = controller
        self.params = params
        self.editor = editor
        self.options = options
        header = ComposeHeaderView(showsFrom: options.layout == .window)
        draft = ComposeDraftController(client: state.client, settings: state.settings, placeholder: { [weak controller] in
            controller?.placeholder ?? true
        }, owner: options.owner)

        plainHint.font = Typo.caption
        plainHint.textColor = Tint.secondary
        plainHint.alignment = .left
        plainHintRow = Self.inset(plainHint, top: 4, left: 12, bottom: 4, right: 12)
        plainHintRow.isHidden = composeRichText
        super.init(nibName: nil, bundle: nil)

        view = options.layout == .inline ? buildInlineContent() : buildContent()
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
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The view that should have the keyboard first: To, or the editor of
    /// a comment (the window's `initialFirstResponder`).
    var initialResponder: NSView {
        isComment ? editor.view : header.toField.editor
    }

    /// Escape may close the window: no recipient suggestions and no link
    /// popover are showing (they take Escape themselves).
    var popupsHidden: Bool {
        !suggestions.contains { $0.isVisible } && !formatToolbar.isLinkPopoverShown
    }

    /// The draft controller's finishing step (`.board`): flush, save when
    /// dirty, clean up; true when nothing typed was lost. On false the pane
    /// stays usable (its autosave armed), so the host may keep it and call
    /// again. The host bounds the wait.
    func finish() async -> Bool {
        let ok = await draft.finish()
        if draft.draft.closed {
            tearDown()
        }
        return ok
    }

    /// The window really closes (or the host drops the pane): pending
    /// suggestion searches must not touch the rows after this, and the
    /// draft is cleaned up.
    func cleanup() {
        tearDown()
        draft.cleanup()
    }

    private func tearDown() {
        for s in suggestions {
            s.cleanup() // a pending search must not touch the rows after this
        }
    }

    // MARK: Layout

    /// compose.blp `content`: the card, the formatting bar, the plain-text
    /// hint, the editor, the chips and the status line, top to bottom.
    private func buildContent() -> NSView {
        let editorBox = makeEditorBox()
        editorBox.setContentHuggingPriority(.defaultLow, for: .vertical)
        editorBox.setContentCompressionResistancePriority(.defaultLow, for: .vertical)
        configureStatusLabel()
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

    /// The inline layout: the header without From (or the comment's), the
    /// formatting bar, the plain-text hint, the sized editor, the chips and
    /// the footer row `[Attach] status … [Discard] [Send]`, top to bottom,
    /// as tall as its content (the host scrolls it).
    private func buildInlineContent() -> NSView {
        let editorBox = makeEditorBox()
        let height = editorBox.heightAnchor.constraint(equalToConstant: EditorHeight.minimum)
        height.isActive = true
        editorHeight = height
        configureStatusLabel()

        let root = FillStackView()
        root.spacing = 0
        for v in [
            Self.inset(commentHeader.map { $0 as NSView } ?? header, top: 0, left: 0, bottom: 6, right: 0),
            formatToolbar, plainHintRow, editorBox, chips, footerRow(),
        ] {
            root.addArrangedSubview(v)
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
        return container
    }

    /// The footer of the inline layout. Send is the suggested action but
    /// has no key equivalent (`ComposePane` comment); Attach is not there
    /// in comment mode (`applyCommentMode`).
    private func footerRow() -> NSView {
        let attach = NSButton(image: Icon.image("mail-attachment", size: .toolbar), target: self, action: Action.attachFiles)
        attach.bezelStyle = .rounded
        attach.toolTip = L10n.T("Attach Files")
        attach.setAccessibilityLabel(L10n.T("Attach Files"))
        attach.setContentHuggingPriority(.required, for: .horizontal)
        attachButton = attach
        let discard = NSButton(title: mn(L10n.T("_Discard")), target: self, action: Action.discardDraft)
        discard.bezelStyle = .rounded
        discard.setAccessibilityLabel(mn(L10n.T("_Discard")))
        discard.setContentHuggingPriority(.required, for: .horizontal)
        discardButton = discard
        let send = NSButton(title: mn(L10n.T("_Send")), image: Icon.symbol("paperplane.fill", size: .toolbar),
                            target: self, action: Action.sendMessage)
        send.bezelStyle = .rounded
        send.imagePosition = .imageLeading
        send.imageScaling = .scaleProportionallyDown
        send.bezelColor = .controlAccentColor
        send.keyEquivalent = ""
        send.setAccessibilityLabel(mn(L10n.T("_Send")))
        send.setContentHuggingPriority(.required, for: .horizontal)
        sendButton = send
        statusLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let row = NSStackView(views: [attach, statusLabel, discard, send])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.distribution = .fill
        row.spacing = 8
        row.edgeInsets = NSEdgeInsets(top: 8, left: 0, bottom: 0, right: 0)
        row.translatesAutoresizingMaskIntoConstraints = false
        return row
    }

    /// The editor in its slot, on the text background.
    private func makeEditorBox() -> NSBox {
        let editorBox = NSBox()
        editorBox.boxType = .custom
        editorBox.titlePosition = .noTitle
        editorBox.borderWidth = 0
        editorBox.cornerRadius = 0
        editorBox.fillColor = .textBackgroundColor
        editorBox.contentViewMargins = .zero
        editorBox.translatesAutoresizingMaskIntoConstraints = false
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
        return editorBox
    }

    private func configureStatusLabel() {
        statusLabel.font = Typo.caption
        statusLabel.textColor = Tint.secondary
        statusLabel.alignment = .left
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.maximumNumberOfLines = 1
        statusLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
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

    // MARK: Inline height

    /// The current height of the inline editor (0 in the window layout).
    var editorFrameHeight: CGFloat { editorHeight?.constant ?? 0 }

    /// The height the cap is taken from: the visible height of the scroll
    /// view the pane sits in, else the window's content.
    private var visibleHeight: CGFloat {
        if let scroll = view.enclosingScrollView {
            return scroll.contentView.bounds.height
        }
        return view.window?.contentLayoutRect.height ?? 0
    }

    /// Sized editor: the document's height, clamped to the inline rule.
    private func editorReported(_ h: CGFloat) {
        contentHeight = h
        applyEditorHeight()
    }

    private func applyEditorHeight() {
        guard let editorHeight else { return }
        let h = EditorHeight(content: contentHeight ?? 0, visible: visibleHeight).height
        guard abs(editorHeight.constant - h) >= 0.5 else { return }
        editorHeight.constant = h
        host?.paneHeightChanged(self)
    }

    /// A new visible height moves the cap.
    override func viewDidLayout() {
        super.viewDidLayout()
        applyEditorHeight()
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
        if options.layout == .inline {
            editor.onHeight = { [weak self] h in
                self?.editorReported(h)
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
            await alerts.confirmDestructive(on: self?.dialogWindow, heading: heading, body: body, confirmLabel: label)
        }
        draft.saveDraftQuestion = { [weak self] in
            switch await alerts.saveDraftQuestion(on: self?.dialogWindow) {
            case .save: return .save
            case .discard: return .discard
            case .cancel: return .cancel
            }
        }
        draft.onSent = { [weak self] text in
            guard let self else { return }
            self.sentText = text
            // The window's confirmation goes to the main window through the
            // manager, as it always did; an inline host shows its own.
            if self.options.layout == .window {
                self.composer.onSent?(text)
            }
        }
        draft.onLost = { [weak self] in
            self?.end(.lost)
        }
    }

    /// The window sheets and alerts go on.
    var dialogWindow: NSWindow? {
        host?.paneWindow ?? view.window
    }

    private func end(_ e: End) {
        guard !ended else { return }
        ended = true
        host?.paneDidEnd(self, e)
    }

    // MARK: Rows

    /// compose.go `validateRow`: whether a recipient row is free of
    /// unparsable tokens. The row shows them itself, as red badges.
    @discardableResult
    func validateRow(_ field: RecipientTokenField) -> Bool {
        field.resolved().invalid.isEmpty
    }

    /// The window's title: the subject, or "New Message"; a comment
    /// names its issue (`Jira.commentTitle`).
    private(set) var titleText = ""

    /// compose.go `updateTitle`.
    func updateTitle() {
        if let c = params.comment {
            titleText = Jira.commentTitle(c.issue.key)
        } else {
            let s = header.subjectField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            titleText = s.isEmpty ? L10n.T("New Message") : s
        }
        // A window binds its title to its content view controller's.
        title = titleText
        host?.paneTitleChanged(self)
    }

    /// The subject row (`subject.ConnectChanged`).
    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard (obj.object as? NSTextField) === header.subjectField else { return }
        updateTitle()
        draft.markDirty()
    }

    /// editor.GrabFocus: the keyboard back to the page.
    func focusEditor() {
        guard let window = view.window else { return }
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
}

// MARK: - ComposeForm

extension ComposePane: ComposeForm {
    /// compose.go `account`: the selected identity; a comment's is the
    /// issue's account (`commentAccount`).
    var account: Account {
        if let a = commentAccount {
            return a
        }
        // Inline (the board's draft): there is no From row, so the account
        // is the draft's own, never the row's placeholder or first entry.
        if options.layout == .inline, let id = params.accountID {
            return accounts.first { $0.id == id } ?? composer.knownAccounts.first { $0.id == id }
                ?? Account(id: id, config: AccountConfig(name: "", email: ""), enabled: true,
                           state: SyncState(accountId: id, status: .idle))
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

    /// A toast in the host (the window's overlay, the board's page).
    func toast(_ text: String) {
        if let host {
            host.paneToast(text)
        } else {
            toasts.show(text)
        }
    }

    func setSendEnabled(_ enabled: Bool) {
        sendButton?.isEnabled = enabled
        host?.paneSendEnabledChanged(self, enabled)
    }

    /// The draft controller decided the form goes: sent, discarded, or
    /// closed.
    func closeWindow() {
        if let sentText {
            end(.sent(sentText))
        } else if draft.draft.discard {
            end(.discarded)
        } else {
            end(.closed)
        }
    }
}

// MARK: - ComposeWindowHandle

extension ComposePane: ComposeWindowHandle {
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

extension ComposePane: MalachiActions {
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

extension ComposePane: NSUserInterfaceValidations {
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

extension ComposePane: NSToolbarItemValidation {
    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        item.action == Action.sendMessage ? !draft.draft.sending : true
    }
}
