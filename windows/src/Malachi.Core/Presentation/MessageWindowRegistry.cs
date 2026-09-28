// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Windows/MessageWindows.swift (track,
// openMessage, closeMessageWindow, openEmbedded, showLoaded,
// refreshRemoteBar, showOutboxState, refreshStars) and WindowRegistry.swift
// (register, present, close, closeAll, unregister); GTK:
// ui/internal/window/window.go (openMessages, openEmbedded),
// message_view.go (openMessageWindow, closeMessageWindow), embedded.go
// (openEmbeddedWindow, closeEmbeddedWindows), remote.go (showLoaded,
// refreshRemoteBar), outbox.go (showOutboxState) and actions.go
// (refreshStars, refreshSeen). One window per message, one per attached
// message, and the fan-out of what the cache and the actions learn to every
// view showing a message (the pane's included); a view of an attached
// message carries the containing message's id and is left out of the
// fan-out. The windows themselves are the app's (MakeMessageWindow,
// MakeEmbeddedWindow), which report their close (Closed). UI-thread-affine.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>The message windows of the application and the views that show messages.</summary>
public sealed partial class MessageWindowRegistry
{
    private readonly IReaderCache cache;
    private readonly ILogger logger;
    private readonly Dictionary<MessageWindowKey, IMessageWindowHandle> windows = [];
    private readonly List<ReaderController> readers = [];

