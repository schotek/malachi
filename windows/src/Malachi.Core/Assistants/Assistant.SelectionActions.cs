// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantSelection.swift
// (messageActionsFor, labelFor); GTK: ui/internal/assistant/selection.go
// (MessageActionsFor, LabelFor). The Assistant's message actions on a
// selection of several rows of the message list (Selections/Selection.cs):
// the ones that make sense for several messages, and Ask's label for them.
// The file is not AssistantSelection.cs, which is the AssistantSelection
// record (Swift's Assistant.Selection).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// assistant.MessageActionsFor: <see cref="MessageActions"/> for
    /// <paramref name="count"/> selected rows: several leave out Draft a
    /// Reply…, which answers one message.
    /// </summary>
    public static IReadOnlyList<AssistantAction> MessageActionsFor(int count) =>
        count <= 1 ? MessageActions : [.. MessageActions.Where(a => a != AssistantAction.DraftReply)];

    /// <summary>
    /// assistant.LabelFor: <see cref="Label"/> for <paramref name="count"/>
    /// selected rows: Ask names them as several.
    /// </summary>
    public static string LabelFor(AssistantAction a, int count) =>
        a == AssistantAction.Ask && count > 1
            // TRANSLATORS: An item of the Assistant menu while several messages are selected.
            ? L10n.T("Ask About These Messages…")
            : Label(a);
}
