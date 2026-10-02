// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board (docs/api.md §4.13, §5 notify.boardChanged; backend/pkg/api/
// board.go): conversations and issues sorted into four states by what is
// owed. Every string of a case, an annotation, a commitment and a message
// comes from mail or from an assistant that read mail: untrusted plain text
// the daemon cleaned (no control or bidi characters, no URLs in annotations),
// never markup. Not to be confused with the namespace `Board` of the model
// (`Board/`), which these types feed.

import Foundation

// MARK: Identifiers

/// api.BoardCaseID: "c_" and 32 lowercase hex digits. Stable across thread
/// merges (unlike `ThreadID`) and restarts; per account.
public struct BoardCaseID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.BoardCommitmentID: a commitment of a case. Opaque.
public struct BoardCommitmentID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.BoardRunID: a triage run (`board.runStart`). Opaque.
public struct BoardRunID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

// MARK: Enums

/// api.BoardState: where a case stands. The contract calls it closed (four
/// values); like every wire enum here an unknown value still decodes as
/// itself, and `BoardState.all` lists the four in display order.
public struct BoardState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Needs the user now: marked important, or flagged by the user.
    public static let hot: BoardState = "hot"
    /// Waits for the user's answer.
    public static let you: BoardState = "you"
    /// The user waits for someone else.
    public static let them: BoardState = "them"
    /// Nothing to do; for reading.
    public static let info: BoardState = "info"

    /// api.BoardStates: display order.
    public static let all: [BoardState] = [.hot, .you, .them, .info]

    /// api.BoardState.Valid.
    public var isValid: Bool { BoardState.all.contains(self) }
}

/// api.BoardReason: the code of the rule that gave a case its `ruleState`.
/// An open enum: a client shows a generic text for a code it does not know
/// (it decodes, never fails). Codes are never reused for another meaning.
public struct BoardReason: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Newest relevant member inbound, the user in its To, and its own
    /// header says Importance: high or X-Priority 1 or 2.
    public static let hotImportant: BoardReason = "hot.important"
    /// The user flagged a member and the newest relevant member is inbound.
    public static let hotFlagged: BoardReason = "hot.flagged"
    /// Newest relevant member inbound and the user is in its To.
    public static let youAddressed: BoardReason = "you.addressed"
    /// Newest relevant member inbound and answers one of the user's messages.
    public static let youRepliedToYou: BoardReason = "you.repliedToYou"
    /// The user replied to an inbound member, to one of its senders.
    public static let themReplied: BoardReason = "them.replied"
    /// The user started the thread with a question to someone else.
    public static let themAsked: BoardReason = "them.asked"
    /// Inbound; the user is only in Cc.
    public static let infoCcOnly: BoardReason = "info.ccOnly"
    /// Inbound; the user is not among the recipients (a list, a Bcc).
    public static let infoNotAddressed: BoardReason = "info.notAddressed"
    /// Inbound and the user in its To, but from a sender the user has never
    /// written to; its Importance does not count either.
    public static let infoUnknownSender: BoardReason = "info.unknownSender"
    /// A note to oneself.
    public static let infoYourNote: BoardReason = "info.yourNote"
    /// Jira: the last item that is not an event is the user's comment.
    public static let jiraYourComment: BoardReason = "jira.yourComment"
    /// Jira: someone else's item on an issue assigned to the user.
    public static let jiraAssigned: BoardReason = "jira.assigned"
    /// Jira: someone else's item on an issue the user reported.
    public static let jiraReporter: BoardReason = "jira.reporter"
    /// Jira: someone else's item on an issue the user commented on before.
    public static let jiraCommented: BoardReason = "jira.commented"
    /// Jira: an issue the user only watches.
    public static let jiraWatching: BoardReason = "jira.watching"
    /// The rules no longer make the thread a case, but a user state, a
    /// future remind, a future deadline or an open commitment keeps it;
    /// `ruleState` is the state the rules gave last.
    public static let kept: BoardReason = "kept"

    /// The codes of this contract version, in the order of board.go.
    public static let known: [BoardReason] = [
        .hotImportant, .hotFlagged, .youAddressed, .youRepliedToYou, .themReplied, .themAsked,
        .infoCcOnly, .infoNotAddressed, .infoUnknownSender, .infoYourNote,
        .jiraYourComment, .jiraAssigned, .jiraReporter, .jiraCommented, .jiraWatching, .kept,
    ]
}

/// api.BoardVisibility: where a case is listed. Derived by the daemon.
public struct BoardVisibility: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// On the board.
    public static let live: BoardVisibility = "live"
    /// The user marked it done (`doneAt`); a later inbound message reopens it.
    public static let done: BoardVisibility = "done"
    /// Hidden until `remindAt`, then live again.
    public static let snoozed: BoardVisibility = "snoozed"
}

/// api.BoardCommitmentState: where a commitment stands.
public struct BoardCommitmentState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let open: BoardCommitmentState = "open"
    /// The user ticked it off.
    public static let done: BoardCommitmentState = "done"
    /// Closed by the daemon, see `BoardCommitment.closedReason`.
    public static let closed: BoardCommitmentState = "closed"
}

