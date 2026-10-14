// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first wiring from docs/chatgpt-integration.md; extends the port of
// AppState.swift / ui/main.go without moving provider or OAuth rules into UI.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.App.Assistants;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Malachi.Core.Daemon;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Platform.Windows.ChatGPT;
using Malachi.Platform.Windows.Files;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

public sealed partial class AppState
{
    private readonly List<SettingsChangeToken> chatGptSettings = [];
    private readonly CancellationTokenSource chatGptLifetime = new();
    private string? lastChatGptConnection;
    private ChatGptConnectionStatus lastChatGptStatus;
    private bool chatGptClosed;

    /// <summary>The account service; widgets read metadata only.</summary>
    public ChatGptConnectionService ChatGpt { get; private set; } = null!;

    /// <summary>The verified runtime for the application's ChatGPT grant.</summary>
    public CodexAssistantProvider Codex { get; private set; } = null!;

    /// <summary>Null preserves the existing Claude execution path.</summary>
    public IAssistantProvider? InAppProvider => Settings.AssistantProvider == AssistantProviderID.ChatGpt ? Codex : null;

    /// <summary>Selection, executable, consent withdrawn or connected account changed (not a model: it applies from the next request).</summary>
    public event EventHandler? InAppProviderChanged;

    private void InitializeChatGpt()
    {
        ChatGpt = WindowsChatGptConnection.Create(Path.Combine(Paths.DataDir, "ChatGPT"));
        RebuildCodexProvider();
        try { Codex.CleanAbandonedSessions(); }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            // Each future session still verifies the private directory
            // policy before starting a child; cleanup never weakens it.
        }
        ChatGpt.Changed += ChatGptConnectionChanged;
        // Not the ChatGPT models: the provider reads its model when a request
        // starts, so a new one applies from the next request and stops
        // nothing under way (Swift providerChangeConcernsActive).
        foreach (var key in new[] { SettingsKey.AssistantProvider, SettingsKey.AssistantTarget, SettingsKey.AssistantCodexPath })
        {
            chatGptSettings.Add(Settings.OnChange(key, InAppSelectionChanged));
        }
        chatGptSettings.Add(Settings.OnChange(SettingsKey.AssistantChatGptConsentVersion, () =>
        {
            // Accepting the current request's consent must not cancel it;
            // withdrawing or replacing that consent does end every request.
            if (Settings.AssistantChatGptConsentVersion != 1) InAppSelectionChanged();
        }));
        _ = InitializeChatGptAsync();
    }

    private async Task InitializeChatGptAsync()
    {
        try { await ChatGpt.InitializeAsync(chatGptLifetime.Token); }
        catch (OperationCanceledException) { }
        catch (ChatGptAuthException) { /* Remains disconnected; preferences offer a fresh connection. */ }
        if (!chatGptClosed) UpdateChatGptAvailability();
    }

    private void RebuildCodexProvider()
    {
        Codex = new CodexAssistantProvider(new CodexAssistantOptions
        {
            Executable = () => CodexExecutable.Resolve(Settings.AssistantCodexPath),
            Bridge = Paths.McpBridge ?? "",
            Socket = Paths.Socket,
            Directory = Path.Combine(Paths.AssistantDir, "chatgpt"),
            Environment = ProcessEnvironment.Copy(ProcessEnvironment.Current()),
            Model = () => Settings.AssistantChatGptModel,
            HasConsent = () => Settings.AssistantChatGptConsentVersion == 1,
            AcceptConsent = () => Settings.AssistantChatGptConsentVersion = 1,
            // The board's own consent for the ChatGPT provider (the triage's run).
            HasBoardConsent = () => Settings.BoardChatGptConsentVersion == 1,
            AcceptBoardConsent = () => Settings.BoardChatGptConsentVersion = 1,
        }, ChatGpt, directories: new PrivateDirectory());
    }

    private void InAppSelectionChanged()
    {
        if (chatGptClosed) return;
        RebuildCodexProvider();
        UpdateChatGptAvailability();
        InAppProviderChanged?.Invoke(this, EventArgs.Empty);
    }

    private void ChatGptConnectionChanged()
    {
        if (!Dispatcher.HasThreadAccess)
        {
            Dispatcher.TryEnqueue(ChatGptConnectionChanged);
            return;
        }
        if (chatGptClosed) return;
        var connection = ChatGpt.Connection;
        if (lastChatGptConnection != connection.ConnectionId || lastChatGptStatus != connection.Status)
        {
            lastChatGptConnection = connection.ConnectionId;
            lastChatGptStatus = connection.Status;
            RebuildCodexProvider();
            InAppProviderChanged?.Invoke(this, EventArgs.Empty);
        }
        UpdateChatGptAvailability();
    }

    /// <summary>Refresh executable/connection availability without starting inference.</summary>
    public void UpdateChatGptAvailability()
    {
        var found = CodexExecutable.Resolve(Settings.AssistantCodexPath) is not null;
        var connected = ChatGpt.Connection.Status == ChatGptConnectionStatus.Connected;
        Assistant.SetInAppProviderAvailable(found && connected, !found ? ChatGptText.MissingCodex : connected ? "" : ChatGptText.Reconnect);
    }

    /// <summary>The active provider's disclosure, without reusing Anthropic's approval.</summary>
    public async Task<bool> AskAssistantConsentAsync(Window? window)
    {
        var provider = Settings.AssistantProvider;
        var runtime = InAppProvider;
        var texts = Core.Assistants.Assistant.PanelTexts();
        var accepted = await Alerts.ConfirmAsync(window,
            provider == AssistantProviderID.ChatGpt ? ChatGptText.ConsentHeading : texts.ConsentHeading,
            provider == AssistantProviderID.ChatGpt ? ChatGptText.ConsentBody : texts.ConsentBody,
            texts.Allow, Core.I18n.L10n.T("_Cancel"));
        return accepted && !chatGptClosed && Settings.AssistantProvider == provider && ReferenceEquals(runtime, InAppProvider);
    }

    /// <summary>Compose asks before opening the editor flyout, which a dialog would close.</summary>
    public async Task<bool> EnsureAssistantConsentAsync(Window? window)
    {
        var provider = InAppProvider;
        if (provider?.HasConsent ?? Settings.AssistantConsent) return true;
        if (!await AskAssistantConsentAsync(window) || !ReferenceEquals(provider, InAppProvider)) return false;
        if (provider is null) Settings.AssistantConsent = true;
        else provider.AcceptConsent();
        return true;
    }

    private void CloseChatGpt()
    {
        chatGptClosed = true;
        foreach (var token in chatGptSettings) token.Cancel();
        chatGptSettings.Clear();
        chatGptLifetime.Cancel();
        ChatGpt.Changed -= ChatGptConnectionChanged;
        ChatGpt.Dispose();
        chatGptLifetime.Dispose();
        InAppProviderChanged = null;
    }
}
