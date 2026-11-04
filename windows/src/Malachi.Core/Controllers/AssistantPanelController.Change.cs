// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Change); GTK: ui/internal/assistantpanel/
// controller.go (Change). Swift's enum with an associated index is GTK's
// kind and index; the index of a Reset is 0 and means nothing.

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>What changed in <see cref="Items"/>, for the view.</summary>
    /// <param name="Kind">Everything (<see cref="ChangeKind.Reset"/>), or the item at <paramref name="Index"/>.</param>
    /// <param name="Index">The index of the appended or updated item; unused for a reset.</param>
    public readonly record struct Change(ChangeKind Kind, int Index);
}
