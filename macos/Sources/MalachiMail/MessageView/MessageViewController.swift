// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// One message display (ui/internal/window/message_view.go `messageView`,
/// window.blp lines 372–605), shared in shape by the main pane, the
/// stand-alone message window and the window of an attached message: the
/// outbox banner, the remote-image bar, the header labels, the attachment
/// chips, the plain-text body and the HTML view (created when the first
/// HTML message is shown). Everything shown is server data: the labels
/// never interpret markup, and the HTML view only ever gets the sanitiser's
/// output.
///
/// The view asks the `MessageCache` for what it shows and renders whatever
/// the cache holds; the actions (flags, moves, images, links, attachments)
/// go through `delegate` (`MessageActionsController`). Rendering is
/// idempotent: `MessageWindows` re-renders every view showing a message
/// when the cache learns something new about it.
@MainActor
final class MessageViewController: NSViewController {
    /// The body area's pages (window.blp `body_stack`).
    private enum BodyPage {
        case text, loading, html
    }

    /// How long the body area stays blank before the spinner appears
    /// (message_view.go `bodySpinnerDelay`): bodies come from the local
    /// store, so most messages render sooner than this and never show a
    /// spinner at all.
    static let spinnerDelay: TimeInterval = 0.4
    static let crossfadeDuration: TimeInterval = 0.2

    let state: AppState
    let cache: MessageCache
    let mode: MessageViewMode

    /// The actions the view triggers (installed by the application).
    weak var delegate: (any MessageActionDelegate)?

    /// What is on display: the summary `show` was given, or the attached
    /// message's own summary in embedded mode (it carries the containing
    /// message's id).
    private(set) var current: MessageSummary?

    /// The links of the body on display, for `openLink`.
    private(set) var links: [Link] = []

    /// The body last rendered, for the fallback to its plain text when the
    /// web view cannot show its HTML (`htmlUnavailable`).
    private var renderedBody: MessageBodyResult?

    /// A plain-text body scrolls to its top the first time it is shown for
    /// a message, not on every re-render (message.get answering after the
    /// body must not jump).
    private var scrollToTopPending = true

    /// Called after every render with what was rendered (the owning
    /// window's title and star follow it).
    var onRender: (@MainActor (MessageSummary, LoadedMessage?) -> Void)?

    /// Embedded mode: the bar's Load Images (the window re-renders the
    /// part through message.embedded).
    var onEmbeddedLoadImages: (@MainActor () -> Void)?

