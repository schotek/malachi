// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.CommandR),
// the Windows-only key ctrl-r (docs/windows-port.md §0, §11.5).

namespace Malachi.Core.Settings;

/// <summary>What Ctrl+R does; stored as <c>reply</c> or <c>refresh</c>.</summary>
public enum CtrlR
{
    /// <summary><c>reply</c>, the default: Outlook's Ctrl+R replies, F5 checks for new mail.</summary>
    Reply,

    /// <summary><c>refresh</c>: the GTK UI's Ctrl+R checks for new mail.</summary>
    Refresh,
}
