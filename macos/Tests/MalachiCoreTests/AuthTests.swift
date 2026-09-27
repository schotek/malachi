// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Darwin
import Foundation
import Testing
@testable import MalachiCore

// The handshake's building blocks (docs/api.md §1.4; backend/pkg/api
// auth_test.go): the proofs against the documented test vectors, the strict
// parsing of hex and of the key file, the key file's path, and the checks
// DaemonKey makes on the file before it reads it.

// The test vectors, copied verbatim from the table in docs/api.md §1.4 (the
// same as backend/pkg/api/auth_test.go): key = bytes 0x00…0x1f, clientNonce
// = 0x20…0x3f, daemonNonce = 0x40…0x5f.
private let vectorKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
private let vectorClientNonce = "202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"
private let vectorDaemonNonce = "404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f"
private let vectorLabel = "6d616c616368692d7270632d617574682d7631"
private let vectorDaemonRole = "6461656d6f6e"
private let vectorClientRole = "636c69656e74"
private let vectorDaemonProof = "04abc851d52b40dc687920756f15f42f44da2635331732bf02be0a0b01de2a1f"
private let vectorClientProof = "024f86a00c241237f4556a27e83f8e41b13053bbcfd2980e029c300a5a2e84b2"

/// 32 bytes counting up from `first`.
private func bytes(from first: Int) -> Data {
    Data((0..<32).map { UInt8(first + $0) })
}

/// The bytes with one bit flipped.
private func flip(_ data: Data, bit: Int) -> Data {
    var d = data
    d[d.startIndex + bit / 8] ^= UInt8(1 << (bit % 8))
    return d
}

