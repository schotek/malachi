// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/Fields.swift (Endpoint); GTK:
// ui/internal/accountwizard/fields.go (Endpoint).

namespace Malachi.Core.Wizard;

/// <summary>accountwizard.Endpoint: the IMAP or SMTP side of the settings.</summary>
public enum Endpoint
{
    /// <summary>Incoming mail.</summary>
    Imap,

    /// <summary>Outgoing mail.</summary>
    Smtp,
}
