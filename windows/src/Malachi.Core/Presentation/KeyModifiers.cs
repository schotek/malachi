// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): the modifiers of a
// keyboard shortcut, without WinUI's VirtualKeyModifiers in Core (the
// router maps those and the pre-translate handler's FCONTROL/FSHIFT/FALT
// onto this).

using System;

namespace Malachi.Core.Presentation;

/// <summary>The modifier keys held with a key.</summary>
[Flags]
public enum KeyModifiers
{
    /// <summary>No modifier.</summary>
    None = 0,

    /// <summary>Ctrl.</summary>
    Control = 1,

    /// <summary>Shift.</summary>
    Shift = 2,

    /// <summary>Alt (Menu).</summary>
    Alt = 4,

    /// <summary>The Windows key.</summary>
    Windows = 8,
}