@Suite struct AuthTests {
    @Test func proofsMatchTheDocumentedVectors() throws {
        let key = bytes(from: 0x00)
        let clientNonce = bytes(from: 0x20)
        let daemonNonce = bytes(from: 0x40)
        #expect(RPCAuth.hex(key) == vectorKey)
        #expect(RPCAuth.hex(clientNonce) == vectorClientNonce)
        #expect(RPCAuth.hex(daemonNonce) == vectorDaemonNonce)
        #expect(RPCAuth.hex(Data(RPCAuth.label.utf8)) == vectorLabel)
        #expect(RPCAuth.hex(Data(RPCAuth.roleDaemon.utf8)) == vectorDaemonRole)
        #expect(RPCAuth.hex(Data(RPCAuth.roleClient.utf8)) == vectorClientRole)
        #expect(RPCAuth.hex(RPCAuth.daemonProof(key: key, clientNonce: clientNonce, daemonNonce: daemonNonce)) == vectorDaemonProof)
        #expect(RPCAuth.hex(RPCAuth.clientProof(key: key, clientNonce: clientNonce, daemonNonce: daemonNonce)) == vectorClientProof)
        // 91 bytes: the label, NUL, a six-letter role, NUL, two 32-byte nonces.
        #expect(RPCAuth.message(RPCAuth.roleDaemon, clientNonce, daemonNonce).count == 91)
        #expect(RPCAuth.message(RPCAuth.roleClient, clientNonce, daemonNonce).count == 91)

        // Verification: the daemon's proof holds, the client's does not,
        // and swapped nonces do not either.
        let daemonProof = try #require(RPCAuth.decodeHex32(vectorDaemonProof))
        let clientProof = try #require(RPCAuth.decodeHex32(vectorClientProof))
        #expect(RPCAuth.isValidDaemonProof(daemonProof, key: key, clientNonce: clientNonce, daemonNonce: daemonNonce))
        #expect(!RPCAuth.isValidDaemonProof(clientProof, key: key, clientNonce: clientNonce, daemonNonce: daemonNonce),
                "a reflected client proof")
        #expect(!RPCAuth.isValidDaemonProof(daemonProof, key: key, clientNonce: daemonNonce, daemonNonce: clientNonce),
                "swapped nonces")
        #expect(!RPCAuth.isValidDaemonProof(Data(count: 32), key: key, clientNonce: clientNonce, daemonNonce: daemonNonce))
        #expect(!RPCAuth.isValidDaemonProof(daemonProof.prefix(31), key: key, clientNonce: clientNonce, daemonNonce: daemonNonce))

        // The key file of the vector key.
        #expect(RPCAuth.parseKey(Data((vectorKey + "\n").utf8)) == key)
    }

    @Test func proofsSeparateRolesAndInputs() {
        let key = RPCAuth.newNonce()
        let clientNonce = RPCAuth.newNonce()
        let daemonNonce = RPCAuth.newNonce()
        let d = RPCAuth.daemonProof(key: key, clientNonce: clientNonce, daemonNonce: daemonNonce)
        let c = RPCAuth.clientProof(key: key, clientNonce: clientNonce, daemonNonce: daemonNonce)
        #expect(d.count == 32 && c.count == 32)
        #expect(d != c, "the daemon's and the client's proofs differ")
        #expect(RPCAuth.daemonProof(key: key, clientNonce: clientNonce, daemonNonce: daemonNonce) == d, "deterministic")
        for bit in [0, 7, 8, 131, 255] {
            #expect(!RPCAuth.isValidDaemonProof(d, key: flip(key, bit: bit), clientNonce: clientNonce, daemonNonce: daemonNonce))
            #expect(!RPCAuth.isValidDaemonProof(d, key: key, clientNonce: flip(clientNonce, bit: bit), daemonNonce: daemonNonce))
            #expect(!RPCAuth.isValidDaemonProof(d, key: key, clientNonce: clientNonce, daemonNonce: flip(daemonNonce, bit: bit)))
            #expect(!RPCAuth.isValidDaemonProof(flip(d, bit: bit), key: key, clientNonce: clientNonce, daemonNonce: daemonNonce))
        }
    }

    @Test func noncesAreFresh() {
        var seen = Set<Data>()
        for _ in 0..<1000 {
            let n = RPCAuth.newNonce()
            #expect(n.count == 32)
            seen.insert(n)
        }
        #expect(seen.count == 1000, "a nonce repeated")
        #expect(!seen.contains(Data(count: 32)))
    }

    @Test func hexIsExactlySixtyFourLowercaseDigits() throws {
        let good = String(repeating: "0123456789abcdef", count: 4)
        let decoded = try #require(RPCAuth.decodeHex32(good))
        #expect(decoded.count == 32 && RPCAuth.hex(decoded) == good)
        let head62 = String(good.prefix(62))
        let head63 = String(good.prefix(63))
        let bad: [String: String] = [
            "empty": "",
            "63 digits": head63,
            "65 digits": good + "0",
            "128 digits": good + good,
            "upper case": good.uppercased(),
            "mixed case": String(good.prefix(60)) + "ABcd",
            "0x prefix": "0x" + head62,
            "0x prefix, 66 bytes": "0x" + good,
            "leading space": " " + String(good.dropFirst()),
            "trailing space": head63 + " ",
            "surrounding space": " " + good + " ",
            "tab": "\t" + String(good.dropFirst()),
            "newline": head63 + "\n",
            "trailing newline": good + "\n",
            "NUL": head63 + "\u{0}",
            "g": head63 + "g",
            "full-width digits": String(repeating: "\u{FF10}", count: 64),
            "full-width, 64 bytes": String(repeating: "\u{FF10}", count: 21) + "0",
            "two-byte character, 64 bytes": head62 + "\u{E9}",
        ]
        for (name, s) in bad {
            if name.hasSuffix("64 bytes") {
                #expect(s.utf8.count == 64, "\(name): the case is \(s.utf8.count) bytes")
            }
            #expect(RPCAuth.decodeHex32(s) == nil, "\(name) accepted")
        }
    }

    @Test func keyFileContentIsStrict() {
        let key = RPCAuth.newNonce()
        let good = RPCAuth.hex(key)
        #expect(RPCAuth.parseKey(Data((good + "\n").utf8)) == key)
        let head63 = String(good.prefix(63))
        let bad: [String: Data] = [
            "empty": Data(),
            "newline only": Data("\n".utf8),
            "missing newline": Data(good.utf8),
            "CRLF": Data((good + "\r\n").utf8),
            "CR instead of newline": Data((good + "\r").utf8),
            "two newlines": Data((good + "\n\n").utf8),
            "newline first": Data(("\n" + good).utf8),
            "leading space": Data((" " + good + "\n").utf8),
            "leading space, 65 bytes": Data((" " + String(good.dropFirst()) + "\n").utf8),
            "trailing space": Data((good + " \n").utf8),
            "space before newline": Data((head63 + " \n").utf8),
            "BOM": Data(("\u{FEFF}" + good + "\n").utf8),
            "BOM, 65 bytes": Data(("\u{FEFF}" + String(good.dropFirst(3)) + "\n").utf8),
            "upper case": Data((good.uppercased() + "\n").utf8),
            "NUL": Data((head63 + "\u{0}\n").utf8),
            "NUL instead of newline": Data((good + "\u{0}").utf8),
            "1 MiB": Data(repeating: UInt8(ascii: "a"), count: 1 << 20),
            "key and garbage": Data((good + "\ngarbage").utf8),
            "two keys": Data((good + "\n" + good + "\n").utf8),
        ]
        for (name, content) in bad {
            if name.hasSuffix("65 bytes") {
                #expect(content.count == 65, "\(name): the case is \(content.count) bytes")
            }
            #expect(RPCAuth.parseKey(content) == nil, "\(name) accepted")
        }
    }

    @Test func keyPathLiesBesideTheSocket() {
        #expect(RPCAuth.keyPath(socket: "/Users/u/.cache/malachi/run/rpc.sock") == "/Users/u/.cache/malachi/run/rpc.sock.key")
        #expect(RPCAuth.keyPath(socket: "/run/user/1000/malachi/rpc.sock") == "/run/user/1000/malachi/rpc.sock.key")
        #expect(RPCAuth.keyPath(socket: "rpc.sock") == "rpc.sock.key")
    }
}

