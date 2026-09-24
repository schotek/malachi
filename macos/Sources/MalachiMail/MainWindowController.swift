// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What the window says about the daemon, mirroring the GTK window's
/// connection status line (ui/internal/window/window.go).
enum ConnectionStatus: Sendable, Equatable {
    case connecting
    case connected(SystemInfo)
    case protocolMismatch(daemon: Int)
    case infoFailed(String)
    case unavailable(String)
    case stopping

    var text: String {
        switch self {
        case .connecting:
            return "Connecting to malachid…"
        case .connected(let info):
            return "Connected to malachid \(info.version) (pid \(info.pid)), protocol \(info.protocolVersion)"
        case .protocolMismatch(let daemon):
            return "Protocol mismatch: app \(API.protocolVersion), daemon \(daemon)"
        case .infoFailed(let reason):
            return "Connected, but system.info failed: \(reason)"
        case .unavailable(let reason):
            return "Backend unavailable: \(reason)"
        case .stopping:
            return "Stopping malachid…"
        }
    }
}

/// The one window of this phase: the connection state, where the socket and
/// the data are, and where the MCP bridge is.
@MainActor
final class MainWindowController: NSWindowController {
    /// How often a dead socket is retried, as in the GTK UI.
    static let reconnectInterval: Duration = .seconds(5)
    /// system.info must answer within this, as in the GTK UI.
    static let infoTimeout: Duration = .seconds(3)

    private let statusLabel = NSTextField(labelWithString: ConnectionStatus.connecting.text)
    private let socketLabel: NSTextField
    private let dataLabel: NSTextField
    private let mcpLabel: NSTextField
    private var client: RPCClient?
    private var supervisor: DaemonSupervisor?
    private var loop: Task<Void, Never>?

    init(paths: Paths) {
        let w = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 240),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false
        )
        w.title = "Malachi Mail"
        w.center()
        w.setFrameAutosaveName("Main")

        socketLabel = NSTextField(wrappingLabelWithString: "Socket: \(paths.socket)")
        dataLabel = NSTextField(wrappingLabelWithString: "Data: \(paths.dataDir.path)")
        mcpLabel = NSTextField(wrappingLabelWithString:
            "MCP bridge: \(paths.mcpBridge?.path ?? "not bundled (running unbundled; use build/malachi-mcp)")")
        super.init(window: w)

        statusLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .medium)
        for label in [socketLabel, dataLabel, mcpLabel] {
            label.isSelectable = true
            label.font = .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
            label.textColor = .secondaryLabelColor
        }
        let version = NSTextField(labelWithString: "Malachi Mail \(Version.full)")
        version.textColor = .secondaryLabelColor

        let stack = NSStackView(views: [statusLabel, socketLabel, dataLabel, mcpLabel, version])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 8
        stack.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 20, right: 20)
        stack.translatesAutoresizingMaskIntoConstraints = false
        let content = NSView()
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: content.topAnchor),
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor),
        ])
        w.contentView = content
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func show(_ status: ConnectionStatus) {
        statusLabel.stringValue = status.text
    }

    /// Starts the connect loop: every `reconnectInterval` while not
    /// connected, make sure a daemon runs, dial, and ask for system.info.
    func attach(client: RPCClient, supervisor: DaemonSupervisor) {
        self.client = client
        self.supervisor = supervisor
        let onState: @Sendable (RPCClient.State) -> Void = { [weak self] state in
            Task { @MainActor in
                if case .disconnected(let reason) = state {
                    self?.show(.unavailable(reason ?? "not connected"))
                }
            }
        }
        Task {
            await client.setStateHandler(onState)
        }
        loop = Task { [weak self] in
            while !Task.isCancelled {
                await self?.reconnectIfNeeded()
                try? await Task.sleep(for: MainWindowController.reconnectInterval)
            }
        }
    }

    func stopReconnecting() {
        loop?.cancel()
        loop = nil
        show(.stopping)
    }

    private func reconnectIfNeeded() async {
        guard let client else { return }
        if case .connected = await client.state {
            return
        }
        show(.connecting)
        if let supervisor {
            do {
                try await supervisor.ensure()
            } catch {
                show(.unavailable("\(error)"))
                return
            }
        }
        do {
            try await client.connect()
        } catch {
            show(.unavailable("\(error)"))
            return
        }
        do {
            let info: SystemInfo = try await client.call(API.systemInfo, EmptyParams(), timeout: Self.infoTimeout)
            if info.protocolVersion == API.protocolVersion {
                show(.connected(info))
            } else {
                show(.protocolMismatch(daemon: info.protocolVersion))
            }
        } catch {
            show(.infoFailed("\(error)"))
        }
    }
}
