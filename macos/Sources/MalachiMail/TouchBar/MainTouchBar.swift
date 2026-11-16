// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The main window's Touch Bar (docs/plans/touch-bar.md §3, §4.1): AppKit's
/// side of MalachiCore `TouchBarLayout`. `MainWindowController` hands it
/// the mode and the selection's flags in `makeTouchBar()` and on every
/// window update (`windowDidUpdate`); it builds the items and keeps their
/// state in step.
///
/// The buttons send the toolbar's selectors (`Action.*`) with a nil target
/// through the responder chain, and whether each is enabled is asked of
/// the responder that would handle it (`validateUserInterfaceItem`, as
/// AppKit asks it for a toolbar item): no rule of its own (T2, T3). What
/// the account does not offer leaves the bar (`ActionFlags.unsupported`);
/// AppKit cannot take an item out of a bar it shows, so a change of that
/// asks for a new bar (`refresh` returns true). Nothing runs on a Mac
/// without a Touch Bar: AppKit never asks for the bar there.
///
/// The Board's and the Calendar's bars hold only the mode switch until
/// stages T3 and T4.
@MainActor
final class MainTouchBar: NSObject, NSTouchBarDelegate {
    /// The window's mode switch (`setWindowMode:`), the Board's segment
    /// disabled while Show the Board is off.
    var boardEnabled: @MainActor () -> Bool = { true }
    /// Whether the responder chain allows `action` now.
    var allows: @MainActor (Selector) -> Bool = { _ in false }
    /// Whether the responder chain enables a menu item of a popover's
    /// menu now (it may retitle or hide it, as for its menu).
    var validateMenuItem: @MainActor (NSMenuItem) -> Bool = { $0.isEnabled }
    /// Move To's menu for the selection (the toolbar's).
    var moveToMenu: @MainActor () -> NSMenu? = { nil }
    /// The Assistant menu of the toolbar, and whether the Assistant is
    /// shown at all.
    var assistantMenu: @MainActor () -> NSMenu? = { nil }
    var assistantShown: @MainActor () -> Bool = { false }
    /// With In App the Assistant opens the panel instead of a menu
    /// (`Assistant.buttonOpensPanel`): true when it did.
    var assistantInstead: @MainActor () -> Bool = { false }
    /// The selection's issue status while Change Status is offered for it
    /// ("" when the row names none), nil otherwise; and its menu.
    var status: @MainActor () -> String? = { nil }
    var statusMenu: @MainActor () -> NSMenu? = { nil }
    /// The Board (plan §4.3): the selected case's detail, the style,
    /// Triage's title while offered, and the menus of Remind… and of the
    /// case's state for the selected case.
    var boardDetail: @MainActor () -> Board.Detail? = { nil }
    var boardStyle: @MainActor () -> Board.Style = { .list }
    var triageTitle: @MainActor () -> String? = { nil }
    var remindMenu: @MainActor () -> NSMenu? = { nil }
    var caseStateMenu: @MainActor () -> NSMenu? = { nil }
    /// The Calendar (plan §4.4): the selected occurrence, the view,
    /// whether the assistant panel is offered, and what the buttons do.
    var calendarSelection: @MainActor () -> Occurrence? = { nil }
    var calendarView: @MainActor () -> Cal.View = { .week }
    var calendarAssistantShown: @MainActor () -> Bool = { false }
    var calendarDo: @MainActor (CalendarCommand) -> Void = { _ in }

    /// What a Calendar button asks the window to do.
    enum CalendarCommand {
        case today
        case step(Int)
        case view(Cal.View)
        case assistant
        /// On the selected occurrence.
        case join, edit, showInMail
        case respond(EventResponse)
    }

