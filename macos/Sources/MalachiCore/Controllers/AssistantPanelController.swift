// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The assistant panel of the main window (ui/internal/assistant, the In
/// App target; GTK ui/internal/assistantpanel `Controller`, a port of
/// this) without its views: the conversation with the user's own Claude
/// Code, restricted to the malachi-mcp tools, as a list of items the panel
/// shows.
///
/// A question goes through these steps, each of which may end it:
///
/// 1. `idle` → `preparing`. The first question ever asks for consent
///    (`consent`, the sheet "Send Mail to Claude?"; the answer is kept in
///    `assistant-consent`). Declined: nothing happens, and the typed text
///    goes back into the field (`onRestoreInput`).
/// 2. The question appears in the transcript (a user item: the action's
///    label and the typed text) and a waiting message action is used up.
///    The first question of a conversation pins its context (see below).
/// 3. The ids of the contexts the prompt names are completed: a folded
///    conversation stands for its newest message until its members are
///    known (`resolveContext`, which the application answers with
///    `ListController.selectedIDs`; after `resolveTimeout` the newest
///    message alone is used).
/// 4. Without a running process: Claude Code is located (none: "Claude
///    Code was not found on this computer"), the bridge must be there
///    (none: the tools are not available), and Claude Code must not say it
///    is signed out (`ClaudeCodeLocator.signedIn`, asked afresh: "Claude
///    Code is not signed in…"; not known counts as signed in, and the
///    process then says what is wrong). Then the process starts
///    (`Assistant.args`, `Assistant.childEnv`, the private directory) and
///    is kept for the follow-up questions of the conversation.
/// 5. `running`: the turn is written to stdin. Its `system/init` must
///    report the bridge connected, or the conversation ends with "The
///    Malachi Mail tools are not available to the assistant". Text deltas
///    stream into the current assistant item, a whole text block replaces
///    it; a tool call is an activity line until its result; a create_draft
///    result whose bridge line names a draft (`Assistant.parseDraftResult`)
///    adds a draft card, whose Open Draft the application checks with
///    draft.list (`openDraft`). The result ends the turn (`idle`); one that
///    is not a success adds "The assistant stopped: …". The process ending
///    during a turn does the same with its stderr's first line.
///
/// `stop()` ends the process and the turn with the note "The conversation
/// was stopped"; the next question starts a new process (a new
/// conversation for Claude). `newConversation()` ends the process and
/// empties the transcript. Errors of steps 4 and 5 offer Try Again, which
/// sends the same question once more. Nothing is kept on disk: Claude
/// Code runs with `--no-session-persistence` in an empty directory, and
/// the transcript lives in memory. Costs are logged as numbers, never mail
/// or model text.
///
/// A conversation keeps its context. Before its first question the chip
/// follows the list's selection (`setContext`, called by the application)
/// unless its remove button (`removeContext`) leaves it at all mail until
/// the next selection change. The first question (a quick action, a
/// waiting action's words, a free question, a menu's action, Summarize
/// Unread in This Folder) pins what the chip showed then: `pinned` starts
/// with it, the chip names it (`Assistant.conversationLabel`), and from
/// then on the selection only decides whether the bar "Another message is
/// selected" shows (`anotherSelected`). Its Add to Conversation
/// (`addSelection`) appends the selection to `pinned`; its New
/// Conversation (`newConversation`) ends the conversation, and the chip
/// follows the selection again. The quick actions act on the newest
/// pinned context; a menu's action (`run(_:on:)`) or an attachment's
/// question on a message that is part of no pinned context adds it first.
///
/// The model hears of each pinned context once per Claude Code process: a
/// free question carries a model-facing line in front of it for every
/// context not yet told (`Assistant.contextPreamble` for the first,
/// `Assistant.addedContextPreamble` for one added later), and an action's
/// own prompt, which names its ids, tells the model of its context. A new
/// process (after Stop, or when Claude Code ended) knows nothing, so the
/// contexts are told again.
///
/// Summarize and Tasks and Deadlines send at once, Draft a Reply… and Ask
/// About This Message… (and an attachment's question) wait for the user's
/// words (`pending`: the field's placeholder changes); Summarize Unread in
/// This Folder sends for a folder.
@MainActor
public final class AssistantPanelController {
    /// One entry of the transcript.
    public struct Item: Sendable, Equatable, Identifiable {
        public let id: Int
        public var content: Content
    }

