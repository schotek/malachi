// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's input: the cases a source hands over (a conversation or an
// issue with the state it is in, what the assistant made of it and what
// the user decided), the user's commitments the assistant found, and the
// cleaning every string of them goes through before it reaches a view
// model. A case is mail, so every string here is hostile input: the view
// model shows only what `cleanLine` and `cleanBlock` let through.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board with the board itself (no msgids yet).

import Foundation

extension Board {
    /// A case on the board: a conversation or an issue. Opaque.
    public struct CaseID: OpaqueID {
        public let rawValue: String
        public init(rawValue: String) { self.rawValue = rawValue }
    }

    /// Where a case stands. `allCases` is the display order: the sections,
    /// the columns and the tiles follow it.
    public enum State: String, Sendable, CaseIterable {
        /// Needs the user now (a deadline, an escalation).
        case hot
        /// Waits for the user's answer.
        case you
        /// The user waits for someone else.
        case them
        /// Nothing to do; for reading.
        case info
    }

    /// Who decided a case's state (`stateSource(of:annotated:)`).
    public enum StateSource: Sendable, Equatable {
        /// The daemon's rules; the assistant has not looked at the case.
        case rules
        /// The rules, and the assistant agreed.
        case assistantKept
        /// The assistant changed the rules' state `from`.
        case assistantChanged(from: State)
        /// The user moved the case.
        case user
        /// The rules; the assistant is off.
        case assistantOff
    }

    /// An account a case belongs to, as the board names it.
    public struct AccountInfo: Sendable, Equatable, Identifiable {
        public var id: AccountID
        public var name: String
        /// The kind capsule ("JIRA", "M365"; `FolderTree.accountHeaderBadge`).
        public var badge: String
        /// A reply can be written in it: a mail account's reply, an issue
        /// tracker's comment (`Capability.reply` or `.comment`).
        public var canReply: Bool

        public init(id: AccountID, name: String, badge: String, canReply: Bool = true) {
            self.id = id
            self.name = name
            self.badge = badge
            self.canReply = canReply
        }
    }

    /// The issue behind a case of a Jira account.
    public struct IssueInfo: Sendable, Equatable {
        public var key: String
        public var status: String
        public var style: Jira.StatusStyle

        public init(key: String, status: String, style: Jira.StatusStyle) {
            self.key = key
            self.status = status
            self.style = style
        }
    }

    /// The message a reply to a case answers and the folder it is in
    /// (`replyMessageId`, `replyFolderId`): what Reply opens and Show in
    /// Mail selects. On a Jira account the reply is a comment.
    public struct ReplyTarget: Sendable, Equatable {
        public var message: MessageID
        public var folder: FolderID

        public init(message: MessageID, folder: FolderID) {
            self.message = message
            self.folder = folder
        }
    }

    /// Where a case is listed.
    public enum Visibility: Sendable, Equatable {
        /// On the board.
        case live
        /// The user marked it done (when, if the source knows).
        case done(at: Date?)
        /// Hidden until `until`, then live again (the source reports that).
        case snoozed(until: Date)

        public var isLive: Bool { self == .live }

        public var isDone: Bool {
            if case .done = self { return true }
            return false
        }

        /// The remind date while snoozed.
        public var remindAt: Date? {
            if case .snoozed(let d) = self { return d }
            return nil
        }
    }

    /// One message of a case's conversation (`board.get`).
    public struct CaseMessage: Sendable, Equatable {
        /// nil for the invented samples.
        public var id: MessageID?
        public var folder: FolderID?
        public var from: String
        public var date: Date
        /// Plain text, never markup.
        public var text: String
        /// The user wrote it.
        public var mine: Bool
        /// The quoted history, the signature or the rest past the cap was
        /// cut off `text`.
        public var trimmed: Bool

        public init(
            id: MessageID? = nil, folder: FolderID? = nil, from: String, date: Date, text: String,
            mine: Bool = false, trimmed: Bool = false
        ) {
            self.id = id
            self.folder = folder
            self.from = from
            self.date = date
            self.text = text
            self.mine = mine
            self.trimmed = trimmed
        }
    }