    /// Whether a bar was made: none on a Mac without a Touch Bar.
    private(set) var made = false
    private var bar = TouchBarLayout.Bar.mail
    private var mode = Board.Mode.mail
    private var mail = TouchBarLayout.mail(.none)
    private var boardBar = TouchBarLayout.board(nil, style: .list, triage: nil)
    private var calendarBar = TouchBarLayout.calendar(nil, view: .week, assistant: false)
    /// The Calendar's view switches.
    private var viewControls: [NSSegmentedControl] = []
    /// The buttons, mode switches and count labels made for the bar
    /// shown, those of the customization palette too (it makes copies of
    /// the items it offers): all of them follow the state.
    private var buttons: [NSButtonTouchBarItem] = []
    private var modeControls: [NSSegmentedControl] = []
    private var countLabels: [NSTextField] = []
    /// The popovers made (Move To, the status, the Assistant, Remind…,
    /// the case's state), the palette's copies too, with the action that
    /// enables each (nil: always enabled).
    private var popovers: [(item: TouchBarLayout.Item, popover: MenuPopover, action: Selector?)] = []
    /// The Board's style switches.
    private var styleControls: [NSSegmentedControl] = []
    /// What `validateUserInterfaceItem` is asked about, one per action: a
    /// toolbar item, the kind of item the window's validators answer by
    /// their `allows` (a menu item they would retitle).
    private var validationItems: [Selector: NSToolbarItem] = [:]

    /// A new bar for `mode` and the selection's `flags`.
    func make(mode: Board.Mode, flags: ActionFlags) -> NSTouchBar {
        made = true
        self.mode = mode
        bar = .of(mode)
        mail = mailState(flags)
        // The board's controller is made on first use: asked only in Board.
        if bar == .board {
            boardBar = boardState()
        }
        if bar == .calendar {
            calendarBar = calendarState()
        }
        viewControls.removeAll()
        commandTargets.removeAll()
        styleControls.removeAll()
        buttons.removeAll()
        modeControls.removeAll()
        countLabels.removeAll()
        popovers.removeAll()
        let tb = NSTouchBar()
        tb.delegate = self
        tb.customizationIdentifier = NSTouchBar.CustomizationIdentifier(bar.customizationIdentifier)
        tb.defaultItemIdentifiers = TouchBarLayout.defaultSlots(bar).map(Self.identifier)
        tb.customizationAllowedItemIdentifiers = TouchBarLayout.allowedItems(bar).map { NSTouchBarItem.Identifier($0.identifier) }
            + [.fixedSpaceSmall, .fixedSpaceLarge, .flexibleSpace]
        return tb
    }

    /// Brings the bar's items in step with `mode` and `flags`; true when
    /// the bar must be made anew (another mode, another set of offered
    /// actions). Cheap when nothing changed: it runs on every window
    /// update.
    func refresh(mode: Board.Mode, flags: ActionFlags) -> Bool {
        guard made else { return false }
        if TouchBarLayout.Bar.of(mode) != bar {
            return true
        }
        self.mode = mode
        if bar == .mail {
            let m = mailState(flags)
            if m.structure != mail.structure {
                return true
            }
            if m != mail {
                mail = m
                applyLooks()
            }
        }
        if bar == .calendar {
            let c = calendarState()
            if c.structure != calendarBar.structure {
                return true
            }
            calendarBar = c
        }
        if bar == .board {
            let b = boardState()
            if b.structure != boardBar.structure {
                return true
            }
            if b != boardBar {
                boardBar = b
                applyLooks()
            }
        }
        applyState()
        return false
    }

    /// The mail's items for the selection, the Assistant and the status.
    private func mailState(_ flags: ActionFlags) -> TouchBarLayout.Mail {
        TouchBarLayout.mail(flags, assistant: assistantShown(), status: status(), maxStatus: PillLabel.statusCharacters)
    }

    /// The Calendar's items for the selection.
    private func calendarState() -> TouchBarLayout.CalendarBar {
        TouchBarLayout.calendar(calendarSelection(), view: calendarView(), assistant: calendarAssistantShown())
    }

    /// The Board's items for the selected case.
    private func boardState() -> TouchBarLayout.BoardBar {
        TouchBarLayout.board(boardDetail(), style: boardStyle(), triage: triageTitle())
    }

    // MARK: NSTouchBarDelegate

