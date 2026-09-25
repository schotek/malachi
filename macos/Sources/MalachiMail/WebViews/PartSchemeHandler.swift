// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import MalachiCore
import WebKit

/// Serves the `malachi-cid:<accountId>/<messageId>/<partId>` pictures of a
/// rendered body through message.part (ui/internal/htmlview/scheme.go). It
/// is stateless: the URL names the message, so switching messages while
/// pictures still load cannot hand one message another one's part, and a
/// message cannot name another message's parts (the sanitiser rewrites
/// `cid:` only for the message's own parts). Only pictures are served,
/// never SVG (`isImageType`); anything else fails the load, which the
/// page's CSP has already limited to this scheme and `data:`.
///
/// WebKit calls the protocol on the main actor (the protocol is main-actor
/// isolated in the Swift overlay); the work runs as a main-actor task per
/// request and `stop` cancels it, so a task never answers a request WebKit
/// gave up on.
@MainActor
final class PartSchemeHandler: NSObject, WKURLSchemeHandler {
    enum PartError: Error {
        /// The URL is not `malachi-cid:<account>/<message>/<part>`.
        case badPath
        /// The part is not a picture (or is an SVG document).
        case notAnImage
    }

    private let cache: MessageCache
    private var tasks: [ObjectIdentifier: Task<Void, Never>] = [:]

    init(cache: MessageCache) {
        self.cache = cache
    }

    func webView(_ webView: WKWebView, start urlSchemeTask: any WKURLSchemeTask) {
        start(urlSchemeTask)
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: any WKURLSchemeTask) {
        stop(urlSchemeTask)
    }

    private func start(_ task: any WKURLSchemeTask) {
        let key = ObjectIdentifier(task)
        guard let url = task.request.url, let ref = Self.parse(url) else {
            task.didFailWithError(PartError.badPath)
            return
        }
        let cache = cache
        tasks[key] = Task { [weak self] in
            do {
                let part = try await cache.fetchPart(accountID: ref.accountID, messageID: ref.messageID, partID: ref.partID)
                // `stop` cancelled the task on the main actor before this
                // continuation ran: WebKit must not hear from it any more.
                guard !Task.isCancelled else { return }
                guard isImageType(part.contentType) else {
                    throw PartError.notAnImage
                }
                let response = URLResponse(
                    url: url, mimeType: Self.mediaType(part.contentType), expectedContentLength: part.data.count,
                    textEncodingName: nil)
                task.didReceive(response)
                task.didReceive(part.data)
                task.didFinish()
            } catch {
                if !Task.isCancelled {
                    task.didFailWithError(error)
                }
            }
            self?.tasks[key] = nil
        }
    }

    private func stop(_ task: any WKURLSchemeTask) {
        tasks.removeValue(forKey: ObjectIdentifier(task))?.cancel()
    }

    /// The account, message and part a `malachi-cid:` URL names, or nil
    /// when the URL is not of that shape (`parsePartPath` on the URL minus
    /// its scheme).
    static func parse(_ url: URL) -> (accountID: AccountID, messageID: MessageID, partID: String)? {
        let s = url.absoluteString
        let prefix = partScheme + ":"
        guard s.lowercased().hasPrefix(prefix) else { return nil }
        return parsePartPath(String(s.dropFirst(prefix.count)))
    }

    /// The media type without its parameters, for the response.
    static func mediaType(_ contentType: String) -> String {
        var ct = contentType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if let semicolon = ct.firstIndex(of: ";") {
            ct = String(ct[..<semicolon]).trimmingCharacters(in: .whitespacesAndNewlines)
        }
        return ct
    }
}
