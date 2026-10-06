// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Board group of macos/Sources/MalachiMail/Preferences/
// AIPaneViewController.swift (addBoardGroup, bindBoard, updateBoardGroup,
// fill, boardConsentChanged, boardAutoChanged, boardIntervalChosen,
// boardDailyChosen; ChatGPTPreferences' boardModelRow); GTK:
// window/preferences.go (bindBoardTriage, boardGroupFor) and
// preferences_chatgpt.go (bindBoardProviderModel).
//
// Shown while triage is offered (Core's one rule, TriageView.Offered, which
// the board's Triage follows too: the Assistant shown with the In App
// target, and a board the daemon has and has not turned off):
//
// - "Let the assistant refine the board": on asks "Let the Assistant Triage
//   the Board?" on this window first (AppState.ConfirmTriageConsentAsync),
//   then gives the consent (BoardTriageController.GiveConsentAsync: the
//   board's assistant preference first, the keys once the daemon stored
//   it); a declined question leaves it off. Off withdraws it (a run under
//   way stops, automatic triage goes off too, the panel's own consent
//   stays). Turning off is always possible; turning on needs a runnable
//   triage.
// - Model: the triage's own model, board-triage-model for Claude (apart
//   from the panel's assistant-model), board-triage-chatgpt-model from the
//   provider's catalog for ChatGPT (its default first, a stored model the
//   catalog no longer lists kept as an item); the next run takes it.
// - "Triage new mail automatically", "At most every" (15, 30, 60, 180
//   minutes) and "Conversations a day" (20, 60, 150): the daemon's
//   board.preferences through the application's BoardPreferencesController
//   (a value outside the lists shows as an extra item; a refused write is
//   taken back and toasted by the application).
// - Status: why automatic triage pauses, else the last run and today's
//   automatic count (Board.TriageSettingsStatus; Core publishes the view
//   again while it names a relative time).
// - "Tokens in the Last 24 Hours": the sum, the split by kind and the runs
//   (TriageView.UsageValue/UsageDetail), shown once a board.list said them;
//   the board is asked to list again whenever the page comes up.
//
// The group's description says why triage cannot run now
// (Board.TriageSettingsDescription); the Claude Code row above offers what
// Claude Code needs. All texts are Core's (Board.Text, ChatGptBoardText).

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.ChatGPT;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

public sealed partial class AiPage
{
    /// <summary>"At most every": minutes between automatic runs.</summary>
    internal static readonly IReadOnlyList<int> BoardIntervals = [15, 30, 60, 180];

    /// <summary>The daily caps of conversations automatic runs annotate.</summary>
    internal static readonly IReadOnlyList<int> BoardDailyCaps = [20, 60, 150];

    private readonly List<int> boardIntervalItems = [];
    private readonly List<int> boardDailyItems = [];
    private readonly List<string> boardChatGptModels = [];
    private BoardObserverToken? boardTriageToken;
    private CancellationTokenSource? boardModelsRefresh;
    private bool syncingBoard;
    private bool givingBoardConsent;
    private int boardModelsGeneration;

    private BoardTriageController BoardTriage => state.BoardTriage;

    private void InitializeBoardGroup(SettingBindings bindings)
    {
        BoardHeading.Text = Board.Text.BoardName;
        BoardConsentRow.Header = Board.Text.TriageSettingsConsent;
        AutomationProperties.SetName(BoardConsentSwitch, Board.Text.TriageSettingsConsent);
        var model = Assistant.PanelTexts().Model;
        BoardModelRow.Header = model;
        AutomationProperties.SetName(BoardModelBox, model);
        BoardChatGptModelRow.Header = model;
        AutomationProperties.SetName(BoardChatGptModelBox, model);
        foreach (var m in Assistant.Models)
        {
            BoardModelBox.Items.Add(new ComboBoxItem { Content = Assistant.ModelName(m) });
        }
        // A client setting like the panel's model, usable whenever the group
        // is shown (choosing it needs no consent); the next run takes it.
        bindings.Choice(BoardModelBox, SettingsKey.BoardTriageModel, Assistant.Models, () => state.Settings.BoardTriageModel, v => state.Settings.BoardTriageModel = v);
        BoardAutoRow.Header = Board.Text.TriageSettingsAutomatic;
        AutomationProperties.SetName(BoardAutoSwitch, Board.Text.TriageSettingsAutomatic);
        BoardIntervalRow.Header = Board.Text.TriageSettingsInterval;
        AutomationProperties.SetName(BoardIntervalBox, Board.Text.TriageSettingsInterval);
        BoardDailyRow.Header = Board.Text.TriageSettingsDaily;
        AutomationProperties.SetName(BoardDailyBox, Board.Text.TriageSettingsDaily);
        BoardStatusRow.Header = L10n.T("Status");
        BoardUsageRow.Header = Board.Text.TriageSettingsUsage;

        boardTriageToken = BoardTriage.Observe(UpdateBoardGroup);
        // The daemon's preferences, should none have come yet.
        if (BoardTriage.Preferences.Preferences is null)
        {
            BoardTriage.Preferences.Load();
        }
        RefreshBoardChatGptModels();
        UpdateBoardGroup();
    }

