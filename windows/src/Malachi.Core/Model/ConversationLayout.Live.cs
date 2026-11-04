// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift (Live);
// GTK: ui/internal/window/conversation_layout.go (convLive).

using System.Collections.Generic;

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>
    /// convLive: what <see cref="LiveCards"/> decided: the items near the
    /// viewport (a body is fetched for them) and those of them that get a web
    /// view.
    /// </summary>
    public sealed record Live(IReadOnlySet<int> Near, IReadOnlySet<int> Web);
}
