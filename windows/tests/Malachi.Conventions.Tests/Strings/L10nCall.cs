// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One use of a msgid in the Windows client's sources (L10n.T/N/C in C#,
// {l:T} in XAML), as the strings check finds it (docs/windows-port.md §9).

namespace Malachi.Conventions.Tests.Strings;

/// <summary>A msgid handed to the translation shim.</summary>
/// <param name="Where">file:line.</param>
/// <param name="Kind">T, N or C.</param>
/// <param name="Context">The context of C; null otherwise.</param>
/// <param name="Msgid">The msgid (the singular of N); null when it is not a literal.</param>
/// <param name="Plural">The plural msgid of N; null otherwise, or when it is not a literal.</param>
/// <param name="WindowsOnly">
/// Marked "Windows-only string": a text with no GTK msgid that still goes
/// through L10n (as macOS's "macOS-only string"); it may be missing from
/// the template.
/// </param>
internal sealed record L10nCall(string Where, char Kind, string? Context, string? Msgid, string? Plural, bool WindowsOnly = false);