/// api.CommitmentClosed*: why the daemon closed a commitment by itself. An
/// open enum.
public struct BoardCommitmentClosedReason: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// A newer message of the user's arrived in the thread.
    public static let replied: BoardCommitmentClosedReason = "replied"
    /// The case was marked done.
    public static let done: BoardCommitmentClosedReason = "done"
}

/// api.BoardTrigger: what started a triage run.
public struct BoardTrigger: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The user pressed Triage.
    public static let manual: BoardTrigger = "manual"
    /// The client's automatic schedule.
    public static let auto: BoardTrigger = "auto"
    /// Annotations without a run id (Claude Desktop, Claude Code); only
    /// ever read, never passed to `board.runStart`.
    public static let external: BoardTrigger = "external"
}

/// api.BoardRunError: the class of a failed triage run, never free text. An
/// open enum for readers; `board.runEnd` stores any value it does not know
/// as `failed`.
public struct BoardRunError: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let cancelled: BoardRunError = "cancelled"
    public static let timeout: BoardRunError = "timeout"
    public static let signedOut: BoardRunError = "signedOut"
    public static let failed: BoardRunError = "failed"
}

/// api.QuoteField: what a `quoteNotFound` error is about.
public struct QuoteField: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// `BoardAnnotateParams.due`'s quote.
    public static let due: QuoteField = "due"
    /// `BoardCommitParams.quote`.
    public static let commitment: QuoteField = "commitment"
}

// MARK: Limits

extension API.Limits {
    /// board.list.
    public static let maxBoardCases = 1000
    /// board.get: the newest.
    public static let maxBoardMessages = 50
    public static let maxBoardMessageTextBytes = 8000
    public static let maxBoardDraftTextBytes = 4000
    public static let defaultBoardQueueLimit = 3
    public static let maxBoardQueueLimit = 5
    public static let maxBoardTitleBytes = 300
    public static let maxBoardWhyBytes = 400
    public static let maxBoardSummaryBytes = 2000
    public static let maxBoardTasks = 10
    public static let maxBoardTaskBytes = 300
    public static let maxBoardCommitmentTextBytes = 300
    public static let maxBoardSourceBytes = 64
    public static let minBoardQuoteBytes = 10
    public static let maxBoardQuoteBytes = 300
    /// `BoardWindows`: 1...this days.
    public static let maxBoardWindowDays = 365
    /// `BoardUsage`: each counter 0...this; the daemon stores a larger one
    /// as this.
    public static let maxBoardUsageTokens: Int64 = 1_000_000_000_000
    /// `board.remind`: `until` at most now plus this.
    public static let maxBoardRemind: Duration = .seconds(365 * 24 * 3600)
    public static let defaultBoardHotDays = 90
    public static let defaultBoardYouDays = 30
    public static let defaultBoardThemDays = 30
    public static let defaultBoardInfoDays = 14
    public static let minBoardAutoTriageMinutes = 5
    public static let maxBoardAutoTriageMinutes = 1440
    public static let defaultBoardAutoTriageMinutes = 30
    public static let maxBoardAutoTriageDailyCases = 1000
    public static let defaultBoardAutoTriageDailyCases = 60
}

// MARK: Cases

/// api.BoardIssue: the issue behind a case of a jira account. `status` is
/// untrusted display text from the site.
public struct BoardIssue: Codable, Sendable, Equatable {
    /// "ITSD-42".
    public var key: String
    public var status: String
    /// nil (or empty) when the daemon does not know it.
    public var statusCategory: IssueStatusCategory?

    public init(key: String, status: String, statusCategory: IssueStatusCategory? = nil) {
        self.key = key
        self.status = status
        self.statusCategory = statusCategory
    }
}

/// api.BoardDraft: a draft linked to a case.
public struct BoardDraft: Codable, Sendable, Equatable {
    public var draftId: DraftID
    /// The draft's plain text, at most `API.Limits.maxBoardDraftTextBytes`.
    public var text: String
    public var updated: Date

    public init(draftId: DraftID, text: String, updated: Date) {
        self.draftId = draftId
        self.text = text
        self.updated = updated
    }
}

/// api.BoardDue: a deadline with the quote it comes from. Also the `due` of
/// `BoardAnnotateParams`; the daemon verifies the quote against the message.
public struct BoardDue: Codable, Sendable, Equatable {
    public var at: Date
    /// The sentence of the message the deadline comes from; a client always
    /// shows it next to the date.
    public var quote: String
    /// A member of the case's thread.
    public var messageId: MessageID

    public init(at: Date, quote: String, messageId: MessageID) {
        self.at = at
        self.quote = quote
        self.messageId = messageId
    }
}

/// api.BoardAnnotation: what an assistant made of a case. Untrusted text,
/// shown only as plain text, never as the daemon's or the user's own words.
/// When `stale` a client uses none of it (state, title, summary, deadline,
/// tasks); the case's `draft` stays.
public struct BoardAnnotation: Codable, Sendable, Equatable {
    /// nil: the assistant left the state to the rules.
    public var state: BoardState?
    /// One line; "" = use the subject.
    public var title: String
    /// A block, line breaks kept.
    public var summary: String
    /// One line: why the case is in its state.
    public var why: String
    @NullAsEmpty public var tasks: [String]
    public var due: BoardDue?
    /// Names the assistant (e.g. a model name).
    public var source: String
    /// When it was made.
    public var at: Date
    /// A member was added, removed or got its body since. nil means false.
    public var stale: Bool?

