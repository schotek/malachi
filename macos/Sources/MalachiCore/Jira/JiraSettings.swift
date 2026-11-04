// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/jira/settings.go: the texts and rules of the settings of a
// Jira account. They are one page: the site (read only, with the button
// that replaces the token), the spaces, the synchronisation, the folders
// with the statuses that count as closed, what a notification e-mail of
// the site does, and how comments posted by bots are cleaned. The page
// opens with account.listSpaces (the stored token), which lists the spaces
// and the statuses to choose from; when that fails the page still edits
// what is stored. Save is account.update with empty credentials, which
// keeps the token.
//
// `SettingsForm` holds the edited copy; `apply` turns it into the
// configuration to save. The daemon validates everything again: what is
// checked here is immediate feedback. Nothing here calls the daemon
// (`JiraAccountController` does).
//
// Where Swift cannot follow Go literally: Go's empty values are nil in the
// API types here (`omitempty`), regexp.Compile is `patternError`
// (JiraPattern.swift), and mail.ParseAddress is `validateEmail`.

import Foundation

extension Jira {
    /// jira.SuggestedBotName: what the page offers to add to the bot
    /// accounts with one click while the list lacks it: the integration
    /// that mirrors comments between two Jira sites. Data, not translated.
    public static let suggestedBotName = "Issue Sync – Synchronization for Jira"

    /// jira.SuggestedMetadataFilter: the technical line that integration
    /// puts under a comment's header, offered for the hidden lines. Data,
    /// not translated.
    public static let suggestedMetadataFilter = #"^Remote comment create date:.*$"#

    /// jira.minBotNameRunes: the shortest bot name the page accepts: the
    /// daemon looks for a name of three characters or more inside an
    /// author's name, and takes a shorter one only when it is the whole
    /// name.
    static let minBotNameRunes = 3

    /// jira.SettingsStrings: the fixed texts of the settings page.
    public struct SettingsStrings: Sendable, Equatable {
        /// The window's title.
        public var title = ""
        /// The site: the section, its rows and the button of the token's
        /// row.
        public var siteTitle = ""
        public var siteAddress = ""
        public var accountName = ""
        public var signedInAs = ""
        public var replaceToken = ""
        /// The spaces: the section, its explanation, the text of an empty
        /// list.
        public var spacesTitle = ""
        public var spacesDescription = ""
        public var noSpaces = ""
        /// The synchronisation: the section, the offline window and the
        /// two switches.
        public var syncTitle = ""
        public var keepOffline = ""
        public var keepOfflineSubtitle = ""
        public var onlyMine = ""
        public var onlyMineSubtitle = ""
        public var showEvents = ""
        /// The folders: the section (its switches are
        /// `virtualFolderTitle`) and the picker of the closed statuses.
        public var foldersTitle = ""
        public var closedStatuses = ""
        public var closedStatusesSubtitle = ""
        /// The notification e-mails: the section, the mode and the
        /// senders.
        public var notificationTitle = ""
        public var notificationMode = ""
        public var senders = ""
        public var sendersSubtitle = ""
        /// The comments of bots: the section and its three lists.
        public var botsTitle = ""
        public var botNames = ""
        public var botNamesSubtitle = ""
        public var hiddenLines = ""
        public var hiddenLinesSubtitle = ""
        public var namePrefixes = ""
        public var namePrefixesSubtitle = ""
        /// The buttons of a list: the one next to the field and the one of
        /// an entry.
        public var add = ""
        public var remove = ""
        /// The progress of account.listSpaces and of account.update.
        public var loading = ""
        public var saving = ""
    }

