// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the triage's part of macos/Sources/MalachiMail/App/AppState.swift
// (triage, autoTriage, wireBoardTriage, confirmTriageConsent,
// stopBoardTriage, providerChanged) and AppDelegate.swift (the schedule
// started at launch, stopBoardTriage on quit); GTK: window/board_triage.go
// (AttachBoardTriage, StopBoardTriage, askConsent, toast). The board's
// triage, once for the application (docs/mcp.md "The board's triage run in
// the app", docs/security.md §10.2): one BoardTriageController over a
// one-shot request of its own (not the panel's, the rewrite's or the
// search's), the bundled bridge and the daemon's socket, and one
// BoardAutoTriageScheduler, started at once (GTK: automatic triage needs no
// first entry into Board; the main window's board source starts when the
// triage wants its data, Integration.Triage.cs).
//
// What the controller needs is fed here: the In App provider (a change of
// the ChatGPT selection, its runtime or its connection is a ProviderChanged,
// AppState.ChatGpt.cs), the consent question ("Let the Assistant Triage the
// Board?" on the main window, the ChatGPT provider's own heading and body),
// and a refused write of the board's preferences as a toast where the user
// looks. Quitting stops the schedule and ends a run under way, waiting for
// board.runEnd at most BoardTriageController.EndWait (QuitSteps.StopTriage).

using System;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Malachi.Core.Controllers;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

/// <summary>The board's triage of the application.</summary>
public sealed partial class AppState
{
    private bool triageStopped;

    /// <summary>The board's triage run (the board's Triage, the strip, Preferences → AI → Board).</summary>
    public BoardTriageController BoardTriage { get; private set; } = null!;

    /// <summary>Starts the automatic runs (board.preferences autoTriage, off by default).</summary>
    public BoardAutoTriageScheduler BoardAutoTriage { get; private set; } = null!;

    // After InitializeAssistant and the board's preferences.
    private void InitializeTriage()
    {
        var request = NewAssistantRequest();
        BoardTriage = new BoardTriageController(
            Client,
            Settings,
            ClaudeCode,
            BoardPreferences,
            request,
            Assistant,
            Paths.McpBridge,
            Paths.Socket,
            logger: Logs.CreateLogger<BoardTriageController>());
        BoardTriage.Consent = () => ConfirmTriageConsentAsync(MainWindow);
        BoardPreferences.ToastRequested += OnBoardPreferencesToast;
        InAppProviderChanged += OnTriageProviderChanged;
        BoardTriage.CheckSignIn();
        BoardAutoTriage = new BoardAutoTriageScheduler(BoardTriage, logger: Logs.CreateLogger<BoardAutoTriageScheduler>());
        BoardAutoTriage.Start();
    }

    /// <summary>
    /// "Let the Assistant Triage the Board?" on <paramref name="window"/> (the
    /// ChatGPT provider's own heading and body; over an ai account the
    /// board's heading with the sentence naming where the mail goes,
    /// AssistantController.ApiBoardConsentBody), with the panel's Allow and
    /// Cancel; true allows. A provider switched while it was up allows nothing.
    /// The manual run asks on the main window, Preferences → AI on its own;
    /// the triage records an account's consent through the provider.
    /// </summary>
    public async Task<bool> ConfirmTriageConsentAsync(Window? window)
    {
        var provider = Settings.AssistantProvider;
        var generation = Assistant.ProviderGeneration;
        var runtime = InAppProvider;
        var texts = Core.Assistants.Assistant.PanelTexts();
        string heading;
        string body;
        switch (Assistant.Source)
        {
            case AssistantProviderSource.ChatGpt:
                heading = ChatGptBoardText.BoardConsentHeading;
                body = ChatGptBoardText.BoardConsentBody;
                break;
            case AssistantProviderSource.Account:
                heading = Core.Boards.Board.Text.TriageConsentHeading;
                body = Assistant.ApiAccount is { } acc
                    ? AssistantController.ApiBoardConsentBody(acc)
                    : Core.I18n.GettextFormat.Format(Core.Assistants.Assistant.ApiText().BoardConsentBody, [Assistant.ApiBrand]);
                break;
            default:
                heading = Core.Boards.Board.Text.TriageConsentHeading;
                body = Core.Boards.Board.Text.TriageConsentBody;
                break;
        }
        var allowed = await Alerts.ConfirmAsync(window, heading, body, texts.Allow, Core.I18n.L10n.T("_Cancel"));
        return allowed && !triageStopped && Settings.AssistantProvider == provider
            && generation == Assistant.ProviderGeneration && ReferenceEquals(runtime, InAppProvider);
    }

    /// <summary>
    /// Quitting (QuitSteps.StopTriage): no new run, and one under way ends as
    /// cancelled, its board.runEnd waited for at most BoardTriageController.EndWait.
    /// </summary>
    public async Task StopBoardTriageAsync()
    {
        if (triageStopped)
        {
            return;
        }
        triageStopped = true;
        BoardAutoTriage.Stop();
        await BoardTriage.CancelAndEndAsync();
    }

    // The request runs the selected provider; a new selection, runtime or
    // connection stops a run and is asked about again (Swift providerChanged;
    // a switch of the provider itself turns automatic triage off, which the
    // controller does on its own for assistant-provider).
    private void OnTriageProviderChanged(object? sender, EventArgs e)
    {
        BoardTriage.Request.Provider = InAppProvider;
        BoardTriage.ProviderChanged();
    }

    private void OnBoardPreferencesToast(object? sender, string text) => Toasts.Show(text);

    private void CloseTriage()
    {
        triageStopped = true;
        BoardPreferences.ToastRequested -= OnBoardPreferencesToast;
        BoardAutoTriage?.Dispose();
        BoardTriage?.Close();
        BoardTriage?.Request.Dispose();
    }
}
