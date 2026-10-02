// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The colours of the board, system colours all (the prototype's hex
/// values are not copied): they follow the appearance by themselves. The
/// views that paint a layer or a path with them do so in `updateLayer` or
/// `draw`, never at init, so they are returned as `NSColor`s.
@MainActor
enum BoardPalette {
    /// A state's accent: its bar, its section title, its dot.
    static func accent(_ state: Board.State) -> NSColor {
        switch state {
        case .hot: return .systemRed
        case .you: return .systemBlue
        case .them: return .systemBrown
        case .info: return .secondaryLabelColor
        }
    }

    /// The colour of a list section's title: the state's, grey for Done.
    static func accent(_ kind: Board.SectionKind) -> NSColor {
        switch kind {
        case .state(let s): return accent(s)
        case .snoozed, .done: return .secondaryLabelColor
        }
    }

    /// The pill of a state: its accent on the accent at low alpha (grey
    /// for the informational state, as a status to do).
    static func colours(_ state: Board.State) -> IssuePill.Colours {
        if state == .info {
            return (.secondaryLabelColor, Tint.fg(alpha: 0.1))
        }
        let c = accent(state)
        return (c, c.withAlphaComponent(0.15))
    }

    /// What the assistant wrote: its heading, the circle of a commitment.
    /// Indigo, a calm violet (purple reads as magenta in light mode).
    static var assistant: NSColor { .systemIndigo }
    /// The summary and draft boxes, and the commitments' cards.
    static var assistantFill: NSColor { NSColor.systemIndigo.withAlphaComponent(0.08) }
    static var assistantBorder: NSColor { NSColor.systemIndigo.withAlphaComponent(0.3) }

    /// The chip of a due date: orange at low alpha, in brown text.
    static var dueColours: IssuePill.Colours {
        (.systemBrown, NSColor.systemOrange.withAlphaComponent(0.15))
    }

    /// The account's tag: neutral, as a status to do.
    static var accountColours: IssuePill.Colours {
        (.secondaryLabelColor, Tint.fg(alpha: 0.1))
    }

    /// A card's fill, laid over by the hot tint for a hot case.
    static var cardFill: NSColor { ConversationTint.cardFill }
    static var hotCardFill: NSColor { NSColor.systemRed.withAlphaComponent(0.06) }
    static var hotCardBorder: NSColor { NSColor.systemRed.withAlphaComponent(0.3) }
    static func cardBorder(in view: NSView) -> NSColor { ConversationTint.cardBorder(in: view) }
    /// The border of the selected card (2 pt), grey while the window is
    /// not the key one.
    static func selectedBorder(emphasized: Bool) -> NSColor {
        emphasized ? .controlAccentColor : .secondaryLabelColor
    }
    /// A message card of the detail's conversation: a faint wash of the
    /// text colour, so the card stands apart from the page in both
    /// appearances (the prototype's hairline-bordered card); the user's
    /// own messages a faint blue (the prototype's cool tint), the colour
    /// of Waiting for You. Both under `cardBorder`.
    static var messageFill: NSColor { Tint.fg(alpha: 0.035) }
    static var ownMessageFill: NSColor { NSColor.systemBlue.withAlphaComponent(0.07) }
    /// The page behind the cards.
    static var surface: NSColor { ConversationTint.surface }
}

/// The measures of the board's rows. Every table of the board has fixed
/// row heights (no automatic row heights: a window resize would lay every
/// row out again), computed here from the line heights of `Typo` so that
/// the larger text size keeps working. The totals are the heights of the
/// table row, a card's outer inset included.
@MainActor
enum BoardMetrics {
    // MARK: Fixed measures

