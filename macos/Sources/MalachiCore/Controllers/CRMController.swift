// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// What the application knows of eWay-CRM once (docs/eway.md §2.7): the
/// accounts (the first enabled eway account is the one used, D11), the
/// messages a save or removal runs for (`CRM.InFlight`), and for a few
/// seconds the record a finished call reported, so that the button turns
/// before the daemon's notify.messagesChanged has refreshed the list and
/// the message (the daemon's record is the truth; this only bridges the
/// gap). Every window's `CRMController` shares it, so a save started in a
/// message window shows as Saving in the main window too.
@MainActor
public final class CRMState {
    /// The accounts of account.list (the mailbox model's).
    public var accounts: @MainActor () -> [Account] = { [] }
    /// The role of the folder a message lies in.
    public var folderRole: @MainActor (MessageSummary) -> FolderRole = { _ in .none }
    /// The full message (`message.get`) when the cache holds it.
    public var loaded: @MainActor (MessageID) -> Message? = { _ in nil }
    /// The clock of the overrides (tests).
    public var now: @MainActor () -> Date = { Date() }
    /// How long a call's own answer outranks the list and the cache.
    public static let overrideSeconds: TimeInterval = 10

    public private(set) var inFlight = CRM.InFlight()
    private var overrides: [CRM.Key: (record: CrmRecord?, until: Date)] = [:]
    private var observers: [Int: @MainActor () -> Void] = [:]
    private var nextObserver = 0

    public init() {}

    /// A registration of `observe`; `cancel` ends it.
    public final class Token {
        private var cancelHandler: (@MainActor () -> Void)?

        init(_ cancel: @escaping @MainActor () -> Void) {
            cancelHandler = cancel
        }

        @MainActor public func cancel() {
            cancelHandler?()
            cancelHandler = nil
        }
    }

    /// Calls `f` whenever a call starts or ends.
    public func observe(_ f: @escaping @MainActor () -> Void) -> Token {
        nextObserver += 1
        let id = nextObserver
        observers[id] = f
        return Token { [weak self] in self?.observers.removeValue(forKey: id) }
    }

    /// Tells the observers (the windows revalidate their buttons).
    public func changed() {
        for f in observers.values {
            f()
        }
    }

    /// The eWay-CRM account the client saves to (`CRM.available`).
    public var account: Account? { CRM.available(accounts()) }

    /// The message's record in the account used: a call's own answer for
    /// a few seconds, else the loaded message's.
    public func record(for s: MessageSummary) -> CrmRecord? {
        let k = CRM.Key(s.accountId, s.id)
        if let o = overrides[k], o.until > now() {
            return o.record
        }
        return CRM.recordOf(loaded(s.id), crm: account?.id)
    }

    /// `CRM.isSaved` with a call's own answer first.
    public func isSaved(_ s: MessageSummary) -> Bool {
        let k = CRM.Key(s.accountId, s.id)
        if let o = overrides[k], o.until > now() {
            return o.record != nil
        }
        return CRM.isSaved(s, loaded(s.id), crm: account?.id)
    }

    /// The rule's row of a message; nil when its account is unknown.
    public func row(_ s: MessageSummary, conversation: Bool = false) -> CRM.Row? {
        guard let a = accounts().first(where: { $0.id == s.accountId }) else { return nil }
        return CRM.Row(account: a, role: folderRole(s), saved: isSaved(s), conversation: conversation)
    }

    /// `CRM.offer(for:rows:)` of the messages (the selected rows' shown
    /// messages); nothing when one of them has an unknown account.
    public func offer(_ messages: [MessageSummary], conversations: Set<MessageID> = []) -> CRM.Offer {
        let rows = messages.compactMap { row($0, conversation: conversations.contains($0.id)) }
        guard rows.count == messages.count else { return CRM.Offer() }
        return CRM.offer(for: accounts(), rows: rows)
    }

