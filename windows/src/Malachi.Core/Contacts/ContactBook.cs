// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Contacts/ContactBook.swift (the sidebar
// and the list, model.go) and of ContactsOptions.swift's ContactBook
// extension (consentCandidates, syncOn, withSync); GTK: ui/internal/contacts
// model.go (Badge, AccountKindBadge, AccountName, AccountLabel,
// BookChoiceText, BookName, ItemKind, Item, Sidebar, HasBooks,
// DefaultSelection, Find, ListSubtitle, SyncedAt, StatusLine, Writable,
// Row, Rows, SectionOf, Breakpoints, Fold, FoldFor) and contactsctl
// client.go and preferences.go (consentCandidates, SyncOn, withSync).
//
// The Contacts window's pure logic (docs/plans/address-book.md §5,
// wireframes W1–W6): the sidebar of address books grouped by account in
// the order of account.list, the list with its index sections, the
// window's breakpoints. The detail is ContactBook.Detail.cs, the form
// ContactBook.Form.cs, Add to Contacts… ContactBook.Add.cs, cleaning
// ContactBook.Clean.cs, the texts ContactsText.
//
// Windows: named ContactBook as in Swift, not Contacts, so that the
// namespace is not hidden; Swift's nested types are nested here as well
// (Cal's way). Go's (value, ok) results are nullable values.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Contacts;

/// <summary>ui/internal/contacts: the address books' pure logic.</summary>
public static partial class ContactBook
{
    /// <summary>The kind capsule of a Microsoft 365 book or account; brand names are never translated.</summary>
    public const string BadgeGraph = "M365";

    /// <summary>The kind capsule of a CardDAV book or account.</summary>
    public const string BadgeCardDAV = "CARDDAV";

    /// <summary>The kind capsule of an eWay-CRM account.</summary>
    public const string BadgeEway = "EWAY";

    // The brand's name, never translated.
    private const string Microsoft365 = "Microsoft 365";

    /// <summary>contacts.Scopes: Recipient Suggestions' choices in their order (the gschema's ContactsSuggestScope); the first is the default.</summary>
    public static IReadOnlyList<ContactSearchScope> Scopes { get; } = [ContactSearchScope.Account, ContactSearchScope.All];

    /// <summary>The gschema nicks of <see cref="Scopes"/>.</summary>
    public static IReadOnlyList<string> ScopeNicks { get; } = [ContactSearchScope.Account, ContactSearchScope.All];

    /// <summary>contacts.ItemKind: a sidebar row's kind.</summary>
    public enum ItemKind
    {
        /// <summary>All Contacts.</summary>
        All,

        /// <summary>An account's heading (not selectable).</summary>
        Account,

        /// <summary>A book.</summary>
        Book,

        /// <summary>The heading On This Computer (not selectable).</summary>
        Local,

        /// <summary>Recently Used.</summary>
        Recent,
    }

    /// <summary>contacts.Badge: a book's kind capsule ("" for Recently Used and an unknown kind).</summary>
    public static string Badge(AddressBookKind k) => k.Value switch
    {
        AddressBookKind.Graph => BadgeGraph,
        AddressBookKind.CardDAV => BadgeCardDAV,
        "eway" => BadgeEway,
        _ => "",
    };

    /// <summary>contacts.AccountKindBadge: M365 for graph, CARDDAV for caldav, EWAY for eway.</summary>
    public static string AccountKindBadge(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return a.Config.ProtocolKind.Value switch
        {
            AccountKind.Graph => BadgeGraph,
            AccountKind.CalDAV => BadgeCardDAV,
            AccountKind.Eway => BadgeEway,
            _ => "",
        };
    }

    /// <summary>contacts.AccountName: an account's heading, its address, else its name.</summary>
    public static string AccountName(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var e = Clean(a.Config.Email, API.Limits.MaxContactFieldBytes);
        return e.Length > 0 ? e : Clean(a.Config.Name, API.Limits.MaxContactFieldBytes);
    }

    /// <summary>
    /// contacts.AccountLabel: an account in a sentence and the detail's
    /// source line: "Microsoft 365 (jana@firma.cz)" for graph, the name (else
    /// the address) of a CardDAV server; "" for null.
    /// </summary>
    public static string AccountLabel(Account? a)
    {
        if (a is null)
        {
            return "";
        }
        if (a.Config.ProtocolKind.Value == AccountKind.Graph)
        {
            var e = AccountName(a);
            return e.Length == 0 ? Microsoft365 : BookChoiceText(Microsoft365, e);
        }
        var n = Clean(a.Config.Name, API.Limits.MaxContactFieldBytes);
        return n.Length == 0 ? AccountName(a) : n;
    }

