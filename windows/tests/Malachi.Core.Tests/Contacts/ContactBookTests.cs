// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ContactBookTests.swift
// (ContactBookTests); GTK: ui/internal/contacts contacts_test.go (the
// template test po_test.go is StringsCheckTests here). Fictional people
// only.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Xunit;

namespace Malachi.Core.Tests.Contacts;

public sealed class ContactBookTests
{
    private static readonly ContactBook.Strftime English = ContactBook.EnglishStrftime;

    internal static DateTimeOffset Stamp(string s) => DateTimeOffset.Parse(s, System.Globalization.CultureInfo.InvariantCulture);

    internal static Account Account(string id, string name, string email, AccountKind? kind = null) => new()
    {
        Id = id,
        Config = new AccountConfig { Name = name, Email = email, Kind = kind },
        Enabled = true,
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
    };

    internal static IReadOnlyList<Account> Accounts() =>
    [
        Account("acc_m365", "Work", "jana@firma.cz", AccountKind.Graph),
        Account("acc_imap", "Home", "jana@example.org"),
        Account("acc_dav", "Nextcloud", "", AccountKind.CalDAV),
    ];

    internal static IReadOnlyList<AddressBook> Books()
    {
        var t = Stamp("2026-10-30T10:00:00Z");
        var t2 = t.AddHours(-1);
        return
        [
            new() { Id = "b_dav", AccountId = "acc_dav", Name = "Personal", Kind = AddressBookKind.CardDAV, Visible = true, Count = 52, LastSyncAt = t2 },
            new() { Id = "b_m1", AccountId = "acc_m365", Name = "Contacts", Kind = AddressBookKind.Graph, Visible = true, Count = 318, LastSyncAt = t },
            new() { Id = "b_m2", AccountId = "acc_m365", Name = "Suppliers", Kind = AddressBookKind.Graph, ReadOnly = true, Visible = true, Count = 41 },
            new() { Id = "b_m3", AccountId = "acc_m365", Name = "Archive 2019", Kind = AddressBookKind.Graph, Visible = false, Count = 122 },
            new() { Id = AddressBookId.Recent, Name = "recent", Kind = AddressBookKind.Recent, ReadOnly = true, Visible = true, Count = 1204 },
        ];
    }

    internal static ContactCard Card() => new()
    {
        Id = "p_1",
        BookId = "b_m1",
        Revision = "r1",
        Name = new ContactName { Given = "Jana", Family = "Nováková" },
        DisplayName = "Jana Nováková",
        Org = "Stavby Novák s.r.o.",
        Title = "Vedoucí nákupu",
        Emails = [new ContactEmail { Label = ContactLabel.Work, Address = "jana.novakova@example.cz" }, new ContactEmail { Label = ContactLabel.Home, Address = "jana@example.org" }],
        Phones = [new ContactPhone { Label = ContactLabel.Mobile, Number = "+420 601 234 567" }, new ContactPhone { Label = "Kancelář", Number = "1" }],
        Addresses = [new ContactAddress { Label = ContactLabel.Work, Street = "Dlouhá 12", City = "Praha 1", Postcode = "110 00" }, new ContactAddress { Street = "  " }],
        Urls = [new ContactUrl { Url = "stavbynovak.example.cz" }, new ContactUrl { Label = ContactLabel.Work, Url = "javascript:alert(1)" }],
        Birthday = new ContactBirthday { Month = 3, Day = 14 },
        Note = "Volat po 10. hodině.\r\n\r\n\r\n\r\nDruhý řádek",
    };

