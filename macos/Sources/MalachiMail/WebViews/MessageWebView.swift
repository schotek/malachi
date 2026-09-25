// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import Network
import os
import WebKit

/// The viewer of a message's sanitised HTML body (ui/internal/htmlview
/// `View`, html_view.blp): a WKWebView with JavaScript off, a strict
/// Content-Security-Policy, no network and no navigation. It is layer 2 of
/// docs/security.md §3.2, re-established here for WebKit on macOS: the
/// backend's sanitiser is what makes the content safe, this view is what
/// keeps a sanitiser bug from becoming a compromise. It knows nothing about
/// mail beyond "here is a body fragment and a way to fetch its pictures".
///
/// docs/security.md §3.2, layer 2, bullet by bullet:
///
/// - *JavaScript disabled; plugins, media, WebGL, local storage, databases,
///   DNS prefetching, hyperlink auditing off*: `allowsContentJavaScript =
///   false` on the default web page preferences (`makeConfiguration`), so
///   no script of the document runs; `javaScriptCanOpenWindowsAutomatically
///   = false`; the data store is `nonPersistent()` (no storage survives, no
///   cookies exist); media needs user action and cannot AirPlay; the CSP
///   (`default-src 'none'`) refuses every plugin, frame, font, media and
///   fetch; link previews and back/forward gestures are off.
/// - *Content-Security-Policy `default-src 'none'; img-src malachi-cid:
///   data:; style-src 'unsafe-inline'`*: `viewerDocument` puts `viewerCSP`
///   into the document as a `<meta>`, identical to the GTK policy; a
///   WKWebView has no default policy of its own, so the `<meta>` is the one
///   carrier, and the document is always built by `viewerDocument`
///   (`load(body:)`), never loaded from anywhere else.
/// - *A custom URI scheme handler serving `malachi-cid:` parts through
///   message.part (images only, never SVG); network otherwise denied (an
///   ephemeral session pointed at an unreachable proxy, plus denial of
///   every navigation but the initial load)*: `PartSchemeHandler` is
///   registered for `malachi-cid` and checks `parsePartPath` and
///   `isImageType`; `proxyConfigurations` sends every request the CSP might
///   let through to 127.0.0.1:1, where nothing listens;
///   `decidePolicyFor navigationAction` allows only the initial
///   `about:blank` load of the main frame (the document is loaded with
///   `baseURL: nil`) and cancels everything else; `createWebViewWith`
///   returns nil, so nothing opens a window; drops onto the view are
///   refused (`draggingEntered`), so a dropped file cannot navigate it.
/// - *Navigation intercepted: any link activation is cancelled, the
///   destination displayed, and opened on user confirmation*: a click on a
///   link is taken by the view's own script (`viewerScript`) before WebKit
///   navigates: it cancels the click and reports the `href` attribute *as
///   written*, which is the string the daemon lists in `links[]`, together
///   with the URL WebKit resolved it to (`ActivatedLink`); the actions
///   layer matches the attribute against the list and confirms a masked or
///   unlisted link before opening it. A link activation that reaches the
///   navigation policy anyway (no click event preceded it) is cancelled
///   there and reported the same way with the resolved URL alone. The link
///   under the pointer is shown in a status label at the bottom left, as
///   plain text.
/// - *A separate process with a fresh ephemeral data manager*: WebKit's
///   process model; one non-persistent data store per view. One view is
///   reused between messages (D+ of the plan): without network nothing
///   persists, `clear()` drops the pictures with the document.
/// - A content rule list (`ruleListJSON`, compiled by `ContentRules`)
///   blocks every load that is not one of the application's own pictures
///   before it reaches the network layer, including what the CSP does not
///   govern: a `<link rel="preconnect">` opens a TCP connection without a
///   request, and the proxy setting does not catch it (found by the
///   network canary). No body is loaded before the list is installed, and
///   none at all when it cannot be: the view then reports `onUnavailable`
///   and the reader shows the plain text instead.
/// - The view's own script runs in a content world of its own
///   (`WKContentWorld.defaultClient`), not in the page's: it shares the
///   DOM, but its functions and message handlers are not reachable from
///   the document's world even if content JavaScript were ever on.
/// - The context menu keeps Copy and Copy Link only (`willOpenMenu`):
///   Reload, Back, Open in New Window and the rest are gone.
@MainActor
final class MessageWebView: NSView {
    /// Called with a link the user activated, an http(s) or mailto target
    /// that `allowedLink` accepted. The view itself never follows one. nil
    /// ignores clicks.
    var onLink: (@MainActor (ActivatedLink) -> Void)?

