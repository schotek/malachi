// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AIPaneViewController.swift
// (the Assistant Uses row, the Model row by source) and ProviderModelBinding.swift
// (the ChatGPT branch); GTK: ui/internal/window/preferences.go
// (bindAssistantSource, choiceListFactory) and preferences_chatgpt.go
// (bindProviderModel). Assistant Uses keeps assistant-provider: its entries
// are Claude Code, ChatGPT and every AI provider account of the daemon
// (AssistantController.ProviderChoices), an entry that cannot run
// insensitive with why under its name; the row's subtitle is that reason for
// the chosen entry, else the row's own line. A chosen account the daemon no
// longer has is listed as "Removed account". The entries are rebuilt outside
// the combo box's selection change (QueueProviderUpdate). Model over ChatGPT
// (assistant-chatgpt-model) lists the provider's default and Codex's
// catalogue while ChatGPT is connected (CodexModelChoice).
//
// Windows: GTK's popup shows an entry that cannot run insensitive with why
// as its tooltip; a disabled ComboBoxItem shows no tooltip, so the reason
// follows the entry's name after " · " (windows/README.md).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using Malachi.App.Assistants;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

public sealed partial class AiPage
{
    private readonly CancellationTokenSource chatGptPageLifetime = new();
    private readonly List<ProviderChoice> providerChoices = [];
    private CodexModelChoice? chatGptModel;
    private SettingsChangeToken? providerKeyToken;
    private CancellationTokenSource? chatGptRefresh;
    private int chatGptGeneration;
    private bool syncingProvider;
    private bool providerUpdateQueued;

    private void InitializeProvider()
    {
        var x = Assistant.ApiText();
        AssistantProviderRow.Header = x.UsesRow;
        AutomationProperties.SetName(AssistantProviderBox, x.UsesRow);
        AssistantProviderBox.SelectionChanged += OnAssistantProviderChanged;
        var model = Assistant.PanelTexts().Model;
        AssistantChatGptModelRow.Header = model;
        AutomationProperties.SetName(AssistantChatGptModelBox, model);
        chatGptModel = new CodexModelChoice(
            AssistantChatGptModelBox, () => state.Settings.AssistantChatGptModel, v => state.Settings.AssistantChatGptModel = v);
        providerKeyToken = state.Settings.OnChange(SettingsKey.AssistantProvider, QueueProviderUpdate);
        state.InAppProviderChanged += OnInAppProviderChanged;
        state.ChatGpt.Changed += OnChatGptConnectionChanged;
        UpdateProviderChoices();
        RefreshChatGptModels();
    }

    private void CloseProvider()
    {
        state.InAppProviderChanged -= OnInAppProviderChanged;
        state.ChatGpt.Changed -= OnChatGptConnectionChanged;
        providerKeyToken?.Cancel();
        providerKeyToken = null;
        chatGptRefresh?.Cancel();
        chatGptRefresh?.Dispose();
        chatGptRefresh = null;
        chatGptPageLifetime.Cancel();
        chatGptPageLifetime.Dispose();
    }

    // The page came up: ChatGPT's availability (Codex installed meanwhile),
    // the entries and ChatGPT's catalogue again.
    private void RefreshProvider()
    {
        state.UpdateChatGptAvailability();
        UpdateProviderChoices();
        RefreshChatGptModels();
    }

    private void OnInAppProviderChanged(object? sender, EventArgs e)
    {
        if (closed)
        {
            return;
        }
        QueueProviderUpdate();
        // Leave SelectionChanged before changing the combo boxes' items.
        DispatcherQueue.TryEnqueue(() =>
        {
            if (closed)
            {
                return;
            }
            UpdateAssistantGroup();
            RefreshChatGptModels();
            // The board's model row follows the provider (AiPage.Board.cs).
            RefreshBoardChatGptModels();
            UpdateBoardGroup();
        });
    }

    private void OnChatGptConnectionChanged()
    {
        if (!DispatcherQueue.HasThreadAccess)
        {
            DispatcherQueue.TryEnqueue(OnChatGptConnectionChanged);
            return;
        }
        if (!closed)
        {
            RefreshChatGptModels();
        }
    }

    // Assistant Uses (bindAssistantSource)

    // deferChoiceUpdate: the entries are rebuilt once the selection change
    // under way is over.
    private void QueueProviderUpdate()
    {
        if (closed || providerUpdateQueued)
        {
            return;
        }
        providerUpdateQueued = true;
        if (!DispatcherQueue.TryEnqueue(() =>
        {
            providerUpdateQueued = false;
            if (!closed)
            {
                UpdateProviderChoices();
                UpdateAssistantGroup();
                UpdateBoardGroup();
            }
        }))
        {
            providerUpdateQueued = false;
        }
    }

