// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of ReaderController: ui/internal/window/message_view.go
// (showMessage with bodyGen, render, renderHeaders, renderBody, loading
// with bodySpinnerDelay), remote.go (showRemoteBar, showPicturesBar),
// outbox.go (renderOutboxBanner), download.go (refreshChips), embedded.go
// (show, loadImages) and window.go (emptyPageName), which macOS keeps
// untested in MessageViewController.swift and
// EmbeddedWindowController.swift. The cache is a fake that answers when the
// test says; the spinner runs on a fake clock.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Presentation.ReaderFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class ReaderControllerTests
{
    private readonly FakeTimeProvider clock = new();
    private readonly FakeReaderCache cache = new();

    private ReaderController Make(ReaderMode mode = ReaderMode.Pane) => new(mode, cache, timeProvider: clock);

    [Fact]
    public void TheSummaryShowsAtOnceAndTheSpinnerOnlyAfterTheDelay()
    {
        var r = Make();
        Assert.Equal(ReaderPage.Empty, r.Page);
        var s = Summary("m1", "  Plans  ", to: [new Address { Name = "Bob", Email = "bob@example.invalid" }]);
        r.Show(s);
        Assert.Equal(ReaderPage.Message, r.Page);
        Assert.Equal("Plans", r.Subject);
        Assert.Equal(Format.FormatDateTime(s.Date), r.DateText);
        Assert.Single(r.Addresses.Row(AddressRowKind.To).Chips);
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        Assert.Equal("", r.BodyText);
        Assert.Equal(1, cache.FetchCalls);

        clock.Advance(ReaderController.SpinnerDelay - TimeSpan.FromMilliseconds(1));
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        clock.Advance(TimeSpan.FromMilliseconds(1));
        Assert.Equal(ReaderBodyPage.Loading, r.BodyPage);

        // The body arrives: the text, and the spinner is gone for good.
        cache.Entry("m1").Body = TextBody("m1", "Dear Bob,\n\nsee you.\n\n");
        cache.Settle("m1");
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        Assert.Equal("Dear Bob,\n\nsee you.", r.BodyText);
        clock.Advance(TimeSpan.FromSeconds(5));
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
    }

    [Fact]
    public void AFastBodyNeverShowsTheSpinner()
    {
        var r = Make();
        r.Show(Summary("m1"));
        cache.Entry("m1").Body = TextBody("m1");
        cache.Settle("m1");
        clock.Advance(TimeSpan.FromSeconds(1));
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        Assert.Equal("plain body", r.BodyText);
    }

    [Fact]
    public void AReplyForAMessageThePaneLeftIsDropped()
    {
        var r = Make();
        r.Show(Summary("m1", "First"));
        r.Show(Summary("m2", "Second"));
        cache.Entry("m1").Body = TextBody("m1", "late");
        cache.Settle("m1");
        Assert.Equal("Second", r.Subject);
        Assert.Equal("", r.BodyText);

        // The spinner of the first message was disarmed by the second's.
        clock.Advance(ReaderController.SpinnerDelay);
        Assert.Equal(ReaderBodyPage.Loading, r.BodyPage);
        cache.Entry("m2").Body = TextBody("m2", "mine");
        cache.Settle("m2");
        Assert.Equal("mine", r.BodyText);
    }

    [Fact]
    public void ACompleteEntryRendersAtOnceWithTheFullHeaders()
    {
        var r = Make();
        var s = Summary("m1", "Short");
        var lm = cache.Entry("m1");
        lm.Msg = Message(s with { Subject = "Full subject" }, cc: [new Address { Email = "cc@example.invalid" }]);
        lm.Body = TextBody("m1");
        var rendered = new List<ReaderRender>();
        r.Rendered += (_, e) => rendered.Add(e);
        r.Show(s);
        Assert.Equal("Full subject", r.Subject);
        Assert.True(r.Addresses.Row(AddressRowKind.Cc).Visible);
        Assert.Equal("plain body", r.BodyText);
        Assert.Single(rendered);
        Assert.Same(lm, rendered[0].Loaded);
    }

    [Fact]
    public void AnHtmlBodyGoesToTheViewerWithItsLinksAndTheBar()
    {
        var r = Make();
        var s = Summary("m1");
        var links = new List<Link> { new() { Text = "Site", Href = "https://example.org/" } };
        var lm = cache.Entry("m1");
        lm.Msg = Message(s);
        lm.Body = HtmlBody("m1", "<p>x</p>", remoteImages: 2, links: links);
        r.Show(s);
        Assert.Equal(ReaderBodyPage.Html, r.BodyPage);
        Assert.Equal("<p>x</p>", r.Html);
        Assert.Same(links, r.Links);
        Assert.False(r.HintVisible);
        Assert.True(r.RemoteBarVisible);
        Assert.False(r.RemoteBarLoading);
        Assert.Equal("2 remote images were blocked", r.RemoteBarText);
        Assert.True(r.TrustVisible);

        // The request for the images: the bar shows the wait, the body stays.
        lm.LoadingImages = true;
        r.RefreshRemoteBar(lm);
        Assert.True(r.RemoteBarLoading);
        Assert.Equal("Loading remote images…", r.RemoteBarText);
        Assert.Equal("<p>x</p>", r.Html);

        // Under allow nothing is left to load: no bar.
        lm.LoadingImages = false;
        lm.Body = HtmlBody("m1", "<p>x</p>", remoteImages: 2, remote: RemoteContentPolicy.Allow);
        r.Render(s, lm);
        Assert.False(r.RemoteBarVisible);
    }

    /// <summary>
    /// remote.go <c>renderPicturesBar</c>: under the remote-image bar, how
    /// many pictures of the HTML are on the mail server only, the wait while
    /// they download, and nothing for a body that is not in the viewer.
    /// </summary>
    [Fact]
    public void ThePicturesBarCountsWhatTheHtmlMissesOnTheServer()
    {
        var r = Make();
        var s = Summary("m1");
        var lm = cache.Entry("m1");
        lm.Msg = Message(s);
        lm.Body = HtmlBody("m1", "<p>x</p>", remoteImages: 2) with { RemotePictures = 3 };
        r.Show(s);
        Assert.True(r.RemoteBarVisible);
        Assert.True(r.PicturesBarVisible);
        Assert.False(r.PicturesBarLoading);
        Assert.Equal("3 pictures of this message are on the server only", r.PicturesBarText);

        // Download Pictures: the wait, the body stays.
        lm.LoadingPictures = true;
        r.RefreshRemoteBar(lm);
        Assert.True(r.PicturesBarVisible && r.PicturesBarLoading);
        Assert.Equal("Downloading pictures…", r.PicturesBarText);
        Assert.Equal("<p>x</p>", r.Html);

        // One left.
        lm.LoadingPictures = false;
        lm.Body = lm.Body with { RemotePictures = 1 };
        r.Render(s, lm);
        Assert.Equal("1 picture of this message is on the server only", r.PicturesBarText);

        // The plain text, a body on its way, an error: no bars.
        lm.Body = TextBody("m1") with { RemotePictures = 2 };
        r.Render(s, lm);
        Assert.False(r.PicturesBarVisible || r.RemoteBarVisible);
        lm.Body = HtmlBody("m1", "<p>x</p>") with { RemotePictures = 2 };
        r.Render(s, lm);
        Assert.True(r.PicturesBarVisible);
        r.Render(s, new LoadedMessage { Msg = lm.Msg });
        Assert.False(r.PicturesBarVisible);
        r.Render(s, lm);
        r.Clear();
        Assert.False(r.PicturesBarVisible);

        // An attached message's view has no pictures bar.
        var e = Make(ReaderMode.Embedded);
        e.ShowEmbedded(s, Attachment("2", "fwd.eml", "message/rfc822"), new MessageEmbeddedResult
        {
            PartId = "2",
            Message = Message(Summary("m1", "Inner")),
            Body = HtmlBody("m1", "<p>a</p>") with { RemotePictures = 2 },
        });
        Assert.False(e.PicturesBarVisible);
    }

    /// <summary>
    /// The body asked for again once its pictures were downloaded carries
    /// the same HTML, whose pictures load now: the viewer is told to load it
    /// again (MessageViewController.swift <c>picturesArrived</c>); a render
    /// of the same body, the same body read again (equal by value, as
    /// Swift compares it), or another HTML, is no reason to.
    /// </summary>
    [Fact]
    public void TheSameHtmlIsLoadedAgainOnceItsPicturesArrived()
    {
        var r = Make();
        var reloads = 0;
        r.HtmlReloadRequested += (_, _) => reloads++;
        var s = Summary("m1");
        var lm = cache.Entry("m1");
        lm.Msg = Message(s);
        const string html = "<p><img src=\"malachi-cid:acc/m1/2\"></p>";
        var inline = new Dictionary<string, string> { ["pic@x"] = "2" };
        var counted = HtmlBody("m1", html, inline: inline) with { RemotePictures = 1 };
        lm.Body = counted;
        r.Show(s);
        r.Render(s, lm);
        Assert.Equal(0, reloads); // the same body again

        // Read again, unchanged: a new body whose lists are new as well,
        // equal by value, with its pictures still on the server.
        lm.Body = HtmlBody("m1", html, inline: new Dictionary<string, string>(inline)) with { RemotePictures = 1 };
        Assert.NotEqual(counted, lm.Body); // the record's equality compares the lists by reference
        r.Render(s, lm);
        Assert.Equal(0, reloads);
        Assert.True(r.PicturesBarVisible);

        // The pictures arrived: the same HTML, a new body.
        lm.Body = counted with { RemotePictures = null };
        r.Render(s, lm);
        Assert.Equal(1, reloads);
        Assert.False(r.PicturesBarVisible);
        r.Render(s, lm);
        Assert.Equal(1, reloads);

        // A new body that counted none before: no reason.
        lm.Body = lm.Body with { Text = "again" };
        r.Render(s, lm);
        Assert.Equal(1, reloads);

        // Another HTML loads by itself (Html changes).
        lm.Body = counted;
        r.Render(s, lm);
        lm.Body = HtmlBody("m1", "<p>other</p>");
        r.Render(s, lm);
        Assert.Equal(1, reloads);
        Assert.Equal("<p>other</p>", r.Html);
    }

    [Fact]
    public void WithheldHtmlShowsTheTextWithTheHint()
    {
        var r = Make();
        var s = Summary("m1");
        cache.Entry("m1").Body = TextBody("m1", "only text") with { HtmlWithheld = true };
        r.Show(s);
        Assert.True(r.HintVisible);
        Assert.Equal("only text", r.BodyText);
        Assert.Null(r.Html);
    }

    [Fact]
    public void ABodyErrorIsShownAsTheSentence()
    {
        var r = Make();
        var s = Summary("m1");
        cache.Entry("m1").Err = new InvalidOperationException("boom");
        r.Show(s);
        cache.Settle("m1");
        Assert.Equal("Loading the message failed", r.BodyText);
        Assert.False(r.RemoteBarVisible);
    }

    [Theory]
    [InlineData(BodyState.Pending, "Downloading…")]
    [InlineData(BodyState.TooBig, "This message is too large to download.")]
    [InlineData(BodyState.Failed, "This message could not be read.")]
    public void BodyStatesAreSentences(string state, string text)
    {
        var r = Make();
        cache.Entry("m1").Body = TextBody("m1") with { BodyState = state };
        r.Show(Summary("m1"));
        cache.Settle("m1");
        Assert.Equal(text, r.BodyText);
    }

    [Fact]
    public void AnHtmlBodyTheViewerGaveUpOnStaysPlainForThatMessage()
    {
        var r = Make();
        var s = Summary("m1");
        var lm = cache.Entry("m1");
        lm.Body = HtmlBody("m1", "<p>bad</p>", remoteImages: 1);
        r.Show(s);
        Assert.Equal("<p>bad</p>", r.Html);

        r.HtmlUnavailable();
        Assert.Null(r.Html);
        Assert.True(r.HintVisible);
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        Assert.Equal("plain of m1", r.BodyText);
        Assert.False(r.RemoteBarVisible);
        Assert.Empty(r.Links);

        // message.get answering renders again: the same body is not loaded.
        lm.Msg = Message(s);
        cache.Settle("m1");
        Assert.Null(r.Html);
        Assert.True(r.HintVisible);

        // Another message tries the viewer again, and so does this one later.
        var other = Summary("m2");
        cache.Entry("m2").Body = HtmlBody("m2", "<p>good</p>");
        r.Show(other);
        Assert.Equal("<p>good</p>", r.Html);
        r.Show(s);
        Assert.Equal("<p>bad</p>", r.Html);
    }

    [Fact]
    public void HtmlUnavailableWithoutHtmlChangesNothing()
    {
        var r = Make();
        cache.Entry("m1").Body = TextBody("m1");
        r.Show(Summary("m1"));
        r.HtmlUnavailable();
        Assert.False(r.HintVisible);
        Assert.Equal("plain body", r.BodyText);
    }

    [Fact]
    public void ThePanesPlaceholderFollowsTheAccounts()
    {
        var r = Make();
        r.SetHasAccounts(false);
        Assert.Equal(ReaderPage.NoAccounts, r.Page);
        r.SetHasAccounts(true);
        Assert.Equal(ReaderPage.Empty, r.Page);

        // A message on display stays; the placeholder comes with the clear.
        r.Show(Summary("m1"));
        r.SetHasAccounts(false);
        Assert.Equal(ReaderPage.Message, r.Page);
        r.Clear();
        Assert.Equal(ReaderPage.NoAccounts, r.Page);
        Assert.Null(r.Current);
        Assert.Null(r.Html);

        // A clear disarms the spinner and drops a late reply.
        r.SetHasAccounts(true);
        r.Show(Summary("m2"));
        r.Clear();
        clock.Advance(TimeSpan.FromSeconds(1));
        cache.Entry("m2").Body = TextBody("m2");
        cache.Settle("m2");
        Assert.Equal(ReaderPage.Empty, r.Page);
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        Assert.Equal("", r.BodyText);
    }

    [Fact]
    public void AWindowNeverClears()
    {
        var r = Make(ReaderMode.Window);
        Assert.Equal(ReaderPage.Message, r.Page);
        r.Show(Summary("m1"));
        r.Clear();
        Assert.Equal(ReaderPage.Message, r.Page);
        Assert.NotNull(r.Current);
    }

    [Fact]
    public void TheDraftBannerIsThePanesAndAsksTheModel()
    {
        var pane = Make();
        pane.IsDraft = s => s.Id == "d1";
        pane.Show(Summary("d1"));
        Assert.True(pane.DraftVisible);
        pane.Show(Summary("m1"));
        Assert.False(pane.DraftVisible);

        var window = Make(ReaderMode.Window);
        window.IsDraft = _ => true;
        window.Show(Summary("d1"));
        Assert.False(window.DraftVisible);
    }

    [Fact]
    public void TheOutboxBannerShowsTheDeliveryState()
    {
        var r = Make(ReaderMode.Window);
        var s = Summary("o1") with { Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } };
        var lm = cache.Entry("o1");
        lm.Msg = Message(s);
        lm.Body = TextBody("o1");
        r.Show(s);
        Assert.True(r.OutboxVisible);
        Assert.Equal("Queued for sending", r.OutboxTitle);
        Assert.Equal("", r.OutboxButton);
        Assert.False(r.OutboxFailed);

        var failed = s with { Outbox = new OutboxInfo { State = OutboxState.Failed, Attempts = 3 } };
        r.RenderOutboxBanner(Message(failed));
        Assert.True(r.OutboxFailed);
        Assert.Equal("Retry", r.OutboxButton);
        Assert.Equal("Sending the message failed", r.OutboxTitle);

        // Delivered: gone.
        r.RenderOutboxBanner(Message(Summary("o1")));
        Assert.False(r.OutboxVisible);
    }

    [Fact]
    public void TheBodyScrollsToTheTopOncePerMessage()
    {
        var r = Make();
        var scrolls = 0;
        r.ScrollToTopRequested += (_, _) => scrolls++;
        var s = Summary("m1");
        r.Show(s);
        Assert.Equal(1, scrolls);
        cache.Entry("m1").Body = TextBody("m1");
        cache.Settle("m1");
        r.Show(s);
        Assert.Equal(1, scrolls);
        r.Show(Summary("m2"));
        Assert.Equal(2, scrolls);
    }

    [Fact]
    public void AClosedViewDropsLateRepliesAndTheSpinner()
    {
        var r = Make(ReaderMode.Window);
        r.Show(Summary("m1"));
        r.Close();
        clock.Advance(TimeSpan.FromSeconds(1));
        Assert.Equal(ReaderBodyPage.Text, r.BodyPage);
        cache.Entry("m1").Body = TextBody("m1");
        cache.Settle("m1");
        Assert.Equal("", r.BodyText);
    }

    [Fact]
    public void ChipsAreRebuiltOnlyWhenTheyChange()
    {
        var r = Make();
        var s = Summary("m1");
        var changes = 0;
        r.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName == nameof(ReaderController.Chips))
            {
                changes++;
            }
        };
        var lm = cache.Entry("m1");
        lm.Msg = Message(s, [Attachment("2", "a.pdf"), Attachment("3", "b.pdf")]);
        r.Show(s);
        Assert.Equal(1, changes);
        Assert.All(r.Chips, c => Assert.False(c.Available));
        Assert.Empty(r.SaveAll);

        lm.Body = TextBody("m1");
        cache.Settle("m1");
        Assert.Equal(2, changes);
        Assert.Equal(2, r.SaveAll.Count);

        // The same again: nothing to rebuild (a menu open on a chip stays).
        r.Render(s, lm);
        Assert.Equal(2, changes);
    }

    /// <summary>
    /// download.go <c>refreshChips</c>: a download of the message on display
    /// begins to show its spinner or ends, and the chips are drawn again from
    /// the cache entry, or from what was rendered last when the cache no
    /// longer holds it; Save All downloads first when a part is on the
    /// server.
    /// </summary>
    [Fact]
    public void TheChipsFollowTheDownloadOfTheMessage()
    {
        var r = Make();
        var s = Summary("m1");
        var lm = cache.Entry("m1");
        lm.Msg = Message(s, [Attachment("2", "a.pdf"), Attachment("3", "big.pdf", size: 300_000) with { Remote = true }]);
        lm.Body = TextBody("m1");
        r.Show(s);
        Assert.Equal([false, true], r.Chips.Select(c => c.OnServer));
        Assert.All(r.Chips, c => Assert.False(c.Downloading));
        Assert.Equal(2, r.SaveAll.Count);
        Assert.True(r.SaveAllRemote);

        cache.Spinning.Add("m1");
        r.RefreshChips("m1", lm);
        Assert.True(r.Chips[1].Downloading);
        Assert.False(r.Chips[0].Downloading);

        // Ended: the downloaded message (nothing on the server any more),
        // drawn from what was rendered when the cache let it go.
        cache.Spinning.Remove("m1");
        lm.Msg = Message(s, [Attachment("2", "a.pdf"), Attachment("3", "big.pdf", size: 300_000)]);
        r.RefreshChips("m1", null);
        Assert.All(r.Chips, c => Assert.False(c.OnServer || c.Downloading));
        Assert.False(r.SaveAllRemote);

        // Another message on display is left alone; an attached message's
        // view has no download of its own.
        r.Show(Summary("m2"));
        r.RefreshChips("m1", lm);
        Assert.Empty(r.Chips);
    }

    [Fact]
    public void AnAttachedMessageRendersReadOnlyAndLoadsItsImagesAgain()
    {
        var r = Make(ReaderMode.Embedded);
        Assert.False(r.TrustVisible);
        var containing = Summary("m1", "Outer");
        var inner = Summary("m1", "Inner") with { AccountId = containing.AccountId };
        MessageEmbeddedResult Result(string html, int blocked) => new()
        {
            PartId = "2",
            Message = Message(inner, [Attachment("", "x.pdf")]),
            Body = HtmlBody("m1", html, remoteImages: blocked),
        };
        r.ShowEmbedded(containing, Attachment("2", "fwd.eml", "message/rfc822"), Result("<p>a</p>", 1));
        Assert.Equal("Inner", r.Subject);
        Assert.Same(containing, r.Containing);
        Assert.Equal("<p>a</p>", r.Html);
        Assert.True(r.RemoteBarVisible);
        Assert.False(r.OutboxVisible);
        Assert.False(r.Chips[0].Available);

        // Show is the pane's and a window's: an attached message has no id.
        r.Show(Summary("m9"));
        Assert.Equal("Inner", r.Subject);
    }

    [Fact]
    public async Task LoadImagesOfAnAttachedMessageRendersThePartAgainWithAllow()
    {
        var r = Make(ReaderMode.Embedded);
        var containing = Summary("m1", "Outer");
        MessageEmbeddedResult Result(string html, int blocked, string remote) => new()
        {
            PartId = "2",
            Message = Message(Summary("m1", "Inner")),
            Body = HtmlBody("m1", html, remoteImages: blocked, remote: remote),
        };
        r.ShowEmbedded(containing, Attachment("2", "fwd.eml", "message/rfc822"), Result("<p>a</p>", 1, RemoteContentPolicy.Block));
        cache.EmbeddedGate = new TaskCompletionSource();
        cache.Embedded = remote => Result("<p>with images</p>", 0, remote?.Value ?? "");
        var load = r.LoadEmbeddedImagesAsync();
        Assert.True(r.RemoteBarLoading);
        // A second click while it runs asks nothing.
        await r.LoadEmbeddedImagesAsync();
        cache.EmbeddedGate.SetResult();
        await load;
        Assert.Equal([(RemoteContentPolicy?)RemoteContentPolicy.Allow], cache.EmbeddedCalls);
        Assert.Equal("<p>with images</p>", r.Html);
        Assert.False(r.RemoteBarVisible);
        Assert.Empty(cache.DownloadCalls);
    }

    /// <summary>
    /// embedded.go <c>loadImages</c> through <c>embeddedData</c>: an attached
    /// message the daemon moved to the mail server since the window opened is
    /// downloaded once, and the part the download renumbered is the one
    /// asked for from then on.
    /// </summary>
    [Fact]
    public async Task LoadImagesOfAnAttachedMessageOnTheServerDownloadsItFirst()
    {
        var r = Make(ReaderMode.Embedded);
        var containing = Summary("m1", "Outer");
        var eml = Attachment("2", "fwd.eml", "message/rfc822");
        r.ShowEmbedded(containing, eml, new MessageEmbeddedResult
        {
            PartId = "2",
            Message = Message(Summary("m1", "Inner")),
            Body = HtmlBody("m1", "<p>a</p>", remoteImages: 1),
        });
        var answered = 0;
        cache.Embedded = _ => ++answered == 1
            ? throw new RpcException(new RpcError { Code = ErrorCode.PartNotDownloaded, Message = "on the server" })
            : new MessageEmbeddedResult { PartId = "4", Message = Message(Summary("m1", "Inner")), Body = HtmlBody("m1", "<p>with images</p>") };
        cache.Downloaded = Message(containing, [eml with { PartId = "4" }]);
        await r.LoadEmbeddedImagesAsync();
        Assert.Equal(["m1"], cache.DownloadCalls.Select(id => id.Value));
        Assert.Equal("<p>with images</p>", r.Html);

        // The next load asks for the part the download renumbered.
        cache.Downloaded = null;
        cache.Embedded = _ => new MessageEmbeddedResult { PartId = "4", Message = Message(Summary("m1", "Inner")), Body = HtmlBody("m1", "<p>b</p>") };
        await r.LoadEmbeddedImagesAsync();
        Assert.Single(cache.DownloadCalls);
        Assert.Equal(["2", "4", "4"], cache.EmbeddedParts);
        Assert.Equal("<p>b</p>", r.Html);
    }

    [Fact]
    public async Task AFailedImageLoadToastsAndOffersTheImagesAgain()
    {
        var r = Make(ReaderMode.Embedded);
        var toasts = new List<string>();
        r.Toast = toasts.Add;
        var containing = Summary("m1", "Outer");
        r.ShowEmbedded(containing, Attachment("2", "fwd.eml", "message/rfc822"), new MessageEmbeddedResult
        {
            PartId = "2",
            Message = Message(Summary("m1", "Inner")),
            Body = HtmlBody("m1", "<p>a</p>", remoteImages: 3),
        });
        cache.Embedded = _ => null;
        await r.LoadEmbeddedImagesAsync();
        Assert.Equal(["Loading the images failed"], toasts);
        Assert.True(r.RemoteBarVisible);
        Assert.False(r.RemoteBarLoading);
        Assert.Equal("3 remote images were blocked", r.RemoteBarText);
        Assert.Equal("<p>a</p>", r.Html);
    }

    [Fact]
    public async Task AReplyAfterTheWindowClosedIsDropped()
    {
        var r = Make(ReaderMode.Embedded);
        r.ShowEmbedded(Summary("m1"), Attachment("2", "fwd.eml", "message/rfc822"), new MessageEmbeddedResult
        {
            PartId = "2",
            Message = Message(Summary("m1", "Inner")),
            Body = HtmlBody("m1", "<p>a</p>", remoteImages: 1),
        });
        cache.EmbeddedGate = new TaskCompletionSource();
        cache.Embedded = _ => new MessageEmbeddedResult { PartId = "2", Message = Message(Summary("m1", "Late")), Body = HtmlBody("m1", "<p>b</p>") };
        var load = r.LoadEmbeddedImagesAsync();
        r.Close();
        cache.EmbeddedGate.SetResult();
        await load;
        Assert.Equal("Inner", r.Subject);
    }
}
