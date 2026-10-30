// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ContactBookTests.swift
// (ContactBookFormTests); GTK: ui/internal/contacts form_test.go, with its
// later cases (TestValidateGraphLimits, TestProblemOf) ahead of Swift, and
// the daemon's data.field of docs/api.md §4.19. Fictional people only.

using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Transport;
using Xunit;
using F = Malachi.Core.Tests.Contacts.ContactBookTests;
using Key = Malachi.Core.Contacts.ContactBook.FieldKey;
using Kind = Malachi.Core.Contacts.ContactBook.FieldKind;
using LV = Malachi.Core.Contacts.ContactBook.LabelledValue;

namespace Malachi.Core.Tests.Contacts;

public sealed class ContactBookFormTests
{
    [Fact]
    public void EditFormUnchanged()
    {
        var f = ContactBook.Form.Edit(F.Card());
        Assert.False(f.Dirty || f.IsNew);
        var output = f.Card;
        Assert.Equal(("Jana Nováková", "p_1", "r1"), (output.DisplayName, output.Id.Value, output.Revision));
        f.Given = " Jana "; // whitespace alone is no change
        Assert.False(f.Dirty);
    }

    [Fact]
    public void EditFormChangesAndDisplayName()
    {
        var f = ContactBook.Form.Edit(F.Card());
        f.Family = "Malá";
        f.Emails.Add(new LV(ContactLabel.Other, "  "));
        Assert.Equal([Key.Family], f.Changed);
        var c = f.Card;
        Assert.Equal(("Jana Malá", 2, 1), (c.DisplayName, c.Emails.Count, c.Addresses.Count));
    }

    [Fact]
    public void DisplayNameOnlyCardKeepsItsName()
    {
        var f = ContactBook.Form.Edit(new ContactCard { Id = "p", DisplayName = "Pavel Novák", Phones = [new ContactPhone { Number = "1" }] });
        Assert.True(f.Given == "Pavel" && f.Family == "Novák" && !f.Dirty);
        f.Phones[0] = f.Phones[0] with { Value = "2" };
        var output = f.Card;
        Assert.Equal(new ContactName(), output.Name); // an unchanged name stays as it was
        Assert.Equal("Pavel Novák", output.DisplayName);
    }

    [Fact]
    public void NewFormCard()
    {
        var f = ContactBook.Form.New("b_m1");
        Assert.False(f.Dirty);
        f.Org = "Acme";
        var c = f.Card;
        Assert.Equal(("b_m1", "Acme"), (c.BookId.Value, c.DisplayName));
        var wire = JsonSerializer.SerializeToElement(c, ApiJsonContext.Wire.ContactCard);
        Assert.Equal(JsonValueKind.Array, wire.GetProperty("emails").ValueKind); // lists are sent as arrays
        Assert.Equal(JsonValueKind.Array, wire.GetProperty("urls").ValueKind);
        f.Org = "";
        f.Emails = [new LV(Value: "a@b.cz")];
        Assert.Equal("a@b.cz", f.Card.DisplayName);
    }

    [Fact]
    public void Rows()
    {
        var f = ContactBook.Form.New("b");
        Assert.Equal(0, f.AddRow(Kind.Email));
        Assert.Equal(ContactLabel.Work, f.Emails[0].Label);
        f.AddRow(Kind.Email);
        f.AddRow(Kind.Email);
        f.AddRow(Kind.Email);
        Assert.Equal([ContactLabel.Home, ContactLabel.Other, ContactLabel.Other], f.Emails.Skip(1).Select(e => e.Label));
        f.AddRow(Kind.Phone);
        Assert.Equal(ContactLabel.Mobile, f.Phones[0].Label);
        f.AddRow(Kind.Address);
        f.AddRow(Kind.Url);
        f.RemoveRow(Kind.Email, 0);
        f.RemoveRow(Kind.Email, 9);
        Assert.True(f.Count(Kind.Email) == 3 && f.Emails[0].Label == ContactLabel.Home && f.Count(Kind.Address) == 1 && f.Count(Kind.Url) == 1);
        while (f.CanAdd(Kind.Url))
        {
            f.AddRow(Kind.Url);
        }
        Assert.Null(f.AddRow(Kind.Url));
        Assert.Equal(API.Limits.MaxContactURLs, f.Count(Kind.Url));
    }