    /// What the assistant made of a case. Text an assistant wrote: shown
    /// only as plain text and always as the assistant's.
    public struct Annotation: Sendable, Equatable {
        /// nil: the assistant left the state to the rules.
        public var state: State?
        /// A short title of its own; the subject otherwise.
        public var title: String
        public var summary: String
        /// Why the case is where it is.
        public var why: String
        public var due: Date?
        /// The sentence the due date comes from.
        public var dueQuote: String
        /// The message the sentence is in.
        public var dueMessage: MessageID?
        public var tasks: [String]
        /// Names the assistant (a model name); "" when unknown.
        public var source: String
        /// When it was made; nil for the samples.
        public var at: Date?
        /// A message was added, removed or got its body since: none of it
        /// counts (`annotation(of:annotated:)`), only the case's draft stays.
        public var stale: Bool

        public init(
            state: State?, title: String, summary: String = "", why: String = "", due: Date? = nil,
            dueQuote: String = "", dueMessage: MessageID? = nil, tasks: [String] = [], source: String = "",
            at: Date? = nil, stale: Bool = false
        ) {
            self.state = state
            self.title = title
            self.summary = summary
            self.why = why
            self.due = due
            self.dueQuote = dueQuote
            self.dueMessage = dueMessage
            self.tasks = tasks
            self.source = source
            self.at = at
            self.stale = stale
        }
    }

    /// The suggested reply an annotation linked to a case: a real draft
    /// of the case's account, never sent by itself. It outlives a stale
    /// annotation.
    public struct DraftLink: Sendable, Equatable {
        public var id: DraftID
        /// Its plain text.
        public var text: String

        public init(id: DraftID, text: String) {
            self.id = id
            self.text = text
        }
    }

    /// A case as the source has it.
    public struct Case: Sendable, Equatable, Identifiable {
        public var id: CaseID
        public var account: AccountID
        /// The case's thread now (a merge can change it, `id` stays); nil
        /// for the samples.
        public var thread: ThreadID?
        /// The other party (the sender, the reporter).
        public var person: String
        /// The latest activity.
        public var date: Date
        public var subject: String
        public var snippet: String
        public var unread: Bool
        public var hasAttachments: Bool
        public var messageCount: Int
        public var issue: IssueInfo?
        /// The daemon's rules' state and the code of the rule
        /// (`Text.reason(_:)`; an open set).
        public var ruleState: State
        public var ruleReason: BoardReason
        public var annotation: Annotation?
        /// The user's own choice; nil = automatic.
        public var userState: State?
        public var visibility: Visibility
        /// When a remind of the user's came due (`board.list` `remindedAt`):
        /// the case is live again and listed first in its state, marked
        /// Reminded, until the user acts on it or new mail comes; nil
        /// otherwise.
        public var remindedAt: Date?
        /// What a reply answers; nil for the samples.
        public var reply: ReplyTarget?
        /// The newest message that counts; nil for the samples.
        public var latestMessage: MessageID?
        /// Archive would move messages (else it only marks the case done).
        public var canArchive: Bool
        public var draft: DraftLink?
        /// The conversation, oldest first; nil until loaded
        /// (`BoardSource.loadMessages`).
        public var messages: [CaseMessage]?
        /// Loading the conversation failed (and nothing was loaded before).
        public var messagesFailed: Bool
        /// Changes whenever the case does (the daemon's `version`).
        public var version: Int64

