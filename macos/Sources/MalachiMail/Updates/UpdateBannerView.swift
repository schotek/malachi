// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The update check's banner at the top of the main window, in every mode
/// (`Updates.bannerFor`): a Liquid Glass card floating under the toolbar
/// (a `CalloutCard` before macOS 26), with a
/// symbol (a warning for a security release), the title as plain text that
/// wraps (a notice may be long) and the banner's buttons in their order, the
/// first one the default (tinted, no key equivalent: Return belongs to the
/// window's own controls). It follows the application's `UpdateController`
/// and hides itself when there is nothing to show.
@MainActor
final class UpdateBannerView: NSView {
    /// A button was clicked, with the banner it belongs to.
    var onAction: (@MainActor (Updates.Action, Updates.Banner) -> Void)?
    /// Called after the banner was shown or hidden.
    var onShownChange: (@MainActor (Bool) -> Void)?

    private(set) var banner = Updates.Banner()
    private let symbolView = CalloutCard.symbolView()
    private let titleLabel = NSTextField(wrappingLabelWithString: "")
    private let buttons = NSStackView()
    private var token: BoardObserverToken?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isHidden = true

        titleLabel.font = Typo.body
        titleLabel.isSelectable = false
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        titleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)

        buttons.orientation = .horizontal
        buttons.spacing = 6
        buttons.setContentHuggingPriority(.required, for: .horizontal)
        buttons.setContentCompressionResistancePriority(.required, for: .horizontal)

        let stack = NSStackView(views: [symbolView, titleLabel, buttons])
        stack.orientation = .horizontal
        stack.distribution = .fill
        stack.alignment = .centerY
        stack.spacing = CalloutCard.spacing
        if #available(macOS 26.0, *) {
            installGlass(stack)
        } else {
            CalloutCard.install(stack, in: self)
        }
    }

    /// The card as Liquid Glass, like the toolbar above it.
    @available(macOS 26.0, *)
    private func installGlass(_ content: NSView) {
        let holder = NSView()
        holder.translatesAutoresizingMaskIntoConstraints = false
        content.translatesAutoresizingMaskIntoConstraints = false
        holder.addSubview(content)
        let glass = NSGlassEffectView()
        glass.translatesAutoresizingMaskIntoConstraints = false
        glass.cornerRadius = 16
        glass.contentView = holder
        addSubview(glass)
        let m = NSEdgeInsets(top: 8, left: 12, bottom: 4, right: 12)
        let p = NSEdgeInsets(top: 8, left: 14, bottom: 8, right: 10)
        NSLayoutConstraint.activate([
            glass.topAnchor.constraint(equalTo: topAnchor, constant: m.top),
            bottomAnchor.constraint(equalTo: glass.bottomAnchor, constant: m.bottom),
            glass.leadingAnchor.constraint(equalTo: leadingAnchor, constant: m.left),
            trailingAnchor.constraint(equalTo: glass.trailingAnchor, constant: m.right),
            content.topAnchor.constraint(equalTo: holder.topAnchor, constant: p.top),
            holder.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: p.bottom),
            content.leadingAnchor.constraint(equalTo: holder.leadingAnchor, constant: p.left),
            holder.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: p.right),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Follows `updates` from now on.
    func bind(_ updates: UpdateController) {
        token?.cancel()
        token = updates.observe { [weak self, weak updates] in
            self?.show(Updates.bannerFor(updates?.status, now: Date()))
        }
        show(Updates.bannerFor(updates.status, now: Date()))
    }

    /// Shows `b` (hidden when it is none).
    func show(_ b: Updates.Banner) {
        guard b != banner || isHidden == b.shown else { return }
        banner = b
        switch b.kind {
        case .security:
            CalloutCard.show("exclamationmark.shield", .warning, in: symbolView)
        case .update:
            CalloutCard.show("arrow.down.circle", .info, in: symbolView)
        case .notice, .none:
            CalloutCard.show("info.circle", .info, in: symbolView)
        }
        titleLabel.stringValue = b.title
        for v in buttons.arrangedSubviews {
            buttons.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        for (i, button) in b.buttons.enumerated() {
            let v = NSButton(title: button.label, target: self, action: #selector(clicked(_:)))
            v.bezelStyle = .push
            v.controlSize = .regular
            v.tag = i
            if i == 0 {
                v.bezelColor = .controlAccentColor
            }
            v.setContentCompressionResistancePriority(.required, for: .horizontal)
            buttons.addArrangedSubview(v)
        }
        let shown = b.shown
        if isHidden == shown {
            isHidden = !shown
            onShownChange?(shown)
        }
    }

    @objc private func clicked(_ sender: NSButton) {
        guard sender.tag >= 0, sender.tag < banner.buttons.count else { return }
        onAction?(banner.buttons[sender.tag].action, banner)
    }
}
