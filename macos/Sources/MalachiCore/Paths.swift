// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Where the daemon's socket, its data and the MCP bridge are on macOS.
///
/// The socket keeps the daemon's own default so that `malachi-mcp`, the
/// repository's `.mcp.json` and `make run-backend` agree with the app
/// without any environment variable; config and store move to the place a
/// macOS user expects and are handed to the daemon as flags.
public struct Paths: Sendable {
    /// `MALACHI_SOCKET`, else `$XDG_RUNTIME_DIR/malachi/rpc.sock`, else
    /// `${XDG_CACHE_HOME:-~/.cache}/malachi/run/rpc.sock` — exactly the
    /// daemon's, the GTK client's and the MCP bridge's resolution.
    public let socket: String
    /// `~/Library/Application Support/Malachi Mail`
    public let dataDir: URL
    /// `Contents/MacOS/malachi-mcp` when bundled, nil otherwise.
    public let mcpBridge: URL?

    public var config: String { dataDir.appendingPathComponent("config.toml").path }
    public var store: String { dataDir.appendingPathComponent("store.db").path }

    public init(socket: String, dataDir: URL, mcpBridge: URL?) {
        self.socket = socket
        self.dataDir = dataDir
        self.mcpBridge = mcpBridge
    }

    public static func resolve(
        environment env: [String: String] = ProcessInfo.processInfo.environment,
        executable: URL? = Bundle.main.executableURL
    ) -> Paths {
        // Go's os.UserHomeDir reads $HOME; do the same so both sides agree.
        let home = env["HOME"].flatMap { $0.isEmpty ? nil : $0 } ?? FileManager.default.homeDirectoryForCurrentUser.path
        let socket: String
        if let s = env["MALACHI_SOCKET"], !s.isEmpty {
            socket = s
        } else if let rt = env["XDG_RUNTIME_DIR"], !rt.isEmpty {
            socket = rt + "/malachi/rpc.sock"
        } else {
            let cache = env["XDG_CACHE_HOME"].flatMap { $0.isEmpty ? nil : $0 } ?? home + "/.cache"
            socket = cache + "/malachi/run/rpc.sock"
        }
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: home).appendingPathComponent("Library/Application Support", isDirectory: true)
        let dataDir = support.appendingPathComponent("Malachi Mail", isDirectory: true)
        let mcp = executable?.deletingLastPathComponent().appendingPathComponent("malachi-mcp")
        let bridge = mcp.flatMap { FileManager.default.isExecutableFile(atPath: $0.path) ? $0 : nil }
        return Paths(socket: socket, dataDir: dataDir, mcpBridge: bridge)
    }

    /// Creates the data directory (0700). The daemon would create it too; an
    /// early, clear error beats a late one.
    public func ensureDirectories() throws {
        try FileManager.default.createDirectory(
            at: dataDir, withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
    }
}