    [Fact]
    public void SidebarGroupsByAccountOrder()
    {
        var items = ContactBook.Sidebar(Books(), Accounts());
        Assert.Equal(
            ["All Contacts/", "jana@firma.cz/M365", "Contacts/", "Suppliers/", "Nextcloud/CARDDAV", "Personal/", "On This Computer/", "Recently Used/"],
            items.Select(i => i.Title + "/" + i.Badge));
        Assert.Equal(318 + 41 + 52, items[0].Count);
        Assert.Equal(AddressBookId.All, items[0].Book);
        Assert.True(!items[1].Selectable && !items[6].Selectable && items[7].Selectable && items[7].ReadOnly && items[3].ReadOnly && !items[2].ReadOnly);
        Assert.True(ContactBook.HasBooks(items));
        Assert.Equal(AddressBookId.All, ContactBook.DefaultSelection(items));
        Assert.Equal("acc_dav", ContactBook.Find(items, "b_dav")?.AccountId?.Value);
        Assert.Null(ContactBook.Find(items, "b_m3")); // a hidden book is not listed
    }

    [Fact]
    public void SidebarOnlyRecent()
    {
        IReadOnlyList<AddressBook> books = [new() { Id = AddressBookId.Recent, Kind = AddressBookKind.Recent, Visible = true, Count = 3 }];
        var items = ContactBook.Sidebar(books, []);
        Assert.True(items.Count == 2 && items[1].Kind == ContactBook.ItemKind.Recent && !ContactBook.HasBooks(items));
        Assert.Equal(AddressBookId.Recent, ContactBook.DefaultSelection(items));
        Assert.Null(ContactBook.DefaultSelection([]));
        // A book of an account account.list no longer has stays, at the end.
        items = ContactBook.Sidebar([.. books, new AddressBook { Id = "b_x", AccountId = "acc_gone", Kind = AddressBookKind.CardDAV, Visible = true }], []);
        Assert.True(items[1].Kind == ContactBook.ItemKind.Account && items[1].Badge == ContactBook.BadgeCardDAV && items[2].Title == "Contacts");
    }

    [Fact]
    public void ListSubtitlesAndStatus()
    {
        var items = ContactBook.Sidebar(Books(), Accounts());
        Assert.Equal("All Contacts · 412", ContactBook.ListSubtitle(items[0], 412));
        Assert.Equal("Recently Used · 1 address", ContactBook.ListSubtitle(items[7], 1));
        Assert.Equal("Personal · 82 contacts", ContactBook.ListSubtitle(items[5], 82));
        Assert.Equal(Stamp("2026-10-30T09:00:00Z"), ContactBook.SyncedAt(Books()));
        var now = Stamp("2026-10-30T10:03:00Z");
        Assert.StartsWith("Contacts synchronized ", ContactBook.StatusLine(Books(), now), StringComparison.Ordinal);
        Assert.Equal("", ContactBook.StatusLine([], now));
    }

    [Fact]
    public void RowsSections()
    {
        IReadOnlyList<ContactSummary> list =
        [
            new() { Id = "p1", BookId = "b", DisplayName = "Tomáš Adámek", Section = "A", Org = "Stavby" },
            new() { Id = "p2", BookId = "b", DisplayName = "Lucie Andělová", Section = "A", Email = "lucie@example.com" },
            new() { Id = "p3", BookId = "b", DisplayName = "", Section = "", Email = "x@example.com" },
            new() { Id = "p4", BookId = "b", DisplayName = "‮Evil", Section = "č" },
        ];
        var rows = ContactBook.Rows(list, headings: true);
        Assert.Equal(
            ["[A]", "Tomáš Adámek|Stavby|TA", "Lucie Andělová|lucie@example.com|LA", "[#]", "x@example.com||X", "[Č]", "Evil||E"],
            rows.Select(r => r.IsHeading ? "[" + r.Heading + "]" : r.Title + "|" + r.Detail + "|" + r.Initials));
        Assert.Equal(4, ContactBook.Rows(list, headings: false).Count);
    }

