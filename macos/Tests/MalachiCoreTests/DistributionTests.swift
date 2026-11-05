// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The Info.plist switch between the DMG and the App Store build, and the
/// socket it moves into the app group (docs/plans/mac-app-store.md §3.2).
@Suite struct DistributionTests {
    @Test func infoPlistValueDecidesAndAnythingElseIsDirect() {
        #expect(Distribution.from("appstore") == .appStore)
        #expect(Distribution.from("direct") == .direct)
        #expect(Distribution.from(nil) == .direct)
        #expect(Distribution.from("") == .direct)
        #expect(Distribution.from("AppStore") == .direct)
    }

    @Test func onlyTheAppStoreBuildHasAGroupContainer() {
        #expect(Distribution.direct.groupContainer == nil)
    }

    @Test func theGroupContainerHoldsTheSocket() {
        let group = URL(fileURLWithPath: "/Users/u/Library/Group Containers/\(Distribution.appGroup)", isDirectory: true)
        let p = Paths.resolve(environment: ["HOME": "/Users/u"], executable: nil, groupContainer: group)
        #expect(p.socket == "/Users/u/Library/Group Containers/PG9F7XU6X8.io.github.schotek.Malachi/rpc.sock")
        #expect(p.socket.utf8.count <= UnixSocketProbe.maxPathBytes)
    }

    @Test func malachiSocketStillWinsInTheAppStoreBuild() {
        let group = URL(fileURLWithPath: "/g", isDirectory: true)
        let p = Paths.resolve(environment: ["HOME": "/Users/u", "MALACHI_SOCKET": "/s.sock"], executable: nil, groupContainer: group)
        #expect(p.socket == "/s.sock")
    }

    /// The DMG's bridge beside the app's executable, the App Store build's
    /// in its helper bundle.
    @Test func theBridgeIsBesideTheAppOrInItsHelperBundle() throws {
        let fm = FileManager.default
        let root = fm.temporaryDirectory.appendingPathComponent("bridge-test-" + UUID().uuidString, isDirectory: true)
        defer { try? fm.removeItem(at: root) }
        let macOS = root.appendingPathComponent("Contents/MacOS", isDirectory: true)
        try fm.createDirectory(at: macOS, withIntermediateDirectories: true)
        let exe = macOS.appendingPathComponent("MalachiMail")
        func make(_ url: URL) throws {
            try fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            fm.createFile(atPath: url.path, contents: Data(), attributes: [.posixPermissions: 0o755])
        }
        #expect(Paths.bridgeExecutable(beside: exe) == nil)
        let helper = root.appendingPathComponent("Contents/Helpers/Malachi Mail MCP.app/Contents/MacOS/malachi-mcp")
        try make(helper)
        #expect(Paths.bridgeExecutable(beside: exe)?.path == helper.path)
        let beside = macOS.appendingPathComponent("malachi-mcp")
        try make(beside)
        #expect(Paths.bridgeExecutable(beside: exe)?.path == beside.path)
        #expect(Paths.bridgeExecutable(beside: nil) == nil)
    }

    /// The longest user name whose socket in the group still fits: the
    /// limit `Paths.resolve` documents.
    @Test func userNamesOfUpTo25BytesFit() {
        func socket(user: String) -> String {
            let group = URL(fileURLWithPath: "/Users/\(user)/Library/Group Containers/\(Distribution.appGroup)", isDirectory: true)
            return Paths.resolve(environment: ["HOME": "/Users/\(user)"], executable: nil, groupContainer: group).socket
        }
        #expect(socket(user: String(repeating: "a", count: 25)).utf8.count == UnixSocketProbe.maxPathBytes)
        #expect(socket(user: String(repeating: "a", count: 26)).utf8.count > UnixSocketProbe.maxPathBytes)
    }
}
