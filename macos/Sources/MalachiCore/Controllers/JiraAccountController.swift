// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The settings of a Jira account (kind `jira`) with the widgets replaced
/// by callbacks: the page Settings ▸ Accounts opens for such an account.
/// Its texts and rules are ui/internal/jira/settings.go (`Jira` here);
/// macOS is the first client, so there is no GTK flow to mirror yet
/// (Swift-first: mirror in ui/internal/jira when GTK gets Jira accounts).
///
/// The page opens with account.listSpaces for the stored account, without
/// a token (the daemon takes the stored one): the spaces and the statuses
/// to choose from and the user the account signs in as. When that fails
/// the banner says why and the page edits what is stored. Everything the
/// page changes is a copy (`form`) until Save, which is account.update
/// with the form applied to the stored configuration and empty
/// credentials, so the token stays as it is; a form that changes nothing
/// the daemon acts on closes without a call. The token itself is replaced
/// by the account assistant in its edit mode (`JiraWizardController`),
/// which the UI opens on `onReplaceToken`.
///
/// Every RPC reply is dropped once the page closed (`closed`) or another
/// call started since (`op`). Nothing typed here is logged.
///
/// Every callback runs on the main actor. Wire them, then call `start()`.
@MainActor
public final class JiraAccountController {
    // MARK: Outputs

    /// What the page shows changed (the rows, a selection, a list, a
    /// problem): the UI reads the controller again.
    public var onChange: (@MainActor () -> Void)?
    /// A call started, with its progress text, or finished (nil). While
    /// the spaces load the page stays usable; while it saves it waits
    /// (`saving`).
    public var onBusy: (@MainActor (String?) -> Void)?
    /// The page's banner; nil hides it.
    public var onBanner: (@MainActor (String?) -> Void)?
    /// The account was stored; the UI closes the page.
    public var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?
    /// Save had nothing to store; the UI closes the page.
    public var onClose: (@MainActor () -> Void)?
    /// "Replace Token…": the UI opens the account assistant in its edit
    /// mode for the account and calls `tokenReplaced()` once it stored the
    /// new token.
    public var onReplaceToken: (@MainActor (Account) -> Void)?

    // MARK: State

    public let client: RPCClient
    /// The account as it is stored.
    public let account: Account
    /// The fixed texts (`Jira.settingsTexts`).
    public let texts = Jira.settingsTexts()

    /// The edited copy.
    public private(set) var form: Jira.SettingsForm
    /// What account.listSpaces answered; nil before it did and when it
    /// failed.
    public private(set) var listing: AccountListSpacesResult?
    /// The banner shown; nil for none: why Save failed, else why the
    /// spaces could not be listed.
    public var banner: String? { saveProblem ?? loadProblem }
    /// The progress text of the call under way; nil when none runs.
    public private(set) var progress: String?
    /// account.update is under way: the page waits.
    public private(set) var saving = false
    public private(set) var closed = false
    /// Bumped per RPC so that stale replies bail out.
    public private(set) var op = 0

