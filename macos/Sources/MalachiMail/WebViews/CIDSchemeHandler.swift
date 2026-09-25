// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import MalachiCore
import WebKit

/// Serves the `cid:<id>` pictures of a draft being composed
/// (ui/internal/editor/cid.go `serveCID`): files the compose window picked
/// and the backend's copies of a quoted original's pictures, which reach
/// the window through attachment.get. Only an id registered in the
/// `CIDRegistry` resolves, so a pasted `<img src="cid:../../etc/passwd">`
/// fails its load; anything served passes `checkInline` (a non-empty
/// picture, never SVG, within `maxCIDBytes`). The message viewer uses a
/// different scheme (`malachi-cid:`), so a displayed message can never
/// address compose attachments, and a composed draft cannot name a
/// received message's parts.
///
/// The handler is stateless apart from the fetches in flight: the URL
/// names the id, the registry holds what it means. WebKit calls the
/// protocol on the main actor (the protocol is main-actor isolated in the
/// Swift overlay); a fetch runs as a main-actor task per request and
/// `stop` cancels it, so a task never answers a request WebKit gave up on.
@MainActor
final class CIDSchemeHandler: NSObject, WKURLSchemeHandler {
    /// The scheme this handler is registered for.
    static let scheme = "cid"

    enum CIDError: Error {
        /// The id is not registered ("unknown inline image").
        case unknown
        /// The registered file is missing, not a regular file or over the
        /// cap ("inline image unavailable").
        case unavailable
        /// The fetcher did not answer within `CIDRegistry.fetchTimeout`.
        case timeout
    }

    private let registry: CIDRegistry
    private var tasks: [ObjectIdentifier: Task<Void, Never>] = [:]

    init(registry: CIDRegistry = .shared) {
        self.registry = registry
    }

    func webView(_ webView: WKWebView, start urlSchemeTask: any WKURLSchemeTask) {
        start(urlSchemeTask)
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: any WKURLSchemeTask) {
        stop(urlSchemeTask)
    }

    // MARK: Requests

    private func start(_ task: any WKURLSchemeTask) {
        guard let url = task.request.url, let entry = registry.lookup(Self.id(of: url)) else {
            task.didFailWithError(CIDError.unknown)
            return
        }
        switch entry {
        case let .file(path, contentType):
            // A local file at once, as GTK does; the registered type is
            // what attachment.import sniffed, and `checkInline` gates it
            // like a fetched one.
            do {
                let data = try Self.readFile(path)
                try checkInline(data: data, contentType: contentType)
                Self.respond(task, url: url, data: data, contentType: contentType)
            } catch {
                task.didFailWithError(error)
            }
        case let .fetcher(fetch):
            let key = ObjectIdentifier(task)
            tasks[key] = Task { [weak self] in
                do {
                    let image = try await Self.fetch(fetch, within: CIDRegistry.fetchTimeout)
                    // `stop` cancelled the task on the main actor before
                    // this continuation ran: WebKit must not hear from it
                    // any more.
                    guard !Task.isCancelled else { return }
                    try checkInline(data: image.data, contentType: image.contentType)
                    Self.respond(task, url: url, data: image.data, contentType: image.contentType)
                } catch {
                    if !Task.isCancelled {
                        task.didFailWithError(error)
                    }
                }
                self?.tasks[key] = nil
            }
        }
    }

    private func stop(_ task: any WKURLSchemeTask) {
        tasks.removeValue(forKey: ObjectIdentifier(task))?.cancel()
    }

    /// Answers with the picture: its media type, its length, and no
    /// caching (the id may be re-registered to a different file).
    private static func respond(_ task: any WKURLSchemeTask, url: URL, data: Data, contentType: String) {
        let type = mediaType(contentType)
        let headers = [
            "Content-Type": type,
            "Content-Length": String(data.count),
            "Cache-Control": "no-store",
        ]
        let response: URLResponse = HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: headers)
            ?? URLResponse(url: url, mimeType: type, expectedContentLength: data.count, textEncodingName: nil)
        task.didReceive(response)
        task.didReceive(data)
        task.didFinish()
    }

    // MARK: Pure helpers

    /// The id a `cid:` URL names, as WebKitGTK's `req.Path()` gives it to
    /// the GTK editor: the URL's path component as written (WebKit keeps
    /// its percent-encoding; the query and fragment are not part of it),
    /// or, when there is none, the URL minus its `cid:` prefix. Ids are
    /// matched exactly against what was registered, nothing is decoded.
    static func id(of url: URL) -> String {
        if let path = URLComponents(url: url, resolvingAgainstBaseURL: false)?.percentEncodedPath, !path.isEmpty {
            return path
        }
        let s = url.absoluteString
        let prefix = scheme + ":"
        if s.lowercased().hasPrefix(prefix) {
            return String(s.dropFirst(prefix.count))
        }
        return s
    }

    /// The media type without its parameters, lower-cased, for the
    /// response header.
    static func mediaType(_ contentType: String) -> String {
        var ct = contentType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if let semicolon = ct.firstIndex(of: ";") {
            ct = String(ct[..<semicolon]).trimmingCharacters(in: .whitespacesAndNewlines)
        }
        return ct
    }

    /// A registered file's bytes: it must exist, be a regular file (after
    /// symbolic links, as os.Stat sees it) and fit the cap before it is
    /// read at all.
    nonisolated static func readFile(_ path: String) throws -> Data {
        var st = stat()
        guard stat(path, &st) == 0, (st.st_mode & S_IFMT) == S_IFREG, st.st_size <= off_t(maxCIDBytes) else {
            throw CIDError.unavailable
        }
        return try Data(contentsOf: URL(fileURLWithPath: path), options: [.uncached])
    }

    /// One fetcher call bounded by `timeout` (editor.fetchTimeout): the
    /// daemon reads a file of the attachment store, which is quick, but the
    /// request must not hang the view's image forever when the daemon is
    /// gone. Whichever finishes first wins; the other is cancelled.
    nonisolated static func fetch(
        _ fetch: @escaping CIDFetcher, within timeout: Duration
    ) async throws -> (data: Data, contentType: String) {
        try await withThrowingTaskGroup(of: (data: Data, contentType: String).self) { group in
            group.addTask { try await fetch() }
            group.addTask {
                try await Task.sleep(for: timeout)
                throw CIDError.timeout
            }
            guard let first = try await group.next() else {
                throw CIDError.timeout
            }
            group.cancelAll()
            return first
        }
    }
}
