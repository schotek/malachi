// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/contactsctl samples.go (SamplesEnv, SamplesOn,
// SampleCaller, NewSampleCaller, add, summary, answer, listed, count,
// list); Go-first, no Swift counterpart. The sample photos (contact.photo,
// contact.lookup withPhotos, docs/plans/address-book.md §9) are Windows
// first: three invented 48 px portraits drawn as PNG, two contacts with a
// ready photo, one with a remote photo the first contact.photo "downloads"
// and one remote contact that turns out to have none.
//
// DEVELOPMENT AID: the invented address books of MALACHI_CONTACTS_SAMPLES=1
// (like MALACHI_CALENDAR_SAMPLES): an in-memory daemon that answers the
// contacts methods, account.list, account.update and account.oauthStart, so
// that the Contacts window, the address chips and Preferences → Contacts
// can be seen and clicked through without a daemon, and the UI smoke tests
// have something to show. Writes change the memory only and raise
// Notified as the daemon's notify.contactsChanged. Every name, address and
// number is invented (W1–W6). Other methods answer methodNotFound.
//
// Windows: params and results go through the contract's JSON (the
// method's own metadata), as they would over the socket, so that a sample
// answer cannot carry what the wire would drop.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.SampleCaller: the in-memory daemon of the samples; safe for calls from any thread.</summary>
public sealed class ContactsSampleCaller : IContactsCaller
{
    /// <summary>contactsctl.SamplesEnv: the variable that turns the samples on ("1").</summary>
    public const string SamplesEnv = "MALACHI_CONTACTS_SAMPLES";

    private readonly Lock gate = new();
    private readonly List<Account> accounts;
    private readonly List<AddressBook> books;
    private readonly Dictionary<ContactId, ContactCard> cards = [];
    private readonly List<ContactSummary> recent = [];
    private readonly Dictionary<ContactId, (ContactPhotoAvailability State, byte[]? Data)> photos = [];
    private int next;

