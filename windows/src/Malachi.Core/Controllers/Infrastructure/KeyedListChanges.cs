// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what KeyedListSync did to a collection
// (docs/windows-port.md §7.5).

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>What <see cref="KeyedListSync"/> did to a collection.</summary>
/// <param name="Inserted">Entries of new keys inserted.</param>
/// <param name="Removed">Entries of keys that are gone removed.</param>
/// <param name="Moved">Entries that stayed but moved.</param>
/// <param name="Updated">
/// Entries that stayed and were updated: every one handed its new item, or
/// every one replaced because its value changed.
/// </param>
public readonly record struct KeyedListChanges(int Inserted, int Removed, int Moved, int Updated)
{
    /// <summary>Whether the collection's entries and their order stayed as they were.</summary>
    public bool KeptStructure => Inserted == 0 && Removed == 0 && Moved == 0;
}
