// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The board's triage run (docs/api.md §4.13 "Triage and runs", docs/mcp.md
/// "Triage of the board", docs/security.md §10.2), once for the whole
/// application: the user's Claude Code annotates the cases of the board
/// through the bridge's triage tools, in one bounded one-shot request
/// (`AssistantRequest` with `Tools`). The daemon never talks to an
/// assistant; this controller starts the run on the user's click
/// (`start(.manual)`) or on the schedule's (`BoardAutoTriageScheduler`).
///
/// A run, each step of which may end it (`state`):
///
/// 1. What it needs: triage available (`available`: the Assistant shown
///    with the In App target, `triageNeedsInAppTarget`), the bridge beside
///    the application, Claude Code found and not signed out (asked afresh
///    for a manual run), and consent (`consentGiven`: the panel's
///    `assistant-consent`, the board's own `board-triage-consent` and the
///    board's `assistant` preference). A manual run without consent asks
///    (`consent`, the sheet) and gives it (`giveConsent`); an automatic run
///    never asks. A manual run whose queue is known to be empty ends at
///    once (`.nothingToDo`).
/// 2. `board.runStart` with the trigger and `Assistant.triageSource`.
/// 3. The request: `Assistant.triageMessage` for at most the run's limit
///    under `Assistant.triageSystemPrompt`, with the board's own model
///    (`board-triage-model`, read when the run starts; not the panel's
///    `assistant-model`), with the bridge started as
///    `--socket <socket> --allow-triage --triage-run <runId> --triage-max
///    <limit>` and only `Assistant.triageTools(for:)` (no create_draft for
///    an automatic run), for at most `Assistant.triageTimeout`. Every
///    `annotate_case` call the bridge accepted counts as progress, of the
///    queue's size at the start capped by the limit; refused ones are
///    counted apart. When the accepted ones reach the limit the run has
///    succeeded: the bridge refuses more and closes the queue, telling the
///    model to stop, so the request is left to end by itself and deliver
///    Claude Code's result, whose usage is the whole run's; after
///    `limitGrace` without it the request is cancelled. Meanwhile the run
///    is still running (its progress full), later tool calls are not
///    counted, and whatever ends it ends it as a success.
/// 4. `board.runEnd` with the failure's class, or none, and the tokens
///    the run used as far as its Claude Code reported them
///    (`Assistant.UsageTally`: the result's usage, else the sum of the API
///    messages seen before it ended — cancelled, timed out, at its limit
///    without a result in time: a lower bound; none when nothing reported
///    any); then `onRefresh` asks the board to
///    list again. A run the application could not end
///    (it was killed) the daemon ends itself later.
///
/// One run at a time: `start` while one is active does nothing. `cancel()`
/// ends it (`.cancelled`, recorded so); so does losing what it needs while
/// it runs (triage no longer available, a consent withdrawn, the board or
/// its assistant preference off, and for an automatic run `autoTriage`
/// off). An automatic run that ends with no note accepted although its
/// queue had cases fails (`.notesRefused`, `.noProgress`), so the
/// schedule backs off. Nothing the model writes is shown
/// or logged, nor any mail text: a run's outcome is its counts and its
/// class.
@MainActor
public final class BoardTriageController {
    /// Owner's decision (2026-10-01): triage is offered only under the
    /// assistant's "In App (experimental)" target, the condition of the
    /// panel, the compose rewrite and the search in the user's own words
    /// (`AssistantController.panelShown`). It is the only target under
    /// which the application itself starts Claude Code, and it stays
    /// experimental until Anthropic confirms the terms for running Claude
    /// Code from an application. False would offer triage whenever the
    /// Assistant is shown, whatever the target.
    public nonisolated static let triageNeedsInAppTarget = true

    /// Whether triage is available with the Assistant `shown` and `target`
    /// chosen (`triageNeedsInAppTarget`).
    public nonisolated static func triageAvailable(shown: Bool, target: Assistant.Target) -> Bool {
        shown && (!triageNeedsInAppTarget || target == .app)
    }

    /// How long a run that reached its limit waits for Claude Code's
    /// result, the only report of the whole run's usage (the API messages
    /// carry only their starts' output tokens). The bridge has closed the
    /// queue and tells the model to stop, so it needs one or two more API
    /// calls over its cached context to notice and end its turn; on the
    /// owner's first real run a call took about 10 s (40 notes, 8 minutes),
    /// so 45 s leaves room for a slow pair without a full progress bar
    /// hanging for long.
    public nonisolated static let limitGrace: Duration = .seconds(45)