    public enum Content: Sendable, Equatable {
        /// The user's question: the action's label (may be empty) and the
        /// typed text (may be empty).
        case user(label: String, text: String)
        /// The answer as Markdown source (`Assistant.markdown`), still
        /// arriving while `streaming`.
        case assistant(text: String, streaming: Bool)
        /// A tool at work (`Assistant.activityLabel`), `done` at its result.
        case activity(label: String, done: Bool)
        /// A draft the bridge saved, with Open Draft.
        case draft(Assistant.DraftRef)
        /// What went wrong; `retry` offers Try Again.
        case error(String, retry: Bool)
        /// A remark of the panel's own ("The conversation was stopped").
        case note(String)
    }

    /// What changed in `items`, for the view.
    public enum Change: Sendable, Equatable {
        case reset
        case appended(Int)
        case updated(Int)
    }

    public enum Phase: Sendable, Equatable {
        case idle
        /// Consent, the context, locating and checking Claude Code, the
        /// process starting.
        case preparing
        /// A turn is under way.
        case running
    }

    /// A message action that waits for the user's words.
    public enum Pending: Sendable, Equatable {
        /// Draft a Reply… or Ask About This Message… on the context.
        case action(Assistant.Action)
        /// A question about an attachment (P12).
        case attachment(accountID: String, messageID: String, partID: String)
    }

    /// What the panel works on: the selected message, or a conversation
    /// of `count` messages (`selection` newest first; with `partial` only
    /// its newest message is known yet).
    public struct Context: Sendable, Equatable {
        public var selection: Assistant.Selection
        public var count: Int
        public var partial: Bool
        /// The message's subject, or the conversation's: what the chip
        /// names once a conversation is about it. Mail text, shown as one
        /// line (`Assistant.conversationLabel`), never sent to the model.
        public var subject: String
        /// The conversation (thread) it belongs to; "" when not known.
        public var threadID: String

        public init(
            selection: Assistant.Selection, count: Int = 1, partial: Bool = false, subject: String = "",
            threadID: String = ""
        ) {
            self.selection = selection
            self.count = max(count, selection.messageIDs.count)
            self.partial = partial
            self.subject = subject
            self.threadID = threadID
        }

        public var conversation: Bool { count > 1 }

        /// Whether `other` is part of this context, or this of it: the same
        /// account and a message in common, or, when either is a
        /// conversation, the same thread (a folded conversation knows only
        /// its newest message until its members are resolved).
        public func overlaps(_ other: Context) -> Bool {
            guard !selection.accountID.isEmpty, selection.accountID == other.selection.accountID else { return false }
            if selection.messageIDs.contains(where: { !$0.isEmpty && other.selection.messageIDs.contains($0) }) {
                return true
            }
            return (conversation || other.conversation) && !threadID.isEmpty && threadID == other.threadID
        }
    }

    /// One context of a conversation that keeps its context: what the
    /// chip showed at its first question, or a selection added since.
    public struct Pinned: Sendable, Equatable {
        /// nil for all mail (nothing was selected, or the chip's context
        /// was removed, when the conversation began).
        public internal(set) var context: Context?
        /// The model was told about it since its Claude Code started.
        public internal(set) var announced: Bool
        /// Its identity while its members are resolved; never reused.
        let key: Int
    }

    /// What a message action or an attachment's question is about: a
    /// pinned context, or one the question pins (or adds) when it is sent.
    enum Target: Sendable, Equatable {
        case pinned(Int)
        case context(Context)
    }

    /// One question, as sent and as retried.
    struct Request: Sendable, Equatable {
        enum Kind: Sendable, Equatable {
            case free
            case action(Assistant.Action)
            case unread(accountID: String, folderID: String)
            case attachment(accountID: String, messageID: String, partID: String)
        }

        var kind: Kind
        var label: String
        var text: String
        /// What the chip showed when it was asked: what the conversation is
        /// about when this question is its first.
        var inEffect: Context?
        /// What a message action or an attachment's question is about.
        var target: Target?
    }

    /// How long the members of a folded conversation are waited for.
    public nonisolated static let defaultResolveTimeout: Duration = .seconds(10)

    // MARK: Dependencies

    public let settings: Settings
    public let locator: ClaudeCodeLocator
    private let bridge: String?
    private let socket: String
    private let directory: URL
    private let environment: [String: String]
    private let killGrace: Duration
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")

    // MARK: Hooks