    public init(
        state: BoardState? = nil, title: String = "", summary: String = "", why: String = "", tasks: [String] = [],
        due: BoardDue? = nil, source: String, at: Date, stale: Bool? = nil
    ) {
        self.state = state
        self.title = title
        self.summary = summary
        self.why = why
        self.tasks = tasks
        self.due = due
        self.source = source
        self.at = at
        self.stale = stale
    }
}

/// api.BoardCase: a conversation or an issue on the board. The state shown
/// is `userState` when set, else `annotation.state` when the assistant is on
/// (`BoardListResult.assistant`) and the annotation is not stale and has
/// one, else `ruleState`.
public struct BoardCase: Codable, Sendable, Equatable {
    public var id: BoardCaseID
    public var accountId: AccountID
    /// The case's thread now; a merge can change it, the id stays.
    public var threadId: ThreadID
    public var ruleState: BoardState
    public var ruleReason: BoardReason
    /// The user's own choice (`board.setState`); nil = automatic.
    public var userState: BoardState?
    /// nil when there is none; present also when stale.
    public var annotation: BoardAnnotation?
    public var visibility: BoardVisibility
    /// Set when `visibility` is done.
    public var doneAt: Date?
    /// Set while snoozed, always in the future.
    public var remindAt: Date?
    /// The newest relevant member's, `Re:`/`Fwd:` stripped; for an issue
    /// "KEY: Summary".
    public var subject: String
    /// The other party.
    public var person: Address
    /// When the newest relevant member arrived.
    public var date: Date
    public var snippet: String
    public var unread: Bool
    public var hasAttachments: Bool
    /// Relevant members.
    public var messageCount: Int
    /// The member a reply answers (`draft.create` reply); on a jira account
    /// a reply is a comment.
    public var replyMessageId: MessageID
    public var replyFolderId: FolderID
    /// The newest relevant member.
    public var latestMessageId: MessageID
    /// Set for a case of a jira account.
    public var issue: BoardIssue?
    /// `board.archive` would move messages.
    public var canArchive: Bool
    /// The suggested reply an annotation linked, while that draft exists;
    /// survives a stale annotation.
    public var draft: BoardDraft?
    /// Changes whenever anything above changes, including the members
    /// `board.get` returns; cache `board.get` by (id, version).
    public var version: Int64

    public init(
        id: BoardCaseID, accountId: AccountID, threadId: ThreadID, ruleState: BoardState, ruleReason: BoardReason,
        userState: BoardState? = nil, annotation: BoardAnnotation? = nil, visibility: BoardVisibility = .live,
        doneAt: Date? = nil, remindAt: Date? = nil, subject: String, person: Address, date: Date,
        snippet: String = "", unread: Bool = false, hasAttachments: Bool = false, messageCount: Int = 1,
        replyMessageId: MessageID, replyFolderId: FolderID, latestMessageId: MessageID, issue: BoardIssue? = nil,
        canArchive: Bool = false, draft: BoardDraft? = nil, version: Int64 = 1
    ) {
        self.id = id
        self.accountId = accountId
        self.threadId = threadId
        self.ruleState = ruleState
        self.ruleReason = ruleReason
        self.userState = userState
        self.annotation = annotation
        self.visibility = visibility
        self.doneAt = doneAt
        self.remindAt = remindAt
        self.subject = subject
        self.person = person
        self.date = date
        self.snippet = snippet
        self.unread = unread
        self.hasAttachments = hasAttachments
        self.messageCount = messageCount
        self.replyMessageId = replyMessageId
        self.replyFolderId = replyFolderId
        self.latestMessageId = latestMessageId
        self.issue = issue
        self.canArchive = canArchive
        self.draft = draft
        self.version = version
    }
}

/// api.BoardCommitment: something the user promised in one of their own
/// messages, as an assistant found it. `text` is the assistant's wording
/// (untrusted); `quote` is verbatim from the user's text of `messageId`.
public struct BoardCommitment: Codable, Sendable, Equatable {
    public var id: BoardCommitmentID
    public var caseId: BoardCaseID
    public var accountId: AccountID
    /// The user's message it is in.
    public var messageId: MessageID
    public var text: String
    public var quote: String
    public var due: Date?
    public var state: BoardCommitmentState
    /// Set for state `closed`. An open enum.
    public var closedReason: BoardCommitmentClosedReason?
    /// When it was recorded.
    public var at: Date

    public init(
        id: BoardCommitmentID, caseId: BoardCaseID, accountId: AccountID, messageId: MessageID, text: String,
        quote: String, due: Date? = nil, state: BoardCommitmentState = .open,
        closedReason: BoardCommitmentClosedReason? = nil, at: Date
    ) {
        self.id = id
        self.caseId = caseId
        self.accountId = accountId
        self.messageId = messageId
        self.text = text
        self.quote = quote
        self.due = due
        self.state = state
        self.closedReason = closedReason
        self.at = at
    }
}

