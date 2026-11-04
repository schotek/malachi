// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ContactsControllerTests.swift (the
// fake daemon and timer: FakeContactsDaemon, FakeTimers, options, settle,
// fire, ctlAccounts, ctlBooks, people, serveList, standard); GTK:
// ui/internal/contactsctl fake_test.go.
//
// The contacts controllers against a fake daemon whose answers come back on
// the UI thread (SettleAsync waits for every call and question to end, as
// the Go tests' loop.run) and a timer the test fires itself (loop.fire).
// Params and results go through the contract's JSON, as over the socket.
// Fictional people only.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Controllers;

/// <summary>The UI thread, the fake daemon and the fake timer of a contacts controller test.</summary>
internal sealed class ContactsHarness : IDisposable
{
    private readonly List<Action> timers = [];

    public ContactsHarness(FakeContactsDaemon? daemon = null)
    {
        Daemon = daemon ?? Standard();
        Options = new ContactsOptions(Daemon, ContactBook.EnglishStrftime, new FixedTime(Stamp("2026-10-30T12:00:00Z")))
        {
            After = (_, f) => timers.Add(f),
        };
    }

    public TestUIContext Ui { get; } = new();

    public FakeContactsDaemon Daemon { get; }

    public ContactsOptions Options { get; }

    public static DateTimeOffset Stamp(string s) => DateTimeOffset.Parse(s, CultureInfo.InvariantCulture);

    /// <summary>Runs <paramref name="f"/> on the UI thread.</summary>
    public Task<T> Run<T>(Func<T> f) => Ui.RunAsync(f);

    /// <summary>Runs <paramref name="f"/> on the UI thread.</summary>
    public Task Run(Action f) => Ui.RunAsync(f);

    /// <summary>loop.run: waits until no call or question runs.</summary>
    public Task SettleAsync() => Quiescence.IdleAsync(Ui, Options.Pending);

    /// <summary>loop.fire: the timers set so far, then what they started.</summary>
    public async Task FireAsync()
    {
        await Run(() =>
        {
            var fs = timers.ToList();
            timers.Clear();
            foreach (var f in fs)
            {
                f();
            }
        });
        await SettleAsync();
    }

    public void Dispose() => Ui.Dispose();

    public static Account Account(
        string id, string name, string email, AccountKind? kind = null, IReadOnlyList<Capability>? caps = null,
        GraphConfig? graph = null, CalDAVConfig? caldav = null) => new()
        {
            Id = id,
            Config = new AccountConfig { Name = name, Email = email, Kind = kind, Graph = graph, CalDAV = caldav },
            Enabled = true,
            State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
            Capabilities = caps,
        };

    public static IReadOnlyList<Account> Accounts()
    {
        var on = new GraphConfig { Source = GraphSource.Daemon, Contacts = new ContactsConfig { Sync = true } };
        return
        [
            Account("acc_m", "Work", "jana@firma.cz", AccountKind.Graph, [Capability.Contacts], graph: on),
            Account("acc_p", "Petr", "petr@firma2.cz", AccountKind.Graph, [Capability.Contacts], graph: on),
            Account("acc_i", "Home", "jana@example.org"),
            Account("acc_d", "Nextcloud", "", AccountKind.CalDAV, [Capability.Calendar, Capability.Contacts],
                caldav: new CalDAVConfig { ServerUrl = "https://cloud.example.cz", Username = "jana", Contacts = true }),
        ];
    }

    public static IReadOnlyList<AddressBook> Books()
    {
        var t = Stamp("2026-10-30T11:57:00Z");
        return
        [
            new() { Id = "b_m1", AccountId = "acc_m", Name = "Contacts", Kind = AddressBookKind.Graph, Visible = true, Count = 3, LastSyncAt = t },
            new() { Id = "b_m2", AccountId = "acc_m", Name = "Archive", Kind = AddressBookKind.Graph, Visible = false, Count = 9 },
            new() { Id = "b_d", AccountId = "acc_d", Name = "Personal", Kind = AddressBookKind.CardDAV, Visible = true, Count = 2 },
            new() { Id = AddressBookId.Recent, Name = "recent", Kind = AddressBookKind.Recent, ReadOnly = true, Visible = true, Count = 5 },
        ];
    }

    /// <summary>people: contacts of a book, sorted, sections by the first letter.</summary>
    public static IReadOnlyList<ContactSummary> People(AddressBookId book, params string[] names) =>
        [.. names.Select((n, i) => new ContactSummary
        {
            Id = $"p_{book}_{i}", BookId = book, DisplayName = n, SortKey = n.ToLowerInvariant(), Section = n[..1].ToUpperInvariant(),
        })];

    /// <summary>serveList: contact.list from <paramref name="all"/> by book, query (a substring) and paging.</summary>
    public static void ServeList(FakeContactsDaemon d, Dictionary<string, IReadOnlyList<ContactSummary>> all) =>
        d.On(API.ContactList.Name, raw =>
        {
            var p = raw.Deserialize(ApiJsonContext.Wire.ContactListParams)!;
            List<ContactSummary> src = p.BookId == AddressBookId.All
                ? [.. all.GetValueOrDefault("b_m1", []), .. all.GetValueOrDefault("b_d", [])]
                : [.. all.GetValueOrDefault(p.BookId.Value, [])];
            src.Sort((a, b) => string.CompareOrdinal(a.SortKey, b.SortKey));
            var q = (p.Query ?? "").ToLowerInvariant();
            var match = src.Where(c => q.Length == 0 || c.DisplayName.ToLowerInvariant().Contains(q, StringComparison.Ordinal)).ToList();
            var limit = p.Limit ?? API.Limits.DefaultContactListLimit;
            var offset = p.Offset ?? 0;
            var end = Math.Min(offset + limit, match.Count);
            return new ContactListResult { Contacts = offset < end ? match[offset..end] : [], Total = match.Count };
        });

    /// <summary>standard: the accounts, the books, a consent refusal for acc_p and the contacts.</summary>
    public static FakeContactsDaemon Standard()
    {
        var d = new FakeContactsDaemon();
        d.Answer(API.AccountList.Name, new AccountListResult { Accounts = Accounts() });
        d.On(API.AddressBookList.Name, raw =>
        {
            var p = raw.Deserialize(ApiJsonContext.Wire.AddressBookListParams)!;
            if (p.AccountId?.Value == "acc_p")
            {
                throw new RpcException(new RpcError
                {
                    Code = ErrorCode.ContactsConsentRequired,
                    Message = "consent",
                    Data = JsonSerializer.SerializeToElement(new ContactsConsentRequiredData { AccountId = "acc_p", SignIn = true }, ApiJsonContext.Wire.ContactsConsentRequiredData),
                });
            }
            return new AddressBookListResult { Books = Books() };
        });
        ServeList(d, new()
        {
            ["b_m1"] = People("b_m1", "Adámek", "Beneš", "Nováková"),
            ["b_d"] = People("b_d", "Bílá", "Pokorný"),
            ["recent"] = People(AddressBookId.Recent, "a@x.cz", "b@x.cz"),
        });
        return d;
    }

    /// <summary>A clock that stands still.</summary>
    private sealed class FixedTime(DateTimeOffset now) : TimeProvider
    {
        public override DateTimeOffset GetUtcNow() => now;
    }
}
