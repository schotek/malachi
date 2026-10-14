// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What Archive did, with Undo (`Board.ArchiveOutcome`: its `text` and
/// `undoLabel`; the button calls `onUndo`), as a capsule at the bottom of
/// the board page, where the window's toasts show (`ToastPresenter`, which
/// has no button): Liquid Glass from macOS 26, a dark HUD before. It stays
/// `seconds` (an Adw.Toast with a button), a click on Undo ends it, and the
/// page takes it away when another toast comes. Only the capsule takes
/// clicks. Every text is set through `stringValue`.
@MainActor
final class BoardUndoToast: NSView {
    static let seconds = 8
    private static let height = ToastPresenter.capsuleHeight

    private let capsule: NSView
    private let label = NSTextField(labelWithString: "")
    private let button = NSButton()
    private var dismissal: DispatchWorkItem?
    private var onUndo: (@MainActor () -> Void)?

    init() {
        let content = NSView()
        content.translatesAutoresizingMaskIntoConstraints = false
        if #available(macOS 26, *) {
            let glass = NSGlassEffectView()
            glass.cornerRadius = Self.height / 2
            glass.contentView = content
            capsule = glass
        } else {
            let hud = NSVisualEffectView()
            hud.material = .hudWindow
            hud.blendingMode = .withinWindow
            hud.state = .active
            hud.appearance = NSAppearance(named: .darkAqua)
            hud.wantsLayer = true
            hud.layer?.cornerRadius = Self.height / 2
            hud.layer?.masksToBounds = true
            hud.addSubview(content)
            capsule = hud
        }
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        capsule.translatesAutoresizingMaskIntoConstraints = false
        capsule.isHidden = true

        label.font = Typo.body
        label.textColor = .labelColor
        label.lineBreakMode = .byTruncatingTail
        label.maximumNumberOfLines = 1
        label.isSelectable = false
        label.translatesAutoresizingMaskIntoConstraints = false
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        button.bezelStyle = .push
        button.controlSize = .regular
        button.target = self
        button.action = #selector(undoClicked(_:))
        button.translatesAutoresizingMaskIntoConstraints = false
        button.setContentCompressionResistancePriority(.required, for: .horizontal)
        content.addSubview(label)
        content.addSubview(button)
        addSubview(capsule)
        NSLayoutConstraint.activate([
            capsule.heightAnchor.constraint(equalToConstant: Self.height),
            capsule.centerXAnchor.constraint(equalTo: centerXAnchor),
            capsule.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -ToastPresenter.bottomMargin),
            capsule.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor, constant: -24),
            content.topAnchor.constraint(equalTo: capsule.topAnchor),
            content.bottomAnchor.constraint(equalTo: capsule.bottomAnchor),
            content.leadingAnchor.constraint(equalTo: capsule.leadingAnchor),
            content.trailingAnchor.constraint(equalTo: capsule.trailingAnchor),
            label.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 18),
            label.centerYAnchor.constraint(equalTo: content.centerYAnchor),
            button.leadingAnchor.constraint(equalTo: label.trailingAnchor, constant: 12),
            content.trailingAnchor.constraint(equalTo: button.trailingAnchor, constant: 8),
            button.centerYAnchor.constraint(equalTo: content.centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Whether the capsule shows.
    var isShowing: Bool { !capsule.isHidden }

    /// Shows `text` with the button `undo`; a toast that showed goes.
    func show(_ text: String, undo: String, onUndo: @escaping @MainActor () -> Void) {
        dismissal?.cancel()
        self.onUndo = onUndo
        label.stringValue = text
        button.title = undo
        button.setAccessibilityLabel(undo)
        capsule.setAccessibilityElement(true)
        capsule.setAccessibilityRole(.group)
        capsule.setAccessibilityLabel(text)
        capsule.isHidden = false
        capsule.alphaValue = 1
        let work = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated {
                self?.dismiss()
            }
        }
        dismissal = work
        DispatchQueue.main.asyncAfter(deadline: .now() + .seconds(Self.seconds), execute: work)
        NSAccessibility.post(
            element: capsule, notification: .announcementRequested,
            userInfo: [.announcement: text, .priority: NSAccessibilityPriorityLevel.medium.rawValue])
    }

    /// Takes the capsule away (its Undo is no longer offered).
    func dismiss() {
        dismissal?.cancel()
        dismissal = nil
        onUndo = nil
        capsule.isHidden = true
    }

    @objc private func undoClicked(_ sender: Any?) {
        let undo = onUndo
        dismiss()
        undo?()
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        guard !capsule.isHidden else { return nil }
        let p = convert(point, from: superview)
        guard capsule.frame.contains(p) else { return nil }
        return super.hitTest(point) ?? capsule
    }
}
