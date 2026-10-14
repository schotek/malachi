// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Darwin

/// Walk directories with openat(O_NOFOLLOW): no path ancestor can substitute
/// a symlink. Leaf files are owner-private regular files, opened before checked.
public enum CodexPrivateFiles {
    public static func directory(_ url: URL) throws -> Int32 {
        guard url.isFileURL, url.path.hasPrefix("/") else { throw ChatGPTFailure("chatgpt_storage") }
        var fd = Darwin.open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC)
        guard fd >= 0 else { throw ChatGPTFailure("chatgpt_storage") }
        do {
            for component in url.path.split(separator: "/") {
                let name = String(component)
                guard name != ".", name != ".." else { throw ChatGPTFailure("chatgpt_storage") }
                if mkdirat(fd, name, 0o700) != 0 && errno != EEXIST { throw ChatGPTFailure("chatgpt_storage") }
                let next = openat(fd, name, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
                guard next >= 0 else { throw ChatGPTFailure("chatgpt_storage") }
                Darwin.close(fd); fd = next
            }
            var attributes = stat()
            guard fstat(fd, &attributes) == 0, attributes.st_uid == getuid(), attributes.st_mode & 0o077 == 0 else { throw ChatGPTFailure("chatgpt_storage") }
            return fd
        } catch { Darwin.close(fd); throw error }
    }
    public static func openFile(directory: URL, name: String, create: Bool = false, exclusive: Bool = false) throws -> Int32? {
        guard !name.contains("/"), name != ".", name != ".." else { throw ChatGPTFailure("chatgpt_storage") }
        let parent = try self.directory(directory); defer { Darwin.close(parent) }
        let flags = O_RDWR | O_NOFOLLOW | O_CLOEXEC | (create ? O_CREAT : 0) | (exclusive ? O_EXCL : 0)
        let fd = openat(parent, name, flags, 0o600)
        if fd < 0 && errno == ENOENT && !create { return nil }
        guard fd >= 0 else { throw ChatGPTFailure("chatgpt_storage") }
        var attrs = stat()
        guard fstat(fd, &attrs) == 0, attrs.st_uid == getuid(), attrs.st_mode & 0o077 == 0,
              attrs.st_mode & S_IFMT == S_IFREG, attrs.st_nlink == 1 else { Darwin.close(fd); throw ChatGPTFailure("chatgpt_storage") }
        return fd
    }
    public static func read(directory: URL, name: String, limit: Int = 65536) throws -> Data? {
        guard let fd = try openFile(directory: directory, name: name) else { return nil }
        let handle = FileHandle(fileDescriptor: fd, closeOnDealloc: true); defer { try? handle.close() }
        var data = Data()
        while true {
            let chunk = try handle.read(upToCount: 8192) ?? Data()
            if chunk.isEmpty { return data }
            guard data.count + chunk.count <= limit else { throw ChatGPTFailure("chatgpt_storage") }
            data.append(chunk)
        }
    }
    public static func write(_ data: Data, directory: URL, name: String) throws {
        guard !name.contains("/"), name != ".", name != ".." else { throw ChatGPTFailure("chatgpt_storage") }
        let parent = try self.directory(directory); defer { Darwin.close(parent) }
        let temporary = ".pending-" + UUID().uuidString
        let fd = openat(parent, temporary, O_CREAT | O_EXCL | O_WRONLY | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw ChatGPTFailure("chatgpt_storage") }
        defer { unlinkat(parent, temporary, 0) }
        let handle = FileHandle(fileDescriptor: fd, closeOnDealloc: true); defer { try? handle.close() }
        try handle.write(contentsOf: data); try handle.synchronize()
        guard renameat(parent, temporary, parent, name) == 0 else { throw ChatGPTFailure("chatgpt_storage") }
        guard fsync(parent) == 0 else { throw ChatGPTFailure("chatgpt_storage") }
    }
}