    [Fact]
    public void Detail()
    {
        var d = ContactBook.Detail(Card(), English);
        Assert.Equal(("Jana Nováková", "Vedoucí nákupu · Stavby Novák s.r.o.", "JN", false), (d.Title, d.Subtitle, d.Initials, d.ReadOnly));
        Assert.Equal(
            [
                "work email=jana.novakova@example.cz=", "home email=jana@example.org=",
                "mobile=+420 601 234 567=", "Kancelář=1=",
                "work=Dlouhá 12, 110 00 Praha 1=",
                "birthday=14 March=",
                "website=stavbynovak.example.cz=https://stavbynovak.example.cz", "work website=javascript:alert(1)=",
                "note=Volat po 10. hodině.\n\n\nDruhý řádek=",
            ],
            d.Rows.Select(r => r.Label + "=" + r.Value + "=" + r.Url));
        var a = d.Actions;
        Assert.True(a.WriteEmail && a.Emails.Count == 2 && a.ShowMail == "from:jana.novakova@example.cz" && a.Edit && a.Delete);
        var c = Card() with { Emails = [Card().Emails[0]], ReadOnly = true };
        var ro = ContactBook.Detail(c, English);
        Assert.True(ro.Actions.Emails.Count == 0 && !ro.Actions.Edit && !ro.Actions.Delete && ro.ReadOnly);
        var empty = ContactBook.Detail(new ContactCard(), English);
        Assert.True(empty.Title == "No Name" && empty.Initials == "" && !empty.Actions.WriteEmail && empty.Actions.ShowMail == "");
    }

    [Fact]
    public void CopyText()
    {
        var got = ContactBook.CopyText(Card() with { Note = "" }, English);
        Assert.StartsWith("Jana Nováková\nVedoucí nákupu · Stavby Novák s.r.o.\nwork email: jana.novakova@example.cz\n", got, StringComparison.Ordinal);
        Assert.DoesNotContain("BEGIN:VCARD", got, StringComparison.Ordinal);
    }

    [Fact]
    public void Birthday()
    {
        Assert.Equal("14 March 1980", ContactBook.BirthdayText(new ContactBirthday { Year = 1980, Month = 3, Day = 14 }, English));
        Assert.Equal("29 February", ContactBook.BirthdayText(new ContactBirthday { Month = 2, Day = 29 }, English));
        foreach (var b in new ContactBirthday[]
        {
            new() { Year = 2023, Month = 2, Day = 29 }, new() { Month = 13, Day = 1 }, new() { Month = 4, Day = 31 },
            new() { Month = 1, Day = 0 }, new() { Year = -1, Month = 1, Day = 1 },
        })
        {
            Assert.False(ContactBook.ValidBirthday(b), b.ToString());
            Assert.Equal("", ContactBook.BirthdayText(b, English));
        }
    }

    [Theory]
    [InlineData("jana@example.org", "from:jana@example.org")]
    [InlineData("\"a b\"@example.org", "from:\"a b@example.org\"")]
    [InlineData("„x“@e.cz", "from:x@e.cz")]
    [InlineData("a\nb@e.cz", "from:\"a b@e.cz\"")]
    [InlineData("  ", "")]
    [InlineData("\"\"", "")]
    [InlineData("jana@example.org\0x", "from:\"jana@example.org x\"")]
    public void ShowMailQuery(string input, string want) => Assert.Equal(want, ContactBook.ShowMailQuery(input));

    [Theory]
    [InlineData("https://example.org/a", "https://example.org/a")]
    [InlineData("http://example.org", "http://example.org")]
    [InlineData("example.org/x", "https://example.org/x")]
    [InlineData("javascript:alert(1)", "")]
    [InlineData("file:///etc/passwd", "")]
    [InlineData("https://user:pw@example.org", "")]
    [InlineData("ftp://example.org", "")]
    [InlineData("localhost", "")]
    [InlineData("/relative", "")]
    [InlineData("exa mple.org", "")]
    [InlineData("https://exa‮mple.org/path", "https://example.org/path")]
    [InlineData("HTTPS://Example.org/A", "https://Example.org/A")]
    public void OpenUrl(string input, string want) => Assert.Equal(want, ContactBook.OpenUrl(input));

