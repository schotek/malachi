// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the bridge half of macos/Sources/MalachiMail/Compose/
// ComposeEditorView.swift (load, flush, receive, flushed, resolveWaiters,
// drainWaiters, crashed); GTK: ui/internal/editor/editor.go (Load, Flush,
// onMessage).
//
// What the compose editor view does with the bridge, without the WebView2:
// the last content the page reported, the ready state, and the flush
// protocol. A flush evaluates window.malachi.flush(), which posts a changed
// and returns its seq; a waiter is resolved once the changed with that seq
// (or a later one) has arrived, as on macOS (GTK, blind to the returned seq,
// settles for the next changed). WebView2 delivers the changed before the
// ExecuteScriptAsync result (15 of 15 in the spike), the order in which
// macOS's echo check fails: the compose window would see the flush's own
// changed before it recorded what the flush saved, and mark every saved
// draft dirty again. So a changed that arrives while a flush's result is
// outstanding is held back: its content is taken at once and due waiters
// are resolved (their done records the flushed HTML in the window's
// FlushEcho), and Changed is raised for it only once no flush result is
// outstanding. The echo check then sees the record in either order, which
// is GTK's semantics (the record happens before OnChanged of the same
// message) with macOS's seq precision. UI-thread-affine (§7.1): the view
// calls it from the WebView2's events.

using System;
using System.Collections.Generic;
using System.Text.Json;

namespace Malachi.Core.Html;

/// <summary>The compose editor's side of the bridge: content, readiness and flushes.</summary>
public sealed class EditorChannel
{
    private readonly List<Waiter> waiters = [];
    private long nextWaiterId;
    private int heldChanges;

    /// <summary>Fires once the bridge runs in a document the view loaded.</summary>
    public event EventHandler? Ready;

    /// <summary>
    /// Fires after the page reported new content (debounced, or a flush):
    /// <see cref="Html"/> and <see cref="Text"/> hold it. Raised only while no
    /// flush result is outstanding, after the waiters it resolved.
    /// </summary>
    public event EventHandler? Changed;

    /// <summary>Fires when the formatting at the caret changed.</summary>
    public event EventHandler<EditorState>? StateChanged;

    /// <summary>Fires for Escape (<c>escape</c>) and Ctrl+K (<c>link</c>) in the page.</summary>
    public event EventHandler<string>? KeyPressed;

    /// <summary>editor.Ready: whether the bridge runs in the current document.</summary>
    public bool IsReady { get; private set; }

    /// <summary>editor.HTML: the body's last known innerHTML (or what was loaded).</summary>
    public string Html { get; private set; } = "";

    /// <summary>editor.Text: the body's last known innerText.</summary>
    public string Text { get; private set; } = "";

    /// <summary>The <c>seq</c> of the last <c>changed</c> of the current document.</summary>
    public long LastSeq { get; private set; }

    /// <summary>
    /// editor.Load: a new document with <paramref name="bodyHtml"/> (already
    /// safe for the page) is being loaded; the view navigates to
    /// <see cref="EditorDocument.Document"/> of it. Callers waiting on a flush
    /// of the old document are released, as they would be were the page not
    /// ready, and its held changes are dropped.
    /// </summary>
    public void Load(string bodyHtml)
    {
        ArgumentNullException.ThrowIfNull(bodyHtml);
        IsReady = false;
        Html = bodyHtml;
        Text = "";
        LastSeq = 0;
        heldChanges = 0;
        Drain();
    }

    /// <summary>
    /// editor.onMessage: one message the page posted (the view has checked
    /// that it came from the current document). Anything that is not a
    /// string holding a message of a known kind is dropped (null); the view
    /// logs that without the content. Returns the message, so the view can
    /// take a <c>drop</c>'s files from the event.
    /// </summary>
    public BridgeMessage? Receive(string? raw)
    {
        var message = BridgeMessage.TryDecode(raw);
        switch (message?.Type)
        {
            case BridgeMessage.Kinds.Ready:
                IsReady = true;
                Ready?.Invoke(this, EventArgs.Empty);
                return message;
            case BridgeMessage.Kinds.Changed:
                Html = message.Html;
                Text = message.Text;
                LastSeq = message.Seq;
                if (Outstanding())
                {
                    heldChanges++;
                    Resolve();
                }
                else
                {
                    Resolve();
                    Changed?.Invoke(this, EventArgs.Empty);
                }
                return message;
            case BridgeMessage.Kinds.State:
                StateChanged?.Invoke(this, message.State);
                return message;
            case BridgeMessage.Kinds.Key:
                KeyPressed?.Invoke(this, message.Key);
                return message;
            case BridgeMessage.Kinds.Drop:
                return message;
            default:
                return null;
        }
    }