    /// Asks the user before the first question ever; true allows.
    /// Without a hook nothing is ever sent.
    public var consent: (@MainActor () async -> Bool)?
    /// Completes a partial context (a folded conversation's members,
    /// newest first); `done` should be called once, and the context's own
    /// selection is used when it is not called within `resolveTimeout`.
    public var resolveContext: (@MainActor (Context, @escaping @MainActor (Assistant.Selection) -> Void) -> Void)?
    /// Open Draft of a draft card (`ActionsController.openSavedDraft`).
    public var openDraft: (@MainActor (Assistant.DraftRef) -> Void)?
    /// `items` changed.
    public var onChange: (@MainActor (Change) -> Void)?
    /// The phase, the context, the waiting action or the model changed.
    public var onState: (@MainActor () -> Void)?
    /// The question field should take the keyboard (a message action waits
    /// for the user's words).
    public var onFocusInput: (@MainActor () -> Void)?
    /// A question that was not sent (consent declined): its text goes back
    /// into the field.
    public var onRestoreInput: (@MainActor (String) -> Void)?
    /// The date for the system prompt, YYYY-MM-DD.
    public var today: @MainActor () -> String = AssistantPanelController.localDate
    /// The UI language's English name for the system prompt.
    public var language: @MainActor () -> String = { Assistant.languageName(L10n.catalogue.language) }
    /// How long `resolveContext` is waited for.
    public var resolveTimeout: Duration = AssistantPanelController.defaultResolveTimeout

    // MARK: State

    public private(set) var items: [Item] = []
    public private(set) var phase: Phase = .idle
    /// The list's selection, as the application last set it: the chip's
    /// context before the conversation's first question, compared with
    /// `pinned` after it.
    public private(set) var context: Context?
    /// The chip's remove button: all mail until the selection changes.
    public private(set) var contextRemoved = false
    /// What the conversation is about, in the order it was pinned; empty
    /// until its first question.
    public private(set) var pinned: [Pinned] = []
    /// The message action that waits for the user's words.
    public private(set) var pending: Pending?
    /// What the waiting action acts on; nil for the chip's context when
    /// its words are sent (before the first question).
    private var pendingTarget: Target?
    /// The conversation's Claude Code, kept between the questions.
    public private(set) var process: ClaudeCodeProcess?
    /// Nothing runs any more (the window went).
    public private(set) var closed = false

    /// Bumped by every question, `stop`, `newConversation` and `close`:
    /// the steps of an older question stop at their next `await`.
    private var gen = 0
    private var nextID = 0
    /// The assistant item that deltas stream into.
    private var streaming: Int?
    /// The activity items by tool call id, and each call's tool.
    private var activities: [String: Int] = [:]
    private var toolNames: [String: String] = [:]
    /// The question of the turn under way (or the last that failed), for
    /// Try Again.
    private var lastRequest: Request?
    private var settingsToken: Settings.ChangeToken?
    /// The next `Pinned.key`.
    private var nextKey = 0
    /// The pinned folded conversations whose members are being asked for.
    private var resolving: [Int: Task<Void, Never>] = [:]

    /// - Parameters:
    ///   - settings: the model, the consent, Claude Code's path.
    ///   - locator: finds and asks Claude Code.
    ///   - bridge: `malachi-mcp` beside the application, nil without.
    ///   - socket: the daemon's socket, for the bridge (`Paths.socket`).
    ///   - directory: Claude Code's working directory (empty, private).
    ///   - environment: the application's environment, filtered by
    ///     `Assistant.childEnv`.
    ///   - killGrace: SIGTERM to SIGKILL (tests shorten it).
    public init(
        settings: Settings, locator: ClaudeCodeLocator, bridge: String?, socket: String,
        directory: URL = ClaudeCodeLocator.defaultDirectory,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        killGrace: Duration = ClaudeCodeProcess.defaultKillGrace
    ) {
        self.settings = settings
        self.locator = locator
        self.bridge = bridge
        self.socket = socket
        self.directory = directory
        self.environment = environment
        self.killGrace = killGrace
        settingsToken = settings.onChange(.assistantModel) { [weak self] in
            self?.onState?()
        }
    }

    /// Ends the conversation for good (the application quits).
    public func close() {
        guard !closed else { return }
        closed = true
        gen += 1
        process?.terminate()
        process = nil
        settingsToken?.cancel()
        settingsToken = nil
    }

    // MARK: Reading

    public var running: Bool { phase != .idle }

    /// The conversation keeps its context: its first question was asked.
    public var isPinned: Bool { !pinned.isEmpty }

    /// The chip's context before the conversation's first question: the
    /// list's, unless removed.
    public var effectiveContext: Context? {
        contextRemoved ? nil : context
    }

    /// The chip's text: the selection it follows (`Assistant.contextLabel`)
    /// until the conversation's first question, then what the
    /// conversation is about (`pinnedLabel`).
    public var contextLabel: String {
        isPinned ? Self.pinnedLabel(pinned.map(\.context)) : Assistant.contextLabel(effectiveContext?.count ?? 0)
    }