    /// <summary>contacts.BookChoiceText: a book in a choice of books, "Contacts (jana@firma.cz)".</summary>
    public static string BookChoiceText(string book, string account)
    {
        ArgumentNullException.ThrowIfNull(account);
        if (account.Length == 0)
        {
            return book;
        }
        // TRANSLATORS: an address book with its account, e.g. "Contacts (jana@example.org)"; also a brand with an address.
        return TrimSpace(I18n.L10n.T("%s (%s)", book, account));
    }

    /// <summary>contacts.BookName: Recently Used translated, an empty name as the kind's default.</summary>
    public static string BookName(AddressBook b)
    {
        ArgumentNullException.ThrowIfNull(b);
        if (b.Id == AddressBookId.Recent || b.Kind.Value == AddressBookKind.Recent)
        {
            return ContactsText.RecentlyUsed;
        }
        var n = Clean(b.Name, API.Limits.MaxContactFieldBytes);
        return n.Length == 0 ? ContactsText.WindowTitle : n;
    }

    /// <summary>contacts.Item: a sidebar row.</summary>
    /// <param name="Kind">The row's kind.</param>
    /// <param name="Book">What the row lists (<see cref="AddressBookId.All"/> for All Contacts); null for a heading.</param>
    /// <param name="AccountId">Its account; null for All Contacts, the local rows and a book of no account.</param>
    /// <param name="Title">The row's text.</param>
    /// <param name="Badge">An account heading's kind capsule.</param>
    /// <param name="Count">The row's number of contacts (-1 for a heading).</param>
    /// <param name="ReadOnly">The book cannot be changed (Recently Used, a read-only book; All Contacts never).</param>
    public sealed record Item(
        ItemKind Kind, AddressBookId? Book = null, AccountId? AccountId = null, string Title = "", string Badge = "",
        int Count = 0, bool ReadOnly = false)
    {
        /// <summary>contacts.Item.Selectable: a row that lists contacts.</summary>
        public bool Selectable => Kind is ItemKind.All or ItemKind.Book or ItemKind.Recent;
    }

    /// <summary>
    /// contacts.Sidebar: the sidebar of books (<c>addressBook.list</c>) and
    /// accounts (<c>account.list</c>, for the order and the headings). All
    /// Contacts is there only with a visible book besides Recently Used.
    /// </summary>
    public static IReadOnlyList<Item> Sidebar(IReadOnlyList<AddressBook> books, IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(books);
        ArgumentNullException.ThrowIfNull(accounts);
        var byAccount = new Dictionary<string, List<AddressBook>>(StringComparer.Ordinal);
        var order = new List<string>();
        AddressBook? recent = null;
        var total = 0;
        foreach (var b in books)
        {
            if (b.Id == AddressBookId.Recent || b.Kind.Value == AddressBookKind.Recent)
            {
                recent = b;
                continue;
            }
            if (!b.Visible)
            {
                continue;
            }
            var key = b.AccountId?.Value ?? "";
            if (!byAccount.TryGetValue(key, out var list))
            {
                list = [];
                byAccount[key] = list;
                order.Add(key);
            }
            list.Add(b);
            total += Math.Max(b.Count, 0);
        }
        var output = new List<Item>();
        if (order.Count > 0)
        {
            output.Add(new Item(ItemKind.All, AddressBookId.All, Title: ContactsText.AllContacts, Count: total));
        }
        var known = new Dictionary<string, Account>(StringComparer.Ordinal);
        var accountOrder = new List<string>();
        foreach (var a in accounts)
        {
            known[a.Id.Value] = a;
            if (byAccount.ContainsKey(a.Id.Value))
            {
                accountOrder.Add(a.Id.Value);
            }
        }
        accountOrder.AddRange(order.Where(id => !known.ContainsKey(id)));
        foreach (var id in accountOrder)
        {
            AccountId? accountId = id.Length == 0 ? (AccountId?)null : new AccountId(id);
            var h = new Item(ItemKind.Account, AccountId: accountId, Count: -1);
            if (known.TryGetValue(id, out var a))
            {
                h = h with { Title = AccountName(a), Badge = AccountKindBadge(a) };
            }
            else if (byAccount[id] is [var first, ..])
            {
                h = h with { Badge = Badge(first.Kind) };
            }
            output.Add(h);
            foreach (var b in byAccount[id])
            {
                output.Add(new Item(
                    ItemKind.Book, b.Id, accountId, BookName(b), Count: Math.Max(b.Count, 0),
                    ReadOnly: b.ReadOnly || b.IsSearchOnly || !KnownKind(b.Kind)));
            }
        }
        if (recent is not null)
        {
            output.Add(new Item(ItemKind.Local, Title: ContactsText.OnThisComputer, Count: -1));
            output.Add(new Item(ItemKind.Recent, AddressBookId.Recent, Title: ContactsText.RecentlyUsed, Count: Math.Max(recent.Count, 0), ReadOnly: true));
        }
        return output;
    }

