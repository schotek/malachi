// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test: the Unsubscribe button's controller over a fake
// daemon (ui/internal/window/bulk.go has no Go test of its own; its flow is
// unsubscribe, bulkDialog, openBulkPage, callUnsubscribe and bulkFallback).
// Nothing happens without the answer to the question; a web-page offer opens
// the page and calls nothing; a one-click or mailto offer calls
// message.unsubscribe and turns the cached message's offer into "unsubscribed
// on"; the toasts say queued and the failures; an openUrl answer offers the
// sender's page in a dialog of its own; only https pages are ever opened.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Tests.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class BulkActionsControllerTests
{
    private static readonly AccountId Account = "acc_1";
    private static readonly MessageId Id = "m1";
    private static readonly DateTimeOffset Remembered = new(2026, 9, 30, 12, 0, 0, TimeSpan.Zero);

    private static readonly UnsubscribeOffer OneClick = new() { Method = UnsubscribeMethod.OneClick, Target = "shop.example" };
    private static readonly UnsubscribeOffer Mailto = new() { Method = UnsubscribeMethod.Mailto, Target = "u@shop.example" };
    private static readonly UnsubscribeOffer Page = new() { Method = UnsubscribeMethod.Url, Target = "shop.example", Url = "https://shop.example/u?x=1" };

    private static Message Msg(UnsubscribeOffer? offer, BulkKind kind = BulkKind.Newsletter) => new()
    {
        Summary = new MessageSummary
        {
            Id = Id,
            AccountId = Account,
            FolderId = "f_inbox",
            Subject = "s",
            Date = Remembered,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
            Bulk = new BulkInfo { Kind = kind, Domain = "shop.example" },
        },
        Unsubscribe = offer,
    };

    private static string Answer(string outcome, string? url = null, bool? unverified = null, DateTimeOffset? at = null) =>
        JsonCoding.EncodeToString(new MessageUnsubscribeResult { Outcome = outcome, Url = url, Unverified = unverified, UnsubscribedAt = at });

    [Fact]
    public async Task OneClickAsksThenUnsubscribesAndRemembers()
    {
        await using var h = await Harness.StartAsync(OneClick);
        h.Reply = _ => Answer(UnsubscribeOutcome.Unsubscribed, at: Remembered);
        await h.ClickAsync();
        await h.IdleAsync();

        var conf = Assert.Single(h.Asked);
        Assert.Equal("Unsubscribe from shop.example?", conf.Heading);
        var sent = Assert.Single(h.Daemon.Params.All<MessageUnsubscribeParams>(API.MessageUnsubscribe.Name));
        Assert.Equal((Account, Id), (sent.AccountId, sent.MessageId)); // the ids only: no URL, no address
        Assert.Equal(Remembered, h.Entry.Msg!.Unsubscribe!.UnsubscribedAt);
        Assert.False(h.Entry.Unsubscribing);
        // Busy from the click, then the offer applied.
        Assert.Equal<(bool, DateTimeOffset?)>([(true, null), (false, Remembered)], h.Changes);
        Assert.Empty(h.Says);
        Assert.Empty(h.Opened);

        // Done: the button has nothing left to do.
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Single(h.Asked);
        Assert.Equal(1, h.Daemon.Params.Count(API.MessageUnsubscribe.Name));
    }

    [Fact]
    public async Task MailtoQueuesAndSaysSo()
    {
        await using var h = await Harness.StartAsync(Mailto);
        h.Reply = _ => Answer(UnsubscribeOutcome.Queued, at: Remembered);
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Equal("_Send Request", Assert.Single(h.Asked).Confirm);
        Assert.Equal(["Unsubscribe request queued"], h.Says);
        Assert.Equal(Remembered, h.Entry.Msg!.Unsubscribe!.UnsubscribedAt);
    }

    [Fact]
    public async Task DecliningSendsNothing()
    {
        await using var h = await Harness.StartAsync(OneClick);
        h.Confirmed = false;
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Single(h.Asked);
        Assert.Empty(h.Daemon.Fake.Calls);
        Assert.True(h.Changes.Count == 0 && !h.Entry.Unsubscribing && h.Entry.Msg!.Unsubscribe!.UnsubscribedAt is null);
    }

    [Fact]
    public async Task WithoutTheQuestionNothingIsSent()
    {
        await using var h = await Harness.StartAsync(OneClick);
        await h.Daemon.Ui.RunAsync(() => h.Controller.Confirm = null);
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Empty(h.Daemon.Fake.Calls);
        Assert.Empty(h.Changes);

        // A dialog that cannot be shown is a no, too.
        await h.Daemon.Ui.RunAsync(() => h.Controller.Confirm = (_, _) => throw new InvalidOperationException("another dialog is open"));
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Empty(h.Daemon.Fake.Calls);
    }

    [Fact]
    public async Task APageOffersOpenTheBrowserAndCallNothing()
    {
        await using var h = await Harness.StartAsync(Page);
        await h.ClickAsync();
        await h.IdleAsync();
        var conf = Assert.Single(h.Asked);
        Assert.Equal("Open the unsubscribe page?", conf.Heading);
        Assert.Contains("https://shop.example/u?x=1", conf.Body, StringComparison.Ordinal);
        Assert.Equal(["https://shop.example/u?x=1"], h.Opened);
        Assert.Empty(h.Daemon.Fake.Calls);
        Assert.True(h.Changes.Count == 0 && h.Entry.Msg!.Unsubscribe!.UnsubscribedAt is null); // nothing is remembered

        // A page that is not https is asked about but never opened.
        h.Entry.Msg = Msg(Page with { Url = "http://shop.example/u" });
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Equal(2, h.Asked.Count);
        Assert.Single(h.Opened);
    }

    [Fact]
    public async Task AnUnverifiedOneClickOffersThePage()
    {
        await using var h = await Harness.StartAsync(OneClick);
        h.Reply = _ => Answer(UnsubscribeOutcome.OpenUrl, "https://shop.example/unsub", unverified: true);
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Equal(2, h.Asked.Count);
        Assert.Equal("The sender could not be verified", h.Asked[1].Heading);
        Assert.Contains("https://shop.example/unsub", h.Asked[1].Body, StringComparison.Ordinal);
        Assert.Equal(["https://shop.example/unsub"], h.Opened);
        Assert.True(h.Entry.Msg!.Unsubscribe!.UnsubscribedAt is null && !h.Entry.Unsubscribing); // nothing was sent, nothing is remembered

        // Declined: the page stays shut.
        h.Opened.Clear();
        h.Answers.Clear();
        h.Answers.AddRange([true, false]);
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Empty(h.Opened);

        // An address that is no https page is not even offered.
        h.Reply = _ => Answer(UnsubscribeOutcome.OpenUrl, "javascript:alert(1)", unverified: true);
        var asked = h.Asked.Count;
        h.Answers.Clear();
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Equal(asked + 1, h.Asked.Count);
        Assert.Empty(h.Opened);
    }

    [Fact]
    public async Task AFailureIsToastedAndTheButtonComesBack()
    {
        await using var h = await Harness.StartAsync(OneClick);
        h.Reply = DaemonHarness.Fails(ErrorCode.UnsubscribeFailed, "status 403");
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Equal(["The sender's server refused the request."], h.Says);
        Assert.False(h.Entry.Unsubscribing);
        Assert.Null(h.Entry.Msg!.Unsubscribe!.UnsubscribedAt);
        Assert.Equal<(bool, DateTimeOffset?)>([(true, null), (false, null)], h.Changes);

        // Another failure reads as the action failing.
        h.Reply = DaemonHarness.Fails(ErrorCode.NetworkError, "x");
        await h.ClickAsync();
        await h.IdleAsync();
        Assert.Equal("Unsubscribing failed: the server could not be reached", h.Says[^1]);
    }

    [Fact]
    public async Task OnlyAnOpenOfferOfALoadedMessageIsActedOn()
    {
        await using var h = await Harness.StartAsync(null);
        await h.ClickAsync(); // no offer
        h.Entry.Msg = null;
        await h.ClickAsync(); // message.get has not answered
        await h.Daemon.Ui.RunAsync(() => h.Controller.Unsubscribe("m404", null, h.Says.Add)); // not in the cache
        h.Entry.Msg = Msg(OneClick with { UnsubscribedAt = Remembered });
        await h.ClickAsync(); // already done
        h.Entry.Msg = Msg(OneClick);
        h.Entry.Unsubscribing = true;
        await h.ClickAsync(); // a request runs
        await h.IdleAsync();
        Assert.Empty(h.Asked);
        Assert.Empty(h.Daemon.Fake.Calls);
        Assert.Empty(h.Changes);
        Assert.True(await h.Daemon.Ui.RunAsync(() => h.Controller.IsBusy(Id)));
    }

    // A newer message.get replaced the entry while the request ran: the
    // answer goes to the entry the cache holds now, and the button of both
    // stops waiting.
    [Fact]
    public async Task TheAnswerGoesToTheEntryTheCacheHoldsNow()
    {
        await using var h = await Harness.StartAsync(OneClick);
        var hold = h.Daemon.Hold();
        h.Hold = hold;
        var first = h.Entry;
        await h.ClickAsync();
        await hold.ArrivedAsync();
        Assert.True(first.Unsubscribing);

        var newer = new LoadedMessage { Msg = Msg(OneClick), Unsubscribing = true };
        h.Cache.Replace(Id, newer);
        hold.Release();
        await h.IdleAsync();

        Assert.False(first.Unsubscribing);
        Assert.False(newer.Unsubscribing);
        Assert.Equal(Remembered, newer.Msg!.Unsubscribe!.UnsubscribedAt);
        Assert.Null(first.Msg!.Unsubscribe!.UnsubscribedAt); // the replaced entry is no longer shown
        Assert.Same(newer, h.Notified[^1]); // the views redraw from the entry they read
    }

    private sealed class Harness : IAsyncDisposable
    {
        private Harness(DaemonHarness daemon) => Daemon = daemon;

        public DaemonHarness Daemon { get; }

        public FakeReaderCache Cache { get; } = new();

        public BulkActionsController Controller { get; private set; } = null!;

        public LoadedMessage Entry => Cache.Entry(Id);

        /// <summary>Holds message.unsubscribe's answer until released, when set.</summary>
        public HeldAnswer? Hold { get; set; }

        /// <summary>What message.unsubscribe answers.</summary>
        public Func<string, string> Reply { get; set; } = _ => Answer(UnsubscribeOutcome.Unsubscribed, at: Remembered);

        /// <summary>The answers of the dialogs in order; when used up, <see cref="Confirmed"/>.</summary>
        public List<bool> Answers { get; } = [];

        public bool Confirmed { get; set; } = true;

        public List<UnsubscribeConfirmation> Asked { get; } = [];

        public List<string> Opened { get; } = [];

        public List<string> Says { get; } = [];

        public List<(bool Busy, DateTimeOffset? At)> Changes { get; } = [];

        /// <summary>The entry of every change, as the views get it.</summary>
        public List<LoadedMessage> Notified { get; } = [];

        public static async Task<Harness> StartAsync(UnsubscribeOffer? offer)
        {
            var h = new Harness(await DaemonHarness.StartAsync());
            h.Entry.Msg = Msg(offer);
            h.Daemon.On(API.MessageUnsubscribe.Name, async p =>
            {
                if (h.Hold is { } hold)
                {
                    await hold.WaitAsync();
                }
                return h.Reply(p);
            });
            var client = await h.Daemon.ConnectAsync();
            await h.Daemon.Ui.RunAsync(() =>
            {
                var c = new BulkActionsController(client, h.Cache, h.Says.Add, pending: h.Daemon.Pending)
                {
                    Confirm = (_, conf) =>
                    {
                        h.Asked.Add(conf);
                        var answer = h.Answers.Count > 0 ? h.Answers[0] : h.Confirmed;
                        if (h.Answers.Count > 0)
                        {
                            h.Answers.RemoveAt(0);
                        }
                        return Task.FromResult(answer);
                    },
                    OpenPage = (_, url) =>
                    {
                        h.Opened.Add(url);
                        return Task.CompletedTask;
                    },
                };
                c.Changed += (_, e) =>
                {
                    h.Changes.Add((e.Loaded.Unsubscribing, e.Loaded.Msg?.Unsubscribe?.UnsubscribedAt));
                    h.Notified.Add(e.Loaded);
                };
                h.Daemon.CloseAtEnd(c.Close);
                h.Controller = c;
            });
            return h;
        }

        // The click of the strip's button, on the UI thread; toasts go to Says.
        public Task ClickAsync() => Daemon.Ui.RunAsync(() => Controller.Unsubscribe(Id, null, Says.Add));

        public Task IdleAsync() => Daemon.IdleAsync();

        public ValueTask DisposeAsync() => Daemon.DisposeAsync();
    }
}
