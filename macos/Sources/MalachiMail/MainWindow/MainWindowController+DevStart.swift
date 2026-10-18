// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature: puts the main window through a few states
// at start-up so that its layout can be looked at without clicking, and
// prints where the toolbar's items and the split views' dividers are.
// Read once, only from the environment; without `MALACHI_START` nothing
// here runs.
//
//   MALACHI_START="mail;board:list;board:list:nav-off;board:columns;board:today;size=800x700;mail"
//   A last step `quit` quits the application as ⌘Q does.
//   `board:<style>:select` selects the case with the most conversation
//   text; every board dump lists the detail's conversation cards (frame,
//   text frame, height the text needs), the detail's document against its
//   visible height, and what a wheel event over the first card does.
//   `compose` opens a blank compose window, dumps how it assembled (its
//   content view controller, first responder, editor frame) and closes it.
//   `reply-pane` (Board with MALACHI_BOARD_SAMPLES=1) has the page's real
//   inline reply host (`BoardReplyEditorHost.developmentShowBlank`) show a
//   blank board-owned pane (never saved, abandoned at the end) in the List's
//   reply slot of the first case with a suggested reply, types a few and
//   then many lines into it through the editor's API, rebuilds the detail
//   around it (Why is this here? twice) and dumps the heights, the pane's
//   place and the keyboard after each step (`DevelopmentReplyPane`). Give
//   both a longer MALACHI_START_INTERVAL (8 s for `reply-pane`).
//   `board-html` (Board with MALACHI_BOARD_SAMPLES=1 only) selects the
//   sample case with the most messages in the List, hands its newest card
//   an invented, sanitised-looking HTML document through the path
//   message.body's answer takes (`BoardConversationBlock.developmentFeed`),
//   and dumps the card's reported height and frame, the live web views,
//   the detail's document against its visible height before and after,
//   the web views made by a repeated identical refresh, where the wheel
//   over the web view goes, and the scroll position when an older card
//   above the viewport opens with the same document (`DevelopmentBoardHTML`;
//   10 s for MALACHI_START_INTERVAL).
//   `calendar:day|week|month|agenda` shows the Calendar in that view
//   and dumps the page's main frames (sidebar, content, assistant slot),
//   the calendar split view's divider and where the toolbar's sidebar
//   separator sits against it (`developmentCalendarLines`).
//   `event-popover` (Calendar, best with MALACHI_CALENDAR_SAMPLES=1) shows
//   the Week, clicks the block of the sample's "Call with the supplier"
//   (else the first block) and dumps the detail popover's frame and rows;
//   `event-editor` opens New Event with the defaults, dumps the window,
//   the label column, the grid's rows and the availability view, and
//   closes it unsaved; `invitation-card` opens a window with one sample
//   card per state (`Cal.sampleData(...).cards`) and dumps each card and
//   its rows; `caldav-sheet` opens Settings → Integrations with the Add CalDAV
//   Calendar sheet (state `entering`), dumps it and cancels;
//   `prefs-calendar` opens Settings → Calendar (with
//   MALACHI_CALENDAR_SAMPLES=1 the sample accounts' sections), dumps the
//   Display group and each section, and closes Settings;
//   `prefs-integrations` opens Settings → Integrations, dumps its
//   connected rows and catalogue rows, and closes Settings.
//   `crm-dialog` and `eway-wizard`: MainWindowController+DevStartCRM.swift.
//   Each prints
//   `LAYOUT:` lines and a `LAYOUT: shot <step> window <n>` line naming
//   the window worth a screenshot (`DevelopmentCalendar`); the
//   `calendar:*` dumps add `LAYOUT:` lines for the sidebar's sections,
//   the day header, the all-day row, the first block and a month cell.
//   MALACHI_START_SIZE=1600x900   (the window's size, optional)
//   MALACHI_START_INTERVAL=3      (seconds per step, optional)
//   MALACHI_START_TRACE=1         (every window resize to stderr; on a
//                                  shrink, the constraints above 250 that
//                                  set the content's width)
//
// Each step is shown, then after a second the window's number and the
// frames go to stderr (and, once the board was shown, its phase, counts,
// notice and empty texts, and the triage's control, each board toolbar's
// Triage item and the status strip's line and note); after the last step
// the hook does nothing more.
//
// The board steps show whichever board the window has: the daemon's, or
// with MALACHI_BOARD_SAMPLES=1 the invented sample cases
// (`MainWindowController.boardSamples`), so both can be looked at.