/// api.BoardMessage: one member of a case as `board.get` shows it; plain
/// text only, never HTML.
public struct BoardMessage: Codable, Sendable, Equatable {
    public var id: MessageID
    public var folderId: FolderID
    public var from: Address
    public var date: Date
    /// In a folder of role sent or outbox.
    public var mine: Bool
    /// The stored plain text with quoted history and signature cut off,
    /// at most `API.Limits.maxBoardMessageTextBytes`.
    public var text: String
    /// Something was cut off `text`. nil means false.
    public var trimmed: Bool?

    public init(
        id: MessageID, folderId: FolderID, from: Address, date: Date, mine: Bool = false, text: String,
        trimmed: Bool? = nil
    ) {
        self.id = id
        self.folderId = folderId
        self.from = from
        self.date = date
        self.mine = mine
        self.text = text
        self.trimmed = trimmed
    }
}

// MARK: Triage status, preferences

/// api.BoardRun: a triage run as `board.list` reports it.
public struct BoardRun: Codable, Sendable, Equatable {
    /// When it started.
    public var at: Date
    /// nil while it runs.
    public var endedAt: Date?
    public var trigger: BoardTrigger
    public var source: String
    /// Cases annotated in it.
    public var annotated: Int
    /// nil after a success.
    public var error: BoardRunError?

    public init(
        at: Date, endedAt: Date? = nil, trigger: BoardTrigger, source: String, annotated: Int = 0,
        error: BoardRunError? = nil
    ) {
        self.at = at
        self.endedAt = endedAt
        self.trigger = trigger
        self.source = source
        self.annotated = annotated
        self.error = error
    }
}

/// api.BoardTriage: what the client's status line and automatic schedule
/// need.
public struct BoardTriage: Codable, Sendable, Equatable {
    /// The newest run by start, of any trigger; nil before the first.
    public var lastRun: BoardRun?
    /// Cases annotated by runs with trigger auto that started today (the
    /// daemon's local day).
    public var annotatedTodayAuto: Int
    /// Live cases `board.queue` would offer; 0 while the assistant is off.
    public var queue: Int
    /// The token usage of the runs that ended within the 24 hours before
    /// board.list answered (the daemon's clock) and carry usage; nil when
    /// none does. It shrinks as runs age out, without a notification.
    public var usage24h: BoardUsageTotal?

    public init(
        lastRun: BoardRun? = nil, annotatedTodayAuto: Int = 0, queue: Int = 0, usage24h: BoardUsageTotal? = nil
    ) {
        self.lastRun = lastRun
        self.annotatedTodayAuto = annotatedTodayAuto
        self.queue = queue
        self.usage24h = usage24h
    }
}

/// api.BoardUsage: the token usage of a triage run as the client's
/// assistant reported it, each counter 0...`API.Limits.maxBoardUsageTokens`.
public struct BoardUsage: Codable, Sendable, Equatable {
    public var inputTokens: Int64
    public var outputTokens: Int64
    public var cacheCreationInputTokens: Int64
    public var cacheReadInputTokens: Int64

    public init(
        inputTokens: Int64 = 0, outputTokens: Int64 = 0, cacheCreationInputTokens: Int64 = 0,
        cacheReadInputTokens: Int64 = 0
    ) {
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheCreationInputTokens = cacheCreationInputTokens
        self.cacheReadInputTokens = cacheReadInputTokens
    }

    /// The assistant's tally of a run, each counter brought into
    /// 0...`API.Limits.maxBoardUsageTokens`.
    public init(_ u: Assistant.Usage) {
        func c(_ n: Int64) -> Int64 { min(max(n, 0), API.Limits.maxBoardUsageTokens) }
        self.init(
            inputTokens: c(u.inputTokens), outputTokens: c(u.outputTokens),
            cacheCreationInputTokens: c(u.cacheCreationInputTokens), cacheReadInputTokens: c(u.cacheReadInputTokens))
    }
}

/// api.BoardUsageTotal: `BoardUsage` summed over `runs` runs
/// (`BoardTriage.usage24h`), one flat object. A counter the daemon left
/// out reads as 0.
public struct BoardUsageTotal: Codable, Sendable, Equatable {
    public var inputTokens: Int64
    public var outputTokens: Int64
    public var cacheCreationInputTokens: Int64
    public var cacheReadInputTokens: Int64
    /// The runs that contributed, ≥ 1.
    public var runs: Int

    public init(
        inputTokens: Int64 = 0, outputTokens: Int64 = 0, cacheCreationInputTokens: Int64 = 0,
        cacheReadInputTokens: Int64 = 0, runs: Int = 1
    ) {
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheCreationInputTokens = cacheCreationInputTokens
        self.cacheReadInputTokens = cacheReadInputTokens
        self.runs = runs
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        inputTokens = try c.decodeIfPresent(Int64.self, forKey: .inputTokens) ?? 0
        outputTokens = try c.decodeIfPresent(Int64.self, forKey: .outputTokens) ?? 0
        cacheCreationInputTokens = try c.decodeIfPresent(Int64.self, forKey: .cacheCreationInputTokens) ?? 0
        cacheReadInputTokens = try c.decodeIfPresent(Int64.self, forKey: .cacheReadInputTokens) ?? 0
        runs = try c.decodeIfPresent(Int.self, forKey: .runs) ?? 0
    }
}

