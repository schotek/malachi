// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeAssistantBar.swift
// (onRequest, onCancel, onApply, setProvider, setMode, show, toggle,
// renderDisclosure, request, cancel, replace); GTK: ui/data/ui/
// compose_extras.blp (assistant_bar) and ui/internal/assistant
// compose_write.go (WriteTexts, WriteBarTitle). A compact, collapsible
// assistant at the bottom of the compose window: generated text is
// previewed and goes into the editor only by an explicit Replace. The
// window wires it (ComposeWindow.Write.cs) and renders its
// ComposeWriteController's state through Show.
//
// Windows differences: the disclosure's state is the button's help text
// ("Expanded" / "Collapsed"), as the date groups of the message list say
// it (macOS sets it as the accessibility value); the mnemonics of Cancel
// and Discard are stripped, as the rewrite's Discard is.

using System;
using Malachi.App.Resources;
using Malachi.Core.Assistants;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Compose;

/// <summary>The assistant's bar of a compose window (ComposeAssistantBar).</summary>
public sealed partial class ComposeAssistantBar : UserControl
{
    // The contents are shown (the disclosure).
    private bool expanded = true;

    // The finished answer Replace puts into the editor ("" while running or
    // without one).
    private string answer = "";

    /// <summary>A bar for a new message, naming Claude, with nothing asked.</summary>
    public ComposeAssistantBar()
    {
        InitializeComponent();
        SetMode(AssistantWriteMode.New);
        SetProvider(chatGpt: false);
        RenderDisclosure();
        Show("", running: false);
    }

    /// <summary>Write, or Enter in the field, with the instruction typed there (onRequest).</summary>
    public event EventHandler<string>? WriteRequested;

    /// <summary>Cancel or Discard (onCancel); the bar has cleared itself.</summary>
    public event EventHandler? CancelRequested;

    /// <summary>Replace with the finished answer (onApply); the bar has cleared itself.</summary>
    public event EventHandler<string>? ApplyRequested;

    /// <summary>setProvider: names the provider in effect after "Assistant".</summary>
    public void SetProvider(bool chatGpt)
    {
        var title = Assistant.WriteBarTitle(chatGpt);
        TitleText.Text = title;
        AutomationProperties.SetName(Disclosure, title);
    }

    /// <summary>setMode: the button and the field's placeholder for a window of <paramref name="mode"/>.</summary>
    public void SetMode(AssistantWriteMode mode)
    {
        var texts = Assistant.WriteTexts(mode);
        WriteButton.Content = texts.Write;
        AutomationProperties.SetName(WriteButton, texts.Write);
        Instruction.PlaceholderText = texts.Instruction;
        AutomationProperties.SetName(Instruction, texts.Instruction);
    }

    /// <summary>
    /// show: the answer so far (<paramref name="running"/>) or the finished
    /// one in the preview, <paramref name="failure"/> in the error line;
    /// Cancel while running, Discard and Replace once there is an answer;
    /// the field and Write wait while running.
    /// </summary>
    public void Show(string text, bool running, string failure = "")
    {
        ArgumentNullException.ThrowIfNull(text);
        ArgumentNullException.ThrowIfNull(failure);
        answer = running ? "" : text;
        if (Preview.Text != text)
        {
            Preview.Text = text;
        }
        Preview.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ErrorText.Text = failure;
        ErrorText.Visibility = failure.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ResultButtons.Visibility = !running && text.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        ReplaceButton.IsEnabled = !running && text.Length > 0;
        var dismiss = running ? Mnemonic.Strip(L10n.T("_Cancel")) : Mnemonic.Strip(L10n.T("_Discard"));
        DiscardButton.Content = dismiss;
        AutomationProperties.SetName(DiscardButton, dismiss);
        Instruction.IsEnabled = !running;
        WriteButton.IsEnabled = !running;
        Spinner.IsActive = running;
        Spinner.Visibility = running ? Visibility.Visible : Visibility.Collapsed;
    }

    // renderDisclosure: the contents shown or not, the chevron, the state.
    private void RenderDisclosure()
    {
        Contents.Visibility = expanded ? Visibility.Visible : Visibility.Collapsed;
        DisclosureIcon.Glyph = Icons.Glyph(expanded ? "pan-down-symbolic" : "pan-end-symbolic");
        AutomationProperties.SetHelpText(Disclosure, expanded ? L10n.T("Expanded") : L10n.T("Collapsed"));
    }

    // toggle
    private void OnDisclosureClick(object sender, RoutedEventArgs e)
    {
        expanded = !expanded;
        RenderDisclosure();
    }

    // request: Write, while it is enabled.
    private void OnWriteClick(object sender, RoutedEventArgs e) => Request();

    // The field's Enter is Write (the macOS field's action).
    private void OnInstructionKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != WinUIKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Request();
    }

    private void Request()
    {
        if (WriteButton.IsEnabled)
        {
            WriteRequested?.Invoke(this, Instruction.Text);
        }
    }

    // cancel: Cancel or Discard.
    private void OnDiscardClick(object sender, RoutedEventArgs e)
    {
        CancelRequested?.Invoke(this, EventArgs.Empty);
        Show("", running: false);
    }

    // replace: the finished answer to the editor.
    private void OnReplaceClick(object sender, RoutedEventArgs e)
    {
        if (answer.Length == 0)
        {
            return;
        }
        var text = answer;
        ApplyRequested?.Invoke(this, text);
        Show("", running: false);
    }
}