    /// jira.SettingsTexts: the fixed texts, translated.
    public static func settingsTexts() -> SettingsStrings {
        SettingsStrings(
            // TRANSLATORS: title of the window with the settings of a Jira account; "Jira" is a product name.
            title: L10n.T("Jira Account"),
            siteTitle: L10n.T("Jira Site"),
            siteAddress: L10n.T("Site Address"),
            accountName: L10n.T("Account Name"),
            // TRANSLATORS: label of the user a Jira account signs in as.
            signedInAs: L10n.T("Signed In As"),
            // TRANSLATORS: button that asks for a new API token of a Jira account.
            replaceToken: L10n.T("Replace Token…"),
            spacesTitle: L10n.C("jira", "Spaces"),
            spacesDescription: L10n.T("Choose the spaces whose issues appear as folders."),
            noSpaces: L10n.T("No spaces are visible to this account"),
            syncTitle: L10n.T("Synchronisation"),
            keepOffline: L10n.T("Keep Issues Offline For"),
            keepOfflineSubtitle: L10n.T("Older issues stay on the site and are not shown"),
            onlyMine: L10n.T("Only Issues Involving Me"),
            onlyMineSubtitle: L10n.T("Assigned to you, reported by you or watched by you"),
            // TRANSLATORS: switch; the changes are listed among the comments of an issue.
            showEvents: L10n.T("Show Status and Assignee Changes"),
            foldersTitle: L10n.T("Folders"),
            closedStatuses: L10n.T("Closed Statuses"),
            // TRANSLATORS: "Open" is the folder of a Jira account with the issues that are not closed yet.
            closedStatusesSubtitle: L10n.T("Issues in these statuses are left out of Open"),
            // TRANSLATORS: the e-mails Jira sends about changes of issues.
            notificationTitle: L10n.T("Notification E-mails"),
            notificationMode: L10n.T("When a Jira Notification Arrives"),
            // TRANSLATORS: the addresses notification e-mails of Jira come from.
            senders: L10n.T("Senders"),
            // TRANSLATORS: "@example.org" is an example, keep it as it is.
            sendersSubtitle: L10n.T("An address, or a domain such as @example.org"),
            botsTitle: L10n.T("Comments Posted by Bots"),
            // TRANSLATORS: the Jira accounts of integrations that post comments for other people.
            botNames: L10n.T("Bot Accounts"),
            botNamesSubtitle: L10n.T("Their comments are shown under the person they name"),
            // TRANSLATORS: lines of a comment that are not shown.
            hiddenLines: L10n.T("Hidden Lines"),
            hiddenLinesSubtitle: L10n.T("Lines matching these patterns are removed from comments"),
            // TRANSLATORS: words a bot puts in front of a person's name, such as a company name.
            namePrefixes: L10n.T("Name Prefixes"),
            namePrefixesSubtitle: L10n.T("Removed from the start of authors' names"),
            // TRANSLATORS: button that adds what was typed to a list.
            add: L10n.T("Add"),
            remove: L10n.T("Remove"),
            loading: L10n.T("Loading the spaces"),
            saving: L10n.T("Saving the account")
        )
    }

    /// jira.SiteInfo: the read-only part of the page.
    public struct SiteInfo: Sendable, Equatable {
        /// The site's URL, and its deployment's brand name ("Jira Cloud").
        public var address = ""
        public var deployment = ""
        /// Who the account signs in as: the user's name once
        /// account.listSpaces told it, the login or the account's address
        /// before. `userDetail` is the address under a name, "" when
        /// `user` is the address already.
        public var user = ""
        public var userDetail = ""
        /// The name of the deployment's token ("API Token").
        public var tokenLabel = ""
    }

    /// jira.SettingsSite: the site of an account; `user` is the one
    /// account.listSpaces signed in as, nil while it is not known.
    public static func settingsSite(_ cfg: AccountConfig, user: SiteUser?) -> SiteInfo {
        let jc = cfg.jira
        var info = SiteInfo(
            address: clean(jc?.siteUrl ?? ""),
            deployment: deploymentName(jc?.deployment ?? ""),
            tokenLabel: credentialFields(jc?.deployment ?? "").tokenLabel
        )
        var address = clean(jc?.login ?? "")
        if address.isEmpty {
            address = clean(cfg.email)
        }
        if let user {
            info.user = clean(user.name)
            let email = clean(user.email ?? "")
            if !email.isEmpty {
                address = email
            }
        }
        if info.user.isEmpty {
            info.user = address
        } else if address != info.user {
            info.userDetail = address
        }
        return info
    }