    /// <summary>contactsctl.NewSampleCaller: the sample daemon as of <paramref name="now"/>.</summary>
    public ContactsSampleCaller(DateTimeOffset now)
    {
        var synced = now.AddMinutes(-3);
        IReadOnlyList<Capability> contactsCaps = [.. API.MailCapabilities, Capability.Calendar, Capability.Contacts];
        static Account Account(string id, string name, string email, AccountKind kind, IReadOnlyList<Capability> caps, GraphConfig? graph = null, CalDAVConfig? dav = null) => new()
        {
            Id = id,
            Enabled = true,
            Capabilities = caps,
            Config = new AccountConfig { Name = name, Email = email, Kind = kind, Graph = graph, CalDAV = dav },
            State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
        };
        var on = new GraphConfig { Source = GraphSource.Daemon, Contacts = new ContactsConfig { Sync = true } };
        accounts =
        [
            Account("acc_work", "Práce", "jana@firma.cz", AccountKind.Graph, contactsCaps, graph: on),
            Account("acc_petr", "Petr", "petr@firma2.cz", AccountKind.Graph, contactsCaps, graph: on),
            Account("acc_cloud", "Osobní", "", AccountKind.CalDAV, [Capability.Calendar, Capability.Contacts],
                dav: new CalDAVConfig { ServerUrl = "https://cloud.example.cz", Username = "jana", Contacts = true }),
        ];
        books =
        [
            new() { Id = "ab_work", AccountId = "acc_work", Name = "Kontakty", Kind = AddressBookKind.Graph, Visible = true, LastSyncAt = synced },
            new() { Id = "ab_work_old", AccountId = "acc_work", Name = "Archiv", Kind = AddressBookKind.Graph, Visible = false, LastSyncAt = synced },
            new() { Id = "ab_family", AccountId = "acc_cloud", Name = "Rodina", Kind = AddressBookKind.CardDAV, Visible = true, LastSyncAt = synced },
            new() { Id = "ab_shared", AccountId = "acc_cloud", Name = "Sdílené", Kind = AddressBookKind.CardDAV, ReadOnly = true, Visible = true, LastSyncAt = synced },
            new() { Id = AddressBookId.Recent, Name = "Recently Used", Kind = AddressBookKind.Recent, ReadOnly = true, Visible = true },
        ];
        static ContactCard Work(string given, string family, string org, string title, string email, string phone) => new()
        {
            BookId = "ab_work",
            Name = new ContactName { Given = given, Family = family },
            DisplayName = given + " " + family,
            Org = org,
            Title = title,
            Emails = [new ContactEmail { Label = ContactLabel.Work, Address = email }],
            Phones = phone.Length > 0 ? [new ContactPhone { Label = ContactLabel.Mobile, Number = phone }] : [],
        };
        var jana = Work("Jana", "Nováková", "Stavby Nová s.r.o.", "projektová manažerka", "jana.novakova@example.cz", "+420 601 234 567") with
        {
            Addresses = [new ContactAddress { Label = ContactLabel.Work, Street = "Dlouhá 12", Postcode = "110 00", City = "Praha 1", Country = "Česko" }],
            Birthday = new ContactBirthday { Month = 3, Day = 14 },
            Urls = [new ContactUrl { Label = ContactLabel.Work, Url = "https://stavby-nova.example.cz" }],
            Note = "Preferuje schůzky dopoledne.\nParkování ve dvoře.",
        };
        jana = jana with { Emails = [.. jana.Emails, new ContactEmail { Label = ContactLabel.Home, Address = "jana@example.org" }] };
        foreach (var c in new[]
        {
            jana,
            Work("Pavel", "Novák", "", "", "pavel.novak@example.cz", "+420 602 111 222"),
            Work("Marie", "Svobodová", "Svobodová účetnictví", "účetní", "marie@example.cz", ""),
            Work("Karel", "Kučera", "Pila Kučera", "", "karel@kucera.example", "+420 603 333 444"),
            Work("Lucie", "Procházková", "", "", "lucie.prochazkova@example.cz", ""),
            Work("Hana", "Růžičková", "Stavby Nová s.r.o.", "asistentka", "hana@stavby-nova.example.cz", ""),
            Work("Tomáš", "Svoboda", "Pila Svoboda s.r.o.", "", "tomas@svoboda.example", ""),
            Work("Eva", "Veselá", "", "", "eva.vesela@example.cz", ""),
            new ContactCard
            {
                BookId = "ab_work", Org = "Tiskárna Dvořák", DisplayName = "Tiskárna Dvořák",
                Emails = [new ContactEmail { Label = ContactLabel.Work, Address = "info@tiskarna.example" }],
            },
            new ContactCard
            {
                BookId = "ab_family", Name = new ContactName { Given = "Babička" }, DisplayName = "Babička",
                Phones = [new ContactPhone { Label = ContactLabel.Home, Number = "+420 241 000 111" }],
            },
            new ContactCard
            {
                BookId = "ab_family", Name = new ContactName { Given = "David", Family = "Dvořák" }, DisplayName = "David Dvořák",
                Emails = [new ContactEmail { Address = "david@dvorak.example" }], Birthday = new ContactBirthday { Year = 1980, Month = 6, Day = 2 },
            },
            new ContactCard
            {
                BookId = "ab_family", Name = new ContactName { Given = "Jiří", Family = "Pokorný" }, DisplayName = "Jiří Pokorný",
                Emails = [new ContactEmail { Label = ContactLabel.Home, Address = "jiri.pokorny@example.org" }],
            },
            new ContactCard
            {
                BookId = "ab_shared", Name = new ContactName { Given = "Petra", Family = "Malá" }, DisplayName = "Petra Malá", Org = "Školka Sluníčko",
                Emails = [new ContactEmail { Label = ContactLabel.Work, Address = "petra@skolka.example" }],
            },
        })
        {
            Add(c);
        }
        // The photos: ready (stored), remote with a photo, remote without.
        foreach (var (address, state, data) in new (string, string, string?)[]
        {
            ("jana.novakova@example.cz", ContactPhotoAvailability.Ready, PhotoA),
            ("david@dvorak.example", ContactPhotoAvailability.Ready, PhotoB),
            ("marie@example.cz", ContactPhotoAvailability.Remote, PhotoC),
            ("karel@kucera.example", ContactPhotoAvailability.Remote, null),
        })
        {
            if (cards.Values.FirstOrDefault(c => c.Emails.Any(e => e.Address == address)) is { } c)
            {
                photos[c.Id] = (new ContactPhotoAvailability(state), data is null ? null : Convert.FromBase64String(data));
            }
        }
        foreach (var a in new[] { "jan.horak@example.com", "objednavky@eshop.example", "petr.svoboda@example.com" })
        {
            recent.Add(new ContactSummary
            {
                Id = "recent:" + a,
                BookId = AddressBookId.Recent,
                DisplayName = a,
                SortKey = a,
                Section = a[..1].ToUpperInvariant(),
                Email = a,
            });
        }
    }

