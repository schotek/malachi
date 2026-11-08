// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/ComposeAssistantPrompt.swift
// (writeMode, the brands) and of the texts macOS sets in
// ComposeAssistantBar.swift and ComposePane.swift; GTK:
// ui/internal/assistant/compose_write.go (WriteMode.API, ClaudeBrand,
// ChatGPTBrand, WriteTexts, WriteBarTitle, WriteBarTitleFor). The compose window's assistant
// bar (the In App target only): from an optional instruction, the user's own
// text, the subject and the prepared original under the editor, the user's
// assistant writes a reply, a forward's introduction or a message. It is a
// one-shot request of purpose write, like the rewrite: the daemon builds
// the system prompt and the message of WriteInput (docs/api.md §4.18), the
// answer comes in the result event, cleaned (CleanRewrite) and previewed;
// only Replace puts it into the editor. Everything but the instruction is
// mail data, never an instruction. The texts go through L10n; the
// providers' names are brands, not translated.

using System;
using System.Globalization;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>assistant.ClaudeBrand: Claude as the compose window's bar names it (a brand, not translated).</summary>
    public const string ClaudeBrand = "Claude (Anthropic)";

    /// <summary>assistant.ChatGPTBrand: ChatGPT as the compose window's bar names it (a brand, not translated).</summary>
    public const string ChatGptBrand = "ChatGPT (OpenAI)";

    /// <summary>
    /// The nick of a mode as Go spells it ("new", "reply", "replyAll",
    /// "forward", "edit"); an unknown value is its number, as its ToString()
    /// spells it.
    /// </summary>
    public static string WriteModeNick(AssistantWriteMode mode) => mode switch
    {
        AssistantWriteMode.New => "new",
        AssistantWriteMode.Reply => "reply",
        AssistantWriteMode.ReplyAll => "replyAll",
        AssistantWriteMode.Forward => "forward",
        AssistantWriteMode.Edit => "edit",
        _ => ((int)mode).ToString(CultureInfo.InvariantCulture),
    };

    /// <summary>
    /// assistant.WriteMode.API: the kind of window as the daemon's
    /// <see cref="Api.WriteInput.Mode"/> takes it (<see cref="WriteModeNick"/>).
    /// </summary>
    public static Api.WriteMode WriteModeOf(AssistantWriteMode mode) => new(WriteModeNick(mode));

    /// <summary>
    /// assistant.WriteTexts: the fixed texts of the bar and of the quoted
    /// panel for a window of <paramref name="mode"/>, translated.
    /// </summary>
    public static WriteStrings WriteTexts(AssistantWriteMode mode)
    {
        // TRANSLATORS: Placeholder of the field of the assistant's bar at the bottom of the compose window.
        var instruction = L10n.T("What should the message say? (optional)");
        // TRANSLATORS: Heading of the panel under the compose window's editor that shows the message being replied to or forwarded.
        var quoted = L10n.T("Quoted Message");
        // TRANSLATORS: A checkbox in the compose window: whether the reply includes the quoted message.
        var include = L10n.T("Include in Reply");
        string write;
        switch (mode)
        {
            case AssistantWriteMode.Forward:
                // TRANSLATORS: A button of the assistant's bar in the compose window of a forward.
                write = L10n.T("Write an Introduction");
                // TRANSLATORS: A checkbox in the compose window: whether the forward includes the forwarded message.
                include = L10n.T("Include in Forward");
                break;
            case AssistantWriteMode.New or AssistantWriteMode.Edit:
                // TRANSLATORS: A button of the assistant's bar in the compose window of a new message.
                write = L10n.T("Write a Message");
                break;
            default:
                // TRANSLATORS: A button of the assistant's bar in the compose window of a reply.
                write = L10n.T("Write a Reply");
                break;
        }
        return new WriteStrings { Instruction = instruction, Write = write, Quoted = quoted, Include = include };
    }

    /// <summary>
    /// assistant.WriteBarTitle: the bar's heading, "Assistant · " and the
    /// provider in effect (<see cref="ClaudeBrand"/>, or
    /// <see cref="ChatGptBrand"/> with <paramref name="chatGpt"/>).
    /// </summary>
    public static string WriteBarTitle(bool chatGpt) => WriteBarTitleFor(chatGpt ? ChatGptBrand : ClaudeBrand);

    /// <summary>
    /// assistant.WriteBarTitleFor: the bar's heading with the provider named,
    /// a brand (<see cref="ClaudeBrand"/>, <see cref="ChatGptBrand"/>) or the
    /// name of an AI provider account.
    /// </summary>
    public static string WriteBarTitleFor(string brand)
    {
        // TRANSLATORS: Heading of the assistant's bar in the compose window; %s is the provider, e.g. "Claude (Anthropic)".
        return L10n.T("Assistant · %s", brand);
    }
}
