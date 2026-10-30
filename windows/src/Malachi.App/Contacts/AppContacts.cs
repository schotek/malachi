// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Contacts.swift
// (AppContacts and Integration.wireContacts); GTK:
// ui/internal/window/contacts_notify.go (the routing of
// notify.contactsChanged) and contacts_entry.go (app.contacts).
//
// The application's address books (docs/plans/address-book.md §5): the
// controllers' options over the daemon (RpcContactsCaller over the app's
// client) or the invented books of MALACHI_CONTACTS_SAMPLES=1
// (ContactsSampleCaller, whose writes come back as Notified, handed on as
// the daemon's notify.contactsChanged), the address chips' lookup
// (ContactsLookupController, forgotten on notify.contactsChanged), the
// books and accounts the chips and Add to Contacts… choose from, the one
// Contacts window, and the ways out of it into the rest of the
// application: Write Email (a compose window), Show Mail (the main
// window's search over every account), Allow… (the re-sign-in with the
// contacts scope, in the browser) and Preferences → Contacts. The window
// has no keyboard shortcut (the owner's answer 5): the "…" menus' Contacts
// and the chips' Show Contact open it. Nothing here logs a contact.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App.Contacts;

/// <summary>The address books of the whole application (macOS <c>AppContacts</c>).</summary>
public sealed partial class AppContacts : IDisposable
{
    private readonly AppState state;
    private readonly ILogger logger;
    private readonly List<IDisposable> tokens = [];
    private readonly ContactsSampleCaller? samples;
    private int refreshGen;
    private ContactsWindow? window;

    /// <summary>The address books of <paramref name="state"/>.</summary>
    public AppContacts(AppState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        this.state = state;
        logger = state.Logs.CreateLogger<AppContacts>();
        IContactsCaller caller;
        if (ContactsSampleCaller.SamplesOn)
        {
            samples = new ContactsSampleCaller(TimeProvider.System.GetUtcNow());
            caller = samples;
            // The samples' writes, as the daemon's notification, on the UI thread.
            samples.Notified += (_, n) => state.Dispatcher.TryEnqueue(() => ContactsChanged(n));
        }
        else
        {
            caller = new RpcContactsCaller(state.Client);
        }
        Options = new ContactsOptions(caller, ContactBook.CultureStrftime(CultureInfo.CurrentCulture), logger: state.Logs.CreateLogger<ContactsOptions>())
        {
            RpcText = RpcErrorText.Text,
        };
        Lookup = new ContactsLookupController(Options);
        tokens.Add(state.Notifications.AddContactsChanged(ContactsChanged));
        tokens.Add(state.Notifications.AddAccountsChanged(AccountsChanged));
        tokens.Add(state.Notifications.AddConnectionState(s =>
        {
            if (s is ConnectionState.Connected or ConnectionState.InfoFailed)
            {
                Refresh();
            }
        }));
        if (samples is not null)
        {
            Refresh();
        }
    }

    /// <summary>A notify.contactsChanged or notify.accountsChanged arrived (the open Contacts pages reload).</summary>
    public event EventHandler<string>? Changed;

    /// <summary>The controllers' options.</summary>
    public ContactsOptions Options { get; }

    /// <summary>The address chips' contact.lookup.</summary>
    public ContactsLookupController Lookup { get; }

    /// <summary>The invented address books of MALACHI_CONTACTS_SAMPLES=1.</summary>
    public bool Samples => samples is not null;

    /// <summary>addressBook.list as last answered.</summary>
    public IReadOnlyList<AddressBook> Books { get; private set; } = [];

    /// <summary>account.list as last answered.</summary>
    public IReadOnlyList<Account> Accounts { get; private set; } = [];

    /// <summary>The open Contacts window, if any.</summary>
    internal ContactsWindow? Window => window;

    /// <summary>Lists the accounts and the books again (the chips choose between Show Contact and Add to Contacts… from them).</summary>
    public void Refresh() => _ = RefreshAsync();

    /// <summary>The account label of a sentence ("Microsoft 365 (jana@…)"); "" for an unknown account.</summary>
    public string AccountLabel(AccountId? id) =>
        id is { } a && Accounts.FirstOrDefault(x => x.Id == a) is { } account ? ContactBook.AccountLabel(account) : "";

    /// <summary>
    /// The "…" menus' Contacts: the one Contacts window to the front (opened
    /// when there is none). Null past Quit's point of no return.
    /// </summary>
    public ContactsWindow? ShowWindow()
    {
        if (window is { } w)
        {
            WindowPresenter.Present(w);
            return w;
        }
        if (state.IsStopping)
        {
            return null;
        }
        var created = new ContactsWindow(state, this);
        window = created;
        created.Closed += (_, _) =>
        {
            if (ReferenceEquals(window, created))
            {
                window = null;
            }
        };
        created.Place(state.Windows.Active?.Window ?? state.MainWindow);
        WindowPresenter.Present(created);
        return created;
    }

    /// <summary>Show Contact of a chip: the window searching the address, the contact selected once listed.</summary>
    public void ShowContact(ContactId id, string address) => ShowWindow()?.Reveal(id, address);

