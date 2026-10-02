// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os
import Quartz

/// The main three-pane window (window.blp, window/window.go): the split
/// view with the status bar under it (`MainContentViewController`), the
/// unified toolbar, the per-message actions of the menu bar and toolbar,
/// and the toast overlay. The panes' content is installed by the sidebar,
/// list and reader parts (`install(sidebar:)`, `install(list:)`,
/// `install(message:)`), the status bar by the app (`install(statusBar:)`),
/// and so are the message actions (`messageActions`) and the assistant
/// panel (`install(assistant:)`), which exists while
/// `AssistantController.panelShown` (the Assistant shown, In App chosen):
/// the window follows that state with the split view's inspector and the
/// toolbar's inspector section. While `AssistantController.canRunInApp`
/// the search field also offers "Search in Your Own Words" (its
/// magnifier's menu, ⌥↩): the typed words go to the user's Claude Code
/// (`SearchConversion`), and the query it answers replaces them and is
/// searched as if typed and Return pressed.
/// The window is one instance for the application's life: with "Run in
/// Background" it hides instead of closing.
/// It has two modes (MalachiCore `Board`, MainWindowController+Mode): Mail,
/// all of the above, and Board, a page in place of the panes with toolbars
/// of its own; the status bar stays in both. The window starts in Mail.
@MainActor
final class MainWindowController: NSWindowController, NSWindowDelegate {
    static let defaultSize = NSSize(width: 1200, height: 760)
    static let minimumSize = NSSize(width: 360, height: 294)
    static let frameAutosaveName = "Main"

    let state: AppState
    let split: MainSplitViewController
    /// The window's content: the split view over the status bar.
    let content: MainContentViewController
    /// The toast overlay of the message pane (window.blp `toast_overlay`).
    let toasts: ToastPresenter

    /// The selection-based actions; nil (everything disabled) until the
    /// app installs them.
    var messageActions: (any MessageActions)?

    /// The window title: the selected folder's name (D1 of the plan).
    var folderTitle: String {
        didSet { applyTitle() }
    }

    /// The window subtitle: the selected folder's unread and total counts
    /// (window.go `refreshListTitle`, `folderCountsText`; the subtitle of
    /// the list's Adw.WindowTitle in GTK), empty for none, as in Mail.
    var folderSubtitle: String {
        didSet { applyTitle() }
    }