extension MainWindowController {
    func applyDevelopmentStart() {
        let env = ProcessInfo.processInfo.environment
        guard let spec = env["MALACHI_START"], !spec.isEmpty, window != nil else { return }
        if let size = env["MALACHI_START_SIZE"] {
            // After the window's frame was restored.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                self?.developmentResize(size)
            }
        }
        if env["MALACHI_START_TRACE"] != nil, let window {
            var last = window.frame.width
            NotificationCenter.default.addObserver(forName: NSWindow.didResizeNotification, object: window, queue: .main) { _ in
                MainActor.assumeIsolated {
                    let w = window.frame.width
                    var text = String(format: "MALACHI_START TRACE width %.0f -> %.0f\n", last, w)
                    if w < last, let content = window.contentView {
                        for c in content.constraintsAffectingLayout(for: .horizontal) where c.priority.rawValue > 250 {
                            text += "  \(c.priority.rawValue) \(c)\n"
                        }
                    }
                    last = w
                    FileHandle.standardError.write(Data(text.utf8))
                }
            }
        }
        let interval = env["MALACHI_START_INTERVAL"].flatMap(Double.init) ?? 3
        let steps = spec.split(separator: ";").map(String.init)
        // Started from a terminal behind other windows, the app would nap
        // and its timers run late.
        let activity = ProcessInfo.processInfo.beginActivity(options: .userInitiated, reason: "MALACHI_START")
        DispatchQueue.main.asyncAfter(deadline: .now() + interval * Double(steps.count) + 2) {
            ProcessInfo.processInfo.endActivity(activity)
        }
        for (i, step) in steps.enumerated() {
            DispatchQueue.main.asyncAfter(deadline: .now() + interval * Double(i) + 1) { [weak self] in
                self?.developmentStep(step)
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + interval * Double(i) + 2.5) { [weak self] in
                self?.developmentDump("step \(i) \(step)")
            }
        }
    }

    /// "1600x900": the window's size.
    private func developmentResize(_ size: String) {
        let parts = size.split(separator: "x").compactMap { Double($0) }
        guard parts.count == 2, let window else { return }
        var frame = window.frame
        frame.size = NSSize(width: parts[0], height: parts[1])
        // As a resize by the user would: the delegate hears of it first.
        frame.size = windowWillResize(window, to: frame.size)
        window.setFrame(frame, display: true)
        FileHandle.standardError.write(Data(String(format: "MALACHI_START resize %@ asked %@ got %@\n", size,
                                                   NSStringFromSize(frame.size), NSStringFromRect(window.frame)).utf8))
    }

    private func developmentStep(_ step: String) {
        if step == "quit" {
            // The application quits as from the menu (the daemon it
            // started stops with it). Not from inside this main-queue
            // block: `terminateLater` waits in a run loop while the main
            // queue, and with it the main actor, could not drain.
            NSApp.perform(#selector(NSApplication.terminate(_:)), with: nil, afterDelay: 0)
            return
        }
        if step == "compose" {
            developmentCompose()
            return
        }
        if step == "reply-pane" {
            DevelopmentReplyPane.run(in: self)
            return
        }
        if step == "board-html" {
            DevelopmentBoardHTML.run(in: self)
            return
        }
        if DevelopmentCalendar.step(step, in: self) {
            return
        }
        if DevelopmentCRM.step(step, in: self) {
            return
        }
        if step.hasPrefix("size=") {
            developmentResize(String(step.dropFirst(5)))
            return
        }
        let parts = step.split(separator: ":").map(String.init)
        guard let first = parts.first else { return }
        if first == "mail" {
            setMode(.mail)
            DispatchQueue.main.async { developmentSplitTrace("next runloop", self.split) }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.6) { developmentSplitTrace("0.6 s", self.split) }
            return
        }
        if first == "calendar" {
            setMode(.calendar)
            let nick = parts.count > 1 ? parts[1] : Cal.defaultView.nick
            if let v = Cal.View.allCases.first(where: { $0.nick == nick }), calendarController.view != v {
                calendarController.setView(v)
            }
            return
        }
        setMode(.board)
        let style: Board.Style
        switch parts.count > 1 ? parts[1] : "list" {
        case "columns": style = .columns
        case "today": style = .today
        default: style = .list
        }
        if board.state.style != style {
            board.setStyle(style)
        }
        if parts.contains("nav-off"), let list = boardPage.sidebarTarget as? BoardListViewController,
           list.splitViewItems.first?.isCollapsed == false
        {
            list.toggleSidebar(nil)
        }
        if parts.contains("select") {
            developmentSelectLongestConversation()
        }
    }

    /// The Calendar's frames: the page's views, its split view's divider,
    /// the toolbar's sidebar separator against it (whichever toolbar shows:
    /// after Mail → Calendar → Board → Mail it must sit on the divider of
    /// the split view in the window), and the controller's view and title.
    private func developmentCalendarLines() -> [String] {
        guard let window else { return [] }
        var lines: [String] = []
        var separator: NSRect?
        func walk(_ v: NSView) {
            if v.responds(to: NSSelectorFromString("item")), let item = v.value(forKey: "item") as? NSToolbarItem,
               item.itemIdentifier == .sidebarTrackingSeparator, !v.isHiddenOrHasHiddenAncestor
            {
                separator = v.convert(v.bounds, to: nil)
            }
            v.subviews.forEach(walk)
        }
        if let frame = window.contentView?.superview {
            walk(frame)
        }
        // The sidebar divider of the split view the window shows.
        var divider: CGFloat?
        if mode == .calendar, let page = calendarExtras.page, page.isViewLoaded, !page.sidebarItem.isCollapsed {
            let v = page.sidebarContainer.view
            divider = v.convert(v.bounds, to: nil).maxX
        } else if mode == .mail, split.isViewLoaded, split.view.window != nil, let first = split.splitView.arrangedSubviews.first {
            divider = first.convert(first.bounds, to: nil).maxX
        } else if mode == .board, let list = boardPage.listSplitView, list.window != nil, let first = list.arrangedSubviews.first {
            divider = first.convert(first.bounds, to: nil).maxX
        }
        // The toolbar's views AppKit lays out (their class names change
        // between releases): every one whose class names a separator or an
        // item, with its frame and item.
        func toolbarViews(_ v: NSView) {
            let cls = NSStringFromClass(type(of: v))
            if cls.contains("Separator") || cls.contains("ItemView") || cls.contains("Tracking") {
                var name = ""
                if v.responds(to: NSSelectorFromString("item")), let item = v.value(forKey: "item") as? NSToolbarItem {
                    name = item.itemIdentifier.rawValue
                    if item.itemIdentifier == .sidebarTrackingSeparator, separator == nil {
                        separator = v.convert(v.bounds, to: nil)
                    }
                }
                let r = v.convert(v.bounds, to: nil)
                lines.append(String(format: "    toolbar view %@ %@ %.0f..%.0f hidden %d", cls, name, r.minX, r.maxX,
                                    v.isHiddenOrHasHiddenAncestor ? 1 : 0))
            }
            v.subviews.forEach(toolbarViews)
        }
        if let frame = window.contentView?.superview {
            toolbarViews(frame)
        }
        for item in window.toolbar?.items ?? [] {
            let f = item.view.map { v in
                v.window == nil ? "off-window \(NSStringFromSize(v.frame.size))" : NSStringFromRect(v.convert(v.bounds, to: nil))
            } ?? "no view"
            var extra = ""
            if let s = item as? NSSearchToolbarItem {
                extra = String(format: " field %.0f preferred %.0f", s.searchField.frame.width, s.preferredWidthForSearchField)
            }
            lines.append("    toolbar item \(item.itemIdentifier.rawValue) visible \(item.isVisible ? 1 : 0) \(f)"
                // Through KVC: the properties are deprecated, the values still useful.
                + " min \((item.value(forKey: "minSize") as? NSValue)?.sizeValue ?? .zero)"
                + " max \((item.value(forKey: "maxSize") as? NSValue)?.sizeValue ?? .zero)"
                + " priority \(item.visibilityPriority.rawValue)\(extra)"
                + (item is NSTrackingSeparatorToolbarItem ? " (tracking separator)" : ""))
        }
        if let title = window.standardWindowButton(.closeButton)?.superview {
            func titles(_ v: NSView) {
                if let t = v as? NSTextField, !t.stringValue.isEmpty, !v.isHiddenOrHasHiddenAncestor,
                   t.stringValue == window.title || t.stringValue == window.subtitle
                {
                    let r = v.convert(v.bounds, to: nil)
                    lines.append(String(format: "    title field '%@' %.0f..%.0f", t.stringValue, r.minX, r.maxX))
                }
                v.subviews.forEach(titles)
            }
            titles(title)
        }
        let sep = separator.map { String(format: "%.0f..%.0f", $0.minX, $0.maxX) } ?? "none"
        let div = divider.map { String(format: "%.0f", $0) } ?? "none"
        var on = "n/a"
        if let separator, let divider {
            on = abs(separator.maxX - divider) <= 2 || abs(separator.minX - divider) <= 2 ? "yes" : "NO"
        }
        lines.append("  sidebar separator \(sep) divider \(div) on divider \(on) (\(mode.nick))")
        // On releases that draw no view for the tracking separator, the
        // first item after it tells where it is bound: it starts at the
        // divider (within the item spacing) when the separator tracks it.
        var viewerX: [String: CGFloat] = [:]
        func viewers(_ v: NSView) {
            if v.responds(to: NSSelectorFromString("item")), let item = v.value(forKey: "item") as? NSToolbarItem,
               !v.isHiddenOrHasHiddenAncestor, NSStringFromClass(type(of: v)).contains("ItemViewer")
            {
                viewerX[item.itemIdentifier.rawValue] = v.convert(v.bounds, to: nil).minX
            }
            v.subviews.forEach(viewers)
        }
        if let frame = window.contentView?.superview {
            viewers(frame)
        }
        var after = "none"
        if let items = window.toolbar?.items, let i = items.firstIndex(where: { $0.itemIdentifier == .sidebarTrackingSeparator }) {
            if let next = items[(i + 1)...].first(where: { viewerX[$0.itemIdentifier.rawValue] != nil }),
               let x = viewerX[next.itemIdentifier.rawValue]
            {
                let bound = divider.map { x >= $0 - 2 && x <= $0 + 16 ? "yes" : "NO" } ?? "n/a"
                after = String(format: "%@ at %.0f bound %@", next.itemIdentifier.rawValue, x, bound)
            }
        } else {
            after = "no tracking separator item"
        }
        lines.append("LAYOUT: tracking separator \(sep) sidebar divider \(div) on divider \(on);"
            + " first item after it \(after) (\(mode.nick))")
        if mode == .calendar, let page = calendarExtras.page {
            lines += page.developmentFrames
            if page.splitView.window != nil {
                let edges = page.splitView.arrangedSubviews.dropLast().map {
                    String(format: "%.0f", $0.convert($0.bounds, to: nil).maxX)
                }
                lines.append("  split calendar width \(Int(page.splitView.frame.width)) dividers \(edges.joined(separator: ", "))")
            }
            let c = calendarController
            lines.append("  calendar view \(c.view.nick) title '\(c.title)' subtitle '\(c.subtitle)' status '\(c.statusLine)'"
                + " unanswered '\(c.unansweredLine)' samples \(state.calendar.samples)")
            lines += DevelopmentCalendar.pageLayout(page)
            if let bar = content.statusBar as? StatusBarViewController {
                lines.append("  status bar '\(bar.shownTexts.line)' note '\(bar.shownTexts.note)'")
            }
        }
        return lines
    }

    /// `board:<style>:select`: the case whose conversation has the most
    /// text (the samples' contract renewal), so that its cards are dumped.
    private func developmentSelectLongestConversation() {
        let v = board.view
        let ids = v.sections.flatMap { $0.rows.map(\.id) } + v.columns.flatMap { $0.rows.map(\.id) }
        var best: (Board.CaseID, Int)?
        for id in ids {
            board.select(id)
            let n = board.view.detail?.messages.reduce(0) { $0 + $1.text.utf8.count } ?? 0
            if n > (best?.1 ?? -1) {
                best = (id, n)
            }
        }
        board.select(best?.0)
    }

    /// The board detail's conversation cards (each card's frame, its
    /// text's frame and the height the whole text needs), the detail's
    /// document against its visible height, and whether a wheel event
    /// over the first card's text moves the detail.
    fileprivate func developmentConversationLines() -> [String] {
        var lines: [String] = []
        var scrolls: [NSScrollView] = []
        func walk(_ v: NSView) {
            if let s = v as? NSScrollView, let doc = s.documentView,
               NSStringFromClass(type(of: doc)).contains("BoardDetailDocument"), !s.isHiddenOrHasHiddenAncestor
            {
                scrolls.append(s)
            }
            v.subviews.forEach(walk)
        }
        if let root = window?.contentView {
            walk(root)
        }
        // Every conversation block the board's two details hold, shown or
        // not, and the web views in each (`board-html`).
        for d in boardPage.children.flatMap({ [$0] + $0.children }).compactMap({ $0 as? BoardDetailViewController }) {
            if let b = d.view.firstSubview(of: BoardConversationBlock.self) {
                lines.append("  board detail \(d.presentation) in window \(b.window != nil) hidden \(b.isHiddenOrHasHiddenAncestor)"
                    + " cards \(b.cards.count) live web views \(b.developmentLiveWebViews) made so far \(BoardMessageCardView.webViewsMade)")
            }
        }
        for s in scrolls {
            let doc = s.documentView!
            lines.append(String(format: "  board detail scroll document %.0f visible %.0f (%@)", doc.frame.height,
                                s.contentView.bounds.height, doc.frame.height > s.contentView.bounds.height + 0.5 ? "scrolls" : "fits"))
            var cards: [BoardMessageCardView] = []
            func find(_ v: NSView) {
                if let c = v as? BoardMessageCardView {
                    cards.append(c)
                }
                v.subviews.forEach(find)
            }
            find(doc)
            for (i, c) in cards.enumerated() {
                lines.append("    message \(i): \(c.developmentMetrics)")
            }
            // A wheel event delivered where the pointer over the first
            // card's text would deliver it (never posted to the screen).
            if let c = cards.first, let target = c.hitTest(c.convert(NSPoint(x: c.bounds.midX, y: c.bounds.midY), to: c.superview)),
               let cg = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: -40, wheel2: 0, wheel3: 0),
               let event = NSEvent(cgEvent: cg)
            {
                let before = s.contentView.bounds.origin.y
                target.scrollWheel(with: event)
                let after = s.contentView.bounds.origin.y
                lines.append(String(format: "    wheel over %@: detail origin %.0f -> %.0f", NSStringFromClass(type(of: target)), before, after))
                s.contentView.scroll(to: NSPoint(x: 0, y: before))
                s.reflectScrolledClipView(s.contentView)
                // Again over the newest card's text while it is being
                // selected (its field editor in place).
                if let last = cards.last, let window, window.makeFirstResponder(last.developmentBody) {
                    let editor: NSView? = last.developmentBody.currentEditor()
                    let center = last.convert(NSPoint(x: last.bounds.midX, y: last.bounds.midY), to: last.superview)
                    if let t = last.hitTest(center) {
                        t.scrollWheel(with: event)
                        lines.append(String(format: "    wheel over the selected text (%@, editor %@): detail origin %.0f -> %.0f",
                                            NSStringFromClass(type(of: t)), editor.map { NSStringFromRect($0.frame) } ?? "none",
                                            before, s.contentView.bounds.origin.y))
                    }
                    window.makeFirstResponder(nil)
                    s.contentView.scroll(to: NSPoint(x: 0, y: before))
                    s.reflectScrolledClipView(s.contentView)
                }
            }
        }
        return lines
    }

    private func developmentDump(_ label: String) {
        guard let window else { return }
        var lines = ["MALACHI_START \(label): window \(window.windowNumber) \(Int(window.frame.width))x\(Int(window.frame.height))"
            + " title '\(window.title)' visible \(window.titleVisibility == .visible)"]
        let d = UserDefaults.standard
        lines.append(String(format: "  frame %@ fitting %.0f stored sidebar %@ list %@ autosave '%@'",
                            NSStringFromRect(window.frame), window.contentView?.fittingSize.width ?? -1,
                            String(describing: d.object(forKey: MainSplitViewController.sidebarWidthKey) ?? "nil"),
                            String(describing: d.object(forKey: MainSplitViewController.listWidthKey) ?? "nil"),
                            d.string(forKey: "NSWindow Frame \(Self.frameAutosaveName)") ?? "nil"))
        var found: [(CGFloat, String)] = []
        func walk(_ v: NSView) {
            let cls = NSStringFromClass(type(of: v))
            if cls.contains("ItemViewer") || cls.contains("TitleField"), !v.isHiddenOrHasHiddenAncestor {
                let r = v.convert(v.bounds, to: nil)
                var name = cls
                if v.responds(to: NSSelectorFromString("item")), let item = v.value(forKey: "item") as? NSToolbarItem {
                    name = item.itemIdentifier.rawValue
                }
                if r.width > 0 {
                    found.append((r.minX, String(format: "  %@ %.0f..%.0f", name, r.minX, r.maxX)))
                }
            }
            v.subviews.forEach(walk)
        }
        if let frame = window.contentView?.superview {
            walk(frame)
        }
        lines += found.sorted { $0.0 < $1.0 }.map(\.1)
        if mode == .board {
            let v = board.view
            let rows = v.sections.reduce(0) { $0 + $1.rows.count }
            lines.append("  board \(boardSource == nil ? "samples" : "daemon") phase \(v.phase) empty \(v.isEmpty)"
                + " sections \(v.sections.map { "\($0.kind):\($0.rows.count)" }.joined(separator: ",")) rows \(rows)"
                + " columns \(v.columns.map { String($0.rows.count) }.joined(separator: "/"))"
                + " detail \(v.detail != nil) notice '\(v.notice)' emptyTitle '\(v.emptyTitle)'")
        }
        lines += triageDevelopmentLines
        if mode == .board {
            lines += developmentConversationLines()
        }
        for (name, split) in [("mail", split.splitView), ("board", boardPage.listSplitView)] {
            guard let split, split.window != nil else { continue }
            let edges = split.arrangedSubviews.dropLast().map { String(format: "%.0f", $0.convert($0.bounds, to: nil).maxX) }
            lines.append("  split \(name) width \(Int(split.frame.width)) dividers \(edges.joined(separator: ", "))")
        }
        // The List style's detail: the document (the pane's visible width)
        // and the content column in it.
        func detailColumns(_ v: NSView) {
            if NSStringFromClass(type(of: v)).contains("BoardDetailDocument"), !v.isHiddenOrHasHiddenAncestor {
                for c in v.subviews {
                    lines.append(String(format: "  board detail document width %.0f column %.0f..%.0f (width %.0f, margins %.0f / %.0f)",
                                        v.bounds.width, c.frame.minX, c.frame.maxX, c.frame.width,
                                        c.frame.minX, v.bounds.width - c.frame.maxX))
                }
            }
            v.subviews.forEach(detailColumns)
        }
        if let board = boardPage.listSplitView, board.window != nil {
            detailColumns(board)
        }
        if split.isViewLoaded, split.view.window != nil {
            let r = split.view.convert(split.view.bounds, to: nil)
            lines.append(String(format: "  mail split frame %.0f..%.0f, content %.0f", r.minX, r.maxX, content.view.frame.width))
            for item in split.splitViewItems {
                let f = item.viewController.view.convert(item.viewController.view.bounds, to: nil)
                lines.append(String(format: "    item %.0f..%.0f collapsed %d min %.0f max %.0f", f.minX, f.maxX,
                                    item.isCollapsed ? 1 : 0, item.minimumThickness, item.maximumThickness))
            }
        }
        lines += developmentCalendarLines()
        FileHandle.standardError.write(Data((lines.joined(separator: "\n") + "\n").utf8))
        developmentSplitTrace("dump \(label)", split)
    }
}

