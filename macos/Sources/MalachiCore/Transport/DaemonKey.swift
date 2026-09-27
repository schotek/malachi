// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation

/// Reads malachid's connection key (docs/api.md §1.4): the file beside the
/// socket (`RPCAuth.keyPath`) that the daemon writes anew at every start.
/// `RPCClient` reads it on every connection, only after the daemon answered
/// `system.hello` in this client's protocol, and never keeps it.
///
/// The file must be a regular file (no symbolic link, directory, pipe or
/// device) of exactly 65 bytes in the key format, as api.ReadKeyFile asks.
/// This reader is stricter than the Go clients on purpose: the file must
/// also belong to this user and grant nothing to group or others, which is
/// how the daemon writes it (0600). The Go clients cannot check owner and
/// mode the same way on every platform they build for; here it is a free
/// defence in depth (the deviation table in macos/README.md).
enum DaemonKey {
    /// Why the key file cannot be used: fixed text around the path, never
    /// anything of the file's content.
    struct Unavailable: Error, Sendable, Equatable, CustomStringConvertible {
        let reason: String

        var description: String { reason }
    }

    /// The key in the file at `path`, 32 bytes. The file is opened once and
    /// everything is checked on the open descriptor, so a path replaced in
    /// between cannot slip another file in.
    static func read(_ path: String) throws -> Data {
        // O_NOFOLLOW refuses a symbolic link (ELOOP) instead of following
        // it; O_NONBLOCK keeps a FIFO from waiting for a writer (it is then
        // refused as not a regular file).
        let fd = Darwin.open(path, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC)
        if fd < 0 {
            let err = errno
            switch err {
            case ENOENT:
                throw Unavailable(reason: "\(path) does not exist")
            case ELOOP:
                throw Unavailable(reason: "\(path) is a symbolic link")
            default:
                throw Unavailable(reason: "cannot open \(path): \(errorText(err))")
            }
        }
        defer { Darwin.close(fd) }

        var st = stat()
        guard fstat(fd, &st) == 0 else {
            throw Unavailable(reason: "cannot inspect \(path): \(errorText(errno))")
        }
        guard (st.st_mode & S_IFMT) == S_IFREG else {
            throw Unavailable(reason: "\(path) is not a regular file")
        }
        guard st.st_uid == geteuid() else {
            throw Unavailable(reason: "\(path) belongs to another user")
        }
        guard (st.st_mode & 0o077) == 0 else {
            throw Unavailable(reason: "\(path) is accessible to other users")
        }
        guard st.st_size == off_t(RPCAuth.keyFileSize) else {
            throw Unavailable(reason: "\(path) is not \(RPCAuth.keyFileSize) bytes")
        }

        // One byte more than a key file: a file that grew since fstat is
        // refused by parseKey rather than cut short.
        var buffer = [UInt8](repeating: 0, count: RPCAuth.keyFileSize + 1)
        var filled = 0
        while filled < buffer.count {
            let n = buffer.withUnsafeMutableBytes { raw in
                Darwin.read(fd, raw.baseAddress! + filled, raw.count - filled)
            }
            if n < 0 {
                let err = errno
                if err == EINTR {
                    continue
                }
                throw Unavailable(reason: "cannot read \(path): \(errorText(err))")
            }
            if n == 0 {
                break
            }
            filled += n
        }
        guard let key = RPCAuth.parseKey(Data(buffer[..<filled])) else {
            throw Unavailable(reason: "\(path) is not a key file")
        }
        return key
    }

    private static func errorText(_ code: Int32) -> String {
        String(cString: strerror(code))
    }
}