/// api.BoardWindows: how long cases of each state stay on the board, in days
/// from their date, 1...`API.Limits.maxBoardWindowDays`.
public struct BoardWindows: Codable, Sendable, Equatable {
    public var hot: Int
    public var you: Int
    public var them: Int
    public var info: Int

    public init(
        hot: Int = API.Limits.defaultBoardHotDays, you: Int = API.Limits.defaultBoardYouDays,
        them: Int = API.Limits.defaultBoardThemDays, info: Int = API.Limits.defaultBoardInfoDays
    ) {
        self.hot = hot
        self.you = you
        self.them = them
        self.info = info
    }
}

/// api.BoardPreferences: the board's daemon-side preferences
/// (`board.preferences`, `board.setPreferences`; not part of `Preferences`).
public struct BoardPreferences: Codable, Sendable, Equatable {
    /// The daemon computes the board. Default true.
    public var enabled: Bool
    /// Annotations count and `board.queue` hands out mail text. Default
    /// false; turned on only after the user's consent.
    public var assistant: Bool
    /// Default 90/30/30/14.
    public var windows: BoardWindows
    /// Limits triage to these accounts; empty = every enabled mail account.
    /// A jira account is triaged only when listed.
    @NullAsEmpty public var triageAccounts: [AccountID]
    /// The client runs triage on its own schedule; the daemon only stores it.
    public var autoTriage: Bool
    /// The least time between automatic runs,
    /// `API.Limits.minBoardAutoTriageMinutes`...`maxBoardAutoTriageMinutes`.
    public var autoTriageMinutes: Int
    /// Cap of cases automatic runs annotate per local day, 0...
    /// `API.Limits.maxBoardAutoTriageDailyCases`; 0 = none.
    public var autoTriageDailyCases: Int

    public init(
        enabled: Bool = true, assistant: Bool = false, windows: BoardWindows = BoardWindows(),
        triageAccounts: [AccountID] = [], autoTriage: Bool = false,
        autoTriageMinutes: Int = API.Limits.defaultBoardAutoTriageMinutes,
        autoTriageDailyCases: Int = API.Limits.defaultBoardAutoTriageDailyCases
    ) {
        self.enabled = enabled
        self.assistant = assistant
        self.windows = windows
        self.triageAccounts = triageAccounts
        self.autoTriage = autoTriage
        self.autoTriageMinutes = autoTriageMinutes
        self.autoTriageDailyCases = autoTriageDailyCases
    }

    /// api.DefaultBoardPreferences: a store that never had any set.
    public static let defaults = BoardPreferences()
}

// MARK: board.list, board.get

/// api.BoardListParams.
public struct BoardListParams: Codable, Sendable, Equatable {
    /// Empty = every enabled account; left out when empty.
    public var accountIds: [AccountID]

    public init(accountIds: [AccountID] = []) {
        self.accountIds = accountIds
    }

    private enum CodingKeys: String, CodingKey { case accountIds }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountIds = try c.decodeIfPresent([AccountID].self, forKey: .accountIds) ?? []
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeUnlessEmpty(accountIds, forKey: .accountIds)
    }
}

/// api.BoardListResult.
public struct BoardListResult: Codable, Sendable, Equatable {
    /// Live, done and snoozed, newest date first, at most
    /// `API.Limits.maxBoardCases` (the newest).
    @NullAsEmpty public var cases: [BoardCase]
    /// The open commitments of the live cases listed.
    @NullAsEmpty public var commitments: [BoardCommitment]
    /// `BoardPreferences.enabled`.
    public var enabled: Bool
    /// `BoardPreferences.assistant`.
    public var assistant: Bool
    public var triage: BoardTriage
    /// The daemon has evaluated every thread once since the board was
    /// enabled or its rules changed; until then `cases` may be partial.
    public var ready: Bool
    /// More than `API.Limits.maxBoardCases`. nil means false.
    public var truncated: Bool?

    public init(
        cases: [BoardCase] = [], commitments: [BoardCommitment] = [], enabled: Bool = true,
        assistant: Bool = false, triage: BoardTriage = BoardTriage(), ready: Bool = true, truncated: Bool? = nil
    ) {
        self.cases = cases
        self.commitments = commitments
        self.enabled = enabled
        self.assistant = assistant
        self.triage = triage
        self.ready = ready
        self.truncated = truncated
    }
}

/// api.BoardGetParams.
public struct BoardGetParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID

    public init(caseId: BoardCaseID) {
        self.caseId = caseId
    }
}

/// api.BoardGetResult.
public struct BoardGetResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase
    /// The case's relevant members, the newest `API.Limits.maxBoardMessages`,
    /// oldest first.
    @NullAsEmpty public var messages: [BoardMessage]

    public init(case: BoardCase, messages: [BoardMessage] = []) {
        self.case = `case`
        self.messages = messages
    }
}

// MARK: The user's decisions

/// api.BoardSetStateParams. A nil `state` goes out as `null` (back to
/// automatic).
public struct BoardSetStateParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var state: BoardState?

    public init(caseId: BoardCaseID, state: BoardState?) {
        self.caseId = caseId
        self.state = state
    }

    private enum CodingKeys: String, CodingKey { case caseId, state }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(caseId, forKey: .caseId)
        if let state { try c.encode(state, forKey: .state) } else { try c.encodeNil(forKey: .state) }
    }
}

