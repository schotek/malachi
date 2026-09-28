// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageList/MessageCellView.swift
// (RowAppearance); GTK: ui/internal/window/messages.go
// (applyRowAppearance: density, the preview line, which a search result
// always shows, and the avatars), threads.go (SetReserveExpander on the
// plain rows of a grouped list) and ui/internal/style (monochrome-avatars).

using Malachi.Core.Settings;

namespace Malachi.Core.Presentation;

/// <summary>How every row of the message list looks, from the settings and the listing.</summary>
/// <param name="Compact">message-list-density is compact.</param>
/// <param name="ShowPreview">show-preview-line, or a search on (an excerpt says why a result was found).</param>
/// <param name="ShowAvatars">show-avatars.</param>
/// <param name="Monochrome">monochrome-avatars.</param>
/// <param name="Grouped">The list is grouped by conversation: a plain row keeps the fold arrow's place.</param>
public readonly record struct RowAppearance(bool Compact, bool ShowPreview, bool ShowAvatars, bool Monochrome, bool Grouped)
{
    /// <summary>The appearance for <paramref name="settings"/>, a search on or not, the listing grouped or not.</summary>
    public static RowAppearance From(SettingsStore settings, bool searching, bool grouped)
    {
        System.ArgumentNullException.ThrowIfNull(settings);
        return new RowAppearance(
            settings.Density == Density.Compact,
            settings.ShowPreviewLine || searching,
            settings.ShowAvatars,
            settings.MonochromeAvatars,
            grouped);
    }
}