    /// Whether the bar "Another message is selected" shows: the
    /// conversation keeps its context and the list's selection is part of
    /// none of it.
    public var anotherSelected: Bool {
        guard isPinned, !closed, let c = context else { return false }
        return !pinned.contains { $0.context?.overlaps(c) == true }
    }

    /// Whether the quick actions (Summarize, Draft a Reply…, Tasks and
    /// Deadlines) can run: nothing under way, and something to act on.
    public var canRunActions: Bool {
        phase == .idle && !closed && quickTarget != nil
    }

    /// What the quick actions act on: the chip's context before the
    /// conversation's first question, the newest pinned context after it
    /// (none when that is all mail).
    private var quickTarget: Target? {
        guard isPinned else { return effectiveContext.map { .context($0) } }
        guard let p = pinned.last, let c = p.context, !c.selection.messageIDs.isEmpty else { return nil }
        return .pinned(p.key)
    }

    /// The chip's text for what a conversation is about (the contexts
    /// in `pinned` order, nil for all mail): one context its subject
    /// (without one, the message or the conversation's count), all mail
    /// "All mail", several the number of their messages, each counted
    /// once (`Assistant.conversationLabel`).
    static func pinnedLabel(_ contexts: [Context?]) -> String {
        let mail = contexts.compactMap { $0 }.filter { !$0.selection.messageIDs.isEmpty }
        if contexts.count == 1 {
            return mail.first.map { subjectLabel($0) } ?? Assistant.contextLabel(0)
        }
        let n = messageCount(mail)
        switch n {
        case 0: return Assistant.contextLabel(0)
        case 1: return subjectLabel(mail[0])
        default: return Assistant.conversationLabel(subject: "", messages: n)
        }
    }

    /// One context's chip: its subject; without one, the message or the
    /// conversation's count.
    private static func subjectLabel(_ c: Context) -> String {
        let hasSubject = !Assistant.oneLine(c.subject, limit: Assistant.maxSubject).isEmpty
        return Assistant.conversationLabel(subject: c.subject, messages: hasSubject ? 1 : c.count)
    }

    /// The messages of `contexts`, each counted once; a folded
    /// conversation's members that are not known yet count as its count
    /// says.
    static func messageCount(_ contexts: [Context]) -> Int {
        var seen = Set<[String]>()
        var unknown = 0
        for c in contexts {
            let ids = c.selection.messageIDs.filter { !$0.isEmpty }
            for id in ids {
                seen.insert([c.selection.accountID, id])
            }
            if c.partial {
                unknown += max(0, c.count - ids.count)
            }
        }
        return seen.count + unknown
    }

    /// The question field's placeholder for the waiting action.
    public var placeholder: String {
        let t = Assistant.panelTexts()
        switch pending {
        case .action(.draftReply)?: return t.replyPlaceholder
        case .action?, .attachment?: return t.askPlaceholder
        case nil: return t.placeholder
        }
    }

    /// The label over the question field while an action waits.
    public var pendingLabel: String {
        switch pending {
        case .action(let a)?: return Assistant.label(a)
        case .attachment?: return Assistant.texts().askFile
        case nil: return ""
        }
    }

    /// The panel's subtitle: "Claude Code · Sonnet".
    public var subtitle: String {
        Assistant.targetName(.code) + " · " + Assistant.modelName(settings.assistantModel)
    }

    // MARK: The context

    /// The list's selection changed: before the conversation's first
    /// question the chip follows it (and a removed context comes back);
    /// after it, the bar "Another message is selected" may come or go.
    public func setContext(_ c: Context?) {
        guard c != context || contextRemoved else { return }
        context = c
        contextRemoved = false
        if !isPinned, c == nil, case .action? = pending {
            pending = nil
            pendingTarget = nil
        }
        onState?()
    }

    /// The chip's remove button: all mail until the next selection. Only
    /// before the conversation's first question.
    public func removeContext() {
        guard !isPinned, !contextRemoved, context != nil else { return }
        contextRemoved = true
        if case .action? = pending {
            pending = nil
            pendingTarget = nil
        }
        onState?()
    }

    /// The bar's Add to Conversation: the list's selection joins what the
    /// conversation is about; the next free question tells the model
    /// (`Assistant.addedContextPreamble`).
    public func addSelection() {
        guard !closed, anotherSelected, let c = context else { return }
        add(c)
        onState?()
    }

    /// Appends `c` to what the conversation is about; a folded
    /// conversation's members are asked for at once, while the list still
    /// shows it selected.
    @discardableResult
    private func add(_ c: Context) -> Int {
        let key = newKey()
        pinned.append(Pinned(context: c, announced: false, key: key))
        if c.partial {
            _ = resolvePinned(key)
        }
        return key
    }

