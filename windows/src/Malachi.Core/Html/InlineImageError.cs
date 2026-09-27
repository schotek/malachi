// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/CIDRegistry.swift
// (InlineImageError); GTK: ui/internal/editor/cid.go (the errors of
// checkInline).

namespace Malachi.Core.Html;

/// <summary>What <see cref="CidRegistry.CheckInline"/> refuses.</summary>
public enum InlineImageError
{
    /// <summary>No bytes.</summary>
    Empty,

    /// <summary>Over <see cref="CidRegistry.MaxCidBytes"/>.</summary>
    TooBig,

    /// <summary>Not an image type the view may render (SVG never).</summary>
    NotAPicture,
}
