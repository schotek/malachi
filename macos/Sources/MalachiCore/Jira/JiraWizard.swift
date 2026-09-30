// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/jira/wizard.go: the texts and rules of the assistant that
// adds a Jira account. It has three pages: the site (account.detectSite),
// the credentials (checked by account.listSpaces, which also lists the
// spaces) and the spaces with the offline window (account.add). Editing an
// account opens on the credentials page and saves with account.update.
// The controller that drives the pages uses these; nothing here calls the
// daemon.

import Foundation

extension Jira {
    /// jira.Page: a page of the Jira account assistant, in order.
    public enum Page: Int, Sendable, Equatable {
        case site
        case credentials
        case spaces
    }

    /// jira.Step: a call of the assistant that can fail.
    public enum Step: Int, Sendable, Equatable {
        /// account.detectSite, from the site page.
        case detect
        /// account.listSpaces, from the credentials page.
        case spaces
        /// account.add, or account.update when editing.
        case save
    }

    /// jira.TokenHelpURL: where a Jira Cloud user creates an API token.
    /// Data Center has no such page: its personal access tokens are made
    /// in the user's profile on the site.
    public static let tokenHelpURL = "https://id.atlassian.com/manage-profile/security/api-tokens"

    /// jira.SitePlaceholder: the example in the empty site address field;
    /// not translated.
    public static let sitePlaceholder = "example.atlassian.net"

    /// jira.maxNameBytes: the daemon's limit of `AccountConfig.name`.
    static let maxNameBytes = 256

    /// jira.WizardStrings: the fixed texts of the Jira account assistant.
    public struct WizardStrings: Sendable, Equatable {
        /// The menu item (with a mnemonic) and the window's title.
        public var addMenu = ""
        public var title = ""
        /// The site page: its title, the explanation, the field and the
        /// progress while the site is looked up.
        public var siteTitle = ""
        public var siteDescription = ""
        public var siteAddress = ""
        public var lookingUp = ""
        /// The spaces page: the progress while the spaces load, the title,
        /// the explanation, the text of an empty list.
        public var loadingSpaces = ""
        public var spacesTitle = ""
        public var spacesDescription = ""
        public var noSpaces = ""
        /// The switch that keeps only the user's issues, with its subtitle.
        public var onlyMine = ""
        public var onlyMineSubtitle = ""
        /// The offline window's row, with its subtitle.
        public var keepOffline = ""
        public var keepOfflineSubtitle = ""
        /// The progress of account.add and of account.update.
        public var adding = ""
        public var saving = ""
    }

    /// jira.WizardTexts: the fixed texts, translated.
    public static func wizardTexts() -> WizardStrings {
        WizardStrings(
            // TRANSLATORS: menu item; "Jira" is a product name.
            addMenu: L10n.T("Add _Jira Account…"),
            title: L10n.T("Add Jira Account"),
            // TRANSLATORS: title of the page that asks for the address of a Jira installation.
            siteTitle: L10n.T("Jira Site"),
            siteDescription: L10n.T("Enter the address of your Jira site. Malachi Mail finds out whether it runs in the cloud or in your company's data center."),
            siteAddress: L10n.T("Site Address"),
            lookingUp: L10n.T("Looking up the Jira site"),
            loadingSpaces: L10n.T("Loading the spaces"),
            // TRANSLATORS: Jira projects, which Jira calls spaces.
            spacesTitle: L10n.C("jira", "Spaces"),
            spacesDescription: L10n.T("Choose the spaces whose issues appear as folders."),
            noSpaces: L10n.T("No spaces are visible to this account"),
            onlyMine: L10n.T("Only Issues Involving Me"),
            onlyMineSubtitle: L10n.T("Assigned to you, reported by you or watched by you"),
            keepOffline: L10n.T("Keep Issues Offline For"),
            // TRANSLATORS: subtitle of "Keep Issues Offline For".
            keepOfflineSubtitle: L10n.T("Older issues stay on the site and are not shown"),
            adding: L10n.T("Adding the account"),
            saving: L10n.T("Saving the account")
        )
    }

