// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

#if !os(macOS)
import Foundation

/// iOS has no `Foundation.Process`: an application there cannot start
/// another program. This stand-in has the surface the core uses, so that
/// the code which runs the daemon, Claude Code and the bridge on macOS
/// compiles unchanged; `run()` always throws, and every caller already
/// reports a program that could not be started.
final class Process: @unchecked Sendable {
    enum TerminationReason { case exit, uncaughtSignal }

    struct Unavailable: LocalizedError {
        var errorDescription: String? { "this platform cannot start another program" }
    }

    var executableURL: URL?
    var arguments: [String]?
    var environment: [String: String]?
    var currentDirectoryURL: URL?
    var standardInput: Any?
    var standardOutput: Any?
    var standardError: Any?
    var terminationHandler: (@Sendable (Process) -> Void)?
    var qualityOfService: QualityOfService = .default

    var isRunning: Bool { false }
    var processIdentifier: Int32 { 0 }
    var terminationStatus: Int32 { 0 }
    var terminationReason: TerminationReason { .exit }

    func run() throws { throw Unavailable() }
    func terminate() {}
    func interrupt() {}
    func waitUntilExit() {}
}
#endif
