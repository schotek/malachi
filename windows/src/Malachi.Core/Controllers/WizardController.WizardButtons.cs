// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.WizardButtons); GTK: ui/internal/accountwizard/
// wizard.go (showButtons, hideButtons).

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>Which buttons the testing page offers (wizard.go <c>showButtons</c>).</summary>
    /// <param name="Edit">Edit Servers, or Sign In Again (<see cref="EditLabel"/>).</param>
    /// <param name="Retry">Retry.</param>
    /// <param name="AddAnyway">Add Anyway / Save Anyway.</param>
    /// <param name="Add">Add Account / Save.</param>
    public sealed record WizardButtons(bool Edit = false, bool Retry = false, bool AddAnyway = false, bool Add = false)
    {
        /// <summary>Nothing shown, while a call runs (wizard.go <c>hideButtons</c>).</summary>
        public static WizardButtons None { get; } = new();
    }
}