    /// jira.CredentialPage: how the credentials page asks for the sign-in
    /// of a deployment: Jira Cloud wants the Atlassian account's e-mail
    /// address and an API token, Data Center a personal access token alone.
    public struct CredentialPage: Sendable, Equatable {
        /// Whether the e-mail field is shown, and its label.
        public var showsLogin = false
        public var loginLabel = ""
        /// The secret field's label, and where the token comes from.
        public var tokenLabel = ""
        public var help = ""
        /// `helpButton` opens `helpURL`; both "" when the deployment has
        /// no such page.
        public var helpButton = ""
        public var helpURL = ""
        /// The page's banner when a token is needed and none is stored
        /// (editing an account whose token is missing).
        public var tokenPrompt = ""
        /// The page's banner when the site refused the token.
        public var rejected = ""
    }

    /// jira.CredentialFields: the credentials page of a deployment; an
    /// unknown one is treated as Data Center.
    public static func credentialFields(_ d: JiraDeployment) -> CredentialPage {
        var p = CredentialPage(
            loginLabel: L10n.T("E-mail Address"),
            rejected: L10n.T("The Jira site rejected the token")
        )
        if d == .cloud {
            p.showsLogin = true
            p.tokenLabel = L10n.T("API Token")
            p.help = L10n.T("Create an API token for Malachi Mail in your Atlassian account, then paste it here.")
            // TRANSLATORS: button that opens id.atlassian.com in the browser.
            p.helpButton = L10n.T("Create API Token…")
            p.helpURL = tokenHelpURL
            p.tokenPrompt = L10n.T("Enter the API token for this account")
            return p
        }
        p.tokenLabel = L10n.T("Personal Access Token")
        p.help = L10n.T("Create a personal access token in your Jira profile, then paste it here.")
        p.tokenPrompt = L10n.T("Enter the personal access token for this account")
        return p
    }

    /// jira.NeedsEmail: a Data Center sign-in whose user the site does not
    /// give an e-mail address: the assistant asks for it (the account's
    /// address). A Jira Cloud account's address is its login.
    public static func needsEmail(_ d: JiraDeployment, _ user: SiteUser) -> Bool {
        d != .cloud && trimSpace(user.email ?? "").isEmpty
    }

    /// jira.CheckSiteInput: checks what the user typed as the site's
    /// address before account.detectSite: a host ("acme.atlassian.net") or
    /// an http(s) URL. `ok` enables Next; `problem` is the text under the
    /// field, "" while the field is empty or fine. The daemon checks the
    /// address again.
    public static func checkSiteInput(_ raw: String) -> (ok: Bool, problem: String) {
        var s = trimSpace(raw)
        if s.isEmpty {
            return (false, "")
        }
        func bad() -> (ok: Bool, problem: String) {
            (false, L10n.T("This is not a web address"))
        }
        for r in s.unicodeScalars {
            if r.properties.isWhitespace || r == "\\" {
                return bad()
            }
            switch r.properties.generalCategory {
            case .control, .format:
                return bad()
            default:
                continue
            }
        }
        if let i = s.range(of: "://", options: .literal) {
            let scheme = s[..<i.lowerBound].lowercased()
            if scheme != "https" && scheme != "http" {
                return bad()
            }
        } else {
            s = "https://" + s
        }
        guard let u = parseURL(s), !u.host.isEmpty, !u.hasUser, !u.hostname.isEmpty else { return bad() }
        return (true, "")
    }

    /// jira.Detected: the text under the site field once
    /// account.detectSite answered: the site's title (its deployment's name
    /// when it has none), and for Data Center its version.
    public static func detected(_ res: AccountDetectSiteResult) -> String {
        var title = clean(res.title ?? "")
        if title.isEmpty {
            title = deploymentName(res.deployment)
        }
        let v = clean(res.version ?? "")
        if !v.isEmpty && res.deployment == .datacenter {
            // TRANSLATORS: the first %s is the name of a Jira site, the second its version ("9.12.4").
            return L10n.T("Found %s, version %s", title, v)
        }
        // TRANSLATORS: %s is the name of a Jira site.
        return L10n.T("Found %s", title)
    }

    /// jira.DefaultAccountName: the name of a new account: the site's
    /// title, else its host, else "Jira".
    public static func defaultAccountName(_ res: AccountDetectSiteResult) -> String {
        var name = clean(res.title ?? "")
        if name.isEmpty, let u = parseURL(trimSpace(res.siteUrl)) {
            name = clean(u.hostname.lowercased())
        }
        if name.isEmpty {
            return "Jira"
        }
        return truncate(name, maxNameBytes)
    }