    // Invented 48 px portraits (a head and shoulders on a gradient), PNG.
    private const string PhotoA =
        "iVBORw0KGgoAAAANSUhEUgAAADAAAAAwCAIAAADYYG7QAAABp0lEQVR42s3O6SqEYRjG8euAHINj8B0RESIiRESIyJgZxgyz2LNm" +
        "y5YtjJzC7PvqqzMw9XyZetf77p3pqd/n6/pj9+dXKgh8l6UCf7AsFXi/SlLB9mdJKvB8lKQC93tRKnC9FaWCjdeCVOB8KUgFjueC" +
        "Jf6CPkt2YH/Ks1UitLA3YXvM8+jUCLxZrD7kGQxrBMYyVu5zVCZrBOo4lu9yJKQagbSPpdssCSOItI/FmywJI4i0j4XrLAkjiLSP" +
        "+auMeYwawfwF5i4zJIwa0j5mL9IkjCDSPmbO0ySMINI+ps/SJIwg0j6mTlNUpBrqOCZPUgwmaxjLmDhO8RjW8GYxfpRk06lhb2Ls" +
        "MGmJSoQlOxg9SEgFI/sJqWB4LyEVDO3EpYLBQFwqGPDH2Boam7SwN9Hvi1HpdChRx9HnjZlHSqlm/gK9W1Ez2CnVzByhxxM1ZEmN" +
        "YPiFbndUn4U1gv4dujYjOiyvEXQe0emKaKlRjaB1io71sJaaBmmdot0ZVlXTGkH1F22OsKo6BKn+otUeUlWHINVftKyFlOpQIyiv" +
        "0WwLKdUtSHn9Dwgfxxt7YvttAAAAAElFTkSuQmCC";

    private const string PhotoB =
        "iVBORw0KGgoAAAANSUhEUgAAADAAAAAwCAIAAADYYG7QAAABo0lEQVR42s3OV05CURSF4TUPR+YAEAEREUVERER9cgY+2WvsNfbe" +
        "a5zL7SZqOC/ktrN3Ljcn+Z7X+qF9J5QC7SuhFGifHUqB9pFUCrT3pFKgvXUqBdprSinQX1JKgf6cVgr0p4xSoD9mInE/2R7JDvSH" +
        "Lra/CD/sTeh3WZ6AGtHEmoV+m2UIrRFN9GUYN91UkjUN1HEY1zkSUo1oouzDuMqRcIIo+zAue0g4QZR9GBd5Ek4QZR/GeV4eo0Y0" +
        "SV/AOOsl4dRQ9mGeFkgYQaR9mCcFEk4QZR/mcR8JJ4iyD/Oon4pWQxyHeVhkkK2hL8M8KPKE17BmYe0PsAXUsDdh7ZUi8R8RxQ6s" +
        "3ZJSYO0MKgXWdlkpsLbKSoG1OaQU2BsVtnTblB/2Juz1ClVAh0cZcRz22rA8UkpTlvQF7NWqDHZKU5bEEeyVaqhIakRT2Bfs5ZFg" +
        "EdaIpsA72Eu1AJHXiCb/RziLNT8tqmnwO4WzMOqntUE+p3Dm655aWiOavH7hzNU9xRHk9QtndsxTHEFev3Bmxt1iqBFNrmv8TE+4" +
        "xRbkvv4FFRNhkh3MnggAAAAASUVORK5CYII=";

    private const string PhotoC =
        "iVBORw0KGgoAAAANSUhEUgAAADAAAAAwCAIAAADYYG7QAAABsElEQVR42s3OaSuEYRjF8fN95jNIUpKUpCQpSQopSUpSkpRkyDKy" +
        "7/uWnWEMZsyMse9k3/I5qPuNerb7unrm6a7f63P+mPo5VwrGv0+VgtGvE6Vg+PNIKRj4iCoFfe8RpaDnLawUdL2GlALPS1ApaH8O" +
        "KAWtT3u2eJ8rsmUHzY+7bH8RRtibaHrw85jUCLxZNN77GCxrBMYyGu62qSRrBOo46m+9JKQagbSPuptNEkYQaR+11xskjCDSPmqu" +
        "1kgYQaR9VF+uymPUCPIXqLpYJmHUkPZReb5Ewggi7aPibJGEEUTaR/npAgkjiLSPsuN5KlINdRylR7MMkjWMZZREZ3gsa3izKD6c" +
        "ZjOpYW+iMDJpi78IW3ZQEJ5QCvJDY0pB3sGoUpAbHFEKcgJDSkH2/iCb2+Uywt5E1l4/lUmHFnUcmf4+eaSU/+QvkLHTK4Od8p/M" +
        "EdJ93ZZsqREsv5C23WXOxhrB/A6pW50mbK8RTB6R4vUYiVGNYHSK5M0OIzENMjpF0kabrpjWCLq/SFxv1eVAkO4vEtZadDkQpPuL" +
        "+NVmLQdqBO014lbcWo4Faa9/AVfFNXVFgnXsAAAAAElFTkSuQmCC";