    [Fact]
    public void Labels()
    {
        Assert.Equal("work", ContactBook.LabelText(ContactBook.FieldKind.Email, ContactLabel.Work));
        Assert.Equal("No Label", ContactBook.LabelText(ContactBook.FieldKind.Email, ContactLabel.None));
        Assert.Equal("fax", ContactBook.LabelText(ContactBook.FieldKind.Phone, ContactLabel.Fax));
        Assert.Equal("home", ContactBook.LabelText(ContactBook.FieldKind.Url, ContactLabel.Home));
        Assert.Equal("x-custom", ContactBook.LabelText(ContactBook.FieldKind.Phone, "x-‮custom"));
        Assert.Equal("other", ContactBook.LabelText(ContactBook.FieldKind.Address, ContactLabel.Other));
        Assert.Equal("email", ContactBook.DetailLabel(ContactBook.FieldKind.Email, ContactLabel.None));
        Assert.Equal("phone", ContactBook.DetailLabel(ContactBook.FieldKind.Phone, ContactLabel.None));
        Assert.Equal("address", ContactBook.DetailLabel(ContactBook.FieldKind.Address, ContactLabel.None));
        Assert.Equal("website", ContactBook.DetailLabel(ContactBook.FieldKind.Url, ContactLabel.Other));
        Assert.Equal("work website", ContactBook.DetailLabel(ContactBook.FieldKind.Url, ContactLabel.Work));
        Assert.Equal("other email", ContactBook.DetailLabel(ContactBook.FieldKind.Email, ContactLabel.Other));
        var phone = ContactBook.LabelChoices(ContactBook.FieldKind.Phone, "custom");
        Assert.True(phone.Count == 9 && phone[7] == ContactLabel.None && phone[8] == "custom");
        Assert.Equal(4, ContactBook.LabelChoices(ContactBook.FieldKind.Email, ContactLabel.Work).Count);
        Assert.Equal(ContactLabel.None, new ContactLabel(""));
        Assert.True(new ContactLabel(null).IsNone && default(ContactLabel).Value == "");
    }

    [Fact]
    public void AddressLineAndClean()
    {
        var a = new ContactAddress { Street = "Dlouhá 12\nvchod B", City = "Praha", Region = " ", Postcode = "110 00", Country = "Česko​" };
        Assert.Equal("Dlouhá 12, vchod B, 110 00 Praha, Česko", ContactBook.AddressLine(a));
        Assert.Equal("a b c", ContactBook.Clean("a\t⁦b⁩  c\u0007", 0));
        Assert.Equal("ěě", ContactBook.Clean("ěěě", 5));
        Assert.Equal("a\nb\n\nc", ContactBook.CleanBlock("a\r\nb\r\rc", 0));
    }

    [Theory]
    [InlineData("Jana Nováková", "", "JN")]
    [InlineData("eva", "", "E")]
    [InlineData("", "x@y.cz", "X")]
    [InlineData("", "", "")]
    [InlineData("(Ing.) Petr  van Dyk", "", "ID")]
    [InlineData("Řehoř Šťastný", "", "ŘŠ")]
    public void Initials(string name, string address, string want) => Assert.Equal(want, ContactBook.Initials(name, address));

    [Fact]
    public void AccountLabels()
    {
        var accs = Accounts();
        Assert.Equal("Microsoft 365 (jana@firma.cz)", ContactBook.AccountLabel(accs[0]));
        Assert.Equal("Nextcloud", ContactBook.AccountLabel(accs[2]));
        Assert.Equal("", ContactBook.AccountLabel(null));
        Assert.Equal("Address book Contacts · Microsoft 365 (jana@firma.cz)", ContactsText.SourceLine("Contacts", ContactBook.AccountLabel(accs[0])));
        Assert.Equal("Address book", ContactsText.SuggestionTooltip("", ""));
        Assert.Equal("Kontakty", ContactsText.SuggestionTooltip("Kontakty", ""));
        Assert.Equal("Kontakty · Microsoft 365", ContactsText.SuggestionTooltip("Kontakty", "Microsoft 365"));
        Assert.Equal("X", ContactsText.SuggestionTooltip("", "X"));
        Assert.Equal(ContactBook.BadgeEway, ContactBook.Badge("eway"));
        Assert.Equal("", ContactBook.Badge("weird"));
        Assert.Equal("", ContactBook.AccountKindBadge(accs[1]));
    }