    // The page came up: the board lists again (the tokens of the last 24
    // hours age out without a notification), and Claude Code's sign-in is
    // asked afresh (signed in meanwhile).
    private void RefreshBoardGroup()
    {
        BoardTriage.CheckSignIn();
        BoardTriage.RelistBoard();
        RefreshBoardChatGptModels();
        UpdateBoardGroup();
    }

    private void CloseBoardGroup()
    {
        boardTriageToken?.Cancel();
        boardTriageToken = null;
        boardModelsRefresh?.Cancel();
        boardModelsRefresh?.Dispose();
        boardModelsRefresh = null;
    }

    // The group from the triage's view and the board's preferences.
    private void UpdateBoardGroup()
    {
        if (closed)
        {
            return;
        }
        var chatGpt = state.Settings.AssistantProvider == AssistantProviderID.ChatGpt;
        BoardModelRow.Visibility = chatGpt ? Visibility.Collapsed : Visibility.Visible;
        BoardChatGptModelRow.Visibility = chatGpt ? Visibility.Visible : Visibility.Collapsed;
        var v = BoardTriage.View;
        if (!v.Offered)
        {
            BoardGroup.Visibility = Visibility.Collapsed;
            return;
        }
        BoardGroup.Visibility = Visibility.Visible;
        var description = Board.TriageSettingsDescription(v);
        BoardDescription.Text = description;
        BoardDescription.Visibility = description.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        BoardConsentRow.Description = chatGpt ? ChatGptBoardText.BoardConsentBody : Board.Text.TriageSettingsConsentSubtitle;
        var ready = v.Control is Board.TriageControl.Triage or Board.TriageControl.Stop;
        var prefs = BoardTriage.Preferences.Preferences;
        var consent = givingBoardConsent ? BoardConsentSwitch.IsOn : BoardTriage.ConsentGiven;
        var auto = prefs?.AutoTriage ?? false;
        var minutes = prefs?.AutoTriageMinutes ?? BoardLimits.DefaultBoardAutoTriageMinutes;
        var cap = prefs?.AutoTriageDailyCases ?? BoardLimits.DefaultBoardAutoTriageDailyCases;
        syncingBoard = true;
        try
        {
            BoardConsentSwitch.IsOn = consent;
            BoardAutoSwitch.IsOn = auto;
            Fill(BoardIntervalBox, boardIntervalItems, BoardIntervals, minutes, Board.Text.TriageInterval);
            Fill(BoardDailyBox, boardDailyItems, BoardDailyCaps, cap, Board.Text.TriageDailyCap);
        }
        finally
        {
            syncingBoard = false;
        }
        // Turning off is always possible; turning on needs a runnable triage.
        BoardConsentRow.IsEnabled = prefs is not null && !givingBoardConsent && (ready || consent);
        BoardAutoRow.IsEnabled = prefs is not null && !givingBoardConsent && (auto || (consent && ready));
        var schedule = prefs is not null && consent && auto && ready && !givingBoardConsent;
        BoardIntervalRow.IsEnabled = schedule;
        BoardDailyRow.IsEnabled = schedule;
        BoardStatusText.Text = Board.TriageSettingsStatus(v);
        // The tokens of the last 24 hours, from the same view.
        BoardUsageRow.Visibility = v.UsageShown ? Visibility.Visible : Visibility.Collapsed;
        BoardUsageValue.Text = v.UsageValue;
        BoardUsageDetail.Text = v.UsageDetail;
        ToolTipService.SetToolTip(BoardUsageRow, v.UsageToolTip.Length == 0 ? null : v.UsageToolTip);
    }

    /// <summary>
    /// The combo's items: <paramref name="values"/>, and <paramref name="selected"/>
    /// as an extra item when it is not one of them, <paramref name="selected"/> chosen.
    /// </summary>
    private static void Fill(ComboBox combo, List<int> shown, IReadOnlyList<int> values, int selected, Func<int, string> title)
    {
        var wanted = new List<int>(values);
        if (!wanted.Contains(selected))
        {
            wanted.Add(selected);
        }
        if (!SameItems(shown, wanted))
        {
            combo.Items.Clear();
            shown.Clear();
            foreach (var value in wanted)
            {
                shown.Add(value);
                combo.Items.Add(new ComboBoxItem { Content = title(value) });
            }
        }
        combo.SelectedIndex = shown.IndexOf(selected);
    }

    private static bool SameItems(List<int> a, List<int> b)
    {
        if (a.Count != b.Count)
        {
            return false;
        }
        for (var i = 0; i < a.Count; i++)
        {
            if (a[i] != b[i])
            {
                return false;
            }
        }
        return true;
    }

    // Actions

