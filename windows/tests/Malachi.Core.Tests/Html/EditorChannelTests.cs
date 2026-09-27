// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of EditorChannel, the bridge half of ComposeEditorView.swift
// (editor.go Load, Flush, onMessage), with the order-independent echo rule
// of docs/windows-port.md §6.5: a compose window (draft.go: save, record,
// editorChanged) is played by Window below, over EditorChannel and
// FlushEcho, and every save must leave the draft clean whichever of the
// flush's two answers comes first.

using System;
using System.Collections.Generic;
using Malachi.Core.Html;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class EditorChannelTests
{
    private static string Changed(long seq, string html) =>
        "{\"type\":\"changed\",\"seq\":" + seq + ",\"html\":\"" + html + "\",\"text\":\"t" + seq + "\"}";

    // WebView2's order (15 of 15 in the spike): the flush's changed, then the
    // ExecuteScriptAsync result. The changed is held until the result is in,
    // so the window records the flushed content before it sees the changed.
    [Fact]
    public void WebView2OrderLeavesTheSavedDraftClean()
    {
        var w = new Window();
        w.Ready();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        Assert.True(w.Dirty);

        var id = w.Save();
        w.Channel.Receive(Changed(2, "<p>a</p>"));
        Assert.Equal(1, w.ChangedCount); // held back
        Assert.Empty(w.Saved);
        w.Channel.Flushed(id, "2");
        Assert.Equal(["<p>a</p>"], w.Saved);
        Assert.False(w.Dirty, "the flush's own changed marked the saved draft dirty");
        Assert.Equal(2, w.ChangedCount);
    }

    // The order macOS assumed: the result first; the waiter waits for its
    // changed.
    [Fact]
    public void ResultFirstLeavesTheSavedDraftClean()
    {
        var w = new Window();
        w.Ready();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        var id = w.Save();
        w.Channel.Flushed(id, "2");
        Assert.Empty(w.Saved);
        w.Channel.Receive(Changed(2, "<p>a</p>"));
        Assert.Equal(["<p>a</p>"], w.Saved);
        Assert.False(w.Dirty);
    }

    // An edit after the save is an edit.
    [Fact]
    public void EditAfterTheSaveIsDirty()
    {
        var w = new Window();
        w.Ready();
        var id = w.Save();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        w.Channel.Flushed(id, "1");
        Assert.False(w.Dirty);
        w.Channel.Receive(Changed(2, "<p>ab</p>"));
        Assert.True(w.Dirty);
    }

    // A debounced changed between the flush's changed and its result: the
    // save reads the newest content, and both held changes are its echo.
    [Fact]
    public void EditBeforeTheResultIsSaved()
    {
        var w = new Window();
        w.Ready();
        var id = w.Save();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        w.Channel.Receive(Changed(2, "<p>ab</p>"));
        w.Channel.Flushed(id, "1");
        Assert.Equal(["<p>ab</p>"], w.Saved);
        Assert.False(w.Dirty);
        Assert.Equal(2, w.ChangedCount);
        Assert.Equal(2, w.Channel.LastSeq);
        Assert.Equal("t2", w.Channel.Text);
    }

    // A failed evaluation resolves its waiter (a save must never hang) and
    // releases the held changes.
    [Fact]
    public void FailedEvaluationResolves()
    {
        var w = new Window();
        w.Ready();
        var id = w.Save();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        w.Channel.Flushed(id, null);
        Assert.Equal(["<p>a</p>"], w.Saved);
        Assert.False(w.Dirty);
        Assert.Equal(1, w.ChangedCount);
        foreach (var bad in new[] { "null", "\"3\"", "", "garbage", "1.5", "{}" })
        {
            var again = w.Save();
            w.Channel.Flushed(again, bad);
        }
        Assert.Equal(7, w.Saved.Count);
    }

    // Two flushes at once: each resolves at its own changed; the held changes
    // wait for the last result.
    [Fact]
    public void TwoFlushes()
    {
        var w = new Window();
        w.Ready();
        var done = new List<string>();
        var first = w.Channel.BeginFlush(() => done.Add("first"))!.Value;
        var second = w.Channel.BeginFlush(() => done.Add("second"))!.Value;
        w.Channel.Receive(Changed(1, "a"));
        w.Channel.Flushed(first, "1");
        Assert.Equal(["first"], done);
        Assert.Equal(0, w.ChangedCount);
        w.Channel.Receive(Changed(2, "a"));
        w.Channel.Flushed(second, "2");
        Assert.Equal(["first", "second"], done);
        Assert.Equal(2, w.ChangedCount);
        // A result of an id that is no longer waiting is ignored.
        w.Channel.Flushed(first, "5");
        Assert.Equal(2, done.Count);
    }

    [Fact]
    public void NotReadyFlushesAtOnce()
    {
        var channel = new EditorChannel();
        var called = 0;
        Assert.Null(channel.BeginFlush(() => called++));
        Assert.Equal(1, called);
        Assert.False(channel.IsReady);
    }

    // editor.Load: the old document's waiters are released, its held changes
    // dropped, the content is what was loaded.
    [Fact]
    public void LoadReleasesWaiters()
    {
        var w = new Window();
        w.Ready();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        var changes = w.ChangedCount;
        w.Save();
        w.Channel.Receive(Changed(2, "<p>b</p>"));
        w.Channel.Load("<p>new</p>");
        Assert.Equal(["<p>new</p>"], w.Saved);
        Assert.False(w.Channel.IsReady);
        Assert.Equal("<p>new</p>", w.Channel.Html);
        Assert.Equal("", w.Channel.Text);
        Assert.Equal(0, w.Channel.LastSeq);
        Assert.Equal(changes, w.ChangedCount);
    }

    // A crash releases the waiters and then the held changes: their content
    // is the latest known.
    [Fact]
    public void CrashReleasesWaitersAndChanges()
    {
        var w = new Window();
        w.Ready();
        w.Save();
        w.Channel.Receive(Changed(1, "<p>a</p>"));
        w.Channel.Crashed();
        Assert.False(w.Channel.IsReady);
        Assert.Equal(["<p>a</p>"], w.Saved);
        Assert.Equal(1, w.ChangedCount);
        Assert.False(w.Dirty);
    }

    [Fact]
    public void MessagesAndEvents()
    {
        var channel = new EditorChannel();
        var events = new List<string>();
        channel.Ready += (_, _) => events.Add("ready");
        channel.Changed += (_, _) => events.Add("changed:" + channel.Html);
        channel.StateChanged += (_, s) => events.Add("state:" + s.Block + (s.Bold ? "+bold" : ""));
        channel.KeyPressed += (_, k) => events.Add("key:" + k);

        Assert.NotNull(channel.Receive("{\"type\":\"ready\"}"));
        Assert.True(channel.IsReady);
        Assert.NotNull(channel.Receive(Changed(1, "x")));
        Assert.NotNull(channel.Receive("{\"type\":\"state\",\"bold\":true,\"block\":\"h1\"}"));
        Assert.NotNull(channel.Receive("{\"type\":\"key\",\"key\":\"escape\"}"));
        var drop = channel.Receive("{\"type\":\"drop\"}");
        Assert.Equal(BridgeMessage.Kinds.Drop, drop?.Type);
        // Unknown kinds and anything that is not a message are dropped.
        foreach (var bad in new[] { "{\"type\":\"brandNew\"}", "{}", "garbage", "[1]", "{\"type\":\"changed\",\"seq\":\"x\"}", null })
        {
            Assert.Null(channel.Receive(bad));
        }
        Assert.Equal(["ready", "changed:x", "state:h1+bold", "key:escape"], events);
    }

    // Windows: a changed with an unpaired surrogate (a plain-text paste that
    // Chromium keeps in the body, posted escaped by JSON.stringify) is the
    // draft: it resolves the flush it answers, and the echo check sees the
    // content the save recorded, U+FFFD included, in either order.
    [Fact]
    public void UnpairedSurrogateResolvesTheFlush()
    {
        const string posted = """{"type":"changed","seq":1,"html":"<p>a\ud800b</p>","text":"a\udc00b"}""";
        var w = new Window();
        w.Ready();
        var id = w.Save();
        Assert.NotNull(w.Channel.Receive(posted));
        w.Channel.Flushed(id, "1");
        Assert.Equal(["<p>a�b</p>"], w.Saved);
        Assert.Equal("a�b", w.Channel.Text);
        Assert.False(w.Dirty, "the flush's own changed marked the saved draft dirty");
        Assert.Equal(1, w.ChangedCount);
        // The debounced changed with the same content is still the echo.
        w.Channel.Receive("""{"type":"changed","seq":2,"html":"<p>a\ud800b</p>"}""");
        Assert.False(w.Dirty);

        var result = new Window();
        result.Ready();
        var again = result.Save();
        result.Channel.Flushed(again, "1");
        result.Channel.Receive(posted);
        Assert.Equal(["<p>a�b</p>"], result.Saved);
        Assert.False(result.Dirty);
    }

    // Windows: nothing the page posts makes Receive throw (it runs in the
    // view's WebMessageReceived handler): every sequence of up to three
    // pieces of JSON, escapes and lone surrogates, alone and as a changed's
    // html.
    [Fact]
    public void ReceiveNeverThrows()
    {
        string[] pieces =
        [
            "{", "}", "[", "]", "\"", ":", ",", "\\", "\\u", "\\ud800", "\\udc00", "\\ud83d\\ude00", "\\\\", "\\\"",
            "type", "changed", "html", "1", "1e999", "null", "true", "\uD800", "\uDC00", "\0", " ",
            "\"type\":\"changed\"", "\"html\":\"", "\"seq\":", new string('[', 100),
        ];
        var channel = new EditorChannel();
        channel.Receive("""{"type":"ready"}""");
        var received = 0;
        foreach (var a in pieces)
        {
            foreach (var b in pieces)
            {
                foreach (var c in pieces)
                {
                    var s = a + b + c;
                    channel.Receive(s);
                    if (channel.Receive("{\"type\":\"changed\",\"seq\":1,\"html\":\"" + s + "\"}") is not null)
                    {
                        received++;
                    }
                }
            }
        }
        Assert.True(received > 0);
        Assert.True(channel.IsReady);
    }

    // Windows: Exec and FocusStart are ignored until the bridge runs in the
    // current document, as GTK and macOS ignore them (editor.go Exec,
    // FocusStart): the view evaluates nothing for a null script.
    [Fact]
    public void ExecAndFocusStartWaitForTheBridge()
    {
        var channel = new EditorChannel();
        Assert.Null(channel.ExecScript("bold", null));
        Assert.Null(channel.FocusStartScript());
        channel.Receive("""{"type":"ready"}""");
        Assert.Equal(EditorBridge.ExecScript("formatBlock", "h1"), channel.ExecScript("formatBlock", "h1"));
        Assert.Equal("window.malachi.exec(\"bold\", null)", channel.ExecScript("bold", ""));
        Assert.Equal(EditorBridge.FocusStartScript, channel.FocusStartScript());
        channel.Load("<p>x</p>");
        Assert.Null(channel.ExecScript("bold", null));
        Assert.Null(channel.FocusStartScript());
        channel.Receive("""{"type":"ready"}""");
        Assert.NotNull(channel.FocusStartScript());
        channel.Crashed();
        Assert.Null(channel.ExecScript("bold", null));
        Assert.Null(channel.FocusStartScript());
        Assert.Throws<ArgumentNullException>(() => channel.ExecScript(null!, null));
    }

    [Theory]
    [InlineData("3", 3L)]
    [InlineData(" 42 ", 42L)]
    [InlineData("3.0", 3L)]
    [InlineData("0", 0L)]
    [InlineData("null", null)]
    [InlineData("\"3\"", null)]
    [InlineData("", null)]
    [InlineData(null, null)]
    [InlineData("1.5", null)]
    [InlineData("1e300", null)]
    [InlineData("garbage", null)]
    public void FlushResults(string? json, long? want)
    {
        Assert.Equal(want, EditorChannel.ParseFlushResult(json));
    }

    // The compose window's half: draft.go save (dirty cleared, the flush's
    // done records what it saves) and editorChanged (an echo is no edit).
    private sealed class Window
    {
        private readonly FlushEcho echo = new();

        public Window()
        {
            Channel.Changed += (_, _) =>
            {
                ChangedCount++;
                if (!echo.Echo(Channel.Html))
                {
                    Dirty = true;
                }
            };
        }

        public EditorChannel Channel { get; } = new();

        public bool Dirty { get; private set; }

        public int ChangedCount { get; private set; }

        public List<string> Saved { get; } = [];

        public void Ready() => Channel.Receive("{\"type\":\"ready\"}");

        public long Save()
        {
            Dirty = false; // edits during the call set it again
            return Channel.BeginFlush(() =>
            {
                echo.Record(Channel.Html);
                Saved.Add(Channel.Html);
            }) ?? throw new InvalidOperationException("the editor is not ready");
        }
    }
}
