// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Alerts.swift (AppAlerts) and of
// AppDelegate.swift's showAbout; GTK: widget/rpc.go ConfirmDestructive,
// compose/draft.go (closeRequest's dialog), window/remote.go
// (openLink's masked-link dialog), accountwizard/trust.go
// (certificateDetails), main.go (the about action). The dialogs are
// ContentDialogs (docs/windows-port.md §11.4, a listed deviation): the
// buttons in WinUI's order, the primary one (the action) on the left and
// Cancel on the right, while the defaults and close responses stay GTK's:
// Cancel is the default (Enter) and the close response (Escape) of every
// destructive question, Save Draft the default of the close question. A
// window shows one ContentDialog at a time, so each window's dialogs wait
// in a DialogScheduler; a dialog for no window, or for one that is not
// shown, goes on the main window, which is shown first. The GTK mnemonics
// are stripped (a ContentDialog's buttons take no access key). Every text
// is set as plain text: the heading and the body are often hostile input.
//
// About carries the GTK fields (U8): the name, the icon, the version, the
// developer, the licence, the website and the issue tracker. GTK's
// comment ("A native mail client for the GNOME desktop.") names GNOME, so
// Windows says what macOS says, a Windows-only string.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;

namespace Malachi.App.Shell;

/// <summary>The app's dialogs, one at a time per window.</summary>
internal sealed partial class AlertService : IAlerts
{
    /// <summary>The project's website (main.go SetWebsite).</summary>
    public const string Website = "https://github.com/schotek/malachi";

    /// <summary>The issue tracker (main.go SetIssueURL).</summary>
    public const string IssueUrl = "https://github.com/schotek/malachi/issues";

    // The width of a value in the certificate's details (Alerts.swift
    // detailValueWidth): the fingerprint takes two lines.
    private const double DetailValueWidth = 220;

    private readonly Dictionary<Window, DialogScheduler> schedulers = [];
    private readonly Func<Window?> mainWindow;
    private readonly Action showMainWindow;
    private readonly Func<string, Task> openUrl;
    private readonly ILogger logger;

    /// <param name="mainWindow">The main window, which takes a dialog of no window.</param>
    /// <param name="showMainWindow">Shows the main window (a dialog cannot go on a hidden one).</param>
    /// <param name="openUrl">Opens a web address in the browser (About's links).</param>
    /// <param name="logger">Where a dialog that could not be shown is reported.</param>
    public AlertService(Func<Window?> mainWindow, Action showMainWindow, Func<string, Task> openUrl, ILogger logger)
    {
        this.mainWindow = mainWindow;
        this.showMainWindow = showMainWindow;
        this.openUrl = openUrl;
        this.logger = logger;
    }

    /// <inheritdoc/>
    public bool IsShowing(Window window) => schedulers.TryGetValue(window, out var s) && s.IsBusy;

    /// <inheritdoc/>
    public async Task<bool> ConfirmDestructiveAsync(Window? window, string heading, string body, string confirmLabel) =>
        await ShowAsync(window, () => Destructive(heading, body, confirmLabel, extra: null)) == ContentDialogResult.Primary;

    /// <inheritdoc/>
    public async Task<(bool Confirmed, bool Extra)> ConfirmDestructiveExtraAsync(
        Window? window, string heading, string body, string confirmLabel, string extraLabel, bool extraDefault)
    {
        var check = new CheckBox { IsChecked = extraDefault };
        Localization.MnemonicLabel.Apply(check, extraLabel);
        var result = await ShowAsync(window, () => Destructive(heading, body, confirmLabel, check));
        return (result == ContentDialogResult.Primary, check.IsChecked == true);
    }

    /// <inheritdoc/>
    public async Task<DraftCloseAnswer> SaveDraftQuestionAsync(Window? window)
    {
        var result = await ShowAsync(window, () => new ContentDialog
        {
            Title = Heading(L10n.T("Save changes to this draft?")),
            PrimaryButtonText = Mnemonic.Strip(L10n.T("_Save Draft")),
            SecondaryButtonText = Mnemonic.Strip(L10n.T("_Discard")),
            CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
            DefaultButton = ContentDialogButton.Primary,
        });
        return result switch
        {
            ContentDialogResult.Primary => DraftCloseAnswer.Save,
            ContentDialogResult.Secondary => DraftCloseAnswer.Discard,
            _ => DraftCloseAnswer.Cancel,
        };
    }

