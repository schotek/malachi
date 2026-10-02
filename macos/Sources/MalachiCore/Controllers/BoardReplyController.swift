// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The board's Suggest Reply (docs/mcp.md "A suggested reply on the
/// board", docs/security.md §10.2), once for the whole application: on the
/// user's click in a case's detail, the user's Claude Code writes one reply
/// draft for that case through the bridge (`AssistantRequest` with
/// `Tools`), and the draft is linked as the case's suggested reply.
///
/// A request, each step of which may end it (`state`):
///
/// 1. It needs what the compose rewrite needs (`available`: the assistant
///    shown with the In App target, `AssistantController.panelShown`),
///    the bridge beside the application, Claude Code found, a case with a
///    message to reply to, and no other request running (one at a time
///    for the application; `start` refuses another). The first request
///    ever asks the assistant's consent (`consent`: the panel's sheet, the
///    key `assistant-consent`); the board's triage consent is not needed.
///    Declined: back to idle. The time (`timeout`) runs from here.
/// 2. `board.get` for the case: its newest members' ids and, fresh, its
///    reply target; a case that has a suggested reply by now ends it
///    quietly.
/// 3. The request: `Assistant.suggestReplyMessage` under
///    `Assistant.suggestReplySystemPrompt`, with the assistant's model, the
///    bridge started as `--socket <socket> --reply-only <replyMessageId>`
///    and only `Assistant.suggestReplyTools`.
/// 4. The draft is the one a successful create_draft result names
///    (`Assistant.parseDraftResult`, as the panel's Open Draft card).
///    Without one the request failed (`.noDraft`). With one it is linked
///    with `board.setDraft`, and the board lists again (`onRefresh`;
///    notify.boardChanged follows anyway) and shows the block. A link the
///    daemon refuses deletes the draft (`draft.delete`, so no orphan
///    stays in Drafts); a refusal because the case got a suggested reply
///    meanwhile (`conflict`) ends quietly, any other as `.backend`.
///
/// Stop (`cancel()`) and the timeout delete a draft that was created and
/// not linked yet; a link already on its way is left to finish (it is a
/// local call), and deletes the draft only when it fails. Losing what it
/// needs while it runs (`availabilityChanged`) stops it like Stop.
/// Quitting stops it and waits for those deletes, at most `endWait`
/// (`cancelAndCleanUp`). Nothing the model writes is shown or logged: what
/// it wrote reaches the user only as the draft, which the detail shows
/// from the daemon.
@MainActor
public final class BoardReplyController {
    /// How long `cancelAndCleanUp` waits for the deletes at most (the
    /// triage's bound at quit).
    public nonisolated static let endWait: Duration = BoardTriageController.endWait

    public let settings: Settings
    private var runtimeAvailable: Bool {
        settings.assistantProvider == .chatgpt ? request.provider?()?.available == true : locator.locate() != nil
    }
    private func runtimeSignedIn() async -> Bool? {
        if settings.assistantProvider == .chatgpt { return request.provider?()?.connected ?? false }
        return await locator.signedIn()
    }
    public func providerChanged() {
        cancel()
        availabilityChanged()
    }
    public let locator: ClaudeCodeLocator
    public let request: AssistantRequest

    public private(set) var state: Board.SuggestReplyState = .idle {
        didSet {
            if state != oldValue {
                observers.notify()
            }
        }
    }

    /// Whether Claude Code is signed in, as last asked; nil when not known.
    public private(set) var signedIn: Bool? {
        didSet {
            if signedIn != oldValue {
                observers.notify()
            }
        }
    }

    /// Asks the user before the first request ever (the panel's consent
    /// sheet, `assistant-consent`, shared with the panel and the compose
    /// rewrite); true allows. Without it a request that needs consent ends
    /// quietly. The board's triage consent is not asked: this request is
    /// the user's own, for one case.
    public var consent: (@MainActor () async -> Bool)?

    /// Asks the board to list again (after a draft was linked). The
    /// application wires it to its board source's `refresh()`.
    public var onRefresh: (@MainActor () -> Void)?

    /// How long the request may take (`Assistant.suggestReplyTimeout`).
    /// The controller keeps the time itself (`sleep`), so the end of a
    /// request that runs out of it is the controller's, created draft and
    /// all; the request's own bound is a little longer.
    public var timeout: Duration = Assistant.suggestReplyTimeout

    /// How the timeout sleeps (tests replace it with a gate they open).
    public typealias Sleep = @Sendable (Duration) async -> Void
    var sleep: Sleep = { d in try? await Task.sleep(for: d) }

