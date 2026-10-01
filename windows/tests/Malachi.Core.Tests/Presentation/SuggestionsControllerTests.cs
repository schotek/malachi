// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of Malachi.Core.Presentation.SuggestionsController, the recipient
// completion of ui/internal/compose/suggest.go (suggestions) and
// macos/Sources/MalachiMail/Compose/RecipientSuggestionsController.swift,
// neither of which has a test of its own (suggest_test.go and Swift's
// SuggestTests cover tokenAt and replaceToken, ported in
// Compose/SuggestTests): the minimum length, the 150 ms pause in typing on a
// FakeTimeProvider, the request (the sender's account, limit 8), the
// generation and the token check that drop an answer the row has outrun,
// quiet failures, the keys with their wrap-around, the acceptance and the
// change it makes, which starts no search, and the rows.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Controllers;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class SuggestionsControllerTests
{
    private static readonly Contact Ann = new() { Name = "Ann Example", Address = "ann@example.org", Source = ContactSource.Sent };
    private static readonly Contact Andy = new() { Address = "andy@example.org", Source = ContactSource.AddressBook, Book = "Work" };

    [Fact]
    public async Task AShortTokenAsksNothing()
    {
        await using var h = await Harness.StartAsync();
        await h.Type("a");
        Assert.False(h.Suggestions.IsArmed);
        await h.PauseAsync();
        Assert.Empty(h.Script.Queries);
        Assert.False(h.Suggestions.IsVisible);
    }

    [Fact]
    public async Task ItAsksAfterThePauseForTheSendersBooks()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann, Andy];
        await h.Type("an");
        Assert.True(h.Suggestions.IsArmed);
        await h.IdleAsync();
        h.Time.Advance(TimeSpan.FromMilliseconds(149));
        await h.IdleAsync();
        Assert.Empty(h.Script.Queries);

        h.Time.Advance(TimeSpan.FromMilliseconds(1));
        await h.IdleAsync();
        var q = Assert.Single(h.Script.Queries);
        Assert.Equal("an", q.Query);
        Assert.Equal(new AccountId("acc1"), q.AccountId);
        Assert.Equal(Suggest.SuggestLimit, q.Limit);
        Assert.True(h.Suggestions.IsVisible);
        Assert.Equal(0, h.Suggestions.SelectedIndex);
        Assert.Equal([Ann, Andy], h.Suggestions.Contacts);
        Assert.Equal(1, h.Changes);
    }

    [Fact]
    public async Task TypingOnRestartsThePause()
    {
        await using var h = await Harness.StartAsync();
        await h.Type("an");
        await h.IdleAsync();
        h.Time.Advance(TimeSpan.FromMilliseconds(100));
        await h.Type("ann");
        await h.IdleAsync();
        h.Time.Advance(TimeSpan.FromMilliseconds(100));
        await h.IdleAsync();
        Assert.Empty(h.Script.Queries);
        h.Time.Advance(TimeSpan.FromMilliseconds(50));
        await h.IdleAsync();
        Assert.Equal(["ann"], h.Script.Queries.Select(q => q.Query));
    }

    [Fact]
    public async Task TheTokenUnderTheCaretIsAsked()
    {
        await using var h = await Harness.StartAsync();
        // The caret in the first of two recipients: the whole token it is in.
        await h.Type("bob, carl@example.org", caret: 2);
        await h.PauseAsync();
        Assert.Equal(["bob"], h.Script.Queries.Select(q => q.Query));
    }

    [Fact]
    public async Task AnAnswerTheRowHasOutrunIsDropped()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        var held = h.Script.Hold();
        await h.Type("an");
        await h.IdleAsync();
        h.Time.Advance(Suggest.SuggestDebounce);
        await held.ArrivedAsync();
        // Typed on while the search was out: the token is another one now,
        // and its own search waits for the next pause.
        await h.Type("ann");
        held.Release();
        await h.IdleAsync();
        Assert.False(h.Suggestions.IsVisible);
        Assert.Equal(0, h.Changes);
        Assert.Single(h.Script.Queries);
    }

    [Fact]
    public async Task AHideDropsTheSearchInFlight()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        var held = h.Script.Hold();
        await h.Type("an");
        await h.IdleAsync();
        h.Time.Advance(Suggest.SuggestDebounce);
        await held.ArrivedAsync();
        await h.Run(() => h.Suggestions.Hide());
        held.Release();
        await h.IdleAsync();
        Assert.False(h.Suggestions.IsVisible);
    }

    [Fact]
    public async Task AHideDisarmsThePause()
    {
        await using var h = await Harness.StartAsync();
        await h.Type("an");
        await h.Run(() => h.Suggestions.Hide());
        Assert.False(h.Suggestions.IsArmed);
        await h.PauseAsync();
        Assert.Empty(h.Script.Queries);
    }

    [Fact]
    public async Task AFailureAndAnEmptyAnswerHideQuietly()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        await h.Type("an");
        await h.PauseAsync();
        Assert.True(h.Suggestions.IsVisible);

        h.Script.Fails = true;
        await h.Type("ann");
        await h.PauseAsync();
        Assert.False(h.Suggestions.IsVisible);

        h.Script.Fails = false;
        await h.Type("anne");
        await h.PauseAsync();
        Assert.True(h.Suggestions.IsVisible);
        h.Script.Answer = [];
        await h.Type("annet");
        await h.PauseAsync();
        Assert.False(h.Suggestions.IsVisible);
        Assert.Empty(h.Suggestions.Rows);
        Assert.Equal(4, h.Script.Queries.Count);
    }

    [Fact]
    public async Task ATokenShorterAgainHidesThePopup()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        await h.Type("an");
        await h.PauseAsync();
        Assert.True(h.Suggestions.IsVisible);
        await h.Type("a");
        Assert.False(h.Suggestions.IsVisible);
    }

    [Fact]
    public async Task TheKeysMoveWithWrapAroundAndEscapeHides()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann, Andy, Ann with { Address = "anna@example.org" }];
        // Hidden: every key goes on to the row.
        await h.Run(() => Assert.False(h.Suggestions.OnKey(SuggestionKey.Down, false)));
        await h.Type("an");
        await h.PauseAsync();
        await h.Run(() =>
        {
            Assert.True(h.Suggestions.OnKey(SuggestionKey.Down, false));
            Assert.Equal(1, h.Suggestions.SelectedIndex);
            Assert.True(h.Suggestions.OnKey(SuggestionKey.Down, false));
            Assert.True(h.Suggestions.OnKey(SuggestionKey.Down, false));
            Assert.Equal(0, h.Suggestions.SelectedIndex);
            Assert.True(h.Suggestions.OnKey(SuggestionKey.Up, false));
            Assert.Equal(2, h.Suggestions.SelectedIndex);
            // Ctrl or Alt: the row's.
            Assert.False(h.Suggestions.OnKey(SuggestionKey.Enter, true));
            Assert.True(h.Suggestions.IsVisible);
            Assert.True(h.Suggestions.OnKey(SuggestionKey.Escape, false));
            Assert.False(h.Suggestions.IsVisible);
            Assert.False(h.Suggestions.OnKey(SuggestionKey.Escape, false));
        });
        Assert.Empty(h.Acceptances);
    }

    [Theory]
    [InlineData(SuggestionKey.Enter)]
    [InlineData(SuggestionKey.Tab)]
    public async Task EnterAndTabAcceptTheSelectedSuggestion(SuggestionKey key)
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Andy, Ann];
        await h.Type("bob@example.org, an");
        await h.PauseAsync();
        await h.Run(() =>
        {
            Assert.True(h.Suggestions.OnKey(SuggestionKey.Down, false));
            Assert.True(h.Suggestions.OnKey(key, false));
        });
        var accepted = Assert.Single(h.Acceptances);
        Assert.Equal("bob@example.org, Ann Example <ann@example.org>, ", accepted.Text);
        Assert.Equal(accepted.Text.Length, accepted.Caret);
        Assert.False(h.Suggestions.IsVisible);
    }

    [Fact]
    public async Task AnAcceptanceIsPickedAsAnAddress()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        await h.Type("an");
        await h.PauseAsync();
        await h.Run(() => h.Suggestions.Accept(0));
        var picked = Assert.Single(h.Picks);
        Assert.Equal("Ann Example", picked.Name);
        Assert.Equal("ann@example.org", picked.Email);
        Assert.False(h.Suggestions.IsVisible);
    }

    [Fact]
    public async Task TheAcceptedTextStartsNoSearch()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        await h.Type("an");
        await h.PauseAsync();
        await h.Run(() => h.Suggestions.Accept(0));
        var accepted = Assert.Single(h.Acceptances);
        Assert.Equal("Ann Example <ann@example.org>, ", accepted.Text);

        // The view puts it into the row, whose TextChanged comes back.
        await h.Type(accepted.Text, accepted.Caret);
        Assert.False(h.Suggestions.IsArmed);
        // The next edit asks again.
        await h.Type(accepted.Text + "bo");
        Assert.True(h.Suggestions.IsArmed);
        await h.PauseAsync();
        Assert.Equal(["an", "bo"], h.Script.Queries.Select(q => q.Query));
    }

    [Fact]
    public async Task AnAcceptanceKeepsTheRestOfTheRow()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        await h.Type("an ,carl@example.org", caret: 2);
        await h.PauseAsync();
        await h.Run(() => h.Suggestions.Accept(0));
        var accepted = Assert.Single(h.Acceptances);
        // A separator already follows: none is added, the spaces before it go.
        Assert.Equal("Ann Example <ann@example.org>,carl@example.org", accepted.Text);
        Assert.Equal("Ann Example <ann@example.org>".Length, accepted.Caret);
    }

    [Fact]
    public async Task AnOutOfRangeAcceptanceDoesNothing()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        await h.Type("an");
        await h.PauseAsync();
        await h.Run(() =>
        {
            h.Suggestions.Accept(1);
            h.Suggestions.Accept(-1);
        });
        Assert.Empty(h.Acceptances);
        Assert.True(h.Suggestions.IsVisible);
    }

    [Fact]
    public async Task AClosedControllerTakesNothingLate()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Answer = [Ann];
        var held = h.Script.Hold();
        await h.Type("an");
        await h.IdleAsync();
        h.Time.Advance(Suggest.SuggestDebounce);
        await held.ArrivedAsync();
        await h.Run(() => h.Suggestions.Dispose());
        held.Release();
        await h.IdleAsync();
        Assert.False(h.Suggestions.IsVisible);
        await h.Type("anna");
        Assert.False(h.Suggestions.IsArmed);
        await h.Run(() => h.Suggestions.Dispose());
    }

    [Fact]
    public void RowsShowTheNameOverTheAddressAndTheirSource()
    {
        var named = SuggestionRow.For(Ann);
        Assert.Equal(new SuggestionRow("Ann Example", "ann@example.org", "document-open-recent-symbolic", "Recently used"), named);
        var book = SuggestionRow.For(Andy);
        Assert.Equal(new SuggestionRow("andy@example.org", "", "x-office-address-book-symbolic", "Work"), book);
        var unnamedBook = SuggestionRow.For(Andy with { Book = null });
        Assert.Equal("Address book", unnamedBook.Tooltip);
        Assert.Equal("", SuggestionRow.For(Ann with { Name = "" }).Secondary);
        // Windows-only: cleaned for display (DisplayText).
        Assert.Equal(new SuggestionRow(Text.DisplayTextTests.CleanedName, "ann@example.org", "document-open-recent-symbolic", "Recently used"),
            SuggestionRow.For(Ann with { Name = Text.DisplayTextTests.HostileName, Address = "ann@example.org\u202E" }));
    }

    [Theory]
    // GTK's test (c.Name != "") on the cleaned name, which is not trimmed:
    // spaces are a name, and so are controls, which cleaning makes spaces.
    [InlineData("   ", "   ", "ann@example.org")]
    [InlineData("\u202E\u0007", " ", "ann@example.org")]
    [InlineData(" Ann ", " Ann ", "ann@example.org")]
    // Windows-only: nothing left once cleaned, or nothing but characters
    // that draw nothing (the marks, ZWSP, WJ, BOM), is no name: the address
    // shows.
    [InlineData("", "ann@example.org", "")]
    [InlineData("\u202E\u2066", "ann@example.org", "")]
    [InlineData("\u200F", "ann@example.org", "")]
    [InlineData("\u200E\u061C\u200B\u2060\uFEFF", "ann@example.org", "")]
    public void ARowIsNamedAsGtkNamesIt(string name, string primary, string secondary)
    {
        var row = SuggestionRow.For(Ann with { Name = name });
        Assert.Equal(primary, row.Primary);
        Assert.Equal(secondary, row.Secondary);
    }

    /// <summary>What contact.search answers, and what it was asked.</summary>
    private sealed class Script
    {
        private readonly Lock gate = new();
        private readonly List<ContactSearchParams> queries = [];
        private IReadOnlyList<Contact> answer = [];
        private bool fails;
        private HeldAnswer? held;

        public IReadOnlyList<Contact> Answer
        {
            get
            {
                lock (gate)
                {
                    return answer;
                }
            }

            set
            {
                lock (gate)
                {
                    answer = value;
                }
            }
        }

        public bool Fails
        {
            get
            {
                lock (gate)
                {
                    return fails;
                }
            }

            set
            {
                lock (gate)
                {
                    fails = value;
                }
            }
        }

        public IReadOnlyList<ContactSearchParams> Queries
        {
            get
            {
                lock (gate)
                {
                    return [.. queries];
                }
            }
        }

        public HeldAnswer Hold()
        {
            var h = new HeldAnswer();
            lock (gate)
            {
                held = h;
            }
            return h;
        }

        public async Task<string> Search(string p)
        {
            Task wait;
            lock (gate)
            {
                queries.Add(JsonCoding.Decode<ContactSearchParams>(p));
                wait = held?.WaitAsync() ?? Task.CompletedTask;
            }
            await wait;
            if (Fails)
            {
                throw new RpcException(new RpcError { Code = ErrorCode.StorageError, Message = "disk" });
            }
            return JsonCoding.EncodeToString(new ContactSearchResult { Contacts = Answer });
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 28, 10, 0, 0, TimeSpan.Zero));

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public SuggestionsController Suggestions { get; private set; } = null!;

        /// <summary>The row: its text and caret (a scalar offset).</summary>
        public (string Text, int Caret) Field { get; set; } = ("", 0);

        public List<SuggestionAcceptance> Acceptances { get; } = [];

        public List<Address> Picks { get; } = [];

        public int Changes { get; private set; }

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            h.Fake.On(API.ContactSearch.Name, h.Script.Search);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Suggestions = await h.Ui.RunAsync(() =>
            {
                var s = new SuggestionsController(h.Client, () => "acc1", () => h.Field, h.Time, h.Pending);
                s.Changed += (_, _) => h.Changes++;
                s.Accepted += (_, a) => h.Acceptances.Add(a);
                s.Picked += (_, a) => h.Picks.Add(a);
                return s;
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        /// <summary>The row now holds <paramref name="text"/> with the caret at <paramref name="caret"/> (its end by default).</summary>
        public Task Type(string text, int? caret = null) => Run(() =>
        {
            Field = (text, caret ?? Suggest.ScalarOffset(text.Length, text));
            Suggestions.TextChanged();
        });

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        /// <summary>The pause in typing passes, and the search it started settles.</summary>
        public async Task PauseAsync()
        {
            await IdleAsync();
            Time.Advance(Suggest.SuggestDebounce);
            await IdleAsync();
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() => Suggestions.Dispose());
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