    /// What the window shows; set only by setMode(_:).
    var mode = Board.initialMode
    /// The toolbar of the Mail mode. Held here: `MainToolbar` holds it
    /// weakly, and while the board's toolbar is the window's nothing else
    /// would. Its dynamic items and validation go to it in either mode.
    let mailToolbar: NSToolbar
    /// The board's source: the daemon's (`DaemonBoardSource` over the
    /// application's client), made on first use and started on the first
    /// entry into Board (`startBoard`, MainWindowController+Board): nothing
    /// outside the board shows its data, so a user who never opens it never
    /// lists it. Then it runs until the application quits, fed by the
    /// notification fan-out (`boardTokens`). Nil with `MALACHI_BOARD_SAMPLES`.
    private(set) lazy var boardSource: DaemonBoardSource? =
        Self.boardSamples ? nil : DaemonBoardSource(client: state.client)
    /// Whether `boardSource` was started (notifications reach it only then).
    var boardStarted = false
    /// DEVELOPMENT AID: `MALACHI_BOARD_SAMPLES=1` shows the invented sample
    /// cases (`InMemoryBoardSource.dummy`) instead of the daemon's board,
    /// to look at the three styles without mail; read once.
    static let boardSamples = ProcessInfo.processInfo.environment["MALACHI_BOARD_SAMPLES"] == "1"
    /// The board (MalachiCore `BoardController`), made on first use.
    lazy var board = BoardController(source: boardSource ?? InMemoryBoardSource.dummy(samples: true))
    /// What the user can do with a case (the toolbar, the panel, the
    /// menus); Reply, Open Draft and Show in Mail go to `boardMail`.
    lazy var boardActions: BoardActions = makeBoardActions()
    /// What the board's actions need of the mail; the application installs
    /// it (Integration+Board).
    var boardMail = BoardMail()
    /// The board source's subscriptions to the notification fan-out.
    var boardTokens: [NotificationHub.Token] = []
    /// The application's triage observed (MainWindowController+Triage);
    /// the window lives as long as the application.
    var triageTokens: [BoardObserverToken] = []
    /// The run this window saw active last, for the toast of a manual
    /// run's end.
    var triageSeenActive: Board.TriageTrigger?
    /// The toolbars of the Board mode (`BoardToolbar`: the List's, and the
    /// one of Columns and Today), made on first use.
    lazy var boardToolbars: BoardToolbar = {
        let d = BoardToolbar(controller: board) { [weak self] in
            self?.boardPage.listSplitView
        }
        // Triage follows the application's triage (MainWindowController+Triage).
        d.triageItem = { [weak self] in
            self?.triageToolbarItem ?? .hidden
        }
        boardToolbarsMade = true
        return d
    }()
    /// Whether `boardToolbars` exists (its switches are counted then).
    private(set) var boardToolbarsMade = false
    /// The Board mode's page, made on first use; the window follows its
    /// changes (MainWindowController+Board).
    lazy var boardPage: BoardPageViewController = {
        let page = BoardPageViewController(actions: boardActions)
        page.onChange = { [weak self] _ in
            self?.boardDidChange()
        }
        return page
    }()
    /// What had the keyboard in the board when the window went back to
    /// Mail, given it back on the next entry.
    weak var boardResponder: NSResponder?
    /// What had the keyboard in Mail when the board took over (a search
    /// field rather than its field editor), given it back on the way back.
    weak var savedResponder: NSResponder?
    /// Gives the keyboard to the mail when what had it is gone (the list);
    /// the app installs it.
    var mailFocusFallback: (@MainActor () -> Void)?

