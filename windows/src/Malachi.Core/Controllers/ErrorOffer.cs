// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Offer); GTK:
// ui/internal/assistantpanel/controller.go (Offer).

namespace Malachi.Core.Controllers;

/// <summary>The button of an error item beyond Try Again (<see cref="ErrorContent.Offer"/>).</summary>
public enum ErrorOffer
{
    /// <summary>OfferNone: no other button.</summary>
    None,

    /// <summary>
    /// OfferSignIn: "Sign In…" beside "Claude Code is not signed in"
    /// (<see cref="AssistantPanelController.SignIn"/>).
    /// </summary>
    SignIn,

    /// <summary>
    /// OfferInstall: "Get Claude Code…" beside "Claude Code was not found on
    /// this computer"; the view opens <see cref="Assistants.Assistant.InstallUrl"/>
    /// in the browser.
    /// </summary>
    Install,
}
