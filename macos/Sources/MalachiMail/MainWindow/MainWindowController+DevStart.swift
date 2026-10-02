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
