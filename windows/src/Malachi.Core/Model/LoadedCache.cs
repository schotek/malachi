// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/LoadedMessage.swift (LoadedCache);
// GTK: ui/internal/window/message_view.go (Window.loaded with storeLoaded,
// loadedFor, pruneLoaded, maxLoaded, maxLoadedBytes). A class where Swift
// has a mutable struct: its only owner is the message cache controller,
// which never takes a snapshot of it.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The cache of loaded messages, bounded by entries and by the size of the
/// bodies in it; the oldest entries are evicted first, the newest never.
/// UI-thread-affine.
/// </summary>
public sealed class LoadedCache
{
    /// <summary>At most this many entries (message_view.go <c>maxLoaded</c>).</summary>
    public const int MaxLoaded = 64;

    /// <summary>
    /// At most this many bytes of bodies (message_view.go
    /// <c>maxLoadedBytes</c>).
    /// </summary>
    public const int MaxLoadedBytes = 32 << 20;

    private readonly Dictionary<MessageId, LoadedMessage> entries;

    // Numbers insertions so Prune can find the oldest (loadedSeq).
    private ulong seq;

    /// <summary>
    /// A cache holding <paramref name="entries"/> as they are, numbering new
    /// insertions after the highest <see cref="LoadedMessage.Seq"/> among
    /// them.
    /// </summary>
    public LoadedCache(IReadOnlyDictionary<MessageId, LoadedMessage>? entries = null)
    {
        this.entries = entries is null ? [] : new Dictionary<MessageId, LoadedMessage>(entries);
        seq = this.entries.Count == 0 ? 0 : this.entries.Values.Max(lm => lm.Seq);
    }

    /// <summary>The entries by message.</summary>
    public IReadOnlyDictionary<MessageId, LoadedMessage> Entries => entries;

    /// <summary>How many entries there are.</summary>
    public int Count => entries.Count;

    /// <summary>The entry of <paramref name="id"/>, null when there is none.</summary>
    public LoadedMessage? this[MessageId id] => entries.GetValueOrDefault(id);

    /// <summary>
    /// The entry of <paramref name="id"/>, created empty when there is none
    /// (<c>loadedFor</c>).
    /// </summary>
    public LoadedMessage LoadedFor(MessageId id)
    {
        if (entries.TryGetValue(id, out var lm))
        {
            return lm;
        }
        lm = new LoadedMessage();
        Store(id, lm);
        return lm;
    }

    /// <summary>
    /// Caches <paramref name="lm"/> for <paramref name="id"/>, evicting the
    /// oldest entries beyond the caps (<c>storeLoaded</c>).
    /// </summary>
    public void Store(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        seq++;
        lm.Seq = seq;
        entries[id] = lm;
        Prune();
    }

    /// <summary>Forgets <paramref name="id"/>.</summary>
    public void Remove(MessageId id) => entries.Remove(id);

    /// <summary>Forgets everything.</summary>
    public void RemoveAll() => entries.Clear();

    /// <summary>
    /// Evicts the entries with the lowest <see cref="LoadedMessage.Seq"/>
    /// until at most <paramref name="limit"/> remain and their bodies fit in
    /// <paramref name="maxBytes"/>; the newest entry always stays
    /// (<c>pruneLoaded</c>).
    /// </summary>
    public void Prune(int limit = MaxLoaded, int maxBytes = MaxLoadedBytes)
    {
        var total = entries.Values.Sum(lm => (long)lm.Size);
        while (entries.Count > 1 && (entries.Count > limit || total > maxBytes))
        {
            var oldest = entries.MinBy(e => e.Value.Seq);
            total -= oldest.Value.Size;
            entries.Remove(oldest.Key);
        }
    }
}