    /// The state bar on a row's leading edge, and the gap after it.
    static let barWidth: CGFloat = 3
    static let barGap: CGFloat = 9
    /// A card: its radius, its padding, and the inset of the drawn card
    /// in its table row (twice the vertical one is the gap between two).
    static let cardRadius = ConversationMetrics.cardRadius
    static let cardPaddingV: CGFloat = 10
    static let cardPaddingH: CGFloat = 12
    /// A case card's leading padding, before its state bar: the bar's width
    /// less than `cardPaddingH`, so the bar sits nearer the card's edge
    /// than the text sits to the trailing one (the prototype's 9 pt).
    static let cardBarPaddingH: CGFloat = cardPaddingH - barWidth
    static let cardInsetH: CGFloat = 0
    static let cardInsetV: CGFloat = 4
    static let cardBorderWidth: CGFloat = 1
    static let selectedBorderWidth: CGFloat = 2
    /// The padding of a list row, left and right.
    static let listPaddingH: CGFloat = 12
    /// The gap between two lines of a row, and above the meta line.
    static let lineGap: CGFloat = 2
    static let metaGap: CGFloat = 3
    /// The gap between the items of a row's line.
    static let itemGap: CGFloat = 6
    /// The ring of a commitment.
    static let ringSize: CGFloat = 12
    static let ringWidth: CGFloat = 1.5

    // MARK: Line heights

    static func lineHeight(_ font: NSFont) -> CGFloat {
        ceil(NSLayoutManager().defaultLineHeight(for: font))
    }

    /// A pill is a line and two points of air (`PillLabel`).
    static func pillHeight(_ font: NSFont) -> CGFloat {
        lineHeight(font) + 2
    }

    static var personLine: CGFloat { lineHeight(BoardFonts.person(unread: true)) }
    static var titleLine: CGFloat { lineHeight(BoardFonts.title) }
    static var snippetLine: CGFloat { lineHeight(BoardFonts.snippet) }
    static var metaLine: CGFloat { max(pillHeight(Typo.caption), lineHeight(Typo.captionNumeric) + 2) }
    /// The space above and below a list row's text.
    static var listPaddingV: CGFloat { RowMetrics.marginComfortable }

    // MARK: Row heights

    /// A case's content: the person line, the title, the snippet and the
    /// meta line; `compact` (Today) is the title line and one detail line.
    static func contentHeight(titleLines: Int, snippetLines: Int, compact: Bool) -> CGFloat {
        if compact {
            return titleLine + lineGap + snippetLine
        }
        var h = personLine + lineGap + titleLine * CGFloat(max(titleLines, 1))
        if snippetLines > 0 {
            h += lineGap + snippetLine * CGFloat(snippetLines)
        }
        return h + metaGap + metaLine
    }

    /// A case row of a table: `card` rows sit in an inset rounded card.
    static func caseRow(titleLines: Int, snippetLines: Int, compact: Bool = false, card: Bool = false) -> CGFloat {
        let content = contentHeight(titleLines: titleLines, snippetLines: snippetLines, compact: compact)
        return card ? content + 2 * cardPaddingV + 2 * cardInsetV : content + 2 * listPaddingV
    }

    /// The Seznam's case row: one title line and two of snippet.
    static var listCaseRow: CGFloat { caseRow(titleLines: 1, snippetLines: 2) }
    /// A card of Sloupce: one line of title and two of snippet.
    static var columnCard: CGFloat { caseRow(titleLines: 1, snippetLines: 2, card: true) }
    /// A row of Dnes: title, and "person · account · snippet".
    static var todayRow: CGFloat { caseRow(titleLines: 1, snippetLines: 1, compact: true) }

    /// A section's header row (a title and a count) and Today's.
    static var sectionHeader: CGFloat { lineHeight(BoardFonts.sectionTitle) + 2 * 6 + 8 }
    static var todaySectionHeader: CGFloat { sectionHeader }

    /// A commitment: its text, its quote and the case it comes from.
    static var commitmentContent: CGFloat { titleLine + lineGap + snippetLine + lineGap + snippetLine }
    static var commitmentRow: CGFloat { commitmentContent + 2 * listPaddingV }
    static var commitmentCard: CGFloat { commitmentContent + 2 * cardPaddingV + 2 * cardInsetV }
}

/// The fonts of the board's rows.
@MainActor
enum BoardFonts {
    static func person(unread: Bool) -> NSFont {
        .systemFont(ofSize: Typo.bodySize, weight: unread ? .bold : .semibold)
    }
    static var title: NSFont { Typo.bodyMedium }
    static var snippet: NSFont { Typo.caption }
    static var sectionTitle: NSFont { .systemFont(ofSize: Typo.bodySize, weight: .bold) }
    static var quote: NSFont {
        NSFontManager.shared.convert(Typo.caption, toHaveTrait: .italicFontMask)
    }
}