    /// How long `cancelAndEnd` waits for `board.runEnd` at most.
    public nonisolated static let endWait: Duration = .seconds(2)

    /// How often the view is published again while it names a relative
    /// time (`Board.TriageView.relativeTime`).
    public nonisolated static let clockTick: Duration = .seconds(60)

    public let settings: Settings
    private var runtimeAvailable: Bool {
        settings.assistantProvider == .chatgpt ? request.provider?()?.available == true : locator.locate() != nil
    }
    private func runtimeSignedIn() async -> Bool? {
        if settings.assistantProvider == .chatgpt { return request.provider?()?.connected ?? false }
        return await locator.signedIn()
    }
    private var providerEpoch = 0
    /// The assistant's provider changed, or its profile (Go
    /// `ProviderChanged`): a run under way stops, automatic triage goes
    /// off when the provider itself changed (`disableAutomaticTriage`: a
    /// consent given to one provider never starts runs of another), the
    /// daemon's assistant preference goes off when the board's consent is
    /// not given (the settings answer for the provider now selected), and
    /// availability and the sign-in are asked again. Both changes go in
    /// one quiet write, so that the repair is not skipped for the write
    /// under way. The controller calls it itself for the settings keys
    /// that concern the provider in effect
    /// (`AssistantRequest.providerChangeConcernsActive`); the application
    /// calls it for a change of the ChatGPT connection while ChatGPT is the
    /// provider.
    public func providerChanged(disableAutomaticTriage: Bool = false) {
        providerEpoch += 1
        cancel()
        var repair = !granting && !settings.selectedBoardConsent
        if preferences.stored?.assistant == false {
            repair = false
        }
        if disableAutomaticTriage || repair {
            if repair {
                log.info("board triage: the assistant preference was on without consent; turning it off")
            }
            preferences.update(
                quiet: true,
                { p in
                    if disableAutomaticTriage {
                        p.autoTriage = false
                    }
                    if repair {
                        p.assistant = false
                    }
                }, completion: nil)
        }
        availabilityChanged()
    }
    public let locator: ClaudeCodeLocator
    public let preferences: BoardPreferencesController
    public let request: AssistantRequest

    public private(set) var state: Board.TriageState = .idle {
        didSet {
            if state != oldValue {
                publish()
            }
        }
    }

    /// Whether Claude Code is signed in, as last asked; nil when not known.
    public private(set) var signedIn: Bool? {
        didSet {
            if signedIn != oldValue {
                publish()
            }
        }
    }

    /// What the board last reported about the triage (`boardChanged`).
    public private(set) var board = BoardInfo()
    /// Bumped whenever `board` changed.
    public private(set) var boardRevision = 0
    /// The board's last decisive phase from a snapshot (ready, preparing,
    /// off, unsupported); nil before one. Off and unsupported take the
    /// triage away (`Board.TriageViewInputs.boardPhase`).
    public private(set) var boardPhase: Board.Phase?

    /// Why automatic triage pauses, as the schedule set it
    /// (`setAutoPause`), for the view.
    public private(set) var autoPause: Board.AutoTriagePause?

    /// Asks the user whether the board's mail may go to the assistant (the
    /// sheet: `Board.Text.triageConsentHeading`, `triageConsentBody`, the
    /// panel's Allow and Cancel); true allows. Without it a manual run
    /// that needs consent ends as `.declined`.
    public var consent: (@MainActor () async -> Bool)?

    /// How long a run may take (`Assistant.triageTimeout`; tests shorten
    /// it).
    public var timeout: Duration = Assistant.triageTimeout

    /// How long a run at its limit waits for the result (`limitGrace`;
    /// tests shorten it).
    public var grace: Duration = BoardTriageController.limitGrace

    /// Asks the board to list again (after a run ended). The application
    /// wires it to its board sources' `refresh()`.
    public var onRefresh: (@MainActor () -> Void)?

    /// How the clock of relative times sleeps (tests replace it).
    public typealias Sleep = @Sendable (Duration) async -> Void
    var clockSleep: Sleep = { d in try? await Task.sleep(for: d) }

    /// What the board reports about the triage.
    public struct BoardInfo: Sendable, Equatable {
        /// A board.list has arrived.
        public var known = false
        /// The board's assistant preference as board.list reported it.
        public var assistantOn = false
        /// Cases waiting for the assistant (0 while it is off).
        public var queue = 0
        /// Cases automatic runs annotated today, and when that was reported.
        public var annotatedToday = 0
        public var countedAt: Date?
        /// The daemon's last run.
        public var lastRun: Board.Run?
        /// The tokens triage runs used in the last 24 hours; nil when none
        /// reported any.
        public var usage24h: BoardUsageTotal?