    internal static bool KnownKind(AddressBookKind k) => k.Value is AddressBookKind.Graph or AddressBookKind.CardDAV;

    /// <summary>contacts.HasBooks: a sidebar with a book besides Recently Used.</summary>
    public static bool HasBooks(IReadOnlyList<Item> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        return items.Any(i => i.Kind == ItemKind.Book);
    }

    /// <summary>
    /// contacts.DefaultSelection: the row selected when the window opens or
    /// the selected row is gone: All Contacts, else Recently Used, else
    /// nothing.
    /// </summary>
    public static AddressBookId? DefaultSelection(IReadOnlyList<Item> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        if (items.FirstOrDefault(i => i.Kind == ItemKind.All) is { } all)
        {
            return all.Book;
        }
        return items.FirstOrDefault(i => i.Selectable)?.Book;
    }

    /// <summary>contacts.Find: the selectable row of a book; null none.</summary>
    public static Item? Find(IReadOnlyList<Item> items, AddressBookId? book)
    {
        ArgumentNullException.ThrowIfNull(items);
        return book is { } b ? items.FirstOrDefault(i => i.Selectable && i.Book == b) : null;
    }

    /// <summary>
    /// contacts.ListSubtitle: "All Contacts · 412", "Recently Used · 318
    /// addresses", "Family · 82 contacts"; <paramref name="total"/> is
    /// <c>contact.list</c>'s.
    /// </summary>
    public static string ListSubtitle(Item item, int total)
    {
        ArgumentNullException.ThrowIfNull(item);
        return item.Kind switch
        {
            ItemKind.All => ContactsText.TwoParts(item.Title, ContactsText.Number(total)),
            ItemKind.Recent => ContactsText.TwoParts(item.Title, ContactsText.AddressCount(total)),
            _ => ContactsText.TwoParts(item.Title, ContactsText.ContactCount(total)),
        };
    }

    /// <summary>contacts.SyncedAt: the oldest lastSyncAt of the visible books of the accounts; null when no book has one.</summary>
    public static DateTimeOffset? SyncedAt(IReadOnlyList<AddressBook> books)
    {
        ArgumentNullException.ThrowIfNull(books);
        DateTimeOffset? oldest = null;
        foreach (var b in books)
        {
            if (!b.Visible || b.LastSyncAt is not { } t || b.Id == AddressBookId.Recent)
            {
                continue;
            }
            if (oldest is null || t < oldest)
            {
                oldest = t;
            }
        }
        return oldest;
    }

    /// <summary>contacts.StatusLine: <see cref="ContactsText.SyncedLine"/> with the oldest synchronisation, "" without one.</summary>
    public static string StatusLine(IReadOnlyList<AddressBook> books, DateTimeOffset now) =>
        SyncedAt(books) is { } t ? ContactsText.SyncedLine(t, now) : "";

    /// <summary>
    /// contacts.Writable: a book a contact can be created in (visible or
    /// not): not Recently Used, not read only, not search only, of a known
    /// kind.
    /// </summary>
    public static bool Writable(AddressBook b)
    {
        ArgumentNullException.ThrowIfNull(b);
        return b.Id != AddressBookId.Recent && !b.ReadOnly && !b.IsSearchOnly && KnownKind(b.Kind);
    }

    /// <summary>contacts.Row: a row of the list, a section heading or a contact.</summary>
    /// <param name="Heading">A section's letter ("" for a contact's row).</param>
    /// <param name="Contact">The contact; null for a heading.</param>
    /// <param name="Title">The contact's name (<see cref="SummaryTitle"/>).</param>
    /// <param name="Detail">Its company, else its address.</param>
    /// <param name="Initials">The avatar's letters.</param>
    public sealed record Row(string Heading = "", ContactSummary? Contact = null, string Title = "", string Detail = "", string Initials = "")
    {
        /// <summary>A section heading.</summary>
        public bool IsHeading => Heading.Length > 0;
    }