    // On: the question on this window, then the consent given (the board's
    // assistant preference on, once the daemon stored it); a declined
    // question leaves the switch off. Off: withdrawn.
    private async void OnBoardConsentToggled(object sender, RoutedEventArgs e)
    {
        if (syncingBoard || closed || givingBoardConsent)
        {
            return;
        }
        var triage = BoardTriage;
        if (!BoardConsentSwitch.IsOn)
        {
            triage.WithdrawConsent();
            UpdateBoardGroup();
            return;
        }
        givingBoardConsent = true;
        UpdateBoardGroup();
        try
        {
            var allowed = !triage.NeedsConsent || await state.ConfirmTriageConsentAsync(window());
            if (allowed && !closed)
            {
                await triage.GiveConsentAsync();
            }
        }
        finally
        {
            givingBoardConsent = false;
            UpdateBoardGroup();
        }
    }

    // Optimistic; a refused write is taken back and toasted by the application.
    private void OnBoardAutoToggled(object sender, RoutedEventArgs e)
    {
        if (syncingBoard || closed)
        {
            return;
        }
        var on = BoardAutoSwitch.IsOn;
        BoardTriage.Preferences.Update(p => p with { AutoTriage = on });
    }

    private void OnBoardIntervalChanged(object sender, SelectionChangedEventArgs e) =>
        Chosen(BoardIntervalBox, boardIntervalItems, p => p.AutoTriageMinutes, (p, v) => p with { AutoTriageMinutes = v });

    private void OnBoardDailyChanged(object sender, SelectionChangedEventArgs e) =>
        Chosen(BoardDailyBox, boardDailyItems, p => p.AutoTriageDailyCases, (p, v) => p with { AutoTriageDailyCases = v });

    // Writes the chosen item's value, unless the daemon's preferences are
    // not known or already have it.
    private void Chosen(ComboBox combo, List<int> items, Func<BoardPreferences, int> current, Func<BoardPreferences, int, BoardPreferences> set)
    {
        if (syncingBoard || closed)
        {
            return;
        }
        var i = combo.SelectedIndex;
        if (BoardTriage.Preferences.Preferences is not { } prefs || i < 0 || i >= items.Count || current(prefs) == items[i])
        {
            return;
        }
        var value = items[i];
        BoardTriage.Preferences.Update(p => set(p, value));
    }

    // The ChatGPT provider's model of the triage (board-triage-chatgpt-model)

    // The catalog while ChatGPT is connected; without it only the default
    // and the stored model.
    private async void RefreshBoardChatGptModels()
    {
        if (closed)
        {
            return;
        }
        boardModelsRefresh?.Cancel();
        boardModelsRefresh?.Dispose();
        boardModelsRefresh = CancellationTokenSource.CreateLinkedTokenSource(chatGptPageLifetime.Token);
        var cancellation = boardModelsRefresh.Token;
        var generation = ++boardModelsGeneration;
        ShowBoardChatGptModels([]);
        if (state.Settings.AssistantProvider != AssistantProviderID.ChatGpt
            || state.ChatGpt.Connection.Status != ChatGptConnectionStatus.Connected
            || CodexExecutable.Resolve(state.Settings.AssistantCodexPath) is null)
        {
            return;
        }
        try
        {
            var models = await state.Codex.GetModelsAsync(cancellation);
            if (!closed && generation == boardModelsGeneration)
            {
                ShowBoardChatGptModels(models);
            }
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception ex) when (ex is AssistantProviderException or ChatGptAuthException or IOException
            or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            // The default and the stored model stay; the provider's row says why.
        }
    }

    private void ShowBoardChatGptModels(IReadOnlyList<CodexModel> models)
    {
        syncingBoard = true;
        try
        {
            var selected = state.Settings.BoardChatGptModel;
            boardChatGptModels.Clear();
            BoardChatGptModelBox.Items.Clear();
            boardChatGptModels.Add("");
            BoardChatGptModelBox.Items.Add(new ComboBoxItem { Content = Assistants.ChatGptText.DefaultModel });
            foreach (var model in models)
            {
                if (model.Id.Length == 0 || boardChatGptModels.Contains(model.Id))
                {
                    continue;
                }
                boardChatGptModels.Add(model.Id);
                BoardChatGptModelBox.Items.Add(new ComboBoxItem { Content = model.DisplayName });
            }
            if (selected.Length != 0 && !boardChatGptModels.Contains(selected))
            {
                boardChatGptModels.Add(selected);
                BoardChatGptModelBox.Items.Add(new ComboBoxItem { Content = selected });
            }
            BoardChatGptModelBox.SelectedIndex = boardChatGptModels.IndexOf(selected);
        }
        finally
        {
            syncingBoard = false;
        }
    }

    private void OnBoardChatGptModelChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!syncingBoard && BoardChatGptModelBox.SelectedIndex is var i && i >= 0 && i < boardChatGptModels.Count)
        {
            state.Settings.BoardChatGptModel = boardChatGptModels[i];
        }
    }
}