    /// Why the entry typed last was not added, per list.
    private var problems: [Jira.ListKind: String] = [:]
    /// Why account.listSpaces failed; stays until it is asked again.
    private var loadProblem: String?
    /// Why Save did nothing; goes with the next change of the form.
    private var saveProblem: String?
    /// The banner the UI was told last.
    private var shownBanner: String?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "jiraaccount")

    public init(client: RPCClient, account: Account) {
        self.client = client
        self.account = account
        form = Jira.SettingsForm(account.config)
    }

    // MARK: Presentation

    /// The window's title.
    public var title: String { texts.title }

    /// The label of the button that saves.
    public var saveLabel: String { withoutMnemonic(L10n.T("_Save")) }

    public var deployment: JiraDeployment { account.config.jira?.deployment ?? .cloud }

    /// The site's rows; the user is the listing's once it answered.
    public var site: Jira.SiteInfo { Jira.settingsSite(account.config, user: listing?.user) }

    private var storedSpaces: [SpaceRef] { account.config.jira?.spaces ?? [] }

    /// The spaces to choose from, and the ids of the chosen ones.
    public var spaceRows: [Jira.SpaceRow] {
        Jira.settingsSpaceRows(stored: storedSpaces, listed: listing?.spaces ?? [])
    }

    public var selectedSpaces: Set<String> { Set(form.spaces.map(\.id)) }

    /// Why the chosen spaces cannot be saved; "" when they can.
    public var spacesProblem: String { Jira.spacesProblem(form.spaces.count) }

    /// The labels of the offline window's choices and the one shown.
    public var offlineLabels: [String] { Jira.offlineChoiceLabels() }
    public var offlineIndex: Int { Jira.indexOfOfflineDays(form.offlineDays) }

    /// Whether the view `v` is switched on.
    public func folderShown(_ v: VirtualFolder) -> Bool {
        Jira.folderShown(form.disabledFolders, v)
    }

    /// The picker of the closed statuses.
    public var statusGroups: [Jira.StatusGroup] {
        Jira.statusGroups(listing?.statuses ?? [], closed: form.closedStatuses)
    }

    /// Why the chosen statuses cannot be saved; "" when they can.
    public var statusesProblem: String { Jira.statusesProblem(form.closedStatuses) }

    /// The labels of the notification modes, the one shown, the text
    /// under it and whether the senders matter.
    public var notificationLabels: [String] { Jira.notificationModeLabels() }
    public var notificationIndex: Int { Jira.indexOfNotificationMode(form.notificationMail) }
    public var notificationHint: String { Jira.notificationHint(form.notificationMail) }
    public var sendersEditable: Bool { Jira.sendersEditable(form.notificationMail) }

    /// What an empty list of senders stands for.
    public var sendersPlaceholder: String { Jira.defaultSenders(account.config) }

    /// The entries of a list.
    public func entries(_ kind: Jira.ListKind) -> [String] {
        switch kind {
        case .botNames: return form.botNames
        case .metadataFilters: return form.metadataFilters
        case .authorPrefixes: return form.authorPrefixes
        case .senders: return form.notificationSenders
        }
    }

    /// Why the entry typed last was not added to the list; "" for none.
    public func problem(_ kind: Jira.ListKind) -> String {
        problems[kind] ?? ""
    }

    /// The entries offered for a list with one click.
    public func suggestions(_ kind: Jira.ListKind) -> [Jira.Suggestion] {
        Jira.suggestions(kind, have: entries(kind))
    }

    /// A call runs.
    public var busy: Bool { progress != nil }

    /// The form differs from the stored account in what the daemon acts
    /// on.
    public var isChanged: Bool { Jira.changed(account.config, form.apply(account.config)) }

    /// Save is offered: nothing is being saved and the form can be.
    public var canSave: Bool { !saving && form.settingsProblem().isEmpty }

    // MARK: Lifecycle

    /// Delivers the initial state and asks for the spaces and statuses.
    /// Call once, after wiring the callbacks.
    public func start() {
        onChange?()
        load()
    }

    /// The page went away: every late reply is dropped from now on.
    public func close() {
        closed = true
        progress = nil
        saving = false
    }

    // MARK: Inputs from the UI

    /// The account's name as typed.
    public func setName(_ name: String) {
        guard name != form.name else { return }
        form.name = name
        edited()
    }

    /// A space's check box.
    public func setSpace(_ id: String, selected on: Bool) {
        guard !saving, spaceRows.contains(where: { $0.id == id }), on != selectedSpaces.contains(id) else { return }
        form.spaces = Jira.setSpaceSelected(form.spaces, stored: storedSpaces, listed: listing?.spaces ?? [], id: id, on: on)
        edited()
    }

    /// The offline window's choice (an index of `Jira.offlineChoices`).
    public func setOfflineIndex(_ i: Int) {
        guard !saving, Jira.offlineChoices.indices.contains(i), Jira.offlineChoices[i] != form.offlineDays else { return }
        form.offlineDays = Jira.offlineChoices[i]
        edited()
    }

    /// "Only Issues Involving Me".
    public func setOnlyMine(_ on: Bool) {
        guard !saving, on != form.onlyMine else { return }
        form.onlyMine = on
        edited()
    }

    /// "Show Status and Assignee Changes".
    public func setShowEvents(_ on: Bool) {
        guard !saving, on != form.showEvents else { return }
        form.showEvents = on
        edited()
    }

    /// The switch of a view.
    public func setFolder(_ v: VirtualFolder, shown: Bool) {
        guard !saving, Jira.virtualFolders.contains(v), shown != folderShown(v) else { return }
        form.disabledFolders = Jira.setFolderShown(form.disabledFolders, v, shown: shown)
        edited()
    }

    /// A check box of the picker of the closed statuses.
    public func setStatus(_ choice: Jira.StatusChoice, selected on: Bool) {
        guard !saving else { return }
        form.closedStatuses = Jira.setStatusSelected(
            listing?.statuses ?? [], closed: form.closedStatuses, choice: choice, on: on)
        edited()
    }

    /// The notification mode's choice (an index of
    /// `Jira.notificationModes`).
    public func setNotificationIndex(_ i: Int) {
        guard !saving, Jira.notificationModes.indices.contains(i), Jira.notificationModes[i] != form.notificationMail else {
            return
        }
        form.notificationMail = Jira.notificationModes[i]
        edited()
    }

    /// Adds what the user typed to a list (`Jira.checkEntry`). True when
    /// the field may be emptied: the entry was added, or there was nothing
    /// to add. Otherwise `problem(kind)` says why not.
    @discardableResult
    public func addEntry(_ kind: Jira.ListKind, _ text: String) -> Bool {
        guard !saving else { return false }
        let checked = Jira.checkEntry(kind, text, have: entries(kind))
        if checked.entry.isEmpty {
            problems[kind] = checked.problem.isEmpty ? nil : checked.problem
            onChange?()
            return checked.problem.isEmpty
        }
        problems[kind] = nil
        setEntries(kind, entries(kind) + [checked.entry])
        edited()
        return true
    }

    /// Adds a suggested entry (`suggestions(kind)`).
    public func addSuggestion(_ kind: Jira.ListKind, _ value: String) {
        guard suggestions(kind).contains(where: { $0.value == value }) else { return }
        addEntry(kind, value)
    }

    /// Removes the entry at `index` of a list.
    public func removeEntry(_ kind: Jira.ListKind, at index: Int) {
        var list = entries(kind)
        guard !saving, list.indices.contains(index) else { return }
        list.remove(at: index)
        problems[kind] = nil
        setEntries(kind, list)
        edited()
    }

    /// The field of a list changed: its problem belongs to what was typed
    /// before.
    public func entryTyped(_ kind: Jira.ListKind) {
        guard problems[kind] != nil else { return }
        problems[kind] = nil
        onChange?()
    }

    /// "Replace Token…": the UI opens the account assistant.
    public func replaceToken() {
        guard !closed, !saving else { return }
        onReplaceToken?(account)
    }

    /// The account assistant stored a new token: the spaces and statuses
    /// are asked for again with it.
    public func tokenReplaced() {
        guard !closed, !saving else { return }
        load()
    }

    /// Save: account.update with the form, or nothing when the form
    /// changes nothing.
    public func save() {
        guard !closed, !saving else { return }
        let problem = form.settingsProblem()
        guard problem.isEmpty else {
            saveProblem = problem
            showBanner()
            return
        }
        let cfg = form.apply(account.config)
        guard Jira.changed(account.config, cfg) else {
            onClose?()
            return
        }
        saveProblem = nil
        showBanner()
        saving = true
        setBusy(texts.saving)
        onChange?()
        op += 1
        let op = self.op
        let client = client
        let id = account.id
        // Empty credentials: the daemon keeps the stored token.
        let params = AccountUpdateParams(accountId: id, config: cfg, credentials: Credentials())
        Task { [weak self] in
            var failure: (any Error)?
            do {
                _ = try await client.call(API.AccountUpdate.self, params, timeout: RPCTimeouts.save)
            } catch {
                failure = error
            }
            guard let self, !self.closed, op == self.op else { return }
            self.saving = false
            self.setBusy(nil)
            if let failure {
                self.saveProblem = self.failure(.save, failure)
                self.showBanner()
                self.onChange?()
                return
            }
            self.log.info("jira account saved: \(id.rawValue, privacy: .public)")
            self.onDone?(id, cfg)
        }
    }

    // MARK: Internals

    private func setEntries(_ kind: Jira.ListKind, _ list: [String]) {
        switch kind {
        case .botNames: form.botNames = list
        case .metadataFilters: form.metadataFilters = list
        case .authorPrefixes: form.authorPrefixes = list
        case .senders: form.notificationSenders = list
        }
    }

    /// The form changed: what the last Save said is about another form.
    private func edited() {
        saveProblem = nil
        showBanner()
        onChange?()
    }

    private func setBusy(_ text: String?) {
        guard progress != text else { return }
        progress = text
        onBusy?(text)
    }

    /// Tells the UI the banner when it changed.
    private func showBanner() {
        let text = banner
        guard shownBanner != text else { return }
        shownBanner = text
        onBanner?(text)
    }

    /// The banner of a failed call: the step's own sentence
    /// (`Jira.failureOf`: a refused or missing token, an account that
    /// exists already), else the client's for the error.
    private func failure(_ step: Jira.Step, _ error: any Error) -> String {
        let cls = Jira.classify(error)
        log.info("jira account step \(step.rawValue, privacy: .public) failed: class \(cls.rawValue, privacy: .public)")
        let f = Jira.failureOf(step, cls, deployment, editing: true)
        return f.banner.isEmpty ? rpcErrorText(f.what, error) : f.banner
    }

    /// account.listSpaces for the stored account with its stored token.
    private func load() {
        let params = AccountListSpacesParams(
            accountId: account.id, config: account.config, credentials: Credentials(), counts: false)
        loadProblem = nil
        showBanner()
        setBusy(texts.loading)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountListSpacesResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountListSpaces.self, params, timeout: RPCTimeouts.listSpaces))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.setBusy(nil)
            switch outcome {
            case .success(let res):
                self.log.info("jira account listed: \(res.spaces.count, privacy: .public) spaces, \(res.statuses.count, privacy: .public) statuses")
                self.listing = res
            case .failure(let error):
                // The page edits what is stored.
                self.loadProblem = self.failure(.spaces, error)
                self.showBanner()
            }
            self.onChange?()
        }
    }
}