    [Fact]
    public void Validate()
    {
        var f = ContactBook.Form.New("b");
        var first = f.Validate();
        Assert.Equal("Enter a name, a company or an email address.", Assert.Single(first).Text);
        f.Given = "Jana";
        f.Emails = [new LV(Value: "jana@example.org"), new LV(Value: "Jana <j@x.cz>"), new LV(Value: "nope"), new LV(Value: ""), new LV(Value: "a@localhost")];
        f.Urls = [new LV(Value: "example.org"), new LV(Value: "javascript:alert(1)"), new LV(Value: "https://ok.cz")];
        f.Birthday = new ContactBirthday { Month = 2, Day = 30 };
        f.Note = new string('x', API.Limits.MaxContactNoteBytes + 1);
        f.Org = new string('o', API.Limits.MaxContactFieldBytes + 1);
        Assert.Equal(
            [
                "This text is too long.", "Jana <j@x.cz> is not a valid email address.", "nope is not a valid email address.",
                "a@localhost is not a valid email address.", "A website must be an address starting with http:// or https://.",
                "Enter a valid date.", "This text is too long.",
            ],
            f.Validate().Select(p => p.Text));
        f = ContactBook.Form.New("b");
        f.Emails = [new LV(Value: "jana@example.org")];
        Assert.Empty(f.Validate()); // an address alone is enough
    }

    [Fact]
    public void RebaseKeepsTheUsersChanges()
    {
        var f = ContactBook.Form.Edit(F.Card());
        f.Title = "Ředitelka";
        f.Phones = [new LV(ContactLabel.Mobile, "+420 777")];
        var server = F.Card() with
        {
            Revision = "r2",
            Org = "Novák a syn",
            Title = "Nákupčí",
            Phones = [.. F.Card().Phones, new ContactPhone { Number = "3" }],
            Note = "Nová poznámka",
        };
        f.Rebase(server);
        Assert.True(f.Org == "Novák a syn" && f.Title == "Ředitelka" && f.Note == "Nová poznámka" && f.Phones.Count == 1 && f.Phones[0].Value == "+420 777");
        Assert.Equal([Key.Title, Key.Phones], f.Changed);
        Assert.Equal("r2", f.Card.Revision);
        f.Saved(null, "r3");
        Assert.True(!f.Dirty && f.Original.Revision == "r3" && f.Original.Title == "Ředitelka");
    }

    [Theory]
    [InlineData("Petr Svoboda", "Petr", "Svoboda")]
    [InlineData("Eva", "Eva", "")]
    [InlineData("", "", "")]
    [InlineData("Jan  Amos Komenský", "Jan Amos", "Komenský")]
    public void SplitName(string input, string given, string family) => Assert.Equal((given, family), ContactBook.SplitName(input));

    [Fact]
    public void AddFromChip()
    {
        var books = F.Books();
        var accs = F.Accounts();
        var a = new ContactBook.AddFromChip(new Address { Name = "Petr ‮Svoboda", Email = "petr.svoboda@example.com" }, "acc_dav", books, accs);
        Assert.True(a.Name == "Petr Svoboda" && a.SelectName && a.Warning.Length > 0 && a.Book == "b_dav" && a.CanAdd);
        Assert.Equal(["Personal (Nextcloud)", "Contacts (jana@firma.cz)", "Archive 2019 (jana@firma.cz)"], a.Books.Select(b => b.Text));
        var f = a.Form();
        Assert.True(f.IsNew && f.Book == "b_dav" && f.Given == "Petr" && f.Family == "Svoboda");
        Assert.Equal([new LV(Value: "petr.svoboda@example.com")], f.Emails);
        // No name: nothing to warn about; an IMAP message goes to the first
        // visible writable book.
        a = new ContactBook.AddFromChip(new Address { Name = "x@y.cz", Email = "x@y.cz" }, "acc_imap", books, accs);
        Assert.True(a.Name == "" && !a.SelectName && a.Warning == "" && a.Book == "b_dav");
        Assert.Equal("b_m1", ContactBook.DefaultBook(books, "b_m1", null));
        Assert.Equal("b_m1", ContactBook.DefaultBook(books, "b_m2", "acc_m365"));
        Assert.Equal("b_m1", ContactBook.DefaultBook(books, null, "acc_m365"));
        Assert.Equal("h", ContactBook.DefaultBook([new AddressBook { Id = "h", Kind = AddressBookKind.Graph, Visible = false }], null, null)); // hidden fallback
        Assert.Null(ContactBook.DefaultBook([], null, null));
        Assert.Equal(ContactBook.ChipItem.Show, ContactBook.ChipItemFor(true, []));
        Assert.Equal(ContactBook.ChipItem.Add, ContactBook.ChipItemFor(false, books));
        Assert.Equal(ContactBook.ChipItem.None, ContactBook.ChipItemFor(false, [books[2]]));
        Assert.Equal("Show Contact", ContactBook.ChipItemText(ContactBook.ChipItem.Show));
        Assert.Equal("Add to Contacts…", ContactBook.ChipItemText(ContactBook.ChipItem.Add));
        Assert.Equal("", ContactBook.ChipItemText(ContactBook.ChipItem.None));
        Assert.False((a with { Address = "bad" }).CanAdd);
    }

