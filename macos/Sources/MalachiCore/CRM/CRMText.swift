// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/crm text.go: every text of the CRM, with the GTK msgids as
// keys. "eWay-CRM" and "eWay" are brands and stay as they are.

extension CRM {
    /// crm.Button: the CRM button of an open message.
    public struct Button: Sendable, Equatable {
        public var state: ButtonState
        /// "" when hidden.
        public var label = ""
        public var tooltip = ""
        /// It can be pressed (not while Saving).
        public var enabled = false
        /// It opens `savedMenu()` instead of the dialog.
        public var menu = false

        public init(state: ButtonState, label: String = "", tooltip: String = "", enabled: Bool = false, menu: Bool = false) {
            self.state = state
            self.label = label
            self.tooltip = tooltip
            self.enabled = enabled
            self.menu = menu
        }
    }

    /// crm.ButtonFor.
    public static func buttonFor(_ state: ButtonState) -> Button {
        var b = Button(state: state)
        switch state {
        case .hidden:
            break
        case .notSaved:
            // TRANSLATORS: the button of a message that saves it to eWay-CRM (a CRM; a brand name).
            b.label = L10n.T("Save to eWay-CRM")
            b.tooltip = b.label
            b.enabled = true
        case .saving:
            b.label = L10n.T("Save to eWay-CRM")
            // TRANSLATORS: tooltip of the button while the message is being saved to eWay-CRM.
            b.tooltip = L10n.T("Saving to eWay-CRM…")
        case .saved:
            // TRANSLATORS: the button of a message saved in eWay-CRM; it opens a menu.
            b.label = L10n.T("In eWay-CRM")
            b.tooltip = savedText
            b.enabled = true
            b.menu = true
        }
        return b
    }

    /// The tooltip of the saved message's button and pill, the bar's title.
    static var savedText: String {
        // TRANSLATORS: the message is saved in eWay-CRM (pill tooltip, bar title).
        L10n.T("Saved in eWay-CRM")
    }

    /// crm.MenuLabel.
    public static func menuLabel(_ id: MenuID) -> String {
        switch id {
        case .open:
            // TRANSLATORS: opens the saved message (or an item) in eWay-CRM in the browser.
            return L10n.T("Open in eWay-CRM")
        case .editLinks:
            // TRANSLATORS: menu item: change which contacts, companies, deals and projects the saved message is linked to.
            return L10n.T("Edit Links…")
        case .remove:
            return L10n.T("Remove from eWay-CRM")
        }
    }

    /// crm.SavedMenu: Open in eWay-CRM, Edit Links…, Remove from eWay-CRM.
    public static func savedMenu() -> [MenuItem] {
        [
            MenuItem(id: .open, label: menuLabel(.open), destructive: false),
            MenuItem(id: .editLinks, label: menuLabel(.editLinks), destructive: false),
            MenuItem(id: .remove, label: menuLabel(.remove), destructive: true),
        ]
    }

    /// crm.SaveMenuLabel: the context menu's Save item for `count` rows.
    public static func saveMenuLabel(_ count: Int) -> String {
        if count > 1 {
            // TRANSLATORS: context menu of several selected messages; %d is their number.
            return L10n.N("Save %d Message to eWay-CRM…", "Save %d Messages to eWay-CRM…", count)
        }
        // TRANSLATORS: context menu of a message: opens the dialog that saves it to eWay-CRM.
        return L10n.T("Save to eWay-CRM…")
    }

    /// crm.DialogStrings: the fixed texts of the dialog.
    public struct DialogStrings: Sendable, Equatable {
        /// Save to eWay-CRM, or Edit Links over a saved message.
        public var title: String
        /// The search field's placeholder.
        public var search: String
        /// The marker of the superior deal or project.
        public var superiorItem: String
        public var `private`: String
        public var privateHint: String
        public var empty: String
        public var partial: String
        /// The quiet line under the groups when no deal or project is
        /// listed (`Dialog.offersSearchHint`).
        public var searchHint: String
        /// The default button, with its mnemonic.
        public var save: String
    }

