// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// Writes an executable script into a fresh temp dir and returns its URL.
private func script(named name: String = "malachid", _ body: String) throws -> URL {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("malachi-sup-\(UUID().uuidString.prefix(8))", isDirectory: true)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let url = dir.appendingPathComponent(name)
    try Data(body.utf8).write(to: url)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    return url
}

private func tempSocket() -> String {
    FileManager.default.temporaryDirectory.appendingPathComponent("malachi-sup-\(UUID().uuidString.prefix(8)).sock").path
}

@Suite(.serialized) struct SupervisorTests {
    @Test func locateHonoursTheEnvironment() throws {
        #expect(try DaemonSupervisor.locate(besides: nil, environment: ["MALACHI_DAEMON": "none"]) == nil)
        #expect(try DaemonSupervisor.locate(besides: nil, environment: ["MALACHI_DAEMON": ""]) == nil)
        #expect(try DaemonSupervisor.locate(besides: nil, environment: ["MALACHI_DAEMON": "/opt/x/malachid"])?.path == "/opt/x/malachid")
    }

    @Test func locateFindsTheDaemonBesideTheExecutableThenOnPath() throws {
        let daemon = try script("#!/bin/sh\nexit 0\n")
        let dir = daemon.deletingLastPathComponent()
        let exe = dir.appendingPathComponent("MalachiMail")
        #expect(try DaemonSupervisor.locate(besides: exe, environment: [:])?.path == daemon.path)
        #expect(try DaemonSupervisor.locate(besides: nil, environment: ["PATH": "/nonexistent:\(dir.path)"])?.path == daemon.path)
        #expect(throws: DaemonSupervisor.SupervisorError.self) {
            try DaemonSupervisor.locate(besides: nil, environment: ["PATH": "/nonexistent"])
        }
        // A directory named malachid does not count.
        let trap = try FileManager.default.temporaryDirectory.appendingPathComponent("malachi-trap-\(UUID().uuidString.prefix(8))", isDirectory: true)
        try FileManager.default.createDirectory(at: trap.appendingPathComponent("malachid"), withIntermediateDirectories: true)
        #expect(throws: DaemonSupervisor.SupervisorError.self) {
            try DaemonSupervisor.locate(besides: trap.appendingPathComponent("x"), environment: ["PATH": ""])
        }
    }

    @Test func adoptsARunningDaemonWithoutSpawning() async throws {
        let fake = try FakeDaemon { _, _ in .failure(RPCError(code: 1000, message: "nope")) }
        try await fake.start()
        defer { Task { await fake.stop() } }
        let sup = DaemonSupervisor(launch: nil, socket: fake.path)
        try await sup.ensure()
        #expect(await sup.spawns == 0)
        await sup.stop() // nothing of ours to stop; must not touch the fake
        #expect(UnixSocketProbe.answers(fake.path))
    }

    @Test func noDaemonAndNothingListening() async throws {
        let sup = DaemonSupervisor(launch: nil, socket: tempSocket())
        await #expect(throws: DaemonSupervisor.SupervisorError.self) { try await sup.ensure() }
    }

    @Test func earlyExitIsReportedAndBackedOff() async throws {
        let daemon = try script("#!/bin/sh\nsleep 0.2\nexit 3\n")
        let sock = tempSocket()
        let sup = DaemonSupervisor(launch: .init(executable: daemon, socket: sock, config: "/tmp/c.toml", store: "/tmp/s.db"), socket: sock)

        await #expect(throws: DaemonSupervisor.SupervisorError.self) { try await sup.ensure() }
        #expect(await sup.spawns == 1)
        // One exit is retried at once; the second consecutive exit backs off.
        await #expect(throws: DaemonSupervisor.SupervisorError.self) { try await sup.ensure() }
        #expect(await sup.spawns == 2)
        do {
            try await sup.ensure()
            Issue.record("expected a backoff error")
        } catch let e as DaemonSupervisor.SupervisorError {
            guard case .backoff(let failures, _) = e else { Issue.record("expected backoff, got \(e)"); return }
            #expect(failures == 2)
            #expect(await sup.spawns == 2)
        }
    }

    @Test func spawnsPollsAndStops() async throws {
        guard FileManager.default.isExecutableFile(atPath: "/usr/bin/python3") else { return }
        // A stand-in daemon: binds the socket it is given as --socket, accepts
        // (and drops) every connection like malachid would, and exits on SIGTERM.
        let daemon = try script("""
            #!/usr/bin/python3
            import signal, socket, sys
            path = sys.argv[sys.argv.index("--socket") + 1]
            s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            s.bind(path); s.listen(8); s.settimeout(0.1)
            signal.signal(signal.SIGTERM, lambda *a: sys.exit(0))
            while True:
                try:
                    c, _ = s.accept(); c.close()
                except socket.timeout:
                    pass
            """)
        let sock = tempSocket()
        let sup = DaemonSupervisor(launch: .init(executable: daemon, socket: sock, config: "/tmp/c.toml", store: "/tmp/s.db"), socket: sock)
        try await sup.ensure()
        #expect(await sup.spawns == 1)
        #expect(UnixSocketProbe.answers(sock))
        try await sup.ensure() // already up: no second spawn
        #expect(await sup.spawns == 1)
        let start = ContinuousClock.now
        await sup.stop()
        #expect(ContinuousClock.now - start < .seconds(5), "SIGTERM must end the stand-in promptly")
        try? FileManager.default.removeItem(atPath: sock)
        #expect(!UnixSocketProbe.answers(sock))
    }

    @Test func socketPathLengthIsChecked() {
        let long = "/tmp/" + String(repeating: "x", count: 110)
        #expect(throws: UnixSocketProbe.PathTooLong.self) { try UnixSocketProbe.check(long) }
        #expect(!UnixSocketProbe.answers(long))
        #expect(throws: Never.self) { try UnixSocketProbe.check("/tmp/short.sock") }
    }

    @Test func pathsFollowTheDaemonsResolution() {
        let env = ["HOME": "/Users/u"]
        #expect(Paths.resolve(environment: env, executable: nil).socket == "/Users/u/.cache/malachi/run/rpc.sock")
        #expect(Paths.resolve(environment: ["HOME": "/Users/u", "XDG_CACHE_HOME": "/c"], executable: nil).socket == "/c/malachi/run/rpc.sock")
        #expect(Paths.resolve(environment: ["HOME": "/Users/u", "XDG_RUNTIME_DIR": "/run/user/1"], executable: nil).socket == "/run/user/1/malachi/rpc.sock")
        #expect(Paths.resolve(environment: ["HOME": "/Users/u", "MALACHI_SOCKET": "/s.sock"], executable: nil).socket == "/s.sock")
        let p = Paths.resolve(environment: env, executable: nil)
        #expect(p.dataDir.lastPathComponent == "Malachi Mail")
        #expect(p.config.hasSuffix("/Malachi Mail/config.toml"))
        #expect(p.store.hasSuffix("/Malachi Mail/store.db"))
        #expect(p.mcpBridge == nil)
    }
}