// With MALACHI_START_TRACE: the mail split view's state (its panes and
// AppKit's NSSplitView.PreferredSize constraints, whose priorities decide
// whether the panes or the window give way) at a stage.
@MainActor func developmentSplitTrace(_ stage: String, _ split: MainSplitViewController) {
    guard ProcessInfo.processInfo.environment["MALACHI_START_TRACE"] != nil else { return }
    let sv = split.splitView
    var text = String(format: "MALACHI_START STAGE %@: window %.0f, ctrl %.0f, splitView %.0f, in window %d\n", stage,
                      split.view.window?.frame.width ?? -1, split.view.frame.width, sv.frame.width, split.view.window != nil ? 1 : 0)
    text += "    items " + split.splitViewItems.map { String(format: "%.0f%@", $0.viewController.view.frame.width, $0.isCollapsed ? "c" : "") }
        .joined(separator: " ") + "\n"
    var all = sv.constraints
    for v in [sv] + sv.subviews { all += v.constraints }
    for c in Set(all) where (c.identifier ?? "").contains("Preferred") || (c.identifier ?? "").contains("ConstantBreadth") {
        text += "    \(c.identifier ?? "") = \(c.constant) @\(c.priority.rawValue) active \(c.isActive)\n"
    }
    FileHandle.standardError.write(Data(text.utf8))
}