    /// crm.DialogTexts.
    public static func dialogTexts(editing: Bool) -> DialogStrings {
        DialogStrings(
            title: editing
                // TRANSLATORS: title of the dialog that changes a saved message's links in eWay-CRM.
                ? L10n.T("Edit Links")
                : L10n.T("Save to eWay-CRM"),
            // TRANSLATORS: placeholder of the search field of the Save to eWay-CRM dialog.
            search: L10n.T("Search eWay-CRM"),
            // TRANSLATORS: marks the one deal or project the message belongs under in eWay-CRM.
            superiorItem: L10n.T("Superior Item"),
            // TRANSLATORS: check box: the saved message is visible to its owner only in eWay-CRM.
            private: L10n.C("crm", "Private"),
            privateHint: privateHint,
            empty: L10n.T("Nothing in eWay-CRM matches these addresses."),
            partial: L10n.T("eWay-CRM did not answer in time; some suggestions may be missing."),
            searchHint: L10n.T("Deals and projects are added by searching above"),
            save: L10n.T("_Save"))
    }

    private static var privateHint: String {
        L10n.T("Only you will see this message in eWay-CRM.")
    }

    /// crm.GroupTitle: the heading of a kind's group.
    public static func groupTitle(_ k: CrmItemKind) -> String {
        switch k {
        case .contact:
            // TRANSLATORS: eWay-CRM contacts (people).
            return L10n.C("crm", "Contacts")
        case .company:
            // TRANSLATORS: eWay-CRM companies.
            return L10n.C("crm", "Companies")
        case .deal:
            // TRANSLATORS: eWay-CRM deals (sales opportunities, "Leads" in its API).
            return L10n.C("crm", "Deals")
        case .project:
            // TRANSLATORS: eWay-CRM projects.
            return L10n.C("crm", "Projects")
        default:
            return ""
        }
    }

    /// crm.BarItem: an item a saved message is linked to.
    public struct BarItem: Sendable, Equatable {
        public var kind: CrmItemKind
        public var name: String
        public var superior = false
        /// `openURL`; "" when there is none.
        public var url = ""

        public init(kind: CrmItemKind, name: String, superior: Bool = false, url: String = "") {
            self.kind = kind
            self.name = name
            self.superior = superior
            self.url = url
        }
    }

    /// crm.Bar: the strip under a saved message's header, plain texts only.
    public struct Bar: Sendable, Equatable {
        public var title = ""
        /// The linked items in `kinds`' order.
        public var items: [BarItem] = []
        /// Their names on one line (capped at 512 bytes); "" when the
        /// message is linked to nothing.
        public var names = ""
        /// Opens the saved e-mail item; `openLabel` is its button.
        public var url = ""
        public var openLabel = ""
        /// The private hint when the saved item is private, else "".
        public var note = ""

        public init(title: String = "", items: [BarItem] = [], names: String = "", url: String = "",
                    openLabel: String = "", note: String = "") {
            self.title = title
            self.items = items
            self.names = names
            self.url = url
            self.openLabel = openLabel
            self.note = note
        }
    }

    /// crm.BarFor: the bar of the message's record; nil when there is none.
    public static func barFor(_ rec: CrmRecord?) -> Bar? {
        guard let rec else { return nil }
        var b = Bar(title: savedText, url: openURL(rec.url), openLabel: menuLabel(.open))
        var names: [String] = []
        for k in kinds {
            for it in rec.items {
                guard let ref = refOf(it.kind, it.guid), ref.kind == k else { continue }
                let c = cleanItem(it, ref)
                guard !c.name.isEmpty else { continue }
                b.items.append(BarItem(kind: k, name: c.name, superior: it.superior, url: c.url))
                names.append(c.name)
            }
        }
        b.names = capBytes(names.joined(separator: ", "), API.Limits.maxCrmDetailBytes)
        if rec.private {
            b.note = privateHint
        }
        return b
    }

