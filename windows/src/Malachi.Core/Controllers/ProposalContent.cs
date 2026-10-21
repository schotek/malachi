// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.proposal); GTK:
// ui/internal/assistantpanel/controller.go (ContentProposal).

namespace Malachi.Core.Controllers;

/// <summary>
/// A note the assistant proposed for the user's profile
/// (propose_profile_note), by its id: the view reads its text from the
/// <see cref="ProfileStore"/> and offers Save, Edit… and Dismiss.
/// </summary>
/// <param name="Proposal">The proposal's id, as the head of the propose_profile_note result names it.</param>
public sealed record ProposalContent(string Proposal) : AssistantPanelContent;
