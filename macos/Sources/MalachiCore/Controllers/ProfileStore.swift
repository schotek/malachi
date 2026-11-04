// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// Port of ui/internal/assistantpanel/profile_store.go: the application's
/// copy of the user's profile (docs/api.md §4.18). `profile` is the profile
/// with the names of its accounts for the prompts; `proposals` and `limits`
/// are for Settings → AI → About You and the panel's cards. One per
/// application (`AppState`); it reads the profile when the connection comes
/// (`refresh`) and again on notify.assistantProfileChanged and
/// notify.accountsChanged, and writes through `set` and `resolve`. A daemon
/// without the profile (an older one) leaves it empty and `supported`
/// false.
@MainActor
public final class ProfileStore {
    /// Loaded at least once (with the profile or without it).
    public private(set) var loaded = false
    /// The daemon has the profile; false until a read ended, and for an
    /// older daemon.
    public private(set) var supported = false
    /// The profile as the daemon has it.
    public private(set) var stored = AssistantProfile()
    /// The pending proposals, oldest first.
    public private(set) var proposals: [ProfileProposal] = []
    /// The daemon's limits.
    public private(set) var limits = ProfileLimits()

    private let client: RPCClient
    private var accounts: [Account] = []
    private let observers = BoardObservers()
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")
    /// Orders the answers: an older one that ends after a newer one is
    /// dropped.
    private var generation = 0

    public init(client: RPCClient) {
        self.client = client
    }

    /// Calls `f` after every change of the profile, the proposals or
    /// `supported`.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// The profile for the prompts, with the names of its accounts.
    public var profile: Assistant.Profile {
        Assistant.Profile(stored, accounts: accounts)
    }

    /// A pending proposal by id.
    public func proposal(_ id: String) -> ProfileProposal? {
        proposals.first { $0.id == id }
    }

    /// Reads the profile and the accounts again.
    public func refresh() {
        generation += 1
        let my = generation
        let client = client
        Task { [weak self] in
            let result: Result<AssistantProfileGetResult, any Error>
            var accounts: [Account]?
            do {
                result = .success(try await client.call(API.AssistantProfileGet.self, EmptyParams()))
                accounts = try? await client.call(API.AccountList.self, EmptyParams()).accounts
            } catch {
                result = .failure(error)
            }
            guard let self, my == self.generation else { return }
            self.loaded = true
            switch result {
            case .success(let r):
                self.supported = true
                self.stored = r.profile
                self.proposals = r.proposals
                self.limits = r.limits ?? ProfileLimits()
                if let accounts {
                    self.accounts = accounts
                }
            case .failure(let error):
                if Self.unsupported(error) {
                    self.supported = false
                    self.stored = AssistantProfile()
                    self.proposals = []
                    self.limits = ProfileLimits()
                    self.accounts = []
                } else {
                    // A transient failure keeps what was read before.
                    self.log.info("assistant.profileGet: \(String(describing: error), privacy: .public)")
                }
            }
            self.observers.notify()
        }
    }

    /// Writes the fields `p` carries; `completion` gets the daemon's error,
    /// nil after it stored them (the store holds the new profile then).
    public func set(_ p: AssistantProfileSetParams, completion: (@MainActor ((any Error)?) -> Void)? = nil) {
        generation += 1
        let my = generation
        let client = client
        Task { [weak self] in
            do {
                let r = try await client.call(API.AssistantProfileSet.self, p)
                guard let self else { return }
                if my == self.generation {
                    self.stored = r.profile
                    self.observers.notify()
                }
                completion?(nil)
            } catch {
                completion?(error)
            }
        }
    }

    /// Accepts (with `text`, the user's edit, or as proposed) or dismisses
    /// a proposal; `completion` gets the daemon's error.
    public func resolve(_ id: String, accept: Bool, text: String? = nil, completion: (@MainActor ((any Error)?) -> Void)? = nil) {
        generation += 1
        let my = generation
        let client = client
        Task { [weak self] in
            do {
                let r = try await client.call(API.AssistantProfileResolve.self, AssistantProfileResolveParams(id: id, accept: accept, text: text))
                guard let self else { return }
                if my == self.generation {
                    self.stored = r.profile
                    self.proposals = r.proposals
                    self.observers.notify()
                }
                completion?(nil)
            } catch {
                completion?(error)
            }
        }
    }

    /// The error of a daemon without the profile.
    static func unsupported(_ error: any Error) -> Bool {
        guard let e = error as? RPCError else { return false }
        return e.code == .methodNotFound || e.code == .notImplemented
    }
}
