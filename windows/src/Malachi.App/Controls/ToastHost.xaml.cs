// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Shared/ToastPresenter.swift (the view:
// present, fade, capsuleClicked); GTK: Adw.ToastOverlay. The queue and its
// timeouts are Core's ToastPresenter; this draws its Current and fades in
// and out (docs/windows-port.md §11.4). WinUI has no in-app toast, and
// system notifications are something else (a toast here is feedback on
// what the user just did). Narrator reads each toast through a UIA
// notification, as it reads an Adw.Toast.

using System;
using Malachi.App.Shell;
using Malachi.Core.Presentation;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace Malachi.App.Controls;

/// <summary>The toast overlay of one window.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The presenter's timer lives as long as the window; its queue holds nothing to release.")]
public sealed partial class ToastHost : UserControl, IToasts
{
    private static readonly TimeSpan Fade = TimeSpan.FromMilliseconds(200);

    private readonly ToastPresenter presenter = new();
    private readonly DispatcherQueueTimer collapse;

    /// <summary>An overlay with nothing to show.</summary>
    public ToastHost()
    {
        InitializeComponent();
        presenter.CurrentChanged += OnCurrentChanged;
        collapse = DispatcherQueue.GetForCurrentThread().CreateTimer();
        collapse.Interval = Fade;
        collapse.IsRepeating = false;
        collapse.Tick += (_, _) =>
        {
            if (presenter.Current is null)
            {
                Capsule.Visibility = Visibility.Collapsed;
            }
        };
        Unloaded += (_, _) => collapse.Stop();
    }

    /// <summary>The toast shown now, or null (for tests and automation).</summary>
    public string? CurrentText => presenter.Current?.Text;

    /// <inheritdoc/>
    public void Show(string text) => presenter.Show(text);

    /// <inheritdoc/>
    public void Show(string text, int seconds) => presenter.Show(text, seconds);

    /// <summary>Hides the current toast now; the next queued one follows.</summary>
    public void DismissCurrent() => presenter.DismissCurrent();

    private void OnCurrentChanged(object? sender, Toast? toast)
    {
        if (toast is null)
        {
            Capsule.Opacity = 0;
            collapse.Start();
            return;
        }
        collapse.Stop();
        Label.Text = toast.Text;
        ToolTipService.SetToolTip(Capsule, toast.Text);
        Capsule.Visibility = Visibility.Visible;
        Capsule.Opacity = 1;
        Announce(toast.Text);
    }

    private void Announce(string text)
    {
        var peer = FrameworkElementAutomationPeer.FromElement(Capsule) ?? FrameworkElementAutomationPeer.CreatePeerForElement(Capsule);
        peer?.RaiseNotificationEvent(
            AutomationNotificationKind.Other, AutomationNotificationProcessing.ImportantMostRecent, text, "io.github.schotek.Malachi.toast");
    }

    private void OnCapsuleTapped(object sender, TappedRoutedEventArgs e)
    {
        e.Handled = true;
        presenter.DismissCurrent();
    }
}
