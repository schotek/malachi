// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Contacts page of the settings, after Calendar (W3;
/// ui/internal/contactsctl preferences.go): a group per account that can
/// have contacts (Microsoft 365, CardDAV) with its kind capsule — Sync
/// Contacts, Microsoft's consent with Allow… when the sign-in lacks it, the
/// account's address books with their checkboxes and counts, and for a
/// CardDAV server the note that its connection is edited on the Calendar
/// page — then Recipient Suggestions (the key contacts-suggest-scope).
/// `ContactsPreferencesController` keeps the state; each change shows at
/// once and is taken back when the daemon refuses it.
@MainActor
final class ContactsPaneViewController: PreferencesPaneViewController {
    private var controller: ContactsPreferencesController?
    private weak var contacts: AppContacts?
    private let sectionsHost = prefsColumn(spacing: 24)
    private let emptyGroup = PreferencesGroupView(title: nil, description: ContactBook.noContactAccounts)
    private let scopeGroup = PreferencesGroupView(title: ContactBook.suggestionsGroup)
    private let errorLabel = NSTextField(wrappingLabelWithString: "")
    private var scopeButtons: [NSButton] = []
    private var shown: [ContactsPreferencesController.Section]?
    private var shownSaving: Set<AccountID> = []
    private var tokens: [NotificationHub.Token] = []
    /// Book ids of the checkboxes, by tag.
    private var bookTags: [AddressBookID] = []
    private var accountTags: [AccountID] = []
    /// The groups' views by account name (`MALACHI_START` prefs-contacts).
    private(set) var sectionViews: [(name: String, view: NSView)] = []

    /// The window closed: nothing is taken any more.
    var closed = false {
        didSet {
            guard closed, !oldValue else { return }
            controller?.stop()
            tokens.forEach { $0.cancel() }
            tokens = []
        }
    }