    /// <summary>
    /// A write changed the memory: the application hands it on as
    /// notify.contactsChanged (raised on the calling thread, after the call's
    /// answer was made).
    /// </summary>
    public event EventHandler<ContactsChangedNotification>? Notified;

    /// <summary>contactsctl.SamplesOn: MALACHI_CONTACTS_SAMPLES=1.</summary>
    public static bool SamplesOn => Environment.GetEnvironmentVariable(SamplesEnv) == "1";

    /// <inheritdoc/>
    public async Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(method);
        await Task.Yield();
        cancellationToken.ThrowIfCancellationRequested();
        var raw = JsonSerializer.SerializeToElement(parameters, method.ParamsInfo);
        object result;
        ContactsChangedNotification? notify;
        lock (gate)
        {
            (result, notify) = Answer(method.Name, raw);
        }
        var bytes = JsonSerializer.SerializeToUtf8Bytes(result, ApiJsonContext.Wire.GetTypeInfo(result.GetType())!);
        var typed = JsonSerializer.Deserialize(bytes, method.ResultInfo)!;
        if (notify is not null)
        {
            Notified?.Invoke(this, notify);
        }
        return typed;
    }

    private static RpcException Fail(int code, string message, JsonElement? data = null) =>
        new(new RpcError { Code = code, Message = message, Data = data });

    private static T Read<T>(JsonElement raw) =>
        (T)raw.Deserialize(ApiJsonContext.Wire.GetTypeInfo(typeof(T))!)!;

    private (object Result, ContactsChangedNotification? Notify) Answer(string method, JsonElement raw)
    {
        switch (method)
        {
            case "account.list":
                return (new AccountListResult { Accounts = [.. accounts] }, null);
            case "account.update":
                {
                    var p = Read<AccountUpdateParams>(raw);
                    var i = accounts.FindIndex(a => a.Id == p.AccountId);
                    if (i < 0)
                    {
                        throw Fail(ErrorCode.AccountNotFound, "no account");
                    }
                    accounts[i] = accounts[i] with { Config = p.Config };
                    return (new EmptyResult(), new ContactsChangedNotification { AccountId = p.AccountId });
                }
            case "account.oauthStart":
                throw Fail(ErrorCode.Unavailable, "samples cannot sign in");
            case "addressBook.list":
                {
                    var p = Read<AddressBookListParams>(raw);
                    if (p.AccountId?.Value == "acc_petr")
                    {
                        throw Fail(ErrorCode.ContactsConsentRequired, "consent", JsonSerializer.SerializeToElement(
                            new ContactsConsentRequiredData { AccountId = "acc_petr", SignIn = true }, ApiJsonContext.Wire.ContactsConsentRequiredData));
                    }
                    var output = new List<AddressBook>();
                    foreach (var b in books)
                    {
                        if (p.AccountId is { } only && b.AccountId != only)
                        {
                            continue;
                        }
                        if (b.AccountId is { } acc && (AccountOf(acc) is not { } a || !ContactBook.SyncOn(a.Config)))
                        {
                            continue;
                        }
                        output.Add(b with { Count = Count(b.Id) });
                    }
                    return (new AddressBookListResult { Books = output }, null);
                }
            case "addressBook.set":
                {
                    var p = Read<AddressBookSetParams>(raw);
                    var i = books.FindIndex(b => b.Id == p.BookId);
                    if (i < 0)
                    {
                        throw Fail(ErrorCode.AddressBookNotFound, "no book");
                    }
                    books[i] = books[i] with { Visible = p.Visible };
                    var b = books[i] with { Count = Count(books[i].Id) };
                    return (new AddressBookSetResult { Book = b }, new ContactsChangedNotification { AccountId = b.AccountId });
                }
            case "contact.list":
                return (List(Read<ContactListParams>(raw)), null);
            case "contact.get":
                {
                    var p = Read<ContactGetParams>(raw);
                    if (cards.TryGetValue(p.ContactId, out var c))
                    {
                        return (new ContactGetResult { Contact = c with { Photo = PhotoOf(c.Id) } }, null);
                    }
                    if (recent.FirstOrDefault(r => r.Id == p.ContactId) is { } r)
                    {
                        return (new ContactGetResult
                        {
                            Contact = new ContactCard
                            {
                                Id = r.Id,
                                BookId = AddressBookId.Recent,
                                ReadOnly = true,
                                DisplayName = r.DisplayName,
                                Emails = [new ContactEmail { Address = r.Email ?? "" }],
                            },
                        }, null);
                    }
                    throw NotFound(p.ContactId);
                }
            case "contact.lookup":
                {
                    var p = Read<ContactLookupParams>(raw);
                    var known = new Dictionary<string, ContactId>(StringComparer.Ordinal);
                    foreach (var a in p.Addresses)
                    {
                        foreach (var c in cards.Values.Where(c => Book(c.BookId).Visible))
                        {
                            if (c.Emails.Any(e => string.Equals(e.Address, a, StringComparison.OrdinalIgnoreCase)))
                            {
                                known[a] = c.Id;
                            }
                        }
                    }
                    // withPhotos: the first contact by sort key with a photo of each address.
                    var withPhotos = new Dictionary<string, ContactPhotoRef>(StringComparer.Ordinal);
                    if (p.WithPhotos == true)
                    {
                        foreach (var a in p.Addresses)
                        {
                            var first = cards.Values
                                .Where(c => Book(c.BookId).Visible && PhotoOf(c.Id) is not null
                                    && c.Emails.Any(e => string.Equals(e.Address, a, StringComparison.OrdinalIgnoreCase)))
                                .OrderBy(c => Summary(c).SortKey, StringComparer.Ordinal)
                                .FirstOrDefault();
                            if (first is not null && PhotoOf(first.Id) is { } state)
                            {
                                withPhotos[a] = new ContactPhotoRef { ContactId = first.Id, State = state };
                            }
                        }
                    }
                    return (new ContactLookupResult { Known = known, Photos = withPhotos }, null);
                }
            case "contact.photo":
                {
                    var p = Read<ContactPhotoParams>(raw);
                    if (!cards.TryGetValue(p.ContactId, out var c))
                    {
                        throw NotFound(p.ContactId);
                    }
                    if (!photos.TryGetValue(c.Id, out var ph))
                    {
                        return (new ContactPhotoResult { State = ContactPhotoState.None, Revision = c.Revision }, null);
                    }
                    ContactsChangedNotification? fetched = null;
                    if (ph.State.Value == ContactPhotoAvailability.Remote)
                    {
                        // "Downloaded": ready from now on, or gone for good, as the daemon does.
                        if (ph.Data is null)
                        {
                            photos.Remove(c.Id);
                        }
                        else
                        {
                            photos[c.Id] = (ContactPhotoAvailability.Ready, ph.Data);
                        }
                        fetched = new ContactsChangedNotification { AccountId = Book(c.BookId).AccountId, BookIds = [c.BookId] };
                    }
                    return ph.Data is null
                        ? (new ContactPhotoResult { State = ContactPhotoState.None, Revision = c.Revision }, fetched)
                        : (new ContactPhotoResult { State = ContactPhotoState.Ready, Mime = "image/png", Data = ph.Data, Revision = c.Revision }, fetched);
                }
            case "contact.create":
                {
                    var p = Read<ContactCreateParams>(raw);
                    var b = Book(p.BookId);
                    if (b.Id.Value is null or "")
                    {
                        throw Fail(ErrorCode.AddressBookNotFound, "no book");
                    }
                    if (b.ReadOnly)
                    {
                        throw Fail(ErrorCode.AddressBookReadOnly, "read only");
                    }
                    var id = Add(p.Card with { BookId = p.BookId });
                    return (new ContactCreateResult { ContactId = id, Revision = "r1" },
                        new ContactsChangedNotification { AccountId = b.AccountId, BookIds = [b.Id] });
                }
            case "contact.update":
                {
                    var p = Read<ContactUpdateParams>(raw);
                    if (!cards.TryGetValue(p.ContactId, out var c))
                    {
                        throw NotFound(p.ContactId);
                    }
                    if (c.ReadOnly)
                    {
                        throw Fail(ErrorCode.AddressBookReadOnly, "read only");
                    }
                    if (c.Revision != p.Revision)
                    {
                        throw Fail(ErrorCode.ContactConflict, "conflict", JsonSerializer.SerializeToElement(
                            new ContactConflictData { Server = c }, ApiJsonContext.Wire.ContactConflictData));
                    }
                    _ = int.TryParse(c.Revision.TrimStart('r'), NumberStyles.None, CultureInfo.InvariantCulture, out var n);
                    var card = p.Card with { Id = c.Id, BookId = c.BookId, ReadOnly = c.ReadOnly, Revision = "r" + (n + 1).ToString(CultureInfo.InvariantCulture) };
                    cards[c.Id] = card;
                    return (new ContactUpdateResult { Revision = card.Revision },
                        new ContactsChangedNotification { AccountId = Book(c.BookId).AccountId, BookIds = [c.BookId] });
                }
            case "contact.delete":
                {
                    var p = Read<ContactDeleteParams>(raw);
                    if (!cards.TryGetValue(p.ContactId, out var c))
                    {
                        throw NotFound(p.ContactId);
                    }
                    if (c.ReadOnly)
                    {
                        throw Fail(ErrorCode.AddressBookReadOnly, "read only");
                    }
                    cards.Remove(p.ContactId);
                    photos.Remove(p.ContactId);
                    return (new EmptyResult(), new ContactsChangedNotification { AccountId = Book(c.BookId).AccountId, BookIds = [c.BookId] });
                }
        }
        throw Fail(ErrorCode.MethodNotFound, method);
    }

    private static RpcException NotFound(ContactId id) => Fail(ErrorCode.ContactNotFound, "no contact " + id);

    // Stores a card with a new id and revision.
    private ContactId Add(ContactCard c)
    {
        next++;
        var id = new ContactId("c_" + next.ToString(CultureInfo.InvariantCulture));
        cards[id] = c with { Id = id, Revision = "r1", ReadOnly = Book(c.BookId).ReadOnly };
        return id;
    }

    private AddressBook Book(AddressBookId id) =>
        books.FirstOrDefault(b => b.Id == id) ?? new AddressBook { Id = "", Kind = "" };

    private Account? AccountOf(AccountId id) => accounts.FirstOrDefault(a => a.Id == id);

    // A book contact.list "all" includes: visible, of an account whose contacts are on.
    private bool Listed(AddressBookId id)
    {
        var b = Book(id);
        return b.Visible && b.AccountId is { } acc && AccountOf(acc) is { } a && ContactBook.SyncOn(a.Config);
    }

    private int Count(AddressBookId id) => id == AddressBookId.Recent ? recent.Count : cards.Values.Count(c => c.BookId == id);

    // The photo state of a contact; null none.
    private ContactPhotoAvailability? PhotoOf(ContactId id) => photos.TryGetValue(id, out var ph) ? ph.State : null;

    private ContactSummary Summary(ContactCard c)
    {
        var key = (c.Name.Family + " " + c.Name.Given).ToLowerInvariant().Trim();
        if (key.Length == 0)
        {
            key = c.DisplayName.ToLowerInvariant().Trim();
        }
        var sec = "";
        foreach (var r in key.EnumerateRunes())
        {
            sec = r.ToString().ToUpperInvariant();
            break;
        }
        return new ContactSummary
        {
            Id = c.Id,
            BookId = c.BookId,
            DisplayName = c.DisplayName,
            SortKey = key,
            Section = sec,
            Org = c.Org,
            Email = c.Emails.Count > 0 ? c.Emails[0].Address : null,
            Photo = PhotoOf(c.Id),
        };
    }

    private ContactListResult List(ContactListParams p)
    {
        var src = p.BookId == AddressBookId.Recent
            ? [.. recent]
            : cards.Values.Where(c => (p.BookId == AddressBookId.All && Listed(c.BookId)) || c.BookId == p.BookId).Select(Summary).ToList();
        src.Sort((a, b) => string.CompareOrdinal(a.SortKey, b.SortKey));
        var q = (p.Query ?? "").Trim().ToLowerInvariant();
        var match = src.Where(c => q.Length == 0 || (c.DisplayName + " " + c.Org + " " + c.Email).ToLowerInvariant().Contains(q, StringComparison.Ordinal)).ToList();
        var limit = p.Limit is > 0 and var l ? l : API.Limits.DefaultContactListLimit;
        var start = Math.Clamp(p.Offset ?? 0, 0, match.Count);
        var end = Math.Min(start + limit, match.Count);
        return new ContactListResult { Contacts = match[start..end], Total = match.Count };
    }
}
