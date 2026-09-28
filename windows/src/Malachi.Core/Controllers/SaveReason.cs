// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift
// (SaveReason); GTK: ui/internal/compose/draft.go (saveReason).

namespace Malachi.Core.Controllers;

/// <summary>compose.saveReason: what asked for a save of the draft.</summary>
public enum SaveReason
{
    /// <summary>Ctrl+S, the menu, the close question, sending, quitting.</summary>
    Explicit,

    /// <summary>The timer 30 s after the first unsaved edit.</summary>
    Autosave,
}
