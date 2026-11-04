// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §10 'Attachments'): how many shell
// icons the attachment chips of one message may look up. GTK asks GIO for a
// symbolic icon by content type and macOS NSWorkspace for the type's icon,
// both cheap; the chip's icon on Windows comes from the shell
// (ShellFileTypes: SHGetFileInfo and a drawn bitmap, on the UI thread, since
// the image lists are the shell's COM objects), so a message is hostile
// input that could list hundreds of parts with as many different extensions
// and stall its first render. One set of chips (a Batch) looks up at most
// PerBatch extensions it has not seen; the rest get the generic glyph, and
// are not remembered, so a later set may still look them up. Every answer,
// an icon or none, is remembered per extension, at most Capacity of them:
// when full, the memory starts over. UI-thread-affine.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Presentation;

/// <summary>The icons of file types by extension, looked up a limited number at a time.</summary>
/// <typeparam name="T">The icon.</typeparam>
public sealed class IconLookups<T>
    where T : class
{
    /// <summary>How many extensions one set of chips may look up.</summary>
    public const int DefaultPerBatch = 24;

    /// <summary>How many extensions are remembered.</summary>
    public const int DefaultCapacity = 256;

    private readonly Func<string, T?> lookup;
    private readonly int perBatch;
    private readonly int capacity;
    private readonly Dictionary<string, T?> known = new(StringComparer.Ordinal);

    /// <summary>Icons that <paramref name="lookup"/> finds by extension (null for none).</summary>
    /// <param name="lookup">The lookup of one extension, with its dot, in lower case.</param>
    /// <param name="perBatch">How many extensions one batch may look up.</param>
    /// <param name="capacity">How many answers are remembered.</param>
    public IconLookups(Func<string, T?> lookup, int perBatch = DefaultPerBatch, int capacity = DefaultCapacity)
    {
        ArgumentNullException.ThrowIfNull(lookup);
        ArgumentOutOfRangeException.ThrowIfNegative(perBatch);
        ArgumentOutOfRangeException.ThrowIfNegativeOrZero(capacity);
        this.lookup = lookup;
        this.perBatch = perBatch;
        this.capacity = capacity;
    }

    /// <summary>How many answers are remembered now.</summary>
    public int Count => known.Count;

    /// <summary>A new set of chips, with its own allowance of lookups.</summary>
    public Batch StartBatch() => new(this, perBatch);

    private bool TryKnown(string extension, out T? icon) => known.TryGetValue(extension, out icon);

    private T? LookUp(string extension)
    {
        var icon = lookup(extension);
        if (known.Count >= capacity)
        {
            known.Clear();
        }
        known[extension] = icon;
        return icon;
    }

    /// <summary>The lookups of one set of chips.</summary>
    public sealed class Batch
    {
        private readonly IconLookups<T> owner;
        private int left;

        internal Batch(IconLookups<T> owner, int left)
        {
            this.owner = owner;
            this.left = left;
        }

        /// <summary>
        /// The icon of <paramref name="extension"/> (with its dot, in lower
        /// case; "" for none): a remembered one, or looked up while the
        /// batch's allowance lasts; null for the generic glyph.
        /// </summary>
        public T? For(string extension)
        {
            ArgumentNullException.ThrowIfNull(extension);
            if (extension.Length == 0)
            {
                return null;
            }
            if (owner.TryKnown(extension, out var icon))
            {
                return icon;
            }
            if (left <= 0)
            {
                return null;
            }
            left--;
            return owner.LookUp(extension);
        }
    }
}
