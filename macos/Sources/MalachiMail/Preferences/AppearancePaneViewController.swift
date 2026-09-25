// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Appearance page of the settings (preferences.blp `appearance_page`,
/// ui/internal/window/preferences.go): every group and row of the
/// Blueprint, each bound two-way to its `Settings` key. The consumers are
/// elsewhere and live: the colour scheme in `AppearanceController`, the
/// list rows in `MessageListViewController`, the grouping in
/// `MailboxController`, the body font and zoom in `MessageViewController`.
///
/// The settings come with `init` or later through `configure`; the
/// bindings start once they and the view are there and end when the
/// window closes (the GTK dialog's `closed`), which the pane notices
/// itself.
@MainActor
final class AppearancePaneViewController: PreferencesPaneViewController {
    // Theme
    let colorScheme = NSPopUpButton(frame: .zero, pullsDown: false)
    // Message List
    let density = NSPopUpButton(frame: .zero, pullsDown: false)
    let groupByConversation = NSSwitch()
    let showPreviewLine = NSSwitch()
    let showAvatars = NSSwitch()
    let monochromeAvatars = NSSwitch()
    // Message View
    let monospacePlainText = NSSwitch()
    let textZoom = PrefsSpinControl(min: Settings.textZoomMin, max: Settings.textZoomMax, step: Settings.textZoomStep, value: 100)

    /// The window closed (or the lead said so): bindings undone.
    var closed: Bool {
        get { bindings.closed }
        set {
            if newValue {
                bindings.close()
            }
        }
    }

    private var settings: Settings?
    private let bindings = PreferenceBindingSet()
    private var bound = false

    init(settings: Settings? = nil) {
        self.settings = settings
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Supplies the settings; may be called before or after the view
    /// loaded. A second call after the bindings started is ignored.
    func configure(settings: Settings) {
        guard !bound else { return }
        self.settings = settings
        bindIfReady()
    }

    override func loadView() {
        super.loadView()

        let theme = PreferencesGroupView(title: L10n.T("Theme"))
        colorScheme.addItems(withTitles: [L10n.T("Follow System"), L10n.T("Light"), L10n.T("Dark")])
        theme.setRows([
            PreferenceRowView(title: L10n.T("Color Scheme"), trailing: colorScheme),
        ])
        addGroup(theme)

        let list = PreferencesGroupView(title: L10n.T("Message List"))
        density.addItems(withTitles: [L10n.T("Comfortable"), L10n.T("Compact")])
        showPreviewLine.state = .on
        showAvatars.state = .on
        list.setRows([
            PreferenceRowView(title: L10n.T("Density"), trailing: density),
            PreferenceRowView(title: L10n.T("Group by Conversation"), subtitle: L10n.T("One row per conversation; expand it to see its messages"), trailing: groupByConversation),
            PreferenceRowView(title: L10n.T("Show Preview Line"), trailing: showPreviewLine),
            PreferenceRowView(title: L10n.T("Show Avatars"), trailing: showAvatars),
            PreferenceRowView(title: L10n.T("Monochrome Avatars"), subtitle: L10n.T("Neutral grey instead of a colour per sender"), trailing: monochromeAvatars),
        ])
        addGroup(list)

        let messageView = PreferencesGroupView(title: L10n.T("Message View"))
        messageView.setRows([
            PreferenceRowView(title: L10n.T("Use Monospace Font for Plain Text"), trailing: monospacePlainText),
            PreferenceRowView(title: L10n.T("Text Zoom"), subtitle: L10n.T("Percent"), trailing: textZoom),
        ])
        addGroup(messageView)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        bindIfReady()
    }

    /// The bindings end with the window.
    override func viewWillAppear() {
        super.viewWillAppear()
        bindings.watch(view.window)
    }

    // MARK: Bindings

    private func bindIfReady() {
        guard !bound, isViewLoaded, !closed, let settings else { return }
        bound = true
        bindings.add(.bind(colorScheme, to: settings, .colorScheme, choices: colorSchemeChoices, \.colorScheme))
        bindings.add(.bind(density, to: settings, .density, choices: densityChoices, \.density))
        bindings.add(.bind(groupByConversation, to: settings, .groupByConversation, \.groupByConversation))
        bindings.add(.bind(showPreviewLine, to: settings, .showPreviewLine, \.showPreviewLine))
        bindings.add(.bind(showAvatars, to: settings, .showAvatars, \.showAvatars))
        bindings.add(.bind(monochromeAvatars, to: settings, .monochromeAvatars, \.monochromeAvatars))
        bindings.add(.bind(monospacePlainText, to: settings, .monospacePlainText, \.monospacePlainText))
        bindings.add(.bind(textZoom, to: settings, .textZoom, \.textZoom))
    }
}