        public init() {}
    }

    private let client: RPCClient
    private let bridge: String?
    private let socket: String
    private let available: @MainActor () -> Bool
    private let now: @MainActor () -> Date
    private let language: @MainActor () -> String
    private let today: @MainActor () -> String
    private let observers = BoardObservers()
    private let ended = BoardObservers()
    private var endedRun: (trigger: Board.TriageTrigger, failure: Board.TriageFailure?)?
    private var tokens: [BoardObserverToken] = []
    private var settingsTokens: [Settings.ChangeToken] = []
    private var signInToken: ClaudeCodeLocator.SignInToken?
    private var assistantToken: AssistantController.Token?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")

    /// Bumped by every `start` and `cancel`: the steps of an older run stop
    /// at their next `await`.
    private var gen = 0
    /// The run of the daemon under way, once board.runStart answered.
    private var runID: BoardRunID?
    /// The annotate_case calls of the run, by id, and the accepted ones.
    private var annotateCalls: Set<String> = []
    /// The distinct cases the run's accepted notes named (Done counts
    /// them: the bridge takes a second note on a case without charging
    /// another of the run's cases).
    private var annotatedCases: Set<String> = []
    /// The annotate_case calls of the run the bridge refused.
    private var refused = 0
    /// The tokens the run's Claude Code reported so far.
    private var usage = Assistant.UsageTally()
    /// The most accepted annotate_case calls of the run.
    private var runLimit = Assistant.triageBatch
    /// The run reached its limit and waits for Claude Code's result
    /// (`limitGrace`): it ends as a success however it ends.
    private var limitHit = false
    /// Ends a run at its limit whose result did not come in time.
    private var graceTask: Task<Void, Never>?
    /// The run's queue was known to have cases when it started.
    private var queueHadCases = false
    /// The run passed its consent step: a consent lost from now on stops
    /// it (before, a manual run is asking or giving it).
    private var permitted = false
    /// Consent is being given (`giveConsent`): the repair of a stray
    /// assistant preference waits.
    private var granting = false
    /// Bumped by every sign-in check and every sign-in a run learnt: an
    /// older check's late answer is dropped.
    private var signInGen = 0
    /// The board.runEnd calls on their way (the tests wait for them).
    private var ending = 0
    private var endTasks: [Int: Task<Void, Never>] = [:]
    private var nextEnd = 0
    /// Publishes the view again while it names a relative time.
    private var clockTask: Task<Void, Never>?

    /// - Parameters:
    ///   - client: the daemon (board.runStart, board.runEnd).
    ///   - settings: the consents, the model.
    ///   - locator: the application's Claude Code (shared with the panel).
    ///   - preferences: the board's preferences (the application's).
    ///   - request: the one-shot request the run uses (tests shorten it).
    ///   - bridge: `malachi-mcp` beside the application, nil without.
    ///   - socket: the daemon's socket, for the bridge.
    ///   - available: whether triage is available (`triageAvailable`); its
    ///     changes come through `availabilityChanged()`.
    ///   - now, language, today: the clock, the English name of the UI
    ///     language ("" English) and today as YYYY-MM-DD for the system
    ///     prompt.
    public init(
        client: RPCClient, settings: Settings, locator: ClaudeCodeLocator, preferences: BoardPreferencesController,
        request: AssistantRequest? = nil, bridge: String?, socket: String,
        available: @escaping @MainActor () -> Bool, now: @escaping @MainActor () -> Date = { Date() },
        language: @escaping @MainActor () -> String = { Assistant.languageName(L10n.catalogue.language) },
        today: @escaping @MainActor () -> String = AssistantPanelController.localDate
    ) {
        self.client = client
        self.settings = settings
        self.locator = locator
        self.preferences = preferences
        self.request = request ?? AssistantRequest(settings: settings, locator: locator)
        self.bridge = bridge
        self.socket = socket
        self.available = available
        self.now = now
        self.language = language
        self.today = today
        // The run checks consent itself and never lets the request ask.
        self.request.consent = nil
        // The only registration of these keys for the triage: a change that
        // concerns the other provider leaves a run alone.
        for key in AssistantRequest.providerKeys {
            settingsTokens.append(settings.onChange(key) { [weak self] in
                guard let self, AssistantRequest.providerChangeConcernsActive(key, settings: self.settings) else { return }
                self.providerChanged(disableAutomaticTriage: key == .assistantProvider)
            })
        }
        self.request.usesBoardConsent = true
        self.request.providerModelID = { [weak settings] in settings?.boardTriageChatGPTModel ?? "" }
        tokens.append(preferences.observe { [weak self] in self?.permissionsChanged() })
        tokens.append(preferences.observeLoaded { [weak self] in self?.repairAssistantPreference() })
        for key in [Settings.Key.assistantConsent, .boardTriageConsent, .assistantChatGPTConsentVersion, .boardTriageChatGPTConsentVersion] {
            settingsTokens.append(settings.onChange(key) { [weak self] in self?.permissionsChanged() })
        }
        // A sign-in that starts or ends: the view says it waits for the
        // browser meanwhile, and asks the sign-in again after.
        signInToken = locator.onSignInChange { [weak self] in
            self?.checkSignIn()
            self?.publish()
        }
    }

