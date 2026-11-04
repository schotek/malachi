// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// Printing (docs/plans/toolbar-customization.md §8, P1–P8): a message, a
/// conversation, several messages, a calendar event or a day. The document
/// is a `Printing` fragment: the application's escaped header rows around
/// the sanitised bodies of `message.body` asked without `trimQuoted` (the
/// whole message, quoted history included; never the cache's variant
/// switch, which would change the reader), the pictures as the message
/// shows them by default (`data:` and `malachi-cid:` only). An off-screen
/// `MessageWebView`, the reader's locked view, loads it; WebKit's print
/// operation prints it with the system's page header and footer (P6).
///
/// DEVELOPMENT AID: with `MALACHI_PRINT_PDF=<path>` the operation saves a
/// PDF there instead of showing the print panel (the `print:*` steps of
/// MALACHI_START), never a printer.
@MainActor
enum PrintController {
    private static let log = Logger(subsystem: "io.github.schotek.Malachi", category: "print")

    // MARK: Mail

    /// Prints `messages` of their accounts (a selection's or a
    /// conversation's) in `layout`; a conversation oldest first. `role`
    /// tells the user's own messages (P6: their Bcc). At most
    /// `Printing.maxMessages`.
    static func printMessages(
        _ messages: [MessageSummary], layout: Printing.Layout, cache: MessageCache,
        role: @escaping @MainActor (MessageSummary) -> FolderRole?, window: NSWindow?
    ) {
        var list = Array(messages.prefix(Printing.maxMessages))
        guard !list.isEmpty else { return }
        if layout == .conversation {
            list.sort { ($0.date, $0.id.rawValue) < ($1.date, $1.id.rawValue) }
        }
        let title = subjectText(list.first?.subject ?? "")
        Task {
            var sections: [String] = []
            for s in list {
                sections.append(await section(s, cache: cache, own: isOwn(role(s))))
            }
            PrintJob.run(fragment: Printing.fragment(sections, layout), title: title, cache: cache, window: window)
        }
    }

    private static func isOwn(_ role: FolderRole?) -> Bool {
        guard let role else { return false }
        return role == .sent || role == .drafts || role == .outbox
    }

    /// One message's section: the header of message.get (the cache's) and
    /// the whole body; a message that cannot be fetched prints its summary
    /// and snippet.
    private static func section(_ s: MessageSummary, cache: MessageCache, own: Bool) async -> String {
        // The cache calls back after each half (message.get, the body)
        // and again on later updates: the print takes the first answer
        // with nothing in flight any more, and resumes once.
        let once = Once()
        let lm: LoadedMessage = await withCheckedContinuation { cont in
            cache.fetch(s) { lm in
                guard !once.done, !lm.getting, !lm.fetching else { return }
                once.done = true
                cont.resume(returning: lm)
            }
        }
        let m = lm.msg
        var body: MessageBodyResult?
        do {
            body = try await cache.client.call(
                API.MessageBody.self,
                MessageBodyParams(accountId: s.accountId, messageId: s.id, remoteContent: nil, trimQuoted: false))
        } catch {
            log.error("print: message.body failed: \(String(describing: type(of: error)), privacy: .public)")
            body = lm.body
        }
        var h = Printing.Header()
        let summary = m?.summary ?? s
        h.subject = subjectText(summary.subject)
        h.from = formatParticipants(summary.from)
        h.to = formatParticipants(summary.to ?? [])
        h.cc = formatParticipants(m?.cc ?? [])
        if own {
            h.bcc = formatParticipants(m?.bcc ?? [])
        }
        h.date = summary.date.isGoZero ? "" : formatDateTime(summary.date)
        h.attachments = chipAttachments(m?.attachments ?? [], body).map { "\(chipName($0)) (\(formatSize($0.size)))" }
        let printed: Printing.Body
        if showsHTML(body), body?.htmlWithheld != true, let html = body?.html {
            printed = Printing.Body(html: html)
        } else {
            printed = Printing.Body(text: body?.text ?? s.snippet)
        }
        return Printing.message(Printing.headerRows(h), printed)
    }

    // MARK: Calendar

    /// P8: the selected occurrence's event, else the day the view stands on.
    static func printCalendar(_ calendar: CalendarController, cache: MessageCache, window: NSWindow?) {
        if let o = calendar.selectedOccurrence, let id = o.eventId, !id.rawValue.isEmpty {
            printEvent(id, recurrenceId: o.recurrenceId, calendar: calendar, cache: cache, window: window)
        } else {
            printDay(calendar.anchor, calendar: calendar, cache: cache, window: window)
        }
    }

    private static func printEvent(
        _ id: EventID, recurrenceId: DateOrTime?, calendar: CalendarController, cache: MessageCache, window: NSWindow?
    ) {
        let source = calendar.source
        let env = calendar.env
        Task {
            let e: Event
            do {
                e = try await source.event(id, recurrenceId: recurrenceId, reload: false)
            } catch {
                log.error("print: event failed: \(String(describing: type(of: error)), privacy: .public)")
                return
            }
            let s = source.snapshot
            let d = Cal.detailOf(
                e, accountName: s.accountName(e.accountId), calendarName: e.calendarId.map(s.calendarName) ?? "", env: env)
            var p = Printing.Event()
            p.title = d.title
            p.lines = [d.timeLine, d.recurrence, d.location, d.calendarLine, d.peopleLine]
            p.attendees = d.attendees.map { a in
                let who = a.name.isEmpty ? a.address : a.name
                return a.responseText.isEmpty ? who : "\(who) — \(a.responseText)"
            }
            p.notes = d.notes
            PrintJob.run(fragment: Printing.fragment([Printing.eventSection(p)], .single), title: d.title, cache: cache,
                         window: window)
        }
    }