    /// The button of the messages: one message by `CRM.buttonState`; for
    /// several the Save button with their count ("Save 3 Messages to
    /// eWay-CRM…", as the context menu has it), Saving while any of them is
    /// busy.
    public func button(_ messages: [MessageSummary], conversations: Set<MessageID> = []) -> CRM.Button {
        let busy = messages.contains { inFlight.has($0.accountId, $0.id) }
        if messages.count == 1, let r = row(messages[0], conversation: conversations.contains(messages[0].id)) {
            return CRM.buttonFor(CRM.buttonState(accounts(), r, busy: busy))
        }
        let o = offer(messages, conversations: conversations)
        guard o.shown else { return CRM.buttonFor(.hidden) }
        var b = CRM.buttonFor(busy ? .saving : .notSaved)
        b.label = CRM.saveMenuLabel(o.count)
        if !busy {
            b.tooltip = b.label
        }
        b.enabled = o.enabled && !busy
        return b
    }

    func begin(_ account: AccountID, _ ids: [MessageID]) {
        inFlight.begin(account, ids)
        changed()
    }

    func end(_ account: AccountID, _ ids: [MessageID]) {
        inFlight.end(account, ids)
        changed()
    }

    /// Remembers what a call reported for the message (nil: not saved).
    func setOverride(_ account: AccountID, _ id: MessageID, _ record: CrmRecord?) {
        overrides[CRM.Key(account, id)] = (record, now().addingTimeInterval(Self.overrideSeconds))
        let t = now()
        overrides = overrides.filter { $0.value.until > t }
    }
}

/// One open Save to eWay-CRM dialog (docs/eway.md §2.2): crm.lookup while
/// it shows a spinner, then the `CRM.Dialog` over its answer with the
/// search field (crm.search after a pause in typing, from 2 bytes), Save
/// and Cancel. Save hands the choice to the controller, which runs
/// crm.save in the background, and closes the dialog.
@MainActor
public final class CRMDialogSession {
    public enum Phase: Equatable {
        /// crm.lookup runs.
        case loading
        /// `dialog` is set.
        case ready
        /// crm.lookup failed; the sentence to show (only Cancel is left).
        case failed(String)
    }

    /// How long typing pauses before the search field asks the CRM.
    public static let searchDelay: Duration = .milliseconds(300)

    public private(set) var phase: Phase = .loading
    public private(set) var dialog: CRM.Dialog?
    /// The messages the dialog saves, of `messageAccount`.
    public let messageAccount: AccountID
    public let messageIDs: [MessageID]
    /// Plain text: the subject and sender of the first message, with the
    /// number of messages when there are several ("subject · sender · 3
    /// messages").
    public let summary: String
    /// A crm.search runs.
    public private(set) var searching = false
    /// Why the last search failed; nil when it did not.
    public private(set) var searchProblem: String?
    /// The session ended (saved or cancelled): the dialog closes.
    public private(set) var finished = false

    /// Anything above changed: the view draws the dialog again.
    public var onChange: (@MainActor () -> Void)?
    /// The session ended; the sheet goes.
    public var onClose: (@MainActor () -> Void)?

    weak var controller: CRMController?
    private var searchTask: Task<Void, Never>?
    private var searchGeneration = 0

    init(controller: CRMController?, messageAccount: AccountID, messageIDs: [MessageID], summary: String) {
        self.controller = controller
        self.messageAccount = messageAccount
        self.messageIDs = messageIDs
        self.summary = summary
    }

    /// The fixed texts for the dialog in its current state.
    public var texts: CRM.DialogStrings { CRM.dialogTexts(editing: dialog?.editing ?? false) }

    func loaded(_ outcome: Result<CrmLookupResult, any Error>) {
        guard !finished else { return }
        switch outcome {
        case .success(let res):
            dialog = CRM.Dialog(res)
            phase = .ready
        case .failure(let error):
            let t = CRM.errorText(error)
            phase = .failed(t.isEmpty ? rpcErrorText(CRM.actionText(.lookup), error) : t)
        }
        onChange?()
    }

    /// An item's check box; false when refused (the view unchecks it).
    @discardableResult
    public func toggle(_ ref: CrmItemRef, _ on: Bool) -> Bool {
        let changed = dialog?.toggle(ref, on) ?? false
        onChange?()
        return changed
    }

