// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The remote-image bar above a message (window.blp `remote_bar`,
/// window/remote.go): how many remote images the sanitiser removed, a
/// button that loads them through the daemon for this message, one that
/// trusts the sender, and the spinner that stands in for the buttons while
/// the images are on their way. Adw.Banner has room for one button, hence a
/// bar of its own. The buttons are small bordered ones (D6 of the plan).
@MainActor
final class RemoteBarView: NSView {
    /// Load Images was clicked.
    var onLoad: (@MainActor () -> Void)?
    /// Always From This Sender was clicked.
    var onTrust: (@MainActor () -> Void)?

    /// Whether the bar has the trust button at all (an attached message's
    /// window has not: the containing message's sender decides the policy).
    let showsTrust: Bool

    var text: String {
        get { label.stringValue }
        set { label.stringValue = newValue }
    }

    private let spinner = Spinner(size: 16)
    private let label = NSTextField(wrappingLabelWithString: "")
    private let loadButton: NSButton
    private let trustButton: NSButton

    init(showsTrust: Bool) {
        self.showsTrust = showsTrust
        loadButton = NSButton(title: mn(L10n.T("Load _Images")), target: nil, action: nil)
        trustButton = NSButton(title: mn(L10n.T("Always From This _Sender")), target: nil, action: nil)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isHidden = true

        label.font = Typo.body
        label.isSelectable = false
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)

        for b in [loadButton, trustButton] {
            b.bezelStyle = .rounded
            b.controlSize = .small
            b.font = NSFont.systemFont(ofSize: NSFont.systemFontSize(for: .small))
            // Clicking these must not move the keyboard focus into a bar
            // that is about to disappear (message_view.go `newMessageView`).
            b.refusesFirstResponder = true
            b.target = self
            b.setContentHuggingPriority(.required, for: .horizontal)
            b.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        loadButton.action = #selector(loadClicked(_:))
        trustButton.action = #selector(trustClicked(_:))
        trustButton.toolTip = L10n.T("Load remote images now and whenever this sender writes")
        trustButton.isHidden = !showsTrust
        spinner.isHidden = true

        let stack = NSStackView(views: [spinner, label, loadButton, trustButton])
        stack.orientation = .horizontal
        stack.distribution = .fill
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        spinner.setContentHuggingPriority(.required, for: .horizontal)
        stack.alignment = .centerY
        stack.spacing = 12
        stack.edgeInsets = NSEdgeInsets(top: 6, left: 12, bottom: 6, right: 12)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        Tint.remoteBar.setFill()
        // Only the view's own area: since macOS 14 a view does not clip
        // to its bounds by default and dirtyRect can reach past them, so
        // filling it painted over the views beside and below this one.
        dirtyRect.intersection(bounds).fill()
    }

    /// Switches between offering the images and showing that they are on
    /// their way: the buttons make way for the spinner (message_view.go
    /// `setBarLoading`, the widget half).
    func setLoading(_ loading: Bool) {
        spinner.isHidden = !loading
        if loading {
            spinner.start()
        } else {
            spinner.stop()
        }
        loadButton.isHidden = loading
        trustButton.isHidden = loading || !showsTrust
    }

    /// Whether the keyboard focus is inside the bar.
    func holdsFirstResponder(of window: NSWindow) -> Bool {
        guard let fr = window.firstResponder as? NSView else { return false }
        return fr === self || fr.isDescendant(of: self)
    }

    @objc private func loadClicked(_ sender: Any?) {
        onLoad?()
    }

    @objc private func trustClicked(_ sender: Any?) {
        onTrust?()
    }
}
