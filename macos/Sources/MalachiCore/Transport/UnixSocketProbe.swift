// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation

/// Synchronous checks on the daemon's unix socket, the counterpart of the
/// GTK supervisor's `answers()` probe.
public enum UnixSocketProbe {
    /// Darwin's `sockaddr_un.sun_path` is 104 bytes including the NUL. The
    /// daemon checks against Linux's 108 and would fail later with a bare
    /// "invalid argument", so the app checks first.
    public static let maxPathBytes = 103

    public struct PathTooLong: Error, Sendable, CustomStringConvertible {
        public let path: String
        public let bytes: Int

        public var description: String {
            "socket path is \(bytes) bytes, macOS allows \(UnixSocketProbe.maxPathBytes): \(path) (set MALACHI_SOCKET to a shorter path)"
        }
    }

    public static func check(_ path: String) throws {
        let n = path.utf8.count
        if n > maxPathBytes {
            throw PathTooLong(path: path, bytes: n)
        }
    }

    /// True when something listens on the socket. The connect is
    /// non-blocking: a missing socket or a dead one answers ENOENT or
    /// ECONNREFUSED at once, and a listener whose backlog is full (it exists,
    /// it is just busy) answers EAGAIN or EINPROGRESS instead of blocking.
    public static func answers(_ path: String) -> Bool {
        guard path.utf8.count <= maxPathBytes else { return false }
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)

        var addr = sockaddr_un()
        let capacity = MemoryLayout.size(ofValue: addr.sun_path)
        addr.sun_family = sa_family_t(AF_UNIX)
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutablePointer(to: &addr.sun_path) { ptr in
            let dst = UnsafeMutableRawPointer(ptr).assumingMemoryBound(to: CChar.self)
            _ = strlcpy(dst, path, capacity)
        }
        let len = socklen_t(MemoryLayout<sockaddr_un>.size)
        let rc = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, len) }
        }
        if rc == 0 {
            return true
        }
        return errno == EINPROGRESS || errno == EAGAIN
    }
}