    /// <inheritdoc/>
    public async Task<bool> OpenLinkQuestionAsync(Window? window, string text, string href)
    {
        var result = await ShowAsync(window, () => new ContentDialog
        {
            Title = Heading(L10n.T("Open This Link?")),
            // TRANSLATORS: %s are the link's visible text and its real destination.
            Content = Body(L10n.T("The link is shown as “%s” but leads to %s.", text, href)),
            PrimaryButtonText = Mnemonic.Strip(L10n.T("_Open Link")),
            CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
            DefaultButton = ContentDialogButton.Close,
        });
        return result == ContentDialogResult.Primary;
    }

    /// <inheritdoc/>
    public async Task<bool> ConfirmTrustCertificateAsync(
        Window? window, string heading, string body, IReadOnlyList<CertificateDetail> details, string confirmLabel)
    {
        ArgumentNullException.ThrowIfNull(details);
        var result = await ShowAsync(window, () => Destructive(heading, body, confirmLabel, CertificateDetails(details)));
        return result == ContentDialogResult.Primary;
    }

    /// <inheritdoc/>
    public async Task ShowAboutAsync(Window? window) => await ShowAsync(window, About);

    /// <summary>The Core hook of a destructive confirmation (ActionsController.Confirm and friends).</summary>
    public ConfirmDestructive ConfirmHook() =>
        (parent, heading, body, confirmLabel) => ConfirmDestructiveAsync(parent as Window, heading, body, confirmLabel);

    private static TextBlock Heading(string text) => new()
    {
        Text = text,
        TextWrapping = TextWrapping.WrapWholeWords,
        MaxLines = 4,
    };

    private static TextBlock Body(string text) => new()
    {
        Text = text,
        TextWrapping = TextWrapping.Wrap,
        IsTextSelectionEnabled = true,
    };

    // Cancel (the default and the close response) and the action.
    private static ContentDialog Destructive(string heading, string body, string confirmLabel, UIElement? extra)
    {
        object content = Body(body);
        if (extra is not null)
        {
            var panel = new StackPanel { Spacing = 12 };
            if (body.Length > 0)
            {
                panel.Children.Add(Body(body));
            }
            panel.Children.Add(extra);
            content = panel;
        }
        return new ContentDialog
        {
            Title = Heading(heading),
            Content = body.Length == 0 && extra is null ? null : content,
            PrimaryButtonText = Mnemonic.Strip(confirmLabel),
            CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
            DefaultButton = ContentDialogButton.Close,
        };
    }