        public init(
            id: CaseID, account: AccountID, thread: ThreadID? = nil, person: String, date: Date, subject: String,
            snippet: String = "", unread: Bool = false, hasAttachments: Bool = false, messageCount: Int = 1,
            issue: IssueInfo? = nil, ruleState: State, ruleReason: BoardReason = "", annotation: Annotation? = nil,
            userState: State? = nil, visibility: Visibility = .live, remindedAt: Date? = nil,
            reply: ReplyTarget? = nil,
            latestMessage: MessageID? = nil, canArchive: Bool = false, draft: DraftLink? = nil,
            messages: [CaseMessage]? = nil, messagesFailed: Bool = false, version: Int64 = 0
        ) {
            self.id = id
            self.account = account
            self.thread = thread
            self.person = person
            self.date = date
            self.subject = subject
            self.snippet = snippet
            self.unread = unread
            self.hasAttachments = hasAttachments
            self.messageCount = messageCount
            self.issue = issue
            self.ruleState = ruleState
            self.ruleReason = ruleReason
            self.annotation = annotation
            self.userState = userState
            self.visibility = visibility
            self.remindedAt = remindedAt
            self.reply = reply
            self.latestMessage = latestMessage
            self.canArchive = canArchive
            self.draft = draft
            self.messages = messages
            self.messagesFailed = messagesFailed
            self.version = version
        }

        /// A live case back from a reminder (`remindedAt`).
        public var reminded: Bool { remindedAt != nil && visibility.isLive }

        /// A case whose newest message is addressed to the user by someone
        /// the user never wrote to (`you.newContact`).
        public var newContact: Bool { ruleReason == .youNewContact }

        /// Marked done. Setting it moves the case to done (when unknown) or
        /// back on the board.
        public var done: Bool {
            get { visibility.isDone }
            set {
                guard newValue != visibility.isDone else { return }
                visibility = newValue ? .done(at: nil) : .live
            }
        }
    }

    /// Where a commitment stands.
    public enum CommitmentState: Sendable, Equatable {
        case open
        /// The user ticked it off.
        case done
        /// The daemon closed it (the user replied, the case was done).
        case closed
    }

    /// Something the user promised, as the assistant found it in a case.
    public struct Commitment: Sendable, Equatable, Identifiable {
        public var id: String
        public var caseID: CaseID
        public var text: String
        /// The sentence it comes from.
        public var quote: String
        public var due: Date?
        /// The user's message the sentence is in; nil for the samples.
        public var messageID: MessageID?
        /// Only open ones are shown.
        public var state: CommitmentState

        public init(
            id: String, caseID: CaseID, text: String, quote: String = "", due: Date? = nil,
            messageID: MessageID? = nil, state: CommitmentState = .open
        ) {
            self.id = id
            self.caseID = caseID
            self.text = text
            self.quote = quote
            self.due = due
            self.messageID = messageID
            self.state = state
        }
    }

    /// The assistant's last pass over the board.
    public struct Run: Sendable, Equatable {
        /// Names the assistant (the run's source).
        public var model: String
        /// When it ended, or started while it runs.
        public var date: Date
        public var note: String
        /// Cases it annotated.
        public var annotated: Int
        /// It has not ended yet.
        public var running: Bool
        /// The class of its failure (`BoardRunError`); nil after a success.
        public var error: String?
        /// Who started it (`BoardTrigger`: manual, auto, external); "" when
        /// not known.
        public var trigger: String
        /// When it started; nil when not known.
        public var started: Date?

        public init(
            model: String, date: Date, note: String = "", annotated: Int = 0, running: Bool = false,
            error: String? = nil, trigger: String = "", started: Date? = nil
        ) {
            self.model = model
            self.date = date
            self.note = note
            self.annotated = annotated
            self.running = running
            self.error = error
            self.trigger = trigger
            self.started = started
        }
    }

    /// What the triage needs to know (`board.list` `triage`); only carried
    /// for now.
    public struct Triage: Sendable, Equatable {
        /// Live cases waiting for the assistant (0 while it is off).
        public var queue: Int
        /// Cases automatic runs annotated today.
        public var annotatedToday: Int
        /// The tokens triage runs used in the last 24 hours, as the daemon
        /// summed them when it listed; nil when no run reported any.
        public var usage24h: BoardUsageTotal?

