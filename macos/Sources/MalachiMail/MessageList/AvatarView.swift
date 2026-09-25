// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.Avatar's colour classes and initials (libadwaita adw-avatar.c and
/// _avatar.scss), so that a sender gets the same colour and letters as in
/// the GTK client: the class is `g_str_hash(text) % 14 + 1`, the initials
/// are the first letter and the letter after the last space.
enum AvatarPalette {
    struct Colours {
        let foreground: NSColor
        let top: NSColor
        let bottom: NSColor
    }

    /// libadwaita `_avatar.scss` `$avatar_colors`: text, gradient top,
    /// gradient bottom, for classes 1 to 14.
    static let classes: [(fg: UInt32, top: UInt32, bottom: UInt32)] = [
        (0xcfe1f5, 0x83b6ec, 0x337fdc),
        (0xcaeaf2, 0x7ad9f1, 0x0f9ac8),
        (0xcef8d8, 0x8de6b1, 0x29ae74),
        (0xe6f9d7, 0xb5e98a, 0x6ab85b),
        (0xf9f4e1, 0xf8e359, 0xd29d09),
        (0xffead1, 0xffcb62, 0xd68400),
        (0xffe5c5, 0xffa95a, 0xed5b00),
        (0xf8d2ce, 0xf78773, 0xe62d42),
        (0xfac7de, 0xe973ab, 0xe33b6a),
        (0xe7c2e8, 0xcb78d4, 0x9945b5),
        (0xd5d2f5, 0x9e91e8, 0x7a59ca),
        (0xf2eade, 0xe3cf9c, 0xb08952),
        (0xe5d6ca, 0xbe916d, 0x785336),
        (0xd8d7d3, 0xc0bfbc, 0x6e6d71),
    ]

    /// GLib's `g_str_hash`: djb2 over the bytes as `signed char`, up to
    /// the first NUL, wrapping at 32 bits.
    static func hash(_ text: String) -> UInt32 {
        var h: UInt32 = 5381
        for b in text.utf8 {
            if b == 0 {
                break
            }
            h = (h << 5) &+ h &+ UInt32(bitPattern: Int32(Int8(bitPattern: b)))
        }
        return h
    }

    /// The colour class of a name, 1 to 14 (adw-avatar.c `set_class_color`).
    static func colourClass(_ text: String) -> Int {
        Int(hash(text) % 14) + 1
    }

    static func colours(forClass cls: Int) -> Colours {
        let c = classes[min(max(cls, 1), 14) - 1]
        return Colours(foreground: rgb(c.fg), top: rgb(c.top), bottom: rgb(c.bottom))
    }

    /// The initials shown on the avatar (adw-avatar.c
    /// `extract_initials_from_text`): the text upper-cased, stripped and
    /// composed; its first character, and the character after the last
    /// space when there is one. Empty for an empty text.
    static func initials(_ text: String) -> String {
        let normalized = text.uppercased()
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .precomposedStringWithCanonicalMapping
        let scalars = normalized.unicodeScalars
        guard let first = scalars.first else {
            return ""
        }
        var out = String.UnicodeScalarView()
        out.append(first)
        if let space = scalars.lastIndex(of: " ") {
            let next = scalars.index(after: space)
            if next < scalars.endIndex, scalars[next] != " " {
                out.append(scalars[next])
            }
        }
        return String(out)
    }

    private static func rgb(_ v: UInt32) -> NSColor {
        NSColor(
            srgbRed: CGFloat((v >> 16) & 0xff) / 255, green: CGFloat((v >> 8) & 0xff) / 255,
            blue: CGFloat(v & 0xff) / 255, alpha: 1
        )
    }
}

/// Adw.Avatar: a circle with a gradient of the sender's colour class and
/// the initials of the name, or a person symbol without a name. The
/// monochrome variant (`ui/internal/style` "monochrome-avatars") is a
/// tinted disc with the initials in the label colour.
@MainActor
final class AvatarView: NSView {
    /// The name the initials and the colour come from; hostile input, only
    /// ever drawn as text.
    var text: String = "" {
        didSet {
            if text != oldValue {
                needsDisplay = true
            }
        }
    }

    var size: CGFloat {
        didSet {
            guard size != oldValue else { return }
            widthConstraint.constant = size
            heightConstraint.constant = size
            invalidateIntrinsicContentSize()
            needsDisplay = true
        }
    }

    var monochrome = false {
        didSet {
            if monochrome != oldValue {
                needsDisplay = true
            }
        }
    }

    private var widthConstraint: NSLayoutConstraint!
    private var heightConstraint: NSLayoutConstraint!

    init(size: CGFloat) {
        self.size = size
        super.init(frame: NSRect(x: 0, y: 0, width: size, height: size))
        translatesAutoresizingMaskIntoConstraints = false
        widthConstraint = widthAnchor.constraint(equalToConstant: size)
        heightConstraint = heightAnchor.constraint(equalToConstant: size)
        NSLayoutConstraint.activate([widthConstraint, heightConstraint])
        setContentHuggingPriority(.required, for: .horizontal)
        setContentCompressionResistancePriority(.required, for: .horizontal)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var intrinsicContentSize: NSSize {
        NSSize(width: size, height: size)
    }

    override func draw(_ dirtyRect: NSRect) {
        let circle = NSBezierPath(ovalIn: bounds)
        let foreground: NSColor
        if monochrome {
            Tint.fg(alpha: 0.12).setFill()
            circle.fill()
            foreground = Tint.fg(alpha: 0.8)
        } else {
            let colours = AvatarPalette.colours(forClass: AvatarPalette.colourClass(text))
            if let gradient = NSGradient(starting: colours.top, ending: colours.bottom) {
                gradient.draw(in: circle, angle: 270)
            } else {
                colours.bottom.setFill()
                circle.fill()
            }
            foreground = colours.foreground
        }

        let initials = AvatarPalette.initials(text)
        if initials.isEmpty {
            // No name: the person symbol, at half the size (adw-avatar.c).
            let config = NSImage.SymbolConfiguration(pointSize: (size / 2).rounded(), weight: .regular)
                .applying(NSImage.SymbolConfiguration(paletteColors: [foreground]))
            guard let image = NSImage(systemSymbolName: "person.crop.circle.fill", accessibilityDescription: nil)?
                .withSymbolConfiguration(config) else { return }
            let s = image.size
            let origin = NSPoint(x: (bounds.width - s.width) / 2, y: (bounds.height - s.height) / 2)
            image.draw(in: NSRect(origin: origin, size: s))
            return
        }
        let font = NSFont.systemFont(ofSize: (size * 0.38).rounded(), weight: .bold)
        let attributes: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: foreground]
        let string = NSAttributedString(string: initials, attributes: attributes)
        let s = string.size()
        let origin = NSPoint(x: (bounds.width - s.width) / 2, y: (bounds.height - s.height) / 2)
        string.draw(at: origin)
    }
}