    // accountwizard trust.go certificateDetails: dim keys, values as
    // selectable wrapping plain text (the server's text is never
    // interpreted), the fingerprints monospaced; a line without a value
    // ("Self-signed") spans both columns in the normal colour.
    private static Grid CertificateDetails(IReadOnlyList<CertificateDetail> details)
    {
        var grid = new Grid { ColumnSpacing = 12, RowSpacing = 6 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(DetailValueWidth) });
        var row = 0;
        foreach (var d in details)
        {
            grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            var key = new TextBlock
            {
                Text = d.Label,
                Style = (Style)Application.Current.Resources[d.Value.Length == 0 ? "CaptionLabelStyle" : "DimCaptionLabelStyle"],
                TextWrapping = TextWrapping.Wrap,
            };
            Grid.SetRow(key, row);
            if (d.Value.Length == 0)
            {
                Grid.SetColumnSpan(key, 2);
                grid.Children.Add(key);
                row++;
                continue;
            }
            var value = new TextBlock
            {
                Text = d.Value,
                Style = (Style)Application.Current.Resources["CaptionLabelStyle"],
                TextWrapping = TextWrapping.Wrap,
                IsTextSelectionEnabled = true,
            };
            if (d.Monospaced)
            {
                value.FontFamily = (FontFamily)Application.Current.Resources["MonospaceFontFamily"];
            }
            Grid.SetRow(value, row);
            Grid.SetColumn(value, 1);
            grid.Children.Add(key);
            grid.Children.Add(value);
            row++;
        }
        return grid;
    }

    private ContentDialog About()
    {
        var panel = new StackPanel { Spacing = 8, HorizontalAlignment = HorizontalAlignment.Center };
        panel.Children.Add(new Image
        {
            Source = new BitmapImage(new Uri("ms-appx:///Assets/Malachi.ico")) { DecodePixelWidth = 96 },
            Width = 96,
            Height = 96,
            HorizontalAlignment = HorizontalAlignment.Center,
        });
        panel.Children.Add(new TextBlock
        {
            Text = AppIdentity.DisplayName,
            Style = (Style)Application.Current.Resources["Title1LabelStyle"],
        });
        panel.Children.Add(Centred(L10n.T("Malachi Mail contributors"), "DimLabelStyle"));
        panel.Children.Add(Centred(AppVersion.Full, "CaptionLabelStyle"));
        // Windows-only string: GTK's comment names the GNOME desktop.
        panel.Children.Add(Centred("A native mail client.", "BodyLabelStyle"));
        // Windows-only string: Adw.AboutDialog words the licence itself.
        panel.Children.Add(Centred("GNU General Public License, version 3 or later", "DimCaptionLabelStyle"));
        var links = new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Center, Spacing = 8 };
        // Windows-only strings: Adw.AboutDialog's own buttons.
        links.Children.Add(Link("Website", Website));
        links.Children.Add(Link("Report an Issue", IssueUrl));
        panel.Children.Add(links);
        return new ContentDialog
        {
            Content = panel,
            // Windows-only string: Adw.AboutDialog closes with its header's button.
            CloseButtonText = "Close",
            DefaultButton = ContentDialogButton.Close,
        };
    }

    private static TextBlock Centred(string text, string style) => new()
    {
        Text = text,
        Style = (Style)Application.Current.Resources[style],
        TextAlignment = TextAlignment.Center,
        HorizontalAlignment = HorizontalAlignment.Center,
        TextWrapping = TextWrapping.Wrap,
    };

    private HyperlinkButton Link(string label, string url)
    {
        var button = new HyperlinkButton { Content = label };
        ToolTipService.SetToolTip(button, url);
        button.Click += async (_, _) => await openUrl(url);
        return button;
    }

    // Puts the dialog on its window once that window's earlier dialogs are
    // over; a dialog that cannot be shown answers None (Cancel).
    private Task<ContentDialogResult> ShowAsync(Window? window, Func<ContentDialog> make)
    {
        var target = Target(window);
        if (target is null)
        {
            LogNoWindow(logger);
            return Task.FromResult(ContentDialogResult.None);
        }
        if (!schedulers.TryGetValue(target, out var scheduler))
        {
            scheduler = new DialogScheduler();
            schedulers[target] = scheduler;
            target.Closed += (_, _) => schedulers.Remove(target);
        }
        return scheduler.Enqueue(async () =>
        {
            if (target.Content?.XamlRoot is not { } xamlRoot)
            {
                LogNoWindow(logger);
                return ContentDialogResult.None;
            }
            var dialog = make();
            dialog.XamlRoot = xamlRoot;
            if (Application.Current.Resources.TryGetValue("DefaultContentDialogStyle", out var style) && style is Style s)
            {
                dialog.Style = s;
            }
            if (target.Content is FrameworkElement root)
            {
                dialog.RequestedTheme = root.ActualTheme;
            }
            try
            {
                return await dialog.ShowAsync();
            }
            catch (Exception e) when (e is InvalidOperationException or System.Runtime.InteropServices.COMException)
            {
                LogShowFailed(logger, e);
                return ContentDialogResult.None;
            }
        });
    }

    // The window a dialog goes on: the given one while it is shown, else
    // the main window, shown first.
    private Window? Target(Window? window)
    {
        if (window is not null && window.AppWindow?.IsVisible == true)
        {
            return window;
        }
        var main = mainWindow();
        if (main is not null && main.AppWindow?.IsVisible != true)
        {
            showMainWindow();
        }
        return main;
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "a dialog has no window to go on")]
    private static partial void LogNoWindow(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "a dialog could not be shown")]
    private static partial void LogShowFailed(ILogger logger, Exception error);
}