    private void UpdateProviderChoices()
    {
        if (closed)
        {
            return;
        }
        var x = Assistant.ApiText();
        var current = state.Settings.AssistantProvider;
        var choices = new List<ProviderChoice>(state.Assistant.ProviderChoices());
        if (!choices.Any(c => c.Id == current))
        {
            choices.Add(new ProviderChoice(current, x.AccountMissing));
        }
        var subtitle = x.UsesSubtitle;
        var selected = 0;
        for (var i = 0; i < choices.Count; i++)
        {
            var c = choices[i];
            if (c.Id == current)
            {
                selected = i;
                if (!c.Available && c.Problem.Length > 0)
                {
                    subtitle = c.Problem;
                }
            }
        }
        // Nothing to run on (Assistant.NothingSetUp): the row says where an
        // assistant comes from; UpdateAssistantGroup greys the group out.
        if (Assistant.NothingSetUp(choices, BridgeRegistered, BridgeKnown))
        {
            subtitle = x.UsesSubtitle;
        }
        syncingProvider = true;
        try
        {
            if (!choices.SequenceEqual(providerChoices))
            {
                AssistantProviderBox.Items.Clear();
                foreach (var c in choices)
                {
                    AssistantProviderBox.Items.Add(ChoiceItem(c));
                }
                providerChoices.Clear();
                providerChoices.AddRange(choices);
            }
            // The closed box shows the chosen entry's name alone (its reason
            // is the row's description), the list the reasons of the others.
            for (var i = 0; i < choices.Count && i < AssistantProviderBox.Items.Count; i++)
            {
                if (AssistantProviderBox.Items[i] is ComboBoxItem item)
                {
                    var label = ChoiceLabel(choices[i], i == selected);
                    if (!Equals(item.Content, label))
                    {
                        item.Content = label;
                    }
                }
            }
            if (AssistantProviderBox.SelectedIndex != selected)
            {
                AssistantProviderBox.SelectedIndex = selected;
            }
        }
        finally
        {
            syncingProvider = false;
        }
        Describe(AssistantProviderRow, subtitle);
    }

    // choiceListFactory: the name; an entry that cannot run cannot be
    // chosen and says why after its name in the list (a disabled item shows
    // no tooltip); the chosen entry shows its name alone, which is what the
    // closed box shows (its reason is the row's description).
    private static string ChoiceLabel(ProviderChoice c, bool chosen) =>
        chosen || c.Available || c.Problem.Length == 0 ? c.Name : c.Name + " · " + c.Problem;

    private static ComboBoxItem ChoiceItem(ProviderChoice c)
    {
        var item = new ComboBoxItem { Content = ChoiceLabel(c, chosen: false), IsEnabled = c.Available };
        AutomationProperties.SetName(item, c.Name);
        if (c.Problem.Length > 0)
        {
            AutomationProperties.SetHelpText(item, c.Problem);
        }
        return item;
    }

    private void OnAssistantProviderChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncingProvider || closed)
        {
            return;
        }
        var i = AssistantProviderBox.SelectedIndex;
        if (i < 0 || i >= providerChoices.Count)
        {
            return;
        }
        var c = providerChoices[i];
        if (c.Id != state.Settings.AssistantProvider)
        {
            state.Settings.AssistantProvider = c.Id;
        }
    }

    // Model over ChatGPT (bindProviderModel with ChatGPT in effect)

    // The catalogue while ChatGPT is connected and Codex is there; without
    // it only the default and the stored model.
    private async void RefreshChatGptModels()
    {
        if (closed || chatGptModel is not { } choice)
        {
            return;
        }
        chatGptRefresh?.Cancel();
        chatGptRefresh?.Dispose();
        chatGptRefresh = CancellationTokenSource.CreateLinkedTokenSource(chatGptPageLifetime.Token);
        var cancellation = chatGptRefresh.Token;
        var generation = ++chatGptGeneration;
        choice.Show([]);
        var connected = state.ChatGpt.Connection.Status == ChatGptConnectionStatus.Connected;
        AssistantChatGptModelBox.IsEnabled = connected;
        Describe(AssistantChatGptModelRow, "");
        if (state.Assistant.Source != AssistantProviderSource.ChatGpt || !connected
            || CodexExecutable.Resolve(state.Settings.AssistantCodexPath) is null)
        {
            return;
        }
        try
        {
            var models = await state.Codex.GetModelsAsync(cancellation);
            if (!closed && generation == chatGptGeneration)
            {
                choice.Show(models);
            }
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception e) when (e is AssistantProviderException or ChatGptAuthException or IOException
            or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            if (!closed && generation == chatGptGeneration)
            {
                Describe(AssistantChatGptModelRow, ChatGptText.ModelsUnavailable);
            }
        }
    }
}
