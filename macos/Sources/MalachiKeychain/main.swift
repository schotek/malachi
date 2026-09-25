// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// malachi-keychain: the keyring helper malachid runs on macOS
// (MALACHI_KEYRING=helper, backend/internal/auth/helper). One operation per
// process: `malachi-keychain get|set|delete`, one JSON line on stdin, the
// answer on stdout, the outcome in the exit status. The value reaches
// stdout only as the answer to get; stderr carries a diagnostic, never data.
import Foundation

private func fail(_ exit: HelperExit, _ message: String) -> Never {
    FileHandle.standardError.write(Data("malachi-keychain: \(message)\n".utf8))
    Darwin.exit(exit.rawValue)
}

/// Reads stdin to EOF, or nil once it exceeds the limit.
private func readInput(limit: Int) -> Data? {
    var data = Data()
    let input = FileHandle.standardInput
    while let chunk = try? input.read(upToCount: 64 * 1024), !chunk.isEmpty {
        data.append(chunk)
        if data.count > limit {
            return nil
        }
    }
    return data
}

let arguments = CommandLine.arguments
guard arguments.count == 2, let operation = Operation(rawValue: arguments[1]) else {
    fail(.badRequest, "usage: malachi-keychain get|set|delete (one JSON line on stdin)")
}
guard let input = readInput(limit: Request.maxInput) else {
    fail(.badRequest, "request exceeds \(Request.maxInput) bytes")
}
let request: Request
switch Request.parse(op: operation.rawValue, data: input) {
case .success(let parsed):
    request = parsed
case .failure(let exit):
    fail(exit, "malformed \(operation.rawValue) request")
}

let store = KeychainStore()
let outcome: Result<String, KeychainFailure>
switch operation {
case .get:
    outcome = store.get(request).map { Request.valueLine($0) }
case .set:
    // parse guarantees a value for set.
    outcome = store.set(request, value: request.value ?? "").map { "{}\n" }
case .delete:
    outcome = store.delete(request).map { "{}\n" }
}

switch outcome {
case .success(let line):
    FileHandle.standardOutput.write(Data(line.utf8))
    Darwin.exit(HelperExit.ok.rawValue)
case .failure(.notFound):
    Darwin.exit(HelperExit.notFound.rawValue)
case .failure(let failure):
    fail(.failure, "\(operation.rawValue): \(failure.message)")
}