    /// jira.SettingsSpaceRows: the spaces the page chooses from: the
    /// spaces of account.listSpaces in the daemon's order, then the stored
    /// ones the listing lacks (a space the token no longer sees, or every
    /// stored space when the listing failed), so that nothing stored is
    /// dropped unseen.
    public static func settingsSpaceRows(stored: [SpaceRef], listed: [Space]) -> [SpaceRow] {
        var rows = spaceRows(listed)
        var seen = Set(listed.map(\.id))
        for ref in stored where !seen.contains(ref.id) {
            seen.insert(ref.id)
            rows.append(SpaceRow(id: ref.id, title: spaceTitle(Space(id: ref.id, key: ref.key, name: ref.name ?? ""))))
        }
        return rows
    }

    /// jira.SetSpaceSelected: the chosen spaces after the check box of the
    /// space `id` changed: `selected` are the chosen ones so far, `stored`
    /// and `listed` what `settingsSpaceRows` shows. The result is in the
    /// order of the rows, with the key and name of the listing where it
    /// has the space.
    public static func setSpaceSelected(
        _ selected: [SpaceRef], stored: [SpaceRef], listed: [Space], id: String, on: Bool
    ) -> [SpaceRef] {
        var chosen = Set(selected.map(\.id))
        if on {
            chosen.insert(id)
        } else {
            chosen.remove(id)
        }
        var out: [SpaceRef] = []
        var done: Set<String> = []
        func add(_ ref: SpaceRef) {
            if chosen.contains(ref.id), !done.contains(ref.id) {
                out.append(ref)
            }
            done.insert(ref.id)
        }
        for s in listed {
            add(SpaceRef(id: s.id, key: s.key, name: nonEmpty(s.name)))
        }
        for ref in stored {
            add(ref)
        }
        return out
    }

    /// jira.VirtualFolders: the fixed views the page has a switch for, in
    /// the order of the sidebar.
    public static let virtualFolders: [VirtualFolder] = [.assignedToMe, .watching, .open]

    /// jira.FolderShown: whether the view `v` is shown: it is not among
    /// the disabled ones (`JiraConfig.disabledFolders`).
    public static func folderShown(_ disabled: [VirtualFolder], _ v: VirtualFolder) -> Bool {
        !disabled.contains(v)
    }

    /// jira.SetFolderShown: `JiraConfig.disabledFolders` after the switch
    /// of the view `v` changed: the disabled views in the order of
    /// `virtualFolders`, each once; empty when every view is shown.
    public static func setFolderShown(_ disabled: [VirtualFolder], _ v: VirtualFolder, shown: Bool) -> [VirtualFolder] {
        virtualFolders.filter { known in
            known == v ? !shown : !folderShown(disabled, known)
        }
    }

    /// jira.NotificationModes: the choices of what a notification e-mail
    /// does, in the order shown.
    public static let notificationModes: [NotificationMailMode] = [.sync, .hide, .ignore]

    /// jira.NotificationModeLabels: the labels of `notificationModes`, in
    /// order.
    public static func notificationModeLabels() -> [String] {
        [
            // TRANSLATORS: what a notification e-mail of Jira does: the issue it names is synchronised.
            L10n.T("Check the Issue at Once"),
            // TRANSLATORS: what a notification e-mail of Jira does: the issue is synchronised and the e-mail is not listed.
            L10n.T("Check the Issue and Hide the E-mail"),
            // TRANSLATORS: what a notification e-mail of Jira does: nothing, it is an e-mail like any other.
            L10n.T("Do Nothing"),
        ]
    }

    /// jira.IndexOfNotificationMode: the position in `notificationModes`
    /// shown for `JiraConfig.notificationMail`; no mode and one this
    /// client does not know are the default, the first.
    public static func indexOfNotificationMode(_ m: NotificationMailMode?) -> Int {
        guard let m else { return 0 }
        return notificationModes.firstIndex(of: m) ?? 0
    }

    /// jira.NotificationHint: the text under the mode: what hiding means;
    /// "" for the other modes.
    public static func notificationHint(_ m: NotificationMailMode?) -> String {
        if m == .hide {
            return L10n.T("Hidden e-mails stay in your mailbox and come back when you turn this off")
        }
        return ""
    }