    private let toolbarDelegate: MainToolbar
    /// The toolbar's Assistant menu (ui/internal/assistant), with Summarize
    /// Unread in This Folder.
    private let assistantMenu: AssistantMenu
    private var assistantToken: AssistantController.Token?
    private var assistantTargetToken: Settings.ChangeToken?
    /// The assistant panel's view, for the Assistant button that opens it.
    private weak var assistantPanel: AssistantPanelViewController?
    /// The search in the user's own words, made on first use.
    private var conversion: SearchConversion?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "window")

    init(state: AppState) {
        self.state = state
        folderTitle = ""
        folderSubtitle = ""
        split = MainSplitViewController()
        content = MainContentViewController(split: split)
        toasts = ToastPresenter()

        // fullSizeContentView is what lets the sidebar run the full height
        // under the unified toolbar (its section then holds only its own
        // items, and the window title moves to the list section as in
        // Mail); the split view controller insets the other panes itself.
        let w = NSWindow(
            contentRect: NSRect(origin: .zero, size: Self.defaultSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        w.title = MainMenu.appName
        w.minSize = Self.minimumSize
        w.toolbarStyle = .unified
        w.tabbingMode = .disallowed
        // The controller owns the window for the application's life; a
        // hidden window ("Run in Background") must not be released.
        w.isReleasedWhenClosed = false
        // Setting contentViewController sizes the window to the view's
        // frame, so the default size goes on the view first (not on
        // preferredContentSize, which would override a restored frame).
        content.view.setFrameSize(Self.defaultSize)
        w.contentViewController = content
        // The toolbar's tracking separators need the split view, so the
        // toolbar comes after the content (the plan's ordering).
        assistantMenu = AssistantMenu(state: state, includesUnread: true)
        toolbarDelegate = MainToolbar(
            splitView: split.splitView, assistantMenu: assistantMenu, showsAssistant: state.assistant.shown,
            showsPanel: state.assistant.panelShown)
        mailToolbar = toolbarDelegate.makeToolbar()
        super.init(window: w)
        w.delegate = self
        w.toolbar = mailToolbar
        // The GTK sizes are window sizes, header bars included: the frame
        // is set once the toolbar is part of it. With Auto Layout content
        // the window's minimum comes from the content's constraints, so the
        // GTK minimum is stated there; a restored frame cannot go below it.
        // The status bar is part of the content, as the status line is part
        // of the GTK window: the window keeps the GTK minimum, and the panes
        // above the bar keep at least 200 pt.
        w.setFrame(NSRect(origin: .zero, size: Self.defaultSize), display: false)
        let chrome = w.frame.height - w.contentRect(forFrameRect: w.frame).height
        NSLayoutConstraint.activate([
            content.view.widthAnchor.constraint(greaterThanOrEqualToConstant: Self.minimumSize.width),
            content.view.heightAnchor.constraint(
                greaterThanOrEqualToConstant: max(Self.minimumSize.height - chrome, 200 + StatusBarViewController.height)),
        ])
        w.center()
        split.onListCollapseChanged = { [weak self] collapsed in
            self?.setListSeparator(visible: !collapsed)
        }
        setListSeparator(visible: !split.isListCollapsed)
        // The Assistant button follows `AssistantController.shown`: the
        // `assistant-menu` setting while the bridge is registered (the
        // window lives as long as the application).
        // The assistant panel follows `panelShown`: the same, and the
        // `assistant-target` preference.
        let assistant = state.assistant
        split.assistantAllowed = assistant.panelShown
        assistantToken = assistant.onChange { [weak self] in
            guard let self else { return }
            self.toolbarDelegate.setAssistant(visible: assistant.shown, in: self.mailToolbar)
            self.updateAssistantPanel()
        }
        assistantTargetToken = state.settings.onChange(.assistantTarget) { [weak self] in
            self?.updateAssistantPanel()
        }
        toolbarDelegate.onSearchOwnWords = { [weak self] words in
            self?.searchInOwnWords(words)
        }
        toolbarDelegate.onOpenAssistantPanel = { [weak self] in
            self?.openAssistantPanel()
        }
        toolbarDelegate.setAssistant(
            opensPanel: Assistant.buttonOpensPanel(state.settings.assistantTarget, hasPanel: true), in: mailToolbar)
        toolbarDelegate.setOwnWords(available: assistant.canRunInApp)
        w.setFrameAutosaveName(Self.frameAutosaveName)

        split.listContainer.install(StatusPageViewController(
            illustration: .icon("folder-symbolic"),
            title: L10n.T("Select a folder"),
            description: L10n.T("Choose a folder in the sidebar to see its messages.")))
        split.messageContainer.install(StatusPageViewController(
            illustration: .icon("mail-unread-symbolic"),
            title: L10n.T("No Message Selected"),
            description: L10n.T("Select a message to read it here.")))
        split.messageContainer.setOverlay(toasts)
        state.toasts.presenter = toasts
        state.toasts.fallback = toasts
        wireBoardSource()
        wireTriage()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Pane slots

    func install(sidebar vc: NSViewController) {
        split.sidebarContainer.install(vc)
    }

    func install(list vc: NSViewController) {
        split.listContainer.install(vc)
    }

    func install(message vc: NSViewController) {
        split.messageContainer.install(vc)
    }

    func install(statusBar vc: NSViewController) {
        content.install(statusBar: vc)
    }

    /// The assistant panel's view (the split view's inspector).
    func install(assistant vc: NSViewController) {
        split.assistantContainer.install(vc)
        assistantPanel = vc as? AssistantPanelViewController
    }

    /// Opens the assistant panel, while it exists; the board gives way to
    /// the mail first.
    func revealAssistant() {
        setMode(Board.mode(for: .revealAssistant, current: mode))
        split.revealAssistant()
    }

    /// The Assistant button with In App chosen (`Assistant.buttonOpensPanel`):
    /// the panel unfolds and its question field, under the quick actions,
    /// takes the keyboard; an open panel stays open and only gets the
    /// keyboard (the inspector toggle folds it). The state is asked for
    /// again, as the Assistant menu asks when it opens.
    private func openAssistantPanel() {
        state.assistant.refresh()
        revealAssistant()
        assistantPanel?.focusInput()
    }

    /// The panel and its toolbar toggle exist while the Assistant is shown
    /// and In App is chosen; otherwise the panel folds.
    private func updateAssistantPanel() {
        let allowed = state.assistant.panelShown
        split.assistantAllowed = allowed
        toolbarDelegate.setAssistantPanel(visible: allowed, in: mailToolbar)
        toolbarDelegate.setAssistant(
            opensPanel: Assistant.buttonOpensPanel(state.settings.assistantTarget, hasPanel: true), in: mailToolbar)
        let ownWords = state.assistant.canRunInApp
        toolbarDelegate.setOwnWords(available: ownWords)
        if !ownWords, toolbarDelegate.converting {
            conversion?.cancel()
            toolbarDelegate.endConverting(text: nil)
        }
    }

    // MARK: Search in your own words

    /// "Search in Your Own Words" with the field's `words`: the field shows
    /// that it converts, then the query replaces the words and is searched
    /// for (the list unfolds when a narrow window folded it, as the status
    /// bar's Outbox does); a failure is a toast and the words stay. The
    /// first request ever asks for consent on this window.
    private func searchInOwnWords(_ words: String) {
        guard state.assistant.canRunInApp, !toolbarDelegate.converting else { return }
        let conversion = searchConversion()
        toolbarDelegate.beginConverting()
        let started = conversion.convert(words) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .query(let query):
                self.toolbarDelegate.endConverting(text: query)
                // Not in Board: the list is hidden there.
                if self.split.isListCollapsed, self.mode != .board {
                    self.split.toggleMessageList(nil)
                }
                self.onSearchReturn?(query)
            case .failed(let text):
                self.toolbarDelegate.endConverting(text: nil)
                self.toasts.show(text)
            case .declined:
                self.toolbarDelegate.endConverting(text: nil)
            }
        }
        if !started {
            toolbarDelegate.endConverting(text: nil)
        }
    }

    /// Ends a search in the user's own words under way (the application
    /// quits); the typed words go back into the field.
    func cancelSearchInOwnWords() {
        conversion?.cancel()
        toolbarDelegate.endConverting(text: nil)
    }

    private func searchConversion() -> SearchConversion {
        if let conversion {
            return conversion
        }
        let request = AssistantRequest(settings: state.settings, locator: state.claudeCode)
        let alerts = state.alerts
        request.consent = { [weak self] in
            let t = Assistant.panelTexts()
            return await alerts.confirm(
                on: self?.window, heading: t.consentHeading, body: t.consentBody, confirmLabel: t.allow,
                declineLabel: t.cancel)
        }
        let c = SearchConversion(request: request)
        conversion = c
        return c
    }

    /// The Mail/Board switches of both toolbars, the ones made so far
    /// (MainWindowController+Mode keeps their selection in step).
    var modeSwitches: [NSToolbarItemGroup] {
        [toolbarDelegate.modeItem].compactMap { $0 } + (boardToolbarsMade ? boardToolbars.modeItems : [])
    }

    private func setListSeparator(visible: Bool) {
        toolbarDelegate.setListSeparator(visible: visible, in: mailToolbar)
    }

    /// Presents the window (a hidden one comes back) and activates the app.
    func showMainWindow() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    // MARK: NSWindowDelegate

    /// "Run in Background": closing hides the window; the application and
    /// the daemon live on until Quit (window.go `ConnectCloseRequest`).
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard state.settings.runInBackground else { return true }
        sender.orderOut(nil)
        return false
    }

    /// A resize by the user (or a tile, a zoom) to `frameSize`: the
    /// board's List folds its panes for the new width first.
    func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
        if mode == .board {
            boardPage.shownList?.willResize(toWidth: frameSize.width)
        }
        return frameSize
    }

    func windowDidBecomeKey(_ notification: Foundation.Notification) {
        state.toasts.presenter = toasts
        onBecomeKey?()
    }

    // MARK: Actions

    /// The split view's actions (⌃⌘S, ⌥⌘L, the assistant panel's toggle)
    /// also while no view of the window has the keyboard focus: the
    /// responder chain then starts at the window and reaches this
    /// controller, not the split view controller. None while the board
    /// shows.
    override func supplementalTarget(forAction action: Selector, sender: Any?) -> Any? {
        if let target = content.splitTarget(forAction: action) {
            return target
        }
        return super.supplementalTarget(forAction: action, sender: sender)
    }

    // MARK: Quick Look

    // An attachment chip's preview (AttachmentPreview): the panel asks the
    // key window's responder chain for a controller, and a chip never
    // becomes first responder, so this controller answers for it.
    nonisolated override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool {
        MainActor.assumeIsolated { AttachmentPreview.shared.accepts }
    }

    nonisolated override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        MainActor.assumeIsolated { AttachmentPreview.shared.begin(panel) }
    }

    nonisolated override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {
        MainActor.assumeIsolated { AttachmentPreview.shared.end(panel) }
    }

    /// True while an editable text view (a field editor) has the keyboard:
    /// the bare-letter menu items must not fire then. The read-only body
    /// does not consume plain keys, as GTK's selectable labels do not.
    private var isTyping: Bool {
        (window?.firstResponder as? NSTextView)?.isEditable == true
    }

    private var flags: ActionFlags {
        messageActions?.flags ?? .none
    }

    @objc func checkForNewMail(_ sender: Any?) {
        state.hooks.checkForNewMail?()
    }

    /// The window became key: the user looks at the selected folder, whose
    /// desktop notifications the app withdraws (window.go, `is-active`).
    var onBecomeKey: (@MainActor () -> Void)?

    /// The toolbar search field's text (after the typing pause; "" when
    /// cleared) and Return in it; the app hands both to the list.
    var onSearchText: (@MainActor (String) -> Void)? {
        get { toolbarDelegate.onSearchText }
        set { toolbarDelegate.onSearchText = newValue }
    }

    var onSearchReturn: (@MainActor (String) -> Void)? {
        get { toolbarDelegate.onSearchReturn }
        set { toolbarDelegate.onSearchReturn = newValue }
    }

    /// Edit → Find… (⌘F; window.go `win.search`): the search field takes
    /// the keyboard.
    @objc func findMessages(_ sender: Any?) {
        toolbarDelegate.focusSearch()
    }

    /// A filter from the toolbar's menu or the View menu (the item's tag).
    @objc func setMessageFilter(_ sender: Any?) {
        guard let tag = (sender as? NSMenuItem)?.tag else { return }
        let f = FilterMenu.filter(tag: tag)
        state.hooks.setMessageFilter?(f)
        toolbarDelegate.setFilter(active: f != .all)
    }

    @objc func reply(_ sender: Any?) {
        messageActions?.reply()
    }

    @objc func replyAll(_ sender: Any?) {
        messageActions?.replyAll()
    }

    @objc func forward(_ sender: Any?) {
        messageActions?.forward()
    }

    @objc func markAsRead(_ sender: Any?) {
        messageActions?.markRead()
    }

    @objc func markAsUnread(_ sender: Any?) {
        messageActions?.markUnread()
    }

    @objc func toggleFlag(_ sender: Any?) {
        messageActions?.toggleFlag()
    }

    @objc func archive(_ sender: Any?) {
        messageActions?.archive()
    }

    @objc func markAsJunk(_ sender: Any?) {
        messageActions?.junk()
    }

    @objc func moveToTrash(_ sender: Any?) {
        messageActions?.trash()
    }

    @objc func loadImages(_ sender: Any?) {
        messageActions?.loadImages()
    }

    @objc func trustSender(_ sender: Any?) {
        messageActions?.trustSender()
    }

    /// The Assistant menu's message actions (the item's tag) on the
    /// selection.
    @objc func askAssistant(_ sender: Any?) {
        guard let a = AssistantMenu.action(tag: (sender as? NSMenuItem)?.tag) else { return }
        messageActions?.askAssistant(a)
    }

    /// Summarize Unread in This Folder, the main window's only.
    @objc func summarizeUnread(_ sender: Any?) {
        messageActions?.summarizeUnread()
    }

    // MARK: Validation

    /// Whether `action` is allowed for the selection (actions.go
    /// `setMessageActionsSensitive`); nil for an action not handled here.
    /// The mode comes first: in Board the hidden list keeps its selection,
    /// and nothing may act on it (`Board.allows`).
    private func allows(_ action: Selector, _ f: ActionFlags) -> Bool? {
        if let c = Self.command(action), !Board.allows(c, in: mode) {
            return false
        }
        switch action {
        case Action.checkForNewMail: return state.hooks.checkForNewMail != nil
        // The filter narrows a folder's listing; search results have none
        // (GTK hides the toggle group while searching).
        case Action.setMessageFilter: return state.hooks.setMessageFilter != nil && !(state.hooks.searchActive?() ?? false)
        case Action.reply: return f.reply
        case Action.replyAll: return f.replyAll
        case Action.forward: return f.forward
        case Action.markAsRead: return f.markRead
        case Action.markAsUnread: return f.markUnread
        case Action.toggleFlag: return f.star
        case Action.archive: return f.archive
        case Action.markAsJunk: return f.junk
        case Action.moveToTrash: return f.trash
        case Action.loadImages: return f.loadImages
        case Action.trustSender: return f.trustSender
        // A message not in the Outbox, and the chosen Claude app can read
        // the mail (ui/internal/assistant `Pick`, no fallback).
        case Action.askAssistant: return f.on && !f.outbox && state.assistant.pick(needsBridge: true).ok
        case Action.summarizeUnread:
            return (messageActions?.canSummarizeUnread ?? false) && state.assistant.pick(needsBridge: true).ok
        // The List's detail actions: on the selected case.
        case Action.boardDone, Action.boardRemind: return boardHasDetail
        case Action.boardArchive: return boardHasDetail && board.view.detail.map { boardActions.canArchive($0.id) } == true
        case Action.boardReply: return boardHasDetail && board.view.detail.map { boardActions.canReply($0.id) } == true
        case Action.boardTriage: return triageClickable
        default: return nil
        }
    }

    /// The trash label: Cancel Sending for an outbox message (outbox.go
    /// `trashTooltip`).
    private func trashTitle(_ f: ActionFlags) -> String {
        f.outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash")
    }

    private func starTitle(_ f: ActionFlags) -> String {
        f.flagged ? L10n.T("Unstar") : L10n.T("Star")
    }
}