    /// The newest pinned context `c` is part of.
    private func pinnedKey(of c: Context) -> Int? {
        pinned.last { $0.context?.overlaps(c) == true }?.key
    }

    private func pinnedContext(_ key: Int) -> Context? {
        pinned.first { $0.key == key }?.context
    }

    private func newKey() -> Int {
        defer { nextKey += 1 }
        return nextKey
    }

    // MARK: Questions

    /// A quick action (the panel's buttons) on the chip's context, or,
    /// once the conversation keeps its context, on its newest pinned
    /// context: Summarize and Tasks and Deadlines send at once, Draft a
    /// Reply… and Ask About This Message… wait for the user's words.
    /// Nothing while a question is under way or without anything to act
    /// on.
    public func run(_ a: Assistant.Action) {
        guard !closed, phase == .idle, Assistant.messageActions.contains(a), let target = quickTarget else { return }
        perform(a, on: target)
    }

    /// A message action of a menu (the Assistant menu, the Message menu, a
    /// message window) on `c`, the selection or the window's message.
    /// Before the conversation's first question the chip takes `c` and the
    /// action runs on it; after it, the action runs on the pinned context
    /// `c` is part of, and a `c` that is part of none is added first (the
    /// action's prompt names its ids, so the model needs no other word of
    /// it).
    public func run(_ a: Assistant.Action, on c: Context) {
        guard !closed, phase == .idle, Assistant.messageActions.contains(a) else { return }
        guard isPinned else {
            setContext(c)
            run(a)
            return
        }
        perform(a, on: .pinned(pinnedKey(of: c) ?? add(c)))
    }

    private func perform(_ a: Assistant.Action, on target: Target) {
        switch a {
        case .summarize, .tasks:
            start(Request(kind: .action(a), label: Assistant.label(a), text: "", inEffect: effectiveContext, target: target))
        default:
            pending = .action(a)
            pendingTarget = isPinned ? target : nil
            onState?()
            onFocusInput?()
        }
    }

    /// Summarize Unread in This Folder, for a folder: sent at once.
    public func summarizeUnread(accountID: String, folderID: String) {
        guard !closed, phase == .idle, !accountID.isEmpty, !folderID.isEmpty else { return }
        start(Request(
            kind: .unread(accountID: accountID, folderID: folderID), label: Assistant.label(.unread), text: "",
            inEffect: effectiveContext, target: nil))
    }

    /// An attachment's question: waits for the user's words. Once the
    /// conversation keeps its context, the attachment's message is added
    /// to it when it is part of none of it (`subject` and `threadID` are
    /// the message's).
    public func askAttachment(
        accountID: String, messageID: String, partID: String, subject: String = "", threadID: String = ""
    ) {
        guard !closed, phase == .idle, !accountID.isEmpty, !messageID.isEmpty, !partID.isEmpty else { return }
        let c = Context(
            selection: Assistant.Selection(accountID: accountID, messageIDs: [messageID]), subject: subject,
            threadID: threadID)
        pending = .attachment(accountID: accountID, messageID: messageID, partID: partID)
        pendingTarget = isPinned ? .pinned(pinnedKey(of: c) ?? add(c)) : .context(c)
        onState?()
        onFocusInput?()
    }

    /// Drops the waiting action (a context it added stays).
    public func cancelPending() {
        guard pending != nil else { return }
        pending = nil
        pendingTarget = nil
        onState?()
    }

    /// The question field's Send: with a waiting action its prompt and the
    /// words, otherwise a free question. False when nothing was taken
    /// (empty, or a question under way); the field keeps its text then.
    @discardableResult
    public func submit(_ text: String) -> Bool {
        guard !closed, phase == .idle else { return false }
        let words = text.trimmingCharacters(in: .whitespacesAndNewlines)
        switch pending {
        case .action(let a)?:
            // A reply may be drafted without instructions.
            guard !words.isEmpty || a == .draftReply,
                  let target = pendingTarget ?? effectiveContext.map({ .context($0) }) else { return false }
            start(Request(kind: .action(a), label: Assistant.label(a), text: words, inEffect: effectiveContext, target: target))
        case .attachment(let acc, let msg, let part)?:
            guard !words.isEmpty else { return false }
            start(Request(
                kind: .attachment(accountID: acc, messageID: msg, partID: part), label: Assistant.texts().askFile,
                text: words, inEffect: effectiveContext, target: pendingTarget))
        case nil:
            guard !words.isEmpty else { return false }
            start(Request(kind: .free, label: "", text: words, inEffect: effectiveContext, target: nil))
        }
        return true
    }