    /// jira.SendersEditable: whether the senders matter in mode `m`: not
    /// when notification e-mails are left alone.
    public static func sendersEditable(_ m: NotificationMailMode?) -> Bool {
        m != .ignore
    }

    /// jira.DefaultSenders: what an empty list of senders stands for, the
    /// placeholder of its field: every address of the site's host for Jira
    /// Cloud ("@acme.atlassian.net"), "" for Data Center, which has no
    /// default.
    public static func defaultSenders(_ cfg: AccountConfig) -> String {
        guard let jc = cfg.jira, jc.deployment == .cloud else { return "" }
        let host = siteHost(cfg)
        return host.isEmpty ? "" : "@" + host
    }

    /// jira.StatusChoice: one check box of the picker of closed statuses:
    /// a name, which stands for every status of the site called so in its
    /// category (team-managed spaces each have their own "Done").
    public struct StatusChoice: Sendable, Equatable {
        public var name = ""
        public var ids: [String] = []
        public var selected = false

        public init(name: String = "", ids: [String] = [], selected: Bool = false) {
            self.name = name
            self.ids = ids
            self.selected = selected
        }
    }

    /// jira.StatusGroup: the statuses of a category, under its title and
    /// in its colour.
    public struct StatusGroup: Sendable, Equatable {
        /// "" for the group of the statuses without a known one.
        public var category: IssueStatusCategory = ""
        public var title = ""
        public var style = StatusStyle.plain
        public var choices: [StatusChoice] = []
    }

    /// jira.statusCategories: the groups of the picker, in order; the last
    /// one takes the statuses of any other category.
    static let statusCategories: [IssueStatusCategory] = [.todo, .inProgress, .done, ""]

    /// jira.StatusCategoryTitle: the title of a group of the picker.
    public static func statusCategoryTitle(_ c: IssueStatusCategory) -> String {
        switch c {
        case .todo:
            // TRANSLATORS: a category of issue statuses, as Jira calls it.
            return L10n.C("status category", "To Do")
        case .inProgress:
            // TRANSLATORS: a category of issue statuses, as Jira calls it.
            return L10n.C("status category", "In Progress")
        case .done:
            // TRANSLATORS: a category of issue statuses, as Jira calls it.
            return L10n.C("status category", "Done")
        default:
            // TRANSLATORS: the issue statuses that belong to no category.
            return L10n.C("status category", "Other")
        }
    }

    /// jira.knownCategory: `c` when the picker has a group for it, ""
    /// otherwise.
    static func knownCategory(_ c: IssueStatusCategory) -> IssueStatusCategory {
        switch c {
        case .todo, .inProgress, .done:
            return c
        default:
            return ""
        }
    }

    /// jira.DefaultClosedStatuses: what an empty
    /// `JiraConfig.closedStatuses` stands for: the statuses of the
    /// category done.
    public static func defaultClosedStatuses(_ statuses: [IssueStatus]) -> [StatusRef] {
        var out: [StatusRef] = []
        var seen: Set<String> = []
        for s in statuses where s.category == .done && !s.id.isEmpty && !seen.contains(s.id) {
            seen.insert(s.id)
            out.append(StatusRef(id: s.id, name: nonEmpty(s.name)))
        }
        return out
    }

    /// jira.StatusGroups: the picker of the closed statuses: the statuses
    /// of the site (account.listSpaces) by category, one choice per name,
    /// and in the last group the stored ones the site does not list (all
    /// of them when the listing failed). `closed` is
    /// `JiraConfig.closedStatuses`; while it is empty the statuses of the
    /// category done are the selected ones. A group without statuses is
    /// left out.
    public static func statusGroups(_ statuses: [IssueStatus], closed: [StatusRef]) -> [StatusGroup] {
        let selected = Set(closed.map(\.id))
        let byDefault = closed.isEmpty

        var groups = statusCategories.map { c in
            StatusGroup(category: c, title: statusCategoryTitle(c), style: styleOf(c))
        }
        var index: [String: (group: Int, choice: Int)] = [:]
        var listed: Set<String> = []
        func add(_ category: IssueStatusCategory, _ id: String, _ name: String, _ on: Bool) {
            if id.isEmpty || listed.contains(id) {
                return
            }
            listed.insert(id)
            var name = clean(name)
            if name.isEmpty {
                name = clean(id)
            }
            let g = statusCategories.lastIndex(of: category) ?? 0
            let key = category.rawValue + "\u{0}" + name
            let at: (group: Int, choice: Int)
            if let have = index[key] {
                at = have
            } else {
                at = (g, groups[g].choices.count)
                index[key] = at
                groups[g].choices.append(StatusChoice(name: name))
            }
            groups[at.group].choices[at.choice].ids.append(id)
            if on {
                groups[at.group].choices[at.choice].selected = true
            }
        }
        for s in statuses {
            add(knownCategory(s.category), s.id, s.name, byDefault ? s.category == .done : selected.contains(s.id))
        }
        for ref in closed {
            add("", ref.id, ref.name ?? "", true)
        }
        return groups.filter { !$0.choices.isEmpty }
    }

