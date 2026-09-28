// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/Mailto.swift (MailtoError); GTK:
// ui/internal/compose/mailto.go (the errors of ParseMailto).

namespace Malachi.Core.Compose;

/// <summary>Why <see cref="Mailto.ParseMailto"/> refused a string.</summary>
public enum MailtoError
{
    /// <summary>
    /// url.Parse would refuse it: a control character, a colon before any
    /// scheme, a malformed escape in the path or the fragment.
    /// </summary>
    InvalidUri,

    /// <summary>The scheme is not mailto.</summary>
    NotMailto,
}