    /// The Superior Item marker moved to `ref`.
    @discardableResult
    public func setSuperior(_ ref: CrmItemRef) -> Bool {
        let changed = dialog?.setSuperior(ref) ?? false
        onChange?()
        return changed
    }

    /// The Private check box.
    public func setPrivate(_ on: Bool) {
        dialog?.setPrivate(on)
        onChange?()
    }

    /// The search field as typed: crm.search once typing pauses
    /// (`searchDelay`), nothing below 2 bytes; the results join the groups
    /// unchecked (`CRM.Dialog.merge`).
    public func search(_ text: String) {
        searchTask?.cancel()
        searchGeneration += 1
        let generation = searchGeneration
        searchProblem = nil
        guard let dialog, let params = dialog.searchParams(text), let controller else {
            if searching {
                searching = false
                onChange?()
            }
            return
        }
        let client = controller.client
        searchTask = Task { [weak self] in
            try? await Task.sleep(for: Self.searchDelay)
            guard !Task.isCancelled, let self, generation == self.searchGeneration else { return }
            self.searching = true
            self.onChange?()
            let outcome: Result<CrmSearchResult, any Error>
            do {
                outcome = .success(try await client.call(API.CrmSearch.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard generation == self.searchGeneration, !self.finished else { return }
            self.searching = false
            switch outcome {
            case .success(let res):
                self.dialog?.merge(res)
            case .failure(let error):
                let t = CRM.errorText(error)
                self.searchProblem = t.isEmpty ? rpcErrorText(CRM.actionText(.search), error) : t
            }
            self.onChange?()
        }
    }

    /// Save: the dialog closes and the controller runs crm.save.
    public func save() {
        guard !finished, phase == .ready, let dialog, dialog.canSave,
              let params = dialog.saveParams(messageAccount: messageAccount, messageIDs: messageIDs) else { return }
        finish()
        controller?.runSave(params)
    }

    /// The messages and items Save would send now (nil when it cannot).
    public var pendingSave: CrmSaveParams? {
        dialog?.saveParams(messageAccount: messageAccount, messageIDs: messageIDs)
    }

    /// Cancel (or Escape): nothing is saved.
    public func cancel() {
        guard !finished else { return }
        finish()
    }

    private func finish() {
        finished = true
        searchTask?.cancel()
        searchTask = nil
        searchGeneration += 1
        onClose?()
    }
}

/// The CRM actions of one window (docs/eway.md §2.7): the main window acts
/// on the selection, a message window on its message (`subjects`). It
/// computes the button from them (`button`, `offer`), runs crm.lookup into
/// the dialog (`save`, `editLinks`: a `CRMDialogSession` the window shows
/// as a sheet), then crm.save in the background with the partNotDownloaded
/// round (message.download for each message the error names, then crm.save
/// again), crm.remove after the question, and opens the saved item. The
/// messages a call runs for are busy in the shared `CRMState`, so every
/// window's button reads Saving meanwhile; the outcome is a toast. The
/// list's pill, the bar and the button follow the daemon's
/// notify.messagesChanged.
@MainActor
public final class CRMController {
    public let client: RPCClient
    public let state: CRMState

    /// The messages the window acts on: the shown message of each selected
    /// row, or the window's message.
    public var subjects: @MainActor () -> [MessageSummary] = { [] }
    /// Of the subjects, the messages shown by a conversation's row (its
    /// newest folder member standing for the thread): such a row offers
    /// nothing (`CRM.Row.conversation`).
    public var conversations: @MainActor () -> Set<MessageID> = { [] }
    /// Shows a dialog (a sheet on the window).
    public var present: (@MainActor (CRMDialogSession) -> Void)?
    /// Asks before crm.remove; true goes ahead.
    public var confirm: (@MainActor (CRM.Question) async -> Bool)?
    /// A transient message in the window.
    public var toast: (@MainActor (String) -> Void)?
    /// Opens a URL the daemon built in the browser, without a question.
    public var openURL: (@MainActor (String) -> Void)?

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "crm")

    public init(client: RPCClient, state: CRMState) {
        self.client = client
        self.state = state
    }

    /// What the window's button and menus offer now.
    public var offer: CRM.Offer { state.offer(subjects(), conversations: conversations()) }

    /// The window's button now.
    public var button: CRM.Button { state.button(subjects(), conversations: conversations()) }

    /// The one subject's record, when exactly one is shown and saved.
    public var record: CrmRecord? {
        let s = subjects()
        guard s.count == 1 else { return nil }
        return state.record(for: s[0])
    }

    /// Save to eWay-CRM (the button, the menu): the dialog over crm.lookup
    /// of the subjects, when they can be saved and none is busy.
    public func save() {
        let s = subjects()
        let o = state.offer(s, conversations: conversations())
        guard o.shown, o.enabled, let first = s.first, !s.contains(where: { state.inFlight.has($0.accountId, $0.id) }),
              let crm = state.account else { return }
        let ids = s.map(\.id)
        let session = CRMDialogSession(
            controller: self, messageAccount: first.accountId, messageIDs: ids, summary: Self.summary(s))
        present?(session)
        let params = CrmLookupParams(accountId: crm.id, messageAccountId: first.accountId, messageIds: ids)
        let client = client
        Task { [weak session] in
            let outcome: Result<CrmLookupResult, any Error>
            do {
                outcome = .success(try await client.call(API.CrmLookup.self, params))
            } catch {
                outcome = .failure(error)
            }
            session?.loaded(outcome)
        }
    }

    /// Edit Links… of a saved message: the same dialog, which edits once
    /// crm.lookup returns the message's record.
    public func editLinks() {
        save()
    }

    /// Open in eWay-CRM: the saved item's URL (the daemon's).
    public func open() {
        guard let rec = record else { return }
        let u = CRM.openURL(rec.url)
        if !u.isEmpty {
            openURL?(u)
        }
    }

    /// Remove from eWay-CRM: the question (`CRM.removeQuestion`), then
    /// crm.remove of the one saved subject.
    public func remove() {
        let s = subjects()
        guard s.count == 1, let m = s.first, state.isSaved(m), !state.inFlight.has(m.accountId, m.id),
              let crm = state.account else { return }
        let q = CRM.removeQuestion(m.subject)
        Task { [weak self] in
            guard let self, let confirm = self.confirm, await confirm(q) else { return }
            self.runRemove(crm: crm.id, m)
        }
    }

    private func runRemove(crm: AccountID, _ m: MessageSummary) {
        state.begin(m.accountId, [m.id])
        let params = CrmRemoveParams(accountId: crm, messageAccountId: m.accountId, messageId: m.id)
        let client = client
        // The shared state outlives this controller: a message window closed
        // while its removal runs must still release the busy mark and record
        // the outcome, or the main window would show Saving for ever. Only
        // the toast needs the window.
        let state = state
        Task { [weak self] in
            var failure: (any Error)?
            do {
                _ = try await client.call(API.CrmRemove.self, params)
            } catch {
                failure = error
            }
            defer { state.end(m.accountId, [m.id]) }
            if let failure {
                if (failure as? RPCError)?.code == .crmItemNotFound {
                    // The daemon dropped its record too.
                    state.setOverride(m.accountId, m.id, nil)
                }
                guard let self else { return }
                let t = CRM.errorText(failure)
                self.toast?(t.isEmpty ? rpcErrorText(CRM.actionText(.remove), failure) : t)
            } else {
                state.setOverride(m.accountId, m.id, nil)
                self?.toast?(CRM.removedToast())
            }
        }
    }

    /// crm.save of a dialog's choice, in the background: the messages are
    /// busy meanwhile; a partNotDownloaded downloads the messages it names
    /// (the status as a toast) and asks again, at most twice.
    func runSave(_ params: CrmSaveParams) {
        let account = params.messageAccountId
        let ids = params.messageIds
        state.begin(account, ids)
        let client = client
        // As in runRemove: the state is released and told the outcome even
        // when the window (and this controller) went away meanwhile.
        let state = state
        Task { [weak self] in
            let outcome = await Self.save(client, params) { [weak self] n in
                self?.toast?(CRM.downloadingText(n))
            }
            defer { state.end(account, ids) }
            switch outcome {
            case .success(let res):
                for o in res.results where o.error == nil {
                    state.setOverride(account, o.messageId, o.record)
                }
                guard let self else { return }
                let (text, err) = CRM.saveToast(CRM.summarize(res))
                self.toast?(text ?? rpcErrorText(CRM.actionText(.save), err))
            case .failure(let error, let action):
                guard let self else { return }
                self.log.info("crm save failed: \((error as? RPCError)?.code.rawValue ?? 0, privacy: .public)")
                let t = CRM.errorText(error)
                self.toast?(t.isEmpty ? rpcErrorText(CRM.actionText(action), error) : t)
            }
        }
    }

    enum SaveOutcome {
        case success(CrmSaveResult)
        case failure(any Error, CRM.Action)
    }

    /// The partNotDownloaded round of crm.save (docs/eway.md D6).
    static func save(
        _ client: RPCClient, _ params: CrmSaveParams, downloading: @MainActor (Int) -> Void
    ) async -> SaveOutcome {
        var attempt = 0
        while true {
            do {
                return .success(try await client.call(API.CrmSave.self, params))
            } catch {
                guard let missing = CRM.notDownloaded(error), attempt < 2 else {
                    return .failure(error, .save)
                }
                attempt += 1
                downloading(missing.count)
                for id in missing {
                    do {
                        _ = try await client.call(
                            API.MessageDownload.self, MessageDownloadParams(accountId: params.messageAccountId, messageId: id))
                    } catch {
                        return .failure(error, .download)
                    }
                }
            }
        }
    }

    /// The dialog's one line: the first message's subject and sender, and
    /// the number of messages when there are several. Plain text.
    static func summary(_ messages: [MessageSummary]) -> String {
        guard let first = messages.first else { return "" }
        var parts = [Jira.clean(subjectText(first.subject))]
        if let from = first.from.first {
            let sender = Jira.clean(displayName(from))
            if !sender.isEmpty {
                parts.append(sender)
            }
        }
        if messages.count > 1 {
            parts.append(L10n.N("%d message", "%d messages", messages.count))
        }
        return parts.filter { !$0.isEmpty }.joined(separator: " · ")
    }
}

extension CRMDialogSession {
    /// DEVELOPMENT AID (`MALACHI_START` crm-dialog): a session over
    /// invented suggestions without a daemon; Save and Cancel only close it,
    /// the search finds nothing.
    public static func sample() -> CRMDialogSession {
        let s = CRMDialogSession(
            controller: nil, messageAccount: "sample", messageIDs: ["m_sample"],
            summary: "Offer for the fleet renewal · Jan Novák")
        s.loaded(.success(CRM.sampleLookup()))
        return s
    }
}

extension CRM {
    /// Invented suggestions for the dialog's look (fictional people and
    /// companies).
    public static func sampleLookup() -> CrmLookupResult {
        func g(_ n: Int) -> String { "00000000-0000-4000-8000-" + String(format: "%012d", n) }
        return CrmLookupResult(
            accountId: "sample", saved: [CrmSavedMessage(messageId: "m_sample")],
            suggestions: [
                CrmItem(kind: .contact, guid: g(1), name: "Novák, Jan", detail: "jan.novak@acme.example", reason: .address),
                CrmItem(kind: .contact, guid: g(2), name: "Dvořáková, Jana", detail: "jana@acme.example", reason: .address),
                CrmItem(kind: .company, guid: g(3), name: "Acme s.r.o.", detail: "acme.example", reason: .domain),
                CrmItem(kind: .company, guid: g(4), name: "Acme Holding a.s.", reason: .contact),
                CrmItem(kind: .deal, guid: g(5), name: "Fleet renewal 2027", detail: "Open", reason: .contact, superior: true),
                CrmItem(kind: .deal, guid: g(6), name: "Service contract", detail: "Open", reason: .company),
                CrmItem(kind: .project, guid: g(7), name: "Rollout Brno", reason: .company),
            ],
            partial: true)
    }
}
