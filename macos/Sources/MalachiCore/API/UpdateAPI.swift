// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The update check (docs/api.md §4.20, §5 notify.updateChanged;
// backend/pkg/api/update.go; the design is docs/plans/update-check.md, the
// service docs/services.md §2). The daemon asks the project's signed
// manifest which versions exist and offers the newest one for the package
// the build was installed from. It never downloads or installs anything: a
// client opens the package's URL in the browser. Every string is plain text
// the daemon checked; it is shown as plain text only.

import Foundation

/// api.UpdateState: what the update check shows.
public struct UpdateState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The user turned the check off.
    public static let disabled: UpdateState = "disabled"
    /// The build has no channel or its version is not a plain
    /// MAJOR.MINOR.PATCH.
    public static let unsupported: UpdateState = "unsupported"
    /// No check has finished yet.
    public static let idle: UpdateState = "idle"
    /// A check is running.
    public static let checking: UpdateState = "checking"
    /// The last check found nothing newer.
    public static let upToDate: UpdateState = "upToDate"
    /// A newer version is offered (`available`).
    public static let available: UpdateState = "available"
    /// The last check failed (`failure`) and nothing is offered.
    public static let failed: UpdateState = "failed"
}

/// api.UpdateFailure: the class of a failed check.
public struct UpdateFailure: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let network: UpdateFailure = "network"
    public static let server: UpdateFailure = "server"
    public static let signature: UpdateFailure = "signature"
    public static let format: UpdateFailure = "format"
}

/// api.UpdateDismissAction: what update.dismiss does with an offered
/// version.
public struct UpdateDismissAction: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Never offer this version again (not a security release).
    public static let skip: UpdateDismissAction = "skip"
    /// Offer it again after `API.Limits.updateRemindDelay` (not a security
    /// release).
    public static let later: UpdateDismissAction = "later"
    /// A client showed the question of a security release; no client asks
    /// again for this version.
    public static let prompted: UpdateDismissAction = "prompted"
}

/// api.UpdateInfo: the version offered. `url` and `infoUrl` are https on
/// the project's hosts (the daemon checked them; `Updates.openableURL`
/// checks again).
public struct UpdateInfo: Codable, Sendable, Equatable {
    public var version: String
    public var publishedAt: Date
    /// This release, or one it skips over, fixes a security problem:
    /// neither skip nor later is accepted, and the banner stays.
    public var security: Bool
    /// The release notes in the language the client asked for, else
    /// English: plain text.
    public var notes: String
    public var infoUrl: String
    /// The package for the build's channel; `size` its bytes.
    public var url: String
    public var size: Int64
    /// The user skipped this version (never with `security`).
    public var skipped: Bool
    /// The user asked to be reminded later; absent once it passed.
    public var remindAfter: Date?
    /// A security release no client has asked about yet.
    public var prompt: Bool

    public init(
        version: String, publishedAt: Date, security: Bool = false, notes: String = "", infoUrl: String = "",
        url: String = "", size: Int64 = 0, skipped: Bool = false, remindAfter: Date? = nil, prompt: Bool = false
    ) {
        self.version = version
        self.publishedAt = publishedAt
        self.security = security
        self.notes = notes
        self.infoUrl = infoUrl
        self.url = url
        self.size = size
        self.skipped = skipped
        self.remindAfter = remindAfter
        self.prompt = prompt
    }

    private enum CodingKeys: String, CodingKey {
        case version, publishedAt, security, notes, infoUrl, url, size, skipped, remindAfter, prompt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        publishedAt = try c.decodeIfPresent(Date.self, forKey: .publishedAt) ?? Date(timeIntervalSince1970: 0)
        security = try c.decodeIfPresent(Bool.self, forKey: .security) ?? false
        notes = try c.decodeIfPresent(String.self, forKey: .notes) ?? ""
        infoUrl = try c.decodeIfPresent(String.self, forKey: .infoUrl) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        size = try c.decodeIfPresent(Int64.self, forKey: .size) ?? 0
        skipped = try c.decodeIfPresent(Bool.self, forKey: .skipped) ?? false
        remindAfter = try c.decodeIfPresent(Date.self, forKey: .remindAfter)
        prompt = try c.decodeIfPresent(Bool.self, forKey: .prompt) ?? false
    }
}

/// api.UpdateNotice: a message of the project to the users of this
/// version, shown once per id.
public struct UpdateNotice: Codable, Sendable, Equatable {
    public var id: String
    public var text: String
    public var url: String?

    public init(id: String, text: String, url: String? = nil) {
        self.id = id
        self.text = text
        self.url = url
    }
}