// MARK: - compose and reply-pane (the compose pane's extraction, P4)

extension MainWindowController {
    /// `compose`: a blank compose window, how it assembled, then closed
    /// (nothing typed, so it closes without a question).
    fileprivate func developmentCompose() {
        state.hooks.composeNew?()
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
            let wcs = NSApp.windows.compactMap { $0.windowController as? ComposeWindowController }
            guard let wc = wcs.first, let w = wc.window else {
                developmentPrint("MALACHI_START compose: no compose window")
                return
            }
            let pane = wc.pane
            let editor = pane.editor.view
            let responder = w.firstResponder.map { NSStringFromClass(type(of: $0)) } ?? "nil"
            let send = w.toolbar?.items.first { $0.itemIdentifier.rawValue == "composeSend" }
            developmentPrint(String(
                format: "MALACHI_START compose: windows %d title '%@' contentViewController pane %d contentView is pane.view %d"
                    + " firstResponder %@ initialFirstResponder is To %d editor %@ (in window %@) toolbar %@"
                    + " send enabled %d status '%@' fromRow %d",
                wcs.count, w.title, w.contentViewController === pane ? 1 : 0, w.contentView === pane.view ? 1 : 0,
                responder, w.initialFirstResponder === pane.header.toField.editor ? 1 : 0,
                NSStringFromRect(editor.frame), NSStringFromRect(editor.convert(editor.bounds, to: nil)),
                (w.toolbar?.items.map(\.itemIdentifier.rawValue) ?? []).joined(separator: ","),
                (send?.view as? NSButton)?.isEnabled == true ? 1 : 0, pane.statusLabel.stringValue,
                pane.header.fromPopup.isHiddenOrHasHiddenAncestor ? 0 : 1))
            // The responder chain from inside the content reaches the pane;
            // from the window itself, the window controller.
            let fromField = NSApp.target(forAction: Action.formatBold, to: nil, from: pane.header.subjectField)
            developmentPrint("MALACHI_START compose: formatBold target from subject \(fromField.map { NSStringFromClass(type(of: $0 as AnyObject)) } ?? "nil")"
                + " pane responds \(pane.responds(to: Action.formatBold)) window controller responds \(wc.responds(to: Action.formatBold))")
            w.performClose(nil)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                developmentPrint("MALACHI_START compose: after close, compose windows \(NSApp.windows.filter { $0.windowController is ComposeWindowController && $0.isVisible }.count)")
            }
        }
    }
}

@MainActor private func developmentPrint(_ line: String) {
    FileHandle.standardError.write(Data((line + "\n").utf8))
}

/// DEVELOPMENT AID for `reply-pane`: the page's inline reply host shows a
/// blank pane in the List's detail, which is measured. Nothing is saved
/// (the host abandons a development pane when it goes).
@MainActor
private final class DevelopmentReplyPane {
    private static var current: DevelopmentReplyPane?

    let pane: ComposePane
    let editor: ComposeEditorView
    weak var main: MainWindowController?

    private init(pane: ComposePane, editor: ComposeEditorView) {
        self.pane = pane
        self.editor = editor
    }

    static func run(in main: MainWindowController) {
        main.setMode(.board)
        if main.board.state.style != .list {
            main.board.setStyle(.list)
        }
        // The first case with a suggested reply (the samples have two).
        var ids: [Board.CaseID] = []
        for section in main.board.view.sections {
            ids += section.rows.map(\.id)
        }
        var chosen: Board.CaseID?
        for id in ids {
            main.board.select(id)
            if main.board.view.detail?.draft.isEmpty == false {
                chosen = id
                break
            }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            start(in: main, case: chosen)
        }
    }

    private static func start(in main: MainWindowController, case id: Board.CaseID?) {
        guard let id, let pane = main.boardPage.replyHost.developmentShowBlank(for: id),
              let editor = pane.editor as? ComposeEditorView
        else {
            developmentPrint("MALACHI_START reply-pane: no sample case with a suggested reply, or no environment")
            return
        }
        let dev = DevelopmentReplyPane(pane: pane, editor: editor)
        dev.main = main
        current = dev
        developmentPrint("MALACHI_START reply-pane: shown in case '\(main.board.view.detail?.title ?? "?")'"
            + " in a window \(pane.view.window != nil) in a scroll view \(pane.view.enclosingScrollView != nil)")
        dev.waitReady(tries: 0)
    }

