// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os
import WebKit

/// The rich-text editor of the compose window (ui/internal/editor
/// `Editor`): a `ComposeWebView` showing a contenteditable document,
/// driven through the bridge script's `window.malachi` functions and
/// observed through its messages. It knows nothing about mail. Callers
/// hand it body HTML that is already safe for the page (escaped quotes,
/// backend-sanitised drafts) and read the body's HTML and text back; the
/// backend sanitises whatever is sent.
///
/// The bridge posts `ready` once per document, `changed{seq, html, text}`
/// after edits (debounced) and on `flush`, and `state` when the formatting
/// at the caret changed. `flush(_:)` evaluates `window.malachi.flush()`,
/// which posts a fresh `changed` and returns its `seq`; the callback runs
/// once the `changed` with that `seq` (or a later one) has been seen, so a
/// save reads content at least as new as the moment it asked. GTK's
/// fire-and-forget eval cannot see the returned `seq` and settles for the
/// next `changed`; the `seq` the bridge returns exists for this.
@MainActor
final class ComposeEditorView: NSView, EditorView {
    var view: NSView { self }
    private(set) var isReady = false

    var onReady: (@MainActor () -> Void)?
    var onChanged: (@MainActor () -> Void)?
    var onState: (@MainActor (EditorState) -> Void)?
    var onDropFiles: (@MainActor ([URL]) -> Void)? {
        get { web.onDropFiles }
        set { web.onDropFiles = newValue }
    }
    var onCrashed: (@MainActor () -> Void)?