    /// Try Again on an error item: the same question once more.
    public func retry(_ itemID: Int) {
        guard !closed, phase == .idle, let req = lastRequest,
              let idx = items.firstIndex(where: { $0.id == itemID }),
              case .error(let text, true) = items[idx].content else { return }
        items[idx].content = .error(text, retry: false)
        onChange?(.updated(idx))
        start(req, echo: false)
    }

    /// A draft card's Open Draft.
    public func openDraft(_ itemID: Int) {
        guard let item = items.first(where: { $0.id == itemID }), case .draft(let ref) = item.content else { return }
        openDraft?(ref)
    }

    /// Stop: ends the process and the turn under way. The conversation
    /// keeps its context; the next question starts a new process, which is
    /// told the context again.
    public func stop() {
        guard phase != .idle else { return }
        gen += 1
        endProcess()
        closeTurn()
        phase = .idle
        append(.note(Assistant.panelTexts().stopped))
        onState?()
    }

    /// New Conversation (the header's button and the bar's): ends the
    /// process, empties the transcript and forgets what the conversation
    /// was about; the chip follows the selection again.
    public func newConversation() {
        gen += 1
        endProcess()
        items = []
        streaming = nil
        activities = [:]
        toolNames = [:]
        pending = nil
        pendingTarget = nil
        lastRequest = nil
        pinned = []
        resolving = [:]
        contextRemoved = false
        phase = .idle
        onChange?(.reset)
        onState?()
    }

    // MARK: Sending

    private func start(_ req: Request, echo: Bool = true) {
        guard !closed, phase == .idle else { return }
        gen += 1
        let my = gen
        phase = .preparing
        onState?()
        Task { @MainActor [weak self] in
            await self?.prepare(req, my, echo: echo)
        }
    }

    private func prepare(_ req: Request, _ my: Int, echo: Bool) async {
        // 1. Consent, once ever.
        if !settings.assistantConsent {
            let allowed = await consent?() ?? false
            guard my == gen else { return }
            guard allowed else {
                phase = .idle
                onState?()
                if !req.text.isEmpty {
                    onRestoreInput?(req.text)
                }
                return
            }
            settings.assistantConsent = true
        }
        // 2. The question in the transcript; the conversation's first
        // question pins what the chip showed, and what the question is
        // about joins it when it is part of none of it; a waiting action is
        // used up.
        var req = req
        if pinned.isEmpty {
            let key = newKey()
            pinned = [Pinned(context: req.inEffect, announced: false, key: key)]
            if req.inEffect?.partial == true {
                _ = resolvePinned(key)
            }
        }
        if case .context(let c)? = req.target {
            req.target = .pinned(pinnedKey(of: c) ?? add(c))
        }
        lastRequest = req
        clearRetries()
        if echo {
            append(.user(label: req.label, text: req.text))
        }
        pending = nil
        pendingTarget = nil
        onState?()
        // 3. What the model is told: a new Claude Code knows nothing of
        // the conversation yet; the contexts the prompt names have their
        // members first.
        if process?.running != true {
            for i in pinned.indices {
                pinned[i].announced = false
            }
        }
        for key in unresolved(req) {
            await resolvePinned(key).value
            guard my == gen else { return }
        }
        let prompt: String
        let told: [Int]
        do {
            (prompt, told) = try self.prompt(req)
        } catch {
            log.warning("assistant prompt: \(String(describing: error), privacy: .public)")
            fail(Assistant.stoppedText(String(describing: error)), retry: false)
            return
        }
        // 4. Claude Code, started when the conversation has none.
        if process?.running != true {
            process = nil
            guard let path = locator.locate() else {
                fail(Assistant.panelTexts().notFound, retry: true)
                return
            }
            guard let bridge else {
                fail(Assistant.panelTexts().toolsMissing, retry: false)
                return
            }
            locator.refresh()
            let signedIn = await locator.signedIn()
            guard my == gen else { return }
            if signedIn == false {
                fail(Assistant.panelTexts().notSignedIn, retry: true)
                return
            }
            do {
                process = try launch(path, bridge)
            } catch {
                log.warning("assistant: \(String(describing: error), privacy: .public)")
                fail(Assistant.stoppedText(String(describing: error)), retry: true)
                return
            }
        }
        // 5. The turn.
        guard let process, process.send(Assistant.userMessage(prompt)) else {
            fail(Assistant.stoppedText("claude is not running"), retry: true)
            return
        }
        for i in pinned.indices where told.contains(pinned[i].key) {
            pinned[i].announced = true
        }
        phase = .running
        onState?()
    }