    private let client: RPCClient
    private let bridge: String?
    private let socket: String
    private let available: @MainActor () -> Bool
    private let observers = BoardObservers()
    private var signInToken: ClaudeCodeLocator.SignInToken?
    private var assistantToken: AssistantController.Token?
    private var settingsToken: Settings.ChangeToken?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")

    /// Bumped by every `start` and `cancel`: the steps of an older request
    /// stop at their next `await`.
    private var gen = 0
    /// The case of the request under way.
    private var caseID: Board.CaseID?
    /// The create_draft calls of the request, by tool use id.
    private var draftCalls: Set<String> = []
    /// The draft the request created and that is not linked yet (the
    /// tests read it).
    private(set) var created: Assistant.DraftRef?
    /// Ends the request when `timeout` passed.
    private var timer: Task<Void, Never>?
    /// board.setDraft is on its way: Stop leaves the draft to it.
    private var linking = false
    /// Bumped by every sign-in check and every sign-in a request learnt.
    private var signInGen = 0
    /// The board.setDraft and draft.delete calls on their way.
    private var pending: [Int: Task<Void, Never>] = [:]
    private var nextPending = 0

    /// - Parameters:
    ///   - client: the daemon (board.get, board.setDraft, draft.delete).
    ///   - settings: the assistant's model and consent.
    ///   - locator: the application's Claude Code (shared with the panel).
    ///   - request: the one-shot request (tests shorten it); its `consent`
    ///     is the caller's to set.
    ///   - bridge: `malachi-mcp` beside the application, nil without.
    ///   - socket: the daemon's socket, for the bridge.
    ///   - available: whether the feature can exist; its changes come
    ///     through `availabilityChanged()`.
    public init(
        client: RPCClient, settings: Settings, locator: ClaudeCodeLocator, request: AssistantRequest? = nil,
        bridge: String?, socket: String, available: @escaping @MainActor () -> Bool
    ) {
        self.client = client
        self.settings = settings
        self.locator = locator
        self.request = request ?? AssistantRequest(settings: settings, locator: locator)
        self.bridge = bridge
        self.socket = socket
        self.available = available
        // The controller asks consent itself, before its time runs.
        self.request.consent = nil
        signInToken = locator.onSignInChange { [weak self] in
            self?.checkSignIn()
            self?.observers.notify()
        }
    }

    /// The application's: available as the compose rewrite is, with the
    /// assistant's `panelShown`, whose changes and those of the
    /// `assistant-target` preference are reported here.
    public convenience init(
        client: RPCClient, settings: Settings, locator: ClaudeCodeLocator, assistant: AssistantController,
        bridge: String?, socket: String
    ) {
        self.init(
            client: client, settings: settings, locator: locator, bridge: bridge, socket: socket,
            available: { [weak assistant] in assistant?.panelShown ?? false })
        assistantToken = assistant.onChange { [weak self] in self?.availabilityChanged() }
        settingsToken = settings.onChange(.assistantTarget) { [weak self] in self?.availabilityChanged() }
    }

    /// Calls `f` after any change of what the control shows.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// Nothing runs and no call is on its way (the tests wait for it).
    var isIdle: Bool { !state.isRunning && pending.isEmpty }

    // MARK: Inputs

    /// Whether the feature is available, or Claude Code where it is, may
    /// have changed: a request that lost it stops, the sign-in is asked
    /// again, and the change reported.
    public func availabilityChanged() {
        if state.isRunning, !canRun {
            log.info("board reply: stopped, no longer available")
            cancel()
        }
        checkSignIn()
        observers.notify()
    }

    /// Asks Claude Code whether it is signed in (its cached answer unless a
    /// sign-in or `locator.refresh()` dropped it); a late answer that a
    /// later check, or a request's own finding, overtook is dropped.
    public func checkSignIn() {
        signInGen += 1
        if settings.assistantProvider == .chatgpt { signedIn = request.provider?()?.connected ?? false; return }
        let g = signInGen
        guard available(), runtimeAvailable else {
            signedIn = nil
            return
        }
        Task { [weak self] in
            guard let self else { return }
            let s = await self.runtimeSignedIn()
            guard g == self.signInGen else { return }
            self.signedIn = s
        }
    }

    private func learnSignedIn(_ s: Bool?) {
        signInGen += 1
        signedIn = s
    }

    // MARK: The view

    /// The feature can run: available, the bridge and Claude Code there.
    public var canRun: Bool {
        available() && bridge != nil && runtimeAvailable
    }

    /// The control for case `c` of snapshot `s` (`Board.suggestReplyView`);
    /// `samples`: the board shows the invented samples.
    public func view(for c: Board.Case, in s: Board.Snapshot, samples: Bool) -> Board.SuggestReplyView {
        Board.suggestReplyView(Board.SuggestReplyInputs(
            offered: Board.suggestReplyOffered(c, in: s, samples: samples), available: available() && bridge != nil,
            claudeFound: runtimeAvailable, signedIn: signedIn, state: state, caseID: c.id, provider: settings.assistantProvider))
    }

