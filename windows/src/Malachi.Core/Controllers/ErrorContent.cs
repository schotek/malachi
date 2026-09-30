// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.error); GTK:
// ui/internal/assistantpanel/controller.go (ContentError).

namespace Malachi.Core.Controllers;

/// <summary>
/// What went wrong; <paramref name="Retry"/> offers Try Again
/// (<see cref="AssistantPanelController.Retry"/>), <paramref name="Offer"/>
/// one more button.
/// </summary>
/// <param name="Text">The line, translated; a technical reason in it is shown as data.</param>
/// <param name="Retry">Whether Try Again is offered (only the last question's error offers it).</param>
/// <param name="Offer">The error's other button (only the last question's error has one).</param>
public sealed record ErrorContent(string Text, bool Retry, ErrorOffer Offer = ErrorOffer.None) : AssistantPanelContent;
