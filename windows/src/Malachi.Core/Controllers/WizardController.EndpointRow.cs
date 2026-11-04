// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.EndpointRow); GTK: ui/internal/accountwizard/
// wizard.go (showResults: the rows' icon and subtitle) and trust.go (the
// row's "Trust Certificate…").

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>
    /// One endpoint row of the results: a GTK icon name (mapped to a glyph by
    /// the page), the subtitle, and whether the row offers "Trust
    /// Certificate…" (<see cref="TrustCertificate"/>).
    /// </summary>
    /// <param name="Icon">emblem-ok-symbolic, dialog-warning-symbolic, dialog-error-symbolic or dialog-question-symbolic.</param>
    /// <param name="Text">The subtitle, plain text.</param>
    /// <param name="Trust">Whether the row offers to trust the certificate the server presented.</param>
    public sealed record EndpointRow(string Icon, string Text, bool Trust = false);
}