    private func waitReady(tries: Int) {
        if editor.isReady || tries > 50 {
            report(editor.isReady ? "ready" : "not ready")
            pane.focusEditor()
            editor.focusStart()
            developmentPrint("MALACHI_START reply-pane: Send from the editor reaches "
                + chainTarget(from: pane.view.window?.firstResponder))
            if let list = main?.boardPage.shownList?.focusTarget {
                developmentPrint("MALACHI_START reply-pane: Send from the list reaches " + chainTarget(from: list))
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                self.typeLines(3, from: 1)
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                    self.report("few lines (3)")
                    self.typeLines(9, from: 4)
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                        self.report("some lines (12)")
                        self.typeLines(31, from: 13)
                        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                            self.report("many lines (43)")
                            // A rebuild of the detail around the pane, as
                            // a board refresh does.
                            self.main?.board.toggleWhy()
                            self.main?.board.toggleWhy()
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                                self.report("after two rebuilds")
                                self.leave()
                            }
                        }
                    }
                }
            }
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            self.waitReady(tries: tries + 1)
        }
    }

    /// The first responder from `start` up that handles Send (the menu's
    /// search, without the window and application ends) and its answer.
    private func chainTarget(from start: NSResponder?) -> String {
        var r = start
        while let x = r {
            if x.responds(to: Action.sendMessage) {
                let enabled = (x as? NSUserInterfaceValidations).map { v in
                    v.validateUserInterfaceItem(NSMenuItem(title: "", action: Action.sendMessage, keyEquivalent: ""))
                }
                return "\(NSStringFromClass(type(of: x))) enabled \(enabled.map(String.init) ?? "?")"
            }
            r = x.nextResponder
        }
        return "nothing"
    }

    /// Another case: the host abandons the development pane.
    private func leave() {
        guard let main else { return }
        let ids = main.board.view.sections.flatMap { $0.rows.map(\.id) }
        if let other = ids.first(where: { $0 != main.board.state.selection }) {
            main.board.select(other)
        }
        developmentPrint("MALACHI_START reply-pane: another case selected, pane in a window \(pane.view.window != nil)"
            + " draft closed \(pane.draft.draft.closed)")
        Self.current = nil
    }

    /// Lines typed at the caret through the bridge's `exec`.
    private func typeLines(_ lines: Int, from first: Int) {
        for i in first..<(first + lines) {
            editor.exec("insertText", "Line \(i) of the inline reply, typed by the development hook.")
            editor.exec("insertParagraph", nil)
        }
    }

    private func report(_ label: String) {
        guard let window = pane.view.window else {
            developmentPrint("MALACHI_START reply-pane \(label): pane not in a window")
            return
        }
        let ev = editor
        let scroll = pane.view.enclosingScrollView
        let visible = scroll?.contentView.bounds.height ?? -1
        let paneInClip = scroll.map { pane.view.convert(pane.view.bounds, to: $0.contentView) } ?? .zero
        let clip = scroll?.contentView.bounds ?? .zero
        let responder = window.firstResponder.map { NSStringFromClass(type(of: $0)) } ?? "nil"
        let inEditor = (window.firstResponder as? NSView)?.isDescendant(of: ev) == true
        developmentPrint(String(
            format: "MALACHI_START reply-pane %@: content %@ editor %.0f cap %.0f fits %d detail visible %.0f"
                + " document %.0f pane %.0f..%.0f in clip %.0f..%.0f bottom visible %d firstResponder %@ in editor %d"
                + " send %d sendKey '%@' fromRow %d",
            label, ev.contentHeight.map { String(format: "%.0f", $0) } ?? "nil", pane.editorFrameHeight,
            EditorHeight.cap(visible: visible), ev.fits ? 1 : 0, visible, scroll?.documentView?.frame.height ?? -1,
            paneInClip.minY, paneInClip.maxY, clip.minY, clip.maxY,
            paneInClip.maxY <= clip.maxY + 1 && paneInClip.maxY >= clip.minY ? 1 : 0, responder, inEditor ? 1 : 0,
            pane.sendButton?.isEnabled == true ? 1 : 0, pane.sendButton?.keyEquivalent ?? "-",
            pane.header.fromPopup.isHiddenOrHasHiddenAncestor ? 0 : 1))
    }
}

/// DEVELOPMENT AID for `board-html`, samples only: the List's detail shows
/// an invented HTML document in a conversation card through the card's
/// real path (`BoardConversationBlock.developmentFeed`), and what came of
/// it is measured. Nothing is fetched, saved or sent.
@MainActor
private enum DevelopmentBoardHTML {
    // macOS-only strings: development output on stderr, never shown.
    /// Invented content, shaped like the sanitiser's output (no scripts,
    /// no remote resources, inline styles only).
    static let document = """
    <h2 style="margin:0 0 8px">Quarterly shelf check</h2>
    <p>Hello team, the <b>shelf labels</b> in aisle 4 were checked on Tuesday. Most of them show the right price.</p>
    <p>Three labels still need a new battery; the table below lists them. Please <b>replace them by Friday</b>.</p>
    <table style="border-collapse:collapse" border="1" cellpadding="4">
    <tr><th>Label</th><th>Aisle</th><th>Battery</th></tr>
    <tr><td>A-104</td><td>4</td><td>12 %</td></tr>
    <tr><td>A-117</td><td>4</td><td>9 %</td></tr>
    <tr><td>B-020</td><td>5</td><td>4 %</td></tr>
    </table>
    <p>Thanks, <b>Invented Sender</b></p>
    """