/// api.UpdateStatus: the whole state a client shows.
public struct UpdateStatus: Codable, Sendable, Equatable {
    /// The user's preference (default true).
    public var enabled: Bool
    /// The package the build was installed from; absent for a build
    /// without one.
    public var channel: String?
    public var current: String
    public var state: UpdateState
    /// When a check last brought an answer.
    public var checkedAt: Date?
    /// The last check failed; absent once one succeeds.
    public var failure: UpdateFailure?
    public var available: UpdateInfo?
    public var notice: UpdateNotice?

    public init(
        enabled: Bool = true, channel: String? = nil, current: String = "", state: UpdateState = .idle,
        checkedAt: Date? = nil, failure: UpdateFailure? = nil, available: UpdateInfo? = nil, notice: UpdateNotice? = nil
    ) {
        self.enabled = enabled
        self.channel = channel
        self.current = current
        self.state = state
        self.checkedAt = checkedAt
        self.failure = failure
        self.available = available
        self.notice = notice
    }

    private enum CodingKeys: String, CodingKey {
        case enabled, channel, current, state, checkedAt, failure, available, notice
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        channel = try c.decodeIfPresent(String.self, forKey: .channel)
        current = try c.decodeIfPresent(String.self, forKey: .current) ?? ""
        state = try c.decodeIfPresent(UpdateState.self, forKey: .state) ?? .idle
        checkedAt = try c.decodeIfPresent(Date.self, forKey: .checkedAt)
        failure = try c.decodeIfPresent(UpdateFailure.self, forKey: .failure)
        available = try c.decodeIfPresent(UpdateInfo.self, forKey: .available)
        notice = try c.decodeIfPresent(UpdateNotice.self, forKey: .notice)
    }
}

// Every update method takes the client's locale ("cs", "cs-CZ") for the
// texts of the status it returns; absent: English.

public struct UpdateStatusParams: Codable, Sendable, Equatable {
    public var locale: String?

    public init(locale: String? = nil) {
        self.locale = locale
    }
}

public struct UpdateCheckParams: Codable, Sendable, Equatable {
    public var locale: String?

    public init(locale: String? = nil) {
        self.locale = locale
    }
}

public struct UpdateSetPrefsParams: Codable, Sendable, Equatable {
    public var enabled: Bool?
    public var locale: String?

    public init(enabled: Bool? = nil, locale: String? = nil) {
        self.enabled = enabled
        self.locale = locale
    }
}

public struct UpdateDismissParams: Codable, Sendable, Equatable {
    public var version: String
    public var action: UpdateDismissAction
    public var locale: String?

    public init(version: String, action: UpdateDismissAction, locale: String? = nil) {
        self.version = version
        self.action = action
        self.locale = locale
    }
}

public struct UpdateDismissNoticeParams: Codable, Sendable, Equatable {
    public var id: String
    public var locale: String?

    public init(id: String, locale: String? = nil) {
        self.id = id
        self.locale = locale
    }
}

extension API {
    /// update.status. (Not `UpdateStatus`: that is the status type.)
    public enum UpdateGetStatus: RPCMethod {
        public typealias Params = UpdateStatusParams
        public typealias Result = UpdateStatus
        public static let name = "update.status"
    }

    /// update.check: the daemon checks before it answers.
    public enum UpdateCheck: RPCMethod {
        public typealias Params = UpdateCheckParams
        public typealias Result = UpdateStatus
        public static let name = "update.check"
        public static let timeout = RPCTimeouts.updateCheck
    }

    public enum UpdateSetPrefs: RPCMethod {
        public typealias Params = UpdateSetPrefsParams
        public typealias Result = UpdateStatus
        public static let name = "update.setPrefs"
    }

    public enum UpdateDismiss: RPCMethod {
        public typealias Params = UpdateDismissParams
        public typealias Result = UpdateStatus
        public static let name = "update.dismiss"
    }

    public enum UpdateDismissNotice: RPCMethod {
        public typealias Params = UpdateDismissNoticeParams
        public typealias Result = UpdateStatus
        public static let name = "update.dismissNotice"
    }

    /// The update check's methods, which end methods.go's table.
    static let updateMethods: [any RPCMethod.Type] = [
        UpdateGetStatus.self, UpdateCheck.self, UpdateSetPrefs.self, UpdateDismiss.self, UpdateDismissNotice.self,
    ]
}

extension API.Limits {
    /// api.UpdateRemindDelay: how long `later` hides a version.
    public static let updateRemindDelay: TimeInterval = 72 * 3600
    /// api.UpdateCheckInterval: update.check within it returns the status
    /// without checking.
    public static let updateCheckInterval: TimeInterval = 60
}