    /// jira.SpaceTitle: a space as the spaces page lists it: "KEY – Name",
    /// or whichever of the two it has.
    public static func spaceTitle(_ s: Space) -> String {
        let key = clean(s.key)
        let name = clean(s.name)
        if key.isEmpty {
            return name
        }
        if name.isEmpty {
            return key
        }
        return key + " – " + name
    }

    /// jira.ApproxCount: the estimate of a space's issues in the offline
    /// window (`Space.issues`); "" when the daemon did not count (-1).
    public static func approxCount(_ n: Int) -> String {
        if n < 0 {
            return ""
        }
        // TRANSLATORS: an estimate of the issues of a Jira space.
        return L10n.N("about %d issue", "about %d issues", n)
    }

    /// jira.SpaceRow: one space of the spaces page: a check box with
    /// `title` and the estimate `count` ("" when not counted).
    public struct SpaceRow: Sendable, Equatable {
        public var id = ""
        public var title = ""
        public var count = ""
        public var serviceDesk = false
    }

    /// jira.SpaceRows: the spaces of account.listSpaces for the spaces
    /// page, in the daemon's order.
    public static func spaceRows(_ spaces: [Space]) -> [SpaceRow] {
        spaces.map { s in
            SpaceRow(id: s.id, title: spaceTitle(s), count: approxCount(s.issues), serviceDesk: s.serviceDesk ?? false)
        }
    }

    /// jira.SpacesProblem: why the spaces page cannot add the account with
    /// `selected` spaces chosen; "" when it can.
    public static func spacesProblem(_ selected: Int) -> String {
        if selected <= 0 {
            return L10n.T("Select at least one space")
        }
        if selected > API.Limits.maxJiraSpaces {
            return L10n.N("Select at most %d space", "Select at most %d spaces", API.Limits.maxJiraSpaces)
        }
        return ""
    }

    /// jira.OfflineChoices: the offline windows the assistant offers, in
    /// days: 1 week, 1 month, 3 months, 1 year. There is no "Everything": a
    /// Jira account keeps at most `API.Limits.maxJiraOfflineDays`.
    public static let offlineChoices = [7, 30, 90, 365]

    /// jira.OfflineChoiceLabels: the labels of `offlineChoices`, in order.
    public static func offlineChoiceLabels() -> [String] {
        [L10n.T("1 week"), L10n.T("1 month"), L10n.T("3 months"), L10n.T("1 year")]
    }

    /// jira.IndexOfOfflineDays: the position in `offlineChoices` shown for
    /// `JiraConfig.offlineDays`: 0 (or less) is the default window
    /// (`API.Limits.defaultJiraOfflineDays`), any other value the nearest
    /// choice, a tie going to the shorter.
    public static func indexOfOfflineDays(_ days: Int) -> Int {
        let days = days <= 0 ? API.Limits.defaultJiraOfflineDays : days
        var best = 0
        var bestDiff = -1
        for (i, v) in offlineChoices.enumerated() {
            let diff = abs(v - days)
            if bestDiff < 0 || diff < bestDiff {
                best = i
                bestDiff = diff
            }
        }
        return best
    }

    /// jira.Setup: what the assistant collected for a new account.
    public struct Setup: Sendable, Equatable {
        public var site: AccountDetectSiteResult
        /// The Atlassian account's e-mail address (Jira Cloud), which is
        /// also the account's address.
        public var login: String
        /// The account's address on Data Center: the signed-in user's
        /// (account.listSpaces), or what the user typed when the site
        /// hides it.
        public var email: String
        /// The account's name; "" = `defaultAccountName`.
        public var name: String
        /// The chosen spaces, in the order shown.
        public var spaces: [Space]
        public var onlyMine: Bool
        /// The offline window; 0 = `API.Limits.defaultJiraOfflineDays`.
        public var offlineDays: Int

        /// Go's zero value for everything left out (an empty site).
        public init(
            site: AccountDetectSiteResult = AccountDetectSiteResult(kind: "", siteUrl: "", deployment: ""),
            login: String = "", email: String = "", name: String = "", spaces: [Space] = [], onlyMine: Bool = false,
            offlineDays: Int = 0
        ) {
            self.site = site
            self.login = login
            self.email = email
            self.name = name
            self.spaces = spaces
            self.onlyMine = onlyMine
            self.offlineDays = offlineDays
        }

