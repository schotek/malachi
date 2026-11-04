// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the questions of macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift
// (ask, askScope) and macos/Sources/MalachiMail/EventEditor/EventEditorWindowController.swift
// (ask, askDelete) and EventEditorViewController.swift (askLink); GTK: the
// calendar's window code follows (docs/calendar.md §6.4), the texts are
// ui/internal/calendar/detail.go and editor.go (Cal.Question,
// Cal.ScopeQuestion, Cal.CloseQuestion).
//
// The calendar's dialogs, on the window of the action, one at a time with
// its other dialogs (the scheduler of AlertService.cs). Windows' button
// order (docs/windows-port.md §11.4): the confirmation is the primary
// button on the left, the alternative (Remove Only, Discard) the secondary
// one, Cancel the close button on the right; the default stays macOS's
// (Cancel for the detail's and Delete's questions, Save for the editor's
// close question). The series question (This Event Only, This and Future
// Events, All Events) has up to three choices beside Cancel, one more than
// a ContentDialog's buttons: its choices are buttons in the dialog's
// content, the first the default (it takes the keyboard), Cancel the close
// button. Paste Link… is a field in a dialog, Done and Cancel. Every text
// is plain text (the event's title is in the questions).

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Shell;

/// <summary>The calendar's dialogs.</summary>
internal sealed partial class AlertService
{
    /// <inheritdoc/>
    public async Task<Cal.Answer> CalendarQuestionAsync(Window? window, Cal.Question question, bool confirmDefault)
    {
        ArgumentNullException.ThrowIfNull(question);
        var result = await ShowAsync(window, () =>
        {
            var dialog = new ContentDialog
            {
                Title = Heading(question.Heading),
                Content = question.Body.Length == 0 ? null : Body(question.Body),
                PrimaryButtonText = Mnemonic.Strip(question.Confirm),
                CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
                DefaultButton = confirmDefault ? ContentDialogButton.Primary : ContentDialogButton.Close,
            };
            if (question.Alternative.Length > 0)
            {
                dialog.SecondaryButtonText = Mnemonic.Strip(question.Alternative);
            }
            return dialog;
        });
        return result switch
        {
            ContentDialogResult.Primary => Cal.Answer.Confirm,
            ContentDialogResult.Secondary => Cal.Answer.Alternative,
            _ => Cal.Answer.Cancel,
        };
    }

    /// <inheritdoc/>
    public async Task<EditScope?> CalendarScopeAsync(Window? window, Cal.ScopeQuestion question)
    {
        ArgumentNullException.ThrowIfNull(question);
        EditScope? chosen = null;
        await ShowAsync(window, () =>
        {
            var dialog = new ContentDialog
            {
                Title = Heading(question.Heading),
                CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
                DefaultButton = ContentDialogButton.None,
            };
            var panel = new StackPanel { Spacing = 8 };
            if (question.Body.Length > 0)
            {
                panel.Children.Add(Body(question.Body));
            }
            Button? first = null;
            foreach (var choice in question.Choices)
            {
                var button = new Button { Content = choice.Title, HorizontalAlignment = HorizontalAlignment.Stretch };
                AutomationProperties.SetAutomationId(button, ScopeAutomationId(choice.Scope));
                if (first is null)
                {
                    first = button;
                    if (Application.Current.Resources.TryGetValue("AccentButtonStyle", out var accent) && accent is Style style)
                    {
                        button.Style = style;
                    }
                }
                var scope = choice.Scope;
                button.Click += (_, _) =>
                {
                    chosen = scope;
                    dialog.Hide();
                };
                panel.Children.Add(button);
            }
            dialog.Content = panel;
            // The first choice is the default: it takes the keyboard, so
            // Enter picks it, as Return does on macOS.
            dialog.Opened += (_, _) => first?.Focus(FocusState.Programmatic);
            return dialog;
        });
        return chosen;
    }

    /// <inheritdoc/>
    public async Task<string?> VideoLinkAsync(Window? window, string initial)
    {
        ArgumentNullException.ThrowIfNull(initial);
        var field = new TextBox
        {
            Text = initial,
            // Windows-only string: the scheme the link must have, not a sentence (as macOS).
            PlaceholderText = "https://",
            MinWidth = 300,
            IsSpellCheckEnabled = false,
        };
        AutomationProperties.SetAutomationId(field, "EventEditorVideoLink");
        AutomationProperties.SetName(field, Cal.PasteLink);
        var result = await ShowAsync(window, () =>
        {
            var dialog = new ContentDialog
            {
                Title = Heading(Cal.AddVideoCall),
                Content = field,
                PrimaryButtonText = Mnemonic.Strip(L10n.T("Done")),
                CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
                DefaultButton = ContentDialogButton.Primary,
            };
            dialog.Opened += (_, _) =>
            {
                field.Focus(FocusState.Programmatic);
                field.SelectAll();
            };
            return dialog;
        });
        return result == ContentDialogResult.Primary ? field.Text : null;
    }

    // The series question's choices for the UI tests (This Event Only, This
    // and Future Events, All Events).
    private static string ScopeAutomationId(EditScope scope) => scope.Value switch
    {
        EditScope.All => "CalendarScopeAll",
        EditScope.ThisAndFuture => "CalendarScopeFuture",
        _ => "CalendarScopeThis",
    };
}
