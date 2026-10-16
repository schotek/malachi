// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The invitation card of a message on display (docs/calendar.md §5.7): the
// reading pane and a message window (`MessageViewController`) and a
// conversation card (`ConversationCardView`) each hold an
// `InvitationCardPresenter`, which they feed with the card of message.get
// (`Message.invitation`) on every render. The presenter makes an
// `InvitationController` over the calendar source of the view's window
// (`AppState.calendarSource(for:)`, started on first use), keeps it while
// the same message shows, and routes Join Video Call through the host's
// link confirmation (`MessageActionDelegate.openLink`) and Show in
// Calendar to the main window (`MainWindowController.showInCalendar`).

/// One invitation card and its controller.
@MainActor
final class InvitationCardPresenter {
    let view: InvitationCardView
    private(set) var controller: InvitationController?
    /// The card message.get gave last: an answer's card (from
    /// invitation.respond) stays until message.get brings another.
    private var fed: InvitationCard?
    private var token: CalendarObserverToken?

    /// Join Video Call: the URL through the host's link confirmation.
    var openLink: (@MainActor (String) -> Void)?
    /// The card's height may have changed (a host that keeps what the user
    /// reads in place runs the change in its own way).
    var onChange: (@MainActor (_ render: () -> Void) -> Void)?

    init(margins: NSEdgeInsets) {
        view = InvitationCardView(margins: margins)
    }

    /// Shows the card of `m` (message.get) for message `s`, or nothing;
    /// `cardOn` is the account's switch, `state` gives the window's
    /// calendar source.
    func show(_ s: MessageSummary, _ m: Message?, cardOn: Bool, state: AppState) {
        if m == nil, let c = controller, c.messageId == s.id, c.accountId == s.accountId {
            // A render before message.get answered again (an entry let go):
            // the card stays as it was.
            return
        }
        guard let card = invitationReading(m, cardOn: cardOn), m?.summary.id == s.id,
              let source = InvitationSources.source(state: state, for: view)
        else {
            clear()
            return
        }
        if let c = controller, c.messageId == s.id, c.accountId == s.accountId, c.source === source, c.cardOn == cardOn {
            if card != fed {
                fed = card
                c.replace(card)
            }
            return
        }
        clear()
        let c = InvitationController(source: source, accountId: s.accountId, messageId: s.id, card: card, cardOn: cardOn)
        fed = card
        controller = c
        c.onChange = { [weak self] in self?.changed() }
        c.onOpenLink = { [weak self] url in self?.openLink?(url) }
        c.onShowInCalendar = { [weak self, weak c] event, day in
            guard let self else { return }
            InvitationSources.showInCalendar(
                eventID: event, recurrenceID: c?.card.recurrenceId, day: day, from: self.view.window)
        }
        // The calendars' and accounts' names come with the source's
        // snapshot: the card follows it.
        token = source.observe { [weak self] _ in self?.changed() }
        changed()
    }

    /// Hides the card and lets its controller go.
    func clear() {
        token?.cancel()
        token = nil
        controller = nil
        fed = nil
        if !view.isHidden {
            run { view.show(nil) }
        }
    }

    private func changed() {
        run { view.show(controller) }
    }

    private func run(_ render: () -> Void) {
        if let onChange {
            onChange(render)
        } else {
            render()
        }
    }
}

/// The calendar source an invitation card works over, and Show in Calendar.
@MainActor
enum InvitationSources {
    /// The daemon's sources started here (a message window's own source is
    /// not started by a calendar page).
    private static var started: [ObjectIdentifier: () -> Bool] = [:]

    /// The source of the window `view` is in (else of a main window); nil
    /// while there is none.
    static func source(state: AppState, for view: NSView) -> (any CalendarDataSource)? {
        guard let wc = view.window?.windowController ?? mainWindow() else { return nil }
        let source = state.calendarSource(for: wc)
        if let d = source as? DaemonCalendarSource {
            let key = ObjectIdentifier(d)
            started = started.filter { $0.value() }
            if started[key] == nil {
                started[key] = { [weak d] in d != nil }
                d.start()
            }
        }
        return source
    }

    /// The main window `window` is (else the frontmost one).
    static func mainWindow(for window: NSWindow? = nil) -> MainWindowController? {
        if let m = window?.windowController as? MainWindowController {
            return m
        }
        let ordered = NSApp.orderedWindows.compactMap { $0.windowController as? MainWindowController }
        return ordered.first ?? NSApp.windows.lazy.compactMap { $0.windowController as? MainWindowController }.first
    }

    /// Show in Calendar: the main window comes forward in Calendar mode at
    /// the event's day with the event's detail.
    static func showInCalendar(eventID: EventID?, recurrenceID: DateOrTime?, day: Date?, from window: NSWindow?) {
        guard let m = mainWindow(for: window) else { return }
        m.showInCalendar(eventID: eventID, recurrenceID: recurrenceID, day: day)
    }
}

extension MessageViewController {
    /// The invitation card above the header, from message.get (`m`) and
    /// the account's switch; an attached message has none.
    func renderInvitation(_ s: MessageSummary, _ m: Message?) {
        guard mode != .embedded else {
            invitation.clear()
            return
        }
        invitation.show(s, m, cardOn: showsInvitationCard(delegate?.account(s.accountId)), state: state)
    }

    /// Join Video Call through the link confirmation of the view's window.
    func openInvitationLink(_ url: String) {
        delegate?.openLink(url, links: [], from: view.window)
    }
}

/// What a conversation card needs for its invitation card: the
/// application's state, from the conversation view it sits in.
@MainActor
protocol InvitationCardStateProviding: AnyObject {
    var invitationState: AppState { get }
}

extension ConversationViewController: InvitationCardStateProviding {
    var invitationState: AppState { state }
}