    /// P7: the day's events as the agenda lists them (all-day first).
    private static func printDay(_ day: Date, calendar: CalendarController, cache: MessageCache, window: NSWindow?) {
        let env = calendar.env
        let now = Date()
        let items = Cal.agenda(
            calendar.occurrences, from: day, days: 1, now: now, env: env, showDeclined: calendar.showDeclined
        ).first?.items ?? []
        let heading = Cal.dayHeading(day, now: now, env: env)
        let rows = items.map {
            Printing.DayItem(time: Cal.itemTime($0, env: env), title: $0.occurrence.title,
                             location: $0.occurrence.location ?? "")
        }
        PrintJob.run(fragment: Printing.fragment([Printing.daySection(heading, rows, none: Cal.agendaEmpty)], .single),
                     title: heading, cache: cache, window: window)
    }
}

/// Whether a callback that may come again has been taken (main thread only).
private final class Once: @unchecked Sendable {
    var done = false
}

/// One print: the off-screen view, kept until its operation ended.
@MainActor
private final class PrintJob: NSObject {
    private static var running: [PrintJob] = []
    private static let log = Logger(subsystem: "io.github.schotek.Malachi", category: "print")

    private let view: MessageWebView
    /// A window never shown: WebKit lays the document out (and paginates
    /// it) only for a view in a window; off one it printed an endless run
    /// of empty pages.
    private let host: NSWindow
    private let title: String
    private weak var window: NSWindow?
    private var started = false

    static func run(fragment: String, title: String, cache: MessageCache, window: NSWindow?) {
        let job = PrintJob(cache: cache, title: title, window: window)
        running.append(job)
        job.start(fragment)
    }

    private init(cache: MessageCache, title: String, window: NSWindow?) {
        view = MessageWebView(cache: cache, zoom: 100)
        host = NSWindow(contentRect: NSRect(x: -10000, y: -10000, width: 612, height: 792), styleMask: [.borderless],
                        backing: .buffered, defer: false)
        host.isReleasedWhenClosed = false
        self.title = title
        self.window = window
        super.init()
    }

    private func start(_ fragment: String) {
        // A page's width at 72 dpi; WebKit paginates the document again
        // for the paper chosen.
        host.contentView = view
        view.frame = NSRect(x: 0, y: 0, width: 612, height: 792)
        view.layoutSubtreeIfNeeded()
        view.onFinishedLoading = { [weak self] in
            guard let self, !self.started else { return }
            self.started = true
            // Let the pictures decode and the layout settle.
            DispatchQueue.main.async {
                self.print()
            }
        }
        view.onUnavailable = { [weak self] in
            Self.log.error("print: the locked view is unavailable")
            self?.finish()
        }
        view.loadPrint(fragment: fragment)
    }

    private func print() {
        guard let info = NSPrintInfo.shared.copy() as? NSPrintInfo else {
            finish()
            return
        }
        info.horizontalPagination = .fit
        info.verticalPagination = .automatic
        info.isHorizontallyCentered = false
        info.isVerticallyCentered = false
        info.dictionary()[NSPrintInfo.AttributeKey.headerAndFooter] = true
        let pdf = ProcessInfo.processInfo.environment["MALACHI_PRINT_PDF"].flatMap { $0.isEmpty ? nil : $0 }
        if let pdf {
            info.jobDisposition = .save
            info.dictionary()[NSPrintInfo.AttributeKey.jobSavingURL] = URL(fileURLWithPath: pdf)
            // A development run never writes more than this.
            info.dictionary()[NSPrintInfo.AttributeKey.firstPage] = 1
            info.dictionary()[NSPrintInfo.AttributeKey.lastPage] = 20
        }
        view.layoutSubtreeIfNeeded()
        let op = view.printOperation(with: info)
        op.jobTitle = title
        if pdf != nil {
            op.showsPrintPanel = false
            op.showsProgressPanel = false
        }
        // Always modal for a window: WebKit draws the pages asynchronously,
        // and a synchronous `run()` printed blank pages without end.
        op.runModal(for: window ?? host, delegate: self, didRun: #selector(didRun(_:success:contextInfo:)), contextInfo: nil)
    }

    /// AppKit ends a modal print operation on a thread of its own and
    /// calls this there: the window and the view are let go on the main
    /// thread (`setContentView` off it aborted the process).
    @objc nonisolated private func didRun(_ op: NSPrintOperation, success: Bool, contextInfo: UnsafeMutableRawPointer?) {
        if let pdf = ProcessInfo.processInfo.environment["MALACHI_PRINT_PDF"], !pdf.isEmpty {
            FileHandle.standardError.write(Data("LAYOUT: print saved \(success ? 1 : 0) to \(pdf)\n".utf8))
        }
        let id = ObjectIdentifier(self)
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                Self.running.first { ObjectIdentifier($0) == id }?.finish()
            }
        }
    }

    private func finish() {
        host.contentView = nil
        host.close()
        Self.running.removeAll { $0 === self }
    }
}
