// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageList/AvatarView.swift
// (AvatarPalette.Colours); GTK: libadwaita _avatar.scss $avatar_colors.

namespace Malachi.Core.Presentation;

/// <summary>
/// One colour class of Adw.Avatar, each colour as 0xRRGGBB: the initials,
/// and the gradient from top to bottom of the disc.
/// </summary>
/// <param name="Foreground">The initials.</param>
/// <param name="Top">The gradient's top.</param>
/// <param name="Bottom">The gradient's bottom.</param>
public readonly record struct AvatarColours(uint Foreground, uint Top, uint Bottom);
