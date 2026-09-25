// swift-tools-version: 6.0
// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The macOS client of Malachi Mail: a Swift/AppKit application over the
// malachid JSON-RPC socket, the third client of the daemon next to the GTK
// UI and the MCP bridge. MalachiCore holds everything that does not need
// AppKit (transport, the typed API, the mail models, controllers, i18n,
// settings) so that it can be tested with `swift test`; MalachiMail is the
// AppKit layer; MalachiKeychain is the small helper the daemon runs as its
// keyring (MALACHI_KEYRING=helper). No SwiftPM resources on purpose: the
// hand-assembled .app carries them in Contents/Resources (see Makefile).
import PackageDescription

let package = Package(
    name: "MalachiMail",
    defaultLocalization: "en",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "MalachiMail", targets: ["MalachiMail"]),
        .executable(name: "malachi-keychain", targets: ["MalachiKeychain"]),
    ],
    targets: [
        .target(name: "MalachiCore"),
        .executableTarget(name: "MalachiMail", dependencies: ["MalachiCore"]),
        .executableTarget(name: "MalachiKeychain"),
        .testTarget(name: "MalachiCoreTests", dependencies: ["MalachiCore"]),
        .testTarget(name: "MalachiKeychainTests", dependencies: ["MalachiKeychain"]),
    ]
)
