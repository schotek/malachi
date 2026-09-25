// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/editor/cid_test.go.
struct CIDRegistryTests {
    /// The registry resolves what was registered, by either route, and
    /// nothing else; forgetting an id makes it unknown again.
    @Test @MainActor func registry() async throws {
        let registry = CIDRegistry()
        registry.register("file@x", path: "/tmp/pic.png", contentType: "image/png")
        registry.registerFetcher("fetched@x") { (Data([1]), "image/png") }

        guard case .file(let path, let contentType)? = registry.lookup("file@x") else {
            Issue.record("file entry missing or of the wrong kind")
            return
        }
        #expect(path == "/tmp/pic.png")
        #expect(contentType == "image/png")

        guard case .fetcher(let fetch)? = registry.lookup("fetched@x") else {
            Issue.record("fetcher entry missing or of the wrong kind")
            return
        }
        let fetched = try await fetch()
        #expect(fetched.data == Data([1]))
        #expect(fetched.contentType == "image/png")

        for id in ["", "other@x", "../file@x", "FILE@x"] {
            #expect(!registry.isRegistered(id), "\(id) resolves")
        }
        #expect(registry.isRegistered("file@x"))
        registry.unregister("fetched@x")
        #expect(!registry.isRegistered("fetched@x"))
        #expect(registry.lookup("fetched@x") == nil)
        // Re-registering replaces the entry.
        registry.registerFetcher("file@x") { (Data([2]), "image/gif") }
        guard case .fetcher? = registry.lookup("file@x") else {
            Issue.record("re-registration kept the old entry")
            return
        }
        // Unregistering an unknown id is not an error.
        registry.unregister("never@x")
        #expect(CIDRegistry.fetchTimeout == .seconds(60))
        #expect(maxCIDBytes == 25 << 20)
    }

    /// Only a non-empty picture within the cap is served; SVG never.
    @Test func checkInlineGate() throws {
        let png = Data([0x89, 0x50, 0x4E, 0x47]) // "\x89PNG"
        try checkInline(data: png, contentType: "image/png")
        try checkInline(data: png, contentType: " Image/JPEG; charset=binary ")

        let bad: [(String, Data, String, InlineImageError)] = [
            ("empty", Data(), "image/png", .empty),
            ("svg", Data("<svg/>".utf8), "image/svg+xml", .notAPicture),
            ("svg with parameters", Data("<svg/>".utf8), "IMAGE/SVG+XML; charset=utf-8", .notAPicture),
            ("html", Data("<p>".utf8), "text/html", .notAPicture),
            ("no type", png, "", .notAPicture),
            ("over cap", Data(count: maxCIDBytes + 1), "image/png", .tooBig),
            ("imageless", png, "imagex/png", .notAPicture),
        ]
        for (name, data, contentType, want) in bad {
            #expect(throws: want, "\(name)") {
                try checkInline(data: data, contentType: contentType)
            }
            #expect(want.description.contains("inline image"))
        }
        try checkInline(data: Data(count: maxCIDBytes), contentType: "image/png")
    }
}
