// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The slice of backend/pkg/api this client needs, re-declared in Swift. The
// contract is docs/api.md; the numbers and names here must follow it.

import Foundation

public enum API {
    /// api.ProtocolVersion. A daemon with another value is refused.
    public static let protocolVersion = 1

    public static let systemInfo = "system.info"
}

/// Result of system.info (docs/api.md §4.0).
public struct SystemInfo: Decodable, Sendable, Equatable {
    public let version: String
    public let protocolVersion: Int
    public let pid: Int
    public let storePath: String

    public init(version: String, protocolVersion: Int, pid: Int, storePath: String) {
        self.version = version
        self.protocolVersion = protocolVersion
        self.pid = pid
        self.storePath = storePath
    }
}