        public init(queue: Int = 0, annotatedToday: Int = 0, usage24h: BoardUsageTotal? = nil) {
            self.queue = queue
            self.annotatedToday = annotatedToday
            self.usage24h = usage24h
        }
    }

    /// How far the source's data is.
    public enum Phase: Sendable, Equatable {
        /// Nothing has arrived yet.
        case loading
        /// The daemon still evaluates the mail for the first time: the
        /// cases may be partial.
        case preparing
        case ready
        /// The daemon cannot be asked; the cases are the last ones known.
        case unavailable
        /// The daemon was asked and could not list the board (a timeout,
        /// a storage error); the cases are the last ones known and the
        /// source asks again after a while.
        case failed
        /// The daemon has no board (`methodNotFound`: an older backend).
        case unsupported
        /// The board is turned off in the daemon: no cases.
        case off

        /// The board could not be listed: the cases shown are old or none.
        public var isFailure: Bool {
            switch self {
            case .unavailable, .failed, .unsupported: return true
            case .loading, .preparing, .ready, .off: return false
            }
        }
    }

    /// Everything a source knows at one moment.
    public struct Snapshot: Sendable, Equatable {
        public var accounts: [AccountInfo]
        public var cases: [Case]
        public var commitments: [Commitment]
        /// The assistant is on and its annotations count.
        public var annotated: Bool
        public var run: Run?
        public var phase: Phase
        public var triage: Triage
        /// More cases than the daemon lists (the oldest left out).
        public var truncated: Bool

        public init(
            accounts: [AccountInfo] = [], cases: [Case] = [], commitments: [Commitment] = [],
            annotated: Bool = false, run: Run? = nil, phase: Phase = .ready, triage: Triage = Triage(),
            truncated: Bool = false
        ) {
            self.accounts = accounts
            self.cases = cases
            self.commitments = commitments
            self.annotated = annotated
            self.run = run
            self.phase = phase
            self.triage = triage
            self.truncated = truncated
        }

        /// No account, no case, the assistant off.
        public static let empty = Snapshot()
    }

    /// The annotation that counts: none while the assistant is off or
    /// when it is stale.
    public static func annotation(of c: Case, annotated: Bool) -> Annotation? {
        guard annotated, let a = c.annotation, !a.stale else { return nil }
        return a
    }

    /// The state a case shows (docs/api.md §4.13): the user's choice, else
    /// the assistant's (when annotations count, the annotation is not
    /// stale and has one), else the rules'.
    public static func state(of c: Case, annotated: Bool) -> State {
        c.userState ?? annotation(of: c, annotated: annotated)?.state ?? c.ruleState
    }

    /// Who decided `state(of:annotated:)`. An annotation that leaves the
    /// state to the rules kept it; a stale one counts as none.
    public static func stateSource(of c: Case, annotated: Bool) -> StateSource {
        if c.userState != nil {
            return .user
        }
        guard annotated else {
            return .assistantOff
        }
        guard let a = annotation(of: c, annotated: annotated) else {
            return .rules
        }
        guard let st = a.state, st != c.ruleState else {
            return .assistantKept
        }
        return .assistantChanged(from: c.ruleState)
    }

    // MARK: Cleaning

    /// The caps of the cleaned strings, in UTF-8 bytes (or a count).
    enum Cap {
        static let person = 200
        static let title = 300
        static let snippet = 400
        static let reason = 400
        static let account = 120
        static let badge = 64
        static let issueKey = 64
        static let status = 64
        static let quote = 300
        static let task = 300
        static let tasks = 20
        /// The tasks looked at to find `tasks` non-empty ones.
        static let taskScan = 200
        static let commitments = 100
        static let commitment = 300
        static let model = 64
        static let note = 300
        static let summary = 2000
        static let draft = 4000
        static let source = 64
        static let message = API.Limits.maxBoardMessageTextBytes
        static let messages = 50
    }