    /// crm.Pill: the list pill of a saved message.
    public struct Pill: Sendable, Equatable {
        public var label: String
        public var tooltip: String
    }

    /// crm.PillFor: nil when the message is not saved.
    public static func pillFor(_ s: MessageSummary) -> Pill? {
        s.crmSaved ? Pill(label: pillBrand, tooltip: savedText) : nil
    }

    /// crm.SaveSummary: the outcomes of crm.save counted.
    public struct SaveSummary: Sendable, Equatable {
        public var saved = 0
        public var failed = 0
        /// The first failed message's error.
        public var error: RPCError?

        public init(saved: Int = 0, failed: Int = 0, error: RPCError? = nil) {
            self.saved = saved
            self.failed = failed
            self.error = error
        }
    }

    /// crm.Summarize.
    public static func summarize(_ res: CrmSaveResult) -> SaveSummary {
        var s = SaveSummary()
        for o in res.results {
            if let e = o.error {
                s.failed += 1
                if s.error == nil {
                    s.error = e
                }
            } else {
                s.saved += 1
            }
        }
        return s
    }

    /// crm.SaveToast: the toast after crm.save. A nil text with an error:
    /// the client's general sentence for it (`rpcErrorText` with
    /// `actionText(.save)`).
    public static func saveToast(_ s: SaveSummary) -> (text: String?, error: RPCError?) {
        if s.failed == 0, s.saved <= 1 {
            // TRANSLATORS: toast after one message was saved to eWay-CRM.
            return (L10n.T("Saved to eWay-CRM"), nil)
        }
        if s.failed == 0 {
            // TRANSLATORS: toast after several messages were saved to eWay-CRM; %d is their number.
            return (L10n.N("%d message saved to eWay-CRM", "%d messages saved to eWay-CRM", s.saved), nil)
        }
        if s.saved + s.failed == 1 {
            let t = errorText(s.error)
            return t.isEmpty ? (nil, s.error) : (t, nil)
        }
        // TRANSLATORS: toast after saving several messages to eWay-CRM, some of which failed; %d is the number that failed.
        return (L10n.N("%d message was not saved to eWay-CRM.", "%d messages were not saved to eWay-CRM.", s.failed), nil)
    }

    /// crm.RemovedToast.
    public static func removedToast() -> String {
        // TRANSLATORS: toast after a message was removed from eWay-CRM.
        L10n.T("Removed from eWay-CRM")
    }

    /// crm.Question: a confirmation's heading, text and destructive button.
    public struct Question: Sendable, Equatable {
        public var heading: String
        public var body: String
        public var confirm: String
    }

    /// The subject quoted in `removeQuestion`, at most this many bytes.
    static let maxSubjectBytes = 200

    /// crm.RemoveQuestion (docs/eway.md D18): `subject` is untrusted,
    /// cleaned and capped here.
    public static func removeQuestion(_ subject: String) -> Question {
        var s = capBytes(Jira.clean(subject), maxSubjectBytes)
        if s.isEmpty {
            s = L10n.T("(No subject)")
        }
        return Question(
            // TRANSLATORS: heading of the question before a saved message is deleted from eWay-CRM.
            heading: L10n.T("Remove from eWay-CRM?"),
            // TRANSLATORS: %s is the message's subject.
            body: L10n.T("“%s” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays.", s),
            confirm: L10n.T("_Remove"))
    }

    /// crm.ErrorText: the sentence for an error of the CRM's own (1205,
    /// 1307, 1109); "" for any other (the client's general sentence).
    public static func errorText(_ error: (any Error)?) -> String {
        if let reason = signInReason(error) {
            return signInText(reason)
        }
        guard let e = error as? RPCError else { return "" }
        switch e.code {
        case .crmDuplicate:
            // TRANSLATORS: eWay-CRM refused the message as a duplicate of another saved item.
            return L10n.T("eWay-CRM already has this message.")
        case .crmItemNotFound:
            // TRANSLATORS: a contact, company, deal or project (or the saved message) was deleted in eWay-CRM meanwhile.
            return L10n.T("eWay-CRM no longer has this item.")
        default:
            return ""
        }
    }