    /// A chip's View: opens the attached message in its own window
    /// (`MessageWindows.openEmbedded`); installed by the hub.
    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ part: String, _ chip: NSView?) -> Void)?

    /// The toast overlay of a window mode view; the pane uses the main
    /// window's (window.blp `toast_overlay`).
    let toasts: ToastPresenter?

    let bodyTextView = MessageBodyTextView()
    let header = MessageHeaderView()

    private let banner = BannerView()
    private let remoteBar: RemoteBarView
    private let textScroll = NSScrollView()
    private let textClamp: ClampView
    private let loadingPage = NSView()
    private let loadingSpinner = Spinner(size: 32)
    private let bodyContainer = NSView()
    private var webView: MessageWebView?
    private let pages = NSView()
    private let messagePage = FillStackView()
    private var emptyPage: StatusPageView?
    private var showingMessage: Bool
    private var chips: [NSView] = []
    private var spinnerWork: DispatchWorkItem?
    private var settingsTokens: [Settings.ChangeToken] = []
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "message")

    init(state: AppState, cache: MessageCache, mode: MessageViewMode) {
        self.state = state
        self.cache = cache
        self.mode = mode
        remoteBar = RemoteBarView(showsTrust: mode != .embedded)
        textClamp = ClampView(maximum: 900, tight: 700, child: bodyTextView)
        toasts = mode == .pane ? nil : ToastPresenter()
        showingMessage = mode != .pane
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        // The headers stay put at the top; the body below scrolls on its
        // own, because a web view scrolls itself and would have no height
        // inside a scroll view.
        let headerClamp = ClampView(maximum: 900, tight: 700, child: header)
        headerClamp.setContentHuggingPriority(.required, for: .vertical)

        textScroll.documentView = textClamp
        textScroll.hasVerticalScroller = true
        textScroll.hasHorizontalScroller = false
        textScroll.autohidesScrollers = true
        textScroll.drawsBackground = false
        textScroll.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            textClamp.widthAnchor.constraint(equalTo: textScroll.contentView.widthAnchor),
        ])

        loadingPage.translatesAutoresizingMaskIntoConstraints = false
        loadingPage.isHidden = true
        loadingPage.addSubview(loadingSpinner)
        loadingSpinner.setAccessibilityLabel(L10n.C("accessibility", "Loading the message"))
        NSLayoutConstraint.activate([
            loadingSpinner.centerXAnchor.constraint(equalTo: loadingPage.centerXAnchor),
            loadingSpinner.centerYAnchor.constraint(equalTo: loadingPage.centerYAnchor),
        ])

        bodyContainer.translatesAutoresizingMaskIntoConstraints = false
        bodyContainer.setContentHuggingPriority(.defaultLow, for: .vertical)
        bodyContainer.setContentCompressionResistancePriority(.defaultLow, for: .vertical)
        bodyContainer.addSubview(textScroll)
        bodyContainer.addSubview(loadingPage)
        NSLayoutConstraint.activate(fill(textScroll, in: bodyContainer) + fill(loadingPage, in: bodyContainer))

        messagePage.spacing = 0
        messagePage.addArrangedSubview(headerClamp)
        messagePage.addArrangedSubview(bodyContainer)

        pages.translatesAutoresizingMaskIntoConstraints = false
        pages.setContentHuggingPriority(.defaultLow, for: .vertical)
        pages.setContentCompressionResistancePriority(.defaultLow, for: .vertical)
        pages.addSubview(messagePage)
        NSLayoutConstraint.activate(fill(messagePage, in: pages))
        if mode == .pane {
            let empty = StatusPageView(
                illustration: .icon("mail-unread-symbolic"),
                title: L10n.T("No Message Selected"),
                description: L10n.T("Select a message to read it here."))
            emptyPage = empty
            pages.addSubview(empty)
            NSLayoutConstraint.activate(fill(empty, in: pages))
            messagePage.isHidden = true
            messagePage.alphaValue = 0
        }

        banner.onButton = { [weak self] in
            guard let self, let id = self.current?.id else { return }
            self.delegate?.retryOutbox(id)
        }
        remoteBar.onLoad = { [weak self] in self?.loadImages() }
        remoteBar.onTrust = { [weak self] in
            guard let self, let id = self.current?.id else { return }
            self.delegate?.trustSender(id)
        }

        let root = FillStackView()
        root.spacing = 0
        if mode != .embedded {
            root.addArrangedSubview(banner)
        }
        root.addArrangedSubview(remoteBar)
        root.addArrangedSubview(pages)
        // In the main window the pane runs under the unified toolbar
        // (fullSizeContentView); the content starts below it, at the safe
        // area, like the GTK pane below its header bar.
        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(root)
        NSLayoutConstraint.activate([
            root.topAnchor.constraint(equalTo: container.safeAreaLayoutGuide.topAnchor),
            root.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            root.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            root.trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ])
        toasts?.install(over: container)
        view = container

        applyBodyFont()
        let settings = state.settings
        settingsTokens.append(settings.onChange(.textZoom) { [weak self] in self?.applyBodyFont() })
        settingsTokens.append(settings.onChange(.monospacePlainText) { [weak self] in self?.applyBodyFont() })
    }

    private func fill(_ sub: NSView, in container: NSView) -> [NSLayoutConstraint] {
        [
            sub.topAnchor.constraint(equalTo: container.topAnchor),
            sub.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            sub.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            sub.trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ]
    }

    /// Detaches from the settings and drops the pending spinner (the
    /// owning window closed; the pane never closes).
    func close() {
        cancelSpinner()
        for t in settingsTokens {
            t.cancel()
        }
        settingsTokens = []
    }

    // MARK: Showing

    /// Displays message `s`: the summary headers at once, the rest (and the
    /// outbox banner, for a queued message) when the cache has it
    /// (message_view.go `showMessage`, message_window.go `show`).
    func show(_ s: MessageSummary) {
        guard mode != .embedded else { return }
        _ = view
        if current?.id != s.id {
            scrollToTopPending = true
        }
        current = s
        setPage(message: true)
        if let lm = cache.loaded(s.id), lm.complete {
            render(s, lm)
            return
        }
        // The fetch first: it clears a stale body error before its retry,
        // so the render below shows the wait rather than the old error.
        cache.fetch(s) { [weak self] lm in
            guard let self, self.mode != .embedded, self.current?.id == s.id else {
                return // the pane moved on
            }
            self.render(s, lm)
        }
        render(s, cache.loaded(s.id))
    }

    /// The pane's "No Message Selected" page; windows never clear.
    func clear() {
        guard mode == .pane else { return }
        _ = view
        current = nil
        links = []
        renderedBody = nil
        scrollToTopPending = true
        cancelSpinner()
        banner.reveal(false)
        setBarVisible(false)
        webView?.clear() // drop the pictures of the message before
        setPage(message: false)
    }

    /// Renders an attached message (embedded.go `show`): its own headers
    /// and body through the shared view. `containing` is the message it
    /// was attached to, `part` the part id.
    func showEmbedded(_ containing: MessageSummary, part: String, result: MessageEmbeddedResult) {
        guard mode == .embedded else { return }
        _ = view
        let s = result.message.summary
        current = s
        scrollToTopPending = true
        render(s, LoadedMessage(msg: result.message, body: result.body))
    }

    // MARK: Rendering

    /// Shows whatever `lm` holds so far: full headers once message.get
    /// answered, the body once message.body did, and the attachment chips
    /// from both. A nil `lm` shows the summary and the loading placeholder
    /// (message_view.go `render`).
    func render(_ s: MessageSummary, _ lm: LoadedMessage?) {
        _ = view
        renderHeaders(s, lm?.msg)
        if let lm, lm.bodySettled {
            renderBody(lm)
        } else {
            loading()
        }
        renderAttachments(s, lm)
        if mode != .embedded {
            renderOutboxBanner(lm?.msg)
        }
        onRender?(s, lm)
    }

    /// Redraws the remote-image bar for `lm` and leaves the body alone
    /// (remote.go `refreshRemoteBar`).
    func refreshRemoteBar(_ lm: LoadedMessage) {
        renderRemoteBar(lm)
    }

    /// The headers: from the summary alone, or from the full message when
    /// `m` is not nil (recipients with Cc). The chips are
    /// `renderAttachments`' business: they depend on the body too.
    private func renderHeaders(_ s: MessageSummary, _ m: Message?) {
        var from = s.from
        var to = s.to
        var cc: [Address]?
        var date = s.date
        var subject = s.subject
        if let m {
            from = m.summary.from
            to = m.summary.to
            cc = m.cc
            date = m.summary.date
            subject = m.summary.subject
        }
        header.subject = subjectText(subject)
        header.from = from.first.map(formatAddress) ?? ""
        header.recipients = recipientsText(to: to, cc: cc)
        header.date = date.isGoZero ? "" : formatDateTime(date)
    }

    /// The body, its state, or the error that prevented it: the sanitised
    /// HTML in the web view when there is one, the plain text otherwise,
    /// with a hint when the HTML was withheld and the bar when remote
    /// images were removed.
    private func renderBody(_ lm: LoadedMessage) {
        cancelSpinner()
        links = []
        renderedBody = lm.body
        if let err = lm.err {
            header.hintVisible = false
            setBarVisible(false)
            showText(rpcErrorText(L10n.T("Loading the message"), err))
            return
        }
        let b = lm.body
        if showsHTML(b), let html = b?.html {
            links = b?.links ?? []
            header.hintVisible = false
            htmlView().load(body: html)
            setBodyPage(.html)
            renderRemoteBar(lm)
            return
        }
        header.hintVisible = b?.htmlWithheld == true
        setBarVisible(false)
        showText(bodyText(b))
    }

    /// Empties the body area while the body is on its way and, if it takes
    /// long enough to notice, shows a spinner in the middle of it.
    private func loading() {
        cancelSpinner()
        links = []
        renderedBody = nil
        header.hintVisible = false
        setBarVisible(false)
        // Blank at once: this also drops the pictures of the message before.
        showText("")
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.spinnerWork = nil
            self.setBodyPage(.loading)
        }
        spinnerWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.spinnerDelay, execute: work)
    }

    /// Disarms the pending spinner, if any. Safe to call twice.
    private func cancelSpinner() {
        spinnerWork?.cancel()
        spinnerWork = nil
    }

    /// Shows plain text in the body area.
    private func showText(_ text: String) {
        bodyTextView.setText(text)
        setBodyPage(.text)
        webView?.clear() // drop the pictures of the previous message
    }

    private func setBodyPage(_ page: BodyPage) {
        textScroll.isHidden = page != .text
        loadingPage.isHidden = page != .loading
        if page == .loading {
            loadingSpinner.start()
        } else {
            loadingSpinner.stop()
        }
        webView?.isHidden = page != .html
        if page == .text, scrollToTopPending {
            scrollToTopPending = false
            textScroll.contentView.scroll(to: .zero)
            textScroll.reflectScrolledClipView(textScroll.contentView)
        }
    }

    /// The web view cannot show HTML at all (its content rule list did
    /// not compile, `ContentRules`): the plain text with the hint instead,
    /// as for HTML the daemon withheld. The next message tries the view
    /// again.
    private func htmlUnavailable() {
        guard let b = renderedBody, showsHTML(b) else { return }
        log.error("HTML body not shown: the web view has no content rule list")
        links = []
        header.hintVisible = true
        setBarVisible(false)
        showText(bodyText(b))
    }

    /// The web view, created on first use: a plain-text mailbox never
    /// starts a web process.
    private func htmlView() -> MessageWebView {
        if let webView {
            return webView
        }
        let wv = MessageWebView(cache: cache, zoom: state.settings.textZoom)
        wv.onLink = { [weak self] link in self?.openLink(link) }
        wv.onUnavailable = { [weak self] in self?.htmlUnavailable() }
        wv.isHidden = true
        bodyContainer.addSubview(wv)
        NSLayoutConstraint.activate(fill(wv, in: bodyContainer))
        webView = wv
        return wv
    }

    /// The text-zoom and monospace settings, for the text body and the
    /// web view.
    private func applyBodyFont() {
        let settings = state.settings
        bodyTextView.applyFont(Typo.body(zoom: settings.textZoom, monospace: settings.monospacePlainText))
        webView?.setZoom(settings.textZoom)
    }

    // MARK: Outbox banner

    /// The delivery state of `m` (nil while message.get has not answered)
    /// on the banner (outbox.go `renderOutboxBanner`). The title may carry
    /// the backend's error message: plain text.
    func renderOutboxBanner(_ m: Message?) {
        guard mode != .embedded else { return }
        let text = outboxBannerText(m?.summary.outbox)
        banner.title = text.title
        banner.buttonTitle = text.button
        banner.reveal(text.shown)
    }

    // MARK: Remote-image bar

    /// The bar for what is known about the message (remote.go
    /// `renderRemoteBar`).
    func renderRemoteBar(_ lm: LoadedMessage) {
        showRemoteBar(remoteBarState(for: lm))
    }

    /// Puts the bar in state `st` (remote.go `showRemoteBar`).
    func showRemoteBar(_ st: RemoteBarState) {
        if st.loading {
            remoteBar.text = L10n.T("Loading remote images…")
        } else if st.blocked > 0 {
            // TRANSLATORS: %d is the number of remote images the message tried to load.
            remoteBar.text = L10n.N("%d remote image was blocked", "%d remote images were blocked", st.blocked)
        }
        setBarLoading(st.loading)
        setBarVisible(st.visible)
    }

    /// Shows or hides the bar, taking the focus out of it first: the body
    /// is somewhere harmless to put it, unlike the selectable label AppKit
    /// might pick (message_view.go `setBarVisible`).
    private func setBarVisible(_ show: Bool) {
        if !show {
            moveFocusOutOfBar()
        }
        remoteBar.isHidden = !show
    }

    /// Switches the bar between offering the images and showing that they
    /// are on their way; hiding a focused button would move the focus, so
    /// it leaves the bar first (message_view.go `setBarLoading`).
    private func setBarLoading(_ loading: Bool) {
        if loading {
            moveFocusOutOfBar()
        }
        remoteBar.setLoading(loading)
    }

    private func moveFocusOutOfBar() {
        guard let window = view.window, remoteBar.holdsFirstResponder(of: window) else { return }
        window.makeFirstResponder(bodyTextView)
    }

    /// The bar's Load Images: the message's images through the actions
    /// (remote.go `loadRemoteImages`), or the attached message rendered
    /// again by its window (embedded.go `loadImages`).
    private func loadImages() {
        if mode == .embedded {
            onEmbeddedLoadImages?()
            return
        }
        guard let id = current?.id else { return }
        delegate?.loadImages(id)
    }

    // MARK: Links

    /// A link activated in the HTML view, handed on as the `href`
    /// attribute as written (`ActivatedLink.href`), which is the string
    /// the daemon lists in `links` and what the delegate matches exactly;
    /// only an activation the view's script did not see carries WebKit's
    /// resolved URL instead, which the delegate then treats as unlisted.
    private func openLink(_ link: ActivatedLink) {
        delegate?.openLink(link.href, links: links, from: view.window)
    }

    // MARK: Attachment chips

    /// Rebuilds the chips for what `lm` holds (attachments.go
    /// `renderAttachments`): nothing until message.get answered, otherwise
    /// every attachment except the pictures the HTML body on display
    /// already shows.
    private func renderAttachments(_ s: MessageSummary, _ lm: LoadedMessage?) {
        header.chips.removeAllViews()
        chips = []
        guard let lm, let m = lm.msg else {
            header.chipsVisible = false
            return
        }
        let atts = chipAttachments(m.attachments, lm.body)
        if atts.isEmpty {
            header.chipsVisible = false
            return
        }
        var allOK = true
        for a in atts {
            var (ok, why) = partAvailable(a, lm.body)
            if mode == .embedded {
                // The parts of an attached message have no numbers; nothing
                // can fetch them (message.embedded in docs/api.md).
                ok = false
                why = L10n.T("Files inside an attached message cannot be opened or saved yet.")
            }
            allOK = allOK && ok
            addChip(buildChip(s, a, available: ok, why: why))
        }
        if atts.count >= 2, allOK {
            addChip(buildSaveAll(s, atts))
        }
        header.chipsVisible = true
    }

    private func addChip(_ chip: NSView) {
        header.chips.addView(chip)
        chips.append(chip)
    }

    /// One attachment (attachments.go `buildChip`); the actions close over
    /// the attachment and the message it belongs to.
    private func buildChip(_ s: MessageSummary, _ a: Attachment, available: Bool, why: String) -> NSView {
        let chip = AttachmentChipView(attachment: a, available: available, why: why)
        chip.onOpen = { [weak self, weak chip] in
            guard let self else { return }
            self.delegate?.openAttachment(a, of: s, from: chip?.window ?? self.view.window)
        }
        chip.onSave = { [weak self, weak chip] in
            guard let self else { return }
            self.delegate?.saveAttachment(a, of: s, from: chip?.window ?? self.view.window)
        }
        chip.onView = { [weak self, weak chip] in
            guard let self else { return }
            self.onOpenEmbedded?(s, a.partId, chip)
        }
        return chip
    }

    /// The button after the chips that saves all of them into one folder;
    /// disabled while the run lasts (attachments.go `saveAllAttachments`
    /// `SetSensitive`), until the delegate reports its end.
    private func buildSaveAll(_ s: MessageSummary, _ atts: [Attachment]) -> NSView {
        let button = SaveAllChipView()
        button.onClick = { [weak self, weak button] in
            guard let self, let delegate = self.delegate else { return }
            let window = button?.window ?? self.view.window
            button?.isEnabled = false
            delegate.saveAllAttachments(atts, of: s, from: window) {
                button?.isEnabled = true
            }
        }
        return button
    }

    // MARK: Pages

    /// Switches between the "No Message Selected" page and the message
    /// with a crossfade (window.blp `message_stack`); windows only ever
    /// show the message.
    private func setPage(message: Bool, animated: Bool = true) {
        guard mode == .pane, let empty = emptyPage else { return }
        guard message != showingMessage else { return }
        showingMessage = message
        let from = message ? empty : messagePage
        let to = message ? messagePage : empty
        to.isHidden = false
        guard animated, view.window != nil else {
            to.alphaValue = 1
            from.alphaValue = 0
            from.isHidden = true
            return
        }
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = Self.crossfadeDuration
            to.animator().alphaValue = 1
            from.animator().alphaValue = 0
        }, completionHandler: {
            MainActor.assumeIsolated {
                if from.alphaValue == 0 {
                    from.isHidden = true
                }
            }
        })
    }
}
