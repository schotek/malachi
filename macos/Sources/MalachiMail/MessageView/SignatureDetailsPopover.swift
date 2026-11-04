// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The details of a message's signature verdict as a popover under its
/// badge (plan §6, ui/internal/signature `DetailsFor`): the verdict and why,
/// who signed, the certificate chain or the OpenPGP key, the notes, and the
/// buttons Check Revocation, Import Key and Confirm Key…. All the content
/// is in `SignatureController.model`; this draws it. Everything that comes
/// from a certificate, a key or a signed header is plain text
/// (`stringValue`), selectable, never interpreted. Nothing is verified
/// here, nothing but the daemon's word is shown.
@MainActor
enum SignatureDetailsPopover {
    /// The open popover; opening another closes it.
    private static var current: Presenter?

    /// Shows the details of message `s`, the verdict `summary` (message.get's)
    /// meanwhile, under `anchor`. The confirmation of a key is asked on
    /// `window`, the toasts go over it.
    static func show(
        _ s: MessageSummary, summary: SignatureSummary?, relativeTo anchor: NSView, state: AppState, window: NSWindow?,
        seed: SignatureDetails? = nil
    ) {
        current?.popover.close()
        let controller = SignatureController(
            client: state.client, messageID: s.id, from: s.from.first?.address ?? "", fallback: summary)
        if let seed {
            controller.seed(seed)
        }
        let p = Presenter(controller: controller, state: state, window: window)
        current = p
        p.show(relativeTo: anchor)
    }

    /// Closes the open popover (a development aid).
    static func close() {
        current?.popover.close()
    }

    /// The open popover's content (a development aid).
    static var shownView: NSView? { current?.popover.contentViewController?.view }

    private static func closed(_ p: Presenter) {
        if current === p {
            current = nil
        }
    }

    // MARK: The presenter

    @MainActor
    private final class Presenter: NSObject, NSPopoverDelegate {
        let popover = NSPopover()
        let controller: SignatureController
        private let viewController: SignatureDetailsViewController
        private var token: BoardObserverToken?

        init(controller: SignatureController, state: AppState, window: NSWindow?) {
            self.controller = controller
            viewController = SignatureDetailsViewController(controller: controller)
            super.init()
            popover.behavior = .transient
            popover.animates = true
            popover.contentViewController = viewController
            popover.delegate = self
            let toasts = state.toasts
            controller.toast = { [weak window] text in windowToast(text, in: window, or: toasts) }
            controller.confirm = { [weak window] q, fingerprint in
                await state.alerts.confirmKey(
                    on: window, heading: q.heading, body: q.body, fingerprint: fingerprint,
                    confirmLabel: q.accept, declineLabel: q.cancel)
            }
            controller.onChange = { [weak self] in self?.viewController.update() }
            // Trust changed (a key imported or confirmed, a check): the
            // popover asks again.
            token = state.signatures.observe { [weak controller] in controller?.load() }
        }

        func show(relativeTo anchor: NSView) {
            viewController.update()
            controller.load()
            popover.show(relativeTo: anchor.bounds, of: anchor, preferredEdge: .maxY)
        }

        func popoverDidClose(_ notification: Foundation.Notification) {
            token?.cancel()
            token = nil
            SignatureDetailsPopover.closed(self)
        }
    }
}

/// The content of the popover: a heading with the verdict's icon, the
/// paragraph that explains it, the rows, the notes, the sections, the
/// caveat and the buttons, in a scroll view that keeps the popover below
/// 520 points.
@MainActor
final class SignatureDetailsViewController: NSViewController {
    private let controller: SignatureController
    private let stack = NSStackView()
    private let scroll = NSScrollView()

    static let width: CGFloat = 380
    static let maxHeight: CGFloat = 520
    private static let inset: CGFloat = 16
    private static let keyWidth: CGFloat = 104
    private static let valueWidth: CGFloat = width - 2 * inset - keyWidth - 12