    static func signInText(_ reason: CrmSignInReason) -> String {
        reason == .plan
            ? L10n.T("This eWay-CRM does not allow access through its API.")
            : L10n.T("This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.")
    }

    /// crm.Action: a CRM call, for the client's general error sentence.
    public enum Action: Int, Sendable, CaseIterable {
        case save, lookup, search, remove, download
    }

    /// crm.ActionText: the "%s" of "%s failed: …".
    public static func actionText(_ a: Action) -> String {
        switch a {
        case .lookup:
            return contacting
        case .search:
            // TRANSLATORS: progressive form for an error text ("Searching eWay-CRM failed: …").
            return L10n.T("Searching eWay-CRM")
        case .remove:
            // TRANSLATORS: progressive form for an error text ("Removing from eWay-CRM failed: …").
            return L10n.T("Removing from eWay-CRM")
        case .download:
            // TRANSLATORS: progressive form for an error text ("Downloading the message failed: …").
            return L10n.T("Downloading the message")
        case .save:
            // TRANSLATORS: progressive form for an error text ("Saving to eWay-CRM failed: …").
            return L10n.T("Saving to eWay-CRM")
        }
    }

    static var contacting: String {
        // TRANSLATORS: progress, and progressive form for an error text ("Contacting eWay-CRM failed: …").
        L10n.T("Contacting eWay-CRM")
    }

    /// crm.DownloadingText: the status while `n` messages crm.save named
    /// are downloaded first.
    public static func downloadingText(_ n: Int) -> String {
        if n <= 1 {
            // TRANSLATORS: status line: a message is downloaded before it is saved to eWay-CRM.
            return L10n.T("Downloading the message first…")
        }
        // TRANSLATORS: status line: messages are downloaded before they are saved to eWay-CRM; %d is their number.
        return L10n.N("Downloading %d message first…", "Downloading %d messages first…", n)
    }

    // MARK: The account assistant

    /// crm.WizardPage. Editing opens on `credentials`.
    public enum WizardPage: Int, Sendable, Hashable {
        case service, credentials
    }

    /// crm.WizardStrings.
    public struct WizardStrings: Sendable, Equatable {
        public var title, editTitle, credentialsTitle: String
        public var serviceLabel, serviceHint: String
        public var nameLabel, userLabel: String
        public var passwordLabel, newPasswordLabel: String
        public var passwordPrompt: String
        /// With mnemonics.
        public var next, add, save: String
        public var contacting, signingIn, adding, saving: String
        /// The sign-in page's check box of `EwayConfig.followThreads`
        /// (docs/eway.md D22) and the line under it.
        public var followThreads, followThreadsHint: String
    }

    /// crm.WizardTexts.
    public static func wizardTexts() -> WizardStrings {
        WizardStrings(
            // TRANSLATORS: title of the assistant that adds an eWay-CRM account.
            title: L10n.T("Add eWay-CRM Account"),
            editTitle: L10n.T("Edit Account"),
            credentialsTitle: L10n.T("Sign In"),
            // TRANSLATORS: the address of the eWay-CRM web service, the one its Outlook add-in uses.
            serviceLabel: L10n.T("Web Service Address"),
            serviceHint: L10n.T("The address of your eWay-CRM, such as https://hosting.eway-crm.com/company. In the web application, point at your company's name in your account panel to see it; in Outlook it is under eWay-CRM → Settings → Connection."),
            nameLabel: L10n.T("Account Name"),
            userLabel: L10n.T("User Name"),
            passwordLabel: L10n.T("Password"),
            newPasswordLabel: L10n.T("New Password (leave empty to keep)"),
            passwordPrompt: L10n.T("Enter the password for this account"),
            next: L10n.T("_Next"),
            add: L10n.T("_Add Account"),
            save: L10n.T("_Save"),
            contacting: contacting,
            // TRANSLATORS: progressive form for the RPC error text
            signingIn: L10n.T("Signing in"),
            adding: L10n.T("Adding the account"),
            saving: L10n.T("Saving the account"),
            // TRANSLATORS: check box of the eWay-CRM account: once a message is saved, the later messages of its conversation are saved too.
            followThreads: L10n.T("Also save later messages of saved conversations"),
            // TRANSLATORS: the line under the check box "Also save later messages of saved conversations".
            followThreadsHint: L10n.T("Replies and new messages in a conversation you saved are saved to eWay-CRM with the same links."))
    }

