// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the registration with Claude the
// switch in Settings → AI makes, without the window, for measuring the
// App Store build's setup through LaunchServices (BridgeSetupLauncher).
// THESE WRITE THE REAL CLAUDE CONFIGURATIONS; back them up first.
//
//   `mcp-status` prints `MCP: mcp-status registered=…`; `mcp-install` and
//   `mcp-uninstall` run that subcommand through a fresh
//   MCPRegistrationController and print `MCP: <step> <client> present=…
//   registered=… other=…` per client, or `MCP: <step> failed`; a toast is
//   printed as `MCP: <step> toast …`.
@MainActor
enum DevelopmentMCP {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        switch step {
        case "mcp-status": run(step, main, want: nil)
        case "mcp-install": run(step, main, want: true)
        case "mcp-uninstall": run(step, main, want: false)
        default: return false
        }
        return true
    }

    private static func run(_ step: String, _ main: MainWindowController, want: Bool?) {
        let c = MCPRegistrationController(bridge: main.state.paths.mcpBridge?.path)
        c.onToast = { print("MCP: \(step) toast \($0)") }
        guard let want else {
            var answered = false
            c.onRegistered = { on in
                guard !answered else { return }
                answered = true
                print("MCP: \(step) registered=\(on)")
                c.close()
            }
            c.load()
            return
        }
        Task { @MainActor in
            guard let status = await c.change(registered: want) else {
                print("MCP: \(step) failed")
                c.close()
                return
            }
            for client in status.clients {
                print("MCP: \(step) \(client.id) present=\(client.present) registered=\(client.registered) other=\(client.other ?? "-")")
            }
            c.close()
        }
    }

    private static func print(_ line: String) {
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }
}