    /// The application's run: available as `triageAvailable` says for
    /// the assistant's `shown` and the `assistant-target` preference, whose
    /// changes are reported here.
    public convenience init(
        client: RPCClient, settings: Settings, locator: ClaudeCodeLocator, preferences: BoardPreferencesController,
        assistant: AssistantController, bridge: String?, socket: String
    ) {
        self.init(
            client: client, settings: settings, locator: locator, preferences: preferences, bridge: bridge,
            socket: socket, available: { [weak assistant, settings] in
                Self.triageAvailable(shown: assistant?.shown ?? false, target: settings.assistantTarget)
            })
        assistantToken = assistant.onChange { [weak self] in self?.availabilityChanged() }
        settingsTokens.append(settings.onChange(.assistantTarget) { [weak self] in self?.availabilityChanged() })
    }

    /// Calls `f` after any change of what the view shows: the state, the
    /// sign-in, the board's triage, the consents, the preferences, the
    /// pause.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// Calls `f` once each time a run ended: its trigger and its failure
    /// (nil after a success), read through `lastEnded`; `state` still says
    /// the run is active then, and changes right after.
    public func observeEnded(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        ended.add(f)
    }

    /// The trigger and failure of the run that ended last.
    public var lastEnded: (trigger: Board.TriageTrigger, failure: Board.TriageFailure?)? { endedRun }

    /// Nothing is waiting or on its way (the tests wait for it).
    var isIdle: Bool { !state.isActive && ending == 0 }

    // MARK: Inputs

    /// The board reported (`DaemonBoardSource.onSnapshot`): the triage's
    /// queue and counts and the daemon's last run. A snapshot of a board
    /// that could not be listed changes nothing.
    public func boardChanged(_ s: Board.Snapshot) {
        switch s.phase {
        case .ready, .preparing, .off: break
        case .unsupported:
            // An older daemon: no board to triage, nothing else to learn.
            if boardPhase != .unsupported {
                boardPhase = .unsupported
                publish()
            }
            return
        case .loading, .unavailable, .failed: return
        }
        if boardPhase != s.phase {
            boardPhase = s.phase
            publish()
        }
        var b = BoardInfo()
        b.known = true
        b.assistantOn = s.annotated
        b.queue = s.triage.queue
        b.annotatedToday = s.triage.annotatedToday
        b.countedAt = now()
        b.lastRun = s.run
        b.usage24h = s.triage.usage24h
        guard b.assistantOn != board.assistantOn || b.queue != board.queue || b.annotatedToday != board.annotatedToday
            || b.lastRun != board.lastRun || b.usage24h != board.usage24h || !board.known
        else {
            board.countedAt = b.countedAt
            return
        }
        board = b
        boardRevision += 1
        publish()
    }

    /// Whether triage is available, or Claude Code where it is, may have
    /// changed: a run that lost it stops, the sign-in is asked again, and
    /// the change reported.
    public func availabilityChanged() {
        enforce()
        checkSignIn()
        publish()
    }

    /// The consents or the preferences changed.
    private func permissionsChanged() {
        enforce()
        publish()
    }

    /// Stops the run under way when it lost what it needs (see the type's
    /// comment).
    private func enforce() {
        guard state.isActive, let trigger = state.trigger else { return }
        let p = preferences.preferences
        let lost = !available() || (permitted && !consentGiven) || p?.enabled == false
            || (trigger == .automatic && p?.autoTriage == false)
        if lost {
            log.info("board triage: stopped, no longer allowed or available")
            cancel()
        }
    }