    init(controller: SignatureController) {
        self.controller = controller
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: Self.inset, left: Self.inset, bottom: Self.inset, right: Self.inset)
        stack.translatesAutoresizingMaskIntoConstraints = false
        let document = SignatureFlippedView()
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: document.topAnchor),
            stack.bottomAnchor.constraint(equalTo: document.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: document.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: document.trailingAnchor),
        ])
        scroll.documentView = document
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.autohidesScrollers = true
        scroll.translatesAutoresizingMaskIntoConstraints = true
        NSLayoutConstraint.activate([
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
        ])
        view = scroll
        scroll.setFrameSize(NSSize(width: Self.width, height: 200))
    }

    /// Draws `controller.model` again.
    func update() {
        _ = view
        for v in stack.arrangedSubviews {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        let m = controller.model
        guard m.severity != .none else {
            let l = wrapping(SignatureView.dialogTitle, font: Typo.heading)
            stack.addArrangedSubview(l)
            resize()
            return
        }
        stack.addArrangedSubview(heading(m))
        if !m.explanation.isEmpty {
            stack.addArrangedSubview(wrapping(m.explanation, font: Typo.body))
        }
        if !m.rows.isEmpty {
            stack.addArrangedSubview(grid(m.rows))
        }
        for n in m.notes {
            stack.addArrangedSubview(wrapping(n, font: Typo.caption, color: Tint.secondary))
        }
        for s in m.sections {
            stack.addArrangedSubview(section(s))
        }
        if !m.caveat.isEmpty {
            stack.addArrangedSubview(wrapping(m.caveat, font: Typo.caption, color: Tint.secondary))
        }
        for b in m.buttons {
            stack.addArrangedSubview(button(b, caption: b.action == .checkRevocation ? m.revocationCaption : ""))
        }
        resize()
    }

    private func resize() {
        stack.layoutSubtreeIfNeeded()
        let h = min(stack.fittingSize.height, Self.maxHeight)
        preferredContentSize = NSSize(width: Self.width, height: h)
    }

    // MARK: Parts

    private func heading(_ m: SignatureView.Details) -> NSView {
        let colour = SignatureTint.color(m.severity)
        let icon = NSImageView(image: SignatureTint.image(m.icon, size: .regular, description: m.title))
        icon.contentTintColor = colour
        icon.setContentHuggingPriority(.required, for: .horizontal)
        let title = NSTextField(wrappingLabelWithString: m.title)
        title.font = Typo.heading
        title.textColor = colour
        title.isSelectable = true
        title.allowsEditingTextAttributes = false
        title.preferredMaxLayoutWidth = Self.width - 2 * Self.inset - 28
        let row = NSStackView(views: [icon, title])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        return row
    }

    private func wrapping(_ text: String, font: NSFont, color: NSColor = .labelColor) -> NSTextField {
        let l = NSTextField(wrappingLabelWithString: text)
        l.font = font
        l.textColor = color
        l.isSelectable = true
        l.allowsEditingTextAttributes = false
        l.preferredMaxLayoutWidth = Self.width - 2 * Self.inset
        return l
    }

    /// Dim labels and selectable values; a fingerprint monospaced.
    private func grid(_ rows: [SignatureView.Row]) -> NSView {
        let g = NSGridView()
        g.rowSpacing = 6
        g.columnSpacing = 12
        g.translatesAutoresizingMaskIntoConstraints = false
        for r in rows {
            let key = NSTextField(wrappingLabelWithString: r.label)
            key.font = Typo.caption
            key.textColor = Tint.secondary
            key.isSelectable = false
            key.preferredMaxLayoutWidth = Self.keyWidth
            let value = NSTextField(wrappingLabelWithString: r.value)
            value.isSelectable = true
            value.allowsEditingTextAttributes = false
            value.font = r.monospace
                ? .monospacedSystemFont(ofSize: Typo.captionSize, weight: .regular) : Typo.caption
            value.preferredMaxLayoutWidth = Self.valueWidth
            g.addRow(with: [key, value])
        }
        if g.numberOfColumns == 2 {
            g.column(at: 0).xPlacement = .leading
            g.column(at: 0).width = Self.keyWidth
            g.column(at: 1).xPlacement = .leading
            g.column(at: 1).width = Self.valueWidth
        }
        g.rowAlignment = .firstBaseline
        return g
    }

    private func section(_ s: SignatureView.Section) -> NSView {
        let title = NSTextField(labelWithString: s.title)
        title.font = Typo.heading
        title.isSelectable = false
        let box = NSStackView(views: [title, grid(s.rows)])
        box.orientation = .vertical
        box.alignment = .leading
        box.spacing = 6
        return box
    }

    private func button(_ b: SignatureView.Button, caption: String) -> NSView {
        let btn = NSButton(title: b.label, target: self, action: #selector(buttonClicked(_:)))
        btn.bezelStyle = .rounded
        btn.controlSize = .small
        btn.tag = b.action.rawValue
        btn.isEnabled = b.enabled
        btn.setAccessibilityLabel(b.label)
        guard !caption.isEmpty else { return btn }
        let note = wrapping(caption, font: Typo.caption, color: Tint.secondary)
        let box = NSStackView(views: [btn, note])
        box.orientation = .vertical
        box.alignment = .leading
        box.spacing = 4
        return box
    }

    @objc private func buttonClicked(_ sender: NSButton) {
        guard let action = SignatureView.Action(rawValue: sender.tag) else { return }
        controller.perform(action)
    }
}

/// A view whose origin is at the top, for the scroll view's document.
private final class SignatureFlippedView: NSView {
    override var isFlipped: Bool { true }
}
