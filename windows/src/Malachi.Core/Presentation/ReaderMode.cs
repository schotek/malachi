// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/MessageViewMode.swift; GTK:
// the one messageView of ui/internal/window/message_view.go, built from
// window.blp, message_window.blp and embedded_window.blp, which differ in a
// few places only.

namespace Malachi.Core.Presentation;

/// <summary>Where a message view lives.</summary>
public enum ReaderMode
{
    /// <summary>The main window's message pane: the No Message Selected and No Accounts pages, the draft banner.</summary>
    Pane,

    /// <summary>A stand-alone message window.</summary>
    Window,

    /// <summary>
    /// An attached message (embedded.go): no outbox banner, no trust button,
    /// the chips only name the parts, Load Images renders the part again
    /// through <c>message.embedded</c>.
    /// </summary>
    Embedded,
}