    func touchBar(_ touchBar: NSTouchBar, makeItemForIdentifier identifier: NSTouchBarItem.Identifier) -> NSTouchBarItem? {
        guard let item = TouchBarLayout.Item(identifier: identifier.rawValue) else { return nil }
        let result: NSTouchBarItem?
        switch item {
        case .mode: result = modeItem(identifier)
        case .newMessage:
            result = button(identifier, Icon.newMessage, mn(L10n.T("_New Message")), Action.newMessage)
        case .checkMail:
            result = button(identifier, Icon.refresh, L10n.T("Check for New Mail"), Action.checkForNewMail)
        case .replies: result = repliesItem(identifier)
        case .moves: result = movesItem(identifier)
        case .star: result = button(identifier, Icon.star, L10n.T("Star"), Action.toggleFlag)
        case .count: result = countItem(identifier)
        case .moveTo: result = mail.moveTo ? moveToItem(identifier) : nil
        case .status: result = mail.status != nil ? statusItem(identifier) : nil
        case .assistant: result = mail.assistant ? assistantItem(identifier) : nil
        case .junk:
            result = mail.junk
                ? button(identifier, Icon.symbol("xmark.bin", size: .toolbar), L10n.T("Mark as Junk"), Action.markAsJunk)
                : nil
        case .markRead:
            result = button(identifier, Icon.symbol("envelope.open", size: .toolbar), mn(L10n.T("Mark as _Read")),
                          Action.markAsRead)
        case .loadImages:
            result = button(identifier, Icon.symbol("photo", size: .toolbar), mn(L10n.T("Load _Images")), Action.loadImages)
        case .boardDone:
            result = textButton(identifier, Icon.symbol("checkmark", size: .toolbar), doneTitle, Action.boardDone)
        case .boardRemind:
            result = popover(.boardRemind, identifier, image: Icon.symbol("clock", size: .toolbar), title: Board.Text.remind,
                             label: Board.Text.remind, enabledBy: Action.boardRemind) { [weak self] in self?.remindMenu() }
        case .boardMoves: result = boardMovesItem(identifier)
        case .boardReply: result = button(identifier, Icon.reply, Board.Text.reply, Action.boardReply)
        case .caseState:
            // macOS-only string
            let label = "Case State"
            result = boardBar.hasCase
                ? popover(.caseState, identifier, image: nil, title: boardBar.stateTitle, label: label, enabledBy: nil) { [weak self] in
                    self?.caseStateMenu()
                }
                : nil
        case .boardStyle: result = boardBar.hasCase ? nil : styleItem(identifier)
        case .triage:
            result = boardBar.triage.map { textButton(identifier, nil, $0, Action.boardTriage) }
        case .calToday, .calStep, .calView, .calJoin, .calEdit, .calRespond, .calShowInMail, .newEvent, .calAssistant:
            result = calendarBar.shows(item) ? calendarItem(item, identifier) : nil
        case .print:
            result = button(identifier, Icon.symbol("printer", size: .toolbar), Printing.itemLabel, Action.printMail)
        }
        applyLooks()
        applyState()
        return result
    }

    // MARK: Items

    private static func identifier(_ slot: TouchBarLayout.Slot) -> NSTouchBarItem.Identifier {
        switch slot {
        case let .item(i): .init(i.identifier)
        case .smallSpace: .fixedSpaceSmall
        case .flexibleSpace: .flexibleSpace
        case .otherItems: .otherItemsProxy
        }
    }

    private func button(
        _ id: NSTouchBarItem.Identifier, _ image: NSImage, _ label: String, _ action: Selector
    ) -> NSButtonTouchBarItem {
        let b = NSButtonTouchBarItem(identifier: id, image: image, target: nil, action: action)
        b.customizationLabel = label
        buttons.append(b)
        return b
    }