    /// <summary>
    /// contacts.Rows: a heading before the first contact of each section
    /// (consecutive contacts with the same section, in <c>contact.list</c>'s
    /// order; an empty section is "#"); a search's rows have no headings.
    /// </summary>
    public static IReadOnlyList<Row> Rows(IReadOnlyList<ContactSummary> list, bool headings)
    {
        ArgumentNullException.ThrowIfNull(list);
        var output = new List<Row>();
        var last = "\0";
        foreach (var c in list)
        {
            var sec = SectionOf(c);
            if (headings && sec != last)
            {
                output.Add(new Row(Heading: sec));
                last = sec;
            }
            var title = SummaryTitle(c);
            var detail = Clean(c.Org, API.Limits.MaxContactFieldBytes);
            if (detail.Length == 0 || detail == title)
            {
                detail = Clean(c.Email, API.Limits.MaxContactFieldBytes);
            }
            if (detail == title)
            {
                detail = "";
            }
            output.Add(new Row(Contact: c, Title: title, Detail: detail, Initials: Initials(title, c.Email ?? "")));
        }
        return output;
    }

    /// <summary>contacts.SectionOf: the section cleaned to one upper case character, "#" when empty.</summary>
    public static string SectionOf(ContactSummary c)
    {
        ArgumentNullException.ThrowIfNull(c);
        var s = Clean(c.Section, 16);
        foreach (var r in s.EnumerateRunes())
        {
            return Upper(r);
        }
        return "#";
    }

    /// <summary>contacts.Breakpoints: below <see cref="Sidebar"/> the sidebar collapses, below <see cref="Detail"/> the detail covers the list.</summary>
    /// <param name="Sidebar">The width the sidebar collapses below.</param>
    /// <param name="Detail">The width the detail covers the list below.</param>
    public readonly record struct Breakpoints(double Sidebar, double Detail);

    /// <summary>contacts.MacBreakpoints, in points (W5 d).</summary>
    public static Breakpoints MacBreakpoints { get; } = new(720, 520);

    /// <summary>contacts.GTKBreakpoints; the Windows window folds like its main window (900/600 DIP).</summary>
    public static Breakpoints GtkBreakpoints { get; } = new(900, 600);

    /// <summary>contacts.Fold: how a window of a width is laid out.</summary>
    /// <param name="SidebarCollapsed">The sidebar is hidden (shown over the list on demand).</param>
    /// <param name="DetailOverList">One column; a selected contact's detail covers the list.</param>
    public readonly record struct Fold(bool SidebarCollapsed = false, bool DetailOverList = false);

    /// <summary>contacts.FoldFor: the layout of a window of <paramref name="width"/>.</summary>
    public static Fold FoldFor(double width, Breakpoints bp) => new(width < bp.Sidebar, width < bp.Detail);

    // Accounts' contacts switches (contactsctl client.go and preferences.go).

    /// <summary>
    /// contactsctl.consentCandidates: the Microsoft accounts with contacts on
    /// and no listed book (<c>addressBook.list</c> left them out, perhaps for
    /// the consent).
    /// </summary>
    public static IReadOnlyList<Account> ConsentCandidates(IReadOnlyList<Account> accounts, IReadOnlyList<AddressBook> books)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        ArgumentNullException.ThrowIfNull(books);
        var have = books.Where(b => b.AccountId is not null).Select(b => b.AccountId!.Value).ToHashSet();
        return [.. accounts.Where(a =>
            a.Enabled && a.Config.ProtocolKind.Value == AccountKind.Graph && a.Can(Capability.Contacts)
            && a.Config.Graph?.Contacts?.Sync == true && !have.Contains(a.Id))];
    }

    /// <summary>contactsctl.SyncOn: an account's contacts switch, graph.contacts.sync or caldav.contacts.</summary>
    public static bool SyncOn(AccountConfig c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.ProtocolKind.Value switch
        {
            AccountKind.Graph => c.Graph?.Contacts?.Sync ?? false,
            AccountKind.CalDAV => c.CalDAV?.ContactsOn ?? false,
            _ => false,
        };
    }

    /// <summary>contactsctl.withSync: <paramref name="c"/> with the contacts switch set (graph or caldav; any other kind as it is).</summary>
    public static AccountConfig WithSync(AccountConfig c, bool on)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.ProtocolKind.Value switch
        {
            AccountKind.Graph => c with
            {
                Graph = (c.Graph ?? new GraphConfig { Source = "" }) with { Contacts = new ContactsConfig { Sync = on } },
            },
            AccountKind.CalDAV => c with
            {
                CalDAV = (c.CalDAV ?? new CalDAVConfig { ServerUrl = "", Username = "" }) with { Contacts = on },
            },
            _ => c,
        };
    }
}
