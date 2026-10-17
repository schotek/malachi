// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One attendee of the event detail popover: the response's symbol (✓
/// accepted green, ? no reply grey, ? tentative orange, ✕ declined red,
/// → delegated grey), the name (the address when it has none) and the
/// organiser and optional labels in grey. The response's text is the
/// row's accessibility label and the address its tooltip, both plain.
@MainActor
final class EventDetailAttendeeRow: NSStackView {
    init(_ a: Cal.AttendeeRow, width: CGFloat) {
        super.init(frame: .zero)
        let (symbol, color) = Self.symbol(a.response)
        let icon = calendarSymbol(symbol, pointSize: 10, color: color)
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.widthAnchor.constraint(equalToConstant: 14).isActive = true
        let name = calendarLabel(.systemFont(ofSize: NSFont.systemFontSize))
        name.stringValue = a.name.isEmpty ? a.address : a.name
        name.toolTip = a.address.isEmpty ? nil : a.address
        name.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        var labels: [String] = []
        if a.organizer {
            labels.append(Cal.organizerLabel)
        }
        if a.optional {
            labels.append(Cal.optionalLabel)
        }
        let extra = calendarLabel(.systemFont(ofSize: NSFont.smallSystemFontSize), color: .secondaryLabelColor)
        extra.stringValue = labels.joined(separator: ", ")
        extra.isHidden = labels.isEmpty
        extra.setContentHuggingPriority(.required, for: .horizontal)
        orientation = .horizontal
        alignment = .firstBaseline
        spacing = 6
        detachesHiddenViews = true
        for v in [icon, name, extra] {
            addArrangedSubview(v)
        }
        widthAnchor.constraint(lessThanOrEqualToConstant: width).isActive = true
        setAccessibilityElement(true)
        setAccessibilityRole(.staticText)
        var label = Cal.Text.dot(name.stringValue, a.responseText)
        if !extra.stringValue.isEmpty {
            label = Cal.Text.dot(label, extra.stringValue)
        }
        setAccessibilityLabel(label)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    static func symbol(_ r: EventResponse) -> (String, NSColor) {
        switch r {
        case .accepted: ("checkmark", .systemGreen)
        case .declined: ("xmark", .systemRed)
        case .tentative: ("questionmark", .systemOrange)
        case .delegated: ("arrow.right", .secondaryLabelColor)
        default: ("questionmark", .secondaryLabelColor)
        }
    }
}

/// The event's notes: plain text in a grey, rounded, non-editable text
/// view (selectable, no link detection, no rich text) that scrolls beyond
/// `maxHeight`. Only the daemon's `links` become clickable ranges; a
/// click goes to `onLink` with the link itself (the controller opens only
/// a link the event lists, through "Open This Link?").
@MainActor
final class EventDetailNotesView: NSView, NSTextViewDelegate {
    var onLink: (@MainActor (String) -> Void)?

    private static let padding: CGFloat = 6

    init(text: String, links: [String], width: CGFloat, maxHeight: CGFloat) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = 6
        let inner = width - 2 * Self.padding
        let scroll = NSScrollView()
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        let tv = NSTextView(frame: NSRect(x: 0, y: 0, width: inner, height: 10))
        tv.isEditable = false
        tv.isSelectable = true
        tv.isRichText = false
        tv.importsGraphics = false
        tv.allowsUndo = false
        tv.isAutomaticLinkDetectionEnabled = false
        tv.isAutomaticDataDetectionEnabled = false
        tv.drawsBackground = false
        tv.textContainerInset = .zero
        tv.textContainer?.lineFragmentPadding = 0
        tv.textContainer?.widthTracksTextView = true
        tv.isVerticallyResizable = true
        tv.isHorizontallyResizable = false
        tv.autoresizingMask = [.width]
        tv.delegate = self
        let font = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize)
        let s = NSMutableAttributedString(
            string: text, attributes: [.font: font, .foregroundColor: NSColor.secondaryLabelColor])
        let ns = text as NSString
        for link in links where !link.isEmpty {
            var from = 0
            while from < ns.length {
                let r = ns.range(of: link, options: [], range: NSRange(location: from, length: ns.length - from))
                guard r.location != NSNotFound else { break }
                s.addAttribute(.link, value: link, range: r)
                from = r.location + r.length
            }
        }
        tv.textStorage?.setAttributedString(s)
        tv.linkTextAttributes = [.foregroundColor: NSColor.linkColor, .underlineStyle: NSUnderlineStyle.single.rawValue]
        var used: CGFloat = 0
        if let lm = tv.layoutManager, let tc = tv.textContainer {
            tc.containerSize = NSSize(width: inner, height: .greatestFiniteMagnitude)
            lm.ensureLayout(for: tc)
            used = ceil(lm.usedRect(for: tc).height)
        }
        tv.setFrameSize(NSSize(width: inner, height: max(used, 10)))
        scroll.documentView = tv
        addSubview(scroll)
        let height = min(max(used, 14), maxHeight)
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: width),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.padding),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.padding),
            scroll.topAnchor.constraint(equalTo: topAnchor, constant: Self.padding),
            scroll.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -Self.padding),
            scroll.heightAnchor.constraint(equalToConstant: height),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.quaternarySystemFill.cgColor
    }

    override var wantsUpdateLayer: Bool { true }

    func textView(_ textView: NSTextView, clickedOnLink link: Any, at charIndex: Int) -> Bool {
        if let s = link as? String {
            onLink?(s)
        }
        // Never handed to the system.
        return true
    }
}
