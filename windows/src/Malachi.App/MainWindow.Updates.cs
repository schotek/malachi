// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first, the main window's part of the update check
// (ui/internal/updates: BannerFor, PromptFor, NotesFor, OpenableURL;
// docs/plans/update-check.md): the banner under the title bar, in every
// mode, from the application's UpdateStatusController (AppState.Updates.cs);
// its buttons in the reference's order, the first the accent one: Download
// and More Information open the page in the browser, What's New shows the
// notes (AlertService.Updates.cs), Remind Me Later and Skip This Version
// call update.dismiss, a notice's Close update.dismissNotice. A security
// release is a warning that has no Later or Skip and stays until the
// update is installed.
//
// The question of a security release is asked once, while this window is
// shown (else when it is shown next): the controller hands it out once per
// version in this process and tells the daemon "prompted" whatever the
// answer, so that no other client asks again. Download opens the package;
// with the check box on (the default) the app then quits through the same
// path as the menu's Quit, its unsaved-work questions included.

using System;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Text;
using Malachi.Core.Updates;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App;

/// <content>The update check's banner and question.</content>
public sealed partial class MainWindow
{
    // The banner shown, so that an equal one is not built again (its
    // buttons would lose the keyboard).
    private UpdateBanner shownUpdate = new();

    // The security question is on screen.
    private bool askingUpdate;

    private void InitializeUpdates()
    {
        var updates = state.Updates;
        EventHandler changed = (_, _) =>
        {
            RenderUpdateBanner();
            _ = AskSecurityUpdateAsync();
        };
        updates.Changed += changed;
        ShownChanged += (_, shown) =>
        {
            if (shown)
            {
                _ = AskSecurityUpdateAsync();
            }
        };
        Closed += (_, _) => updates.Changed -= changed;
        RenderUpdateBanner();
    }

    // updates.BannerFor at the time of this call; a put-off version comes
    // back with the daemon's notify.updateChanged when its time is up.
    private void RenderUpdateBanner()
    {
        var banner = UpdateCheck.BannerFor(state.Updates.Status, TimeProvider.System.GetUtcNow());
        if (SameBanner(banner, shownUpdate))
        {
            return;
        }
        shownUpdate = banner;
        UpdateBarButtons.Children.Clear();
        if (!banner.Shown)
        {
            UpdateBar.IsOpen = false;
            UpdateBar.Margin = default;
            return;
        }
        UpdateBar.Severity = banner.Kind == BannerKind.Security ? InfoBarSeverity.Warning : InfoBarSeverity.Informational;
        UpdateBar.Message = banner.Title;
        var first = true;
        foreach (var b in banner.Buttons)
        {
            var button = new Button { Content = b.Label };
            if (first)
            {
                button.Style = (Style)Application.Current.Resources["AccentButtonStyle"];
                first = false;
            }
            AutomationProperties.SetAutomationId(button, "UpdateBanner" + b.Action);
            var action = b.Action;
            button.Click += (_, _) => OnUpdateBannerAction(banner, action);
            UpdateBarButtons.Children.Add(button);
        }
        UpdateBar.Margin = new Thickness(6, 0, 6, 6);
        UpdateBar.IsOpen = true;
    }

    private static bool SameBanner(UpdateBanner a, UpdateBanner b) =>
        a.Kind == b.Kind && a.Title == b.Title && a.Version == b.Version && a.Url == b.Url && a.NoticeId == b.NoticeId
        && a.Buttons.SequenceEqual(b.Buttons);

    private void OnUpdateBannerAction(UpdateBanner banner, BannerAction action)
    {
        switch (action)
        {
            case BannerAction.Download:
            case BannerAction.MoreInfo:
                OpenUpdatePage(banner.Url);
                break;
            case BannerAction.WhatsNew:
                _ = ShowWhatsNewAsync();
                break;
            case BannerAction.Later:
                state.Updates.Dismiss(banner.Version, UpdateDismissAction.Later);
                break;
            case BannerAction.Skip:
                state.Updates.Dismiss(banner.Version, UpdateDismissAction.Skip);
                break;
            case BannerAction.Close:
                state.Updates.DismissNotice(banner.NoticeId);
                break;
        }
    }

    // What's New over this window: Download or More Information open
    // their page, Close nothing.
    private async Task ShowWhatsNewAsync()
    {
        if (UpdateCheck.NotesFor(state.Updates.Status, t => Format.FormatDateTime(t)) is not { } notes)
        {
            return;
        }
        switch (await state.Alerts.WhatsNewAsync(this, notes))
        {
            case BannerAction.Download:
                OpenUpdatePage(notes.Url);
                break;
            case BannerAction.MoreInfo:
                OpenUpdatePage(notes.InfoUrl);
                break;
        }
    }

    // updates.PromptFor, once per version and process, while this window is
    // shown; "prompted" goes to the daemon whatever the answer.
    private async Task AskSecurityUpdateAsync()
    {
        if (askingUpdate || state.IsStopping || !AppWindow.IsVisible
            || state.Updates.PendingPrompt is not { } prompt || !state.Updates.BeginPrompt(prompt.Version))
        {
            return;
        }
        askingUpdate = true;
        try
        {
            var (download, quit) = await state.Alerts.SecurityUpdateAsync(this, prompt);
            state.Updates.PromptAnswered(prompt.Version);
            if (download && UpdateCheck.OpenableUrl(prompt.Url) is { } url)
            {
                await state.OpenUrlAsync(url);
                if (quit)
                {
                    state.Quit();
                }
            }
        }
        finally
        {
            askingUpdate = false;
        }
    }

    // Only a URL the reference lets through reaches the browser.
    private void OpenUpdatePage(string url)
    {
        if (UpdateCheck.OpenableUrl(url) is { } u)
        {
            _ = state.OpenUrlAsync(u);
        }
    }
}