        /// jira.Setup.Config: the `AccountConfig` of account.add (and of
        /// account.listSpaces, which accepts it without spaces). Go's empty
        /// values are nil here, as `omitempty` leaves them off the wire.
        public func config() -> AccountConfig {
            let days = min(max(offlineDays, 0), API.Limits.maxJiraOfflineDays)
            var jc = JiraConfig(
                siteUrl: site.siteUrl,
                deployment: site.deployment,
                cloudId: nonEmpty(site.cloudId ?? ""),
                spaces: spaces.map { SpaceRef(id: $0.id, key: $0.key, name: nonEmpty($0.name)) },
                offlineDays: days == 0 ? nil : days,
                onlyMine: onlyMine ? true : nil
            )
            var email = Jira.trimSpace(self.email)
            if site.deployment == .cloud {
                let login = Jira.trimSpace(self.login)
                jc.login = nonEmpty(login)
                email = login
            } else {
                jc.cloudId = nil
            }
            var name = Jira.truncate(Jira.trimSpace(self.name), Jira.maxNameBytes)
            if name.isEmpty {
                name = Jira.defaultAccountName(site)
            }
            return AccountConfig(name: name, email: email, kind: .jira, jira: jc)
        }

        /// Go's "" as nil.
        private func nonEmpty(_ s: String) -> String? {
            s.isEmpty ? nil : s
        }
    }

    /// jira.ErrorClass: the errors of the assistant's calls sorted by what
    /// the user can do about them.
    public enum ErrorClass: Int, Sendable, Equatable, CaseIterable {
        /// Anything else, also no reply (disconnected, timed out).
        case other
        /// invalidArgument: the daemon refused the input.
        case invalid
        /// serverError: the site answered, but not as Jira does.
        case server
        /// offline, networkError, serverTimeout.
        case network
        /// tlsError.
        case tls
        /// authFailed: the site refused the token.
        case authFailed
        /// authRequired: no token typed and none stored.
        case authRequired
        /// conflict: an account for this site and address exists.
        case conflict
    }

    /// jira.ClassOf: the class of an API error code; one without a class
    /// (0 included) is `other`.
    public static func classOf(_ code: ErrorCode) -> ErrorClass {
        switch code {
        case .invalidArgument, .invalidParams:
            return .invalid
        case .serverError:
            return .server
        case .offline, .networkError, .serverTimeout:
            return .network
        case .tlsError:
            return .tls
        case .authFailed:
            return .authFailed
        case .authRequired:
            return .authRequired
        case .conflict:
            return .conflict
        default:
            return .other
        }
    }

    /// jira.Classify: `classOf` for an error of the RPC client; anything
    /// that is not a daemon error is `other`.
    public static func classify(_ error: (any Error)?) -> ErrorClass {
        guard let e = error as? RPCError else { return .other }
        return classOf(e.code)
    }

    /// jira.Failure: what the assistant does after a failed step: it shows
    /// `page` with `banner`. An empty banner means the client's general
    /// sentence for the error (`rpcErrorText`) with `what`, the step's
    /// action.
    public struct Failure: Sendable, Equatable {
        public var page: Page
        public var banner: String
        public var what: String
    }

    /// jira.FailureOf: a failed step of the assistant for a deployment;
    /// `editing` is true when the assistant edits an existing account (it
    /// saves from the credentials page).
    public static func failureOf(_ step: Step, _ errorClass: ErrorClass, _ d: JiraDeployment, editing: Bool) -> Failure {
        let creds = credentialFields(d)
        switch step {
        case .detect:
            var f = Failure(page: .site, banner: "", what: L10n.T("Looking up the Jira site"))
            switch errorClass {
            case .server:
                f.banner = L10n.T("This address is not a Jira site")
            case .invalid:
                f.banner = L10n.T("This is not a web address")
            default:
                break
            }
            return f
        case .spaces:
            var f = Failure(page: .credentials, banner: "", what: L10n.T("Loading the spaces"))
            switch errorClass {
            case .authFailed:
                f.banner = creds.rejected
            case .authRequired:
                f.banner = creds.tokenPrompt
            default:
                break
            }
            return f
        case .save:
            break
        }
        var f = editing
            ? Failure(page: .credentials, banner: "", what: L10n.T("Saving the account"))
            : Failure(page: .spaces, banner: "", what: L10n.T("Adding the account"))
        switch errorClass {
        case .conflict:
            f.banner = L10n.T("An account for this Jira site already exists")
        case .authFailed:
            f.page = .credentials
            f.banner = creds.rejected
        case .authRequired:
            f.page = .credentials
            f.banner = creds.tokenPrompt
        default:
            break
        }
        return f
    }
}
