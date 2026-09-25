// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The AI page of the settings (preferences.blp `ai_page`,
/// ui/internal/window/preferences.go `bindMCP`): the MCP group with its one
/// switch, "Register with Claude", which puts the bundled `malachi-mcp`
/// bridge into the MCP configuration of Claude Desktop and Claude Code (or
/// takes it out) through `MCPRegistrationController`. The row is
/// insensitive until the bridge answered `status` and while a call runs;
/// the switch shows what the bridge last confirmed and flips back when a
/// call fails, with a toast that says why.
///
/// The bridge path (or its absence) and the toast sink come through
/// `configure`; the controller exists once they and the view are there,
/// and the status is asked every time the page comes up. Everything ends
/// when the window closes (the GTK dialog's `closed`), which the pane
/// notices itself.
@MainActor
final class AIPaneViewController: PreferencesPaneViewController {
    let registerSwitch = NSSwitch()
    let mcpGroup = PreferencesGroupView(
        title: L10n.T("MCP"),
        description: L10n.T("Lets AI assistants read your mail and prepare drafts through the Model Context Protocol.")
    )

    /// The MCP controller; nil until `configure` was called and the view
    /// loaded.
    private(set) var registration: MCPRegistrationController?
    /// The window closed (or the lead said so): late replies dropped.
    var closed: Bool {
        get { bindings.closed }
        set {
            if newValue {
                bindings.close()
            }
        }
    }

    private var bridge: String?
    private var toast: (@MainActor (String) -> Void)?
    private var configured = false
    private let bindings = PreferenceBindingSet()
    private var registerRow: PreferenceRowView?
    /// The switch is being set from the controller, not by the user.
    private var syncing = false

    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Supplies the bridge path (`Paths.mcpBridge`; nil when there is none
    /// beside the application) and the toast sink; may be called before or
    /// after the view loaded. A second call after the controller started
    /// is ignored.
    func configure(bridge: String?, toast: @escaping @MainActor (String) -> Void) {
        guard registration == nil else { return }
        self.bridge = bridge
        self.toast = toast
        configured = true
        bindIfReady()
    }

    override func loadView() {
        super.loadView()
        let row = PreferenceRowView(
            title: L10n.T("Register with Claude"),
            subtitle: L10n.T("Adds the malachi-mcp bridge to Claude Desktop and Claude Code on this computer"),
            trailing: registerSwitch
        )
        row.isEnabled = false
        registerRow = row
        mcpGroup.setRows([row])
        addGroup(mcpGroup)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        bindIfReady()
    }

    /// The controller ends with the window.
    override func viewWillAppear() {
        super.viewWillAppear()
        bindings.watch(view.window)
    }

    /// The status is asked whenever the page comes up.
    override func viewDidAppear() {
        super.viewDidAppear()
        registration?.load()
    }

    // MARK: Binding (preferences.go `bindMCP`)

    private func bindIfReady() {
        guard registration == nil, isViewLoaded, !closed, configured else { return }
        let c = MCPRegistrationController(bridge: bridge)
        registration = c
        // The row's sensitivity covers the switch inside it.
        c.onEnabled = { [weak self] on in
            self?.registerRow?.isEnabled = on
        }
        c.onRegistered = { [weak self] on in
            self?.setSwitch(on)
        }
        c.onToast = { [weak self] text in
            self?.toast?(text)
        }
        registerRow?.isEnabled = c.isEnabled
        setSwitch(c.isRegistered)
        registerSwitch.target = self
        registerSwitch.action = #selector(registerChanged(_:))
        bindings.onClose = { [weak self] in
            self?.registration?.close()
        }
    }

    private func setSwitch(_ on: Bool) {
        syncing = true
        registerSwitch.state = on ? .on : .off
        syncing = false
    }

    @objc private func registerChanged(_ sender: Any?) {
        guard !syncing, let registration else { return }
        registration.set(registered: registerSwitch.state == .on)
    }
}
