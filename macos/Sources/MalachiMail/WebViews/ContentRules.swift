// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os
import WebKit

/// The content rule lists of the two web views (docs/security.md §3.2 and
/// §3.3), compiled once per process and shared by every view with the
/// same identifier. The list is what stops a load the CSP and the proxy
/// setting do not see (a `<link rel="preconnect">` opens a connection
/// without a request), so a view that cannot install it must not load a
/// document: the viewer shows the plain text instead, the editor refuses
/// the document. A compile that fails is therefore never remembered: the
/// next view asks again, first the default store, then a store of its own
/// in a temporary directory in case the default store's disk is the
/// problem, and a failure of both is a fault in the log.
@MainActor
enum ContentRules {
    /// Lists that compiled, by identifier.
    private static var compiled: [String: WKContentRuleList] = [:]
    /// Compiles under way, by identifier, so that two views created at once
    /// share one.
    private static var inFlight: [String: Task<WKContentRuleList?, Never>] = [:]
    private static let log = Logger(subsystem: "io.github.schotek.Malachi", category: "webview")

    /// The compiled list for `identifier`, or nil when no store could
    /// compile `json` this time.
    static func list(identifier: String, json: String) async -> WKContentRuleList? {
        if let list = compiled[identifier] {
            return list
        }
        if let task = inFlight[identifier] {
            return await task.value
        }
        let task = Task<WKContentRuleList?, Never> { @MainActor in
            await compile(identifier: identifier, json: json)
        }
        inFlight[identifier] = task
        let list = await task.value
        inFlight[identifier] = nil
        if let list {
            compiled[identifier] = list
        }
        return list
    }

    private static func compile(identifier: String, json: String) async -> WKContentRuleList? {
        if let list = await compile(in: WKContentRuleListStore.default(), identifier: identifier, json: json) {
            return list
        }
        // The default store keeps its files under the application's
        // caches; when that fails, a store of this process's own.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(
            "io.github.schotek.Malachi.rules-\(ProcessInfo.processInfo.processIdentifier)", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        if let list = await compile(in: WKContentRuleListStore(url: dir), identifier: identifier, json: json) {
            log.error("the content rule list \(identifier, privacy: .public) compiled only in a temporary store")
            return list
        }
        log.fault("the content rule list \(identifier, privacy: .public) did not compile in any store; no document is loaded")
        return nil
    }

    /// One compile in `store`; the result of a failure is nil, its error
    /// logged by domain and code.
    private static func compile(in store: WKContentRuleListStore, identifier: String, json: String) async -> WKContentRuleList? {
        await withCheckedContinuation { (cont: CheckedContinuation<WKContentRuleList?, Never>) in
            store.compileContentRuleList(forIdentifier: identifier, encodedContentRuleList: json) { list, error in
                if let error {
                    let ns = error as NSError
                    log.error("compiling the content rule list \(identifier, privacy: .public): \(ns.domain, privacy: .public) \(ns.code, privacy: .public)")
                }
                cont.resume(returning: list)
            }
        }
    }
}
