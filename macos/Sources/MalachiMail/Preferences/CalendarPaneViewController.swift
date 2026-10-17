// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Calendar page of the settings (the owner's decision of 2026-10-16;
/// ui/internal/calendar/preferences.go `PreferencesLayout`), one scrolling
/// pane: the group Display (the daemon's calendar.preferences through
/// `CalendarPreferencesController`), a section per account with the
/// calendar capability (`CalendarAccountGroups`), or the empty state, and
/// Add CalDAV Calendar… at the bottom. Everything works over the settings
/// window's calendar source (`AppState.startedCalendarSource`), bound when
/// the page first comes up; the sections follow its snapshot.
@MainActor
final class CalendarPaneViewController: PreferencesPaneViewController {
    private let client: RPCClient
    private let confirmRemoval: PrefsConfirmRemoval
    private let confirmTrust: WizardConfirmTrust
    private let toast: @MainActor (String) -> Void

    // Display
    let displayGroup = PreferencesGroupView(title: Cal.Prefs.display)
    let weekStart = NSPopUpButton(frame: .zero, pullsDown: false)
    let defaultDuration = NSPopUpButton(frame: .zero, pullsDown: false)
    let defaultAlert = NSPopUpButton(frame: .zero, pullsDown: false)
    let showDeclined = NSSwitch()
    let workStart = NSPopUpButton(frame: .zero, pullsDown: false)
    let workEnd = NSPopUpButton(frame: .zero, pullsDown: false)
    private var workDayButtons: [NSButton] = []
    private let workDays = NSStackView()
    private var workDaysRow: PreferenceRowView?
    private var syncingDisplay = false

    // Accounts
    private let sectionsHost = prefsColumn(spacing: 24)
    private let emptyGroup = PreferencesGroupView(title: Cal.Prefs.noCalendars, description: Cal.Prefs.noCalendarsHint)
    private var sections: [CalendarAccountGroups] = []
    private var shownAccounts: [AccountID] = []
    /// The sections' views by account name (`MALACHI_START` prefs-calendar).
    private(set) var sectionViews: [(name: String, view: NSView)] = []

    private(set) var preferences: CalendarPreferencesController?
    private var source: (any CalendarDataSource)?
    /// Kept only when the application gives no source.
    private var ownSource: DaemonCalendarSource?
    private var token: CalendarObserverToken?

    /// The window closed: everything stops, late replies are dropped.
    var closed = false {
        didSet {
            guard closed, !oldValue else { return }
            token?.cancel()
            token = nil
            preferences?.stop()
            sections.forEach { $0.stop() }
            sections = []
            (source as? DaemonCalendarSource)?.stop()
        }
    }

