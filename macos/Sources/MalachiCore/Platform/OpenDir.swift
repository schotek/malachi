// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Where attachments being opened are written (ui/internal/window/
/// attachments.go `openDir`, `writeOpenFile`, `sweepOpenDir`,
/// `SweepOpenedAttachments`, `purgeOpenDir`; docs/security.md §8): a
/// private directory (0700) under the user's caches, one fresh
/// subdirectory per file, each file created exclusively with mode 0600.
/// Entries older than `openMaxAge` are swept before every write; the whole
/// directory goes when the application starts and when it quits, whatever
/// the preferences say (`removeAll`).
public struct OpenDir: Sendable {
    /// What `removeAll` throws for a URL that is not an open directory;
    /// nothing was touched. The path names the user's directories: log it
    /// as private.
    public struct NotTheOpenDirectory: Error, Equatable, CustomStringConvertible {
        public let path: String

        public var description: String {
            "not the directory for opened attachments: \(path)"
        }
    }

    /// The last two components of every open directory's path.
    static let parentName = "Malachi Mail"
    static let name = "open"

    public let url: URL

    public init(url: URL) {
        self.url = url
    }

    /// `~/Library/Caches/Malachi Mail/open`. macOS has no runtime dir of the
    /// XDG kind; the caches directory is per user and not shared.
    public static var `default`: OpenDir {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first
            ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Caches", isDirectory: true)
        return OpenDir(url: caches
            .appendingPathComponent(parentName, isDirectory: true)
            .appendingPathComponent(name, isDirectory: true))
    }

    /// Writes `data` as `name` into a fresh private subdirectory and returns
    /// the file's URL (`writeOpenFile`). Entries older than `openMaxAge` go
    /// first. The subdirectory comes from `mkdtemp` (0700) and the file is
    /// opened with `O_CREAT | O_EXCL` and mode 0600, so nothing that was
    /// there before is ever reused or overwritten.
    public func write(name: String, data: Data) throws -> URL {
        sweep(maxAge: openMaxAge)
        try FileManager.default.createDirectory(
            at: url, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        var template = Array((url.path + "/XXXXXXXXXX").utf8CString)
        guard mkdtemp(&template) != nil else {
            throw posixError("mkdtemp")
        }
        let sub = String(decoding: template.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self)
        let path = sub + "/" + name
        let fd = open(path, O_WRONLY | O_CREAT | O_EXCL, 0o600)
        guard fd >= 0 else {
            let err = posixError("open")
            try? FileManager.default.removeItem(atPath: sub)
            throw err
        }
        do {
            try writeAll(fd, data)
        } catch {
            close(fd)
            try? FileManager.default.removeItem(atPath: sub)
            throw error
        }
        guard close(fd) == 0 else {
            let err = posixError("close")
            try? FileManager.default.removeItem(atPath: sub)
            throw err
        }
        return URL(fileURLWithPath: path)
    }

    /// Removes the entries older than `maxAge` (`sweepOpenDir`). A missing
    /// directory is a no-op.
    public func sweep(maxAge: TimeInterval = openMaxAge) {
        let fm = FileManager.default
        guard let entries = try? fm.contentsOfDirectory(
            at: url, includingPropertiesForKeys: [.contentModificationDateKey], options: []) else {
            return
        }
        let cutoff = Date().addingTimeInterval(-maxAge)
        for entry in entries {
            guard let modified = try? entry.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate else {
                continue
            }
            if modified < cutoff {
                try? fm.removeItem(at: entry)
            }
        }
    }

    /// Removes the directory with every file written for opening
    /// (`SweepOpenedAttachments`, `purgeOpenDir`), and refuses any
    /// directory but an open one: `url` must be an absolute file path
    /// that ends in `Malachi Mail/open` once `.` and `..` are taken as
    /// written, or `NotTheOpenDirectory` is thrown and nothing is touched,
    /// so a slip cannot take anything else with it. A symbolic link in its
    /// place is removed, never followed; a missing directory is no error.
    /// Any other failure is thrown for the caller's log.
    public func removeAll() throws {
        guard let clean = Self.purgeable(url) else {
            throw NotTheOpenDirectory(path: url.path)
        }
        do {
            try FileManager.default.removeItem(at: clean)
        } catch CocoaError.fileNoSuchFile {
            // Nothing to remove.
        }
    }

    /// `url` with its `.` and `..` resolved lexically, when that names an
    /// open directory; nil otherwise. A path relative to the working
    /// directory is refused, not resolved against it.
    static func purgeable(_ url: URL) -> URL? {
        guard url.isFileURL, url.baseURL == nil, url.relativePath.hasPrefix("/") else {
            return nil
        }
        let clean = url.standardizedFileURL
        let parts = clean.pathComponents
        guard parts.count >= 3, parts[parts.count - 1] == name, parts[parts.count - 2] == parentName else {
            return nil
        }
        return clean
    }

    private func writeAll(_ fd: Int32, _ data: Data) throws {
        try data.withUnsafeBytes { (buffer: UnsafeRawBufferPointer) in
            var offset = 0
            while offset < buffer.count {
                let n = Foundation.write(fd, buffer.baseAddress! + offset, buffer.count - offset)
                if n < 0 {
                    if errno == EINTR {
                        continue
                    }
                    throw posixError("write")
                }
                offset += n
            }
        }
    }

    private func posixError(_ call: String) -> any Error {
        let code = errno
        return NSError(domain: NSPOSIXErrorDomain, code: Int(code), userInfo: [
            NSLocalizedDescriptionKey: "\(call): \(String(cString: strerror(code)))",
        ])
    }
}
