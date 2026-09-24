// swift-tools-version: 6.0
// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The macOS client of Malachi Mail: a Swift/AppKit application over the
// malachid JSON-RPC socket, the third client of the daemon next to the GTK
// UI and the MCP bridge. MalachiCore holds everything that does not need
// AppKit (transport, daemon supervision, paths) so that it can be tested
// with `swift test`; MalachiMail is the thin AppKit executable.
import PackageDescription

let package = Package(
    name: "MalachiMail",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "MalachiMail", targets: ["MalachiMail"]),
    ],
    targets: [
        .target(name: "MalachiCore"),
        .executableTarget(name: "MalachiMail", dependencies: ["MalachiCore"]),
        .testTarget(name: "MalachiCoreTests", dependencies: ["MalachiCore"]),
    ]
)
