// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

// The wizard's own small views: a banner, a status page, a toast presenter,
// an entry with an error underline, and the page root that routes Return.
// They stand in for the shell's shared views and may be swapped for them.

/// `AdwBanner` without a button: a tinted strip with a bold title.
@MainActor
final class WizardBannerView: NSView {
    private let label: PrefsWrappingLabel

    var title: String {
        get { label.stringValue }
        set { label.stringValue = newValue }
    }

    init() {
        label = PrefsWrappingLabel("", weight: .bold)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.required, for: .vertical)
        wantsLayer = true
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(label)
        NSLayoutConstraint.activate([
            label.topAnchor.constraint(equalTo: topAnchor, constant: 6),
            label.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -6),
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
        ])
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.controlAccentColor.blended(withFraction: 0.7, of: .windowBackgroundColor)?.cgColor
    }

    /// Shows the banner with `text`, or hides it for nil.
    func reveal(_ text: String?) {
        if let text {
            title = text
            isHidden = false
        } else {
            isHidden = true
        }
    }
}

/// `AdwStatusPage`: a large symbol or a spinner, a title, a description and
/// an optional child, centred.
@MainActor
final class WizardStatusPageView: NSView {
    enum Illustration {
        case symbol(String)
        case spinner
    }

    private let imageView = NSImageView()
    private let spinner = NSProgressIndicator()
    private let titleLabel: PrefsWrappingLabel
    private let descriptionLabel: PrefsWrappingLabel
    private let stack: NSStackView

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

    var symbolName: String? {
        didSet {
            if let symbolName {
                imageView.image = wizardSymbol(symbolName, pointSize: 96)
            }
        }
    }

    init(illustration: Illustration, title: String, description: String?, child: NSView? = nil) {
        titleLabel = PrefsWrappingLabel(title, size: 20, weight: .bold, alignment: .center)
        descriptionLabel = PrefsWrappingLabel(description ?? "", alignment: .center)
        descriptionLabel.isHidden = (description ?? "").isEmpty

        imageView.contentTintColor = .secondaryLabelColor
        imageView.imageScaling = .scaleNone
        spinner.style = .spinning
        spinner.controlSize = .regular
        spinner.isIndeterminate = true
        spinner.isDisplayedWhenStopped = false
        spinner.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            spinner.widthAnchor.constraint(equalToConstant: 32),
            spinner.heightAnchor.constraint(equalToConstant: 32),
        ])

        var views: [NSView] = []
        switch illustration {
        case .symbol(let name):
            imageView.image = wizardSymbol(name, pointSize: 96)
            views.append(imageView)
            spinner.isHidden = true
        case .spinner:
            views.append(spinner)
            imageView.isHidden = true
        }
        views.append(titleLabel)
        views.append(descriptionLabel)
        if let child {
            views.append(child)
        }
        stack = NSStackView(views: views)
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 12
        stack.edgeInsets = NSEdgeInsets(top: 36, left: 12, bottom: 36, right: 12)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        stack.setCustomSpacing(36, after: views[0])
        if let child {
            stack.setCustomSpacing(24, after: descriptionLabel)
            child.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -24).isActive = true
        }
        // The labels take the page's width up to 400 pt; without the
        // (active) equality a wrapping label settles on the width of its
        // first layout, which on a page not yet sized is a narrow column.
        for label in [titleLabel, descriptionLabel] {
            label.widthAnchor.constraint(lessThanOrEqualToConstant: 400).isActive = true
            let fill = label.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -24)
            fill.priority = .defaultHigh
            fill.isActive = true
        }
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        let centre = stack.centerYAnchor.constraint(equalTo: centerYAnchor)
        centre.priority = .defaultLow
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
            stack.topAnchor.constraint(greaterThanOrEqualTo: topAnchor),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor),
            centre,
        ])
        if case .spinner = illustration {
            spinner.startAnimation(nil)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Starts or stops the spinner (a page hidden behind another need not spin).
    func setSpinning(_ on: Bool) {
        if on {
            spinner.startAnimation(nil)
        } else {
            spinner.stopAnimation(nil)
        }
    }
}

/// `AdwToastOverlay` for the wizard sheet and the settings window: a dark
/// capsule at the bottom, one toast at a time, FIFO, 5 s by default (0
/// keeps it until the next one). Clicks pass through.
@MainActor
final class WizardToastPresenter: NSView {
    private var queue: [(text: String, seconds: Int)] = []
    private var current: NSView?
    private var timer: Task<Void, Never>?

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        nil
    }

    /// Lays the presenter over `host`.
    func attach(to host: NSView) {
        host.addSubview(self, positioned: .above, relativeTo: nil)
        NSLayoutConstraint.activate([
            leadingAnchor.constraint(equalTo: host.leadingAnchor),
            trailingAnchor.constraint(equalTo: host.trailingAnchor),
            topAnchor.constraint(equalTo: host.topAnchor),
            bottomAnchor.constraint(equalTo: host.bottomAnchor),
        ])
    }

    func show(_ text: String, seconds: Int = 5) {
        queue.append((text, seconds))
        if current == nil {
            advance()
        }
    }

    private func advance() {
        timer?.cancel()
        timer = nil
        if let current {
            fade(current, in: false) { current.removeFromSuperview() }
            self.current = nil
        }
        guard !queue.isEmpty else { return }
        let (text, seconds) = queue.removeFirst()
        let capsule = makeCapsule(text)
        addSubview(capsule)
        NSLayoutConstraint.activate([
            capsule.centerXAnchor.constraint(equalTo: centerXAnchor),
            capsule.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -24),
            capsule.heightAnchor.constraint(equalToConstant: 42),
            capsule.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor, constant: -48),
        ])
        current = capsule
        capsule.alphaValue = 0
        fade(capsule, in: true, completion: nil)
        if seconds > 0 {
            timer = Task { [weak self] in
                try? await Task.sleep(for: .seconds(seconds))
                guard !Task.isCancelled else { return }
                self?.advance()
            }
        }
    }

    private func makeCapsule(_ text: String) -> NSView {
        let capsule = NSView()
        capsule.translatesAutoresizingMaskIntoConstraints = false
        capsule.wantsLayer = true
        capsule.layer?.cornerRadius = 21
        capsule.layer?.backgroundColor = NSColor(white: 0.16, alpha: 0.94).cgColor
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: 13)
        label.textColor = .white
        label.lineBreakMode = .byTruncatingTail
        label.translatesAutoresizingMaskIntoConstraints = false
        capsule.addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: capsule.leadingAnchor, constant: 18),
            label.trailingAnchor.constraint(equalTo: capsule.trailingAnchor, constant: -18),
            label.centerYAnchor.constraint(equalTo: capsule.centerYAnchor),
        ])
        return capsule
    }

    private func fade(_ view: NSView, in visible: Bool, completion: (@MainActor () -> Void)?) {
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = 0.2
            view.animator().alphaValue = visible ? 1 : 0
        }, completionHandler: {
            // AppKit calls this on the main thread.
            MainActor.assumeIsolated {
                completion?()
            }
        })
    }
}

