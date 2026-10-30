// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ContactsControllerTests.swift
// (ContactsListControllerTests, ContactsDetailControllerTests,
// ContactsLookupAndPreferencesTests); GTK: ui/internal/contactsctl
// window_test.go, contact_test.go (with TestContactGraphLimitsAndRefusedField,
// ahead of Swift) and prefs_lookup_test.go; and the sample daemon of
// samples.go. Fictional people only.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;
using Xunit;
using H = Malachi.Core.Tests.Controllers.ContactsHarness;
using LV = Malachi.Core.Contacts.ContactBook.LabelledValue;

namespace Malachi.Core.Tests.Controllers;

public sealed class ContactsControllerTests
{
    private static RpcError Err(int code, object? data = null) => new()
    {
        Code = code,
        Message = "x",
        Data = data switch
        {
            ContactCard c => JsonSerializer.SerializeToElement(new ContactConflictData { Server = c }, ApiJsonContext.Wire.ContactConflictData),
            ContactFieldData f => JsonSerializer.SerializeToElement(f, ApiJsonContext.Wire.ContactFieldData),
            _ => null,
        },
    };

    private static async Task<ContactsListController> StartList(H h)
    {
        var w = await h.Run(() =>
        {
            var w = new ContactsListController(h.Options);
            w.Start();
            return w;
        });
        await h.SettleAsync();
        return w;
    }

    private static string[] Titles(ContactsListController w) => [.. w.Rows.Select(r => r.IsHeading ? "[" + r.Heading + "]" : r.Title)];

    // Windows-only: Allow…'s account.oauthStart asks for the contacts scope
    // and hands out an https page only.
    [Fact]
    public async Task SignInAsksForTheContactsScope()
    {
        using var h = new H();
        h.Daemon.Answer("account.oauthStart", new AccountOAuthStartResult { SessionId = "s1", AuthUrl = "https://login.example.com/authorize?x=1", ExpiresAt = DateTimeOffset.UnixEpoch });
        var open = Assert.IsType<SignInUrl.Open>(await h.Options.RequestSignInUrlAsync(new AccountId("acc_work"), TestContext.Current.CancellationToken));
        Assert.Equal("https://login.example.com/authorize?x=1", open.Url);
        var p = h.Daemon.Last(API.AccountOAuthStart);
        Assert.Equal(new AccountId("acc_work"), p.AccountId);
        Assert.True(p.Contacts);
        Assert.Null(p.Calendar);

        h.Daemon.Answer("account.oauthStart", new AccountOAuthStartResult { SessionId = "s2", AuthUrl = "http://login.example.com/", ExpiresAt = DateTimeOffset.UnixEpoch });
        Assert.IsType<SignInUrl.Failed>(await h.Options.RequestSignInUrlAsync(new AccountId("acc_work"), TestContext.Current.CancellationToken));
        h.Daemon.Fail("account.oauthStart", Err(ErrorCode.Unavailable));
        Assert.IsType<SignInUrl.Failed>(await h.Options.RequestSignInUrlAsync(new AccountId("acc_work"), TestContext.Current.CancellationToken));
    }

    // window_test.go

    [Fact]
    public async Task Loads()
    {
        using var h = new H();
        var w = await StartList(h);
        Assert.True(w.Loaded && w.SelectedBook == AddressBookId.All && w.Error == "");
        Assert.Equal(["[A]", "Adámek", "[B]", "Beneš", "Bílá", "[N]", "Nováková", "[P]", "Pokorný"], Titles(w));
        Assert.Equal("All Contacts · 5", w.Subtitle);
        Assert.Equal("Contacts synchronized 3 minutes ago", w.Status);
        Assert.Equal([new ContactsConsentNeed("acc_p", "petr@firma2.cz", true)], w.Consent);
        var p = h.Daemon.Last(API.ContactList);
        Assert.True(p.BookId == AddressBookId.All && p.Limit == ContactsListController.PageSize && (p.Offset ?? 0) == 0);
        Assert.True(w.ListEmpty == ContactsEmpty.None && w.DetailEmpty == ContactsEmpty.Selection && !w.HasMore);
        Assert.True(w.NewContactBook == "b_m1" && w.CanCreate);
        AccountId? signed = null;
        w.SignInRequested += (_, id) => signed = id;
        w.AllowConsent("acc_p");
        Assert.Equal("acc_p", signed?.Value);
        var dav = w.Book("b_d");
        Assert.True(dav?.Account?.Id == "acc_d" && dav?.Book.Name == "Personal");
        Assert.Null(w.Book(AddressBookId.Recent)?.Account);
    }

