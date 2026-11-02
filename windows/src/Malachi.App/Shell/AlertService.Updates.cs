// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first, the dialogs of the update check (ui/internal/updates
// NotesFor and PromptFor; docs/plans/update-check.md): What's New and the
// question of a security release, ContentDialogs on the window's
// DialogScheduler like every other dialog of the app. Every text is set as
// plain text (TextBlock.Text): the notes come from the signed manifest and
// were checked by the daemon, and are still never markup. The buttons in
// WinUI's order: the action on the left, Close or Cancel on the right.

using System.Threading.Tasks;
using Malachi.App.Assistants;
using Malachi.Core.Updates;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Shell;

/// <content>The update check's dialogs.</content>
internal sealed partial class AlertService
{
    /// <inheritdoc/>
    public async Task<BannerAction?> WhatsNewAsync(Window? window, UpdateNotes notes)
    {
        var result = await ShowAsync(window, () =>
        {
            var panel = new StackPanel { Spacing = 12 };
            panel.Children.Add(new TextBlock
            {
                Text = notes.Subtitle,
                Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
                TextWrapping = TextWrapping.Wrap,
            });
            if (notes.Blocks.Count > 0)
            {
                // The notes as Markdown, drawn like the assistant's answers;
                // links are plain text (NotesBlocks), so openLink is never called.
                var view = new RichTextBlock { IsTextSelectionEnabled = true, TextWrapping = TextWrapping.Wrap };
                AnswerRenderer.Render(view, notes.Blocks, _ => { });
                panel.Children.Add(new ScrollViewer { Content = view, MaxHeight = 360 });
            }
            var dialog = new ContentDialog
            {
                Title = Heading(notes.Title),
                Content = panel,
                CloseButtonText = notes.Close,
                DefaultButton = ContentDialogButton.Close,
            };
            if (notes.Url.Length > 0)
            {
                dialog.PrimaryButtonText = notes.Download;
                dialog.DefaultButton = ContentDialogButton.Primary;
            }
            if (notes.InfoUrl.Length > 0)
            {
                dialog.SecondaryButtonText = notes.MoreInfo;
            }
            return dialog;
        });
        return result switch
        {
            ContentDialogResult.Primary => BannerAction.Download,
            ContentDialogResult.Secondary => BannerAction.MoreInfo,
            _ => null,
        };
    }

    /// <inheritdoc/>
    public async Task<(bool Download, bool Quit)> SecurityUpdateAsync(Window? window, UpdatePrompt prompt)
    {
        var quit = new CheckBox { IsChecked = true, Content = prompt.Quit };
        var result = await ShowAsync(window, () =>
        {
            var panel = new StackPanel { Spacing = 12 };
            panel.Children.Add(Body(prompt.Body));
            panel.Children.Add(quit);
            return new ContentDialog
            {
                Title = Heading(prompt.Title),
                Content = panel,
                PrimaryButtonText = prompt.Download,
                CloseButtonText = prompt.Cancel,
                DefaultButton = ContentDialogButton.Primary,
            };
        });
        return (result == ContentDialogResult.Primary, quit.IsChecked == true);
    }
}
