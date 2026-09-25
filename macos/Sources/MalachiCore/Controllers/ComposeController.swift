// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

// ui/internal/compose/manager.go without the widgets: what the compose
// windows share (the client, the settings, the account list) and the list
// of open windows. Windows are made by an injected factory (the AppKit
// layer) and reached through `ComposeWindowHandle`.

/// What the manager pushes to an open compose window: the refreshed
/// account list (compose.go `setAccounts`) and a toast; and what it asks:
/// whether the window edits a draft, and to come to the front.
@MainActor
public protocol ComposeWindowHandle: AnyObject {
    func setAccounts(_ accounts: [Account], placeholder: Bool)
    func toast(_ text: String)
    /// Whether the window edits `draft`: the same saved draft, or the same
    /// Drafts message taken over (manager.go `FindDraft`).
    func edits(_ draft: Draft) -> Bool
    /// Brings the window to the front.
    func present()
}

extension ComposeWindowHandle {
    public func edits(_ draft: Draft) -> Bool { false }
    public func present() {}
}

/// compose.Manager: opens compose windows and keeps what they share.
@MainActor
public final class ComposeController {
    /// compose.dummyAccounts: the identity used while account.list lists
    /// nothing (or cannot be asked).
    public static let placeholderAccounts: [Account] = [
        Account(
            id: "acc_dummy",
            config: AccountConfig(name: "Placeholder", email: "me@example.invalid", displayName: "Malachi User"),
            enabled: true,
            state: SyncState(accountId: "acc_dummy", status: .idle)
        ),
    ]

    public let client: RPCClient
    public let settings: Settings

    /// Manager.OnSent: called with a short message when a window queued a
    /// message (e.g. to show a toast on the main window).
    public var onSent: (@MainActor (String) -> Void)?

    /// compose.newWindow + Present: builds and shows a window prefilled from
    /// the params; the manager reads `accounts`/`placeholder` for it.
    public var makeWindow: (@MainActor (ComposeParams) -> any ComposeWindowHandle)?

    private var windows: [any ComposeWindowHandle] = []
    private var known: [Account] = []
    private var fetched = false
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "compose")

    public init(client: RPCClient, settings: Settings) {
        self.client = client
        self.settings = settings
    }

    /// Manager.Accounts: the known accounts, or the placeholder while the
    /// backend cannot list any.
    public var accounts: [Account] {
        known.isEmpty ? Self.placeholderAccounts : known
    }

    /// Manager.Placeholder: whether `accounts` is the placeholder identity.
    public var placeholder: Bool { known.isEmpty }

    /// Manager.SelfAddress: the first account's address, for Reply All
    /// exclusion.
    public var selfAddress: Address {
        let a = accounts[0]
        return Address(name: a.config.displayName, address: a.config.email)
    }

    /// The open windows, in the order they were opened.
    public var openWindows: [any ComposeWindowHandle] { windows }

    /// Manager.Open: shows a new compose window prefilled from `p`. Nothing
    /// happens without a factory.
    public func open(_ p: ComposeParams) {
        guard let makeWindow else {
            log.error("compose window requested before the window factory was installed")
            return
        }
        if !fetched {
            refreshAccounts()
        }
        let w = makeWindow(p)
        windows.append(w)
        let msg = blockedSummary(p.blocked)
        if !msg.isEmpty {
            // The backend quoted the original without its remote images and
            // scripts; said once, as after a save.
            w.toast(msg)
        }
    }

    /// Manager.FindDraft: the open window that edits `draft` (from
    /// draft.open), nil when none.
    public func findDraft(_ draft: Draft) -> (any ComposeWindowHandle)? {
        windows.first { $0.edits(draft) }
    }

    /// Manager.remove: the window closed.
    public func remove(_ w: any ComposeWindowHandle) {
        windows.removeAll { $0 === w }
    }

    /// Manager.Invalidate: drops the cached account list
    /// (notify.accountsChanged). Open windows are refreshed at once;
    /// otherwise the next window fetches again.
    public func invalidate() {
        fetched = false
        if !windows.isEmpty {
            refreshAccounts()
        }
    }

    /// Manager.refreshAccounts: asks the backend once per process (until
    /// `invalidate`) and pushes the result to open windows.
    public func refreshAccounts() {
        fetched = true
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountListResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountList.self, EmptyParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                self.log.debug("account.list: \(String(describing: err), privacy: .public)")
                self.fetched = false // try again on the next window
            case .success(let res):
                self.known = res.accounts
                for w in self.windows {
                    w.setAccounts(self.accounts, placeholder: self.placeholder)
                }
            }
        }
    }
}
