// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/LoadedMessage.swift
// (QuotedTextOffer); Go: ui/internal/conversation/quoted.go (QuotedOffer,
// OfferQuoted). Swift-first.

namespace Malachi.Core.Model;

/// <summary>
/// The button under a body that shows or hides the quoted history the daemon
/// cut from it (<see cref="Api.MessageBodyParams.TrimQuoted"/>).
/// </summary>
public enum QuotedTextOffer
{
    /// <summary>The body is trimmed: Show Quoted Text.</summary>
    Show,

    /// <summary>The whole body shows: Hide Quoted Text.</summary>
    Hide,
}