    /// The prompt of a question and the keys of the pinned contexts it
    /// tells the model about. A free question carries a line for every
    /// context not told yet (`Assistant.contextPreamble` for the
    /// conversation's first, `Assistant.addedContextPreamble` for one added
    /// later); a message action names its context's ids, and an
    /// attachment's question its message's.
    private func prompt(_ req: Request) throws -> (String, [Int]) {
        switch req.kind {
        case .free:
            var lines: [String] = []
            var told: [Int] = []
            for (i, p) in pinned.enumerated() where !p.announced {
                told.append(p.key)
                guard let s = p.context?.selection else { continue }
                let line = i == 0 ? Assistant.contextPreamble(s) : Assistant.addedContextPreamble(s)
                if !line.isEmpty {
                    lines.append(line)
                }
            }
            let preamble = lines.joined(separator: "\n")
            return (preamble.isEmpty ? req.text : preamble + "\n\n" + req.text, told)
        case .action(let a):
            guard case .pinned(let key)? = req.target, let c = pinnedContext(key) else {
                throw Assistant.Failure.noMessages
            }
            return (try Assistant.prompt(.app, a, c.selection) + req.text, [key])
        case .unread(let acc, let folder):
            return (try Assistant.unreadPrompt(accountID: acc, folderID: folder), [])
        case .attachment(let acc, let msg, let part):
            let p = try Assistant.attachmentPrompt(accountID: acc, messageID: msg, partID: part) + req.text
            if case .pinned(let key)? = req.target,
               pinnedContext(key)?.selection == Assistant.Selection(accountID: acc, messageIDs: [msg]) {
                return (p, [key])
            }
            return (p, [])
        }
    }

    /// The pinned folded conversations whose members the prompt of `req`
    /// needs: every one a free question tells the model about, a message
    /// action's own.
    private func unresolved(_ req: Request) -> [Int] {
        let keys: [Int]
        switch req.kind {
        case .free:
            keys = pinned.filter { !$0.announced }.map(\.key)
        case .action:
            guard case .pinned(let key)? = req.target else { return [] }
            keys = [key]
        case .unread, .attachment:
            return []
        }
        return keys.filter { pinnedContext($0)?.partial == true }
    }

    /// Asks once for the members of a pinned folded conversation (the
    /// application answers while the list still shows it selected, and
    /// with its own selection otherwise); a question that needs them waits
    /// for the same answer.
    private func resolvePinned(_ key: Int) -> Task<Void, Never> {
        if let t = resolving[key] {
            return t
        }
        let t = Task { @MainActor [weak self] in
            guard let self, let c = self.pinnedContext(key), c.partial else { return }
            let sel = await self.resolve(c)
            self.resolving[key] = nil
            self.settle(key, sel)
        }
        resolving[key] = t
        return t
    }

    /// A pinned folded conversation's members arrived.
    private func settle(_ key: Int, _ sel: Assistant.Selection) {
        guard let i = pinned.firstIndex(where: { $0.key == key }), var c = pinned[i].context, c.partial,
              !sel.messageIDs.isEmpty, sel != c.selection else { return }
        c.selection = sel
        c.count = sel.messageIDs.count
        c.partial = false
        pinned[i].context = c
        onState?()
    }

    /// The context's selection, its members asked for when partial.
    private func resolve(_ c: Context) async -> Assistant.Selection {
        guard c.partial, let resolveContext else { return c.selection }
        return await withCheckedContinuation { (cont: CheckedContinuation<Assistant.Selection, Never>) in
            let once = Once()
            resolveContext(c) { sel in
                if once.fire() {
                    cont.resume(returning: sel.messageIDs.isEmpty ? c.selection : sel)
                }
            }
            let timeout = resolveTimeout
            Task { @MainActor in
                try? await Task.sleep(for: timeout)
                if once.fire() {
                    cont.resume(returning: c.selection)
                }
            }
        }
    }

