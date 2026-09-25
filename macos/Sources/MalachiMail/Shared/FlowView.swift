// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.WrapBox: a horizontal box that wraps its children onto new lines
/// when they do not fit. Children keep their fitting size; the view's
/// intrinsic height follows its width, so it works inside a vertical
/// `NSStackView` (attachment chips under the headers, compose chips).
@MainActor
final class FlowView: NSView {
    /// Space between children on a line (`child-spacing`).
    var spacing: CGFloat {
        didSet { needsLayout = true; invalidateIntrinsicContentSize() }
    }

    /// Space between lines (`line-spacing`).
    var lineSpacing: CGFloat {
        didSet { needsLayout = true; invalidateIntrinsicContentSize() }
    }

    /// The children in order.
    private(set) var views: [NSView] = []
    private var lastWidth: CGFloat = -1

    init(spacing: CGFloat = 6, lineSpacing: CGFloat = 6) {
        self.spacing = spacing
        self.lineSpacing = lineSpacing
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.defaultLow, for: .horizontal)
        setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func addView(_ view: NSView) {
        view.translatesAutoresizingMaskIntoConstraints = true
        views.append(view)
        addSubview(view)
        needsLayout = true
        invalidateIntrinsicContentSize()
    }

    func removeView(_ view: NSView) {
        guard let i = views.firstIndex(where: { $0 === view }) else { return }
        views.remove(at: i)
        view.removeFromSuperview()
        needsLayout = true
        invalidateIntrinsicContentSize()
    }

    func removeAllViews() {
        for v in views {
            v.removeFromSuperview()
        }
        views.removeAll()
        needsLayout = true
        invalidateIntrinsicContentSize()
    }

    override var isFlipped: Bool { true }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: height(for: bounds.width))
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        if newSize.width != lastWidth {
            lastWidth = newSize.width
            invalidateIntrinsicContentSize()
            needsLayout = true
        }
    }

    override func layout() {
        super.layout()
        _ = arrange(width: bounds.width, place: true)
    }

    /// The height needed for `width`; every child at least starts a line.
    func height(for width: CGFloat) -> CGFloat {
        arrange(width: width, place: false)
    }

    /// Lays the children out in lines for `width`, moving them when `place`
    /// is set, and returns the total height.
    @discardableResult
    private func arrange(width: CGFloat, place: Bool) -> CGFloat {
        let visible = views.filter { !$0.isHidden }
        guard !visible.isEmpty else { return 0 }
        var x: CGFloat = 0
        var y: CGFloat = 0
        var lineHeight: CGFloat = 0
        for v in visible {
            let size = v.fittingSize
            if x > 0, x + size.width > width {
                x = 0
                y += lineHeight + lineSpacing
                lineHeight = 0
            }
            if place {
                v.frame = NSRect(x: x, y: y, width: size.width, height: size.height)
            }
            x += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }
        return y + lineHeight
    }
}
