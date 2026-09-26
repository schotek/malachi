// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.Banner in the Mac's own form: a `CalloutCard` with an SF Symbol,
/// the title in the regular weight and at most one button, revealed and
/// hidden with a short animation. Meant for a vertical `NSStackView`,
/// whose animator hides arranged views smoothly.
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

    private let symbolView = CalloutCard.symbolView()
    private let titleLabel = NSTextField(wrappingLabelWithString: "")
    private let button = NSButton(title: "", target: nil, action: nil)

    /// - Parameters:
    ///   - symbol: the SF Symbol at the start, `severity` its colour; a
    ///     banner whose meaning changes sets them with `setSymbol`.
    init(title: String = "", buttonTitle: String? = nil, symbol: String = "info.circle",
         severity: CalloutSeverity = .info) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        isHidden = true
        setSymbol(symbol, severity)

        titleLabel.font = Typo.body
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

        let stack = NSStackView(views: [symbolView, titleLabel, button])
        stack.orientation = .horizontal
        stack.distribution = .fill
        stack.alignment = .centerY
        stack.spacing = CalloutCard.spacing
        CalloutCard.install(stack, in: self)
    }

    /// The symbol at the start and its colour.
    func setSymbol(_ name: String, _ severity: CalloutSeverity) {
        CalloutCard.show(name, severity, in: symbolView)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
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
