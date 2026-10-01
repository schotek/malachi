// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Threads (docs/api.md §4.4; types.go "Threads"). Computed per account,
// listed per folder: every field of a summary a folder listing returns
// describes the members in that folder, except `folderIds`.

import Foundation

/// api.ThreadSummary.
public struct ThreadSummary: Codable, Sendable, Equatable {
    public var id: ThreadID
    public var accountId: AccountID
    /// The newest member's, Re:/Fwd: stripped.
    public var subject: String
    /// Distinct senders, newest first, at most `API.Limits.maxThreadParticipants`.
    @NullAsEmpty public var participants: [Address]
    public var messageCount: Int
    public var unreadCount: Int
    public var latestDate: Date
    /// The newest member in scope, in full.
    public var latest: MessageSummary
    /// From the latest member.
    public var snippet: String
    /// Union of member flags; read state comes from `unreadCount`.
    @NullAsEmpty public var flags: [Flag]
    public var hasAttachments: Bool
    /// Every folder of the account with at least one member, whatever the scope.
    @NullAsEmpty public var folderIds: [FolderID]
    /// Present only for a thread of a jira account, which is one issue;
    /// `latest` may then be an event row.
    public var issue: IssueInfo?
    /// In a folder's scope: how many members of the thread in the
    /// account's folders of role `sent` the folder lacks (the user's own
    /// replies; compared by Message-ID as well), part of no other field.
    /// 0 in a sent folder, the outbox, a jira account and the account-wide
    /// summary of thread.get. Absent from an older daemon: 0.
    public var sentCount: Int

    public init(
        id: ThreadID, accountId: AccountID, subject: String, participants: [Address], messageCount: Int,
        unreadCount: Int, latestDate: Date, latest: MessageSummary, snippet: String, flags: [Flag],
        hasAttachments: Bool, folderIds: [FolderID], issue: IssueInfo? = nil, sentCount: Int = 0
    ) {
        self.id = id
        self.accountId = accountId
        self.subject = subject
        self.participants = participants
        self.messageCount = messageCount
        self.unreadCount = unreadCount
        self.latestDate = latestDate
        self.latest = latest
        self.snippet = snippet
        self.flags = flags
        self.hasAttachments = hasAttachments
        self.folderIds = folderIds
        self.issue = issue
        self.sentCount = sentCount
    }

    private enum CodingKeys: String, CodingKey {
        case id, accountId, subject, participants, messageCount, unreadCount, latestDate, latest, snippet, flags,
             hasAttachments, folderIds, issue, sentCount
    }

    /// The synthesised decoding would refuse a summary without
    /// `sentCount` (a daemon from before the field, 2026-10-01); it is 0
    /// then. Everything else decodes as before. The encoding stays
    /// synthesised and always writes the key.
    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(ThreadID.self, forKey: .id)
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        subject = try c.decode(String.self, forKey: .subject)
        _participants = try c.decode(NullAsEmpty<Address>.self, forKey: .participants)
        messageCount = try c.decode(Int.self, forKey: .messageCount)
        unreadCount = try c.decode(Int.self, forKey: .unreadCount)
        latestDate = try c.decode(Date.self, forKey: .latestDate)
        latest = try c.decode(MessageSummary.self, forKey: .latest)
        snippet = try c.decode(String.self, forKey: .snippet)
        _flags = try c.decode(NullAsEmpty<Flag>.self, forKey: .flags)
        hasAttachments = try c.decode(Bool.self, forKey: .hasAttachments)
        _folderIds = try c.decode(NullAsEmpty<FolderID>.self, forKey: .folderIds)
        issue = try c.decodeIfPresent(IssueInfo.self, forKey: .issue)
        sentCount = try c.decodeIfPresent(Int.self, forKey: .sentCount) ?? 0
    }
}

/// api.ThreadListParams: a thread is unread or flagged when any member in
/// the folder is.
public struct ThreadListParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var folderId: FolderID
    public var page: Page
    public var sort: SortOrder?
    public var filter: MessageFilter?

    public init(
        accountId: AccountID, folderId: FolderID, page: Page = Page(), sort: SortOrder? = nil,
        filter: MessageFilter? = nil
    ) {
        self.accountId = accountId
        self.folderId = folderId
        self.page = page
        self.sort = sort
        self.filter = filter
    }
}

/// api.ThreadListResult.
public struct ThreadListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var threads: [ThreadSummary]
    public var page: PageInfo

    public init(threads: [ThreadSummary], page: PageInfo) {
        self.threads = threads
        self.page = page
    }
}

/// api.ThreadGetParams. `folderId` restricts the members and the summary to
/// one folder; nil = every member of the account. `withSent`, with
/// `folderId`, also returns the members `ThreadSummary.sentCount` counts
/// (`ThreadGetResult.sent`); ignored without it, left out when nil.
public struct ThreadGetParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var threadId: ThreadID
    public var folderId: FolderID?
    public var withSent: Bool?

    public init(accountId: AccountID, threadId: ThreadID, folderId: FolderID? = nil, withSent: Bool? = nil) {
        self.accountId = accountId
        self.threadId = threadId
        self.folderId = folderId
        self.withSent = withSent
    }
}

/// api.ThreadGetResult: `messages` oldest first, at most
/// `API.Limits.maxThreadMessages` (the newest). `sent` (only with
/// `withSent` and `folderId`; empty when the daemon leaves it out) are
/// the user's replies in the account's sent folders that the folder lacks,
/// one per Message-ID, oldest first, at most `API.Limits.maxThreadMessages`
/// (the newest), each with its sent folder's `folderId`: not members of the
/// folder and in none of `thread`'s aggregates.
public struct ThreadGetResult: Codable, Sendable, Equatable {
    public var thread: ThreadSummary
    @NullAsEmpty public var messages: [MessageSummary]
    @NullAsEmpty public var sent: [MessageSummary]

    public init(thread: ThreadSummary, messages: [MessageSummary], sent: [MessageSummary] = []) {
        self.thread = thread
        self.messages = messages
        self.sent = sent
    }
}
