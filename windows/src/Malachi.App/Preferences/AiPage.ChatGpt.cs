// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first ChatGPT preferences from docs/chatgpt-integration.md §8;
// extends AIPaneViewController.swift / ui/internal/window/preferences.go.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading;
using Malachi.App.Assistants;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

public sealed partial class AiPage
{
    private readonly CancellationTokenSource chatGptPageLifetime = new();
    private readonly List<string> chatGptModels = [];
    private bool syncingChatGptModel;
    private bool changingChatGptConnection;
    private int chatGptGeneration;
    private CancellationTokenSource? chatGptRefresh;
    private bool disconnectingChatGpt;
    private bool chatGptUpdateQueued;

    private void InitializeChatGpt(SettingBindings bindings)
    {
        AssistantProviderRow.Header = ChatGptText.Provider;
        AutomationProperties.SetName(AssistantProviderBox, ChatGptText.Provider);
        // Windows-only string: the provider brand is identical across languages.
        const string claudeBrand = "Claude";
        AssistantProviderBox.Items.Add(new ComboBoxItem { Content = claudeBrand });
        AssistantProviderBox.Items.Add(new ComboBoxItem { Content = ChatGptText.Name });
        bindings.Choice(AssistantProviderBox, SettingsKey.AssistantProvider,
            new[] { AssistantProviderID.Claude, AssistantProviderID.ChatGpt },
            () => state.Settings.AssistantProvider, value => state.Settings.AssistantProvider = value);
        ChatGptDescription.Text = ChatGptText.Description;
        CodexRow.Header = ChatGptText.Codex;
        CodexInstallButton.Content = ChatGptText.Install;
        CodexChooseButton.Content = Assistant.PanelTexts().Choose;
        ChatGptAccountRow.Header = L10n.T("Account");
        ChatGptSignInButton.Content = ChatGptText.SignIn;
        ChatGptDisconnectButton.Content = ChatGptText.Disconnect;
        ChatGptModelRow.Header = Assistant.PanelTexts().Model;
        AutomationProperties.SetName(ChatGptModelBox, Assistant.PanelTexts().Model);
        ChatGptUsageRow.Header = ChatGptText.Usage;
        ChatGptUsageButton.Content = ChatGptText.Usage;
        state.InAppProviderChanged += OnInAppProviderChanged;
        state.ChatGpt.Changed += OnChatGptConnectionChanged;
        ShowChatGptConnection();
        RefreshChatGpt();
    }

    private void CloseChatGpt()
    {
        state.InAppProviderChanged -= OnInAppProviderChanged;
        state.ChatGpt.Changed -= OnChatGptConnectionChanged;
        chatGptRefresh?.Cancel();
        chatGptRefresh?.Dispose();
        chatGptRefresh = null;
        chatGptPageLifetime.Cancel();
        chatGptPageLifetime.Dispose();
    }

    private void OnInAppProviderChanged(object? sender, EventArgs e)
    {
        if (closed || chatGptUpdateQueued) return;
        chatGptUpdateQueued = true;
        // Leave SelectionChanged before changing the ComboBox item collection.
        if (!DispatcherQueue.TryEnqueue(() =>
        {
            chatGptUpdateQueued = false;
            if (closed) return;
            UpdateAssistantGroup();
            RefreshChatGpt();
            // The board's model row follows the provider (AiPage.Board.cs).
            RefreshBoardChatGptModels();
            UpdateBoardGroup();
        })) chatGptUpdateQueued = false;
    }

    private void OnChatGptConnectionChanged()
    {
        if (!DispatcherQueue.HasThreadAccess)
        {
            DispatcherQueue.TryEnqueue(OnChatGptConnectionChanged);
            return;
        }
        if (!closed) ShowChatGptConnection();
    }

    private void ShowChatGptConnection()
    {
        var connection = state.ChatGpt.Connection;
        var connected = connection.Status == ChatGptConnectionStatus.Connected;
        var signingIn = connection.Status == ChatGptConnectionStatus.SigningIn;
        ChatGptAccountState.Text = connection.Status switch
        {
            ChatGptConnectionStatus.SigningIn => ChatGptText.Connecting,
            ChatGptConnectionStatus.Connected => ChatGptText.Connected(connection.Email ?? "ChatGPT"),
            ChatGptConnectionStatus.ReconnectRequired => ChatGptText.Reconnect,
            _ => ChatGptText.Disconnected,
        };
        ChatGptSignInButton.IsEnabled = !changingChatGptConnection && !signingIn;
        ChatGptSignInButton.Visibility = connected ? Visibility.Collapsed : Visibility.Visible;
        ChatGptDisconnectButton.IsEnabled = !disconnectingChatGpt;
        ChatGptDisconnectButton.Visibility = connected || signingIn || connection.Status == ChatGptConnectionStatus.ReconnectRequired
            ? Visibility.Visible : Visibility.Collapsed;
        ChatGptUsageButton.IsEnabled = connected;
        ChatGptModelBox.IsEnabled = connected;
    }

