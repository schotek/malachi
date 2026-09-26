// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.Banner: a tinted strip with a bold title and at most one button,
/// revealed and hidden with a short animation. Meant for a vertical
/// `NSStackView`, whose animator hides arranged views smoothly.
@MainActor
final class BannerView: NSView {
    static let revealDuration: TimeInterval = 0.2

    var title: String {
        get { titleLabel.stringValue }
        set { titleLabel.stringValue = newValue }
    }

    /// The button's title; nil or empty hides the button (Adw.Banner
    /// without `button-label`).
    var buttonTitle: String? {
        didSet {
            let t = buttonTitle ?? ""
            button.title = t
            button.isHidden = t.isEmpty
        }
    }

    /// Called when the button is clicked.
    var onButton: (@MainActor () -> Void)?

    /// Whether the banner is shown (`revealed`).
    private(set) var isRevealed = false

    private let titleLabel = NSTextField(wrappingLabelWithString: "")
    private let button = NSButton(title: "", target: nil, action: nil)

    init(title: String = "", buttonTitle: String? = nil) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isHidden = true

        titleLabel.font = Typo.heading
        titleLabel.isSelectable = false
        titleLabel.stringValue = title
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        titleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)

        button.bezelStyle = .rounded
        button.controlSize = .small
        button.font = NSFont.systemFont(ofSize: NSFont.systemFontSize(for: .small))
        button.target = self
        button.action = #selector(buttonClicked(_:))
        button.setContentHuggingPriority(.required, for: .horizontal)
        button.setContentCompressionResistancePriority(.required, for: .horizontal)
        defer { self.buttonTitle = buttonTitle }

        let stack = NSStackView(views: [titleLabel, button])
        stack.orientation = .horizontal
        stack.distribution = .fill
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
        Tint.banner.setFill()
        // Only the view's own area: since macOS 14 a view does not clip
        // to its bounds by default and dirtyRect can reach past them, so
        // filling it painted over the views beside and below this one.
        dirtyRect.intersection(bounds).fill()
    }

    /// Shows or hides the banner, animated over `revealDuration` unless
    /// `animated` is false.
    func reveal(_ on: Bool, animated: Bool = true) {
        guard on != isRevealed || isHidden == on else { return }
        isRevealed = on
        guard animated, window != nil else {
            isHidden = !on
            return
        }
        NSAnimationContext.runAnimationGroup { ctx in
            ctx.duration = Self.revealDuration
            ctx.allowsImplicitAnimation = true
            self.animator().isHidden = !on
        }
    }

    @objc private func buttonClicked(_ sender: Any?) {
        onButton?()
    }
}
