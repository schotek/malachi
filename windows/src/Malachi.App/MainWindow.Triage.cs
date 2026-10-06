// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+Triage.swift
// (triageChanged, toastTriageEnd, updateTriageStrip, triageStripText,
// triageToolbarItem, boardTriage, triageSignIn); GTK:
// window/board_triage_button.go (wireTriage, onTriageClicked,
// renderTriageButton) and board.go (refreshBoardStatusLabel). The board's
// triage in the main window (the run is the application's
// BoardTriageController, AppState.Triage.cs):
//
// - ✦ Triage in the board's bar (BoardPage.BoardTriageButton): what the
//   triage view's control offers — hidden, Get Claude Code… (Get Codex…),
//   Sign In… (Continue with ChatGPT), unavailable (insensitive, the tooltip
//   says why), ✦ Triage, Stop while a run works with a turning ring — with
//   the view's title and tooltip. Whether it shows at all is Core's one rule
//   (TriageView.Offered, which Preferences → AI → Board follows too). The
//   second click of a double click is ignored (Windows: a click within the
//   system's double-click time of the last one), so a double click starts
//   one run and does not stop it. A run without consent asks with "Let the
//   Assistant Triage the Board?" on this window first (the triage's
//   Consent). Get Claude Code… opens Anthropic's page (Get Codex… the
//   Codex page), Sign In… runs Claude Code's own sign-in in the browser (the
//   application's one sign-in; ChatGPT's connection for the ChatGPT
//   provider), a failure or a timeout as a toast. With
//   MALACHI_BOARD_SAMPLES it stays a placeholder: shown with the samples'
//   assistant on, and a click only says "not in this preview".
// - The strip (BoardPage.BoardTriageStrip, under the board's bar): Core's
//   Board.TriageStripText for Board mode and the board's phase, a turning
//   ring while a run works; in Mail the status line's note carries a run's
//   progress (StatusBarView.SetNote), as macOS's status strip does in both
//   modes. Core publishes the view again while it names a relative time,
//   so the window keeps no clock of its own.
// - Toasts: a manual run's result or failure (not a declined consent: the
//   user just said no). Automatic runs never toast; the strip says enough.
//
// Nothing a run's assistant wrote is shown here: only the Core view's
// counts and classes.

using System;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.ChatGPT;
using Malachi.Core.Controllers;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App;

/// <summary>The board's triage in the main window.</summary>
public sealed partial class MainWindow
{
    private readonly TextBlock triageTitle = new() { VerticalAlignment = VerticalAlignment.Center };
    private readonly ProgressRing triageRing = new() { Width = 16, Height = 16, IsActive = false, Visibility = Visibility.Collapsed };
    private readonly TextBlock triageStripText = new()
    {
        VerticalAlignment = VerticalAlignment.Center,
        MaxLines = 1,
        TextTrimming = TextTrimming.CharacterEllipsis,
        TextWrapping = TextWrapping.NoWrap,
    };

    private readonly ProgressRing triageStripRing = new() { Width = 14, Height = 14, IsActive = false, Visibility = Visibility.Collapsed };
    private BoardObserverToken? triageToken;
    private BoardObserverToken? triageEndedToken;
    private long lastTriageClick = long.MinValue / 2;
    private bool triageSigningIn;

    private BoardTriageController Triage => state.BoardTriage;

    // The samples are not the daemon's: nothing to triage.
    private bool TriageSamples => boardIntegration?.BoardSamples ?? true;

    // From Attach, after AttachBoard: the button, the strip, the observers.
    private void AttachTriage()
    {
        var button = BoardView.BoardTriageButton;
        triageTitle.Text = Board.Text.Triage;
        var content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        content.Children.Add(triageRing);
        content.Children.Add(triageTitle);
        button.Content = content;
        button.Click += OnTriageClick;

        triageStripText.Style = (Style)Application.Current.Resources["CaptionTextBlockStyle"];
        triageStripText.Foreground = (Microsoft.UI.Xaml.Media.Brush)Application.Current.Resources["TextFillColorSecondaryBrush"];
        AutomationProperties.SetLiveSetting(triageStripText, AutomationLiveSetting.Polite);
        var strip = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Padding = new Thickness(12, 4, 12, 4) };
        strip.Children.Add(triageStripRing);
        strip.Children.Add(triageStripText);
        BoardView.BoardTriageStrip.Content = strip;

