// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The calendar and the contacts of a mail account (docs/plans/
// account-services.md): which of them an account can sync, the wizard's
// offer to switch them on before the first sign-in, and the fallback offer
// in the Calendar and the Contacts window for an account that can sync one
// and has it off, until the user says Not Now.
//
// This is the reference of the rules: Go (ui/internal/accountservices),
// C# (Malachi.Core/AccountServices) and Kotlin (core/.../accountservices)
// port it 1:1 with its tests (AccountServicesTests). It is pure: no AppKit,
// no RPC, no settings; the callers pass the account and the dismissals of
// the key account-services-dismissed.
//
// Only a graph account offers anything today: its capabilities say what it
// can have (`calendar`, `contacts` always), its configuration whether it is
// on (`calendar.sync`, `graph.contacts.sync`; nil is off). A CalDAV account
// decides both in its own wizard; an IMAP account (Gmail included) has
// neither yet.

import Foundation

public enum AccountServices {
    /// What an account can sync besides its mail.
    public enum Service: String, Sendable, CaseIterable {
        case calendar
        case contacts

        /// The capability that says the account can have it.
        public var capability: Capability {
            switch self {
            case .calendar: return .calendar
            case .contacts: return .contacts
            }
        }
    }

    /// The services the account can sync and the wizard and the fallback
    /// offer: an account of kind graph with the capability. `capabilities`
    /// nil (a daemon from before them) is `API.mailCapabilities`, which has
    /// neither.
    public static func offered(_ cfg: AccountConfig, capabilities: [Capability]?) -> [Service] {
        guard cfg.protocolKind == .graph else { return [] }
        let caps = capabilities ?? API.mailCapabilities
        return Service.allCases.filter { caps.contains($0.capability) }
    }

    /// `offered` for an account not added yet (the wizard has no
    /// capabilities): the daemon gives every graph account both
    /// (capabilitiesFor).
    public static func offeredForNew(_ cfg: AccountConfig) -> [Service] {
        offered(cfg, capabilities: [.calendar, .contacts])
    }

    /// The service's switch in the configuration: `calendar.sync`,
    /// `graph.contacts.sync` (or a CalDAV account's `caldav.contacts`,
    /// `ContactBook.syncOn`).
    public static func isOn(_ cfg: AccountConfig, _ s: Service) -> Bool {
        switch s {
        case .calendar: return cfg.calendar?.sync ?? false
        case .contacts: return ContactBook.syncOn(cfg)
        }
    }

    /// `cfg` with the services of `on` switched on (true) or off (false);
    /// a service not in `on` stays as it is. Only a graph account changes.
    /// Switching on writes the same as Preferences
    /// (`CalendarAccountController.setSync`, `ContactBook.withSync`): the
    /// calendar keeps its other settings. Switching off leaves an absent
    /// part absent (nil is off already).
    public static func apply(_ cfg: AccountConfig, on: [Service: Bool]) -> AccountConfig {
        guard cfg.protocolKind == .graph else { return cfg }
        var cfg = cfg
        for s in Service.allCases {
            guard let want = on[s] else { continue }
            switch s {
            case .calendar:
                if want || cfg.calendar != nil {
                    var cal = cfg.calendar ?? CalendarConfig(sync: want)
                    cal.sync = want
                    cfg.calendar = cal
                }
            case .contacts:
                if want || cfg.graph?.contacts != nil {
                    cfg = ContactBook.withSync(cfg, want)
                }
            }
        }
        return cfg
    }

    /// The services the account can sync and has off (the fallback offer).
    public static func off(_ cfg: AccountConfig, capabilities: [Capability]?) -> [Service] {
        offered(cfg, capabilities: capabilities).filter { !isOn(cfg, $0) }
    }

    /// The entry of account-services-dismissed for Not Now of `s` on the
    /// account: "<account id>:<service>".
    public static func dismissKey(_ id: AccountID, _ s: Service) -> String {
        "\(id.rawValue):\(s.rawValue)"
    }

    /// Whether the Calendar (`.calendar`) or the Contacts window
    /// (`.contacts`) offers to switch the service on for the account: it is
    /// enabled, can sync the service, has it off, and Not Now was not said.
    public static func showPrompt(_ account: Account, _ s: Service, dismissed: [String]) -> Bool {
        account.enabled && off(account.config, capabilities: account.capabilities).contains(s)
            && !dismissed.contains(dismissKey(account.id, s))
    }

    /// The accounts the Calendar or the Contacts window offers the service
    /// for, in their order.
    public static func prompts(_ accounts: [Account], _ s: Service, dismissed: [String]) -> [Account] {
        accounts.filter { showPrompt($0, s, dismissed: dismissed) }
    }

    /// `dismissed` with Not Now of `s` on the account added (once).
    public static func dismissing(_ dismissed: [String], _ id: AccountID, _ s: Service) -> [String] {
        let key = dismissKey(id, s)
        return dismissed.contains(key) ? dismissed : dismissed + [key]
    }

    /// `dismissed` without the entries of accounts that no longer exist
    /// (an entry that is not "<id>:<service>" goes too); unchanged when
    /// nothing goes.
    public static func pruned(_ dismissed: [String], accounts: [AccountID]) -> [String] {
        let ids = Set(accounts.map(\.rawValue))
        return dismissed.filter { e in
            guard let i = e.lastIndex(of: ":") else { return false }
            return ids.contains(String(e[..<i])) && Service(rawValue: String(e[e.index(after: i)...])) != nil
        }
    }

    // MARK: Texts

    /// The wizard's group over the switches.
    public static var groupTitle: String { L10n.T("Also Sync from This Account") }

    /// The wizard's switch of a service (Preferences' wording).
    public static func switchTitle(_ s: Service) -> String {
        switch s {
        case .calendar: return L10n.T("Sync This Account’s Calendars")
        case .contacts: return L10n.T("Sync Contacts")
        }
    }

    /// The note under the wizard's group.
    public static var laterNote: String { L10n.T("You can change this later in Preferences.") }

    /// The fallback offer's sentence for the account's name.
    public static func offLine(_ s: Service, account: String) -> String {
        switch s {
        case .calendar: return L10n.T("Calendar sync is off for %s", account)
        case .contacts: return L10n.T("Contacts sync is off for %s", account)
        }
    }

    /// The fallback offer's buttons.
    public static var turnOn: String { L10n.T("Turn On") }
    public static var notNow: String { L10n.T("Not Now") }

    /// The wizard's offer after a sign-in that asked for a service failed.
    public static var mailOnly: String { L10n.T("Try Again with Mail Only") }
}
