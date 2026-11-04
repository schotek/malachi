// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift (Span);
// GTK: ui/internal/window/conversation_layout.go (convSpan, convSpanOf,
// distance).

using System;

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>convSpan: a vertical stretch of the stack, in the document's coordinates (top down).</summary>
    public readonly record struct Span(double Min, double Max)
    {
        /// <summary>convSpanOf: the stretch from <paramref name="minY"/> to <paramref name="maxY"/>; an inverted one is empty at its top.</summary>
        public static Span Of(double minY, double maxY) => new(minY, Math.Max(minY, maxY));

        /// <summary>How far this is from <paramref name="other"/>: 0 when they overlap (touching counts).</summary>
        public double Distance(Span other)
        {
            if (Max < other.Min)
            {
                return other.Min - Max;
            }
            if (Min > other.Max)
            {
                return Min - other.Max;
            }
            return 0;
        }
    }
}