    private let web: ComposeWebView
    private let registry: CIDRegistry
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "editor")

    /// The body's last known innerHTML (or what was loaded) and innerText.
    private var lastHTML = ""
    private var lastText = ""
    /// The `seq` of the last `changed` of the current document.
    private var lastSeq = 0
    /// The view refused a document for want of its content rule list and
    /// `onCrashed` has said so; nothing more is said until a document
    /// loads, so the compose window's reload on a failure cannot loop.
    private var reportedUnavailable = false

    /// A `flush` caller: `seq` is what the page's `flush()` returned, nil
    /// until the evaluation came back.
    private struct Waiter {
        let id: Int
        var seq: Int?
        let done: @MainActor () -> Void
    }

    private var waiters: [Waiter] = []
    private var nextWaiterID = 0

    /// An editor over `registry` (the process-wide registry in the
    /// application; the `cid:` handler of the view resolves in the same one).
    init(registry: CIDRegistry = .shared) {
        self.registry = registry
        web = ComposeWebView(registry: registry)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true

        web.translatesAutoresizingMaskIntoConstraints = false
        web.onBridgeMessage = { [weak self] body in
            self?.receive(body)
        }
        web.onCrashed = { [weak self] in
            self?.crashed()
        }
        web.onUnavailable = { [weak self] in
            self?.unavailable()
        }
        addSubview(web)
        NSLayoutConstraint.activate([
            web.topAnchor.constraint(equalTo: topAnchor),
            web.bottomAnchor.constraint(equalTo: bottomAnchor),
            web.leadingAnchor.constraint(equalTo: leadingAnchor),
            web.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Appearance

    /// The slot shows the text background until the page paints.
    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.textBackgroundColor.cgColor
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }

    // MARK: EditorView

    /// editor.Load: replaces the document with `bodyHTML` (already safe for
    /// the page). Callers waiting on a `flush` of the old document are
    /// released, as they would be were the page not ready.
    func load(bodyHTML: String) {
        isReady = false
        lastHTML = bodyHTML
        lastText = ""
        lastSeq = 0
        drainWaiters()
        web.loadDocument(editorDocument(body: bodyHTML))
    }

    func html() -> String {
        lastHTML
    }

    func text() -> String {
        lastText
    }

    /// editor.Flush: asks the page for its current content and calls
    /// `done` once the `changed` message it produced has arrived, or at
    /// once when the bridge is not running. Should the evaluation fail
    /// (the page went away), `done` runs too: a save must never hang.
    func flush(_ done: @escaping @MainActor () -> Void) {
        guard isReady else {
            done()
            return
        }
        let id = nextWaiterID
        nextWaiterID += 1
        waiters.append(Waiter(id: id, seq: nil, done: done))
        web.run("window.malachi.flush()") { [weak self] result, error in
            self?.flushed(id, result: result, error: error)
        }
    }

    /// editor.Exec: runs an editing command (`bold`, `formatBlock` with
    /// `h1`, `createLink`, `foreColor`, `insertImage`, `justifyLeft`,
    /// `insertUnorderedList`, `removeFormat`, `unlink`, …) on the current
    /// selection through the bridge's `exec`. Ignored until the document
    /// is ready; an empty argument is no argument, as in GTK.
    func exec(_ command: String, _ argument: String?) {
        guard isReady else { return }
        let arg: String
        if let argument, !argument.isEmpty {
            arg = jsString(argument)
        } else {
            arg = "null"
        }
        web.run("window.malachi.exec(\(jsString(command)), \(arg))")
    }

    /// editor.FocusStart: focuses the view and, once the bridge runs, puts
    /// the caret at the start of the body.
    func focusStart() {
        window?.makeFirstResponder(web)
        if isReady {
            web.run("window.malachi.focusStart()")
        }
    }

    func registerCID(_ id: String, path: String, contentType: String) {
        registry.register(id, path: path, contentType: contentType)
    }

    func registerCIDFetcher(_ id: String, fetch: @escaping CIDFetcher) {
        registry.registerFetcher(id, fetch: fetch)
    }

    func unregisterCID(_ id: String) {
        registry.unregister(id)
    }

    // MARK: Bridge

    /// editor.onMessage: one message the bridge posted. Anything that is
    /// not a string holding one of the three message shapes is dropped;
    /// the content is never logged.
    private func receive(_ body: Any) {
        guard let raw = body as? String, let message = try? BridgeMessage.decode(raw) else {
            log.debug("bad bridge message")
            return
        }
        switch message.type {
        case "ready":
            isReady = true
            reportedUnavailable = false
            log.debug("bridge ready")
            onReady?()
        case "changed":
            lastHTML = message.html
            lastText = message.text
            lastSeq = message.seq
            log.debug("content changed seq=\(message.seq, privacy: .public) bytes=\(message.html.utf8.count, privacy: .public)")
            resolveWaiters()
            onChanged?()
        case "state":
            onState?(message.state)
        default:
            log.debug("bad bridge message")
        }
    }

    /// The page's `flush()` returned (the `seq` of the `changed` it
    /// posted), or failed.
    private func flushed(_ id: Int, result: Any?, error: (any Error)?) {
        guard let index = waiters.firstIndex(where: { $0.id == id }) else {
            return
        }
        guard error == nil, let seq = (result as? NSNumber)?.intValue else {
            let waiter = waiters.remove(at: index)
            log.debug("flush failed")
            waiter.done()
            return
        }
        waiters[index].seq = seq
        resolveWaiters()
    }

    /// Calls every waiter whose `changed` has arrived, in order.
    private func resolveWaiters() {
        let due = waiters.filter { waiter in
            guard let seq = waiter.seq else { return false }
            return seq <= lastSeq
        }
        guard !due.isEmpty else { return }
        waiters.removeAll { waiter in due.contains { $0.id == waiter.id } }
        for waiter in due {
            waiter.done()
        }
    }

    /// Releases every waiter: the document they asked about is gone.
    private func drainWaiters() {
        let all = waiters
        waiters = []
        for waiter in all {
            waiter.done()
        }
    }

    /// The web content process died; the view is blank until `load` is
    /// called again (the compose window reloads `html()`).
    private func crashed() {
        isReady = false
        drainWaiters()
        onCrashed?()
    }

    /// The view could not install its content rule list and dropped the
    /// document (docs/security.md §3.3: without the list nothing loads).
    /// Reported once as a failure of the editor, which the compose window
    /// answers with its toast and a reload of `html()`; a reload that
    /// fails the same way stays quiet, and the window keeps the text it
    /// loaded (`html()`), so a save loses nothing.
    private func unavailable() {
        isReady = false
        drainWaiters()
        guard !reportedUnavailable else {
            log.debug("document refused again without the rule list")
            return
        }
        reportedUnavailable = true
        onCrashed?()
    }
}