    /// jira.SetStatusSelected: `JiraConfig.closedStatuses` after the check
    /// box of `choice` changed. Ticking stores every status of the name.
    /// The result is empty, the default, when it names exactly the
    /// statuses of the category done, and also when nothing is left: an
    /// empty list cannot say "no status is closed", so the default comes
    /// back.
    public static func setStatusSelected(
        _ statuses: [IssueStatus], closed: [StatusRef], choice: StatusChoice, on: Bool
    ) -> [StatusRef] {
        let current = closed.isEmpty ? defaultClosedStatuses(statuses) : closed
        let touched = Set(choice.ids)
        var out: [StatusRef] = []
        var have: Set<String> = []
        for ref in current {
            if ref.id.isEmpty || have.contains(ref.id) || (touched.contains(ref.id) && !on) {
                continue
            }
            have.insert(ref.id)
            out.append(ref)
        }
        if on {
            var names: [String: String] = [:]
            for s in statuses {
                names[s.id] = s.name
            }
            for id in choice.ids where !id.isEmpty && !have.contains(id) {
                have.insert(id)
                out.append(StatusRef(id: id, name: nonEmpty(names[id] ?? choice.name)))
            }
        }
        let def = defaultClosedStatuses(statuses)
        if !def.isEmpty, def.count == out.count, def.allSatisfy({ have.contains($0.id) }) {
            return []
        }
        return out
    }

    /// jira.StatusesProblem: why the chosen closed statuses cannot be
    /// saved; "" when they can.
    public static func statusesProblem(_ closed: [StatusRef]) -> String {
        if closed.count > API.Limits.maxJiraStatuses {
            return L10n.N("Select at most %d status", "Select at most %d statuses", API.Limits.maxJiraStatuses)
        }
        return ""
    }

    /// jira.ListKind: one of the lists of texts the page edits.
    public enum ListKind: Int, Sendable, Equatable, CaseIterable {
        /// `JiraConfig.botNames`.
        case botNames
        /// `JiraConfig.metadataFilters`.
        case metadataFilters
        /// `JiraConfig.authorPrefixes`.
        case authorPrefixes
        /// `JiraConfig.notificationSenders`.
        case senders
    }

    /// jira.NormaliseEntry: an entry as it is stored: without the spaces
    /// around it; a sender in lower case.
    public static func normaliseEntry(_ kind: ListKind, _ raw: String) -> String {
        let s = trimSpace(raw)
        return kind == .senders ? s.lowercased() : s
    }

    /// jira.entryKey: what two entries of a list are the same by: a
    /// pattern and a sender as they are, a name prefix ignoring case, and
    /// a bot name the way the daemon compares names (visible text, lower
    /// case, every dash a hyphen).
    static func entryKey(_ kind: ListKind, _ entry: String) -> String {
        switch kind {
        case .botNames:
            var out = String.UnicodeScalarView()
            for r in clean(entry).lowercased().unicodeScalars {
                switch r.value {
                case 0x2010, 0x2011, 0x2012, 0x2013, 0x2014, 0x2015, 0x2212, 0xFE58, 0xFE63, 0xFF0D:
                    out.append("-")
                default:
                    out.append(r)
                }
            }
            return String(out)
        case .authorPrefixes:
            return clean(entry).lowercased()
        case .metadataFilters, .senders:
            return entry
        }
    }