extension MainWindowController: NSUserInterfaceValidations {
    func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        guard let action = item.action else { return false }
        let f = flags
        if let menuItem = item as? NSMenuItem {
            switch action {
            case Action.toggleFlag: menuItem.title = starTitle(f)
            case Action.moveToTrash: menuItem.title = trashTitle(f)
            case Action.setMessageFilter:
                let current = state.hooks.messageFilter?() ?? .all
                menuItem.state = FilterMenu.tag(current) == menuItem.tag ? .on : .off
            case Action.setWindowMode:
                menuItem.state = mode.rawValue == menuItem.tag ? .on : .off
            case Action.setBoardStyle:
                validateBoardStyle(menuItem)
            default: break
            }
            ActionPresentation.present(menuItem, action, f)
            if Action.bareKeyActions.contains(action), isTyping {
                return false
            }
        }
        return allows(action, f) ?? true
    }
}

extension MainWindowController: NSToolbarItemValidation {
    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        guard let action = item.action else { return true }
        let f = flags
        switch action {
        case Action.toggleFlag:
            (item as? StarToolbarItem)?.flagged = f.flagged
        case Action.moveToTrash:
            item.toolTip = trashTitle(f)
            item.label = trashTitle(f)
        default:
            break
        }
        ActionPresentation.present(item, f)
        return allows(action, f) ?? true
    }
}

extension MainWindowController: MalachiActions {}