    [Fact]
    public async Task SearchDebouncedAndFlat()
    {
        using var h = new H();
        var w = await StartList(h);
        var n = h.Daemon.Count(API.ContactList.Name);
        await h.Run(() =>
        {
            w.SetQuery("b");
            w.SetQuery("be");
        });
        await h.SettleAsync();
        Assert.Equal(n, h.Daemon.Count(API.ContactList.Name)); // not before the debounce
        await h.FireAsync();
        Assert.Equal(n + 1, h.Daemon.Count(API.ContactList.Name));
        Assert.Equal(["Beneš"], Titles(w));
        await h.Run(() => w.SetQuery("zz"));
        await h.FireAsync();
        Assert.Equal(ContactsEmpty.Search, w.ListEmpty);
        await h.Run(() => w.SetQuery(" "));
        await h.FireAsync();
        Assert.True(w.Contacts.Count == 5 && w.ListEmpty == ContactsEmpty.None);
    }

    [Fact]
    public async Task SelectionKeptAcrossReload()
    {
        using var h = new H();
        var w = await StartList(h);
        var selected = new List<ContactId?>();
        w.Selected += (_, id) => selected.Add(id);
        var id = w.Contacts[2].Id;
        await h.Run(() =>
        {
            w.Select(id);
            w.Select(id);
        });
        Assert.Equal([id], selected);
        Assert.Equal(ContactsEmpty.None, w.DetailEmpty);
        // A change elsewhere: one reload after the debounce, selection kept.
        var n = h.Daemon.Count(API.AddressBookList.Name);
        await h.Run(() =>
        {
            w.Handle(API.Notify.ContactsChanged);
            w.Handle(API.Notify.ContactsChanged);
            w.Handle(API.Notify.NewMessage);
        });
        await h.FireAsync();
        Assert.Equal(n + 2, h.Daemon.Count(API.AddressBookList.Name)); // the books and acc_p's consent
        Assert.Equal(id, w.SelectedContact);
        Assert.Single(selected);
        // The contact goes away: the selection with it.
        H.ServeList(h.Daemon, new() { ["b_m1"] = H.People("b_m1", "Adámek") });
        await h.Run(w.Reload);
        await h.SettleAsync();
        Assert.Null(w.SelectedContact);
        Assert.Null(selected[^1]);
    }

    [Fact]
    public async Task BooksAndPaging()
    {
        using var h = new H();
        var w = await StartList(h);
        await h.Run(() => w.SelectBook("b_m2")); // hidden: not in the sidebar
        Assert.Equal(AddressBookId.All, w.SelectedBook);
        await h.Run(() => w.SelectBook(AddressBookId.Recent));
        await h.SettleAsync();
        Assert.Equal("Recently Used · 2 addresses", w.Subtitle);
        Assert.Equal("b_m1", w.NewContactBook?.Value); // recent is not writable
        const string letters = "abcdefghijklmnopqrstuvwxyz";
        var names = Enumerable.Range(0, 250).Select(i => "N" + letters[i % 26] + letters[i / 26]).ToArray();
        H.ServeList(h.Daemon, new() { ["b_d"] = H.People("b_d", names) });
        await h.Run(() => w.SelectBook("b_d"));
        await h.SettleAsync();
        Assert.True(w.Contacts.Count == ContactsListController.PageSize && w.HasMore && w.Total == 250);
        await h.Run(() =>
        {
            w.LoadMore();
            w.LoadMore(); // on its way: nothing
        });
        await h.SettleAsync();
        await h.Run(w.LoadMore);
        await h.SettleAsync();
        Assert.True(w.Contacts.Count == 250 && !w.HasMore);
        Assert.Equal(200, h.Daemon.Last(API.ContactList).Offset);
        // A reload keeps every shown row.
        await h.Run(w.Reload);
        await h.SettleAsync();
        Assert.Equal(250, h.Daemon.Last(API.ContactList).Limit);
        Assert.Equal(250, w.Contacts.Count);
    }