    // MARK: A request

    /// Starts a suggested reply for `c` with the user's `instruction` (may
    /// be empty). False, and nothing changes, when one runs already or it
    /// cannot run (see the type's comment).
    @discardableResult
    public func start(_ c: Board.Case, instruction: String) -> Bool {
        guard !state.isRunning, canRun, c.reply != nil, c.draft == nil else { return false }
        gen += 1
        let my = gen
        caseID = c.id
        draftCalls = []
        created = nil
        linking = false
        state = .running(c.id)
        log.info("board reply: started")
        Task { [weak self] in
            await self?.run(my, c.id, instruction)
        }
        return true
    }

    /// Stop: the request under way ends as `.cancelled`; a draft it
    /// created and that is not being linked is deleted.
    public func cancel() {
        guard case .running(let id) = state else { return }
        log.info("board reply: stopped")
        end(id, .cancelled)
    }

    /// The request of `my` ran out of time.
    private func timedOut(_ my: Int) {
        guard my == gen, case .running(let id) = state else { return }
        log.info("board reply: no draft in time")
        end(id, .timeout)
    }

    /// Ends the request under way from outside its own steps (Stop, the
    /// timeout): its process goes, a draft it created and that is not
    /// being linked is deleted.
    private func end(_ id: Board.CaseID, _ failure: Board.SuggestReplyFailure) {
        gen += 1
        timer?.cancel()
        timer = nil
        request.cancel()
        if let ref = created, !linking {
            delete(ref)
        }
        created = nil
        state = .failed(id, failure)
    }