    /// <summary>
    /// form_test.go TestValidateGraphLimits: a Microsoft book holds at most 3
    /// e-mail addresses, one address per slot (other and custom labels share
    /// one) and one website; the problem names the first row over, counting
    /// only filled rows. Other books do not have these limits.
    /// </summary>
    [Fact]
    public void ValidateGraphLimits()
    {
        var f = ContactBook.Form.New("b");
        f.Given = "Jana";
        f.Emails = [new LV(Value: "a@b.cz"), new LV(Value: ""), new LV(Value: "c@b.cz"), new LV(Value: "d@b.cz"), new LV(Value: "e@b.cz")];
        f.Addresses =
        [
            new ContactAddress { Label = ContactLabel.Work, City = "A" }, new ContactAddress { Label = "custom", City = "B" },
            new ContactAddress { City = "C" }, new ContactAddress { Label = ContactLabel.Home, City = "D" },
        ];
        f.Urls = [new LV(Value: "a.cz"), new LV(Value: "b.cz")];
        Assert.Empty(f.Validate()); // a CardDAV book
        f.Kind = AddressBookKind.Graph;
        var got = f.Validate().ToDictionary(p => p.Key, p => p.Index);
        Assert.Equal(new Dictionary<Key, int?> { [Key.Emails] = 4, [Key.Addresses] = 2, [Key.Urls] = 1 }, got);
        Assert.Equal("A Microsoft 365 contact has at most 3 email addresses.", f.Validate()[0].Text);
        f.Emails = [.. f.Emails.Take(4)];
        f.Addresses = [.. f.Addresses.Take(2)];
        f.Urls = [.. f.Urls.Take(1)];
        Assert.Empty(f.Validate()); // within the limits
    }

    /// <summary>
    /// form_test.go TestProblemOf: the field from the daemon's message, or
    /// from its data.field (docs/api.md §4.19); anything it cannot read is no
    /// problem (the sentence is said instead).
    /// </summary>
    [Theory]
    [InlineData("card.emails[2].address: not an e-mail address", Key.Emails, 2)]
    [InlineData("card.emails: a Microsoft contact has at most 3 e-mail addresses", Key.Emails, null)]
    [InlineData("card.addresses[1]: a Microsoft contact has one work address", Key.Addresses, 1)]
    [InlineData("card.phones[0].label: over 64 bytes", Key.Phones, 0)]
    [InlineData("card.name.family: over 256 bytes", Key.Family, null)]
    [InlineData("card.urls: a Microsoft contact has one web address", Key.Urls, null)]
    [InlineData("card.note: not UTF-8", Key.Note, null)]
    public void ProblemOfMessage(string message, Key key, int? index)
    {
        var p = ContactBook.ProblemOf(new RpcError { Code = ErrorCode.InvalidArgument, Message = message });
        Assert.Equal(new ContactBook.Problem(key, index, ContactBook.RefusedText), p);
        Assert.Equal("The server did not accept the marked detail. Correct it and try again.", ContactBook.RefusedText);
    }

    [Theory]
    [InlineData("emails[2].address", Key.Emails, 2)]
    [InlineData("name.given", Key.Given, null)]
    [InlineData("addresses[0]", Key.Addresses, 0)]
    [InlineData("emails", Key.Emails, null)]
    [InlineData("card.urls[1].url", Key.Urls, 1)]
    public void ProblemOfData(string field, Key key, int? index)
    {
        var data = JsonSerializer.SerializeToElement(new ContactFieldData { Field = field }, ApiJsonContext.Wire.ContactFieldData);
        var e = new RpcException(new RpcError { Code = ErrorCode.InvalidArgument, Message = "not for display", Data = data });
        Assert.Equal(new ContactBook.Problem(key, index, ContactBook.RefusedText), ContactBook.ProblemOf(e));
    }

    [Fact]
    public void ProblemOfNothing()
    {
        static RpcError Inv(string m) => new() { Code = ErrorCode.InvalidArgument, Message = m };
        foreach (var e in new[]
        {
            null, new RpcError { Code = ErrorCode.Offline, Message = "card.org: x" }, Inv("bookId: unknown"), Inv("card.colour: x"),
            Inv("card.emails[x].address: y"), Inv("card.emails[-1]: y"), Inv("card.emails[2: y"), Inv("card.name.nick: y"), Inv("no colon"),
        })
        {
            Assert.Null(ContactBook.ProblemOf(e));
        }
        Assert.Null(ContactBook.ProblemOf((System.Exception?)null));
    }

    /// <summary>A street of several lines keeps its line breaks in the form and the card; only its ends are trimmed.</summary>
    [Fact]
    public void StreetOfSeveralLinesKeepsItsLines()
    {
        var f = ContactBook.Form.Edit(F.Card() with { Addresses = [new ContactAddress { Street = "Dlouhá 12\nvchod B", City = "Praha" }] });
        Assert.Equal("Dlouhá 12\nvchod B", f.Addresses[0].Street);
        f.Addresses[0] = f.Addresses[0] with { Street = "  Dlouhá 12\nvchod C\n" };
        Assert.Equal([Key.Addresses], f.Changed);
        Assert.Equal("Dlouhá 12\nvchod C", f.Card.Addresses[0].Street);
    }
}