    [Fact]
    public async Task OnlyRecentAndFailure()
    {
        var d = new FakeContactsDaemon();
        d.Answer(API.AccountList.Name, new AccountListResult());
        d.Answer(API.AddressBookList.Name, new AddressBookListResult { Books = [H.Books()[3]] });
        H.ServeList(d, []);
        using var h = new H(d);
        var changes = 0;
        var w = await h.Run(() =>
        {
            var w = new ContactsListController(h.Options);
            w.Changed += (_, _) => changes++;
            w.Start();
            return w;
        });
        await h.SettleAsync();
        Assert.True(w.SelectedBook == AddressBookId.Recent && w.DetailEmpty == ContactsEmpty.NoBooks && w.ListEmpty == ContactsEmpty.Book && !w.CanCreate);
        Assert.True(changes > 0, "no change reported");
        d.Fail(API.AddressBookList.Name, Err(ErrorCode.StorageError));
        await h.Run(w.Reload);
        await h.SettleAsync();
        Assert.NotEqual("", w.Error);
        await h.Run(() =>
        {
            w.Stop();
            w.Reload();
        });
        await h.SettleAsync();
    }

    // contact_test.go

    private static ContactCard Jana() => new()
    {
        Id = "p_1",
        BookId = "b_m1",
        Revision = "r1",
        Name = new ContactName { Given = "Jana", Family = "Nováková" },
        DisplayName = "Jana Nováková",
        Org = "Stavby",
        Emails = [new ContactEmail { Label = ContactLabel.Work, Address = "jana@firma.cz" }],
        Phones = [new ContactPhone { Label = ContactLabel.Mobile, Number = "+420 601" }],
    };

    /// <summary>What the questions asked and what the next answer is.</summary>
    private sealed class Questions
    {
        public List<ContactsQuestion> Asked { get; } = [];

        public bool Answer { get; set; } = true;
    }

    // contactSetup: a detail controller over the standard daemon's window
    // (for the books), contact.get answering card.
    private static async Task<(ContactsDetailController C, Questions Qs)> Setup(H h, ContactCard card)
    {
        var w = await StartList(h);
        h.Daemon.On(API.ContactGet.Name, raw =>
        {
            var p = raw.Deserialize(ApiJsonContext.Wire.ContactGetParams)!;
            return p.ContactId == card.Id ? new ContactGetResult { Contact = card } : throw new RpcException(Err(ErrorCode.ContactNotFound));
        });
        var qs = new Questions();
        var c = await h.Run(() =>
        {
            var c = new ContactsDetailController(h.Options, w.Book)
            {
                AskQuestion = q =>
                {
                    qs.Asked.Add(q);
                    return Task.FromResult(qs.Answer);
                },
            };
            c.Open(card.Id);
            Assert.True(c.Loading);
            return c;
        });
        await h.SettleAsync();
        return (c, qs);
    }

    [Fact]
    public async Task Detail()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        Assert.Equal("Jana Nováková", c.Detail?.Title);
        Assert.True(c.CanEdit && c.CanDelete);
        Assert.Equal("Address book Contacts · Microsoft 365 (jana@firma.cz)", c.SourceLine);
        Assert.StartsWith("Jana Nováková\nStavby\nwork email: jana@firma.cz", c.CopyText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task OpenGone()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        await h.Run(() => c.Open("p_none"));
        await h.SettleAsync();
        Assert.Null(c.Card);
        Assert.Equal("The contact no longer exists.", c.Error);
        await h.Run(() => c.Open(null));
        Assert.True(!c.Loading && c.Id is null);
    }

