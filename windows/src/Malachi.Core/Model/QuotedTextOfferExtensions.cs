// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/LoadedMessage.swift
// (QuotedTextOffer.label); GTK: ui/internal/conversation/quoted.go
// (QuotedTextLabel).

namespace Malachi.Core.Model;

/// <summary>Swift's computed properties of <see cref="QuotedTextOffer"/>.</summary>
public static class QuotedTextOfferExtensions
{
    extension(QuotedTextOffer offer)
    {
        /// <summary>The button's tooltip and accessible name (conversation.QuotedTextLabel).</summary>
        public string Label => Conversation.QuotedTextLabel(shown: offer == QuotedTextOffer.Hide);
    }
}