    /// Called when a body handed to `load(body:)` was dropped because the
    /// content rule list could not be installed (`ContentRules`): nothing
    /// is on display, and the next `load` tries again.
    var onUnavailable: (@MainActor () -> Void)?

    private let web: ViewerWebView
    private let handler: PartSchemeHandler
    private let messages = ViewerMessageProxy()
    private let status = NSTextField(labelWithString: "")
    private let statusBox = NSView()
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "htmlview")

    /// The body on display, so a re-render of the same message does not
    /// reload the document (and drop its scroll position) for nothing.
    private var loadedBody: String?
    /// The web content process went away: the next load must happen even
    /// for the body already on display.
    private var needsReload = false
    /// The content rule list is installed; until then a body waits in
    /// `pendingBody` while `rulesCompiling` says a compile is under way.
    private var rulesReady = false
    private var rulesCompiling = false
    private var pendingBody: String?

    static let hoverHandlerName = "hover"
    static let linkHandlerName = "link"
    static let statusMaxChars = 512

    /// The content rule list: everything blocked, then the application's
    /// own scheme, the daemon's inlined pictures and the document itself
    /// allowed again. Bump the identifier with the rules: the store keeps
    /// the compiled list by it.
    static let ruleListIdentifier = "io.github.schotek.Malachi.viewer.1"
    static let ruleListJSON = """
    [
      {"trigger": {"url-filter": ".*"}, "action": {"type": "block"}},
      {"trigger": {"url-filter": "^malachi-cid:"}, "action": {"type": "ignore-previous-rules"}},
      {"trigger": {"url-filter": "^data:"}, "action": {"type": "ignore-previous-rules"}},
      {"trigger": {"url-filter": "^about:blank$"}, "action": {"type": "ignore-previous-rules"}}
    ]
    """

    init(cache: MessageCache, zoom: Int) {
        handler = PartSchemeHandler(cache: cache)
        let config = Self.makeConfiguration(handler: handler, messages: messages)
        web = ViewerWebView(frame: .zero, configuration: config)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        messages.target = self

        web.translatesAutoresizingMaskIntoConstraints = false
        web.navigationDelegate = self
        web.uiDelegate = self
        web.allowsBackForwardNavigationGestures = false
        web.allowsLinkPreview = false
        web.allowsMagnification = false
        web.setAccessibilityLabel(L10n.T("Message"))
        addSubview(web)

        // The link under the pointer, browser-style, in the bottom-left
        // corner: plain text, the middle elided.
        status.font = Typo.caption
        status.textColor = .labelColor
        status.lineBreakMode = .byTruncatingMiddle
        status.maximumNumberOfLines = 1
        status.isSelectable = false
        // Below the split view's holding priorities (250–260): a long
        // target is cut in the middle, it never widens the pane.
        status.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(240), for: .horizontal)
        status.setContentHuggingPriority(.defaultLow, for: .horizontal)
        status.translatesAutoresizingMaskIntoConstraints = false
        statusBox.translatesAutoresizingMaskIntoConstraints = false
        statusBox.wantsLayer = true
        statusBox.layer?.backgroundColor = NSColor.windowBackgroundColor.withAlphaComponent(0.92).cgColor
        statusBox.layer?.cornerRadius = 4
        statusBox.layer?.borderWidth = 1
        statusBox.layer?.borderColor = NSColor.separatorColor.cgColor
        statusBox.isHidden = true
        statusBox.addSubview(status)
        addSubview(statusBox)

        NSLayoutConstraint.activate([
            web.topAnchor.constraint(equalTo: topAnchor),
            web.bottomAnchor.constraint(equalTo: bottomAnchor),
            web.leadingAnchor.constraint(equalTo: leadingAnchor),
            web.trailingAnchor.constraint(equalTo: trailingAnchor),
            statusBox.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
            statusBox.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -6),
            statusBox.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor, multiplier: 0.8),
            status.leadingAnchor.constraint(equalTo: statusBox.leadingAnchor, constant: 6),
            statusBox.trailingAnchor.constraint(equalTo: status.trailingAnchor, constant: 6),
            status.topAnchor.constraint(equalTo: statusBox.topAnchor, constant: 2),
            statusBox.bottomAnchor.constraint(equalTo: status.bottomAnchor, constant: 2),
        ])
        setZoom(zoom)
        ensureRules()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
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

    /// Installs the rule list and loads the body that waited for it. When
    /// no store could compile the list (`ContentRules` has logged the
    /// fault), the body is dropped and `onUnavailable` says so: the CSP,
    /// the proxy and the navigation policy are not enough on their own.
    private func installRules(_ list: WKContentRuleList?) {
        rulesCompiling = false
        guard let list else {
            pendingBody = nil
            onUnavailable?()
            return
        }
        web.configuration.userContentController.add(list)
        rulesReady = true
        if let body = pendingBody {
            pendingBody = nil
            load(body: body)
        }
    }

    /// The configuration of html_view.blp: no content JavaScript, no
    /// storage, no windows, our scheme handler, the view's script in its
    /// own content world, and a proxy nothing answers on for whatever the
    /// CSP might let through.
    private static func makeConfiguration(handler: PartSchemeHandler, messages: ViewerMessageProxy) -> WKWebViewConfiguration {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.defaultWebpagePreferences.allowsContentJavaScript = false
        config.preferences.javaScriptCanOpenWindowsAutomatically = false
        config.preferences.isFraudulentWebsiteWarningEnabled = false
        config.preferences.isElementFullscreenEnabled = false
        config.mediaTypesRequiringUserActionForPlayback = .all
        config.allowsAirPlayForMediaPlayback = false
        config.suppressesIncrementalRendering = false
        config.setURLSchemeHandler(handler, forURLScheme: partScheme)
        let controller = config.userContentController
        controller.addUserScript(
            WKUserScript(source: viewerScript, injectionTime: .atDocumentEnd, forMainFrameOnly: true, in: .defaultClient))
        controller.add(messages, contentWorld: .defaultClient, name: hoverHandlerName)
        controller.add(messages, contentWorld: .defaultClient, name: linkHandlerName)
        // Nothing the document asks for may leave the process: the CSP
        // stops it first, this stops whatever might slip past the CSP.
        config.websiteDataStore.proxyConfigurations = [
            ProxyConfiguration(httpCONNECTProxy: .hostPort(host: "127.0.0.1", port: 1)),
        ]
        return config
    }

    /// The view's own script, in its own content world; the document's
    /// scripts never run. It reports the link under the pointer
    /// (`mouseover`/`mouseout` on `a[href]`) to the `hover` handler, and
    /// takes every click on a link away from WebKit, reporting the `href`
    /// attribute as written and the URL it resolves to, to the `link`
    /// handler. A middle click, which would open a window, is cancelled
    /// too.
    private static let viewerScript = """
    (function () {
        function post(name, m) {
            try { window.webkit.messageHandlers[name].postMessage(m); } catch (e) {}
        }
        function anchor(t) {
            return (t && t.closest) ? t.closest('a[href]') : null;
        }
        document.addEventListener('mouseover', function (e) {
            var a = anchor(e.target);
            if (a) { post('\(hoverHandlerName)', String(a.href)); }
        }, true);
        document.addEventListener('mouseout', function (e) {
            var a = anchor(e.target);
            if (a && anchor(e.relatedTarget) !== a) { post('\(hoverHandlerName)', ''); }
        }, true);
        document.addEventListener('click', function (e) {
            var a = anchor(e.target);
            if (!a) { return; }
            e.preventDefault();
            e.stopPropagation();
            post('\(linkHandlerName)', {raw: String(a.getAttribute('href')), resolved: String(a.href)});
        }, true);
        document.addEventListener('auxclick', function (e) {
            if (anchor(e.target)) { e.preventDefault(); e.stopPropagation(); }
        }, true);
    })();
    """

    // MARK: Content

    /// Shows a sanitised body fragment (htmlview `Load`). The fragment is
    /// the sanitiser's output and nothing else may ever be passed here.
    /// Until the rule list is installed the body waits; should the list
    /// fail to compile, it is dropped and `onUnavailable` is called.
    func load(body: String) {
        showStatus("")
        guard rulesReady else {
            pendingBody = body
            ensureRules()
            return
        }
        if !needsReload, loadedBody == body {
            return
        }
        needsReload = false
        loadedBody = body
        web.loadHTMLString(viewerDocument(body: body), baseURL: nil)
    }

    /// Drops the current document (and its pictures). Without the rule
    /// list nothing was ever loaded; a body still waiting is forgotten.
    func clear() {
        guard rulesReady else {
            showStatus("")
            pendingBody = nil
            return
        }
        load(body: "")
    }

    /// Scales the content; `percent` is the text-zoom setting.
    func setZoom(_ percent: Int) {
        let p = percent <= 0 ? 100 : percent
        web.pageZoom = CGFloat(p) / 100
    }

    /// One message from the view's script, by handler name. Anything
    /// that is not of the expected shape is dropped.
    fileprivate func scriptMessage(_ name: String, _ body: Any) {
        switch name {
        case Self.hoverHandlerName:
            guard let href = body as? String else { return }
            hover(href)
        case Self.linkHandlerName:
            guard let dict = body as? [String: Any], let resolved = dict["resolved"] as? String else { return }
            linkActivated(ActivatedLink(raw: dict["raw"] as? String, resolved: resolved))
        default:
            log.debug("script message from an unknown handler")
        }
    }

    /// The link under the pointer, from the script; "" hides the status.
    /// Plain text, capped: it is content of the mail.
    private func hover(_ href: String) {
        showStatus(String(href.prefix(Self.statusMaxChars)))
        web.hoveredHref = href.isEmpty ? nil : href
    }

    private func showStatus(_ text: String) {
        status.stringValue = text
        statusBox.isHidden = text.isEmpty
    }

    /// A link the user activated: cancelled for the view, handed on when
    /// it is one the application opens.
    private func linkActivated(_ link: ActivatedLink) {
        guard allowedLink(link.href) else {
            log.debug("navigation refused")
            return
        }
        onLink?(link)
    }

    fileprivate func processTerminated() {
        log.warning("web content process terminated")
        needsReload = true
    }
}