    static func run(in main: MainWindowController) {
        guard main.boardSource == nil else {
            developmentPrint("MALACHI_START board-html: samples only (MALACHI_BOARD_SAMPLES=1)")
            return
        }
        main.setMode(.board)
        if main.board.state.style != .list {
            main.board.setStyle(.list)
        }
        let ids = main.board.view.sections.flatMap { $0.rows.map(\.id) }
        var best: (Board.CaseID, Int)?
        for id in ids {
            main.board.select(id)
            let n = main.board.view.detail?.messages.count ?? 0
            if n > (best?.1 ?? 0) {
                best = (id, n)
            }
        }
        main.board.select(best?.0)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            step1(main)
        }
    }

    private static func block(in main: MainWindowController) -> BoardConversationBlock? {
        var found: BoardConversationBlock?
        func walk(_ v: NSView) {
            if let b = v as? BoardConversationBlock, b.window != nil, !b.isHiddenOrHasHiddenAncestor {
                found = found ?? b
            }
            v.subviews.forEach(walk)
        }
        if let root = main.window?.contentView {
            walk(root)
        }
        return found
    }

    private static func developmentDumpCards(_ label: String, _ b: BoardConversationBlock) {
        let s = b.enclosingScrollView
        let doc = s?.documentView?.frame.height ?? -1
        let clip = s?.contentView.bounds ?? .zero
        developmentPrint(String(
            format: "MALACHI_START board-html %@: cards %d live web views %d made so far %d document %.0f visible %.0f top %.0f",
            label, b.cards.count, b.developmentLiveWebViews, BoardMessageCardView.webViewsMade, doc, clip.height, clip.minY))
        for (i, c) in b.cards.enumerated() {
            let f = s.map { c.convert(c.bounds, to: $0.documentView) } ?? .zero
            developmentPrint(String(format: "    card %d at %.0f..%.0f: %@", i, f.minY, f.maxY, c.developmentMetrics))
        }
    }

    private static func step1(_ main: MainWindowController) {
        guard let b = block(in: main), let newest = b.cards.indices.last else {
            developmentPrint("MALACHI_START board-html: no visible conversation block with cards")
            return
        }
        developmentDumpCards("before", b)
        b.developmentFeed(newest, html: document)
        developmentDumpCards("fed (no height yet)", b)
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
            developmentDumpCards("after the height arrived", b)
            let made = BoardMessageCardView.webViewsMade
            // Refreshes that leave the members alone, as an autosave does:
            // the detail renders twice with a rebuild of its upper part.
            main.board.toggleWhy()
            main.board.toggleWhy()
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                developmentPrint("MALACHI_START board-html: two identical refreshes made \(BoardMessageCardView.webViewsMade - made) web views,"
                    + " live \(b.developmentLiveWebViews)")
                wheel(b)
                compensation(b, main)
            }
        }
    }

    /// A wheel event delivered where the pointer over the newest card's web
    /// view would deliver it (never posted to the screen).
    private static func wheel(_ b: BoardConversationBlock) {
        guard let card = b.cards.last, let wv = card.webView, let s = b.enclosingScrollView,
              let cg = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: -40, wheel2: 0, wheel3: 0),
              let event = NSEvent(cgEvent: cg)
        else {
            developmentPrint("MALACHI_START board-html: wheel not tried (no web view)")
            return
        }
        let center = wv.convert(NSPoint(x: wv.bounds.midX, y: wv.bounds.midY), to: wv.superview)
        guard let target = wv.hitTest(center) else { return }
        let before = s.contentView.bounds.origin.y
        target.scrollWheel(with: event)
        let after = s.contentView.bounds.origin.y
        developmentPrint(String(format: "MALACHI_START board-html: wheel over %@: detail origin %.0f -> %.0f",
                                NSStringFromClass(type(of: target)), before, after))
        s.contentView.scroll(to: NSPoint(x: 0, y: before))
        s.reflectScrolledClipView(s.contentView)
    }

    /// The detail scrolled to its end, the oldest card (above the viewport)
    /// gets the document and opens: the viewport's top follows its growth.
    private static func compensation(_ b: BoardConversationBlock, _ main: MainWindowController) {
        guard b.cards.count > 1, let s = b.enclosingScrollView, let doc = s.documentView else { return }
        let clip = s.contentView
        let end = max(0, doc.frame.height - clip.bounds.height)
        clip.scroll(to: NSPoint(x: 0, y: end))
        s.reflectScrolledClipView(clip)
        let first = b.cards[0]
        let f = first.convert(first.bounds, to: doc)
        let anchor = b.cards.last.map { $0.convert($0.bounds, to: doc).minY } ?? 0
        developmentPrint(String(format: "MALACHI_START board-html: scrolled to %.0f, card 0 at %.0f..%.0f (above the viewport %d), newest card top %.0f",
                                clip.bounds.minY, f.minY, f.maxY, f.maxY <= clip.bounds.minY ? 1 : 0, anchor))
        b.developmentFeed(0, html: document)
        b.developmentOpen(0)
        let newTop = b.cards.last.map { $0.convert($0.bounds, to: doc).minY } ?? 0
        developmentPrint(String(format: "MALACHI_START board-html: card 0 opened: top %.0f, newest card top %.0f (on screen at %.0f before, %.0f now)",
                                clip.bounds.minY, newTop, anchor - (end), newTop - clip.bounds.minY))
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
            let late = b.cards.last.map { $0.convert($0.bounds, to: doc).minY } ?? 0
            developmentPrint(String(format: "MALACHI_START board-html: after card 0's height arrived: top %.0f, newest card on screen at %.0f",
                                    clip.bounds.minY, late - clip.bounds.minY))
            developmentDumpCards("end", b)
        }
    }
}

extension NSView {
    /// DEVELOPMENT AID: the first view of type `T` under this one.
    fileprivate func firstSubview<T: NSView>(of type: T.Type) -> T? {
        for v in subviews {
            if let t = v as? T ?? v.firstSubview(of: type) {
                return t
            }
        }
        return nil
    }
}

// MARK: - Calendar layout steps (docs/calendar.md §5.10, M11)

/// DEVELOPMENT AID for the calendar's `MALACHI_START` steps: measures the
/// wireframes' numbers (sidebar 240, day header 40, all-day 36, 60 pt an
/// hour, block bar 3, popover 380, editor 760×800 with a 110 label column,
/// CalDAV sheet 560×560, the invitation card's date tile at the left).
/// Nothing is saved: the editor closes unsaved, the sheet is cancelled.
@MainActor
enum DevelopmentCalendar {
    /// The windows a step opened, closed by the next calendar step.
    private static var opened: [NSWindow] = []
    private static var controllers: [InvitationController] = []

    private static func r(_ v: NSView) -> String {
        let f = v.convert(v.bounds, to: nil)
        return String(format: "(%.0f,%.0f %.0fx%.0f)", f.minX, f.minY, f.width, f.height)
    }

    private static func all<T: NSView>(_ type: T.Type, under v: NSView) -> [T] {
        var found: [T] = []
        func walk(_ v: NSView) {
            if let t = v as? T {
                found.append(t)
            }
            v.subviews.forEach(walk)
        }
        walk(v)
        return found
    }

    private static func firstText(_ v: NSView) -> String {
        all(NSTextField.self, under: v).first { !$0.stringValue.isEmpty }?.stringValue ?? ""
    }

    static func step(_ step: String, in main: MainWindowController) -> Bool {
        switch step {
        case "event-popover": eventPopover(main)
        case "event-editor": eventEditor(main)
        case "invitation-card": invitationCards(main)
        case "caldav-sheet": caldavSheet(main)
        case "prefs-calendar": prefsCalendar(main)
        case "prefs-integrations": prefsIntegrations(main)
        default: return false
        }
        return true
    }

    /// The `calendar:*` dumps' layout lines.
    static func pageLayout(_ page: CalendarPageViewController) -> [String] {
        guard page.isViewLoaded, page.view.window != nil else { return [] }
        var lines = ["LAYOUT: calendar sidebar \(r(page.sidebarContainer.view)) (wireframe width 240)"
            + " content \(r(page.contentContainer.view))"]
        for h in all(SidebarSectionHeading.self, under: page.sidebar.view) where !h.isHiddenOrHasHiddenAncestor {
            lines.append("LAYOUT: calendar sidebar section '\(firstText(h))' \(r(h))")
        }
        let rows = all(SidebarCalendarRow.self, under: page.sidebar.view).filter { !$0.isHiddenOrHasHiddenAncestor }
        if let first = rows.first {
            lines.append("LAYOUT: calendar sidebar calendar rows \(rows.count) first \(r(first))")
        }
        let content = page.contentContainer.view
        if let week = all(CalendarWeekView.self, under: content).first(where: { !$0.isHiddenOrHasHiddenAncestor }) {
            lines.append("LAYOUT: calendar day header \(r(week.header)) (wireframe height 40)")
            lines.append("LAYOUT: calendar all-day \(r(week.allDay)) (wireframe height 36 minimum)")
            lines.append(String(format: "LAYOUT: calendar grid height %.0f for %d min = %.1f pt/hour (wireframe 60)",
                                week.grid.frame.height, Cal.dayMinutes, week.grid.frame.height * 60 / CGFloat(Cal.dayMinutes)))
            if let b = week.grid.blocks.first {
                lines.append("LAYOUT: calendar first block \(r(b)) '\(b.accessibilityLabel() ?? "")' bar \(CalendarMetrics.blockBar)"
                    + " tint \(CalendarMetrics.blockTint) (wireframe tint + 3 pt bar)")
            }
        }
        if let month = all(CalendarMonthView.self, under: content).first(where: { !$0.isHiddenOrHasHiddenAncestor }) {
            if let cell = month.cells.first {
                lines.append("LAYOUT: calendar month cells \(month.cells.count) first cell \(r(cell)) month \(r(month))")
            }
        }
        return lines
    }

