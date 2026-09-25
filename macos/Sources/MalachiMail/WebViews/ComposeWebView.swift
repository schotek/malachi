// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import Network
import os
import WebKit

/// The WKWebView behind the compose editor (ui/internal/editor
/// `Editor`, editor.blp): a contenteditable document with the page's own
/// JavaScript off, a strict Content-Security-Policy, no network and no
/// navigation. It is layer 2 of docs/security.md §3.3, re-established
/// here for WebKit on macOS: what the user types, pastes or quotes is
/// hostile input until the backend sanitises it in draft.save, and this
/// view is what keeps the raw HTML from doing anything in the meantime. It
/// knows nothing about mail: the editor above it loads a document, runs
/// the bridge script's functions and receives the bridge's messages.
///
/// docs/security.md §3.3, rule by rule:
///
/// - *No page JavaScript*: `allowsContentJavaScript = false` on the
///   default web page preferences (`makeConfiguration`), so no `<script>`,
///   `on*=` attribute or `javascript:` URL of the document runs (editor.blp
///   `enable-javascript-markup: false`); `javaScriptCanOpenWindowsAutomatically
///   = false`; the data store is `nonPersistent()` (no storage survives, no
///   cookies exist). The bridge is a `WKUserScript`, which runs although
///   content JavaScript is off and which the CSP does not govern, and the
///   application's own `evaluateJavaScript` calls (`run`) reach it the
///   same way. Both live in a content world of their own
///   (`WKContentWorld.defaultClient`): they share the DOM with the
///   document, but `window.malachi` and the `malachi` message handler do
///   not exist in the page's world, so nothing of the document could
///   reach them even if content JavaScript were ever on.
/// - *A CSP without network access*: `editorDocument` puts `editorCSP`
///   (`default-src 'none'; style-src 'unsafe-inline'; img-src cid: data:`)
///   into the document as a `<meta>`, identical to the GTK policy; a
///   WKWebView has no default policy of its own, so the `<meta>` is the
///   one carrier, and the document is always built by `editorDocument`
///   (`ComposeEditorView.load`), never loaded from anywhere else.
///   `proxyConfigurations` sends whatever the CSP might let through to
///   127.0.0.1:1, where nothing listens, and a content rule list
///   (`ruleListJSON`) blocks every load that is not a `cid:` or `data:`
///   picture before it reaches the network layer, including what the CSP
///   does not govern (a pasted `<link rel="preconnect">` opens a TCP
///   connection without a request). No document is loaded before the list
///   is compiled and installed (`ContentRules`), and none at all when it
///   cannot be: the view then drops the document and reports
///   `onUnavailable`, and the editor above reports a failure.
/// - *Navigation denied*: `decidePolicyFor navigationAction` allows only
///   the initial `about:blank` load of the main frame (the document is
///   loaded with `baseURL: nil`) and cancels everything else, link
///   activation and form submission included; `createWebViewWith` returns
///   nil, so nothing opens a window; a dropped file never reaches the page
///   (`performDragOperation` hands file URLs to `onDropFiles`, so WebKit
///   cannot navigate to or embed a `file:` URL), while text drops behave
///   as WebKit's editing does.
/// - *A `cid:` handler that serves only ids the window registered*:
///   `CIDSchemeHandler` resolves ids through `CIDRegistry` and gates every
///   picture with `checkInline` (never SVG, within the cap).
/// - No context menu (`willOpenMenu` empties it): WebKit's would offer
///   Reload and Open Link. Paste is left to WebKit: the backend sanitises
///   on draft.save.
@MainActor
final class ComposeWebView: WKWebView {
    /// The body of a message the bridge posted to the `malachi` handler
    /// (a JSON string; the editor decodes it).
    var onBridgeMessage: (@MainActor (Any) -> Void)?
    /// Files dropped onto the view, taken away from WebKit.
    var onDropFiles: (@MainActor ([URL]) -> Void)?
    /// The web content process died; the page is gone until the next load.
    var onCrashed: (@MainActor () -> Void)?
    /// A document handed to `loadDocument` was dropped because the content
    /// rule list could not be installed (`ContentRules`); the next load
    /// tries again.
    var onUnavailable: (@MainActor () -> Void)?

    /// The script message handler the bridge posts to.
    static let bridgeHandlerName = "malachi"