/// api.BoardSetStateResult.
public struct BoardSetStateResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase

    public init(case: BoardCase) {
        self.case = `case`
    }
}

/// api.BoardSetDoneParams.
public struct BoardSetDoneParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var done: Bool

    public init(caseId: BoardCaseID, done: Bool) {
        self.caseId = caseId
        self.done = done
    }
}

/// api.BoardSetDoneResult.
public struct BoardSetDoneResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase

    public init(case: BoardCase) {
        self.case = `case`
    }
}

/// api.BoardRemindParams. `until` must lie in the future and within
/// `API.Limits.maxBoardRemind`; nil goes out as `null` (remind no more, live
/// again).
public struct BoardRemindParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var until: Date?

    public init(caseId: BoardCaseID, until: Date?) {
        self.caseId = caseId
        self.until = until
    }

    private enum CodingKeys: String, CodingKey { case caseId, until }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(caseId, forKey: .caseId)
        if let until { try c.encode(until, forKey: .until) } else { try c.encodeNil(forKey: .until) }
    }
}

/// api.BoardRemindResult.
public struct BoardRemindResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase

    public init(case: BoardCase) {
        self.case = `case`
    }
}

/// api.BoardArchiveParams.
public struct BoardArchiveParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID

    public init(caseId: BoardCaseID) {
        self.caseId = caseId
    }
}

/// api.BoardArchiveResult.
public struct BoardArchiveResult: Codable, Sendable, Equatable {
    /// Messages moved to the archive folder.
    public var archived: Int
    /// The account cannot archive: the case is only marked done. nil means
    /// false.
    public var noArchive: Bool?
    public var `case`: BoardCase

    public init(archived: Int, noArchive: Bool? = nil, case: BoardCase) {
        self.archived = archived
        self.noArchive = noArchive
        self.case = `case`
    }
}

/// api.BoardUnflagParams: removes the star from every message whose flag
/// puts the case on the board (`hot.flagged`), copies in other folders
/// included.
public struct BoardUnflagParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID

    public init(caseId: BoardCaseID) {
        self.caseId = caseId
    }
}

/// api.BoardUnflagResult: the case as stored (the rules re-evaluate it
/// afterwards and `notify.boardChanged` follows) and how many messages
/// lost their flag; 0 is not an error.
public struct BoardUnflagResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase
    public var unflagged: Int

    public init(case: BoardCase, unflagged: Int) {
        self.case = `case`
        self.unflagged = unflagged
    }
}

/// api.BoardDiscardDraftParams.
public struct BoardDiscardDraftParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID

    public init(caseId: BoardCaseID) {
        self.caseId = caseId
    }
}

/// api.BoardDiscardDraftResult: the case without `draft`.
public struct BoardDiscardDraftResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase

    public init(case: BoardCase) {
        self.case = `case`
    }
}

/// api.BoardSetDraftParams: links `draftId`, a reply draft within the case
/// in its account, as the case's suggested reply (the board's Suggest
/// Reply, which the application runs itself).
public struct BoardSetDraftParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var draftId: DraftID

    public init(caseId: BoardCaseID, draftId: DraftID) {
        self.caseId = caseId
        self.draftId = draftId
    }
}

/// api.BoardSetDraftResult: the case with `draft`.
public struct BoardSetDraftResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase

    public init(case: BoardCase) {
        self.case = `case`
    }
}

// MARK: Triage (the MCP bridge under --allow-triage)

/// api.BoardQueueParams.
public struct BoardQueueParams: Codable, Sendable, Equatable {
    /// Empty = every triage account; left out when empty.
    public var accountIds: [AccountID]
    /// Empty = any; others are skipped; left out when empty.
    public var caseIds: [BoardCaseID]
    /// nil or 0 = `API.Limits.defaultBoardQueueLimit`; at most
    /// `API.Limits.maxBoardQueueLimit`.
    public var limit: Int?

    public init(accountIds: [AccountID] = [], caseIds: [BoardCaseID] = [], limit: Int? = nil) {
        self.accountIds = accountIds
        self.caseIds = caseIds
        self.limit = limit
    }

    private enum CodingKeys: String, CodingKey { case accountIds, caseIds, limit }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountIds = try c.decodeIfPresent([AccountID].self, forKey: .accountIds) ?? []
        caseIds = try c.decodeIfPresent([BoardCaseID].self, forKey: .caseIds) ?? []
        limit = try c.decodeIfPresent(Int.self, forKey: .limit)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeUnlessEmpty(accountIds, forKey: .accountIds)
        try c.encodeUnlessEmpty(caseIds, forKey: .caseIds)
        try c.encodeIfPresent(limit, forKey: .limit)
    }
}

/// api.BoardQueueMessage: one member of a `BoardQueueItem`.
public struct BoardQueueMessage: Codable, Sendable, Equatable {
    public var messageId: MessageID
    public var from: Address
    public var to: [Address]?
    public var cc: [Address]?
    public var date: Date
    public var mine: Bool
    /// As `BoardMessage.text`, at most 3000 bytes and 12 KiB for the item's
    /// texts together.
    public var text: String
    /// Something was cut off `text`. nil means false.
    public var truncated: Bool?