    private static func closeOpened() {
        EventDetailPopover.close()
        for w in opened {
            if let wc = w.windowController as? EventEditorWindowController {
                wc.closeUnasked()
            } else {
                w.close()
            }
        }
        opened = []
        controllers = []
    }

    // MARK: event-popover

    private static func eventPopover(_ main: MainWindowController) {
        closeOpened()
        main.setMode(.calendar)
        if main.calendarController.view != .week {
            main.calendarController.setView(.week)
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
            let content = main.calendarPage.contentContainer.view
            let blocks = all(EventBlockView.self, under: content).filter { !$0.isHiddenOrHasHiddenAncestor }
            guard let b = blocks.first(where: { ($0.accessibilityLabel() ?? "").localizedCaseInsensitiveContains("supplier") })
                ?? blocks.first
            else {
                developmentPrint("LAYOUT: event-popover no block in the Week")
                return
            }
            developmentPrint("LAYOUT: event-popover block \(r(b)) '\(b.accessibilityLabel() ?? "")'")
            b.onClick?(b)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.7) {
                guard let w = NSApp.windows.first(where: { NSStringFromClass(type(of: $0)).contains("Popover") && $0.isVisible }),
                      let root = w.contentView
                else {
                    developmentPrint("LAYOUT: event-popover no popover shown (isShown \(EventDetailPopover.isShown))")
                    return
                }
                let detail = all(EventDetailRootView.self, under: root).first
                let stack = detail.flatMap { all(NSStackView.self, under: $0).first }
                let rows = stack?.arrangedSubviews.filter { !$0.isHidden } ?? []
                developmentPrint(String(format: "LAYOUT: event-popover window %ld frame %@ content %@ (wireframe width 380) rows %d",
                                        w.windowNumber, NSStringFromRect(w.frame), detail.map(r) ?? "none", rows.count))
                for (i, row) in rows.enumerated() {
                    developmentPrint("LAYOUT: event-popover row \(i) \(r(row)) '\(firstText(row).prefix(40))'")
                }
                developmentPrint("LAYOUT: shot event-popover window \(main.window?.windowNumber ?? 0)")
                developmentPrint("LAYOUT: shot event-popover-window window \(w.windowNumber)")
            }
        }
    }

    // MARK: event-editor

    private static func eventEditor(_ main: MainWindowController) {
        closeOpened()
        let before = Set(NSApp.windows.map(ObjectIdentifier.init))
        EventEditorManager.shared.openNew(main.calendarController.newEventDefaults())
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            guard let w = NSApp.windows.first(where: {
                $0.windowController is EventEditorWindowController && !before.contains(ObjectIdentifier($0))
            }), let root = w.contentView else {
                developmentPrint("LAYOUT: event-editor no window")
                return
            }
            opened.append(w)
            developmentPrint(String(format: "LAYOUT: event-editor window %ld frame %@ content %@ (wireframe 760x800) title '%@'",
                                    w.windowNumber, NSStringFromRect(w.frame), NSStringFromRect(root.frame), w.title))
            if let grid = all(NSGridView.self, under: root).first {
                for i in 0..<grid.numberOfRows {
                    let label = grid.cell(atColumnIndex: 0, rowIndex: i).contentView
                    let field = grid.cell(atColumnIndex: 1, rowIndex: i).contentView
                    if grid.row(at: i).isHidden {
                        continue
                    }
                    developmentPrint("LAYOUT: event-editor row \(i) label \(label.map(r) ?? "-") '\(label.map(firstText) ?? "")'"
                        + " field \(field.map(r) ?? "-")")
                }
                if let l = grid.cell(atColumnIndex: 0, rowIndex: 1).contentView, let f = grid.cell(atColumnIndex: 1, rowIndex: 1).contentView {
                    let lf = l.convert(l.bounds, to: nil), ff = f.convert(f.bounds, to: nil)
                    developmentPrint(String(format: "LAYOUT: event-editor label column width %.0f (wireframe 110) label right %.0f field x %.0f grid %@",
                                            grid.column(at: 0).width, lf.maxX, ff.minX, r(grid)))
                }
            }
            if let a = all(AvailabilityView.self, under: root).first {
                developmentPrint("LAYOUT: event-editor availability \(r(a)) hidden \(a.isHiddenOrHasHiddenAncestor)")
            }
            developmentPrint("LAYOUT: shot event-editor window \(w.windowNumber)")
            // An all-day sample: the Alert popup's day-based items at 9:00.
            if let wc = w.windowController as? EventEditorWindowController {
                wc.controller.setAllDay(true)
                developmentPrint("LAYOUT: event-editor alert \(wc.content.alertSummary)")
            }
            // An edit sample: the footer's buttons (left Delete; right Cancel, Save).
            let source = main.state.calendarSource(for: main)
            if let sample = (source as? InMemoryCalendarSource)?.data.events.first(where: { $0.canDelete }) {
                let before = Set(NSApp.windows.map(ObjectIdentifier.init))
                EventEditorManager.shared.openEdit(EventEditorManager.Edit(event: sample, scope: .all))
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
                    guard let ew = NSApp.windows.first(where: {
                        $0.windowController is EventEditorWindowController && !before.contains(ObjectIdentifier($0))
                    }), let ewc = ew.windowController as? EventEditorWindowController else {
                        developmentPrint("LAYOUT: event-editor edit footer no window")
                        return
                    }
                    opened.append(ew)
                    developmentPrint("LAYOUT: event-editor edit footer \(ewc.content.footerButtons) (wireframe left Delete; right Cancel, Save)")
                }
            } else {
                developmentPrint("LAYOUT: event-editor edit footer no sample (MALACHI_CALENDAR_SAMPLES=1)")
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.6) {
                closeOpened()
                developmentPrint("LAYOUT: event-editor closed unsaved, editor windows left"
                    + " \(NSApp.windows.filter { $0.windowController is EventEditorWindowController && $0.isVisible }.count)")
            }
        }
    }

    // MARK: invitation-card

    private static func invitationCards(_ main: MainWindowController) {
        closeOpened()
        let source = main.state.calendarSource(for: main)
        let samples = Cal.sampleData(now: Date(), zone: .current).cards
        var cards = samples
        var labels = Cal.invitationStates.map { "\($0)" }
        if let base = samples.first(where: { $0.state == .unanswered }) {
            // An update that moved the room and changed the series: two
            // Previous lines.
            var update = base
            update.kind = .update
            update.sequence = 1
            update.recurrence = Recurrence(frequency: .weekly, interval: 2)
            update.previous = InvitationPrevious(
                location: "Room B", recurrence: Recurrence(frequency: .weekly, interval: 1)) // macOS-only string
            cards.append(update)
            labels.append("update-room-series")
            // Answered in another client: a response and the linked event,
            // no respondedAt.
            var elsewhere = base
            elsewhere.state = .answered
            elsewhere.response = .accepted
            elsewhere.respondedAt = nil
            elsewhere.eventId = "ev_sample_demo"
            elsewhere.calendarId = Cal.samplePersonal
            elsewhere.me?.response = .accepted
            cards.append(elsewhere)
            labels.append("answered-elsewhere")
            // The user's own request, stored: "You organise this event."
            // with Show in Calendar.
            var own = base
            own.kind = .update
            own.sequence = 1
            own.state = .processed
            own.ownEvent = true
            own.canRespond = false
            own.targets = []
            own.conflicts = []
            own.eventId = "ev_sample_demo"
            own.calendarId = Cal.samplePersonal
            cards.append(own)
            labels.append("own-event")
        }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 760, height: 760),
                         styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        w.isReleasedWhenClosed = false
        w.title = "Invitation cards (development)" // macOS-only string
        let stack = FlippedStack()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 16
        stack.translatesAutoresizingMaskIntoConstraints = false
        var views: [InvitationCardView] = []
        for (i, card) in cards.enumerated() {
            let c = InvitationController(source: source, accountId: Cal.sampleWork,
                                         messageId: MessageID(rawValue: "msg_sample_card_\(i)"), card: card)
            controllers.append(c)
            let v = InvitationCardView(margins: NSEdgeInsets(
                top: MessageHeaderView.spacing, left: MessageHeaderView.inset, bottom: 0, right: MessageHeaderView.inset))
            stack.addArrangedSubview(v)
            v.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
            v.show(c)
            views.append(v)
        }
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = true
        scroll.documentView = stack
        stack.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor).isActive = true
        stack.trailingAnchor.constraint(equalTo: scroll.contentView.trailingAnchor).isActive = true
        stack.topAnchor.constraint(equalTo: scroll.contentView.topAnchor).isActive = true
        w.contentView = scroll
        w.center()
        w.makeKeyAndOrderFront(nil)
        opened.append(w)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            w.contentView?.layoutSubtreeIfNeeded()
            developmentPrint("LAYOUT: invitation-card window \(w.windowNumber) frame \(NSStringFromRect(w.frame)) cards \(views.count)")
            for (i, v) in views.enumerated() {
                let state = i < labels.count ? labels[i] : "?"
                developmentPrint("LAYOUT: invitation-card \(i) \(state) \(r(v)) hidden \(v.isHidden)")
                // The date tile: the card's first subview of 52 pt width.
                for t in all(NSView.self, under: v) where NSStringFromClass(type(of: t)).contains("DateTile") {
                    developmentPrint("LAYOUT: invitation-card \(i) date tile \(r(t)) (wireframe: at the left)")
                }
                if let column = all(FillStackView.self, under: v).first {
                    for (j, row) in column.arrangedSubviews.enumerated() where !row.isHidden {
                        developmentPrint("LAYOUT: invitation-card \(i) row \(j) \(r(row)) '\(firstText(row).prefix(40))'")
                        // A stack of lines (Previous …, the reply note): each.
                        if let lines = row as? FillStackView {
                            for (k, l) in lines.arrangedSubviews.enumerated() where !l.isHidden {
                                developmentPrint("LAYOUT: invitation-card \(i) row \(j).\(k) \(r(l)) '\(firstText(l).prefix(60))'")
                            }
                        }
                    }
                }
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                developmentPrint("LAYOUT: shot invitation-card window \(w.windowNumber)")
                // The same as a screen rectangle (top-left origin), for
                // `screencapture -R` where -l cannot read the window.
                let h = NSScreen.screens.first?.frame.height ?? 0
                developmentPrint(String(format: "LAYOUT: shotrect invitation-card %.0f,%.0f,%.0f,%.0f",
                                        w.frame.minX, h - w.frame.maxY, w.frame.width, w.frame.height))
            }
        }
    }

    // MARK: caldav-sheet

    private static func caldavSheet(_ main: MainWindowController) {
        closeOpened()
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.integrations)
        guard let parent = prefs.window else {
            developmentPrint("LAYOUT: caldav-sheet no Settings window")
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
            main.state.showAddCalDAVCalendar(from: parent)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.7) {
                guard let sheet = parent.attachedSheet, let root = sheet.contentView else {
                    developmentPrint("LAYOUT: caldav-sheet no sheet on Settings")
                    return
                }
                let phase = (sheet.windowController as? CalDAVSheetController).map { "\($0.wizard.wizard.phase)" } ?? "?"
                developmentPrint(String(format: "LAYOUT: caldav-sheet window %ld frame %@ (wireframe 560x560) phase %@ parent %@",
                                        sheet.windowNumber, NSStringFromRect(sheet.frame), phase, NSStringFromRect(parent.frame)))
                for f in all(NSTextField.self, under: root) where f.isEditable && !f.isHiddenOrHasHiddenAncestor {
                    developmentPrint("LAYOUT: caldav-sheet field \(r(f)) placeholder '\(f.placeholderString ?? "")'")
                }
                for b in all(NSButton.self, under: root) where !b.isHiddenOrHasHiddenAncestor && !b.title.isEmpty {
                    developmentPrint("LAYOUT: caldav-sheet button '\(b.title)' \(r(b)) enabled \(b.isEnabled)")
                }
                // A sample error, to check the banner's label spans its card.
                if let c = sheet.windowController as? CalDAVSheetController {
                    c.banner.reveal(L10n.T("The server answered, but it is not a CalDAV server."))
                    root.layoutSubtreeIfNeeded()
                    developmentPrint("LAYOUT: caldav-sheet banner \(r(c.banner)) card \(c.banner.card.map(r) ?? "none") label \(r(c.banner.label))")
                }
                developmentPrint("LAYOUT: shot caldav-sheet window \(parent.windowNumber)")
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                    (sheet.windowController as? CalDAVSheetController)?.dismiss()
                    parent.close()
                    developmentPrint("LAYOUT: caldav-sheet cancelled, sheet left \(parent.attachedSheet != nil)")
                }
            }
        }
    }
}

