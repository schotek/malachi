// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.IdentityField); GTK: ui/internal/accountwizard/
// wizard.go (the GrabFocus calls on email and password).

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>The identity fields the page can focus.</summary>
    public enum IdentityField
    {
        /// <summary>The address.</summary>
        Email,

        /// <summary>The password.</summary>
        Password,
    }
}