    public init(
        messageId: MessageID, from: Address, to: [Address]? = nil, cc: [Address]? = nil, date: Date,
        mine: Bool = false, text: String, truncated: Bool? = nil
    ) {
        self.messageId = messageId
        self.from = from
        self.to = to
        self.cc = cc
        self.date = date
        self.mine = mine
        self.text = text
        self.truncated = truncated
    }
}

/// api.BoardQueueItem: a case handed to an assistant, everything it may read
/// of it. Every string but the ids and `inputKey` comes from mail.
public struct BoardQueueItem: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var accountId: AccountID
    /// Names the members the item was built from; `board.annotate` and
    /// `board.commit` pass it back and are refused (`conflict`) when the
    /// case changed since.
    public var inputKey: String
    public var ruleState: BoardState
    public var ruleReason: BoardReason
    public var userState: BoardState?
    public var subject: String
    /// For `create_draft` mode reply.
    public var replyMessageId: MessageID
    public var issue: BoardIssue?
    /// The user's addresses on the account, lower case.
    @NullAsEmpty public var own: [String]
    /// The newest 8, oldest first.
    @NullAsEmpty public var messages: [BoardQueueMessage]

    public init(
        caseId: BoardCaseID, accountId: AccountID, inputKey: String, ruleState: BoardState, ruleReason: BoardReason,
        userState: BoardState? = nil, subject: String, replyMessageId: MessageID, issue: BoardIssue? = nil,
        own: [String] = [], messages: [BoardQueueMessage] = []
    ) {
        self.caseId = caseId
        self.accountId = accountId
        self.inputKey = inputKey
        self.ruleState = ruleState
        self.ruleReason = ruleReason
        self.userState = userState
        self.subject = subject
        self.replyMessageId = replyMessageId
        self.issue = issue
        self.own = own
        self.messages = messages
    }
}

/// api.BoardQueueResult.
public struct BoardQueueResult: Codable, Sendable, Equatable {
    /// Newest date first.
    @NullAsEmpty public var items: [BoardQueueItem]
    /// Further cases the queue would offer.
    public var remaining: Int

    public init(items: [BoardQueueItem] = [], remaining: Int = 0) {
        self.items = items
        self.remaining = remaining
    }
}

/// api.BoardAnnotateParams. Empty strings and lists are left out, as Go's
/// `omitempty` does.
public struct BoardAnnotateParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var inputKey: String
    /// Counts the call in that run; absent, unknown or ended = the implicit
    /// external run of `source` and the day.
    public var runId: BoardRunID?
    public var state: BoardState?
    public var title: String?
    public var summary: String?
    public var why: String?
    public var tasks: [String]
    public var due: BoardDue?
    /// A draft of the case's account that replies to a member of the case;
    /// nil keeps no link.
    public var draftId: DraftID?
    public var source: String

    public init(
        caseId: BoardCaseID, inputKey: String, runId: BoardRunID? = nil, state: BoardState? = nil,
        title: String? = nil, summary: String? = nil, why: String? = nil, tasks: [String] = [],
        due: BoardDue? = nil, draftId: DraftID? = nil, source: String
    ) {
        self.caseId = caseId
        self.inputKey = inputKey
        self.runId = runId
        self.state = state
        self.title = title
        self.summary = summary
        self.why = why
        self.tasks = tasks
        self.due = due
        self.draftId = draftId
        self.source = source
    }

    private enum CodingKeys: String, CodingKey {
        case caseId, inputKey, runId, state, title, summary, why, tasks, due, draftId, source
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        caseId = try c.decode(BoardCaseID.self, forKey: .caseId)
        inputKey = try c.decode(String.self, forKey: .inputKey)
        runId = try c.decodeIfPresent(BoardRunID.self, forKey: .runId)
        state = try c.decodeIfPresent(BoardState.self, forKey: .state)
        title = try c.decodeIfPresent(String.self, forKey: .title)
        summary = try c.decodeIfPresent(String.self, forKey: .summary)
        why = try c.decodeIfPresent(String.self, forKey: .why)
        tasks = try c.decodeIfPresent([String].self, forKey: .tasks) ?? []
        due = try c.decodeIfPresent(BoardDue.self, forKey: .due)
        draftId = try c.decodeIfPresent(DraftID.self, forKey: .draftId)
        source = try c.decode(String.self, forKey: .source)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(caseId, forKey: .caseId)
        try c.encode(inputKey, forKey: .inputKey)
        try c.encodeIfPresent(runId, forKey: .runId)
        try c.encodeIfPresent(state, forKey: .state)
        try c.encodeIfPresent(title, forKey: .title)
        try c.encodeIfPresent(summary, forKey: .summary)
        try c.encodeIfPresent(why, forKey: .why)
        try c.encodeUnlessEmpty(tasks, forKey: .tasks)
        try c.encodeIfPresent(due, forKey: .due)
        try c.encodeIfPresent(draftId, forKey: .draftId)
        try c.encode(source, forKey: .source)
    }
}

/// api.BoardAnnotateResult.
public struct BoardAnnotateResult: Codable, Sendable, Equatable {
    public var `case`: BoardCase

