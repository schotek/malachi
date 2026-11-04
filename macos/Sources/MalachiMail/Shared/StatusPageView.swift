// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.StatusPage: an illustration, a title, a description and an optional
/// child, centred in whatever space the page gets. The text is plain and
/// wraps; the content is at most 400 pt wide.
@MainActor
final class StatusPageView: NSView {
    /// What sits above the title.
    enum Illustration: Equatable {
        /// A GTK icon name (`Icon.image`), 96 pt in the secondary colour.
        case icon(String)
        /// An SF Symbol name, 96 pt in the secondary colour.
        case symbol(String)
        /// A 32 pt spinner (the loading pages).
        case spinner
        case none
    }

    static let maxContentWidth: CGFloat = 400
    static let horizontalInset: CGFloat = 36
    static let verticalInset: CGFloat = 12
    /// All below every split-view holding priority (250–260): the labels'
    /// compression resistance, the preferred content width, and the
    /// stack's hugging, in that order of strength.
    static let textPriority = NSLayoutConstraint.Priority(150)
    static let widthPriority = NSLayoutConstraint.Priority(120)
    static let huggingPriority = NSLayoutConstraint.Priority(100)

    var illustration: Illustration {
        didSet { if illustration != oldValue { applyIllustration() } }
    }

    var title: String {
        get { titleLabel.stringValue }
        set { titleLabel.stringValue = newValue }
    }

    var descriptionText: String {
        get { descriptionLabel.stringValue }
        set {
            descriptionLabel.stringValue = newValue
            descriptionLabel.isHidden = newValue.isEmpty
        }
    }

    /// The optional child under the description (a button, a list).
    private(set) var child: NSView?

    private let imageView = NSImageView()
    private let spinner = Spinner(size: 32)
    private let titleLabel = NSTextField(wrappingLabelWithString: "")
    private let descriptionLabel = NSTextField(wrappingLabelWithString: "")
    private let stack = NSStackView()

    init(illustration: Illustration, title: String, description: String, child: NSView? = nil) {
        self.illustration = illustration
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        imageView.imageScaling = .scaleNone
        imageView.contentTintColor = Tint.secondary
        imageView.setContentHuggingPriority(.required, for: .vertical)

        // The text must never push on the pane: a status page fills
        // whatever it gets, so its compression resistance sits below the
        // split view's holding priorities (250–260). Wrapping fields keep
        // `preferredMaxLayoutWidth` at 0: AppKit then measures them at the
        // width the constraints give them, in its second layout pass.
        titleLabel.font = Typo.title1
        titleLabel.alignment = .center
        titleLabel.isSelectable = false
        titleLabel.stringValue = title
        titleLabel.setContentCompressionResistancePriority(Self.textPriority, for: .horizontal)

        descriptionLabel.font = Typo.body
        descriptionLabel.alignment = .center
        descriptionLabel.isSelectable = false
        descriptionLabel.stringValue = description
        descriptionLabel.isHidden = description.isEmpty
        descriptionLabel.setContentCompressionResistancePriority(Self.textPriority, for: .horizontal)

        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 12
        stack.translatesAutoresizingMaskIntoConstraints = false
        // The stack takes the preferred width below rather than hugging
        // its content, so the labels get room to wrap.
        stack.setHuggingPriority(Self.huggingPriority, for: .horizontal)
        stack.addArrangedSubview(imageView)
        stack.addArrangedSubview(spinner)
        stack.addArrangedSubview(titleLabel)
        stack.addArrangedSubview(descriptionLabel)
        stack.setCustomSpacing(36, after: imageView)
        stack.setCustomSpacing(36, after: spinner)
        addSubview(stack)

        // The content width is preferred at a priority below the split
        // view's holding priorities: a real one would make the page demand
        // 400 + insets from its pane (and, through the split view, from
        // the window). The insets are required, so a narrow pane wins.
        let preferred = stack.widthAnchor.constraint(equalToConstant: Self.maxContentWidth)
        preferred.priority = Self.widthPriority
        NSLayoutConstraint.activate([
            preferred,
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: Self.maxContentWidth),
            titleLabel.widthAnchor.constraint(lessThanOrEqualTo: stack.widthAnchor),
            descriptionLabel.widthAnchor.constraint(lessThanOrEqualTo: stack.widthAnchor),
            stack.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor, constant: Self.horizontalInset),
            trailingAnchor.constraint(greaterThanOrEqualTo: stack.trailingAnchor, constant: Self.horizontalInset),
            stack.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: Self.verticalInset),
            bottomAnchor.constraint(greaterThanOrEqualTo: stack.bottomAnchor, constant: Self.verticalInset),
        ])
        setChild(child)
        applyIllustration()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Replaces the child under the description; nil removes it.
    func setChild(_ view: NSView?) {
        if let old = child {
            stack.removeArrangedSubview(old)
            old.removeFromSuperview()
        }
        child = view
        if let view {
            view.translatesAutoresizingMaskIntoConstraints = false
            stack.addArrangedSubview(view)
            stack.setCustomSpacing(24, after: descriptionLabel)
        }
    }

    private func applyIllustration() {
        switch illustration {
        case .icon(let name):
            imageView.image = Icon.image(name, size: .status)
            imageView.isHidden = false
            spinner.isHidden = true
            spinner.stop()
        case .symbol(let name):
            imageView.image = Icon.symbol(name, size: .status)
            imageView.isHidden = false
            spinner.isHidden = true
            spinner.stop()
        case .spinner:
            imageView.isHidden = true
            spinner.isHidden = false
            spinner.start()
        case .none:
            imageView.isHidden = true
            spinner.isHidden = true
            spinner.stop()
        }
    }
}
