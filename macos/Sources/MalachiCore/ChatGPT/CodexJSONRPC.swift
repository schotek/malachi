// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Darwin

/// Strict JSONL and an owned process group. stdout/stderr never enter logs.
/// posix_spawn establishes the group before exec, avoiding a setpgid race.
@MainActor final class CodexJSONRPC {
    var notification: ((String, [String: Any]) -> Void)?
    var request: (@MainActor (String, [String: Any]) async throws -> [String: Any])?
    var onExit: (() -> Void)?
    private(set) var running = false
    private var pid: pid_t = 0
    private let writes = DispatchQueue(label: "io.github.schotek.Malachi.codex-stdin")
    private var input: FileHandle?
    private var output: FileHandle?
    private var reader: Task<Void, Never>?
    private var pending: [Int: CheckedContinuation<[String: Any], Error>] = [:]
    private var nextID = 0
    private var ended = false
    private var waited = false
    private var eof = false
    init(executable: String, arguments: [String], environment: [String: String], directory: URL) throws {
        var stdinPipe: [Int32] = [0, 0], stdoutPipe: [Int32] = [0, 0]
        guard pipe(&stdinPipe) == 0 else { throw ChatGPTFailure("codex_launch_failed") }
        guard pipe(&stdoutPipe) == 0 else { Darwin.close(stdinPipe[0]); Darwin.close(stdinPipe[1]); throw ChatGPTFailure("codex_launch_failed") }
        var actions: posix_spawn_file_actions_t?
        var attributes: posix_spawnattr_t?
        posix_spawn_file_actions_init(&actions); posix_spawnattr_init(&attributes)
        defer { posix_spawn_file_actions_destroy(&actions); posix_spawnattr_destroy(&attributes) }
        posix_spawn_file_actions_adddup2(&actions, stdinPipe[0], STDIN_FILENO)
        posix_spawn_file_actions_adddup2(&actions, stdoutPipe[1], STDOUT_FILENO)
        posix_spawn_file_actions_addopen(&actions, STDERR_FILENO, "/dev/null", O_WRONLY, 0)
        for descriptor in stdinPipe + stdoutPipe { posix_spawn_file_actions_addclose(&actions, descriptor) }
        posix_spawn_file_actions_addchdir_np(&actions, directory.path)
        posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_SETPGROUP | POSIX_SPAWN_CLOEXEC_DEFAULT))
        posix_spawnattr_setpgroup(&attributes, 0)
        let argv = ([executable] + arguments).map { strdup($0) } + [nil]
        let envp = environment.sorted { $0.key < $1.key }.map { strdup($0.key + "=" + $0.value) } + [nil]
        defer { argv.forEach { free($0) }; envp.forEach { free($0) } }
        let result = argv.withUnsafeBufferPointer { args in envp.withUnsafeBufferPointer { env in
            posix_spawn(&pid, executable, &actions, &attributes, args.baseAddress!, env.baseAddress!)
        } }
        Darwin.close(stdinPipe[0]); Darwin.close(stdoutPipe[1])
        guard result == 0 else { Darwin.close(stdinPipe[1]); Darwin.close(stdoutPipe[0]); throw ChatGPTFailure("codex_launch_failed") }
        _ = fcntl(stdinPipe[1], F_SETNOSIGPIPE, 1)
        input = FileHandle(fileDescriptor: stdinPipe[1], closeOnDealloc: true)
        output = FileHandle(fileDescriptor: stdoutPipe[0], closeOnDealloc: true)
        running = true
        let (stream, continuation) = AsyncStream<Data>.makeStream()
        output?.readabilityHandler = { handle in
            let data = handle.availableData
            if data.isEmpty { handle.readabilityHandler = nil; continuation.finish() }
            else { continuation.yield(data) }
        }
        reader = Task { @MainActor [weak self] in
            var buffer = Data()
            for await chunk in stream {
                guard let self, !self.ended else { return }
                buffer.append(chunk)
                while let newline = buffer.firstIndex(of: 10) {
                    let frame = Data(buffer[..<newline]); buffer.removeSubrange(...newline)
                    do { try self.receive(frame) } catch { self.terminate(); return }
                }
                if buffer.count > CodexPolicy.frameLimit { self.terminate(); return }
            }
            self?.eof = true
            if self?.waited == true { self?.finish() }
        }
        let child = pid
        Task.detached { [weak self] in
            var status: Int32 = 0
            while waitpid(child, &status, 0) < 0 && errno == EINTR {}
            await self?.didWait()
        }
    }
    private func didWait() {
        waited = true; running = false
        // The process group is still ours until descendants die; no pid lookup.
        if pid > 0 { kill(-pid, SIGKILL) }
        if eof { finish() }
        else { Task { @MainActor [weak self] in try? await Task.sleep(for: .milliseconds(500)); self?.finish() } }
    }
    func call(_ method: String, _ params: [String: Any] = [:]) async throws -> [String: Any] {
        guard running, pending.count < 32 else { throw ChatGPTFailure("codex_session_closed") }
        nextID += 1; let id = nextID
        return try await withCheckedThrowingContinuation { continuation in
            pending[id] = continuation
            do { try send(["id": id, "method": method, "params": params]) }
            catch { pending.removeValue(forKey: id)?.resume(throwing: error) }
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(30))
                if let c = self?.pending.removeValue(forKey: id) { c.resume(throwing: ChatGPTFailure("codex_protocol_timeout")); self?.terminate() }
            }
        }
    }
    func notify(_ method: String) throws { try send(["method": method, "params": [:]]) }
    private func send(_ value: [String: Any]) throws {
        var frame = value; frame["jsonrpc"] = "2.0"
        let data = try JSONSerialization.data(withJSONObject: frame)
        guard running, let input, data.count <= CodexPolicy.frameLimit else { throw ChatGPTFailure("codex_session_closed") }
        writes.async { [weak self] in
            do { try input.write(contentsOf: data + Data([10])) }
            catch { Task { @MainActor in self?.terminate() } }
        }
    }
    private func receive(_ data: Data) throws {
        guard data.count <= CodexPolicy.frameLimit, String(data: data, encoding: .utf8) != nil,
              let value = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw ChatGPTFailure("codex_invalid_frame") }
        if let method = value["method"] as? String {
            let params = value["params"] as? [String: Any] ?? [:]
            if let id = value["id"] {
                Task { @MainActor [weak self] in
                    guard let self else { return }
                    do {
                        guard let handler = self.request else { throw ChatGPTFailure("codex_request_denied") }
                        let result = try await handler(method, params); try self.send(["id": id, "result": result])
                    } catch { try? self.send(["id": id, "error": ["code": -32601, "message": "request denied by Malachi Mail policy"]]) }
                }
            } else { notification?(method, params) }
            return
        }
        guard let id = value["id"] as? Int, let continuation = pending.removeValue(forKey: id) else { throw ChatGPTFailure("codex_unexpected_response") }
        if let result = value["result"] as? [String: Any], value["error"] == nil { continuation.resume(returning: result) }
        else { continuation.resume(throwing: ChatGPTFailure("codex_request_failed")) }
    }
    func terminate() {
        guard !ended else { return }
        running = false; try? input?.close(); input = nil
        if pid > 0 { kill(-pid, SIGTERM) }
        Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(2))
            guard let self, !self.waited, self.pid > 0 else { return }; kill(-self.pid, SIGKILL)
        }
        for c in pending.values { c.resume(throwing: CancellationError()) }; pending = [:]
    }
    private func finish() {
        guard !ended else { return }; ended = true; running = false
        output?.readabilityHandler = nil; try? output?.close(); output = nil; try? input?.close(); input = nil
        for c in pending.values { c.resume(throwing: ChatGPTFailure("codex_session_ended")) }; pending = [:]
        onExit?()
    }
}