        BoardView.Changed += (_, _) => RenderTriage();
        // The mode decides what the strip and the status line's note say.
        BoardView.RegisterPropertyChangedCallback(UIElement.VisibilityProperty, (_, _) => RenderTriage());
        if (!TriageSamples)
        {
            triageToken = Triage.Observe(RenderTriage);
            triageEndedToken = Triage.ObserveEnded(OnTriageEnded);
        }
        Closed += (_, _) =>
        {
            triageToken?.Cancel();
            triageEndedToken?.Cancel();
        };
        RenderTriage();
    }

    // Anything the triage view shows changed: the button, the strip, the note.
    private void RenderTriage()
    {
        var button = BoardView.BoardTriageButton;
        var model = BoardView.Controller?.View;
        if (TriageSamples)
        {
            // The samples: the placeholder, shown while their assistant is on.
            button.Visibility = model is { AssistantOn: true } && model.Phase != Board.Phase.Off ? Visibility.Visible : Visibility.Collapsed;
            SetTriageButton(Board.Text.Triage, Board.Text.Triage, enabled: true, running: false);
            ShowTriageStrip(mode == Board.Mode.Board ? model?.StatusLine ?? "" : "", running: false);
            StatusLine.SetNote("");
            return;
        }
        var v = Triage.View;
        button.Visibility = v.Offered ? Visibility.Visible : Visibility.Collapsed;
        SetTriageButton(v.Title, v.ToolTip, v.Enabled && !triageSigningIn, v.Running);
        var phase = model?.Phase ?? Board.Phase.Loading;
        ShowTriageStrip(mode == Board.Mode.Board ? Board.TriageStripText(v, Board.Mode.Board, phase) : "", v.Running);
        StatusLine.SetNote(mode == Board.Mode.Mail ? Board.TriageStripText(v, Board.Mode.Mail, phase) : "");
    }

    private void SetTriageButton(string title, string toolTip, bool enabled, bool running)
    {
        var button = BoardView.BoardTriageButton;
        triageTitle.Text = title;
        AutomationProperties.SetName(button, title);
        ToolTipService.SetToolTip(button, toolTip.Length == 0 ? null : toolTip);
        button.IsEnabled = enabled;
        triageRing.IsActive = running;
        triageRing.Visibility = running ? Visibility.Visible : Visibility.Collapsed;
    }

    private void ShowTriageStrip(string text, bool running)
    {
        triageStripText.Text = text;
        triageStripRing.IsActive = running && text.Length > 0;
        triageStripRing.Visibility = triageStripRing.IsActive ? Visibility.Visible : Visibility.Collapsed;
        BoardView.BoardTriageStrip.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    // A run ended: a manual one's result as a toast, unless the user
    // declined the consent. The state still says the run is active here;
    // the result is the view's right after.
    private void OnTriageEnded()
    {
        if (Triage.LastEnded is not { } e
            || e.Trigger != Board.TriageTrigger.Manual
            || e.Failure == Board.TriageFailure.Declined)
        {
            return;
        }
        DispatcherQueue.TryEnqueue(() =>
        {
            var text = Triage.View.Result;
            if (text.Length > 0)
            {
                Toasts.Show(text);
            }
        });
    }

    // The bar's Triage: what the view offers.
    private void OnTriageClick(object sender, RoutedEventArgs e)
    {
        if (mode != Board.Mode.Board)
        {
            return;
        }
        // The second click of a double click: the first one acted (and made
        // Triage a Stop under the pointer).
        var now = Environment.TickCount64;
        var second = now - lastTriageClick < DoubleClickMilliseconds();
        lastTriageClick = now;
        if (second)
        {
            return;
        }
        if (TriageSamples)
        {
            Toasts.Show(Board.Text.Later);
            return;
        }
        var chatGpt = state.Settings.AssistantProvider == AssistantProviderID.ChatGpt;
        switch (Triage.View.Control)
        {
            case Board.TriageControl.Triage:
                Triage.Start(Board.TriageTrigger.Manual);
                break;
            case Board.TriageControl.Stop:
                Triage.Cancel();
                break;
            case Board.TriageControl.GetClaudeCode:
                _ = state.OpenUrlAsync(chatGpt ? CodexExecutable.InstallUrl : Assistant.InstallUrl);
                break;
            case Board.TriageControl.SignIn:
                TriageSignIn(chatGpt);
                break;
            default:
                break;
        }
    }

    // Sign In…: Claude Code's own sign-in in the browser (the application's
    // locator, as the panel and Preferences run it; the triage asks the
    // sign-in again by itself when it ends), or ChatGPT's connection. A
    // failure or a timeout is a toast, as Preferences says it.
    private async void TriageSignIn(bool chatGpt)
    {
        if (triageSigningIn)
        {
            return;
        }
        triageSigningIn = true;
        RenderTriage();
        try
        {
            if (chatGpt)
            {
                if (state.ChatGpt.Connection.Status == ChatGptConnectionStatus.SigningIn)
                {
                    return;
                }
                try
                {
                    await state.ChatGpt.SignInAsync();
                }
                catch (OperationCanceledException)
                {
                }
                catch (ChatGptAuthException)
                {
                    Toasts.Show(Assistants.ChatGptText.ConnectionFailed);
                }
                return;
            }
            // The application has one sign-in: a second would end the first.
            if (state.ClaudeCode.SigningIn)
            {
                return;
            }
            switch (await state.ClaudeCode.SignInAsync())
            {
                case ClaudeCodeSignIn.Failed failed:
                    Toasts.Show(Assistant.SignInFailedText(failed.Reason));
                    break;
                case ClaudeCodeSignIn.TimedOut:
                    Toasts.Show(Assistant.SignInTexts().TimedOut);
                    break;
                default:
                    break;
            }
        }
        finally
        {
            triageSigningIn = false;
            RenderTriage();
        }
    }

    // The system's double-click time, in milliseconds.
    private static uint DoubleClickMilliseconds()
    {
        try
        {
            return new global::Windows.UI.ViewManagement.UISettings().DoubleClickTime;
        }
        catch (System.Runtime.InteropServices.COMException)
        {
            return 500;
        }
    }
}