    /// Starts Claude Code for a new conversation.
    private func launch(_ path: String, _ bridge: String) throws -> ClaudeCodeProcess {
        try FileManager.default.createDirectory(
            at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let options = Assistant.Options(
            bridge: bridge, socket: socket, model: settings.assistantModel,
            systemPrompt: Assistant.systemPrompt(language: language(), today: today()))
        let p = ClaudeCodeProcess(
            executable: path, arguments: Assistant.args(options),
            environment: Assistant.childEnvironment(environment, claudePath: path), directory: directory,
            killGrace: killGrace)
        p.onEvents = { [weak self, weak p] events in
            guard let self, let p, p === self.process else { return }
            self.handle(events)
        }
        p.onExit = { [weak self, weak p] exit in
            guard let self, let p, p === self.process else { return }
            self.exited(exit)
        }
        try p.start()
        return p
    }

    // MARK: The stream

    private func handle(_ events: [Assistant.Event]) {
        for e in events {
            guard process != nil else { return }
            switch e.kind {
            case .systemInit:
                guard e.bridgeConnected else {
                    log.warning("assistant: the malachi MCP server is not connected")
                    endProcess()
                    fail(Assistant.panelTexts().toolsMissing, retry: false)
                    return
                }
            case .textDelta:
                if let idx = streaming, case .assistant(let text, _) = items[idx].content {
                    items[idx].content = .assistant(text: text + e.text, streaming: true)
                    onChange?(.updated(idx))
                } else if !e.text.isEmpty {
                    streaming = append(.assistant(text: e.text, streaming: true))
                }
            case .text:
                if let idx = streaming, case .assistant(let text, _) = items[idx].content {
                    items[idx].content = .assistant(text: e.text.isEmpty ? text : e.text, streaming: false)
                    streaming = nil
                    onChange?(.updated(idx))
                } else if !e.text.isEmpty {
                    append(.assistant(text: e.text, streaming: false))
                }
            case .toolUse:
                closeStreaming()
                let idx = append(.activity(label: Assistant.activityLabel(e.tool), done: false))
                if !e.toolUseID.isEmpty {
                    activities[e.toolUseID] = idx
                    toolNames[e.toolUseID] = e.tool
                }
            case .toolResult:
                if let idx = activities.removeValue(forKey: e.toolUseID), case .activity(let label, false) = items[idx].content {
                    items[idx].content = .activity(label: label, done: true)
                    onChange?(.updated(idx))
                }
                let tool = toolNames.removeValue(forKey: e.toolUseID)
                if tool == "create_draft", !e.isError, let ref = Assistant.parseDraftResult(e.resultText) {
                    append(.draft(ref))
                }
            case .result:
                log.info("assistant turn: success \(e.success, privacy: .public), cost \(e.costUSD, privacy: .public) USD, \(e.denied.count, privacy: .public) denied")
                for tool in e.denied {
                    log.info("assistant: denied \(tool, privacy: .public)")
                }
                closeTurn()
                phase = .idle
                if !e.success {
                    append(.error(Assistant.stoppedText(e.resultText), retry: true))
                } else {
                    lastRequest = nil
                }
                onState?()
            case .other:
                continue
            }
        }
    }

    /// The process ended: during a turn that is an error with its reason;
    /// between turns (or while the next question is being prepared) the
    /// next question starts a new one.
    private func exited(_ exit: ClaudeCodeProcess.Exit) {
        process = nil
        log.info("assistant: claude ended with status \(exit.status, privacy: .public)")
        guard phase == .running else { return }
        closeTurn()
        phase = .idle
        append(.error(Assistant.stoppedText(exit.description), retry: true))
        onState?()
    }

    /// Ends the turn with an error line.
    private func fail(_ text: String, retry: Bool) {
        closeTurn()
        phase = .idle
        append(.error(text, retry: retry))
        onState?()
    }

    /// Try Again belongs to the last question only.
    private func clearRetries() {
        for (idx, item) in items.enumerated() {
            if case .error(let text, true) = item.content {
                items[idx].content = .error(text, retry: false)
                onChange?(.updated(idx))
            }
        }
    }

    /// Terminates the conversation's process; its end is not reported.
    private func endProcess() {
        let p = process
        process = nil
        p?.terminate()
    }

    /// Nothing streams any more and every activity is over.
    private func closeTurn() {
        closeStreaming()
        for idx in activities.values.sorted() {
            if case .activity(let label, false) = items[idx].content {
                items[idx].content = .activity(label: label, done: true)
                onChange?(.updated(idx))
            }
        }
        activities = [:]
        toolNames = [:]
    }

    private func closeStreaming() {
        guard let idx = streaming else { return }
        streaming = nil
        if case .assistant(let text, true) = items[idx].content {
            items[idx].content = .assistant(text: text, streaming: false)
            onChange?(.updated(idx))
        }
    }

    @discardableResult
    private func append(_ c: Content) -> Int {
        items.append(Item(id: nextID, content: c))
        nextID += 1
        let idx = items.count - 1
        onChange?(.appended(idx))
        return idx
    }

    /// Today in the user's time zone, YYYY-MM-DD.
    public static func localDate() -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = Calendar(identifier: .gregorian)
        f.timeZone = .current
        f.dateFormat = "yyyy-MM-dd"
        return f.string(from: Date())
    }
}

/// A flag that fires once.
@MainActor
private final class Once {
    private var fired = false

    func fire() -> Bool {
        guard !fired else { return false }
        fired = true
        return true
    }
}