    [Fact]
    public void Errors()
    {
        foreach (var c in new ErrorCode[]
        {
            ErrorCode.ContactNotFound, ErrorCode.AddressBookNotFound, ErrorCode.AddressBookReadOnly, ErrorCode.ContactConflict,
            ErrorCode.ContactsConsentRequired, ErrorCode.Offline, ErrorCode.InvalidArgument,
        })
        {
            Assert.False(string.IsNullOrEmpty(ContactsText.ErrorText(c, "")), c.Name);
        }
        Assert.Equal("Microsoft needs your permission to access the contacts of petr@firma2.cz.", ContactsText.ErrorText(ErrorCode.ContactsConsentRequired, "petr@firma2.cz"));
        Assert.Null(ContactsText.ErrorText(ErrorCode.NetworkError, "")); // network error has its own sentence
        var q = ContactsText.DeleteQuestion("Jana Nováková", "Microsoft 365 (jana@firma.cz)");
        Assert.True(q.Heading == "Delete contact Jana Nováková?" && q.Destructive && q.Body.Contains("Microsoft 365 (jana@firma.cz)", StringComparison.Ordinal));
        var discard = ContactsText.DiscardQuestion;
        Assert.True(discard.Heading == "Discard Changes?" && discard.Confirm == "Discard" && discard.Destructive);
        Assert.Equal("Copied to Clipboard", ContactsText.Copied);
        Assert.Equal("Contact added", ContactsText.ContactAdded);
        Assert.Equal(("No Contacts Found", "Try another name, company or address."), ContactsText.EmptyText(ContactsEmpty.Search));
        Assert.Equal(("", ""), ContactsText.EmptyText(ContactsEmpty.None));
        Assert.Equal("All Address Books", ContactsText.ScopeText(ContactSearchScope.All));
        Assert.Equal("Address Books of the Account You Write From", ContactsText.ScopeText(ContactSearchScope.Account));
    }

    [Fact]
    public void Fold()
    {
        Assert.Equal(new ContactBook.Fold(), ContactBook.FoldFor(900, ContactBook.MacBreakpoints));
        Assert.Equal(new ContactBook.Fold(SidebarCollapsed: true), ContactBook.FoldFor(719, ContactBook.MacBreakpoints));
        Assert.Equal(new ContactBook.Fold(true, true), ContactBook.FoldFor(519, ContactBook.MacBreakpoints));
        Assert.Equal(new ContactBook.Fold(), ContactBook.FoldFor(720, ContactBook.MacBreakpoints));
        Assert.True(ContactBook.FoldFor(800, ContactBook.GtkBreakpoints).SidebarCollapsed);
    }

    [Fact]
    public void AccountsContactsSwitches()
    {
        Assert.True(ContactBook.SyncOn(ContactBook.WithSync(new AccountConfig { Name = "", Email = "", Kind = AccountKind.Graph }, true)));
        Assert.True(ContactBook.SyncOn(ContactBook.WithSync(new AccountConfig { Name = "", Email = "", Kind = AccountKind.CalDAV }, true)));
        Assert.False(ContactBook.SyncOn(new AccountConfig { Name = "", Email = "" }));
        Assert.False(ContactBook.SyncOn(ContactBook.WithSync(new AccountConfig { Name = "", Email = "" }, true)));
    }
}
