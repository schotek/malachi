// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// One message display (ui/internal/window/message_view.go `messageView`,
/// window.blp lines 372–605), shared in shape by the main pane, the
/// stand-alone message window and the window of an attached message: the
/// outbox banner, the remote-image bar, the pictures bar (not for an
/// attached message, whose pictures come inlined), the header labels, the attachment
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
    /// (`MessageWindows.openEmbedded`), downloading the message first when
    /// the chip showed it on the mail server (`remote`); installed by the
    /// hub.
    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)?

    /// The toast overlay of a window mode view; the pane uses the main
    /// window's (window.blp `toast_overlay`).
    let toasts: ToastPresenter?

    let bodyTextView = MessageBodyTextView()
    let header = MessageHeaderView()

    private let banner = BannerView()
    /// A message of the Drafts folder (window.blp `draft_banner`; the pane
    /// only): the button opens it in the compose window.
    private let draftBanner = BannerView(
        title: L10n.T("This message is a draft"), buttonTitle: L10n.T("Edit"), symbol: "square.and.pencil"
    )
    private let remoteBar: RemoteBarView
    /// The pictures kept on the mail server only (window.blp
    /// `pictures_bar`), below the remote-image bar; both may show.
    private let picturesBar = RemoteBarView.pictures()
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
    /// The cache entry last rendered, for redrawing the chips when the
    /// cache no longer holds it (`refreshChips`).
    private var shownLoaded: LoadedMessage?
    private var spinnerWork: DispatchWorkItem?
    private var settingsTokens: [Settings.ChangeToken] = []
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "message")

    init(state: AppState, cache: MessageCache, mode: MessageViewMode) {
        self.state = state
        self.cache = cache
        self.mode = mode
        remoteBar = RemoteBarView(showsTrust: mode != .embedded)
        // tight = maximum: min(width, 900), the width viewerBaseCSS gives
        // an HTML body, so its text lines up with the headers (window.blp).
        textClamp = ClampView(maximum: 900, tight: 900, child: bodyTextView)
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
        let headerClamp = ClampView(maximum: 900, tight: 900, child: header)
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
        draftBanner.onButton = { [weak self] in
            guard let self, let id = self.current?.id else { return }
            self.delegate?.editDraft(id)
        }
        remoteBar.onLoad = { [weak self] in self?.loadImages() }
        picturesBar.onLoad = { [weak self] in self?.downloadPictures() }
        header.addresses.onCopy = { [weak self] address in self?.copyAddress(address) }
        header.addresses.onWrite = { [weak self] address, account in
            self?.delegate?.newMessage(to: address, account: account)
        }
        remoteBar.onTrust = { [weak self] in
            guard let self, let id = self.current?.id else { return }
            self.delegate?.trustSender(id)
        }

        let root = FillStackView()
        root.spacing = 0
        if mode != .embedded {
            root.addArrangedSubview(banner)
        }
        if mode == .pane {
            root.addArrangedSubview(draftBanner)
        }
        root.addArrangedSubview(remoteBar)
        if mode != .embedded {
            root.addArrangedSubview(picturesBar)
        }
        root.addArrangedSubview(pages)
        // In the main window the pane runs under the unified toolbar
        // (fullSizeContentView); the content starts below it, at the safe
        // area, like the GTK pane below its header bar. The pane is white
        // like the list beside it (the header over the window's grey read
        // as a separate bar).
        let container = ContentBackgroundView()
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
        if mode == .pane {
            draftBanner.reveal(delegate?.isDraft(s) == true)
        }
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
        shownLoaded = nil
        links = []
        renderedBody = nil
        scrollToTopPending = true
        cancelSpinner()
        banner.reveal(false)
        draftBanner.reveal(false)
        hideBars()
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
        shownLoaded = lm
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

    /// Redraws the remote-image bar and the pictures bar for `lm` and
    /// leaves the body alone (remote.go `refreshRemoteBar`).
    func refreshRemoteBar(_ lm: LoadedMessage) {
        renderRemoteBar(lm)
        renderPicturesBar(lm)
    }

    /// Redraws the attachment chips of the message on display from `lm`
    /// (or the entry last rendered, when the cache no longer holds it) and
    /// leaves the rest alone: its download began to show the spinner or
    /// ended (download.go `refreshChips`).
    func refreshChips(_ lm: LoadedMessage?) {
        guard let s = current else { return }
        let entry = lm ?? shownLoaded
        if let lm {
            shownLoaded = lm
        }
        renderAttachments(s, entry)
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
        header.addresses.show(s.id, account: s.accountId, from: from, to: to, cc: cc)
        header.date = date.isGoZero ? "" : formatDateTime(date)
    }

    /// The body, its state, or the error that prevented it: the sanitised
    /// HTML in the web view when there is one, the plain text otherwise,
    /// with a hint when the HTML was withheld, the bar when remote images
    /// were removed and the pictures bar when pictures are on the mail
    /// server only.
    private func renderBody(_ lm: LoadedMessage) {
        cancelSpinner()
        links = []
        let before = renderedBody
        renderedBody = lm.body
        if let err = lm.err {
            header.hintVisible = false
            hideBars()
            showText(rpcErrorText(L10n.T("Loading the message"), err))
            return
        }
        let b = lm.body
        if showsHTML(b), let b, let html = b.html {
            links = b.links
            header.hintVisible = false
            htmlView().load(body: html, reload: picturesArrived(before, b))
            setBodyPage(.html)
            renderRemoteBar(lm)
            renderPicturesBar(lm)
            return
        }
        header.hintVisible = b?.htmlWithheld == true
        hideBars()
        showText(bodyText(b))
    }

    /// Whether `now` is the body on display (`before`) asked for again
    /// after its pictures kept on the mail server were downloaded: the
    /// HTML is the same, but its `malachi-cid:` pictures load now, so the
    /// web view must load it again (GTK loads every render anyway).
    private func picturesArrived(_ before: MessageBodyResult?, _ now: MessageBodyResult) -> Bool {
        guard let before, before.messageId == now.messageId else { return false }
        return before.remotePictureCount > 0 && before != now
    }

    /// Empties the body area while the body is on its way and, if it takes
    /// long enough to notice, shows a spinner in the middle of it.
    private func loading() {
        cancelSpinner()
        links = []
        renderedBody = nil
        header.hintVisible = false
        hideBars()
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
        hideBars()
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
        if m?.summary.outbox?.state == .failed {
            banner.setSymbol("exclamationmark.triangle.fill", .warning)
        } else {
            banner.setSymbol("paperplane", .info)
        }
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
        setVisible(remoteBar, show)
    }

    /// Switches the bar between offering the images and showing that they
    /// are on their way; hiding a focused button would move the focus, so
    /// it leaves the bar first (message_view.go `setBarLoading`).
    private func setBarLoading(_ loading: Bool) {
        setLoading(remoteBar, loading)
    }

    /// Hides the remote-image bar and the pictures bar (a body that is not
    /// in the HTML view, or none).
    private func hideBars() {
        setVisible(remoteBar, false)
        setVisible(picturesBar, false)
    }

    private func setVisible(_ bar: RemoteBarView, _ show: Bool) {
        if !show {
            moveFocus(outOf: bar)
        }
        bar.isHidden = !show
    }

    private func setLoading(_ bar: RemoteBarView, _ loading: Bool) {
        if loading {
            moveFocus(outOf: bar)
        }
        bar.setLoading(loading)
    }

    private func moveFocus(outOf bar: RemoteBarView) {
        guard let window = view.window, bar.holdsFirstResponder(of: window) else { return }
        window.makeFirstResponder(bodyTextView)
    }

    // MARK: Pictures bar

    /// The pictures bar for what is known about the message (remote.go
    /// `renderPicturesBar`); an attached message's view has none.
    func renderPicturesBar(_ lm: LoadedMessage) {
        guard mode != .embedded else { return }
        let st = picturesBarState(for: lm)
        if st.loading {
            picturesBar.text = L10n.T("Downloading pictures…")
        } else if st.remote > 0 {
            // TRANSLATORS: %d is the number of pictures of the message kept on the mail server only.
            picturesBar.text = L10n.N(
                "%d picture of this message is on the server only", "%d pictures of this message are on the server only",
                st.remote)
        }
        setLoading(picturesBar, st.loading)
        setVisible(picturesBar, st.visible)
    }

    /// The pictures bar's Download Pictures (remote.go `downloadPictures`):
    /// through the actions, which toast a failure in this view's window.
    private func downloadPictures() {
        guard mode != .embedded, let id = current?.id else { return }
        delegate?.downloadPictures(id, from: view.window)
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

    // MARK: Address chips

    /// A chip's Copy Address (addresses.go `chip`): the bare address on
    /// the general pasteboard, and a toast that says so.
    private func copyAddress(_ a: Address) {
        let addr = a.address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !addr.isEmpty else { return }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(addr, forType: .string)
        windowToast(L10n.T("Address copied"), in: view.window, or: state.toasts)
    }

    // MARK: Attachment chips

    /// Rebuilds the chips for what `lm` holds (attachments.go
    /// `renderAttachments`): nothing until message.get answered, otherwise
    /// every attachment except the pictures the HTML body on display
    /// already shows. A part on the mail server shows the server symbol,
    /// or the spinner while its message downloads; Save All appears with
    /// two or more parts that can all be saved, now or after a download.
    /// The chip that holds the keyboard focus (one used a moment ago, whose
    /// download starts or ends now) goes with the rest; the focus goes to
    /// the chip in its place, or to the body when there is none.
    private func renderAttachments(_ s: MessageSummary, _ lm: LoadedMessage?) {
        let focusAt = focusedChip()
        let window = view.window
        if focusAt != nil {
            window?.makeFirstResponder(nil)
        }
        defer {
            if let at = focusAt, let window {
                let target = at < chips.count ? ((chips[at] as? AttachmentChipView)?.control ?? chips[at]) : nil
                if target.map({ window.makeFirstResponder($0) }) != true {
                    window.makeFirstResponder(bodyTextView)
                }
            }
        }
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
        let downloading = mode != .embedded && cache.showsDownload(s.id)
        var allOK = true
        for a in atts {
            var (state, why) = partState(a, lm.body)
            if mode == .embedded {
                // The parts of an attached message have no numbers; nothing
                // can fetch them (message.embedded in docs/api.md).
                state = .unavailable
                why = L10n.T("Files inside an attached message cannot be opened or saved yet.")
            }
            allOK = allOK && (state == .local || state == .remote)
            addChip(buildChip(s, a, state: state, why: why, downloading: downloading))
        }
        if atts.count >= 2, allOK {
            addChip(buildSaveAll(s, atts, remote: anyRemote(atts, lm.body)))
        }
        header.chipsVisible = true
    }

    /// The position of the chip (or Save All) that holds the keyboard
    /// focus of the view's window, nil when none does.
    private func focusedChip() -> Int? {
        guard let focus = view.window?.firstResponder as? NSView else { return nil }
        return chips.firstIndex { focus === $0 || focus.isDescendant(of: $0) }
    }

    private func addChip(_ chip: NSView) {
        header.chips.addView(chip)
        chips.append(chip)
    }

    /// The chip on display for part `partId`, for Quick Look to zoom out
    /// of: the chips are drawn again while a download runs, so the one
    /// that was clicked may be gone by the time the file is ready.
    private func chipView(forPart partId: String) -> NSView? {
        chips.first { ($0 as? AttachmentChipView)?.attachment.partId == partId }
    }

    /// One attachment (attachments.go `buildChip`); the actions close over
    /// the attachment and the message it belongs to.
    private func buildChip(_ s: MessageSummary, _ a: Attachment, state: PartState, why: String, downloading: Bool) -> NSView {
        let chip = AttachmentChipView(attachment: a, state: state, why: why, downloading: downloading)
        let remote = state == .remote
        chip.onPreview = { [weak self, weak chip] in
            guard let self else { return }
            self.delegate?.previewAttachment(a, of: s, remote: remote, from: chip?.window ?? self.view.window) { [weak self] part in
                self?.chipView(forPart: part)
            }
        }
        chip.onOpen = { [weak self, weak chip] in
            guard let self else { return }
            self.delegate?.openAttachment(a, of: s, remote: remote, from: chip?.window ?? self.view.window)
        }
        chip.onSave = { [weak self, weak chip] in
            guard let self else { return }
            self.delegate?.saveAttachment(a, of: s, remote: remote, from: chip?.window ?? self.view.window)
        }
        chip.onView = { [weak self, weak chip] in
            guard let self else { return }
            self.onOpenEmbedded?(s, a, remote, chip)
        }
        // The Assistant (ui/internal/assistant): hidden while it is not
        // shown (its menu off, or the bridge not registered), disabled while
        // no app handles the chosen Claude app's links (the file itself goes
        // without the bridge, and never to the other app).
        chip.assistantItem = { [weak self] in
            guard let self, self.state.assistant.shown else { return nil }
            self.state.assistant.refreshHandlers()
            return self.state.assistant.pick(needsBridge: false).ok
        }
        chip.onAskAssistant = { [weak self, weak chip] in
            guard let self else { return }
            self.delegate?.askAssistant(about: a, of: s, remote: remote, from: chip?.window ?? self.view.window)
        }
        return chip
    }

    /// The button after the chips that saves all of them into one folder;
    /// `remote` says that some are on the mail server. It stays disabled
    /// while a Save All of the message runs, wherever the message is shown
    /// (attachments.go `buildSaveAll`, `MessageCache.isSavingAll`).
    private func buildSaveAll(_ s: MessageSummary, _ atts: [Attachment], remote: Bool) -> NSView {
        let button = SaveAllChipView()
        button.isEnabled = !cache.isSavingAll(s.id)
        button.onClick = { [weak self, weak button] in
            guard let self, let delegate = self.delegate else { return }
            delegate.saveAllAttachments(atts, of: s, remote: remote, from: button?.window ?? self.view.window)
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