    /// `cancel()`, then waits until the board.setDraft and draft.delete
    /// calls on their way were answered, at most `wait`: quitting awaits it
    /// before the connection stops, and never hangs on a daemon that does
    /// not answer.
    public func cancelAndCleanUp(wait: Duration = BoardReplyController.endWait) async {
        cancel()
        let tasks = Array(pending.values)
        guard !tasks.isEmpty else { return }
        let gate = ReplyGate()
        await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
            gate.cont = c
            let timer = Task { @MainActor in
                try? await Task.sleep(for: wait)
                gate.open()
            }
            Task { @MainActor in
                for t in tasks {
                    await t.value
                }
                timer.cancel()
                gate.open()
            }
        }
    }

    private func run(_ my: Int, _ id: Board.CaseID, _ instruction: String) async {
        // 1. The assistant's consent, once ever; an answer counts even when
        // the request was stopped while the question was up.
        if !settings.selectedAssistantConsent {
            let allowed = await consent?() ?? false
            if allowed {
                settings.selectedAssistantConsent = true
            }
            guard my == gen else { return }
            guard allowed else {
                state = .idle
                return
            }
        }
        // The time runs from here, not while the question was up.
        let sleep = sleep
        let timeout = timeout
        timer = Task { [weak self] in
            await sleep(timeout)
            guard !Task.isCancelled else { return }
            self?.timedOut(my)
        }
        // 2. The case, fresh.
        let got: BoardGetResult
        do {
            got = try await client.call(API.BoardGet.self, BoardGetParams(caseId: BoardCaseID(rawValue: id.rawValue)))
        } catch {
            log.info("board reply: board.get: \(String(describing: error), privacy: .public)")
            return fail(my, id, .backend)
        }
        guard my == gen, let bridge else { return }
        let c = got.case
        if c.draft != nil {
            // A suggested reply came meanwhile (the triage): it shows.
            log.info("board reply: the case has a suggested reply already")
            timer?.cancel()
            timer = nil
            state = .idle
            onRefresh?()
            return
        }
        // 3. The request.
        let message = Assistant.suggestReplyMessage(
            accountID: c.accountId.rawValue, messageID: c.replyMessageId.rawValue,
            others: got.messages.map(\.id.rawValue), instruction: instruction)
        request.start(
            systemPrompt: Assistant.suggestReplySystemPrompt(), message: message,
            tools: AssistantRequest.Tools(
                bridge: bridge, socket: socket,
                bridgeArgs: Assistant.suggestReplyBridgeArgs(messageID: c.replyMessageId.rawValue),
                allowed: Assistant.suggestReplyTools),
            timeout: timeout + .seconds(10),
            // The assistant's model (`assistant-model`), not the board's
            // triage model: the user asked for this reply and waits for
            // it, as for the panel and the compose rewrite.
            model: settings.assistantModel,
            onTool: { [weak self] e in self?.tool(my, e) },
            // No usage is recorded: the 24-hour row of tokens is the
            // triage's (board.runEnd), and a suggested reply is no run.
            onUsage: nil,
            completion: { [weak self] outcome in self?.answered(my, id, outcome) })
    }

    /// Notes the draft a create_draft of request `my` created.
    private func tool(_ my: Int, _ e: Assistant.Event) {
        guard my == gen else { return }
        switch e.kind {
        case .toolUse where e.tool == Assistant.suggestReplyDraftTool:
            draftCalls.insert(e.toolUseID)
        case .toolResult where draftCalls.contains(e.toolUseID):
            draftCalls.remove(e.toolUseID)
            guard !e.isError, created == nil, let ref = Assistant.parseDraftResult(e.resultText) else { return }
            created = ref
        default:
            break
        }
    }

    /// The request of `my` ended.
    private func answered(_ my: Int, _ id: Board.CaseID, _ outcome: AssistantRequest.Outcome) {
        guard my == gen else { return }
        let ref = created
        timer?.cancel()
        timer = nil
        switch outcome {
        case .declined:
            if let ref {
                delete(ref)
            }
            created = nil
            state = .idle
        case .failed(.stopped(let reason)) where reason == AssistantRequest.timedOut:
            if let ref {
                delete(ref)
            }
            fail(my, id, .timeout)
        case .answered, .failed:
            if case .answered = outcome {
                learnSignedIn(true)
            } else if outcome == .failed(.notSignedIn) {
                learnSignedIn(false)
            }
            if let ref {
                // The draft is there, whatever the turn did after it.
                link(my, id, ref)
                return
            }
            switch outcome {
            case .failed(let f): fail(my, id, Self.failure(f))
            default: fail(my, id, .noDraft)
            }
        }
    }

    private static func failure(_ f: AssistantRequest.Failure) -> Board.SuggestReplyFailure {
        switch f {
        case .notFound: return .notFound
        case .notSignedIn: return .notSignedIn
        case .toolsMissing: return .toolsMissing
        case .stopped(let reason): return reason == AssistantRequest.timedOut ? .timeout : .stopped
        }
    }

    /// 4. board.setDraft for the created draft, then the board lists
    /// again; a refused link deletes the draft.
    private func link(_ my: Int, _ id: Board.CaseID, _ ref: Assistant.DraftRef) {
        linking = true
        timer?.cancel()
        timer = nil
        let client = client
        track { [weak self] in
            var refused: ErrorCode?
            do {
                _ = try await client.call(
                    API.BoardSetDraft.self,
                    BoardSetDraftParams(caseId: BoardCaseID(rawValue: id.rawValue), draftId: DraftID(rawValue: ref.draftID)))
            } catch {
                refused = (error as? RPCError)?.code ?? .internalError
                self?.log.info("board reply: board.setDraft: \(String(describing: error), privacy: .public)")
            }
            guard let self else { return }
            if refused != nil {
                self.delete(ref)
            }
            self.onRefresh?()
            guard my == self.gen else { return }
            self.linking = false
            self.created = nil
            switch refused {
            case nil:
                self.log.info("board reply: linked")
                self.state = .idle
            case .some(.conflict):
                // The case got a suggested reply meanwhile: that one shows.
                self.state = .idle
            default:
                self.state = .failed(id, .backend)
            }
        }
    }

    /// Ends request `my` with `failure`.
    private func fail(_ my: Int, _ id: Board.CaseID, _ failure: Board.SuggestReplyFailure) {
        guard my == gen else { return }
        timer?.cancel()
        timer = nil
        if failure == .notSignedIn {
            learnSignedIn(false)
        }
        created = nil
        log.info("board reply: failed, \(String(describing: failure), privacy: .public)")
        state = .failed(id, failure)
    }

    /// Deletes a draft the request created and that is not linked.
    private func delete(_ ref: Assistant.DraftRef) {
        let client = client
        track { [weak self] in
            do {
                _ = try await client.call(
                    API.DraftDelete.self,
                    DraftDeleteParams(accountId: AccountID(rawValue: ref.accountID), draftId: DraftID(rawValue: ref.draftID)))
            } catch {
                self?.log.info("board reply: draft.delete: \(String(describing: error), privacy: .public)")
            }
        }
    }

    /// Runs `body` as a call on its way, kept in `pending` until it ends.
    private func track(_ body: @escaping @MainActor () async -> Void) {
        nextPending += 1
        let key = nextPending
        pending[key] = Task { [weak self] in
            await body()
            self?.pending[key] = nil
            // `isIdle` changed.
            self?.observers.notify()
        }
    }
}

/// Resumes `cancelAndCleanUp`'s wait once.
@MainActor
private final class ReplyGate {
    var cont: CheckedContinuation<Void, Never>?

    func open() {
        cont?.resume()
        cont = nil
    }
}