    [Fact]
    public async Task EditUnchangedSendsNothing()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        await h.Run(() =>
        {
            c.BeginEdit();
            Assert.True(c.Editing && !c.Dirty);
            c.Save();
        });
        Assert.False(c.Editing);
        Assert.Equal(0, h.Daemon.Count(API.ContactUpdate.Name));
    }

    [Fact]
    public async Task Update()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        h.Daemon.Answer(API.ContactUpdate.Name, new ContactUpdateResult { Revision = "r2" });
        await h.Run(() =>
        {
            c.BeginEdit();
            c.UpdateForm(f => f.Title = "Ředitelka");
            c.Save();
            Assert.True(c.Saving);
        });
        await h.SettleAsync();
        var p = h.Daemon.Last(API.ContactUpdate);
        Assert.True(p.ContactId == "p_1" && p.Revision == "r1" && p.Card.Title == "Ředitelka" && p.Card.DisplayName == "Jana Nováková");
        Assert.True(!c.Editing && !c.Saving);
        Assert.Equal(2, h.Daemon.Count(API.ContactGet.Name));
    }

    [Fact]
    public async Task ValidationKeepsTheForm()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        await h.Run(() =>
        {
            c.BeginEdit();
            c.UpdateForm(f => f.Emails[0] = f.Emails[0] with { Value = "nope" });
            c.Save();
        });
        Assert.True(c.Editing);
        Assert.Single(c.Problems);
        Assert.Equal(0, h.Daemon.Count(API.ContactUpdate.Name));
    }

    [Fact]
    public async Task OfflineKeepsTheForm()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        h.Daemon.Fail(API.ContactUpdate.Name, Err(ErrorCode.Offline));
        await h.Run(() =>
        {
            c.BeginEdit();
            c.UpdateForm(f => f.Org = "Jiná");
            c.Save();
        });
        await h.SettleAsync();
        Assert.True(c.Editing && c.Form?.Org == "Jiná");
        Assert.Equal("Contacts cannot be changed while offline.", c.Error);
        await h.Run(c.CancelEdit);
        Assert.True(!c.Editing && c.Error == "");
    }

    [Fact]
    public async Task ConflictReloadKeepsTheUsersFields()
    {
        using var h = new H();
        var (c, qs) = await Setup(h, Jana());
        var server = Jana() with { Revision = "r9", Org = "Novák a syn", Note = "nová" };
        h.Daemon.Fail(API.ContactUpdate.Name, Err(ErrorCode.ContactConflict, server));
        await h.Run(() =>
        {
            c.BeginEdit();
            c.UpdateForm(f => f.Title = "Ředitelka");
            c.Save();
        });
        await h.SettleAsync();
        Assert.Equal("Reload", Assert.Single(qs.Asked).Confirm);
        var f = c.Form!;
        Assert.True(f.Org == "Novák a syn" && f.Title == "Ředitelka" && f.Note == "nová" && f.Original.Revision == "r9" && c.Error == "");
        h.Daemon.Answer(API.ContactUpdate.Name, new ContactUpdateResult { Revision = "r10" });
        await h.Run(c.Save);
        await h.SettleAsync();
        var p = h.Daemon.Last(API.ContactUpdate);
        Assert.True(p.Revision == "r9" && p.Card.Org == "Novák a syn" && !c.Editing);
    }

    [Fact]
    public async Task ConflictWithoutDataAsksGet()
    {
        using var h = new H();
        var (c, qs) = await Setup(h, Jana());
        h.Daemon.Fail(API.ContactUpdate.Name, Err(ErrorCode.ContactConflict));
        await h.Run(() =>
        {
            c.BeginEdit();
            c.UpdateForm(f => f.Title = "X");
            c.Save();
        });
        await h.SettleAsync();
        Assert.Single(qs.Asked);
        Assert.Equal("X", c.Form?.Title);
        // Cancel keeps the form as it was.
        qs.Answer = false;
        await h.Run(c.Save);
        await h.SettleAsync();
        Assert.True(c.Editing);
        Assert.Equal("The contact changed meanwhile", c.Error);
    }

    [Fact]
    public async Task Create()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        ContactId? created = null;
        c.Created += (_, id) => created = id;
        h.Daemon.Answer(API.ContactCreate.Name, new ContactCreateResult { ContactId = "p_new", Revision = "n1" });
        var a = new ContactBook.AddFromChip(new Address { Name = "Petr Svoboda", Email = "petr@example.com" }, "acc_d", H.Books(), H.Accounts());
        await h.Run(() =>
        {
            c.BeginNew(a.Book, a.Form());
            Assert.True(c.Editing && c.Card is null);
            c.Save();
        });
        await h.SettleAsync();
        var p = h.Daemon.Last(API.ContactCreate);
        Assert.True(p.BookId == "b_d" && p.Card.DisplayName == "Petr Svoboda" && p.Card.Emails[0].Address == "petr@example.com");
        Assert.True(created == "p_new" && c.Id == "p_new" && !c.Editing);
        // A refused create keeps the form with the sentence.
        h.Daemon.Fail(API.ContactCreate.Name, Err(ErrorCode.AddressBookReadOnly));
        await h.Run(() =>
        {
            c.BeginNew("b_m1");
            c.UpdateForm(f => f.Given = "X");
            c.Save();
        });
        await h.SettleAsync();
        Assert.True(c.Editing);
        Assert.Equal("This address book cannot be changed.", c.Error);
        // Consent names the account.
        h.Daemon.Fail(API.ContactCreate.Name, Err(ErrorCode.ContactsConsentRequired));
        await h.Run(c.Save);
        await h.SettleAsync();
        Assert.Equal("Microsoft needs your permission to access the contacts of Microsoft 365 (jana@firma.cz).", c.Error);
    }

    [Fact]
    public async Task Delete()
    {
        using var h = new H();
        var (c, qs) = await Setup(h, Jana());
        ContactId? deleted = null;
        c.Deleted += (_, id) => deleted = id;
        h.Daemon.Answer(API.ContactDelete.Name, new EmptyResult());
        await h.Run(c.Delete);
        await h.SettleAsync();
        var q = Assert.Single(qs.Asked);
        Assert.Equal("Delete contact Jana Nováková?", q.Heading);
        Assert.Contains("Microsoft 365 (jana@firma.cz)", q.Body, StringComparison.Ordinal);
        var p = h.Daemon.Last(API.ContactDelete);
        Assert.True(p.ContactId == "p_1" && p.Revision == "r1" && deleted == "p_1" && c.Card is null);
    }

    [Fact]
    public async Task DeleteGoneIsDeleted()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        ContactId? deleted = null;
        c.Deleted += (_, id) => deleted = id;
        h.Daemon.Fail(API.ContactDelete.Name, Err(ErrorCode.ContactNotFound));
        await h.Run(c.Delete);
        await h.SettleAsync();
        Assert.True(deleted == "p_1" && c.Card is null && c.Error == "");
    }

    [Fact]
    public async Task DeleteCancelledAndConflict()
    {
        using var h = new H();
        var (c, qs) = await Setup(h, Jana());
        qs.Answer = false;
        await h.Run(c.Delete);
        await h.SettleAsync();
        Assert.Equal(0, h.Daemon.Count(API.ContactDelete.Name)); // not after Cancel
        qs.Answer = true;
        h.Daemon.Fail(API.ContactDelete.Name, Err(ErrorCode.ContactConflict, Jana() with { Revision = "r5" }));
        await h.Run(c.Delete);
        await h.SettleAsync();
        Assert.Equal("r5", c.Card?.Revision);
        Assert.NotEqual("", c.Error);
        // Read only: no delete, no edit.
        using var h2 = new H();
        var (c2, _) = await Setup(h2, Jana() with { ReadOnly = true });
        Assert.True(!c2.CanEdit && !c2.CanDelete);
    }

    [Fact]
    public async Task FollowsChanges()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        var n = h.Daemon.Count(API.ContactGet.Name);
        await h.Run(() =>
        {
            c.ContactsChanged(new ContactsChangedNotification { AccountId = "acc_d" });
            c.ContactsChanged(new ContactsChangedNotification { AccountId = "acc_m", BookIds = ["b_m2"] });
        });
        await h.SettleAsync();
        Assert.Equal(n, h.Daemon.Count(API.ContactGet.Name)); // not for another book
        await h.Run(() => c.ContactsChanged(new ContactsChangedNotification { AccountId = "acc_m" }));
        await h.SettleAsync();
        Assert.Equal(n + 1, h.Daemon.Count(API.ContactGet.Name));
        await h.Run(() =>
        {
            c.BeginEdit();
            c.ContactsChanged(new ContactsChangedNotification());
        });
        await h.SettleAsync();
        Assert.Equal(n + 1, h.Daemon.Count(API.ContactGet.Name)); // not under the form
    }

    /// <summary>
    /// contact_test.go TestContactGraphLimitsAndRefusedField: a card of a
    /// Microsoft book is checked against Graph's limits before anything is
    /// sent; a field the daemon refuses is marked, not said (by its message
    /// or its data.field).
    /// </summary>
    [Fact]
    public async Task GraphLimitsAndRefusedField()
    {
        using var h = new H();
        var (c, _) = await Setup(h, Jana());
        await h.Run(() =>
        {
            c.BeginEdit();
            c.UpdateForm(f => f.Urls = [new LV(Value: "a.cz"), new LV(Value: "b.cz")]);
            c.Save();
        });
        await h.SettleAsync();
        Assert.Equal(0, h.Daemon.Count(API.ContactUpdate.Name));
        Assert.Equal(ContactBook.FieldKey.Urls, Assert.Single(c.Problems).Key);
        h.Daemon.Fail(API.ContactUpdate.Name, new RpcError { Code = ErrorCode.InvalidArgument, Message = "card.phones[0].number: empty" });
        await h.Run(() =>
        {
            c.UpdateForm(f => f.Urls = [.. f.Urls.Take(1)]);
            c.Save();
        });
        await h.SettleAsync();
        var p = Assert.Single(c.Problems);
        Assert.True(c.Editing && c.Error == "" && p.Key == ContactBook.FieldKey.Phones && p.Index == 0);
        h.Daemon.Fail(API.ContactUpdate.Name, Err(ErrorCode.InvalidArgument, new ContactFieldData { Field = "emails[0].address" }));
        await h.Run(c.Save);
        await h.SettleAsync();
        p = Assert.Single(c.Problems);
        Assert.True(c.Error == "" && p.Key == ContactBook.FieldKey.Emails && p.Index == 0 && p.Text == ContactBook.RefusedText);
        // An invalidArgument without a field the form has is the sentence.
        h.Daemon.Fail(API.ContactUpdate.Name, Err(ErrorCode.InvalidArgument));
        await h.Run(c.Save);
        await h.SettleAsync();
        Assert.Equal("The contact could not be saved. Check its details.", c.Error);
    }

    // prefs_lookup_test.go

    [Fact]
    public async Task Lookup()
    {
        var d = new FakeContactsDaemon();
        d.On(API.ContactLookup.Name, raw =>
        {
            var p = raw.Deserialize(ApiJsonContext.Wire.ContactLookupParams)!;
            return new ContactLookupResult { Known = p.Addresses.Where(a => a == "jana@firma.cz").ToDictionary(a => a, _ => new ContactId("p_1")) };
        });
        using var h = new H(d);
        var lk = new ContactsLookupController(h.Options);
        var changed = new List<string>();
        lk.Changed += (_, k) => changed.Add(k);
        await h.Run(() =>
        {
            lk.Ask("m1", [" Jana@Firma.cz ", "petr@example.net", "JANA@firma.cz", ""]);
            lk.Ask("m1", ["x@y.cz"]);
            Assert.Equal(ContactBook.ChipItem.None, lk.Item("m1", "jana@firma.cz", H.Books()).Item); // no item before the answer
        });
        await h.SettleAsync();
        Assert.Equal(["jana@firma.cz", "petr@example.net"], d.Last(API.ContactLookup).Addresses);
        Assert.Equal(1, d.Count(API.ContactLookup.Name));
        var show = lk.Item("m1", "JANA@firma.cz", H.Books());
        Assert.True(show.Item == ContactBook.ChipItem.Show && show.Id == "p_1");
        Assert.Equal(ContactBook.ChipItem.Add, lk.Item("m1", "petr@example.net", H.Books()).Item);
        Assert.Equal(ContactBook.ChipItem.None, lk.Item("m1", "petr@example.net", [H.Books()[3]]).Item); // no writable book
        Assert.Equal(["m1"], changed);
        // Invalidate drops answers, also those on their way.
        await h.Run(() =>
        {
            lk.Ask("m2", ["a@b.cz"]);
            lk.Invalidate();
        });
        await h.SettleAsync();
        Assert.False(lk.Known("m2", "a@b.cz").Answered); // no stale answer
        Assert.False(lk.Known("m1", "jana@firma.cz").Answered); // forgotten
        // Many addresses go in chunks; an empty message is answered at once.
        var many = Enumerable.Range(0, 450).Select(i => $"a{i}@x.cz").ToArray();
        var n = d.Count(API.ContactLookup.Name);
        await h.Run(() => lk.Ask("big", many));
        await h.SettleAsync();
        Assert.Equal(n + 3, d.Count(API.ContactLookup.Name));
        Assert.True(lk.Known("big", "a1@x.cz").Answered);
        await h.Run(() => lk.Ask("empty", []));
        Assert.True(lk.Known("empty", "x").Answered);
        // Only Kept messages stay.
        await h.Run(() =>
        {
            for (var i = 0; i < ContactsLookupController.Kept + 5; i++)
            {
                lk.Ask($"k{i}", []);
            }
        });
        Assert.False(lk.Known("big", "a1@x.cz").Answered); // old answers go
        // A failed lookup stores nothing; it is asked again.
        d.Fail(API.ContactLookup.Name, Err(ErrorCode.StorageError));
        await h.Run(() => lk.Ask("f", ["a@b.cz"]));
        await h.SettleAsync();
        Assert.False(lk.Known("f", "a@b.cz").Answered);
        await h.Run(() => lk.Ask("f", ["a@b.cz"]));
        await h.SettleAsync();
        Assert.Equal(n + 5, d.Count(API.ContactLookup.Name));
    }

    private static async Task<ContactsPreferencesController> StartPrefs(H h)
    {
        var p = await h.Run(() =>
        {
            var p = new ContactsPreferencesController(h.Options);
            p.Start();
            return p;
        });
        await h.SettleAsync();
        return p;
    }

    [Fact]
    public async Task Sections()
    {
        using var h = new H();
        var p = await StartPrefs(h);
        var secs = p.Sections;
        Assert.Equal(
            [
                "jana@firma.cz/M365 sync=True consent=False carddav=False [Contacts:True Archive:False]",
                "petr@firma2.cz/M365 sync=True consent=True carddav=False []",
                "Nextcloud/CARDDAV sync=True consent=False carddav=True [Personal:True]",
            ],
            secs.Select(s => $"{s.Title}/{s.Badge} sync={s.SyncOn} consent={s.Consent} carddav={s.CardDAV} [{string.Join(' ', s.Books.Select(b => $"{b.Name}:{b.Visible}"))}]"));
        Assert.True(secs[1].SignIn && !p.Empty);
        AccountId? signed = null;
        p.SignInRequested += (_, id) => signed = id;
        p.Allow("acc_p");
        Assert.Equal("acc_p", signed?.Value);
    }

    [Fact]
    public async Task SyncSwitch()
    {
        using var h = new H();
        var p = await StartPrefs(h);
        h.Daemon.Answer(API.AccountUpdate.Name, new EmptyResult());
        await h.Run(() =>
        {
            p.SetSync("acc_m", false);
            Assert.True(!p.Sections[0].SyncOn && p.IsSaving("acc_m")); // shown at once
            p.SetSync("acc_m", true); // while saving: nothing
        });
        await h.SettleAsync();
        var u = h.Daemon.Last(API.AccountUpdate);
        Assert.True(u.AccountId == "acc_m" && u.Config.Graph?.Contacts?.Sync == false && u.Credentials == new Credentials());
        Assert.Equal(1, h.Daemon.Count(API.AccountUpdate.Name));
        // CardDAV and a refusal undone.
        h.Daemon.Fail(API.AccountUpdate.Name, Err(ErrorCode.AccountNotFound));
        await h.Run(() => p.SetSync("acc_d", false));
        await h.SettleAsync();
        u = h.Daemon.Last(API.AccountUpdate);
        Assert.True(u.Config.CalDAV?.Contacts is null && u.Config.CalDAV?.ServerUrl == "https://cloud.example.cz");
        Assert.True(p.Sections[2].SyncOn && p.Error != ""); // the refusal undone
        await h.Run(() => p.SetSync("acc_i", true)); // no contacts capability
        Assert.Equal(2, h.Daemon.Count(API.AccountUpdate.Name));
    }

    [Fact]
    public async Task BookCheckbox()
    {
        using var h = new H();
        var p = await StartPrefs(h);
        var archive = H.Books()[1];
        h.Daemon.On(API.AddressBookSet.Name, raw =>
        {
            var s = raw.Deserialize(ApiJsonContext.Wire.AddressBookSetParams)!;
            return new AddressBookSetResult { Book = archive with { Visible = s.Visible, Count = 10 } };
        });
        await h.Run(() =>
        {
            p.SetVisible("b_m2", true);
            Assert.True(p.Sections[0].Books[1].Visible); // at once
        });
        await h.SettleAsync();
        Assert.Equal(10, p.Sections[0].Books[1].Count);
        h.Daemon.Fail(API.AddressBookSet.Name, Err(ErrorCode.AddressBookNotFound));
        await h.Run(() => p.SetVisible("b_m1", false));
        await h.SettleAsync();
        Assert.True(p.Sections[0].Books[0].Visible);
        Assert.Equal("The address book no longer exists.", p.Error);
        await h.Run(() => p.SetVisible("b_none", true));
    }

    [Fact]
    public async Task Scope()
    {
        using var h = new H();
        var p = await StartPrefs(h);
        Assert.Equal(ContactSearchScope.Account, p.SuggestScope.Value);
        var stored = new ContactSearchScope("");
        p.Scope = () => stored;
        p.SetScope = s => stored = s;
        p.SetSuggestScope("bogus");
        p.SetSuggestScope(ContactSearchScope.All);
        Assert.Equal(ContactSearchScope.All, stored.Value);
        Assert.Equal(ContactSearchScope.All, p.SuggestScope.Value);
        stored = "weird";
        Assert.Equal(ContactSearchScope.Account, p.SuggestScope.Value); // unknown stored value
    }

    [Fact]
    public async Task EmptyAndReload()
    {
        var d = new FakeContactsDaemon();
        d.Answer(API.AccountList.Name, new AccountListResult { Accounts = [H.Accounts()[2]] });
        d.Answer(API.AddressBookList.Name, new AddressBookListResult { Books = [H.Books()[3]] });
        using var h = new H(d);
        var p = await StartPrefs(h);
        Assert.True(p.Empty && p.Loaded);
        var n = d.Count(API.AccountList.Name);
        await h.Run(() =>
        {
            p.Handle(API.Notify.AccountsChanged);
            p.Handle(API.Notify.NewMessage);
        });
        await h.SettleAsync();
        Assert.Equal(n + 1, d.Count(API.AccountList.Name));
    }

    [Fact]
    public void Helpers()
    {
        var typed = new RpcException(Err(ErrorCode.ContactConflict, new ContactCard { Id = "p" }));
        Assert.Equal("p", ContactsOptions.ConflictCard(typed)?.Id.Value);
        Assert.Null(ContactsOptions.ConflictCard(new RpcException(Err(ErrorCode.Offline))));
        Assert.Null(ContactsOptions.ConsentData(new RpcException(Err(ErrorCode.ContactsConsentRequired))));
        Assert.Equal(ErrorCode.InternalError, ContactsOptions.AsRpcError(new InvalidOperationException()).Code.Value);
        Assert.Equal(ErrorCode.Unavailable, ContactsOptions.AsRpcError(new RpcClientException(ClientError.Disconnected)).Code.Value);
        Assert.Equal(ErrorCode.Cancelled, ContactsOptions.AsRpcError(new OperationCanceledException()).Code.Value);
        using var h = new H(new FakeContactsDaemon());
        Assert.Equal("Contacts", h.Options.ErrorMessage(new RpcException(Err(ErrorCode.NetworkError))));
        Assert.Equal("", h.Options.ErrorMessage(null));
        h.Options.RpcText = (what, _) => what + "!";
        Assert.Equal("Contacts!", h.Options.ErrorMessage(new RpcException(Err(ErrorCode.NetworkError))));
    }

    // samples.go

    [Fact]
    public async Task SamplesServeTheWindow()
    {
        var samples = new ContactsSampleCaller(H.Stamp("2026-10-30T12:00:00Z"));
        var notified = new List<ContactsChangedNotification>();
        samples.Notified += (_, n) => notified.Add(n);
        using var h = new H(new FakeContactsDaemon());
        var o = new ContactsOptions(samples, ContactBook.EnglishStrftime);
        var w = await h.Run(() =>
        {
            var w = new ContactsListController(o);
            w.Start();
            return w;
        });
        await Fixtures.Quiescence.IdleAsync(h.Ui, o.Pending);
        Assert.Equal("All Contacts", w.Items[0].Title);
        Assert.DoesNotContain(w.Items, i => i.Title == "Archiv"); // hidden
        Assert.Equal(w.Total, w.Contacts.Count);
        Assert.Equal(13, w.Total); // the visible books of accounts with contacts on
        Assert.Equal([new ContactsConsentNeed("acc_petr", "petr@firma2.cz", true)], w.Consent);
        var detail = await h.Run(() =>
        {
            var c = new ContactsDetailController(o, w.Book) { AskQuestion = _ => Task.FromResult(true) };
            c.Open(w.Contacts.First(x => x.DisplayName == "Jana Nováková").Id);
            return c;
        });
        await Fixtures.Quiescence.IdleAsync(h.Ui, o.Pending);
        Assert.Equal("14 March", detail.Detail!.Rows.Single(r => r.Kind == ContactBook.RowKind.Birthday).Value);
        await h.Run(() =>
        {
            detail.BeginEdit();
            detail.UpdateForm(f => f.Title = "ředitelka");
            detail.Save();
        });
        await Fixtures.Quiescence.IdleAsync(h.Ui, o.Pending);
        Assert.Equal("r2", detail.Card?.Revision);
        Assert.Contains(notified, n => n.BookIds.Contains("ab_work"));
        await h.Run(detail.Delete);
        await Fixtures.Quiescence.IdleAsync(h.Ui, o.Pending);
        Assert.Null(detail.Card);
        await Assert.ThrowsAsync<RpcException>(() => samples.CallAsync(API.AccountOAuthStart, new AccountOAuthStartParams(), TimeSpan.FromSeconds(1), TestContext.Current.CancellationToken));
    }
}