// The WebKit delegate protocols are main-actor isolated in the Swift
// overlay: the methods below are ordinary main-actor methods.

extension MessageWebView: WKNavigationDelegate {
    /// Allows only the initial document load (the main frame's
    /// `about:blank`, from `loadHTMLString`). A link activation that the
    /// script did not already take (`viewerScript` cancels clicks before
    /// they navigate) is refused for the view and handed to `onLink` with
    /// the resolved URL alone; everything else is refused outright.
    /// Resource loads are not navigations: the CSP and the rule list have
    /// already limited them to the application's own pictures.
    func webView(
        _ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void
    ) {
        let url = navigationAction.request.url
        let initial = navigationAction.navigationType == .other
            && navigationAction.targetFrame?.isMainFrame == true
            && url?.absoluteString == "about:blank"
        if initial {
            decisionHandler(.allow)
            return
        }
        decisionHandler(.cancel)
        if navigationAction.navigationType == .linkActivated, let url {
            linkActivated(ActivatedLink(raw: nil, resolved: url.absoluteString))
        } else {
            log.debug("navigation refused")
        }
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        processTerminated()
    }
}

extension MessageWebView: WKUIDelegate {
    /// Nothing opens a new window (a link with `target`, which the
    /// sanitiser removes anyway, or a window.open, which cannot run).
    func webView(
        _ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        nil
    }
}