    /// <param name="cache">The loaded messages (<c>message.embedded</c>, the outbox state).</param>
    /// <param name="logger">Kinds and codes only.</param>
    public MessageWindowRegistry(IReaderCache cache, ILogger<MessageWindowRegistry>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(cache);
        this.cache = cache;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>Makes (and does not show) the window of a message (message_window.go <c>newMessageWindow</c>).</summary>
    public Func<MessageSummary, IMessageWindowHandle>? MakeMessageWindow { get; set; }

    /// <summary>
    /// Makes (and does not show) the window of the attached message
    /// <c>part</c> of <c>containing</c> with what <c>message.embedded</c>
    /// answered (embedded.go <c>newEmbeddedWindow</c>).
    /// </summary>
    public Func<MessageSummary, string, MessageEmbeddedResult, IMessageWindowHandle>? MakeEmbeddedWindow { get; set; }

    /// <summary>A toast in the window where a chip was clicked (a failed <c>message.embedded</c>).</summary>
    public Action<object?, string>? Toast { get; set; }

    /// <summary>The views that show messages now, the pane's first.</summary>
    public IReadOnlyList<ReaderController> Readers => readers;

    /// <summary>The keys of the open windows.</summary>
    public IReadOnlyCollection<MessageWindowKey> Keys => windows.Keys;

    /// <summary>The window of <paramref name="key"/>, if one is open.</summary>
    public IMessageWindowHandle? Window(MessageWindowKey key) => windows.GetValueOrDefault(key);

    // Views

    /// <summary>Registers a view for the fan-out (MessageWindows.swift <c>track</c>).</summary>
    public void Track(ReaderController reader)
    {
        ArgumentNullException.ThrowIfNull(reader);
        if (!readers.Contains(reader))
        {
            readers.Add(reader);
        }
    }

    /// <summary>Forgets a view (its window closed).</summary>
    public void Untrack(ReaderController reader) => readers.Remove(reader);

    // Windows

    /// <summary>Brings the window of <paramref name="key"/> to the front; false when there is none.</summary>
    public bool Present(MessageWindowKey key)
    {
        ArgumentNullException.ThrowIfNull(key);
        if (!windows.TryGetValue(key, out var w))
        {
            return false;
        }
        w.Present();
        return true;
    }

    /// <summary>
    /// Registers <paramref name="window"/> under <paramref name="key"/> and
    /// tracks its view; a window already there is replaced (and left open).
    /// </summary>
    public void Register(MessageWindowKey key, IMessageWindowHandle window)
    {
        ArgumentNullException.ThrowIfNull(key);
        ArgumentNullException.ThrowIfNull(window);
        windows[key] = window;
        Track(window.Reader);
    }

    /// <summary>
    /// The window of <paramref name="key"/> closed (its <c>close-request</c>):
    /// it is forgotten, and its view with it, unless another window took its
    /// key meanwhile.
    /// </summary>
    public void Closed(MessageWindowKey key, IMessageWindowHandle window)
    {
        ArgumentNullException.ThrowIfNull(key);
        ArgumentNullException.ThrowIfNull(window);
        if (windows.TryGetValue(key, out var w) && ReferenceEquals(w, window))
        {
            windows.Remove(key);
        }
        Untrack(window.Reader);
    }

    /// <summary>
    /// Opens message <paramref name="s"/> in its own window, or raises the
    /// window that already shows it (message_view.go <c>openMessageWindow</c>).
    /// </summary>
    public void OpenMessage(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var key = new MessageWindowKey.Message(s.Id);
        if (Present(key) || MakeMessageWindow is not { } make)
        {
            return;
        }
        var w = make(s);
        Register(key, w);
        // What the cache holds already, then the window (newMessageWindow's
        // show before Present); the rest arrives through the fan-out.
        w.Reader.Show(s);
        w.Present();
    }

    /// <summary>
    /// Shows the attached message <paramref name="part"/> of
    /// <paramref name="containing"/> in its own window, or raises the window
    /// already showing it (embedded.go <c>openEmbeddedWindow</c>). Nothing
    /// changes on screen while the daemon renders; a failure is a toast in
    /// <paramref name="chipWindow"/>, where the chip is.
    /// </summary>
    public async Task OpenEmbeddedAsync(MessageSummary containing, string part, object? chipWindow)
    {
        ArgumentNullException.ThrowIfNull(containing);
        ArgumentNullException.ThrowIfNull(part);
        var key = new MessageWindowKey.Embedded(containing.Id, part);
        if (Present(key))
        {
            return;
        }
        MessageEmbeddedResult result;
        try
        {
            result = await cache.FetchEmbeddedAsync(containing.AccountId, containing.Id, part);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogEmbeddedFailed(logger, part, e.GetType().Name);
            Toast?.Invoke(chipWindow, RpcErrorText.Text(L10n.T("Opening the attached message"), e));
            return;
        }
        if (Present(key) || MakeEmbeddedWindow is not { } make)
        {
            return; // a second click overtook the first
        }
        var w = make(containing, part, result);
        Register(key, w);
        w.Present();
    }

    /// <summary>
    /// Closes the window of message <paramref name="id"/>, if any, and the
    /// windows of the messages attached to it (the message left the folder;
    /// message_view.go <c>closeMessageWindow</c>).
    /// </summary>
    public void CloseMessage(MessageId id)
    {
        foreach (var key in windows.Keys.Where(k => k.Id == id).ToList())
        {
            if (windows.Remove(key, out var w))
            {
                Untrack(w.Reader);
                w.Close();
            }
        }
    }

    // Fan-out

    /// <summary>
    /// Re-renders message <paramref name="id"/> wherever it is on display
    /// (remote.go <c>showLoaded</c>, for every settle of the cache).
    /// </summary>
    public void ShowLoaded(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        foreach (var v in Showing(id))
        {
            v.Render(v.Current!, lm);
        }
    }

    /// <summary>
    /// Redraws the remote-image bar of message <paramref name="id"/> wherever
    /// it is on display and leaves the body alone (remote.go
    /// <c>refreshRemoteBar</c>).
    /// </summary>
    public void RefreshRemoteBar(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        foreach (var v in Showing(id))
        {
            v.RefreshRemoteBar(lm);
        }
    }

    /// <summary>
    /// Pushes the cached delivery state of <paramref name="id"/> to the
    /// banners showing it (outbox.go <c>showOutboxState</c>).
    /// </summary>
    public void ShowOutboxState(MessageId id)
    {
        var m = cache.Loaded(id)?.Msg;
        foreach (var v in Showing(id))
        {
            v.RenderOutboxBanner(m);
        }
    }

    /// <summary>
    /// The flags of messages changed: every message window's commands follow
    /// (actions.go <c>refreshStars</c>, <c>refreshSeen</c>).
    /// </summary>
    public void RefreshActions()
    {
        foreach (var w in windows.Values.ToList())
        {
            w.RefreshActions();
        }
    }

    // The views showing message id, attached messages' views left out: they
    // carry the containing message's id.
    private List<ReaderController> Showing(MessageId id) =>
        [.. readers.Where(v => v.Mode != ReaderMode.Embedded && v.Current is { } s && s.Id == id)];

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.embedded {Part} failed: {Kind}")]
    private static partial void LogEmbeddedFailed(ILogger logger, string part, string kind);
}