    /// The content rule list: everything blocked, then the draft's inline
    /// pictures, the daemon-inlined `data:` pictures and the document
    /// itself allowed again. Bump the identifier with the rules: the store
    /// keeps the compiled list by it.
    static let ruleListIdentifier = "io.github.schotek.Malachi.editor.1"
    static let ruleListJSON = """
    [
      {"trigger": {"url-filter": ".*"}, "action": {"type": "block"}},
      {"trigger": {"url-filter": "^cid:"}, "action": {"type": "ignore-previous-rules"}},
      {"trigger": {"url-filter": "^data:"}, "action": {"type": "ignore-previous-rules"}},
      {"trigger": {"url-filter": "^about:blank$"}, "action": {"type": "ignore-previous-rules"}}
    ]
    """

    private let handler: CIDSchemeHandler
    private let bridgeProxy: BridgeMessageProxy
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "editor")

    /// The content rule list is installed; until then a document waits in
    /// `pendingDocument` while `rulesCompiling` says a compile is under way.
    private var rulesReady = false
    private var rulesCompiling = false
    private var pendingDocument: String?

    /// A view over `registry`, the registry its `cid:` handler resolves
    /// ids in (the process-wide one in the application).
    init(registry: CIDRegistry = .shared) {
        let handler = CIDSchemeHandler(registry: registry)
        let proxy = BridgeMessageProxy()
        self.handler = handler
        self.bridgeProxy = proxy
        super.init(frame: .zero, configuration: Self.makeConfiguration(handler: handler, bridge: proxy))
        proxy.target = self
        navigationDelegate = self
        uiDelegate = self
        allowsBackForwardNavigationGestures = false
        allowsLinkPreview = false
        allowsMagnification = false
        underPageBackgroundColor = .textBackgroundColor
        ensureRules()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The configuration of editor.blp: no content JavaScript, no storage,
    /// no windows, the `cid:` scheme handler, the bridge as a user script
    /// with its message handler in their own content world, and a proxy
    /// nothing answers on for whatever the CSP might let through.
    private static func makeConfiguration(handler: CIDSchemeHandler, bridge: BridgeMessageProxy) -> WKWebViewConfiguration {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.defaultWebpagePreferences.allowsContentJavaScript = false
        config.preferences.javaScriptCanOpenWindowsAutomatically = false
        config.preferences.isFraudulentWebsiteWarningEnabled = false
        config.preferences.isElementFullscreenEnabled = false
        config.mediaTypesRequiringUserActionForPlayback = .all
        config.allowsAirPlayForMediaPlayback = false
        config.setURLSchemeHandler(handler, forURLScheme: CIDSchemeHandler.scheme)
        // The bridge must exist before the document loads: it posts
        // `ready` from the end of the document.
        config.userContentController.addUserScript(
            WKUserScript(source: bridgeJS, injectionTime: .atDocumentEnd, forMainFrameOnly: true, in: .defaultClient))
        config.userContentController.add(bridge, contentWorld: .defaultClient, name: bridgeHandlerName)
        // Nothing the document asks for may leave the process: the CSP
        // stops it first, this stops whatever might slip past the CSP.
        config.websiteDataStore.proxyConfigurations = [
            ProxyConfiguration(httpCONNECTProxy: .hostPort(host: "127.0.0.1", port: 1)),
        ]
        return config
    }

    /// Starts compiling the rule list unless it is installed or under way.
    private func ensureRules() {
        guard !rulesReady, !rulesCompiling else { return }
        rulesCompiling = true
        Task { [weak self] in
            let list = await ContentRules.list(identifier: Self.ruleListIdentifier, json: Self.ruleListJSON)
            self?.installRules(list)
        }
    }

    /// Installs the rule list and loads the document that waited for it.
    /// When no store could compile the list (`ContentRules` has logged
    /// the fault), the document is dropped and `onUnavailable` says so:
    /// the CSP, the proxy and the navigation policy are not enough on
    /// their own.
    private func installRules(_ list: WKContentRuleList?) {
        rulesCompiling = false
        guard let list else {
            pendingDocument = nil
            onUnavailable?()
            return
        }
        configuration.userContentController.add(list)
        rulesReady = true
        if let document = pendingDocument {
            pendingDocument = nil
            loadDocument(document)
        }
    }

    // MARK: Content and scripts

    /// Loads a complete editor document (`editorDocument(body:)` and
    /// nothing else) at the `about:blank` origin. Until the rule list is
    /// installed the document waits; should the list fail to compile, it
    /// is dropped and `onUnavailable` is called.
    func loadDocument(_ html: String) {
        guard rulesReady else {
            pendingDocument = html
            ensureRules()
            return
        }
        loadHTMLString(html, baseURL: nil)
    }

    /// Runs `script` in the bridge's content world without waiting for a
    /// result (editor.eval). Only literals produced by `jsString` may be
    /// spliced into it.
    func run(_ script: String) {
        evaluateJavaScript(script, in: nil, in: .defaultClient) { [weak self] result in
            if case .failure = result {
                self?.log.debug("script failed")
            }
        }
    }

    /// Runs `script` in the bridge's content world and hands back its
    /// result (or the error).
    func run(_ script: String, then done: @escaping @MainActor (Any?, (any Error)?) -> Void) {
        evaluateJavaScript(script, in: nil, in: .defaultClient) { result in
            switch result {
            case .success(let value):
                done(value, nil)
            case .failure(let error):
                done(nil, error)
            }
        }
    }

    // MARK: Context menu

    /// No context menu at all (editor.go `ConnectContextMenu`): WebKit's
    /// offers Reload, Open Link and Back.
    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        super.willOpenMenu(menu, with: event)
        menu.removeAllItems()
    }

    // MARK: Drops

    /// A drag carrying file URLs is the window's business (attachment
    /// import); WebKit would otherwise embed or navigate to `file:` URLs.
    /// Everything else (text, an image from another editor) is left to
    /// WebKit's editing, as in GTK.
    private func isFileDrag(_ sender: any NSDraggingInfo) -> Bool {
        sender.draggingPasteboard.canReadObject(forClasses: [NSURL.self], options: Self.fileURLReading)
    }

    private static let fileURLReading: [NSPasteboard.ReadingOptionKey: Any] = [.urlReadingFileURLsOnly: true]

    override func draggingEntered(_ sender: any NSDraggingInfo) -> NSDragOperation {
        isFileDrag(sender) ? .copy : super.draggingEntered(sender)
    }

    override func draggingUpdated(_ sender: any NSDraggingInfo) -> NSDragOperation {
        isFileDrag(sender) ? .copy : super.draggingUpdated(sender)
    }

    override func draggingExited(_ sender: (any NSDraggingInfo)?) {
        if let sender, isFileDrag(sender) {
            return
        }
        super.draggingExited(sender)
    }

    override func prepareForDragOperation(_ sender: any NSDraggingInfo) -> Bool {
        isFileDrag(sender) ? true : super.prepareForDragOperation(sender)
    }

    override func performDragOperation(_ sender: any NSDraggingInfo) -> Bool {
        guard isFileDrag(sender) else {
            return super.performDragOperation(sender)
        }
        let objects = sender.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: Self.fileURLReading) ?? []
        let urls = objects.compactMap { $0 as? URL }
        if !urls.isEmpty {
            onDropFiles?(urls)
        }
        return true
    }

    override func concludeDragOperation(_ sender: (any NSDraggingInfo)?) {
        if let sender, isFileDrag(sender) {
            return
        }
        super.concludeDragOperation(sender)
    }

    /// The bridge posted a message.
    fileprivate func receive(_ body: Any) {
        onBridgeMessage?(body)
    }
}

