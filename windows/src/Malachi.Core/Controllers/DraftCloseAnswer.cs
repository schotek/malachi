// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift
// (DraftCloseAnswer); GTK: ui/internal/compose/draft.go (closeRequest, the
// responses of "Save changes to this draft?").

namespace Malachi.Core.Controllers;

/// <summary>
/// What the user answered to "Save changes to this draft?" (the alert
/// service maps its dialog's result onto this).
/// </summary>
public enum DraftCloseAnswer
{
    /// <summary>_Save Draft (the default response).</summary>
    Save,

    /// <summary>_Discard.</summary>
    Discard,

    /// <summary>_Cancel (the close response): the window stays.</summary>
    Cancel,
}
