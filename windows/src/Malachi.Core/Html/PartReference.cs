// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the (accountID, messageID, partID) tuple of
// macos/Sources/MalachiCore/HTML/PartPath.swift (parsePartPath); GTK:
// ui/internal/htmlview/links.go (ParsePath's results).

using Malachi.Core.Api;

namespace Malachi.Core.Html;

/// <summary>The message part a <c>malachi-cid:</c> URL names, checked by <see cref="PartPath.ParsePartPath"/>.</summary>
/// <param name="AccountId">The account.</param>
/// <param name="MessageId">The message.</param>
/// <param name="PartId">The part number ("1.2").</param>
public sealed record PartReference(AccountId AccountId, MessageId MessageId, string PartId);
