// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ActionHelpersTests.swift, the
// counterpart of ui/internal/window/actions_test.go (TestSubjectText,
// TestBodyText, TestFlagChange, TestPruneLoaded, TestLoadedMessageState,
// TestLoadableImages, TestRemoteBarState, TestPicturesBarState,
// TestPicturesPolicy, TestRecheckPictures, TestReloadAfterDownload): the
// pure helpers behind the message pane and the actions. The catalogue is
// English in tests, so the msgids come back verbatim.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class ActionHelpersTests
{
    [Fact]
    public void SubjectTextTest()
    {
        Assert.Equal("Hello", LoadedMessageText.SubjectText("  Hello  "));
        Assert.Equal("(No subject)", LoadedMessageText.SubjectText(" \t"));
        // Windows-only (DisplayText, docs/security.md §4): cleaned for the
        // list, the reader, the captions and the questions.
        Assert.Equal(Text.DisplayTextTests.CleanedSubject, LoadedMessageText.SubjectText(Text.DisplayTextTests.HostileSubject));
        Assert.Equal("(No subject)", LoadedMessageText.SubjectText("\u202E\u0007\u2066"));
        // So is one of nothing but characters that draw nothing.
        Assert.Equal("(No subject)", LoadedMessageText.SubjectText("\u200F"));
        Assert.Equal("(No subject)", LoadedMessageText.SubjectText(" \u200E\u061C \u200B\u2060\uFEFF "));
        Assert.Equal("\u200FHi", LoadedMessageText.SubjectText(" \u200FHi "));
    }

    [Fact]
    public void BodyTextTest()
    {
        (string Name, MessageBodyResult? B, string Want)[] cases =
        [
            ("nil", null, "(Empty message)"),
            ("pending", Body(BodyState.Pending), "Downloading…"),
            ("tooBig", Body(BodyState.TooBig, text: "ignored"), "This message is too large to download."),
            ("failed", Body(BodyState.Failed), "This message could not be read."),
            ("fetched", Body(BodyState.Fetched, text: "Hi\n\n"), "Hi"),
            ("empty", Body(BodyState.Fetched, text: " \n"), "(Empty message)"),
            ("tags stay text", Body(BodyState.Fetched, text: "<b>x</b>"), "<b>x</b>"),
            ("crlf", Body(BodyState.Fetched, text: "Hi\r\n"), "Hi"),
        ];
        foreach (var (name, b, want) in cases)
        {
            Assert.True(want == LoadedMessageText.BodyText(b), name);
        }
    }

    [Fact]
    public void FlagChangeTest()
    {
        var change = ActionRules.FlagChange(Flag.Seen, on: true);
        Assert.Equal([Flag.Seen], change.Set!);
        Assert.Null(change.Clear);
        change = ActionRules.FlagChange(Flag.Flagged, on: false);
        Assert.Null(change.Set);
        Assert.Equal([Flag.Flagged], change.Clear!);
    }

    [Fact]
    public void PruneLoaded()
    {
        var entries = new Dictionary<MessageId, LoadedMessage>();
        string[] ids = ["a", "b", "c", "d"];
        for (var i = 0; i < ids.Length; i++)
        {
            entries[new MessageId(ids[i])] = new LoadedMessage { Seq = (ulong)(i + 1) };
        }
        var cache = new LoadedCache(entries);
        cache.Prune(limit: 2, maxBytes: LoadedCache.MaxLoadedBytes);
        Assert.Equal(2, cache.Count);
        Assert.NotNull(cache[new MessageId("c")]);
        Assert.NotNull(cache[new MessageId("d")]);
        cache.Prune(limit: 2, maxBytes: LoadedCache.MaxLoadedBytes); // no-op at the limit
        Assert.Equal(2, cache.Count);

        // Bodies count too: big HTML evicts older entries before the count
        // limit, but the newest entry always stays, however big.
        static LoadedMessage Big(ulong seq, int n) => new() { Body = Body(html: new string('x', n)), Seq = seq };
        cache = new LoadedCache(new Dictionary<MessageId, LoadedMessage>
        {
            [new MessageId("a")] = Big(1, 600),
            [new MessageId("b")] = Big(2, 600),
            [new MessageId("c")] = Big(3, 600),
        });
        cache.Prune(limit: 10, maxBytes: 1000);
        Assert.Equal(1, cache.Count);
        Assert.NotNull(cache[new MessageId("c")]); // byte cap should keep only the newest
        cache = new LoadedCache(new Dictionary<MessageId, LoadedMessage> { [new MessageId("a")] = Big(1, 5000) });
        cache.Prune(limit: 10, maxBytes: 1000);
        Assert.Equal(1, cache.Count); // the newest entry must survive the byte cap

        // The cache numbers what it stores and prunes as it goes.
        cache = new LoadedCache();
        foreach (var id in new[] { "p", "q", "r" })
        {
            _ = cache.LoadedFor(new MessageId(id));
        }
        Assert.Equal(3, cache.Count);
        Assert.True(cache[new MessageId("p")]!.Seq < cache[new MessageId("r")]!.Seq);
        var hoisted1 = cache.LoadedFor(new MessageId("p"));
        Assert.Same(cache[new MessageId("p")], hoisted1);
        cache.Prune(limit: 1);
        Assert.Equal(1, cache.Count);
        Assert.NotNull(cache[new MessageId("r")]);
    }

    // Storing beyond the cap evicts the oldest at once (storeLoaded), and a
    // stored entry is numbered after the entries the cache started with.
    [Fact]
    public void StoreEvictsBeyondTheCaps()
    {
        var cache = new LoadedCache(new Dictionary<MessageId, LoadedMessage> { [new MessageId("old")] = new() { Seq = 41 } });
        var lm = new LoadedMessage();
        cache.Store(new MessageId("new"), lm);
        Assert.Equal(42UL, lm.Seq);
        for (var i = 0; i < LoadedCache.MaxLoaded; i++)
        {
            _ = cache.LoadedFor(new MessageId(string.Create(CultureInfo.InvariantCulture, $"m{i}")));
        }
        Assert.Equal(LoadedCache.MaxLoaded, cache.Count);
        Assert.Null(cache[new MessageId("old")]);
        Assert.Null(cache[new MessageId("new")]);
        cache.Remove(new MessageId("m0"));
        Assert.Equal(LoadedCache.MaxLoaded - 1, cache.Count);
        cache.RemoveAll();
        Assert.Equal(0, cache.Count);
    }

    [Fact]
    public void LoadedMessageState()
    {
        var lm = new LoadedMessage();
        Assert.False(lm.Complete);
        Assert.False(lm.BodySettled);
        lm.Err = new InvalidOperationException("test");
        Assert.True(lm.BodySettled, "body error must settle the body");
        Assert.False(lm.Complete, "body error must not complete the entry");
        lm.Err = null;
        lm.Body = Body();
        lm.Msg = new Message { Summary = Summary("m") };
        Assert.True(lm.Complete);
        Assert.Equal(0, lm.Size);
        lm.Body = Body(html: "<p>hi</p>", text: "hi");
        Assert.Equal(11, lm.Size);
        // The size is in UTF-8 bytes, as Go's len.
        lm.Body = Body(text: "ž");
        Assert.Equal(2, lm.Size);
    }

    [Fact]
    public void LoadableImagesTest()
    {
        var html = Body(html: "<p>x</p>", remoteImages: 3);
        (string Name, MessageBodyResult? B, int Want)[] cases =
        [
            ("nil body", null, 0),
            ("blocked html", html, 3),
            // Already allowed: the daemon fetched what it could, and what the
            // counter still holds no button can bring back.
            ("allowed html", Body(html: "<p>x</p>", remoteImages: 3, policy: RemoteContentPolicy.Allow), 0),
            ("text only", Body(remoteImages: 2), 0),
            ("nothing blocked", Body(html: "<p>x</p>"), 0),
        ];
        foreach (var (name, b, want) in cases)
        {
            Assert.True(want == RemoteBar.LoadableImages(b), name);
        }
    }

    // The bar shows the wait from the click until the daemon answers,
    // whatever the body says meanwhile.
    [Fact]
    public void RemoteBarStateTest()
    {
        var blocked = Body(html: "<p>x</p>", remoteImages: 2);
        var allowed = Body(html: "<p>x</p>", remoteImages: 2, policy: RemoteContentPolicy.Allow);
        (string Name, LoadedMessage? Lm, RemoteBarState Want)[] cases =
        [
            ("nothing loaded", null, new RemoteBarState()),
            ("body on its way", new LoadedMessage(), new RemoteBarState()),
            ("blocked", new LoadedMessage { Body = blocked }, new RemoteBarState(Visible: true, Blocked: 2)),
            ("loading", new LoadedMessage { Body = blocked, LoadingImages = true }, new RemoteBarState(Visible: true, Loading: true)),
            ("loading before the body", new LoadedMessage { LoadingImages = true }, new RemoteBarState(Visible: true, Loading: true)),
            ("images in", new LoadedMessage { Body = allowed }, new RemoteBarState()),
        ];
        foreach (var (name, lm, want) in cases)
        {
            Assert.True(want == RemoteBar.RemoteBarStateFor(lm), name);
        }
        Assert.Equal("B", RemoteBar.LinkTextFor("https://b", [Link("A", "https://a"), Link("B", "https://b")]));
        Assert.Equal("", RemoteBar.LinkTextFor("https://c", []));
    }

    /// <summary>
    /// The pictures bar counts the pictures of an HTML body kept on the mail
    /// server only, and shows the wait from the click until the body was
    /// asked for again (remote.go <c>picturesBarStateFor</c>,
    /// <c>remotePictures</c>); it stands beside the remote-image bar, not
    /// instead of it.
    /// </summary>
    [Fact]
    public void PicturesBarStateTest()
    {
        const string Html = "<p><img src=\"malachi-cid:a/m/2\"></p>";
        Assert.Equal(0, RemoteBar.RemotePictures(null));
        Assert.Equal(0, RemoteBar.RemotePictures(Body(html: Html)));
        Assert.Equal(3, RemoteBar.RemotePictures(Body(html: Html, remotePictures: 3)));
        Assert.Equal(0, RemoteBar.RemotePictures(Body(html: Html, remotePictures: -1)));
        Assert.Equal(0, RemoteBar.RemotePictures(Body(BodyState.Pending, html: Html, remotePictures: 3)));
        Assert.Equal(0, RemoteBar.RemotePictures(Body(html: null, remotePictures: 3)));
        Assert.Equal(0, RemoteBar.RemotePictures(Body(html: "", withheld: true, remotePictures: 3)));

        var server = Body(html: Html, remotePictures: 2);
        var local = Body(html: Html);
        var withBlocked = Body(html: Html, remoteImages: 3, remotePictures: 2);
        (string Name, LoadedMessage? Lm, PicturesBarState Want)[] cases =
        [
            ("nothing loaded", null, new PicturesBarState()),
            ("body on its way", new LoadedMessage(), new PicturesBarState()),
            ("on the server", new LoadedMessage { Body = server }, new PicturesBarState(Visible: true, Remote: 2)),
            ("with remote images blocked too", new LoadedMessage { Body = withBlocked }, new PicturesBarState(Visible: true, Remote: 2)),
            ("downloading", new LoadedMessage { Body = server, LoadingPictures = true }, new PicturesBarState(Visible: true, Loading: true)),
            ("all here", new LoadedMessage { Body = local }, new PicturesBarState()),
            ("remote images loading", new LoadedMessage { Body = server, LoadingImages = true }, new PicturesBarState(Visible: true, Remote: 2)),
        ];
        foreach (var (name, lm, want) in cases)
        {
            Assert.True(want == RemoteBar.PicturesBarStateFor(lm), name);
        }
        // The remote-image bar is unaffected by the pictures.
        Assert.Equal(
            new RemoteBarState(Visible: true, Blocked: 3),
            RemoteBar.RemoteBarStateFor(new LoadedMessage { Body = withBlocked, LoadingPictures = true }));
    }

    /// <summary>
    /// After the download the body is asked for under allow when the remote
    /// images are shown or on their way, so they stay (remote.go
    /// <c>picturesPolicy</c>).
    /// </summary>
    [Fact]
    public void PicturesPolicyTest()
    {
        const string Html = "<p>x</p>";
        Assert.Null(RemoteBar.PicturesPolicy(new LoadedMessage()));
        Assert.Null(RemoteBar.PicturesPolicy(new LoadedMessage { Body = Body(html: Html, remoteImages: 2) }));
        Assert.Equal(RemoteContentPolicy.Allow, RemoteBar.PicturesPolicy(new LoadedMessage { Body = Body(html: Html, policy: RemoteContentPolicy.Allow) })?.Value);
        Assert.Equal(
            RemoteContentPolicy.Allow,
            RemoteBar.PicturesPolicy(new LoadedMessage { Body = Body(html: Html, remoteImages: 2), LoadingImages = true })?.Value);
    }

    /// <summary>
    /// A picture the daemon no longer serves asks for the body again only
    /// when the cached body lists it and counts none on the server, nothing
    /// newer is on its way, and it has not asked since the last download
    /// (remote.go <c>recheckPictures</c>).
    /// </summary>
    [Fact]
    public void RecheckPicturesTest()
    {
        var parts = new Dictionary<string, string> { ["p@x"] = "1.2", ["q@x"] = "1.3" };
        var listed = Body(html: "<p>x</p>") with { InlineParts = parts };
        var counted = listed with { RemotePictures = 1 };
        var text = Body(html: null, text: "x") with { InlineParts = parts };
        (string Name, LoadedMessage? Lm, string Part, bool Want)[] cases =
        [
            ("not cached", null, "1.2", false),
            ("no body", new LoadedMessage(), "1.2", false),
            ("listed, none counted", new LoadedMessage { Body = listed }, "1.2", true),
            ("another listed one", new LoadedMessage { Body = listed }, "1.3", true),
            ("not a picture of the body", new LoadedMessage { Body = listed }, "2", false),
            ("asked already", new LoadedMessage { Body = listed, PicturesRechecked = true }, "1.2", false),
            ("the bar is up already", new LoadedMessage { Body = counted }, "1.2", false),
            ("body on its way", new LoadedMessage { Body = listed, Fetching = true }, "1.2", false),
            ("remote images on their way", new LoadedMessage { Body = listed, LoadingImages = true }, "1.2", false),
            ("pictures on their way", new LoadedMessage { Body = listed, LoadingPictures = true }, "1.2", false),
            ("text shown", new LoadedMessage { Body = text }, "1.2", false),
        ];
        foreach (var (name, lm, part, want) in cases)
        {
            Assert.True(want == RemoteBar.RecheckPictures(lm, part), name);
        }
    }

    /// <summary>
    /// A download asks for the body again when it counts pictures on the
    /// server, unless Download Pictures does that itself or a body is on its
    /// way (remote.go <c>reloadAfterDownload</c>).
    /// </summary>
    [Fact]
    public void ReloadAfterDownloadTest()
    {
        var counted = Body(html: "<p>x</p>", remotePictures: 2);
        (string Name, LoadedMessage? Lm, bool Want)[] cases =
        [
            ("not cached", null, false),
            ("no body", new LoadedMessage(), false),
            ("pictures on the server", new LoadedMessage { Body = counted }, true),
            ("remote images on their way", new LoadedMessage { Body = counted, LoadingImages = true }, true),
            ("none on the server", new LoadedMessage { Body = Body(html: "<p>x</p>") }, false),
            ("Download Pictures asks itself", new LoadedMessage { Body = counted, LoadingPictures = true }, false),
            ("body on its way", new LoadedMessage { Body = counted, Fetching = true }, false),
            ("text shown", new LoadedMessage { Body = Body(html: null, text: "x", remotePictures: 2) }, false),
        ];
        foreach (var (name, lm, want) in cases)
        {
            Assert.True(want == RemoteBar.ReloadAfterDownload(lm), name);
        }
    }

    // The sensitivity rule of setMessageActionsSensitive as a pure function
    // (no Go counterpart; the GTK test is manual) over a model whose account
    // "a" (of a daemon before capabilities: the mail default) has an inbox,
    // an archive and an outbox, no junk folder.
    [Fact]
    public void MessageActionStateTest()
    {
        static Folder F(string id, FolderRole role) => new()
        {
            Id = id,
            AccountId = "a",
            Name = id,
            Path = id,
            Role = role,
            Subscribed = true,
            Selectable = true,
            Synced = true,
            Unread = 0,
            Total = 0,
        };
        var model = new MailModel(
            folders: [new(new AccountId("a"), [F("in", FolderRole.Inbox), F("arch", FolderRole.Archive), F("out", FolderRole.Outbox)])]);
        ActionFlags State(ListRow? row, bool on = true) => ActionRules.MessageActionState(row, model, on);

        var s = Summary("1") with { AccountId = new AccountId("a"), FolderId = new FolderId("in"), Flags = [Flag.Seen] };
        var row = Row(s);
        Assert.Equal(ActionFlags.None, State(null));
        Assert.Equal(ActionFlags.None, State(row, on: false));

        var st = State(row);
        Assert.True(st.On && st.Reply && st.ReplyAll && st.Forward && st.Trash && st.LoadImages);
        Assert.True(st.Archive, "an archive folder exists");
        Assert.False(st.Junk, "no junk folder");
        Assert.True(!st.MarkRead && st.MarkUnread, "a read message can only be marked unread");
        Assert.True(st.Star && st.ToggleFlag && st.TrustSender);
        Assert.True(!st.Flagged && !st.Outbox);

        // In the archive already: archive is off.
        st = State(Row(s with { FolderId = new FolderId("arch"), Flags = [Flag.Flagged] }));
        Assert.False(st.Archive);
        Assert.True(st.MarkRead && !st.MarkUnread);
        Assert.True(st.Flagged);

        // An outbox message keeps reply, forward and trash (which cancels).
        st = State(Row(s with { FolderId = new FolderId("out"), Flags = [Flag.Flagged] }));
        Assert.True(st.Outbox && st.Reply && st.Forward && st.Trash && st.LoadImages);
        Assert.True(!st.Star && !st.Archive && !st.Junk && !st.MarkRead && !st.MarkUnread && !st.ToggleFlag && !st.TrustSender);
        // So is a message with a delivery state whose folder is unknown.
        st = State(Row(s with { FolderId = new FolderId("gone"), Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } }));
        Assert.True(st.Outbox && !st.Star && !st.Archive);

        // A conversation row: read when every member is, flagged when any is.
        var a2 = Summary("a2") with
        {
            AccountId = new AccountId("a"),
            FolderId = new FolderId("in"),
            ThreadId = new ThreadId("t_a"),
            Flags = [Flag.Seen],
        };
        var thread = new ThreadSummary
        {
            Id = new ThreadId("t_a"),
            AccountId = new AccountId("a"),
            Subject = "s",
            MessageCount = 2,
            UnreadCount = 1,
            LatestDate = DateTimeOffset.MinValue,
            Latest = a2,
            Snippet = "",
            Flags = [Flag.Flagged],
            HasAttachments = false,
        };
        var grouped = new ListRow { Key = new ListKey(Thread: thread.Id), Thread = true, Message = a2, Summary = thread };
        st = State(grouped);
        Assert.True(st.Flagged);
        Assert.True(st.MarkRead && st.MarkUnread, "one unread member of two: both directions apply");
        st = State(grouped with { Summary = thread with { UnreadCount = 0 } });
        Assert.True(!st.MarkRead && st.MarkUnread);
        // A member row is judged by its own flags.
        st = State(grouped with { Thread = false, Member = true });
        Assert.True(!st.Flagged && !st.MarkRead && st.MarkUnread);
    }

    private static MessageBodyResult Body(
        BodyState? state = null, string? html = null, string text = "", bool? withheld = null,
        int remoteImages = 0, RemoteContentPolicy? policy = null, int? remotePictures = null) =>
        new()
        {
            MessageId = new MessageId("m"),
            BodyState = state ?? BodyState.Fetched,
            HasHtml = html is not null,
            Html = html,
            HtmlWithheld = withheld,
            Text = text,
            Blocked = new BlockedContent { RemoteImages = remoteImages },
            RemotePictures = remotePictures,
            RemoteContent = policy ?? RemoteContentPolicy.Block,
            SanitizerVersion = "1",
        };

    private static Link Link(string text, string href) => new() { Text = text, Href = href };

    private static ListRow Row(MessageSummary s) => new() { Key = new ListKey(Message: s.Id), Message = s };

    private static MessageSummary Summary(string id) => new()
    {
        Id = new MessageId(id),
        AccountId = new AccountId("acc"),
        FolderId = new FolderId("f"),
        Subject = "",
        Date = DateTimeOffset.MinValue,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
    };
}