    /// jira.NormaliseList: a list as it is stored: every entry normalised,
    /// empty ones and repetitions left out.
    public static func normaliseList(_ kind: ListKind, _ list: [String]) -> [String] {
        var out: [String] = []
        var seen: Set<String> = []
        for raw in list {
            let entry = normaliseEntry(kind, raw)
            if entry.isEmpty {
                continue
            }
            let key = entryKey(kind, entry)
            if seen.contains(key) {
                continue
            }
            seen.insert(key)
            out.append(entry)
        }
        return out
    }

    /// jira.validSender: an entry of the senders: a bare address, or "@"
    /// and a host name.
    static func validSender(_ s: String) -> Bool {
        if s.hasPrefix("@") {
            return validHostName(String(s.dropFirst()))
        }
        return validateEmail(s) == s
    }

    /// jira.validHostName: a DNS name: labels of letters, digits and inner
    /// hyphens, at most 63 bytes each and 253 together.
    static func validHostName(_ h: String) -> Bool {
        var bytes = Array(h.utf8)
        if bytes.isEmpty || bytes.count > 253 {
            return false
        }
        if bytes.last == UInt8(ascii: ".") {
            bytes.removeLast()
        }
        let hyphen = UInt8(ascii: "-")
        for label in bytes.split(separator: UInt8(ascii: "."), omittingEmptySubsequences: false) {
            if label.isEmpty || label.count > 63 || label.first == hyphen || label.last == hyphen {
                return false
            }
            for c in label {
                let letter = (c >= UInt8(ascii: "a") && c <= UInt8(ascii: "z")) || (c >= UInt8(ascii: "A") && c <= UInt8(ascii: "Z"))
                let digit = c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9")
                if !letter && !digit && c != hyphen {
                    return false
                }
            }
        }
        return true
    }

    /// unicode.IsControl somewhere in `s`.
    private static func hasControl(_ s: String) -> Bool {
        s.unicodeScalars.contains { $0.properties.generalCategory == .control }
    }

    /// jira.CheckEntry: checks what the user typed to add to a list that
    /// holds `have`. `entry` is what to add, normalised; it is "" when
    /// there is nothing to add: the field is empty (`problem` is "" too)
    /// or the entry cannot be added, and `problem` says why under the
    /// field.
    public static func checkEntry(_ kind: ListKind, _ raw: String, have: [String]) -> (entry: String, problem: String) {
        let s = normaliseEntry(kind, raw)
        if s.isEmpty {
            return ("", "")
        }
        if s.utf8.count > API.Limits.maxJiraPatternBytes {
            return ("", L10n.T("This entry is too long"))
        }
        if hasControl(s) {
            return ("", L10n.T("This entry contains control characters"))
        }
        switch kind {
        case .botNames:
            if clean(s).unicodeScalars.count < minBotNameRunes {
                return ("", L10n.T("A bot name needs at least 3 characters"))
            }
        case .metadataFilters:
            let reason = patternError(s)
            if !reason.isEmpty {
                // TRANSLATORS: %s says what is wrong with a regular expression, in English ("missing closing )").
                return ("", L10n.T("This pattern is not valid: %s", reason))
            }
        case .authorPrefixes:
            if clean(s).isEmpty {
                return ("", L10n.T("This entry contains control characters"))
            }
        case .senders:
            if !validSender(s) {
                return ("", L10n.T("Enter an address, or a domain such as @example.org"))
            }
        }
        let key = entryKey(kind, s)
        if have.contains(where: { entryKey(kind, normaliseEntry(kind, $0)) == key }) {
            return ("", L10n.T("This entry is already in the list"))
        }
        if have.count >= API.Limits.maxJiraListEntries {
            return ("", L10n.N("The list holds at most %d entry", "The list holds at most %d entries", API.Limits.maxJiraListEntries))
        }
        return (s, "")
    }

    /// jira.Suggestion: an entry the page offers to add with one click:
    /// the entry and the button's text.
    public struct Suggestion: Sendable, Equatable {
        public var value = ""
        public var label = ""
    }