    /// The daemon answered its preferences: its `assistant` preference on
    /// while the board's consent is not given here (a withdrawal whose write
    /// failed, say) is turned off again, so the daemon stops handing mail
    /// to a triage bridge. Not while consent is being given or a write is
    /// under way (each write reads afresh first).
    private func repairAssistantPreference() {
        guard !granting, !preferences.writing, !settings.selectedBoardConsent,
              preferences.stored?.assistant == true
        else { return }
        log.info("board triage: the assistant preference was on without consent; turning it off")
        preferences.update(quiet: true, { $0.assistant = false }, completion: nil)
    }

    /// Asks the board to list again (`onRefresh`), so the tokens of the
    /// last 24 hours, which age out without a notification, are current
    /// (Settings → AI asks whenever its page comes up).
    public func relistBoard() {
        onRefresh?()
    }

    /// Set by the schedule.
    public func setAutoPause(_ p: Board.AutoTriagePause?) {
        guard p != autoPause else { return }
        autoPause = p
        publish()
    }

    /// Asks Claude Code whether it is signed in (its cached answer unless a
    /// sign-in or `locator.refresh()` dropped it). An answer that a later
    /// check, or a run's own finding, overtook is dropped.
    public func checkSignIn() {
        signInGen += 1
        if settings.assistantProvider == .chatgpt { signedIn = request.provider?()?.connected ?? false; return }
        let g = signInGen
        guard runtimeAvailable else {
            signedIn = nil
            return
        }
        Task { [weak self] in
            guard let self else { return }
            let s = await self.runtimeSignedIn()
            self.signInAnswers += 1
            guard g == self.signInGen else { return }
            self.signedIn = s
        }
    }

    /// The sign-in checks that were answered, taken or dropped (the tests
    /// wait for them).
    private(set) var signInAnswers = 0

    /// What a run learnt about the sign-in, which overtakes any check under
    /// way.
    func learnSignedIn(_ s: Bool?) {
        signInGen += 1
        signedIn = s
    }

    // MARK: What it can do

    /// Triage is available, Claude Code is there and so is the bridge.
    public var canRun: Bool {
        available() && bridge != nil && runtimeAvailable
    }

    /// Both consents and the board's assistant preference.
    public var consentGiven: Bool {
        (settings.assistantProvider == .chatgpt || settings.selectedAssistantConsent) && settings.selectedBoardConsent && preferences.preferences?.assistant == true
    }

    /// A manual run would ask for consent first.
    public var needsConsent: Bool { !consentGiven }

    /// The board source should run even while the Board is not shown: the
    /// schedule learns the queue only from its snapshots. True while
    /// automatic triage is on, consent is given and triage can run.
    public var wantsBoardData: Bool {
        preferences.preferences?.autoTriage == true && consentGiven && canRun
    }

    /// The user agreed: the board's assistant preference goes on and, once
    /// the daemon stored it, both consents are kept (the board's sheet
    /// grants the panel's consent too: one sheet, both keys). True once
    /// stored; false sets no key, and a refusal is reported through
    /// `preferences.onError` unless `quiet` (a run reports its own failure).
    @discardableResult
    public func giveConsent(quiet: Bool = false) async -> Bool {
        let epoch = providerEpoch
        let provider = settings.assistantProvider
        granting = true
        defer { granting = false }
        if preferences.preferences?.assistant != true || preferences.writing {
            guard await preferences.update(quiet: quiet, { $0.assistant = true }) else { return false }
        }
        guard epoch == providerEpoch, provider == settings.assistantProvider else {
            preferences.update(quiet: true, { $0.assistant = false; $0.autoTriage = false }, completion: nil)
            return false
        }
        if provider == .claude { settings.assistantConsent = true; settings.boardTriageConsent = true }
        else { settings.boardTriageChatGPTConsentVersion = 1 }
        return true
    }

    /// The user withdrew the board's consent (Settings): a run under way
    /// stops, the board's assistant preference goes off (its notes no
    /// longer count), so does automatic triage (a consent given again
    /// later must not bring back runs the user did not turn on again), and
    /// the assistant's consent for the panel stays.
    public func withdrawConsent() {
        cancel()
        settings.selectedBoardConsent = false
        preferences.update(
            {
                $0.assistant = false
                $0.autoTriage = false
            }, completion: nil)
    }

