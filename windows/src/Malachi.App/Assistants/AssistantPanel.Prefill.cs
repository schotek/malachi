// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift
// (MainWindowController.askAssistant(about:)) does to the panel's field
// (panel.input.text, focusInput); GTK: the calendar's window code follows
// (docs/calendar.md §6.4).
//
// Ask about This Event (docs/calendar.md §5.5): the question naming the
// event (Assistant.AskAboutEventPrompt, no text of the event in it) goes
// into the panel's field with the caret at its end, for the user to finish
// and send; nothing is sent.

namespace Malachi.App.Assistants;

/// <summary>The panel's field, filled for the user.</summary>
public sealed partial class AssistantPanel
{
    /// <summary>Puts <paramref name="text"/> into the question field, the caret at its end, and gives it the keyboard.</summary>
    public void Prefill(string text)
    {
        System.ArgumentNullException.ThrowIfNull(text);
        Input.Text = text;
        Input.SelectionStart = text.Length;
        Input.SelectionLength = 0;
        // After the panel has unfolded.
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, FocusInput);
    }
}