    public init(case: BoardCase) {
        self.case = `case`
    }
}

/// api.BoardCommitParams.
public struct BoardCommitParams: Codable, Sendable, Equatable {
    public var caseId: BoardCaseID
    public var inputKey: String
    public var runId: BoardRunID?
    /// A member of the case that is the user's own.
    public var messageId: MessageID
    public var text: String
    public var quote: String
    public var due: Date?
    public var source: String

    public init(
        caseId: BoardCaseID, inputKey: String, runId: BoardRunID? = nil, messageId: MessageID, text: String,
        quote: String, due: Date? = nil, source: String
    ) {
        self.caseId = caseId
        self.inputKey = inputKey
        self.runId = runId
        self.messageId = messageId
        self.text = text
        self.quote = quote
        self.due = due
        self.source = source
    }
}

/// api.BoardCommitResult.
public struct BoardCommitResult: Codable, Sendable, Equatable {
    public var commitment: BoardCommitment

    public init(commitment: BoardCommitment) {
        self.commitment = commitment
    }
}

/// api.BoardSetCommitmentParams.
public struct BoardSetCommitmentParams: Codable, Sendable, Equatable {
    public var commitmentId: BoardCommitmentID
    /// false reopens a done or closed one.
    public var done: Bool

    public init(commitmentId: BoardCommitmentID, done: Bool) {
        self.commitmentId = commitmentId
        self.done = done
    }
}

/// api.BoardSetCommitmentResult.
public struct BoardSetCommitmentResult: Codable, Sendable, Equatable {
    public var commitment: BoardCommitment

    public init(commitment: BoardCommitment) {
        self.commitment = commitment
    }
}

// MARK: Preferences

/// api.BoardPreferencesParams: encodes as `{}`.
public struct BoardPreferencesParams: Codable, Sendable, Equatable {
    public init() {}
}

/// api.BoardPreferencesResult.
public struct BoardPreferencesResult: Codable, Sendable, Equatable {
    public var preferences: BoardPreferences

    public init(preferences: BoardPreferences) {
        self.preferences = preferences
    }
}

/// api.BoardSetPreferencesParams: replaces every preference; send the
/// object `board.preferences` gave with the changes.
public struct BoardSetPreferencesParams: Codable, Sendable, Equatable {
    public var preferences: BoardPreferences

    public init(preferences: BoardPreferences) {
        self.preferences = preferences
    }
}

/// api.BoardSetPreferencesResult: as stored.
public struct BoardSetPreferencesResult: Codable, Sendable, Equatable {
    public var preferences: BoardPreferences

    public init(preferences: BoardPreferences) {
        self.preferences = preferences
    }
}

// MARK: Runs

/// api.BoardRunStartParams.
public struct BoardRunStartParams: Codable, Sendable, Equatable {
    /// `.manual` or `.auto`; never `.external`.
    public var trigger: BoardTrigger
    public var source: String

    public init(trigger: BoardTrigger, source: String) {
        self.trigger = trigger
        self.source = source
    }
}

/// api.BoardRunStartResult.
public struct BoardRunStartResult: Codable, Sendable, Equatable {
    public var runId: BoardRunID

    public init(runId: BoardRunID) {
        self.runId = runId
    }
}

/// api.BoardRunEndParams.
public struct BoardRunEndParams: Codable, Sendable, Equatable {
    public var runId: BoardRunID
    /// nil = success.
    public var error: BoardRunError?
    /// The run's token usage as the assistant reported it; nil = unknown,
    /// left out.
    public var usage: BoardUsage?

    public init(runId: BoardRunID, error: BoardRunError? = nil, usage: BoardUsage? = nil) {
        self.runId = runId
        self.error = error
        self.usage = usage
    }
}

// MARK: Notification and error data

/// api.BoardChangedNotification (`notify.boardChanged`): what `board.list`
/// returns changed for these accounts; empty = any. Clients list again. The
/// notification may come without params, which `DaemonNotification` reads as
/// any account.
public struct BoardChangedNotification: Codable, Sendable, Equatable {
    @NullAsEmpty public var accountIds: [AccountID]

    public init(accountIds: [AccountID] = []) {
        self.accountIds = accountIds
    }

    /// True when the change concerns `account`.
    public func concerns(_ account: AccountID) -> Bool {
        accountIds.isEmpty || accountIds.contains(account)
    }
}

/// A notification's params that may be missing or `null`.
struct OptionalParamsEnvelope<P: Decodable>: Decodable {
    let params: P?
}

/// api.QuoteNotFoundData: `error.data` of `quoteNotFound`.
public struct QuoteNotFoundData: Codable, Sendable, Equatable {
    public var field: QuoteField

    public init(field: QuoteField) {
        self.field = field
    }
}

extension RPCError {
    /// api.QuoteNotFoundData of a `quoteNotFound` error; nil for any other
    /// code, without data or with data of another shape (no `field`).
    public var quoteNotFound: QuoteNotFoundData? {
        guard code == .quoteNotFound, let data, data.objectValue != nil,
              let raw = try? JSONCoding.encoder().encode(data),
              let d = try? JSONCoding.decoder().decode(QuoteNotFoundData.self, from: raw),
              !d.field.rawValue.isEmpty else { return nil }
        return d
    }
}
