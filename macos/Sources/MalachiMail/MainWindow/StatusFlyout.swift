// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The window the status popover opens in, drawn as the Windows client's
/// flyout (StatusBarView.xaml `StatusFlyout`, `Placement="TopEdgeAlignedLeft"`)
/// rather than as an NSPopover: no arrow, a radius of 8 with a hairline
/// border and a shadow, its leading edge on the status line's and its
/// bottom just above the line. NSPopover has no public way to drop its
/// arrow, so this is a borderless panel, a child of the main window.
///
/// It behaves as a transient popover: a click anywhere else in the app,
/// Escape, the app going to the background and the main window closing or
/// leaving the screen close it. `onWillClose` gets the event that closed
/// it (the mouse-down of a click outside, else nil), so the status line can
/// tell a click on itself from one elsewhere.
@MainActor
final class StatusFlyout: NSObject, NSWindowDelegate {
    /// The flyout's corner radius (WinUI's OverlayCornerRadius).
    static let cornerRadius: CGFloat = 8

    var onWillClose: (@MainActor (NSEvent?) -> Void)?

    private let content: NSViewController
    private var panel: FlyoutPanel?
    private var clickMonitor: Any?
    private var observers: [NSObjectProtocol] = []
    /// Where the flyout's bottom edge stays while its rows change height.
    private var bottom: CGFloat = 0

    init(content: NSViewController) {
        self.content = content
        super.init()
    }

    var isShown: Bool { panel != nil }

    /// Opens the flyout `gap` above `rect` of `view` (in its coordinates),
    /// its leading edge on the rect's, kept on the screen of `view`'s window.
    func show(above rect: NSRect, of view: NSView, gap: CGFloat) {
        guard panel == nil, let parent = view.window else { return }
        let panel = FlyoutPanel(content: content)
        panel.onCancel = { [weak self] in self?.close(nil) }
        panel.delegate = self
        self.panel = panel

        // The panel's size from its content (the width is fixed, the
        // height follows the rows); then its place.
        panel.layoutIfNeeded()
        panel.setContentSize(panel.contentView?.fittingSize ?? .zero)
        let size = panel.frame.size
        let anchor = parent.convertToScreen(view.convert(rect, to: nil))
        var origin = NSPoint(x: anchor.minX, y: anchor.maxY + gap)
        if view.userInterfaceLayoutDirection == .rightToLeft {
            origin.x = anchor.maxX - size.width
        }
        if let visible = (parent.screen ?? NSScreen.main)?.visibleFrame {
            origin.x = min(max(origin.x, visible.minX), visible.maxX - size.width)
            origin.y = min(origin.y, visible.maxY - size.height)
        }
        bottom = origin.y
        panel.setFrameOrigin(origin)
        parent.addChildWindow(panel, ordered: .above)
        panel.makeKeyAndOrderFront(nil)

        clickMonitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown, .otherMouseDown]) { [weak self] event in
            guard let self, let panel = self.panel else { return event }
            if event.window !== panel {
                self.close(event)
            }
            return event
        }
        let center = NotificationCenter.default
        observers = [
            center.addObserver(forName: NSApplication.didResignActiveNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.close(nil) }
            },
            center.addObserver(forName: NSWindow.willCloseNotification, object: parent, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.close(nil) }
            },
            center.addObserver(forName: NSWindow.willMiniaturizeNotification, object: parent, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.close(nil) }
            },
        ]
    }

    /// Closes the flyout; `event` is the click outside that closed it.
    func close(_ event: NSEvent? = nil) {
        guard let panel else { return }
        self.panel = nil
        onWillClose?(event)
        if let clickMonitor {
            NSEvent.removeMonitor(clickMonitor)
        }
        clickMonitor = nil
        for o in observers {
            NotificationCenter.default.removeObserver(o)
        }
        observers = []
        let parent = panel.parent
        parent?.removeChildWindow(panel)
        panel.orderOut(nil)
        // The keyboard back to the window under it, unless the click that
        // closed the flyout went to another window of the app.
        if event == nil || event?.window === parent, NSApp.isActive {
            parent?.makeKey()
        }
    }

    /// Another window took the keyboard (a sheet, a window opened by a
    /// row's action, another app): as a transient popover, it closes.
    func windowDidResignKey(_ notification: Notification) {
        close(nil)
    }

    /// The rows change height in place (a reason that wraps, the unsent
    /// row), and AppKit keeps a window's top edge where it was: the bottom
    /// edge goes back above the line.
    func windowDidResize(_ notification: Notification) {
        guard let panel else { return }
        if panel.frame.minY != bottom {
            panel.setFrameOrigin(NSPoint(x: panel.frame.minX, y: bottom))
        }
        panel.invalidateShadow()
    }
}

/// The flyout's borderless panel: it takes the keyboard (Tab through the
/// rows' buttons, Escape closes it) but never becomes the main window.
@MainActor
private final class FlyoutPanel: NSPanel {
    var onCancel: (@MainActor () -> Void)?

    init(content: NSViewController) {
        super.init(contentRect: .zero, styleMask: [.borderless], backing: .buffered, defer: false)
        isOpaque = false
        backgroundColor = .clear
        hasShadow = true
        isReleasedWhenClosed = false
        hidesOnDeactivate = false
        animationBehavior = .utilityWindow

        let background = NSVisualEffectView()
        background.material = .popover
        background.blendingMode = .behindWindow
        background.state = .active
        background.maskImage = Self.mask(radius: StatusFlyout.cornerRadius)
        background.translatesAutoresizingMaskIntoConstraints = false

        let border = FlyoutBorderView()
        border.translatesAutoresizingMaskIntoConstraints = false

        let root = NSView()
        root.translatesAutoresizingMaskIntoConstraints = false
        let body = content.view
        body.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(background)
        root.addSubview(body)
        root.addSubview(border)
        NSLayoutConstraint.activate([
            background.topAnchor.constraint(equalTo: root.topAnchor),
            background.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            background.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            background.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            border.topAnchor.constraint(equalTo: root.topAnchor),
            border.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            border.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            border.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            body.topAnchor.constraint(equalTo: root.topAnchor),
            body.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            body.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            body.trailingAnchor.constraint(equalTo: root.trailingAnchor),
        ])
        contentView = root
    }

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }

    override func cancelOperation(_ sender: Any?) {
        onCancel?()
    }

    /// A rounded mask that stretches (cap insets of the radius).
    private static func mask(radius: CGFloat) -> NSImage {
        let edge = radius * 2 + 1
        let image = NSImage(size: NSSize(width: edge, height: edge), flipped: false) { rect in
            NSColor.black.setFill()
            NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius).fill()
            return true
        }
        image.capInsets = NSEdgeInsets(top: radius, left: radius, bottom: radius, right: radius)
        image.resizingMode = .stretch
        return image
    }
}

/// The flyout's hairline border (WinUI's SurfaceStrokeColorFlyoutBrush),
/// drawn so that it follows the appearance.
@MainActor
private final class FlyoutBorderView: NSView {
    override func draw(_ dirtyRect: NSRect) {
        let r = StatusFlyout.cornerRadius
        let path = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: r - 0.5, yRadius: r - 0.5)
        path.lineWidth = 1
        NSColor.separatorColor.setStroke()
        path.stroke()
    }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