    /// The Triage control and the status strip.
    public var view: Board.TriageView {
        let now = now()
        var today: Int?
        if board.known, let at = board.countedAt, Calendar.current.isDate(at, inSameDayAs: now) {
            today = board.annotatedToday
        }
        return Board.triageView(
            Board.TriageViewInputs(
                shown: available(), claudeFound: runtimeAvailable, bridge: bridge != nil, signedIn: signedIn,
                needsConsent: needsConsent, assistantOn: board.known ? board.assistantOn : consentGiven,
                state: state, lastRun: board.lastRun, autoTriage: preferences.preferences?.autoTriage ?? false,
                pause: autoPause, backendFailed: preferences.preferences == nil && preferences.lastLoadFailed,
                annotatedToday: today, boardPhase: viewBoardPhase, signingIn: settings.assistantProvider == .claude && locator.signingIn, usageKnown: board.known,
                usage24h: board.usage24h, queue: board.known && board.assistantOn ? board.queue : nil, now: now, provider: settings.assistantProvider))
    }

    /// The board's phase for the view: off as the daemon's preferences say
    /// once known (they are newer than a snapshot), else as the board last
    /// reported.
    private var viewBoardPhase: Board.Phase? {
        guard let p = preferences.preferences, boardPhase != .unsupported else { return boardPhase }
        if !p.enabled {
            return .off
        }
        return boardPhase == .off ? nil : boardPhase
    }

    /// Reports a change, and keeps the clock of relative times running
    /// exactly while the view names one.
    private func publish() {
        observers.notify()
        updateClock()
    }

    /// The clock is running (the tests read it).
    var clockRunning: Bool { clockTask != nil }

    private func updateClock() {
        guard view.relativeTime else {
            clockTask?.cancel()
            clockTask = nil
            return
        }
        guard clockTask == nil else { return }
        let sleep = clockSleep
        clockTask = Task { [weak self] in
            await sleep(Self.clockTick)
            guard !Task.isCancelled, let self else { return }
            // Published again; `updateClock` starts the next tick while the
            // view still names a relative time.
            self.clockTask = nil
            self.publish()
        }
    }

    // MARK: A run

    /// Starts a run (see the type's comment); `limit` is the most cases it
    /// asks for (nil: `Assistant.triageBatch`). False when one is active.
    @discardableResult
    public func start(_ trigger: Board.TriageTrigger, limit: Int? = nil) -> Bool {
        guard !state.isActive else { return false }
        gen += 1
        let my = gen
        runID = nil
        annotateCalls = []
        annotatedCases = []
        refused = 0
        usage = Assistant.UsageTally()
        limitHit = false
        permitted = false
        queueHadCases = false
        let limit = max(1, min(limit ?? Assistant.triageBatch, Assistant.triageBatch))
        runLimit = limit
        state = .starting(trigger)
        Task { [weak self] in
            await self?.run(my, trigger, limit)
        }
        return true
    }

    /// Ends the run under way as cancelled; one that already reached its
    /// limit and waits for its result ends at once as the success it is.
    public func cancel() {
        guard state.isActive, let trigger = state.trigger else { return }
        gen += 1
        request.cancel()
        let id = runID
        runID = nil
        finish(trigger, limitHit ? nil : .cancelled, run: id)
    }