/// The WKWebView itself: the context menu is trimmed here because
/// `willOpenMenu` is a view method, and drops are refused.
@MainActor
private final class ViewerWebView: WKWebView {
    /// The link under the pointer, for the Copy Link fallback.
    var hoveredHref: String?

    /// The identifiers of WebKit's own Copy and Copy Link items.
    private static let keptItems: Set<String> = ["WKMenuItemIdentifierCopy", "WKMenuItemIdentifierCopyLink"]

    /// Keeps only what makes sense for inert content: copying the
    /// selection and the link under the pointer (htmlview `contextMenu`).
    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        super.willOpenMenu(menu, with: event)
        let kept = menu.items.filter { Self.keptItems.contains($0.identifier?.rawValue ?? "") }
        menu.removeAllItems()
        if !kept.isEmpty {
            for item in kept {
                menu.addItem(item)
            }
            return
        }
        // WebKit's items were not found by their identifiers: our own.
        menu.addItem(NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "")) // macOS-only string
        if let href = hoveredHref, !href.isEmpty {
            let link = NSMenuItem(title: "Copy Link", action: #selector(copyLink(_:)), keyEquivalent: "") // macOS-only string
            link.target = self
            link.representedObject = href
            menu.addItem(link)
        }
    }

    @objc private func copyLink(_ sender: Any?) {
        guard let href = (sender as? NSMenuItem)?.representedObject as? String else { return }
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(href, forType: .string)
    }

    // A dropped file would otherwise be a navigation request.
    override func draggingEntered(_ sender: any NSDraggingInfo) -> NSDragOperation {
        []
    }

    override func performDragOperation(_ sender: any NSDraggingInfo) -> Bool {
        false
    }
}

/// The `hover` and `link` message handler. WebKit retains its handlers
/// strongly, so this small object stands between the configuration and
/// the view.
@MainActor
private final class ViewerMessageProxy: NSObject, WKScriptMessageHandler {
    weak var target: MessageWebView?

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.scriptMessage(message.name, message.body)
    }
}