    init(
        client: RPCClient, confirmRemoval: @escaping PrefsConfirmRemoval, confirmTrust: @escaping WizardConfirmTrust,
        toast: @escaping @MainActor (String) -> Void
    ) {
        self.client = client
        self.confirmRemoval = confirmRemoval
        self.confirmTrust = confirmTrust
        self.toast = toast
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        super.loadView()
        buildDisplay()
        prefsAddFilling(sectionsHost, to: contentStack)
        emptyGroup.isHidden = true
        addGroup(emptyGroup)
        let add = NSButton(title: Cal.Prefs.addCalDAV, target: self, action: #selector(addCalDAVClicked(_:)))
        add.bezelStyle = .rounded
        let bar = NSStackView()
        bar.orientation = .horizontal
        bar.addView(add, in: .leading)
        prefsAddFilling(bar, to: contentStack)
    }

    /// The settings window gives the application's calendar source as soon
    /// as it exists (the samples' with MALACHI_CALENDAR_SAMPLES=1).
    func attach(window controller: NSWindowController) {
        bind(controller)
    }

    override func viewWillAppear() {
        super.viewWillAppear()
        bind(view.window?.windowController)
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        // viewWillAppear of a tab's page runs before its view is in the window.
        bind(view.window?.windowController)
        snapshotChanged()
    }

    // MARK: Binding

    private func bind(_ wc: NSWindowController?) {
        guard source == nil, !closed else { return }
        _ = view
        let s: any CalendarDataSource
        if let state = AppState.current, let wc {
            s = state.startedCalendarSource(for: wc)
        } else if wc == nil {
            return
        } else {
            let own = DaemonCalendarSource(client: client)
            own.start()
            ownSource = own
            s = own
        }
        source = s
        let p = CalendarPreferencesController(source: s)
        preferences = p
        p.onChange = { [weak self] in self?.renderDisplay() }
        p.start()
        workDayButtons = p.workingDayChoices.enumerated().map { i, d in
            let b = NSButton(title: d.title, target: self, action: #selector(workDayToggled(_:)))
            b.setButtonType(.pushOnPushOff)
            b.bezelStyle = .recessed
            b.controlSize = .small
            b.tag = i
            return b
        }
        for b in workDayButtons {
            workDays.addArrangedSubview(b)
        }
        token = s.observe { [weak self] change in
            guard change == .snapshot else { return }
            self?.snapshotChanged()
        }
        snapshotChanged()
    }

    private func snapshotChanged() {
        guard !closed else { return }
        renderDisplay()
        renderSections()
    }

    // MARK: Accounts

    /// A section per account with the calendar capability, in the
    /// accounts' order; rebuilt only when that list changes.
    private func renderSections() {
        guard let source else { return }
        let snap = source.snapshot
        let ids = snap.accounts.filter { $0.capabilities?.contains(.calendar) ?? false }.map(\.id)
        emptyGroup.isHidden = !(ids.isEmpty && snap.phase != .loading)
        guard ids != shownAccounts else { return }
        shownAccounts = ids
        sections.forEach { $0.stop() }
        sections = []
        sectionViews = []
        for v in sectionsHost.arrangedSubviews {
            sectionsHost.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        for id in ids {
            guard let account = snap.account(id) else { continue }
            let g = CalendarAccountGroups(source: source, account: account) { [weak self] in self?.view.window }
            g.onSignIn = { [weak self] id in self?.signInForCalendar(id) }
            g.onRemove = { [weak self] a in self?.removeAccount(a) }
            let section = prefsColumn(spacing: 12)
            for v in g.views {
                prefsAddFilling(v, to: section)
            }
            prefsAddFilling(section, to: sectionsHost)
            sectionViews.append((accountRowTitle(account), section))
            sections.append(g)
            g.start()
        }
    }

    @objc private func addCalDAVClicked(_ sender: Any?) {
        guard let window = view.window, window.attachedSheet == nil, let source else { return }
        CalDAVSheetController.present(from: window, source: source, confirmTrust: confirmTrust) { [weak self] _ in
            guard let self, !self.closed else { return }
            self.source?.refresh()
        }
    }

    /// Remove Account… of a CalDAV account: the Accounts page's question,
    /// then account.remove; the section goes with the next snapshot.
    private func removeAccount(_ a: Account) {
        let prompt = PrefsConfirmation(
            heading: L10n.T("Remove this account?"),
            // TRANSLATORS: %s is the account's e-mail address.
            body: L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", accountRowTitle(a)),
            confirmLabel: wizardLabel("_Remove"),
            extraLabel: wizardLabel("Also delete _drafts and downloaded data"),
            extraDefault: true
        )
        let window = view.window
        let confirmRemoval = confirmRemoval
        let client = client
        Task { [weak self] in
            let answer = await confirmRemoval(window, prompt)
            guard answer.confirmed, let self, !self.closed else { return }
            do {
                _ = try await client.call(
                    API.AccountRemove.self, AccountRemoveParams(accountId: a.id, deleteLocalData: answer.deleteLocalData),
                    timeout: RPCTimeouts.default)
            } catch {
                guard !self.closed else { return }
                self.toast(rpcErrorText(L10n.T("Removing the account"), error))
                return
            }
            guard !self.closed else { return }
            self.source?.refresh()
        }
    }

    /// The consent's Sign In…: the browser sign-in of the account again,
    /// asking for the calendar scopes too (account.oauthStart `calendar:
    /// true`); the daemon finishes it by itself and the account's state
    /// follows notify.calendarState. Only an https page is opened.
    private func signInForCalendar(_ id: AccountID) {
        let client = client
        Task { [weak self] in
            var url = ""
            var failure: (any Error)?
            do {
                url = try await client.call(
                    API.AccountOAuthStart.self, AccountOAuthStartParams(accountId: id, browserPage: browserPage(), calendar: true),
                    timeout: RPCTimeouts.oauthStart
                ).authUrl
            } catch {
                failure = error
            }
            guard let self, !self.closed else { return }
            if let failure {
                self.toast(rpcErrorText(L10n.T("Starting the sign-in"), failure))
                return
            }
            guard isBrowserURL(url), let u = URL(string: url) else {
                self.toast(refusedBrowserURLText())
                return
            }
            NSWorkspace.shared.open(u)
        }
    }

    // MARK: Display (calendar.preferences)

    private func buildDisplay() {
        let timeRange = NSStackView(views: [workStart, NSTextField(labelWithString: "\u{2013}"), workEnd])
        timeRange.orientation = .horizontal
        timeRange.alignment = .centerY
        timeRange.spacing = 6
        workDays.orientation = .horizontal
        workDays.alignment = .centerY
        workDays.spacing = 4
        let daysRow = PreferenceRowView(title: Cal.Prefs.workingHoursSetting, prefix: workDays)
        workDaysRow = daysRow
        displayGroup.setRows([
            PreferenceRowView(title: Cal.Prefs.weekStartSetting, trailing: weekStart),
            PreferenceRowView(title: Cal.Prefs.durationSetting, trailing: defaultDuration),
            PreferenceRowView(title: Cal.Prefs.alertSetting, trailing: defaultAlert),
            PreferenceRowView(title: Cal.Prefs.declinedSetting, trailing: showDeclined),
            PreferenceRowView(title: Cal.Prefs.workingHoursSetting, subtitle: Cal.Prefs.workingHoursSubtitle, trailing: timeRange),
            daysRow,
        ])
        for popup in [weekStart, defaultDuration, defaultAlert, workStart, workEnd] {
            popup.target = self
            popup.action = #selector(displayChanged(_:))
        }
        showDeclined.target = self
        showDeclined.action = #selector(displayChanged(_:))
        displayGroup.isEnabled = false
        addGroup(displayGroup)
    }

    private func renderDisplay() {
        guard let c = preferences, !closed else { return }
        syncingDisplay = true
        defer { syncingDisplay = false }
        // Enabled once the source has data (the samples' at once).
        let phase = c.source.snapshot.phase
        displayGroup.isEnabled = phase != .loading && phase != .unsupported
        displayGroup.descriptionText = c.error
        let p = c.preferences
        Self.fill(weekStart, c.weekStartChoices.map(\.title), at: c.weekStartChoices.firstIndex { $0.value == p.weekStart })
        Self.fill(defaultDuration, c.durationChoices.map(\.title), at: c.durationChoices.firstIndex { $0.value == p.defaultDurationMinutes })
        Self.fill(defaultAlert, c.alertChoices.map(\.title), at: c.alertChoices.firstIndex { $0.value == p.defaultAlertMinutes })
        showDeclined.state = p.showDeclined ? .on : .off
        let starts = c.workingTimeChoices(including: p.workingHours.start)
        let ends = c.workingTimeChoices(including: p.workingHours.end)
        Self.fill(workStart, starts.map(\.title), at: starts.firstIndex { $0.value == p.workingHours.start })
        Self.fill(workEnd, ends.map(\.title), at: ends.firstIndex { $0.value == p.workingHours.end })
        let days = c.workingDayChoices
        for (i, b) in workDayButtons.enumerated() where days.indices.contains(i) {
            b.state = c.isWorkingDay(days[i].value) ? .on : .off
        }
        workDaysRow?.title = c.workingHoursText
    }

    /// A pop-up's titles (replaced only when they changed) and selection.
    private static func fill(_ popup: NSPopUpButton, _ titles: [String], at index: Int?) {
        if popup.itemTitles != titles {
            popup.removeAllItems()
            popup.addItems(withTitles: titles)
        }
        popup.selectItem(at: index ?? -1)
    }

    @objc private func displayChanged(_ sender: Any?) {
        guard !syncingDisplay, let c = preferences else { return }
        let p = c.preferences
        func pick<T>(_ popup: NSPopUpButton, _ v: [CalendarPreferencesController.Choice<T>], _ set: (T) -> Void) {
            if v.indices.contains(popup.indexOfSelectedItem) {
                set(v[popup.indexOfSelectedItem].value)
            }
        }
        switch sender as AnyObject {
        case let x where x === weekStart: pick(weekStart, c.weekStartChoices, c.setWeekStart)
        case let x where x === defaultDuration: pick(defaultDuration, c.durationChoices, c.setDefaultDuration)
        case let x where x === defaultAlert: pick(defaultAlert, c.alertChoices, c.setDefaultAlert)
        case let x where x === showDeclined: c.setShowDeclined(showDeclined.state == .on)
        case let x where x === workStart: pick(workStart, c.workingTimeChoices(including: p.workingHours.start), c.setWorkingStart)
        case let x where x === workEnd: pick(workEnd, c.workingTimeChoices(including: p.workingHours.end), c.setWorkingEnd)
        default: break
        }
        // A refused choice (an end before the start) shows the stored one.
        renderDisplay()
    }

    @objc private func workDayToggled(_ sender: NSButton) {
        guard !syncingDisplay, let c = preferences else { return }
        let days = c.workingDayChoices
        guard days.indices.contains(sender.tag) else { return }
        c.setWorkingDay(days[sender.tag].value, sender.state == .on)
        // The last working day stays on.
        renderDisplay()
    }
}
