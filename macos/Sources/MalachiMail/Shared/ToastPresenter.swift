// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Adw.ToastOverlay: a capsule at the bottom of the view it covers, one
/// toast at a time, the rest queued in order. The default timeout is 5 s;
/// 0 keeps a toast until the next one replaces it (window.go `ToastFor`).
/// A click dismisses the toast; clicks anywhere else pass through.
///
/// Install it over a container with `install(over:)`; it sizes itself to
/// the container and stays on top of later subviews.
@MainActor
final class ToastPresenter: NSView, Toasts {
    static let defaultSeconds = 5
    static let fadeDuration: TimeInterval = 0.2
    static let capsuleHeight: CGFloat = 42
    static let bottomMargin: CGFloat = 24

    private struct Toast {
        let text: String
        let seconds: Int
    }

    private var queue: [Toast] = []
    private var current: Toast?
    private var dismissal: DispatchWorkItem?

    private let capsule: NSView
    private let label = NSTextField(labelWithString: "")

    init() {
        let content = NSView()
        content.translatesAutoresizingMaskIntoConstraints = false
        capsule = Self.makeCapsule(content: content)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        capsule.translatesAutoresizingMaskIntoConstraints = false
        capsule.alphaValue = 0
        capsule.isHidden = true

        label.font = Typo.body
        label.textColor = .labelColor
        label.lineBreakMode = .byTruncatingTail
        label.maximumNumberOfLines = 1
        label.isSelectable = false
        label.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(label)
        addSubview(capsule)

        NSLayoutConstraint.activate([
            capsule.heightAnchor.constraint(equalToConstant: Self.capsuleHeight),
            capsule.centerXAnchor.constraint(equalTo: centerXAnchor),
            capsule.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -Self.bottomMargin),
            capsule.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor, constant: -24),
            content.topAnchor.constraint(equalTo: capsule.topAnchor),
            content.bottomAnchor.constraint(equalTo: capsule.bottomAnchor),
            content.leadingAnchor.constraint(equalTo: capsule.leadingAnchor),
            content.trailingAnchor.constraint(equalTo: capsule.trailingAnchor),
            label.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 18),
            content.trailingAnchor.constraint(equalTo: label.trailingAnchor, constant: 18),
            label.centerYAnchor.constraint(equalTo: content.centerYAnchor),
        ])

        let click = NSClickGestureRecognizer(target: self, action: #selector(capsuleClicked(_:)))
        capsule.addGestureRecognizer(click)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The capsule around `content`: Liquid Glass from macOS 26, as the
    /// system's own floating controls over content, following the
    /// appearance; before that a dark HUD material, the look of an
    /// Adw.Toast.
    private static func makeCapsule(content: NSView) -> NSView {
        if #available(macOS 26, *) {
            let glass = NSGlassEffectView()
            glass.cornerRadius = capsuleHeight / 2
            glass.contentView = content
            return glass
        }
        let hud = NSVisualEffectView()
        hud.material = .hudWindow
        hud.blendingMode = .withinWindow
        hud.state = .active
        hud.appearance = NSAppearance(named: .darkAqua)
        hud.wantsLayer = true
        hud.layer?.cornerRadius = capsuleHeight / 2
        hud.layer?.masksToBounds = true
        hud.addSubview(content)
        return hud
    }

    /// Adds the presenter as the topmost subview of `container`, filling it.
    func install(over container: NSView) {
        container.addSubview(self, positioned: .above, relativeTo: nil)
        NSLayoutConstraint.activate([
            topAnchor.constraint(equalTo: container.topAnchor),
            bottomAnchor.constraint(equalTo: container.bottomAnchor),
            leadingAnchor.constraint(equalTo: container.leadingAnchor),
            trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ])
    }

    // Only the capsule takes clicks; the rest is transparent to the mouse.
    override func hitTest(_ point: NSPoint) -> NSView? {
        guard !capsule.isHidden, capsule.alphaValue > 0 else { return nil }
        let inCapsule = capsule.frame.contains(convert(point, from: superview))
        return inCapsule ? capsule : nil
    }

    // MARK: Toasts

    func show(_ text: String) {
        show(text, seconds: Self.defaultSeconds)
    }

    func show(_ text: String, seconds: Int) {
        let toast = Toast(text: text, seconds: max(seconds, 0))
        // A toast without a timeout gives way to the next one at once.
        if let c = current, c.seconds == 0 {
            replaceCurrent(with: toast)
            return
        }
        if current == nil {
            present(toast)
        } else {
            queue.append(toast)
        }
    }

    /// Hides the current toast now and shows the next queued one, if any.
    func dismissCurrent() {
        guard current != nil else { return }
        dismissal?.cancel()
        dismissal = nil
        current = nil
        fade(in: false) { [weak self] in
            self?.presentNext()
        }
    }

    // MARK: Internals

    private func present(_ toast: Toast) {
        current = toast
        label.stringValue = toast.text
        capsule.toolTip = toast.text
        fade(in: true, then: nil)
        scheduleDismissal(toast)
    }

    private func replaceCurrent(with toast: Toast) {
        dismissal?.cancel()
        dismissal = nil
        current = toast
        label.stringValue = toast.text
        capsule.toolTip = toast.text
        scheduleDismissal(toast)
    }

    private func scheduleDismissal(_ toast: Toast) {
        guard toast.seconds > 0 else { return }
        let work = DispatchWorkItem { [weak self] in
            self?.dismissCurrent()
        }
        dismissal = work
        DispatchQueue.main.asyncAfter(deadline: .now() + .seconds(toast.seconds), execute: work)
    }

    private func presentNext() {
        guard current == nil, !queue.isEmpty else { return }
        present(queue.removeFirst())
    }

    private func fade(in visible: Bool, then completion: (@MainActor () -> Void)?) {
        if visible {
            capsule.isHidden = false
        }
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = Self.fadeDuration
            self.capsule.animator().alphaValue = visible ? 1 : 0
        }, completionHandler: {
            // The completion runs on the main thread; hop onto the actor
            // explicitly so the compiler can see it.
            MainActor.assumeIsolated {
                if !visible, self.current == nil {
                    self.capsule.isHidden = true
                }
                completion?()
            }
        })
    }

    @objc private func capsuleClicked(_ sender: Any?) {
        dismissCurrent()
    }
}
