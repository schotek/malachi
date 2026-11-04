// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The document the client prints (ui/internal/printing, ported 1:1;
/// docs/plans/toolbar-customization.md §8, P1–P8): a message, a
/// conversation, several messages, a calendar event or a day, as the HTML
/// fragment the locked message view loads in place of a body. The header
/// rows and the event's and day's lines are the application's, escaped here
/// (`escape`); the message bodies are the sanitiser's output, inserted
/// verbatim; a body without HTML is its plain text, escaped, in a <pre>.
/// Dates, addresses and sizes come formatted by the caller.
public enum Printing {
    /// printing.MaxMessages: the most messages one print takes.
    public static let maxMessages = 50

    /// printing.MenuTitle: Print in the File menu (with a mnemonic).
    public static var menuTitle: String { L10n.T("_Print…") }

    /// printing.ItemLabel: Print as a toolbar item and its tooltip.
    public static var itemLabel: String { L10n.T("Print") }

    /// printing.Header: a message's header as the caller formatted it.
    public struct Header: Sendable, Equatable {
        public var from = "", to = "", cc = "", bcc = "", date = "", subject = ""
        /// The attachments' names with their sizes (no inline pictures).
        public var attachments: [String] = []

        public init() {}
    }

    /// printing.Row: a labelled line of a header; `strong` is the subject's.
    public struct Row: Sendable, Equatable {
        public let label: String
        public let value: String
        public var strong = false

        public init(_ label: String, _ value: String, strong: Bool = false) {
            self.label = label
            self.value = value
            self.strong = strong
        }
    }

    /// printing.HeaderRows: Subject, From, To, Cc, Bcc, Date, Attachments,
    /// the empty ones left out.
    public static func headerRows(_ h: Header) -> [Row] {
        var rows: [Row] = []
        func add(_ label: String, _ value: String) {
            if !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                rows.append(Row(label, value))
            }
        }
        add(L10n.T("Subject"), h.subject)
        if rows.count == 1 {
            rows[0].strong = true
        }
        add(L10n.T("From"), h.from)
        add(L10n.T("To"), h.to)
        add(L10n.T("Cc"), h.cc)
        add(L10n.T("Bcc"), h.bcc)
        add(L10n.T("Date"), h.date)
        add(L10n.T("Attachments"), h.attachments.joined(separator: ", "))
        return rows
    }

    /// printing.Escape: & < > " ' as entities.
    public static func escape(_ s: String) -> String {
        var out = ""
        out.reserveCapacity(s.utf8.count)
        for ch in s.unicodeScalars {
            switch ch {
            case "&": out += "&amp;"
            case "<": out += "&lt;"
            case ">": out += "&gt;"
            case "\"": out += "&quot;"
            case "'": out += "&#39;"
            default: out.unicodeScalars.append(ch)
            }
        }
        return out
    }

    /// printing.Body: the sanitiser's HTML, or (empty) the plain text.
    public struct Body: Sendable, Equatable {
        public var html = ""
        public var text = ""

        public init(html: String = "", text: String = "") {
            self.html = html
            self.text = text
        }
    }

    /// printing.CSS.
    public static let css = ".malachi-print-header{border-collapse:collapse;margin:0 0 12px 0;font:13px -apple-system,system-ui,sans-serif;color:#000}"
        + ".malachi-print-header th{text-align:right;vertical-align:top;padding:1px 8px 1px 0;font-weight:600;white-space:nowrap;color:#444}"
        + ".malachi-print-header td{padding:1px 0;word-break:break-word}"
        + ".malachi-print-subject td{font-weight:700;font-size:15px}"
        + ".malachi-print-body{margin:0 0 12px 0}"
        + ".malachi-print-text{white-space:pre-wrap;word-wrap:break-word;font:12px ui-monospace,Menlo,monospace;margin:0}"
        + ".malachi-print-sep{border:0;border-top:1px solid #999;margin:18px 0}"
        + ".malachi-print-page{break-before:page;page-break-before:always}"
        + ".malachi-print-title{font:700 17px -apple-system,system-ui,sans-serif;margin:0 0 6px 0}"
        + ".malachi-print-line{font:13px -apple-system,system-ui,sans-serif;margin:2px 0}"
        + ".malachi-print-notes{white-space:pre-wrap;font:13px -apple-system,system-ui,sans-serif;margin:10px 0 0 0}"
        + ".malachi-print-day{border-collapse:collapse;width:100%;font:13px -apple-system,system-ui,sans-serif}"
        + ".malachi-print-day td{padding:4px 8px 4px 0;vertical-align:top;border-bottom:1px solid #ddd}"
        + ".malachi-print-day td.time{white-space:nowrap;color:#444;width:1%}"

    /// printing.Message: one message's header table and body.
    public static func message(_ rows: [Row], _ body: Body) -> String {
        var b = "<table class=\"malachi-print-header\">"
        for r in rows {
            let cls = r.strong ? " class=\"malachi-print-subject\"" : ""
            b += "<tr\(cls)><th>" + escape(r.label) + "</th><td>" + escape(r.value) + "</td></tr>"
        }
        b += "</table><div class=\"malachi-print-body\">"
        if !body.html.isEmpty {
            b += body.html
        } else {
            b += "<pre class=\"malachi-print-text\">" + escape(body.text) + "</pre>"
        }
        return b + "</div>"
    }

    /// printing.Layout.
    public enum Layout: Sendable {
        case single, conversation, pages
    }

    /// printing.Fragment: the sheet and the sections in order.
    public static func fragment(_ sections: [String], _ layout: Layout) -> String {
        var b = "<style>" + css + "</style>"
        for (i, s) in sections.enumerated() {
            if i > 0 {
                switch layout {
                case .conversation: b += "<hr class=\"malachi-print-sep\">"
                case .pages: b += "<div class=\"malachi-print-page\"></div>"
                case .single: break
                }
            }
            b += s
        }
        return b
    }

    /// printing.Event.
    public struct Event: Sendable, Equatable {
        public var title = ""
        public var lines: [String] = []
        public var attendees: [String] = []
        public var notes = ""

        public init() {}
    }

    /// printing.EventSection.
    public static func eventSection(_ e: Event) -> String {
        var b = "<h1 class=\"malachi-print-title\">" + escape(e.title) + "</h1>"
        for l in e.lines where !l.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            b += "<p class=\"malachi-print-line\">" + escape(l) + "</p>"
        }
        for a in e.attendees {
            b += "<p class=\"malachi-print-line\">" + escape(a) + "</p>"
        }
        if !e.notes.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            b += "<div class=\"malachi-print-notes\">" + escape(e.notes) + "</div>"
        }
        return b
    }

    /// printing.DayItem.
    public struct DayItem: Sendable, Equatable {
        public let time: String
        public let title: String
        public let location: String

        public init(time: String, title: String, location: String = "") {
            self.time = time
            self.title = title
            self.location = location
        }
    }

    /// printing.DaySection.
    public static func daySection(_ heading: String, _ items: [DayItem], none: String) -> String {
        var b = "<h1 class=\"malachi-print-title\">" + escape(heading) + "</h1>"
        guard !items.isEmpty else {
            return b + "<p class=\"malachi-print-line\">" + escape(none) + "</p>"
        }
        b += "<table class=\"malachi-print-day\">"
        for it in items {
            b += "<tr><td class=\"time\">" + escape(it.time) + "</td><td>" + escape(it.title)
            if !it.location.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                b += "<br>" + escape(it.location)
            }
            b += "</td></tr>"
        }
        return b + "</table>"
    }
}