extension DevelopmentCalendar {
    // MARK: prefs-calendar

    static func prefsCalendar(_ main: MainWindowController) {
        closeOpened()
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.calendar)
        guard let w = prefs.window else {
            developmentPrint("LAYOUT: prefs-calendar no Settings window")
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            MainActor.assumeIsolated {
            let pane = prefs.calendarPane
            let sections = pane.sectionViews
            @MainActor func frame(_ v: NSView) -> String {
                guard let content = w.contentView else { return "?" }
                return NSStringFromRect(v.convert(v.bounds, to: content))
            }
            developmentPrint("LAYOUT: prefs-calendar window \(w.windowNumber) frame \(NSStringFromRect(w.frame)) sections \(sections.count)")
            developmentPrint("LAYOUT: prefs-calendar display (\(frame(pane.displayGroup)))")
            for s in sections {
                developmentPrint("LAYOUT: prefs-calendar section '\(s.name)' (\(frame(s.view)))")
            }
            developmentPrint("LAYOUT: shot prefs-calendar window \(w.windowNumber)")
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                MainActor.assumeIsolated {
                    w.close()
                }
            }
            }
        }
    }
}

extension DevelopmentCalendar {
    // MARK: prefs-integrations

    static func prefsIntegrations(_ main: MainWindowController) {
        closeOpened()
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.integrations)
        guard let w = prefs.window else {
            developmentPrint("LAYOUT: prefs-integrations no Settings window")
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            MainActor.assumeIsolated {
            let pane = prefs.integrationsPane
            @MainActor func frame(_ v: NSView) -> String {
                guard let content = w.contentView else { return "?" }
                return NSStringFromRect(v.convert(v.bounds, to: content))
            }
            developmentPrint("LAYOUT: prefs-integrations window \(w.windowNumber) frame \(NSStringFromRect(w.frame)) connected \(pane.accounts.count)")
            for a in pane.accounts {
                let row = Integrations.rowFor(a)
                developmentPrint("LAYOUT: prefs-integrations connected '\(row.title)' kind \(row.kind.rawValue) switch \(row.offersSwitch) editor \(row.editor)")
            }
            for s in pane.serviceRows {
                developmentPrint("LAYOUT: prefs-integrations service \(s.kind.rawValue) (\(frame(s.view)))")
            }
            developmentPrint("LAYOUT: shot prefs-integrations window \(w.windowNumber)")
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                MainActor.assumeIsolated {
                    w.close()
                }
            }
            }
        }
    }
}

/// The invitation window's document, top-down.
@MainActor
private final class FlippedStack: NSStackView {
    override var isFlipped: Bool { true }
}