    private async void RefreshChatGpt()
    {
        if (closed) return;
        chatGptRefresh?.Cancel();
        chatGptRefresh?.Dispose();
        chatGptRefresh = CancellationTokenSource.CreateLinkedTokenSource(chatGptPageLifetime.Token);
        var cancellation = chatGptRefresh.Token;
        var generation = ++chatGptGeneration;
        ShowChatGptConnection();
        var path = CodexExecutable.Resolve(state.Settings.AssistantCodexPath);
        state.UpdateChatGptAvailability();
        CodexState.Text = path ?? ChatGptText.MissingCodex;
        CodexInstallButton.Visibility = path is null ? Visibility.Visible : Visibility.Collapsed;
        ShowChatGptModels([]);
        if (path is null) return;
        try
        {
            var version = await CodexExecutable.VersionAsync(path, cancellation);
            if (closed || generation != chatGptGeneration) return;
            CodexState.Text = version is null ? path : path + " · " + version;
            if (state.ChatGpt.Connection.Status != ChatGptConnectionStatus.Connected) return;
            var models = await state.Codex.GetModelsAsync(cancellation);
            if (!closed && generation == chatGptGeneration) ShowChatGptModels(models);
        }
        catch (OperationCanceledException) { }
        catch (Exception e) when (e is AssistantProviderException or ChatGptAuthException or IOException
            or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            if (!closed && generation == chatGptGeneration) Describe(ChatGptModelRow, ChatGptText.ModelsUnavailable);
        }
    }

    private void ShowChatGptModels(IReadOnlyList<CodexModel> models)
    {
        syncingChatGptModel = true;
        try
        {
            var selected = state.Settings.AssistantChatGptModel;
            chatGptModels.Clear();
            ChatGptModelBox.Items.Clear();
            chatGptModels.Add("");
            ChatGptModelBox.Items.Add(new ComboBoxItem { Content = ChatGptText.DefaultModel });
            foreach (var model in models)
            {
                if (model.Id.Length == 0 || chatGptModels.Contains(model.Id)) continue;
                chatGptModels.Add(model.Id);
                ChatGptModelBox.Items.Add(new ComboBoxItem { Content = model.DisplayName });
            }
            // Keep a previously chosen ID visible when temporarily absent
            // from the catalog; the runtime still validates actual access.
            if (selected.Length != 0 && !chatGptModels.Contains(selected))
            {
                chatGptModels.Add(selected);
                ChatGptModelBox.Items.Add(new ComboBoxItem { Content = selected });
            }
            ChatGptModelBox.SelectedIndex = chatGptModels.IndexOf(selected);
            Describe(ChatGptModelRow, "");
        }
        finally { syncingChatGptModel = false; }
    }

    private void OnChatGptModelChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!syncingChatGptModel && ChatGptModelBox.SelectedIndex is var index && index >= 0 && index < chatGptModels.Count)
            state.Settings.AssistantChatGptModel = chatGptModels[index];
    }

    private async void OnChatGptSignInClick(object sender, RoutedEventArgs e)
    {
        if (changingChatGptConnection) return;
        changingChatGptConnection = true;
        ShowChatGptConnection();
        try { await state.ChatGpt.SignInAsync(); }
        catch (OperationCanceledException) { }
        catch (ChatGptAuthException) { if (!closed) toasts.Show(ChatGptText.ConnectionFailed); }
        finally
        {
            changingChatGptConnection = disconnectingChatGpt;
            if (!closed) RefreshChatGpt();
        }
    }

    private async void OnChatGptDisconnectClick(object sender, RoutedEventArgs e)
    {
        if (disconnectingChatGpt) return;
        disconnectingChatGpt = true;
        changingChatGptConnection = true;
        ShowChatGptConnection();
        try
        {
            var revoked = await state.ChatGpt.DisconnectAsync();
            if (!closed && !revoked) toasts.Show(ChatGptText.RevocationUnconfirmed);
        }
        catch (OperationCanceledException) { }
        catch (ChatGptAuthException) { if (!closed) toasts.Show(ChatGptText.ConnectionFailed); }
        finally
        {
            disconnectingChatGpt = false;
            changingChatGptConnection = false;
            if (!closed) RefreshChatGpt();
        }
    }

    private async void OnChooseCodexClick(object sender, RoutedEventArgs e)
    {
        if (window() is not { } owner) return;
        var chosen = await ComposeFileDialog.OpenAsync(WindowPresenter.Handle(owner), ChatGptText.Codex, multiple: false, ("codex.exe", "*.exe"));
        if (closed || chosen is not { Count: > 0 }) return;
        if (chosen[0] is not { } path || !CodexExecutable.IsExecutableFile(path))
        {
            toasts.Show(ChatGptText.MissingCodex);
            return;
        }
        state.Settings.AssistantCodexPath = string.Equals(path, CodexExecutable.AutomaticPath(), StringComparison.OrdinalIgnoreCase) ? "" : path;
        RefreshChatGpt();
    }

    private async void OnInstallCodexClick(object sender, RoutedEventArgs e) => await state.OpenUrlAsync(CodexExecutable.InstallUrl);
    private async void OnChatGptUsageClick(object sender, RoutedEventArgs e) => await state.OpenUrlAsync(ChatGptOAuth.UsageUri.AbsoluteUri);
}
