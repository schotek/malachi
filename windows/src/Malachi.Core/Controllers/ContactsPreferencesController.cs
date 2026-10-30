// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsPreferencesController.swift;
// GTK: ui/internal/contactsctl preferences.go (PreferencesController,
// PrefsAccount).
//
// Preferences → Contacts (W3), after Calendar: a section per account with
// the contacts capability in account order — a Microsoft account's switch
// Sync Contacts (account.update with graph.contacts.sync; switching on when
// the sign-in lacks Contacts.ReadWrite shows the consent row with Allow…,
// which hands the account to the re-sign-in with contacts), a CardDAV
// account's switch (caldav.contacts) and note, the account's books with
// their checkboxes (addressBook.set visible) — then the group Recipient
// Suggestions (the key contacts-suggest-scope). Each change is shown at
// once and undone when the daemon refuses it.
//
// Windows: Swift's onChange and onSignIn are the events Changed and
// SignInRequested; scope and setScope read and write
// SettingsStore.ContactsSuggestScope (Scope, SetScope). Drive it from the
// page: Start, Handle for notify.contactsChanged and
// notify.accountsChanged, SetSync, SetVisible, Allow, SetSuggestScope, Stop.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Contacts;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.PreferencesController: Preferences → Contacts.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class ContactsPreferencesController
{
    private readonly Dictionary<AccountId, ContactsConsentNeed> consent = [];
    private readonly HashSet<AccountId> savingAccounts = [];
    private List<Account> accounts = [];
    private List<AddressBook> books = [];
    private int gen;
    private bool closed;

    /// <summary>A page over <paramref name="options"/>.</summary>
    public ContactsPreferencesController(ContactsOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);
        Options = options;
    }

    /// <summary>The page changed; the view reads it anew.</summary>
    public event EventHandler? Changed;

    /// <summary>Allow… (account.oauthStart with the account and contacts).</summary>
    public event EventHandler<AccountId>? SignInRequested;

    /// <summary>The shared options.</summary>
    public ContactsOptions Options { get; }

    /// <summary>Reads contacts-suggest-scope (<c>SettingsStore.ContactsSuggestScope</c>); null is the default, not stored.</summary>
    public Func<ContactSearchScope>? Scope { get; set; }

    /// <summary>Writes contacts-suggest-scope.</summary>
    public Action<ContactSearchScope>? SetScope { get; set; }

    /// <summary>The first answer arrived.</summary>
    public bool Loaded { get; private set; }

    /// <summary>The last refusal's sentence ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>addressBook.list's books (for Add to Contacts' default).</summary>
    public IReadOnlyList<AddressBook> Books => books;

    /// <summary>The page's sections: the accounts with the contacts capability (graph and caldav), in account order.</summary>
    public IReadOnlyList<ContactsPreferencesSection> Sections
    {
        get
        {
            var output = new List<ContactsPreferencesSection>();
            foreach (var a in accounts)
            {
                var kind = a.Config.ProtocolKind.Value;
                if (!a.Can(Capability.Contacts) || kind is not (AccountKind.Graph or AccountKind.CalDAV))
                {
                    continue;
                }
                var on = ContactBook.SyncOn(a.Config);
                var need = on && consent.TryGetValue(a.Id, out var c) ? c : null;
                output.Add(new ContactsPreferencesSection(
                    a, ContactBook.AccountName(a), ContactBook.AccountKindBadge(a), on,
                    Consent: need is not null, SignIn: need?.SignIn ?? false,
                    Books: on ? [.. books.Where(b => b.AccountId == a.Id)] : [],
                    CardDAV: kind == AccountKind.CalDAV));
            }
            return output;
        }
    }

    /// <summary>No account that can have contacts (<see cref="ContactsText.NoContactAccounts"/>).</summary>
    public bool Empty => Loaded && Sections.Count == 0;

    /// <summary>Recipient Suggestions' choice (the account's books by default).</summary>
    public ContactSearchScope SuggestScope =>
        Scope is { } s && s().Value == ContactSearchScope.All ? ContactSearchScope.All : ContactSearchScope.Account;

    /// <summary>Loads the accounts and the books.</summary>
    public void Start() => Refresh();

    /// <summary>Ends the page.</summary>
    public void Stop() => closed = true;

    /// <summary>Reloads after notify.contactsChanged and notify.accountsChanged.</summary>
    public void Handle(string method)
    {
        if (method is API.Notify.ContactsChanged or API.Notify.AccountsChanged)
        {
            Refresh();
        }
    }

    /// <summary>An account.update of the account without its answer.</summary>
    public bool IsSaving(AccountId id) => savingAccounts.Contains(id);

    /// <summary>
    /// Sync Contacts: one account.update of the whole configuration with
    /// empty credentials (the keyring keeps the secret), shown at once and
    /// undone on a refusal.
    /// </summary>
    public void SetSync(AccountId id, bool on)
    {
        var i = accounts.FindIndex(a => a.Id == id);
        if (i < 0 || savingAccounts.Contains(id) || ContactBook.SyncOn(accounts[i].Config) == on || !accounts[i].Can(Capability.Contacts))
        {
            return;
        }
        var before = accounts[i].Config;
        var name = ContactBook.AccountName(accounts[i]);
        var cfg = ContactBook.WithSync(before, on);
        accounts[i] = accounts[i] with { Config = cfg };
        savingAccounts.Add(id);
        Error = "";
        RaiseChanged();
        var g = gen;
        Options.Call(API.AccountUpdate, new AccountUpdateParams { AccountId = id, Config = cfg }, ContactsOptions.WriteTimeout, r =>
        {
            savingAccounts.Remove(id);
            if (closed)
            {
                return;
            }
            if (r.Error is { } e)
            {
                var j = accounts.FindIndex(a => a.Id == id);
                if (g == gen && j >= 0)
                {
                    accounts[j] = accounts[j] with { Config = before };
                }
                Error = Options.ErrorMessage(e, name);
                RaiseChanged();
                return;
            }
            Refresh();
        });
    }

    /// <summary>A book's checkbox (addressBook.set), shown at once and undone on a refusal.</summary>
    public void SetVisible(AddressBookId book, bool on)
    {
        var i = books.FindIndex(b => b.Id == book);
        if (i < 0 || books[i].Visible == on)
        {
            return;
        }
        books[i] = books[i] with { Visible = on };
        Error = "";
        RaiseChanged();
        Options.Call(API.AddressBookSet, new AddressBookSetParams { BookId = book, Visible = on }, r =>
        {
            if (closed)
            {
                return;
            }
            var j = books.FindIndex(b => b.Id == book);
            if (r.TryGetValue(out var res, out var e))
            {
                if (j >= 0)
                {
                    books[j] = res.Book;
                }
            }
            else
            {
                if (j >= 0)
                {
                    books[j] = books[j] with { Visible = !on };
                }
                Error = Options.ErrorMessage(e);
            }
            RaiseChanged();
        });
    }

    /// <summary>Allow… of an account's consent row.</summary>
    public void Allow(AccountId id) => SignInRequested?.Invoke(this, id);

    /// <summary>Writes Recipient Suggestions' choice; a value outside <see cref="ContactBook.Scopes"/> is ignored.</summary>
    public void SetSuggestScope(ContactSearchScope s)
    {
        if (!ContactBook.Scopes.Contains(s) || s == SuggestScope)
        {
            return;
        }
        SetScope?.Invoke(s);
        RaiseChanged();
    }

    private void RaiseChanged()
    {
        if (!closed)
        {
            Changed?.Invoke(this, EventArgs.Empty);
        }
    }

    private void Refresh()
    {
        if (closed)
        {
            return;
        }
        var g = ++gen;
        Options.Call(API.AccountList, new EmptyParams(), r =>
        {
            if (closed || g != gen)
            {
                return;
            }
            if (!r.TryGetValue(out var res, out var e))
            {
                Error = Options.ErrorMessage(e);
                RaiseChanged();
                return;
            }
            var listed = res.Accounts;
            Options.Call(API.AddressBookList, new AddressBookListParams(), b =>
            {
                if (closed || g != gen)
                {
                    return;
                }
                accounts = [.. listed];
                if (b.TryGetValue(out var bs, out _))
                {
                    books = [.. bs.Books];
                }
                Loaded = true;
                CheckConsent(g);
                RaiseChanged();
            });
        });
    }

    private void CheckConsent(int g)
    {
        var candidates = ContactBook.ConsentCandidates(accounts, books);
        foreach (var k in consent.Keys.Where(k => !candidates.Any(a => a.Id == k)).ToList())
        {
            consent.Remove(k);
        }
        foreach (var a in candidates)
        {
            var id = a.Id;
            var name = ContactBook.AccountName(a);
            Options.Call(API.AddressBookList, new AddressBookListParams { AccountId = id }, r =>
            {
                if (closed || g != gen)
                {
                    return;
                }
                consent.Remove(id);
                if (r.Error is { } e && ContactsOptions.AsRpcError(e).Code == ErrorCode.ContactsConsentRequired)
                {
                    consent[id] = new ContactsConsentNeed(id, name, ContactsOptions.ConsentData(e)?.SignIn ?? true);
                }
                RaiseChanged();
            });
        }
    }
}
