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

    public init(
        id: ThreadID, accountId: AccountID, subject: String, participants: [Address], messageCount: Int,
        unreadCount: Int, latestDate: Date, latest: MessageSummary, snippet: String, flags: [Flag],
        hasAttachments: Bool, folderIds: [FolderID]
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
/// one folder; nil = every member of the account.
public struct ThreadGetParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var threadId: ThreadID
    public var folderId: FolderID?

    public init(accountId: AccountID, threadId: ThreadID, folderId: FolderID? = nil) {
        self.accountId = accountId
        self.threadId = threadId
        self.folderId = folderId
    }
}

/// api.ThreadGetResult: `messages` oldest first, at most
/// `API.Limits.maxThreadMessages` (the newest).
public struct ThreadGetResult: Codable, Sendable, Equatable {
    public var thread: ThreadSummary
    @NullAsEmpty public var messages: [MessageSummary]

    public init(thread: ThreadSummary, messages: [MessageSummary]) {
        self.thread = thread
        self.messages = messages
    }
}