    /// crm.CheckServiceInput: ok when the address can be looked up, else
    /// the problem to show under it ("" while empty). https is assumed
    /// without a scheme; http is refused here (docs/eway.md D2).
    public static func checkServiceInput(_ raw: String) -> (ok: Bool, problem: String) {
        var s = Jira.trimSpace(raw)
        if s.isEmpty {
            return (false, "")
        }
        let bad = (false, L10n.T("This is not a web address"))
        for r in s.unicodeScalars {
            if r.properties.isWhitespace || r == "\\" {
                return bad
            }
            switch r.properties.generalCategory {
            case .control, .format:
                return bad
            default:
                break
            }
        }
        if let i = s.range(of: "://") {
            switch s[..<i.lowerBound].lowercased() {
            case "https":
                break
            case "http":
                return (false, httpOnly)
            default:
                return bad
            }
        } else {
            s = "https://" + s
        }
        guard let u = Jira.parseURL(s), !u.host.isEmpty, !u.hasUser, !u.hostname.isEmpty else { return bad }
        return (true, "")
    }

    static var httpOnly: String {
        L10n.T("Malachi Mail connects to eWay-CRM over https only.")
    }

    /// crm.WizardStep: a call of the assistant.
    public enum WizardStep: Int, Sendable {
        /// account.detectEway.
        case detect
        /// account.test (one sign-in).
        case test
        /// account.add, or account.update when editing.
        case save
    }

    /// crm.WizardField: a field the assistant flags and focuses.
    public enum WizardField: Int, Sendable {
        case none, service, user, password
    }

    /// crm.Failure: after a failed step the assistant shows `page` with
    /// `banner` (empty: the client's general sentence with `what`) and
    /// flags `field`.
    public struct Failure: Sendable, Equatable {
        public var page: WizardPage
        public var banner: String
        public var what: String
        public var field: WizardField

        public init(page: WizardPage, banner: String = "", what: String = "", field: WizardField = .none) {
            self.page = page
            self.banner = banner
            self.what = what
            self.field = field
        }
    }

    /// crm.FailureOf.
    public static func failureOf(_ step: WizardStep, _ error: (any Error)?, editing: Bool) -> Failure {
        var f = Failure(page: step == .detect && !editing ? .service : .credentials)
        switch step {
        case .detect:
            f.what = contacting
        case .test:
            f.what = L10n.T("Signing in")
        case .save:
            f.what = editing ? L10n.T("Saving the account") : L10n.T("Adding the account")
        }
        if let reason = signInReason(error) {
            f.banner = signInText(reason)
            if reason == .microsoft, !editing {
                f.page = .service
                f.field = .service
            }
            return f
        }
        guard let e = error as? RPCError else { return f }
        switch e.code {
        case .authFailed:
            f.page = .credentials
            f.field = .password
            f.banner = L10n.T("The server rejected the user name or password")
        case .authRequired:
            f.page = .credentials
            f.field = .password
            f.banner = L10n.T("Enter the password for this account")
        case .invalidArgument, .serverError:
            if f.page == .service {
                f.field = .service
                if e.code == .invalidArgument {
                    f.banner = L10n.T("This is not a web address")
                }
            }
        default:
            break
        }
        return f
    }
}