    /// jira.Suggestions: the entries offered for a list that holds `have`:
    /// the suggested bot name and the suggested pattern while their lists
    /// lack them and have room.
    public static func suggestions(_ kind: ListKind, have: [String]) -> [Suggestion] {
        let value: String
        switch kind {
        case .botNames:
            value = suggestedBotName
        case .metadataFilters:
            value = suggestedMetadataFilter
        default:
            return []
        }
        if have.count >= API.Limits.maxJiraListEntries {
            return []
        }
        let key = entryKey(kind, value)
        if have.contains(where: { entryKey(kind, normaliseEntry(kind, $0)) == key }) {
            return []
        }
        // TRANSLATORS: button that adds a suggested entry to a list; %s is the entry, such as the name of a bot.
        return [Suggestion(value: value, label: L10n.T("Add %s", value))]
    }

    /// jira.SettingsForm: the edited copy of what the page changes.
    public struct SettingsForm: Sendable, Equatable {
        /// The account's name.
        public var name = ""
        /// The chosen spaces.
        public var spaces: [SpaceRef] = []
        /// The offline window as stored (0 = the default) until the user
        /// picks one of `offlineChoices`.
        public var offlineDays = 0
        public var onlyMine = false
        /// "Show Status and Assignee Changes": not `hideEvents`.
        public var showEvents = true
        /// The views switched off.
        public var disabledFolders: [VirtualFolder] = []
        /// The statuses that count as closed; empty = those of the
        /// category done.
        public var closedStatuses: [StatusRef] = []
        /// The mode, never empty.
        public var notificationMail = NotificationMailMode.sync
        public var notificationSenders: [String] = []
        public var botNames: [String] = []
        public var metadataFilters: [String] = []
        public var authorPrefixes: [String] = []

        /// Go's zero form with the defaults of `NewSettingsForm`.
        public init() {}

        /// jira.NewSettingsForm: the form of an account as it is stored.
        public init(_ cfg: AccountConfig) {
            name = cfg.name
            guard let jc = cfg.jira else { return }
            spaces = jc.spaces
            offlineDays = jc.offlineDays ?? 0
            onlyMine = jc.onlyMine ?? false
            showEvents = !(jc.hideEvents ?? false)
            disabledFolders = jc.disabledFolders
            closedStatuses = jc.closedStatuses
            notificationMail = Jira.notificationModes[Jira.indexOfNotificationMode(jc.notificationMail)]
            notificationSenders = jc.notificationSenders
            botNames = jc.botNames
            metadataFilters = jc.metadataFilters
            authorPrefixes = jc.authorPrefixes
        }

        /// jira.SettingsForm.Apply: the configuration of account.update:
        /// `cfg`, the account as it is stored, with what the form holds,
        /// normalised (texts trimmed, repetitions and empty entries
        /// dropped, the default mode left out). The connection (site,
        /// deployment, login) and everything the page does not edit stay
        /// as they are. A configuration of another kind comes back
        /// unchanged.
        public func apply(_ cfg: AccountConfig) -> AccountConfig {
            guard var jc = cfg.jira else { return cfg }
            var cfg = cfg
            var name = Jira.truncate(Jira.trimSpace(self.name), Jira.maxNameBytes)
            if name.isEmpty {
                name = Jira.siteHost(cfg)
                if name.isEmpty {
                    name = "Jira"
                }
            }
            cfg.name = name

            var seen: Set<String> = []
            jc.spaces = spaces.filter { !$0.id.isEmpty && seen.insert($0.id).inserted }
            let days = min(max(offlineDays, 0), API.Limits.maxJiraOfflineDays)
            jc.offlineDays = days == 0 ? nil : days
            jc.onlyMine = onlyMine ? true : nil
            jc.hideEvents = showEvents ? nil : true
            jc.disabledFolders = Jira.virtualFolders.filter { !Jira.folderShown(disabledFolders, $0) }
            seen = []
            jc.closedStatuses = closedStatuses.compactMap { ref in
                let id = Jira.trimSpace(ref.id)
                guard !id.isEmpty, seen.insert(id).inserted else { return nil }
                return StatusRef(id: id, name: Jira.nonEmpty(Jira.trimSpace(ref.name ?? "")))
            }
            let mode = Jira.notificationModes[Jira.indexOfNotificationMode(notificationMail)]
            jc.notificationMail = mode == .sync ? nil : mode
            jc.notificationSenders = Jira.normaliseList(.senders, notificationSenders)
            jc.botNames = Jira.normaliseList(.botNames, botNames)
            jc.metadataFilters = Jira.normaliseList(.metadataFilters, metadataFilters)
            jc.authorPrefixes = Jira.normaliseList(.authorPrefixes, authorPrefixes)
            cfg.jira = jc
            return cfg
        }