    /// <summary>More Details… of Add to Contacts: the window with the whole form, prefilled.</summary>
    public void NewContact(ContactBook.Form form) => ShowWindow()?.NewContactWith(form);

    /// <summary>
    /// Write Email: a new message to the address, from the contact's account
    /// when it writes mail, else the first account that does (macOS
    /// <c>writeEmail(to:name:account:)</c>).
    /// </summary>
    public void WriteEmail(string address, string name, AccountId? account)
    {
        if (state.Hooks.OpenCompose is not { } open || address.Length == 0)
        {
            return;
        }
        var from = Capabilities.ForwardFrom(Accounts, account ?? new AccountId(""))?.Id ?? account;
        var to = new Address { Name = name.Length == 0 || name == ContactsText.NoName ? null : name, Email = address };
        open(new ComposeParams { Kind = ComposeKind.New, AccountId = from, To = [to] });
    }

    /// <summary>
    /// Show Mail: the main window in Mail with the search
    /// <paramref name="query"/> over every account (A14).
    /// </summary>
    public void ShowMail(string query)
    {
        if (query.Length == 0 || state.MainWindow is not { } w)
        {
            return;
        }
        state.ShowMainWindow();
        w.ShowMailSearch(query);
    }

    /// <summary>Preferences → Contacts (the window's No Address Books).</summary>
    public void OpenPreferences() => Preferences.PreferencesWindow.Show(state)?.ShowContacts();

    /// <summary>
    /// Allow… of a consent banner: the browser with the re-sign-in asking for
    /// the contacts scope (account.oauthStart contacts: true); the daemon
    /// finishes it and its notify.accountsChanged lists the books again. A
    /// failure is a toast in <paramref name="toasts"/>. The samples cannot
    /// sign in.
    /// </summary>
    public void SignIn(AccountId account, Window owner, IToasts toasts) => _ = SignInAsync(account, owner, toasts);

    /// <summary>
    /// The recipient suggestions of a compose window or an event's attendees
    /// (macOS RecipientSuggestionsController.search): contact.search with the
    /// scope of contacts-suggest-scope (Preferences → Contacts), and a
    /// synchronised book's tooltip naming the sending account while the
    /// scope is the account's books.
    /// </summary>
    public void WireSuggestions(Core.Presentation.SuggestionsController controller, Func<AccountId> account)
    {
        ArgumentNullException.ThrowIfNull(controller);
        ArgumentNullException.ThrowIfNull(account);
        var settings = state.Settings;
        controller.Scope = () => settings.ContactsSuggestScope;
        controller.BookAccount = () => settings.ContactsSuggestScope.Value == ContactSearchScope.Account ? AccountLabel(account()) : "";
    }

    /// <summary>The routing ends (Quit).</summary>
    public void Dispose()
    {
        foreach (var t in tokens)
        {
            t.Dispose();
        }
        tokens.Clear();
    }

    private async Task SignInAsync(AccountId account, Window owner, IToasts toasts)
    {
        if (samples is not null)
        {
            toasts.Show(Core.Calendars.Cal.Text.PreviewOnly);
            return;
        }
        switch (await Options.RequestSignInUrlAsync(account))
        {
            case SignInUrl.Open open:
                try
                {
                    await state.Launcher.OpenUrlAsync(open.Url, WindowPresenter.Handle(owner));
                }
                catch (Exception e) when (e is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
                {
                    var detail = e is ArgumentException ? "not an https address" : e.Message; // Windows-only string (the technical detail, as in GTK)
                    // TRANSLATORS: %s is a technical error message.
                    toasts.Show(L10n.T("The link could not be opened: %s", detail));
                }
                break;
            case SignInUrl.Failed failed:
                toasts.Show(failed.Text);
                break;
        }
    }

    private async Task RefreshAsync()
    {
        var gen = ++refreshGen;
        var caller = Options.Caller;
        AccountListResult? a = null;
        AddressBookListResult? b = null;
        try
        {
            a = await caller.CallAsync(API.AccountList, new EmptyParams(), API.AccountList.Timeout, default);
            b = await caller.CallAsync(API.AddressBookList, new AddressBookListParams(), API.AddressBookList.Timeout, default);
        }
#pragma warning disable CA1031 // Not known is the safe answer: the chips offer nothing until the next refresh.
        catch (Exception e)
#pragma warning restore CA1031
        {
            var kind = e.GetType().Name;
            LogRefreshFailed(logger, kind);
        }
        if (gen != refreshGen)
        {
            return;
        }
        if (a is not null)
        {
            Accounts = a.Accounts;
        }
        if (b is not null)
        {
            Books = b.Books;
        }
    }

    private void ContactsChanged(ContactsChangedNotification n)
    {
        Lookup.Invalidate();
        Refresh();
        window?.Handle(n);
        Changed?.Invoke(this, API.Notify.ContactsChanged);
    }

    private void AccountsChanged()
    {
        Refresh();
        window?.HandleAccountsChanged();
        Changed?.Invoke(this, API.Notify.AccountsChanged);
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "contacts: the books could not be listed ({Kind})")]
    private static partial void LogRefreshFailed(ILogger logger, string kind);
}
