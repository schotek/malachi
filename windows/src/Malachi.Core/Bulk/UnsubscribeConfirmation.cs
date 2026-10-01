// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Bulk/BulkMail.swift
// (BulkMail.Confirmation); GTK: ui/internal/bulkmail/bulkmail.go
// (Confirmation).

namespace Malachi.Core.Bulk;

/// <summary>
/// The dialog that asks before the daemon acts or a page opens
/// (bulkmail.Confirmation). The texts are plain text; <see cref="Confirm"/>
/// has a mnemonic.
/// </summary>
/// <param name="Heading">The question.</param>
/// <param name="Body">What will happen; may name the sender's address or page (hostile input).</param>
/// <param name="Confirm">The label of the confirming button, with its mnemonic.</param>
public sealed record UnsubscribeConfirmation(string Heading, string Body, string Confirm);