    /// Mail · Board · Calendar, icons as the toolbar's switch (`ModeSwitch`).
    private func modeItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        let t = Board.texts()
        let images = Board.Mode.allCases.map { m in
            Icon.symbol(ModeSwitch.symbol(m), size: .toolbar, description: t.name(m))
        }
        let control = NSSegmentedControl(images: images, trackingMode: .selectOne, target: nil, action: Action.setWindowMode)
        let it = NSCustomTouchBarItem(identifier: id)
        it.view = control
        // macOS-only string
        it.customizationLabel = "Mail, Board, Calendar"
        modeControls.append(control)
        return it
    }

    /// Reply (Comment) · Reply All · Forward, those the account offers.
    private func repliesItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem? {
        guard !mail.replies.isEmpty else { return nil }
        let items: [NSTouchBarItem] = mail.replies.compactMap { kind in
            let sub = NSTouchBarItem.Identifier(id.rawValue + ".\(kind)")
            switch kind {
            case .reply: return button(sub, Icon.reply, L10n.T("Reply"), Action.reply)
            case .replyAll: return button(sub, Icon.replyAll, L10n.T("Reply All"), Action.replyAll)
            case .forward: return button(sub, Icon.forward, L10n.T("Forward"), Action.forward)
            default: return nil
            }
        }
        let group = NSGroupTouchBarItem(identifier: id, items: items)
        // macOS-only string
        group.customizationLabel = "Reply, Reply All, Forward"
        return group
    }

    /// Archive · Move to Trash, those the account offers.
    private func movesItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem? {
        guard !mail.moves.isEmpty else { return nil }
        let items: [NSTouchBarItem] = mail.moves.compactMap { kind in
            let sub = NSTouchBarItem.Identifier(id.rawValue + ".\(kind)")
            switch kind {
            case .archive: return button(sub, Icon.archive, L10n.T("Archive"), Action.archive)
            case .trash: return button(sub, Icon.trash, L10n.T("Move to Trash"), Action.moveToTrash)
            default: return nil
            }
        }
        let group = NSGroupTouchBarItem(identifier: id, items: items)
        // macOS-only string
        group.customizationLabel = "Archive, Move to Trash"
        return group
    }

    /// Move To: the account's folders in a scrubber (plan §4.2).
    private func moveToItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        let label = mn(L10n.T("Move To"))
        let p = MenuPopover(id, image: Icon.symbol("folder", size: .toolbar, description: label), title: "", label: label,
                            scrubber: true)
        p.menu = { [weak self] in self?.moveToMenu() }
        popovers.append((.moveTo, p, Action.moveToFolder))
        return p.item
    }

    /// A popover of buttons over `menu`, enabled as `enabledBy` is.
    private func popover(
        _ item: TouchBarLayout.Item, _ id: NSTouchBarItem.Identifier, image: NSImage?, title: String, label: String,
        enabledBy action: Selector?, menu: @escaping @MainActor () -> NSMenu?
    ) -> NSTouchBarItem {
        let p = MenuPopover(id, image: image, title: title, label: label, scrubber: false)
        p.menu = menu
        popovers.append((item, p, action))
        return p.item
    }

    /// A button with a title (Done, Triage).
    private func textButton(
        _ id: NSTouchBarItem.Identifier, _ image: NSImage?, _ title: String, _ action: Selector
    ) -> NSButtonTouchBarItem {
        let b = image.map { NSButtonTouchBarItem(identifier: id, title: title, image: $0, target: nil, action: action) }
            ?? NSButtonTouchBarItem(identifier: id, title: title, target: nil, action: action)
        b.customizationLabel = title
        buttons.append(b)
        return b
    }

    /// Archive · Delete of the Board, Delete while the account deletes.
    private func boardMovesItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        var items: [NSTouchBarItem] = [
            button(NSTouchBarItem.Identifier(id.rawValue + ".archive"), Icon.archive, Board.Text.archive, Action.boardArchive),
        ]
        if boardBar.delete {
            items.append(button(NSTouchBarItem.Identifier(id.rawValue + ".delete"), Icon.trash, Board.Text.delete,
                                Action.boardTrash))
        }
        let group = NSGroupTouchBarItem(identifier: id, items: items)
        // macOS-only string
        group.customizationLabel = "Archive, Delete"
        return group
    }

    /// List · Columns · Today (`setBoardStyle:`), while no case is selected.
    private func styleItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        let titles = Board.Style.allCases.map { Board.Text.styleTitle($0) }
        let control = NSSegmentedControl(labels: titles, trackingMode: .selectOne, target: nil, action: Action.setBoardStyle)
        let it = NSCustomTouchBarItem(identifier: id)
        it.view = control
        // macOS-only string
        it.customizationLabel = "List, Columns, Today"
        styleControls.append(control)
        return it
    }

    // MARK: Calendar

    /// A Calendar item (plan §4.4). Today, the steps, the views, the
    /// event's actions and ✦ do what the Calendar's toolbar and the
    /// event's detail popover do (`calendarDo`); New Event sends the
    /// toolbar's `newEvent:`.
    private func calendarItem(_ item: TouchBarLayout.Item, _ id: NSTouchBarItem.Identifier) -> NSTouchBarItem? {
        switch item {
        case .calToday:
            return command(id, nil, Cal.Text.today, .today)
        case .calStep:
            let labels = [Cal.Text.previous, Cal.Text.next]
            let control = NSSegmentedControl(
                images: [
                    Icon.symbol("chevron.left", size: .toolbar, description: labels[0]),
                    Icon.symbol("chevron.right", size: .toolbar, description: labels[1]),
                ], trackingMode: .momentary, target: self, action: #selector(stepChosen(_:)))
            let it = NSCustomTouchBarItem(identifier: id)
            it.view = control
            it.customizationLabel = labels.joined(separator: " / ")
            return it
        case .calView:
            let titles = Cal.View.allCases.map(Cal.Text.viewTitle)
            let control = NSSegmentedControl(labels: titles, trackingMode: .selectOne, target: self, action: #selector(viewChosen(_:)))
            control.selectedSegment = calendarBar.view.rawValue
            let it = NSCustomTouchBarItem(identifier: id)
            it.view = control
            // macOS-only string
            it.customizationLabel = "Day, Week, Month, Agenda"
            viewControls.append(control)
            return it
        case .calJoin:
            let b = command(id, Icon.symbol("video", size: .toolbar), Cal.joinVideoCall, .join)
            b.bezelColor = .controlAccentColor
            return b
        case .calEdit:
            return command(id, Icon.symbol("pencil", size: .toolbar), Cal.edit, .edit)
        case .calRespond:
            let items: [NSTouchBarItem] = Cal.responseChoices.map { r in
                let b = command(NSTouchBarItem.Identifier(id.rawValue + "." + r.rawValue), nil, Cal.responseAction(r), .respond(r))
                if r == .accepted {
                    b.bezelColor = .controlAccentColor
                }
                return b
            }
            let group = NSGroupTouchBarItem(identifier: id, items: items)
            // macOS-only string
            group.customizationLabel = "Accept, Tentative, Decline"
            return group
        case .calShowInMail:
            return command(id, Icon.symbol("envelope", size: .toolbar), Cal.showInvitation, .showInMail)
        case .newEvent:
            return button(id, Icon.symbol("plus", size: .toolbar, description: Cal.Text.newEvent), Cal.Text.newEvent,
                          CalendarAction.newEvent)
        case .calAssistant:
            let label = Assistant.texts().assistant
            return command(id, Icon.symbol("sparkles", size: .toolbar, description: label), "", .assistant, label: label)
        default:
            return nil
        }
    }

    /// A button that runs `cmd` through `calendarDo` (not validated: the
    /// window acts only on what it shows).
    private func command(
        _ id: NSTouchBarItem.Identifier, _ image: NSImage?, _ title: String, _ cmd: CalendarCommand, label: String? = nil
    ) -> NSButtonTouchBarItem {
        let target = CommandTarget { [weak self] in self?.calendarDo(cmd) }
        let b: NSButtonTouchBarItem
        if let image, title.isEmpty {
            b = NSButtonTouchBarItem(identifier: id, image: image, target: target, action: #selector(CommandTarget.run(_:)))
        } else if let image {
            b = NSButtonTouchBarItem(identifier: id, title: title, image: image, target: target, action: #selector(CommandTarget.run(_:)))
        } else {
            b = NSButtonTouchBarItem(identifier: id, title: title, target: target, action: #selector(CommandTarget.run(_:)))
        }
        b.customizationLabel = label ?? title
        commandTargets.append(target)
        return b
    }

    /// The targets of the Calendar's buttons (a button's target is weak).
    private var commandTargets: [CommandTarget] = []

    @objc private func stepChosen(_ sender: NSSegmentedControl) {
        calendarDo(.step(sender.selectedSegment == 0 ? -1 : 1))
    }

    @objc private func viewChosen(_ sender: NSSegmentedControl) {
        guard Cal.View.allCases.indices.contains(sender.selectedSegment) else { return }
        calendarDo(.view(Cal.View.allCases[sender.selectedSegment]))
    }

    /// Done, or Move Back to Board for a done case.
    private var doneTitle: String {
        boardBar.done ? Board.Text.notDone : Board.Text.done
    }

    /// The issue's status: Change Status's transitions as buttons.
    private func statusItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        let p = MenuPopover(id, image: nil, title: statusTitle, label: Jira.changeStatusLabel(), scrubber: false)
        p.menu = { [weak self] in self?.statusMenu() }
        popovers.append((.status, p, nil))
        return p.item
    }

    /// The Assistant: the toolbar menu's message actions as buttons, or
    /// the panel with In App.
    private func assistantItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        let label = Assistant.texts().assistant
        let p = MenuPopover(id, image: Icon.symbol("sparkles", size: .toolbar, description: label), title: "", label: label,
                            scrubber: false)
        p.menu = { [weak self] in self?.assistantMenu() }
        // Only the message actions and their help: the menu's choice of
        // where the Assistant runs stays in the menu.
        p.includes = { $0.action != Action.setAssistantTarget }
        p.validate = { [weak self] item in self?.validateMenuItem(item) ?? false }
        p.instead = { [weak self] in self?.assistantInstead() ?? false }
        popovers.append((.assistant, p, nil))
        return p.item
    }

    /// The status button's title: the status, else Change Status.
    private var statusTitle: String {
        guard let s = mail.status, !s.isEmpty else { return Jira.changeStatusLabel() }
        return s
    }

    /// "3 messages selected" while several rows are (`Selection.selectedText`).
    private func countItem(_ id: NSTouchBarItem.Identifier) -> NSTouchBarItem {
        let label = NSTextField(labelWithString: "")
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingTail
        let it = NSCustomTouchBarItem(identifier: id)
        it.view = label
        // macOS-only string
        it.customizationLabel = "Selected Messages"
        countLabels.append(label)
        return it
    }

    // MARK: State

    /// What the selection changes in the items' looks: Reply's label, the
    /// star, the read toggle, Trash's label and the count.
    private func applyLooks() {
        let markRead = NSTouchBarItem.Identifier(TouchBarLayout.Item.markRead.identifier)
        for b in buttons {
            switch b.action {
            case Action.reply?:
                if mail.comment {
                    b.image = Icon.symbol("text.bubble", size: .toolbar)
                    b.title = Jira.replyLabel(comment: true)
                } else {
                    b.image = Icon.reply
                    b.title = ""
                }
                b.customizationLabel = Jira.replyLabel(comment: mail.comment)
            case Action.moveToTrash?:
                b.customizationLabel = mail.outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash")
            case Action.toggleFlag?:
                b.image = mail.flagged
                    ? (Icon.starFilled.withSymbolConfiguration(.init(paletteColors: [.systemYellow])) ?? Icon.starFilled)
                    : Icon.star
            default:
                break
            }
            // The read toggle: Mark as Read while any selected row is
            // unread, else Mark as Unread (the toolbar's one item).
            if b.identifier == markRead {
                b.action = mail.marksRead ? Action.markAsRead : Action.markAsUnread
                b.image = Icon.symbol(mail.marksRead ? "envelope.open" : "envelope.badge", size: .toolbar)
                b.customizationLabel = mn(mail.marksRead ? L10n.T("Mark as _Read") : L10n.T("Mark as _Unread"))
            }
        }
        let text = mail.count.map(Selection.selectedText) ?? ""
        for label in countLabels where label.stringValue != text {
            label.stringValue = text
        }
        let title = statusTitle
        for p in popovers where p.item == .status && p.popover.button.title != title {
            p.popover.button.title = title
        }
        // The Board's: Done's title, Triage's, the case's state.
        let done = doneTitle
        let triage = boardBar.triage ?? ""
        for b in buttons {
            if b.action == Action.boardDone, b.title != done {
                b.title = done
                b.customizationLabel = done
            } else if b.action == Action.boardTriage, b.title != triage {
                b.title = triage
                b.customizationLabel = triage
            }
        }
        for p in popovers where p.item == .caseState && p.popover.button.title != boardBar.stateTitle {
            p.popover.button.title = boardBar.stateTitle
        }
    }

    /// Enabled as the responder chain allows; the switch on the window's
    /// mode.
    private func applyState() {
        for b in buttons {
            let on = b.action.map(allows) ?? false
            if b.isEnabled != on {
                b.isEnabled = on
            }
        }
        for p in popovers {
            guard let action = p.action else { continue }
            let on = allows(action)
            if p.popover.button.isEnabled != on {
                p.popover.button.isEnabled = on
            }
        }
        for control in styleControls where control.selectedSegment != boardBar.style.rawValue {
            control.selectedSegment = boardBar.style.rawValue
        }
        for control in viewControls where control.selectedSegment != calendarBar.view.rawValue {
            control.selectedSegment = calendarBar.view.rawValue
        }
        let board = boardEnabled()
        for control in modeControls {
            if control.selectedSegment != mode.rawValue {
                control.selectedSegment = mode.rawValue
            }
            if control.isEnabled(forSegment: Board.Mode.board.rawValue) != board {
                control.setEnabled(board, forSegment: Board.Mode.board.rawValue)
            }
        }
    }

    /// The item a validator is asked about for `action`.
    func validationItem(_ action: Selector) -> NSToolbarItem {
        if let it = validationItems[action] {
            return it
        }
        let it = NSToolbarItem(itemIdentifier: NSToolbarItem.Identifier("touchbar.\(NSStringFromSelector(action))"))
        it.action = action
        validationItems[action] = it
        return it
    }

    // MARK: Development aid

    /// The bar's items as `MALACHI_START`'s `touchbar` step prints them:
    /// identifier, enabled and looks.
    func describe(_ touchBar: NSTouchBar) -> [String] {
        var out = ["bar \(bar.rawValue) mode \(mode.nick)"]
        func line(_ it: NSTouchBarItem, indent: String) {
            var s = "\(indent)\(it.identifier.rawValue)"
            if let b = it as? NSButtonTouchBarItem {
                s += " enabled \(b.isEnabled ? 1 : 0) title '\(b.title)' label '\((b.customizationLabel as String?) ?? "")'"
                if let a = b.action { s += " action \(NSStringFromSelector(a))" }
            } else if let g = it as? NSGroupTouchBarItem {
                out.append(s + " group")
                for id in g.groupTouchBar.itemIdentifiers {
                    if let sub = g.groupTouchBar.item(forIdentifier: id) { line(sub, indent: indent + "  ") }
                }
                return
            } else if let p = it as? NSPopoverTouchBarItem, let b = p.collapsedRepresentation as? NSButton {
                s += " popover enabled \(b.isEnabled ? 1 : 0) title '\(b.title)'"
            } else if let c = (it as? NSCustomTouchBarItem)?.view as? NSSegmentedControl {
                s += " segment \(c.selectedSegment) board-enabled \(c.isEnabled(forSegment: Board.Mode.board.rawValue) ? 1 : 0)"
            } else if let t = (it as? NSCustomTouchBarItem)?.view as? NSTextField {
                s += " text '\(t.stringValue)'"
            }
            out.append(s)
        }
        for id in touchBar.itemIdentifiers {
            if let it = touchBar.item(forIdentifier: id) {
                line(it, indent: "")
            } else {
                out.append(id.rawValue)
            }
        }
        return out
    }
}

/// Runs a closure as a button's action (the Calendar's buttons).
@MainActor
final class CommandTarget: NSObject {
    private let action: @MainActor () -> Void

    init(_ action: @escaping @MainActor () -> Void) {
        self.action = action
    }

    @objc func run(_ sender: Any?) {
        action()
    }
}