    override func loadView() {
        super.loadView()
        prefsAddFilling(sectionsHost, to: contentStack)
        emptyGroup.isHidden = true
        addGroup(emptyGroup)
        var rows: [NSView] = []
        for (i, s) in ContactBook.scopes.enumerated() {
            let b = NSButton(radioButtonWithTitle: ContactBook.scopeText(s), target: self, action: #selector(scopeChosen(_:)))
            b.tag = i
            scopeButtons.append(b)
            rows.append(PreferenceRowView(title: "", prefix: b))
        }
        scopeGroup.setRows(rows)
        addGroup(scopeGroup)
        errorLabel.font = Typo.caption
        errorLabel.textColor = .systemRed
        errorLabel.isHidden = true
        prefsAddFilling(errorLabel, to: contentStack)
    }

    /// Binds the page to the application's address books (once).
    func configure(_ contacts: AppContacts) {
        guard controller == nil, !closed else { return }
        _ = view
        self.contacts = contacts
        let c = ContactsPreferencesController(options: contacts.options)
        let settings = contacts.state.settings
        c.scope = { settings.contactsSuggestScope }
        c.setScope = { settings.contactsSuggestScope = $0 }
        c.onChange = { [weak self] in self?.update() }
        c.onSignIn = { [weak self] id in
            guard let self, let contacts = self.contacts else { return }
            contacts.signIn(id) { [weak self] text in self?.errorText(text) }
        }
        controller = c
        let hub = contacts.state.notifications
        tokens = [
            hub.addContactsChanged { [weak c] _ in c?.handle(API.Notify.contactsChanged) },
            hub.addAccountsChanged { [weak c] in c?.handle(API.Notify.accountsChanged) },
        ]
        c.start()
        update()
    }

    private func errorText(_ text: String) {
        errorLabel.stringValue = text
        errorLabel.isHidden = text.isEmpty
    }

    private func update() {
        guard let c = controller else { return }
        let sections = c.sections
        let saving = Set(sections.map(\.account.id).filter { c.saving($0) })
        if shown != sections || saving != shownSaving {
            shown = sections
            shownSaving = saving
            rebuild(sections, c)
        }
        emptyGroup.isHidden = !c.empty
        for (i, b) in scopeButtons.enumerated() {
            b.state = ContactBook.scopes[i] == c.suggestScope ? .on : .off
        }
        errorText(c.error)
    }

    private func rebuild(_ sections: [ContactsPreferencesController.Section], _ c: ContactsPreferencesController) {
        for v in sectionsHost.arrangedSubviews {
            sectionsHost.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        bookTags = []
        accountTags = []
        sectionViews = []
        for s in sections {
            let pill = PillLabel()
            pill.font = Typo.sidebarKindBadge
            pill.textColor = .secondaryLabelColor
            pill.stringValue = s.badge
            pill.isHidden = s.badge.isEmpty
            let group = PreferencesGroupView(title: s.title, headerSuffix: pill)
            var rows: [NSView] = []
            let sync = NSSwitch()
            sync.state = s.syncOn ? .on : .off
            sync.isEnabled = !c.saving(s.account.id)
            sync.tag = accountTags.count
            accountTags.append(s.account.id)
            sync.target = self
            sync.action = #selector(syncToggled(_:))
            sync.setAccessibilityLabel(ContactBook.syncContacts)
            rows.append(PreferenceRowView(title: ContactBook.syncContacts, trailing: sync))
            if s.consent {
                let symbol = CalloutCard.symbolView()
                CalloutCard.show("exclamationmark.triangle", .warning, in: symbol)
                if s.signIn {
                    let allow = NSButton(title: ContactBook.allow, target: self, action: #selector(allowClicked(_:)))
                    allow.bezelStyle = .rounded
                    allow.tag = sync.tag
                    // The sentence wraps as a subtitle (W3 ②).
                    rows.append(PreferenceRowView(title: "", subtitle: ContactBook.consentBanner(s.title), prefix: symbol, trailing: allow))
                } else {
                    rows.append(PreferenceRowView(
                        title: "", subtitle: ContactBook.consentBanner(s.title) + "\n" + ContactBook.consentOnline, prefix: symbol))
                }
            }
            for b in s.books {
                let check = NSButton(checkboxWithTitle: "", target: self, action: #selector(bookToggled(_:)))
                check.state = b.visible ? .on : .off
                check.tag = bookTags.count
                bookTags.append(b.id)
                let name = ContactBook.bookName(b)
                check.setAccessibilityLabel(name)
                let count = NSTextField(labelWithString: b.count.formatted())
                count.font = Typo.captionNumeric
                count.textColor = .secondaryLabelColor
                rows.append(PreferenceRowView(title: name, prefix: check, trailing: count))
            }
            if s.cardDAV {
                let info = NSImageView(image: NSImage(systemSymbolName: "info.circle", accessibilityDescription: nil) ?? NSImage())
                info.contentTintColor = .secondaryLabelColor
                let note = PreferenceRowView(title: "", subtitle: ContactBook.cardDAVNote, prefix: info)
                rows.append(note)
            }
            group.setRows(rows)
            prefsAddFilling(group, to: sectionsHost)
            sectionViews.append((s.title, group))
        }
    }

    @objc private func syncToggled(_ sender: NSSwitch) {
        guard accountTags.indices.contains(sender.tag) else { return }
        controller?.setSync(accountTags[sender.tag], on: sender.state == .on)
    }

    @objc private func allowClicked(_ sender: NSButton) {
        guard accountTags.indices.contains(sender.tag) else { return }
        controller?.allow(accountTags[sender.tag])
    }

    @objc private func bookToggled(_ sender: NSButton) {
        guard bookTags.indices.contains(sender.tag) else { return }
        controller?.setVisible(bookTags[sender.tag], sender.state == .on)
    }

    @objc private func scopeChosen(_ sender: NSButton) {
        guard ContactBook.scopes.indices.contains(sender.tag) else { return }
        controller?.setSuggestScope(ContactBook.scopes[sender.tag])
        update()
    }
}