    /// <summary>
    /// editor.Flush: asks for the page's current content. Returns null after
    /// calling <paramref name="done"/> at once when the bridge is not
    /// running; otherwise the flush's id: the view evaluates
    /// <see cref="EditorBridge.FlushScript"/> and hands its result to
    /// <see cref="Flushed"/>, and <paramref name="done"/> runs once the
    /// <c>changed</c> it produced has arrived. A failed evaluation calls it
    /// too: a save must never hang.
    /// </summary>
    public long? BeginFlush(Action done)
    {
        ArgumentNullException.ThrowIfNull(done);
        if (!IsReady)
        {
            done();
            return null;
        }
        var id = nextWaiterId++;
        waiters.Add(new Waiter(id, done));
        return id;
    }

    /// <summary>
    /// The page's <c>flush()</c> returned: <paramref name="scriptResult"/> is
    /// the JSON of <c>ExecuteScriptAsync</c> (the <c>seq</c>), null when the
    /// evaluation failed. An id that is no longer waiting (a new document, a
    /// crash) is ignored.
    /// </summary>
    public void Flushed(long id, string? scriptResult)
    {
        var index = waiters.FindIndex(w => w.Id == id);
        if (index < 0)
        {
            return;
        }
        if (ParseFlushResult(scriptResult) is { } seq)
        {
            waiters[index].Seq = seq;
            Resolve();
        }
        else
        {
            var waiter = waiters[index];
            waiters.RemoveAt(index);
            waiter.Done();
        }
        Release();
    }

    /// <summary>
    /// The page's process died, or its document was refused; the view is
    /// blank until <see cref="Load"/> is called again (the compose window
    /// reloads <see cref="Html"/>). Waiters are released; held changes are
    /// raised after them, their content is the latest known.
    /// </summary>
    public void Crashed()
    {
        IsReady = false;
        Drain();
        Release();
    }

    /// <summary>
    /// The <c>seq</c> in the JSON result of the flush script: an integer
    /// number (a whole double as well, as NSNumber reads it); null for
    /// anything else.
    /// </summary>
    public static long? ParseFlushResult(string? json)
    {
        if (string.IsNullOrWhiteSpace(json))
        {
            return null;
        }
        try
        {
            using var document = JsonDocument.Parse(json);
            var root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Number)
            {
                return null;
            }
            if (root.TryGetInt64(out var n))
            {
                return n;
            }
            return root.TryGetDouble(out var d) && Math.Floor(d) == d && Math.Abs(d) < 9.007199254740992E15
                ? (long)d
                : null;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    // A flush result is outstanding: some waiter does not know its seq yet.
    private bool Outstanding() => waiters.Exists(w => w.Seq is null);

    // Calls every waiter whose changed has arrived, in order.
    private void Resolve()
    {
        var due = waiters.FindAll(w => w.Seq is { } seq && seq <= LastSeq);
        if (due.Count == 0)
        {
            return;
        }
        waiters.RemoveAll(due.Contains);
        foreach (var waiter in due)
        {
            waiter.Done();
        }
    }

    // Raises Changed for the held changes once no flush result is
    // outstanding.
    private void Release()
    {
        while (heldChanges > 0 && !Outstanding())
        {
            heldChanges--;
            Changed?.Invoke(this, EventArgs.Empty);
        }
    }

    // Releases every waiter: the document they asked about is gone.
    private void Drain()
    {
        var all = waiters.ToArray();
        waiters.Clear();
        foreach (var waiter in all)
        {
            waiter.Done();
        }
    }

    private sealed class Waiter(long id, Action done)
    {
        public long Id { get; } = id;

        public Action Done { get; } = done;

        // What the page's flush() returned; null until the evaluation came
        // back.
        public long? Seq { get; set; }
    }
}