    /// `cancel()`, then waits until the `board.runEnd` calls on their way
    /// were answered, at most `wait`: quitting awaits it before the
    /// connection stops, and never hangs on a daemon that does not answer.
    public func cancelAndEnd(wait: Duration = BoardTriageController.endWait) async {
        cancel()
        let pending = Array(endTasks.values)
        guard !pending.isEmpty else { return }
        // Whichever comes first: the answers, or the bound. (A task group
        // would wait for its children, and a call to a dead daemon is not
        // cancellable.)
        let gate = EndGate()
        await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
            gate.cont = c
            let timer = Task { @MainActor in
                try? await Task.sleep(for: wait)
                gate.open()
            }
            Task { @MainActor in
                for t in pending {
                    await t.value
                }
                timer.cancel()
                gate.open()
            }
        }
    }

    private func run(_ my: Int, _ trigger: Board.TriageTrigger, _ limit: Int) async {
        // 1. What it needs.
        guard available() else { return fail(my, trigger, .assistantOff) }
        guard let bridge else { return fail(my, trigger, .toolsMissing) }
        guard runtimeAvailable else { return fail(my, trigger, .notFound) }
        if preferences.preferences == nil {
            _ = await preferences.loadNow()
            guard my == gen else { return }
            // A daemon that does not answer the board's preferences (or
            // does not know the board) cannot take a consent: no sheet.
            guard preferences.preferences != nil else { return fail(my, trigger, .backend) }
        }
        if !consentGiven {
            guard trigger == .manual, let ask = consent else { return fail(my, trigger, .declined) }
            let allowed = await ask()
            guard my == gen else { return }
            guard allowed else { return fail(my, trigger, .declined) }
            let stored = await giveConsent(quiet: true)
            guard my == gen else { return }
            guard stored else { return fail(my, trigger, .backend) }
        }
        permitted = true
        // A consent lost while the sheet was up stops it here.
        guard consentGiven else { return fail(my, trigger, .declined) }
        // The queue is known only from a board that listed with the
        // assistant on.
        let queueKnown = board.known && board.assistantOn
        if queueKnown, board.queue <= 0 {
            return fail(my, trigger, .nothingToDo)
        }
        queueHadCases = queueKnown && board.queue > 0
        if trigger == .manual {
            locator.refresh()
        }
        let signed = await runtimeSignedIn()
        guard my == gen else { return }
        learnSignedIn(signed)
        if signed == false {
            return fail(my, trigger, .notSignedIn)
        }
        // 2. The daemon's run.
        let id: BoardRunID
        do {
            let r = try await client.call(
                API.BoardRunStart.self, BoardRunStartParams(trigger: trigger.wire, source: settings.assistantProvider == .chatgpt ? "malachi-chatgpt" : Assistant.triageSource))
            id = r.runId
        } catch {
            log.info("board.runStart: \(String(describing: error), privacy: .public)")
            guard my == gen else { return }
            return fail(my, trigger, .backend)
        }
        guard my == gen else {
            // Cancelled while it started: it is ended as such.
            endRun(id, .cancelled)
            return
        }
        runID = id
        let total = queueKnown ? min(board.queue, limit) : limit
        state = .running(trigger, done: 0, total: total)
        log.info("board triage: run started (\(trigger == .manual ? "manual" : "auto", privacy: .public))")
        // 3. The request.
        let drafts = Assistant.triageDrafts(for: trigger)
        request.start(
            systemPrompt: Assistant.triageSystemPrompt(language: language(), today: today()),
            message: Assistant.triageMessage(maxCases: limit, drafts: drafts),
            tools: AssistantRequest.Tools(
                bridge: bridge, socket: socket,
                bridgeArgs: Assistant.triageBridgeArgs(runID: id.rawValue, maxCases: limit),
                allowed: Assistant.triageTools(drafts: drafts)),
            timeout: timeout, model: settings.boardTriageModel,
            onTool: { [weak self] e in self?.tool(my, trigger, e) },
            onUsage: { [weak self] e in self?.counted(my, e) },
            completion: { [weak self] outcome in self?.answered(my, trigger, outcome) })
    }

    /// Counts an annotate_case of run `my`, accepted (each case once) or
    /// refused, until the accepted cases reach the run's limit
    /// (`limitReached`).
    private func tool(_ my: Int, _ trigger: Board.TriageTrigger, _ e: Assistant.Event) {
        guard my == gen, !limitHit, case .running(_, _, let total) = state else { return }
        switch e.kind {
        case .toolUse where e.tool == Assistant.triageAnnotateTool:
            annotateCalls.insert(e.toolUseID)
        case .toolResult where annotateCalls.contains(e.toolUseID):
            annotateCalls.remove(e.toolUseID)
            guard !e.isError else {
                refused += 1
                return
            }
            // A result that names no case counts as a case of its own.
            let key = Assistant.triageAnnotatedCase(e.resultText) ?? "call:" + e.toolUseID
            guard annotatedCases.insert(key).inserted else { return }
            let done = annotatedCases.count
            state = .running(trigger, done: done, total: total)
            if done >= runLimit {
                limitReached(my, trigger)
            }
        default:
            break
        }
    }

    /// Counts the usage an event of run `my` carries.
    private func counted(_ my: Int, _ e: Assistant.Event) {
        guard my == gen else { return }
        usage.add(e)
    }

    /// The run's limit of accepted notes is reached: the run has succeeded
    /// whatever the model does next. The bridge accepts no more notes and
    /// tells the model to stop, so the request goes on until Claude Code's
    /// result (`answered`), which carries the whole run's usage, for at
    /// most `grace`; then it is cancelled and the run ends with the usage
    /// seen so far, a lower bound.
    private func limitReached(_ my: Int, _ trigger: Board.TriageTrigger) {
        log.info("board triage: the run's limit of \(self.runLimit, privacy: .public) notes is reached")
        limitHit = true
        learnSignedIn(true)
        let grace = grace
        graceTask = Task { [weak self] in
            try? await Task.sleep(for: grace)
            guard !Task.isCancelled, let self, my == self.gen, self.limitHit else { return }
            self.log.info("board triage: no result after the limit in time")
            self.gen += 1
            self.request.cancel()
            let id = self.runID
            self.runID = nil
            self.finish(trigger, nil, run: id)
        }
    }

    /// The request of run `my` ended.
    private func answered(_ my: Int, _ trigger: Board.TriageTrigger, _ outcome: AssistantRequest.Outcome) {
        guard my == gen else { return }
        if limitHit {
            // At its limit the run has succeeded, whatever the result says;
            // its usage was counted before this.
            if outcome == .failed(.notSignedIn) {
                learnSignedIn(false)
            }
            if case .answered = outcome {
                usage.finished()
            }
            let id = runID
            runID = nil
            finish(trigger, nil, run: id)
            return
        }
        var failure: Board.TriageFailure?
        switch outcome {
        case .answered:
            failure = nil
            usage.finished()
            if case .running(_, 0, _) = state {
                if refused > 0 {
                    failure = .notesRefused
                } else if trigger == .automatic, queueHadCases {
                    failure = .noProgress
                }
            }
        case .declined:
            failure = .declined
        case .failed(let f):
            switch f {
            case .notFound: failure = .notFound
            case .notSignedIn: failure = .notSignedIn
            case .toolsMissing: failure = .toolsMissing
            case .limit: failure = .limit
            case .stopped(let reason): failure = reason == AssistantRequest.timedOut ? .timeout : .stopped
            }
        }
        if failure == .notSignedIn {
            learnSignedIn(false)
        } else if case .answered = outcome {
            learnSignedIn(true)
        }
        let id = runID
        runID = nil
        finish(trigger, failure, run: id)
    }

    /// Ends the run with `failure` (nil: a success): the daemon's run is
    /// ended, the board asked again, the state set and the end reported.
    private func finish(_ trigger: Board.TriageTrigger, _ failure: Board.TriageFailure?, run: BoardRunID?) {
        graceTask?.cancel()
        graceTask = nil
        limitHit = false
        let annotated: Int
        if case .running(_, let done, _) = state {
            annotated = done
        } else {
            annotated = 0
        }
        if let run {
            let lowerBound = usage.lowerBound
            endRun(run, failure.map(\.runError), usage: usage.total.map { BoardUsage($0, lowerBound: lowerBound) })
        }
        // The end first, while `state` still says active: the schedule
        // counts the failure before the state's change asks it again.
        endedRun = (trigger, failure)
        ended.notify()
        permitted = false
        if let failure {
            log.info("board triage: ended, \(String(describing: failure), privacy: .public)")
            state = .failed(trigger, failure, at: now())
        } else {
            log.info("board triage: ended, \(annotated, privacy: .public) annotated, \(self.refused, privacy: .public) refused")
            state = .finished(trigger, annotated: annotated, refused: refused, at: now())
        }
    }

    /// A failure before the daemon's run started.
    private func fail(_ my: Int, _ trigger: Board.TriageTrigger, _ failure: Board.TriageFailure) {
        guard my == gen else { return }
        if failure == .notSignedIn {
            learnSignedIn(false)
        }
        finish(trigger, failure, run: nil)
    }

    /// board.runEnd (`usage` nil: not known, left out), then the board
    /// listed again.
    private func endRun(_ id: BoardRunID, _ error: BoardRunError?, usage: BoardUsage? = nil) {
        ending += 1
        nextEnd += 1
        let key = nextEnd
        let client = client
        endTasks[key] = Task { [weak self] in
            do {
                _ = try await client.call(API.BoardRunEnd.self, BoardRunEndParams(runId: id, error: error, usage: usage))
            } catch {
                self?.log.info("board.runEnd: \(String(describing: error), privacy: .public)")
            }
            guard let self else { return }
            self.ending -= 1
            self.endTasks[key] = nil
            self.onRefresh?()
        }
    }
}

/// Resumes `cancelAndEnd`'s wait once.
@MainActor
private final class EndGate {
    var cont: CheckedContinuation<Void, Never>?

    func open() {
        cont?.resume()
        cont = nil
    }
}