// The WebKit delegate protocols are main-actor isolated in the Swift
// overlay: the methods below are ordinary main-actor methods.

extension ComposeWebView: WKNavigationDelegate {
    /// Allows only the initial document load (the main frame's
    /// `about:blank`, from `loadHTMLString`); every other navigation, a
    /// clicked link of a quoted original or a pasted form included, is
    /// refused (editor.go `decidePolicy`). Resource loads are not
    /// navigations: the CSP and the rule list have already limited them to
    /// the draft's own pictures.
    func webView(
        _ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void
    ) {
        let initial = navigationAction.navigationType == .other
            && navigationAction.targetFrame?.isMainFrame == true
            && navigationAction.request.url?.absoluteString == "about:blank"
        if initial {
            decisionHandler(.allow)
            return
        }
        log.debug("navigation refused")
        decisionHandler(.cancel)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        log.warning("web content process terminated")
        onCrashed?()
    }
}

extension ComposeWebView: WKUIDelegate {
    /// Nothing opens a new window (editor.go refuses every new-window
    /// action).
    func webView(
        _ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        nil
    }
}

/// The `malachi` message handler. WebKit retains its handlers strongly, so
/// this small object stands between the configuration and the view.
@MainActor
private final class BridgeMessageProxy: NSObject, WKScriptMessageHandler {
    weak var target: ComposeWebView?

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.receive(message.body)
    }
}