    /// One line of display text made safe: drops invalid UTF-8 (the
    /// replacement character), control and format characters (Cc, Cf: NUL,
    /// bidirectional overrides such as U+202E, zero-width characters)
    /// except a joiner between two kept characters (`JoinerState`), turns every whitespace (line breaks, tabs, Zl, Zp) into a space,
    /// collapses runs of spaces, trims, and caps the result at `max` UTF-8
    /// bytes on a character boundary (a scalar boundary as `Jira.clean`,
    /// without a trailing grapheme cluster the cut broke). Stops reading
    /// once the cap is passed, or after `budget(max)` input scalars when
    /// most of them are dropped, so a huge input costs no more than a
    /// short one.
    public static func cleanLine(_ s: String, max: Int) -> String {
        guard max > 0 else { return "" }
        var out = String.UnicodeScalarView()
        var bytes = 0
        var limit = max
        var space = false
        var j = JoinerState()
        var left = budget(max)
        for r in s.unicodeScalars {
            if left == 0 {
                // Out of budget: the text ends here. `r` goes along past the
                // limit only so the cut can tell whether it broke a character.
                limit = Swift.min(limit, bytes)
                if !space && kept(r) {
                    out.append(r)
                }
                break
            }
            left -= 1
            if r.value == 0xFFFD {
                continue
            }
            if r.properties.isWhitespace {
                space = !out.isEmpty
                j.reset()
                continue
            }
            if j.take(r, atStart: out.isEmpty || space) {
                continue
            }
            if dropped(r) {
                continue
            }
            if space {
                out.append(" ")
                bytes += 1
                space = false
            }
            bytes += j.flush(&out)
            out.append(r)
            bytes += UTF8.width(r)
            if bytes > max {
                break
            }
        }
        return capped(out, limit)
    }

    /// A block of display text made safe: like `cleanLine`, but line
    /// breaks stay (CR LF, CR, NEL, VT, FF, Zl and Zp become `\n`), other
    /// whitespace becomes a space, spaces at the end of a line go, at most
    /// one empty line is kept in a row, and the block is trimmed.
    public static func cleanBlock(_ s: String, max: Int) -> String {
        guard max > 0 else { return "" }
        var out = String.UnicodeScalarView()
        var bytes = 0
        var limit = max
        var spaces = 0
        var breaks = 0
        var afterCR = false
        var j = JoinerState()
        var left = budget(max)
        for r in s.unicodeScalars {
            if left == 0 {
                // Out of budget, as in `cleanLine`.
                limit = Swift.min(limit, bytes)
                if spaces == 0 && breaks == 0 && kept(r) {
                    out.append(r)
                }
                break
            }
            left -= 1
            let cr = afterCR
            afterCR = false
            if r.value == 0xFFFD {
                continue
            }
            if r.properties.isWhitespace {
                switch r.value {
                case 0x0A:
                    if !cr {
                        breaks += 1
                    }
                    spaces = 0
                case 0x0D:
                    breaks += 1
                    spaces = 0
                    afterCR = true
                case 0x0B, 0x0C, 0x85, 0x2028, 0x2029:
                    breaks += 1
                    spaces = 0
                default:
                    spaces += 1
                }
                j.reset()
                continue
            }
            if j.take(r, atStart: out.isEmpty || spaces > 0 || breaks > 0) {
                continue
            }
            if dropped(r) {
                continue
            }
            if !out.isEmpty {
                // A run of whitespace is bounded by the cap: it is cut anyway.
                let lines = Swift.min(breaks, 2)
                let pad = Swift.min(spaces, max)
                out.append(contentsOf: repeatElement("\n", count: lines))
                out.append(contentsOf: repeatElement(" ", count: pad))
                bytes += lines + pad
            }
            breaks = 0
            spaces = 0
            bytes += j.flush(&out)
            out.append(r)
            bytes += UTF8.width(r)
            if bytes > max {
                break
            }
        }
        return capped(out, limit)
    }

