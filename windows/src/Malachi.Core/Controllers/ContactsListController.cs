// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsListController.swift;
// GTK: ui/internal/contactsctl window.go (WindowController).
//
// The Contacts window (W1, W5): the sidebar of books (account.list and
// addressBook.list), the list of the selected row (contact.list, a page at
// a time, searched by the daemon from the first character after
// SearchDebounce), the selected contact (kept by id across reloads), the
// consent banners of Microsoft accounts whose contacts are on but whose
// sign-in lacks the scope (addressBook.list with the account answers
// contactsConsentRequired), and the status line. notify.contactsChanged and
// notify.accountsChanged reload everything after ChangeDebounce, keeping the
// book, the search, as many rows as were shown and the selection.
//
// Windows: Swift's callbacks onChange, onSelect and onSignIn are the events
// Changed, Selected and SignInRequested; Handle takes the notification's
// method as Swift's does. Drive it from the window: Start once, Handle for
// every notify.contactsChanged and notify.accountsChanged, SelectBook,
// SetQuery, Select and LoadMore from the views, Stop when the window
// closes; read the state on every Changed.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Contacts;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.WindowController: the Contacts window's sidebar, list and selection.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class ContactsListController
{
    /// <summary>The pause in typing before the daemon is searched.</summary>
    public static readonly TimeSpan SearchDebounce = TimeSpan.FromMilliseconds(150);

    /// <summary>The pause after a notification before everything is listed again.</summary>
    public static readonly TimeSpan ChangeDebounce = TimeSpan.FromMilliseconds(300);

    /// <summary>How many contacts a page asks for.</summary>
    public const int PageSize = API.Limits.DefaultContactListLimit;

    private bool listed;
    private int refreshGen;
    private int listGen;
    private int searchGen;
    private int changeGen;
    private bool closed;

    /// <summary>A controller over <paramref name="options"/>.</summary>
    public ContactsListController(ContactsOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);
        Options = options;
    }

    /// <summary>The sidebar, the list or a state changed; the view reads it anew.</summary>
    public event EventHandler? Changed;

    /// <summary>The selected contact changed (null none); the detail opens it (<see cref="ContactsDetailController.Open"/>).</summary>
    public event EventHandler<ContactId?>? Selected;

    /// <summary>Allow… of a consent banner: the window calls account.oauthStart with the account and contacts and opens the page.</summary>
    public event EventHandler<AccountId>? SignInRequested;

    /// <summary>The shared options.</summary>
    public ContactsOptions Options { get; }

    /// <summary>account.list's accounts.</summary>
    public IReadOnlyList<Account> Accounts { get; private set; } = [];

    /// <summary>addressBook.list's books (hidden ones too).</summary>
    public IReadOnlyList<AddressBook> Books { get; private set; } = [];

    /// <summary>The sidebar's rows.</summary>
    public IReadOnlyList<ContactBook.Item> Items { get; private set; } = [];

    /// <summary>The sidebar's selected row.</summary>
    public AddressBookId? SelectedBook { get; private set; }

    /// <summary>The search.</summary>
    public string Query { get; private set; } = "";

    /// <summary>The listed contacts (the pages asked for so far).</summary>
    public IReadOnlyList<ContactSummary> Contacts { get; private set; } = [];

    /// <summary>contact.list's total of the book and search.</summary>
    public int Total { get; private set; }

    /// <summary>The selected contact.</summary>
    public ContactId? SelectedContact { get; private set; }

    /// <summary>The consent banners, in account order.</summary>
    public IReadOnlyList<ContactsConsentNeed> Consent { get; private set; } = [];

    /// <summary>The books have been listed once.</summary>
    public bool Loaded { get; private set; }

    /// <summary>A contact.list without its answer.</summary>
    public bool Loading { get; private set; }

    /// <summary>The last failure's sentence ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>The list's rows: with index headings, flat while searching.</summary>
    public IReadOnlyList<ContactBook.Row> Rows => ContactBook.Rows(Contacts, headings: Query.Length == 0);

    /// <summary>More contacts than listed (the list asks <see cref="LoadMore"/> near its end).</summary>
    public bool HasMore => listed && Contacts.Count < Total;

    /// <summary>The list's subtitle ("All Contacts · 412"); "" before the first answer.</summary>
    public string Subtitle => ContactBook.Find(Items, SelectedBook) is { } it && listed ? ContactBook.ListSubtitle(it, Total) : "";

    /// <summary>The status bar's line.</summary>
    public string Status => ContactBook.StatusLine(Books, Options.Now);

    /// <summary>The list's empty state: search, book or none.</summary>
    public ContactsEmpty ListEmpty =>
        !listed || Loading || Total > 0 ? ContactsEmpty.None : Query.Length == 0 ? ContactsEmpty.Book : ContactsEmpty.Search;

    /// <summary>
    /// The detail's empty state without a selected contact: NoBooks (with
    /// Open Preferences…) when only Recently Used exists, else Selection;
    /// None with a selection or before the books.
    /// </summary>
    public ContactsEmpty DetailEmpty =>
        !Loaded || SelectedContact is not null ? ContactsEmpty.None : ContactBook.HasBooks(Items) ? ContactsEmpty.Selection : ContactsEmpty.NoBooks;

    /// <summary>New Contact has a book to create in.</summary>
    public bool CanCreate => NewContactBook is not null;

    /// <summary>The book New Contact creates in: the selected one when writable, else the default.</summary>
    public AddressBookId? NewContactBook => ContactBook.DefaultBook(Books, SelectedBook, null);

    /// <summary>
    /// A book with its account (null for Recently Used or an account
    /// account.list no longer has); null for an unknown book. The
    /// <see cref="ContactsDetailController"/>'s resolver.
    /// </summary>
    public (AddressBook Book, Account? Account)? Book(AddressBookId id)
    {
        if (Books.FirstOrDefault(b => b.Id == id) is not { } b)
        {
            return null;
        }
        return (b, Accounts.FirstOrDefault(a => a.Id == b.AccountId));
    }

    /// <summary>Loads the accounts, the books and the first page.</summary>
    public void Start() => Refresh();

    /// <summary>Ends the window: no answer is taken any more.</summary>
    public void Stop() => closed = true;

    /// <summary>Loads everything again at once (after the window's own write).</summary>
    public void Reload() => Refresh();

    /// <summary>Asks for the next page; nothing while a page is on its way or everything is listed.</summary>
    public void LoadMore()
    {
        if (Loading || !HasMore || SelectedBook is not { } book)
        {
            return;
        }
        Loading = true;
        var gen = listGen;
        var p = new ContactListParams { BookId = book, Query = Query, Offset = Contacts.Count, Limit = PageSize };
        Options.Call(API.ContactList, p, r =>
        {
            if (closed || gen != listGen)
            {
                return;
            }
            Loading = false;
            if (r.TryGetValue(out var res, out var e))
            {
                var seen = Contacts.Select(c => c.Id).ToHashSet();
                Contacts = [.. Contacts, .. res.Contacts.Where(c => !seen.Contains(c.Id))];
                Total = res.Contacts.Count == 0 ? Contacts.Count : res.Total;
            }
            else
            {
                Error = Options.ErrorMessage(e);
            }
            RaiseChanged();
        });
    }

    /// <summary>Shows a sidebar row; the search stays.</summary>
    public void SelectBook(AddressBookId id)
    {
        if (ContactBook.Find(Items, id) is null || id == SelectedBook)
        {
            return;
        }
        SelectedBook = id;
        Contacts = [];
        Total = 0;
        listed = false;
        ListFirst(PageSize);
        RaiseChanged();
    }

    /// <summary>Searches: the daemon is asked after <see cref="SearchDebounce"/>, from the first character; "" lists the book again.</summary>
    public void SetQuery(string q)
    {
        var t = ContactBook.TrimSpace(q);
        if (System.Text.Encoding.UTF8.GetByteCount(t) > API.Limits.MaxContactQueryBytes)
        {
            t = ContactBook.Clean(t, API.Limits.MaxContactQueryBytes);
        }
        if (t == Query)
        {
            return;
        }
        Query = t;
        var gen = ++searchGen;
        Options.After(SearchDebounce, () =>
        {
            if (closed || gen != searchGen)
            {
                return;
            }
            ListFirst(PageSize);
            RaiseChanged();
        });
    }

    /// <summary>Selects a contact (null none).</summary>
    public void Select(ContactId? id)
    {
        if (id == SelectedContact)
        {
            return;
        }
        SelectedContact = id;
        if (!closed)
        {
            Selected?.Invoke(this, id);
        }
    }

    /// <summary>
    /// Takes a notification of the daemon by its method:
    /// notify.contactsChanged and notify.accountsChanged reload after
    /// <see cref="ChangeDebounce"/> (several in a row once).
    /// </summary>
    public void Handle(string method)
    {
        if (method is not (API.Notify.ContactsChanged or API.Notify.AccountsChanged))
        {
            return;
        }
        var gen = ++changeGen;
        Options.After(ChangeDebounce, () =>
        {
            if (!closed && gen == changeGen)
            {
                Refresh();
            }
        });
    }

    /// <summary>Allow… of an account's banner.</summary>
    public void AllowConsent(AccountId id) => SignInRequested?.Invoke(this, id);

    private void RaiseChanged()
    {
        if (!closed)
        {
            Changed?.Invoke(this, EventArgs.Empty);
        }
    }

    // Lists the accounts and the books, then the list of the shown book with
    // as many rows as are shown, then the consent of accounts without books.
    private void Refresh()
    {
        if (closed)
        {
            return;
        }
        var gen = ++refreshGen;
        Options.Call(API.AccountList, new EmptyParams(), r =>
        {
            if (closed || gen != refreshGen)
            {
                return;
            }
            if (r.TryGetValue(out var res, out _))
            {
                Accounts = res.Accounts;
            }
            Options.Call(API.AddressBookList, new AddressBookListParams(), b =>
            {
                if (closed || gen != refreshGen)
                {
                    return;
                }
                if (!b.TryGetValue(out var books, out var e))
                {
                    Error = Options.ErrorMessage(e);
                    RaiseChanged();
                    return;
                }
                Error = "";
                Books = books.Books;
                Loaded = true;
                Items = ContactBook.Sidebar(Books, Accounts);
                if (ContactBook.Find(Items, SelectedBook) is null)
                {
                    SelectedBook = ContactBook.DefaultSelection(Items);
                    Query = "";
                    listed = false;
                }
                ListFirst(Math.Max(Contacts.Count, PageSize));
                CheckConsent();
                RaiseChanged();
            });
        });
    }

    // Asks for the first limit contacts of the book and search.
    private void ListFirst(int limit)
    {
        var gen = ++listGen;
        if (SelectedBook is not { } book)
        {
            Contacts = [];
            Total = 0;
            listed = true;
            Loading = false;
            KeepSelection();
            return;
        }
        Loading = true;
        var p = new ContactListParams { BookId = book, Query = Query, Limit = Math.Min(limit, API.Limits.MaxContactListLimit) };
        Options.Call(API.ContactList, p, r =>
        {
            if (closed || gen != listGen)
            {
                return;
            }
            Loading = false;
            if (r.TryGetValue(out var res, out var e))
            {
                Error = "";
                Contacts = res.Contacts;
                Total = res.Total;
                listed = true;
                KeepSelection();
            }
            else
            {
                Error = Options.ErrorMessage(e);
            }
            RaiseChanged();
        });
    }

    // Drops a selection the list no longer has.
    private void KeepSelection()
    {
        if (SelectedContact is { } s && !Contacts.Any(c => c.Id == s))
        {
            Select(null);
        }
    }

    // Asks each candidate's books alone; a contactsConsentRequired makes a
    // banner, anything else drops it.
    private void CheckConsent()
    {
        var candidates = ContactBook.ConsentCandidates(Accounts, Books);
        Consent = [.. Consent.Where(c => candidates.Any(a => a.Id == c.AccountId))];
        var gen = refreshGen;
        foreach (var a in candidates)
        {
            var id = a.Id;
            var name = ContactBook.AccountName(a);
            Options.Call(API.AddressBookList, new AddressBookListParams { AccountId = id }, r =>
            {
                if (closed || gen != refreshGen)
                {
                    return;
                }
                var kept = Consent.Where(c => c.AccountId != id).ToList();
                if (r.Error is { } e && ContactsOptions.AsRpcError(e).Code == ErrorCode.ContactsConsentRequired)
                {
                    kept.Add(new ContactsConsentNeed(id, name, ContactsOptions.ConsentData(e)?.SignIn ?? true));
                }
                Consent = SortConsent(kept);
                RaiseChanged();
            });
        }
    }

    private List<ContactsConsentNeed> SortConsent(List<ContactsConsentNeed> consent)
    {
        var pos = new Dictionary<AccountId, int>();
        for (var i = 0; i < Accounts.Count; i++)
        {
            pos[Accounts[i].Id] = i;
        }
        return [.. consent.Select((c, i) => (c, i)).OrderBy(x => pos.GetValueOrDefault(x.c.AccountId)).ThenBy(x => x.i).Select(x => x.c)];
    }
}
