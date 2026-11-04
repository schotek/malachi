// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The assistant profile (docs/api.md §4.18, §5
// notify.assistantProfileChanged; backend/pkg/api/assistant.go; the design
// is docs/plans/assistant-profile.md): what the user wrote about themselves
// for an assistant, the grammatical gender they write in, a note for each
// mail and Jira account, and the notes an assistant proposed. Every string
// is plain text the daemon cleaned; a proposal's text is an assistant's, so
// it is shown as plain text only.

import Foundation

/// api.ProfileGender: the grammatical gender the user writes in.
public struct ProfileGender: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let unset: ProfileGender = "unset"
    public static let masculine: ProfileGender = "masculine"
    public static let feminine: ProfileGender = "feminine"
    public static let neutral: ProfileGender = "neutral"

    /// The four values, in the order Settings offers them.
    public static let all: [ProfileGender] = [.unset, .masculine, .feminine, .neutral]
}

/// api.ProfileSource: where a proposal came from.
public struct ProfileSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The assistant panel of Malachi Mail.
    public static let panel: ProfileSource = "panel"
    /// Another MCP client (Claude Desktop, Claude Code).
    public static let mcp: ProfileSource = "mcp"
}

/// api.ProfileAccountNote: the note of one account.
public struct ProfileAccountNote: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var note: String

    public init(accountId: AccountID, note: String) {
        self.accountId = accountId
        self.note = note
    }
}

/// api.AssistantProfile: the stored profile. `accounts` lists only accounts
/// that exist and have a note, in account.list order.
public struct AssistantProfile: Codable, Sendable, Equatable {
    public var text: String
    public var gender: ProfileGender
    @NullAsEmpty public var accounts: [ProfileAccountNote]
    /// The MCP bridge gives the profile to MCP clients other than Malachi
    /// Mail itself. Default true.
    public var shareWithMcp: Bool
    public var updatedAt: Date?

    public init(
        text: String = "", gender: ProfileGender = .unset, accounts: [ProfileAccountNote] = [],
        shareWithMcp: Bool = true, updatedAt: Date? = nil
    ) {
        self.text = text
        self.gender = gender
        self.accounts = accounts
        self.shareWithMcp = shareWithMcp
        self.updatedAt = updatedAt
    }

    private enum CodingKeys: String, CodingKey {
        case text, gender, accounts, shareWithMcp, updatedAt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        gender = try c.decodeIfPresent(ProfileGender.self, forKey: .gender) ?? .unset
        _accounts = try c.decode(NullAsEmpty<ProfileAccountNote>.self, forKey: .accounts)
        shareWithMcp = try c.decodeIfPresent(Bool.self, forKey: .shareWithMcp) ?? true
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(text, forKey: .text)
        try c.encode(gender, forKey: .gender)
        try c.encode(accounts, forKey: .accounts)
        try c.encode(shareWithMcp, forKey: .shareWithMcp)
        try c.encodeIfPresent(updatedAt, forKey: .updatedAt)
    }
}

/// api.ProfileProposal: a note an assistant proposed; part of the profile
/// only once the user accepts it.
public struct ProfileProposal: Codable, Sendable, Equatable {
    public var id: String
    public var text: String
    /// A note for this account; nil: the global text.
    public var accountId: AccountID?
    public var source: ProfileSource
    public var createdAt: Date?

    public init(id: String, text: String, accountId: AccountID? = nil, source: ProfileSource, createdAt: Date? = nil) {
        self.id = id
        self.text = text
        self.accountId = accountId
        self.source = source
        self.createdAt = createdAt
    }
}

/// api.ProfileLimits: the limits, in characters, for a client's counters.
public struct ProfileLimits: Codable, Sendable, Equatable {
    public var text: Int
    public var accountNote: Int
    public var proposal: Int

    public init(
        text: Int = API.Limits.maxProfileText, accountNote: Int = API.Limits.maxProfileAccountNote,
        proposal: Int = API.Limits.maxProfileProposal
    ) {
        self.text = text
        self.accountNote = accountNote
        self.proposal = proposal
    }
}

public struct AssistantProfileGetResult: Codable, Sendable, Equatable {
    public var profile: AssistantProfile
    @NullAsEmpty public var proposals: [ProfileProposal]
    public var limits: ProfileLimits?

    public init(profile: AssistantProfile = AssistantProfile(), proposals: [ProfileProposal] = [], limits: ProfileLimits? = ProfileLimits()) {
        self.profile = profile
        self.proposals = proposals
        self.limits = limits
    }
}

/// api.AssistantProfileSetParams: the fields it carries change; absent ones
/// stay. `accounts` sets the note of each account it names ("" removes it).
public struct AssistantProfileSetParams: Codable, Sendable, Equatable {
    public var text: String?
    public var gender: ProfileGender?
    public var accounts: [ProfileAccountNote]?
    public var shareWithMcp: Bool?

    public init(text: String? = nil, gender: ProfileGender? = nil, accounts: [ProfileAccountNote]? = nil, shareWithMcp: Bool? = nil) {
        self.text = text
        self.gender = gender
        self.accounts = accounts
        self.shareWithMcp = shareWithMcp
    }
}

public struct AssistantProfileSetResult: Codable, Sendable, Equatable {
    public var profile: AssistantProfile
}

public struct AssistantProfileProposeParams: Codable, Sendable, Equatable {
    public var text: String
    public var accountId: AccountID?
    public var source: ProfileSource

    public init(text: String, accountId: AccountID? = nil, source: ProfileSource) {
        self.text = text
        self.accountId = accountId
        self.source = source
    }
}

public struct AssistantProfileProposeResult: Codable, Sendable, Equatable {
    public var proposal: ProfileProposal
}

/// api.AssistantProfileResolveParams: accepts (with `text`, the user's
/// edit, or as proposed) or dismisses a proposal.
public struct AssistantProfileResolveParams: Codable, Sendable, Equatable {
    public var id: String
    public var accept: Bool
    public var text: String?

    public init(id: String, accept: Bool, text: String? = nil) {
        self.id = id
        self.accept = accept
        self.text = text
    }
}

public struct AssistantProfileResolveResult: Codable, Sendable, Equatable {
    public var profile: AssistantProfile
    @NullAsEmpty public var proposals: [ProfileProposal]
}

extension API {
    public enum AssistantProfileGet: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = AssistantProfileGetResult
        public static let name = "assistant.profileGet"
    }

    public enum AssistantProfileSet: RPCMethod {
        public typealias Params = AssistantProfileSetParams
        public typealias Result = AssistantProfileSetResult
        public static let name = "assistant.profileSet"
    }

    public enum AssistantProfilePropose: RPCMethod {
        public typealias Params = AssistantProfileProposeParams
        public typealias Result = AssistantProfileProposeResult
        public static let name = "assistant.profilePropose"
    }

    public enum AssistantProfileResolve: RPCMethod {
        public typealias Params = AssistantProfileResolveParams
        public typealias Result = AssistantProfileResolveResult
        public static let name = "assistant.profileResolve"
    }

    /// The assistant profile's methods, which end methods.go's table.
    static let assistantProfileMethods: [any RPCMethod.Type] = [
        AssistantProfileGet.self, AssistantProfileSet.self, AssistantProfilePropose.self, AssistantProfileResolve.self,
    ]
}

extension API.Limits {
    /// pkg/api/assistant.go, in characters of the cleaned text.
    public static let maxProfileText = 2000
    public static let maxProfileAccountNote = 500
    public static let maxProfileProposal = 300
    public static let maxProfileProposals = 20
}