/// An entry of the wizard's rows: a text field with an error state (red
/// text and a red underline).
@MainActor
final class WizardEntryField: NSView {
    let field: NSTextField
    private let underline = NSView()

    var hasError = false {
        didSet {
            guard hasError != oldValue else { return }
            field.textColor = hasError ? .systemRed : .labelColor
            underline.isHidden = !hasError
        }
    }

    var isEnabled: Bool {
        get { field.isEnabled }
        set { field.isEnabled = newValue }
    }

    init(secure: Bool = false) {
        field = secure ? NSSecureTextField() : NSTextField()
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        field.font = .systemFont(ofSize: 13)
        field.lineBreakMode = .byClipping
        field.usesSingleLineMode = true
        field.cell?.isScrollable = true
        field.translatesAutoresizingMaskIntoConstraints = false
        underline.translatesAutoresizingMaskIntoConstraints = false
        underline.wantsLayer = true
        underline.layer?.backgroundColor = NSColor.systemRed.cgColor
        underline.isHidden = true
        addSubview(field)
        addSubview(underline)
        NSLayoutConstraint.activate([
            field.topAnchor.constraint(equalTo: topAnchor),
            field.leadingAnchor.constraint(equalTo: leadingAnchor),
            field.trailingAnchor.constraint(equalTo: trailingAnchor),
            underline.topAnchor.constraint(equalTo: field.bottomAnchor, constant: 1),
            underline.leadingAnchor.constraint(equalTo: field.leadingAnchor, constant: 1),
            underline.trailingAnchor.constraint(equalTo: field.trailingAnchor, constant: -1),
            underline.heightAnchor.constraint(equalToConstant: 1),
            underline.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }
}

/// The root view of a wizard page. Return pressed while one of the page's
/// text fields edits goes to `returnHandler` before the window's default
/// button can see it (the `AdwEntryRow` `entry-activated` semantics); true
/// consumes the key.
@MainActor
final class WizardPageView: NSView {
    var returnHandler: ((NSTextField) -> Bool)?

    init() {
        super.init(frame: .zero)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if WizardPageView.isPlainReturn(event), let field = editingField(), let handler = returnHandler, handler(field) {
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    /// The text field of this page whose field editor is the first responder.
    private func editingField() -> NSTextField? {
        guard let editor = window?.firstResponder as? NSTextView, editor.isFieldEditor,
              let field = editor.delegate as? NSTextField, field.isDescendant(of: self) else { return nil }
        return field
    }

    static func isPlainReturn(_ event: NSEvent) -> Bool {
        guard event.type == .keyDown, event.keyCode == 36 || event.keyCode == 76 else { return false }
        return event.modifierFlags.intersection([.command, .option, .control, .shift]).isEmpty
    }
}

/// The sheet's Cancel button (macOS convention: a sheet has no close
/// control in its header; Cancel sits at the bottom left and takes
/// Escape). GTK's wizard has the header bar's close button instead.
@MainActor
final class WizardCancelButton: NSButton {
    var onCancel: (() -> Void)?

    init() {
        super.init(frame: .zero)
        title = wizardLabel("_Cancel")
        bezelStyle = .rounded
        controlSize = .large
        keyEquivalent = "\u{1b}"
        target = self
        action = #selector(clicked(_:))
        translatesAutoresizingMaskIntoConstraints = false
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func clicked(_ sender: Any?) {
        onCancel?()
    }
}

/// A button-bar row at the bottom of a page: right-aligned buttons with
/// the Blueprint's margins (12) and spacing (8); `cancel` sits at the
/// leading end.
@MainActor
func wizardButtonBar(_ buttons: [NSButton], cancel: NSButton? = nil) -> NSStackView {
    let bar = NSStackView()
    bar.orientation = .horizontal
    bar.alignment = .centerY
    bar.spacing = 8
    bar.edgeInsets = NSEdgeInsets(top: 12, left: 12, bottom: 12, right: 12)
    bar.setHuggingPriority(.required, for: .vertical)
    if let cancel {
        bar.addView(cancel, in: .leading)
    }
    // The trailing gravity area packs the buttons at the end (`halign: end`).
    for button in buttons {
        bar.addView(button, in: .trailing)
    }
    return bar
}