    /// A joiner (ZWJ, ZWNJ) the cleaners hold back until they see what
    /// follows it. The rule is the daemon's (board.CleanText, Go
    /// `joinerState`): a joiner is kept only when, once the dropped
    /// characters are gone, the characters right before and right after it
    /// are kept characters that are neither whitespace nor a joiner. A
    /// joiner at the start or end of a line, next to whitespace, or in a run
    /// of joiners goes; a variation selector right after a held joiner goes
    /// too. Emoji ZWJ sequences and Persian or Indic words keep theirs.
    struct JoinerState {
        static let zwnj: UInt32 = 0x200C
        static let zwj: UInt32 = 0x200D
        static let vs15: UInt32 = 0xFE0E
        static let vs16: UInt32 = 0xFE0F

        /// The joiner held back, nil when none.
        private var joiner: Unicode.Scalar?
        /// The held joiner goes whatever follows (nothing kept before it,
        /// whitespace before it, or a run of joiners).
        private var stray = false

        static func isJoiner(_ r: Unicode.Scalar) -> Bool { r.value == zwnj || r.value == zwj }

        /// Whether `r` was consumed by the joiner rule: a joiner (held, or
        /// marking a run), or a variation selector after a held joiner.
        /// `atStart`: nothing kept is before `r` on its line, or whitespace
        /// is.
        mutating func take(_ r: Unicode.Scalar, atStart: Bool) -> Bool {
            if Self.isJoiner(r) {
                if joiner != nil {
                    stray = true
                } else {
                    joiner = r
                    stray = atStart
                }
                return true
            }
            if (r.value == Self.vs15 || r.value == Self.vs16) && joiner != nil {
                return true
            }
            return false
        }

        /// Writes the held joiner before a kept character, unless it is
        /// stray, and forgets it; returns the bytes written.
        mutating func flush(_ out: inout String.UnicodeScalarView) -> Int {
            defer { reset() }
            if let joiner, !stray {
                out.append(joiner)
                return UTF8.width(joiner)
            }
            return 0
        }

        /// Forgets a held joiner (whitespace followed it).
        mutating func reset() {
            joiner = nil
            stray = false
        }
    }

    /// How many input scalars the cleaners read for a cap of `max` bytes:
    /// what they keep stops them at the cap, so only dropped characters and
    /// whitespace run into this.
    private static func budget(_ max: Int) -> Int {
        max > Int.max / 8 ? Int.max : max * 8
    }

    /// Whether the cleaners keep `r` as it is (not whitespace, not dropped).
    private static func kept(_ r: Unicode.Scalar) -> Bool {
        r.value != 0xFFFD && !r.properties.isWhitespace && !dropped(r)
    }

    /// The control and format characters the cleaners drop.
    private static func dropped(_ r: Unicode.Scalar) -> Bool {
        switch r.properties.generalCategory {
        case .control, .format, .lineSeparator, .paragraphSeparator:
            return true
        default:
            return false
        }
    }

    /// `s` cut to at most `n` UTF-8 bytes on a character boundary, the
    /// whitespace before the cut trimmed. A cut inside a grapheme cluster
    /// (a letter whose combining mark is past it, one regional indicator
    /// of a flag) drops the whole cluster.
    private static func capped(_ s: String.UnicodeScalarView, _ n: Int) -> String {
        let text = String(s)
        let scalars = text.unicodeScalars
        var end = scalars.startIndex
        var bytes = 0
        while end < scalars.endIndex {
            let w = UTF8.width(scalars[end])
            if bytes + w > n {
                break
            }
            bytes += w
            scalars.formIndex(after: &end)
        }
        // What follows the cut is still in `text`, so the boundary is that
        // of the whole text.
        while end > scalars.startIndex, end.samePosition(in: text) == nil {
            scalars.formIndex(before: &end)
        }
        var out = String.UnicodeScalarView(scalars[..<end])
        // Trailing whitespace goes, and a joiner the cut left last joins
        // nothing.
        while let last = out.last, last == " " || last == "\n" || JoinerState.isJoiner(last) {
            out.removeLast()
        }
        return String(out)
    }
}