        /// jira.SettingsForm.SettingsProblem: why the form cannot be
        /// saved; "" when it can.
        public func settingsProblem() -> String {
            let p = Jira.spacesProblem(spaces.count)
            return p.isEmpty ? Jira.statusesProblem(closedStatuses) : p
        }
    }

    /// jira.Changed: whether `updated` differs from `old` in what the
    /// daemon acts on: the page saves only then. Two configurations that
    /// say the same in different words are equal: the default written out
    /// or left out (the offline window, the mode), lists that differ in
    /// spaces, repetitions or, where the order means nothing, in order,
    /// and the names of spaces and statuses, which are for display.
    public static func changed(_ old: AccountConfig, _ updated: AccountConfig) -> Bool {
        compared(old) != compared(updated)
    }

    /// jira.compared: `cfg` in the form `changed` compares. What Go's
    /// zero values are is nil here.
    private static func compared(_ cfg: AccountConfig) -> AccountConfig {
        var cfg = cfg
        cfg.name = trimSpace(cfg.name)
        cfg.kind = cfg.protocolKind
        cfg.displayName = nonEmpty(cfg.displayName ?? "")
        cfg.syncIntervalSeconds = (cfg.syncIntervalSeconds ?? 0) == 0 ? nil : cfg.syncIntervalSeconds
        guard var jc = cfg.jira else { return cfg }
        jc.cloudId = nonEmpty(jc.cloudId ?? "")
        jc.login = nonEmpty(jc.login ?? "")

        var seen: Set<String> = []
        jc.spaces = jc.spaces
            .filter { seen.insert($0.id).inserted }
            .map { SpaceRef(id: $0.id, key: $0.key) }
            .sorted { $0.id < $1.id }
        seen = []
        jc.closedStatuses = jc.closedStatuses
            .map { trimSpace($0.id) }
            .filter { !$0.isEmpty && seen.insert($0).inserted }
            .sorted()
            .map { StatusRef(id: $0) }
        var views: Set<VirtualFolder> = []
        jc.disabledFolders = jc.disabledFolders
            .filter { views.insert($0).inserted }
            .sorted { $0.rawValue < $1.rawValue }

        let days = jc.offlineDays ?? 0
        jc.offlineDays = days <= 0 ? API.Limits.defaultJiraOfflineDays : days
        jc.onlyMine = jc.onlyMine == true ? true : nil
        jc.hideEvents = jc.hideEvents == true ? true : nil
        if jc.notificationMail == nil || jc.notificationMail == "" {
            jc.notificationMail = .sync
        }
        jc.notificationSenders = keys(.senders, jc.notificationSenders, anyOrder: true)
        jc.botNames = keys(.botNames, jc.botNames, anyOrder: true)
        jc.metadataFilters = keys(.metadataFilters, jc.metadataFilters, anyOrder: true)
        // The prefixes are stripped in their order.
        jc.authorPrefixes = keys(.authorPrefixes, jc.authorPrefixes, anyOrder: false)
        cfg.jira = jc
        return cfg
    }

    /// jira.keys: what the entries of a list are compared by (`entryKey`),
    /// each once, in order when the order of the list means something.
    private static func keys(_ kind: ListKind, _ list: [String], anyOrder: Bool) -> [String] {
        let out = normaliseList(kind, list).map { entryKey(kind, $0) }
        return anyOrder ? out.sorted() : out
    }

    /// Go's "" as nil.
    static func nonEmpty(_ s: String) -> String? {
        s.isEmpty ? nil : s
    }
}