/// DaemonKey's checks on the key file, stricter than the Go clients':
/// a regular file, not a link, of this user, private, 65 bytes, a key.
@Suite(.serialized) struct DaemonKeyTests {
    /// A fresh private directory, removed by `cleanUp`.
    private func scratch() throws -> URL {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("malachi-key-\(UUID().uuidString.prefix(8))", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        return dir
    }

    private func cleanUp(_ dir: URL) {
        try? FileManager.default.removeItem(at: dir)
    }

    /// Writes `content` to `name` in `dir` with exactly `mode`.
    private func write(_ content: Data, _ name: String, in dir: URL, mode: Int = 0o600) throws -> String {
        let path = dir.appendingPathComponent(name).path
        #expect(FileManager.default.createFile(atPath: path, contents: content))
        try FileManager.default.setAttributes([.posixPermissions: mode], ofItemAtPath: path)
        return path
    }

    private func keyFile(_ key: Data) -> Data {
        Data((RPCAuth.hex(key) + "\n").utf8)
    }

    /// Why DaemonKey refused the file; nil (and an issue) when it read it.
    private func refusal(_ path: String) -> String? {
        do {
            _ = try DaemonKey.read(path)
            Issue.record("\(path) was read")
        } catch let e as DaemonKey.Unavailable {
            return e.reason
        } catch {
            Issue.record("\(path): \(error)")
        }
        return nil
    }

    @Test func readsTheKeyTheDaemonWrote() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        let key = RPCAuth.newNonce()
        let path = try write(keyFile(key), "rpc.sock.key", in: dir)
        #expect(try DaemonKey.read(path) == key)
        // Read-only for its owner is private as well.
        try FileManager.default.setAttributes([.posixPermissions: 0o400], ofItemAtPath: path)
        #expect(try DaemonKey.read(path) == key)
    }

    @Test func refusesAMissingFile() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        let path = dir.appendingPathComponent("missing.key").path
        #expect(refusal(path) == "\(path) does not exist")
    }

    @Test func refusesASymbolicLink() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        let target = try write(keyFile(RPCAuth.newNonce()), "target.key", in: dir)
        let link = dir.appendingPathComponent("link.key").path
        try FileManager.default.createSymbolicLink(atPath: link, withDestinationPath: target)
        #expect(refusal(link) == "\(link) is a symbolic link")
    }

    @Test func refusesAFileOthersMayRead() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        for mode in [0o640, 0o604, 0o660, 0o644, 0o606] {
            let path = try write(keyFile(RPCAuth.newNonce()), "key-\(String(mode, radix: 8))", in: dir, mode: mode)
            #expect(refusal(path) == "\(path) is accessible to other users", "mode \(String(mode, radix: 8))")
        }
    }

    /// O_NONBLOCK: opening a FIFO does not wait for a writer.
    @Test func refusesAFIFOAtOnce() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        let path = dir.appendingPathComponent("fifo.key").path
        #expect(mkfifo(path, 0o600) == 0)
        let start = ContinuousClock.now
        #expect(refusal(path) == "\(path) is not a regular file")
        #expect(ContinuousClock.now - start < .seconds(1))
    }

    @Test func refusesDirectoriesAndDevices() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        let sub = dir.appendingPathComponent("dir.key").path
        try FileManager.default.createDirectory(atPath: sub, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        #expect(refusal(sub) == "\(sub) is not a regular file")
        #expect(refusal("/dev/null") == "/dev/null is not a regular file")
    }

    @Test func refusesWhatIsNotAKeyFile() throws {
        let dir = try scratch()
        defer { cleanUp(dir) }
        let good = RPCAuth.hex(RPCAuth.newNonce())
        let sized: [String: Data] = [
            "empty": Data(),
            "no newline": Data(good.utf8),
            "two keys": Data((good + "\n" + good + "\n").utf8),
            "10 MiB": Data(repeating: UInt8(ascii: "a"), count: 10 << 20),
        ]
        for (name, content) in sized {
            let path = try write(content, name, in: dir)
            #expect(refusal(path) == "\(path) is not 65 bytes", "\(name)")
        }
        // The right size, the wrong content; the reason never quotes it.
        let upper = try write(Data((good.uppercased() + "\n").utf8), "upper", in: dir)
        #expect(refusal(upper) == "\(upper) is not a key file")
        let crlf = try write(Data((String(good.dropLast()) + "\r\n").utf8), "crlf", in: dir)
        #expect(refusal(crlf) == "\(crlf) is not a key file")
    }
}
