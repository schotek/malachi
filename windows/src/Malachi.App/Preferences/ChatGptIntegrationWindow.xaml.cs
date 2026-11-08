// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/ChatGPTIntegrationSheetController.swift;
// GTK: ui/internal/window/chatgpt_integration.go (newChatGPTIntegration,
// bind) and preferences_chatgpt.go (bindProviderModel). The settings of
// ChatGPT through Codex (Preferences → Integrations → ChatGPT (Codex), its
// Settings and the catalogue's Add…; docs/chatgpt-integration.md §8),
// moved here unchanged from the AI page (docs/plans/ai-api.md §7.2): the
// codex.exe it runs (Get Codex…, Choose…), the connection (Continue with
// ChatGPT, Disconnect), the model of the assistant in the app over ChatGPT
// (assistant-chatgpt-model, the provider's default and Codex's catalogue
// while connected) and Manage usage. The choice between Claude Code, ChatGPT
// and an AI provider account is the AI page's Assistant Uses, not this.
//
// Windows (docs/windows-port.md §11.3): an owned modal window (ModalDialog)
// of the Adw.Dialog's 560×460 with Close at the bottom; its close button,
// Escape and Ctrl+W close it; a sign-in under way goes on in the browser.

using System;
using System.IO;
using System.Threading;
using Malachi.App.Assistants;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;

namespace Malachi.App.Preferences;

/// <summary>The settings of ChatGPT (Codex).</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The cancellation sources are disposed with the window (Closed).")]
public sealed partial class ChatGptIntegrationWindow : Window
{
    /// <summary>chatgpt_integration.blp content-width.</summary>
    public const int ContentWidth = 560;

    /// <summary>chatgpt_integration.blp content-height.</summary>
    public const int ContentHeight = 460;

    private readonly AppState state;
    private readonly ILogger logger;
    private readonly ModalDialog modal;
    private readonly CodexModelChoice model;
    private readonly CancellationTokenSource lifetime = new();
    private CancellationTokenSource? refresh;
    private int generation;
    private bool changingConnection;
    private bool disconnecting;
    private bool closed;

    private ChatGptIntegrationWindow(AppState state)
    {
        this.state = state;
        logger = state.Logs.CreateLogger<ChatGptIntegrationWindow>();
        InitializeComponent();
        var title = ChatGptText.Name;
        Title = title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = title;
        Header.Loaded += (_, _) => Title = title;
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);

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
        model = new CodexModelChoice(ChatGptModelBox, () => state.Settings.AssistantChatGptModel, v => state.Settings.AssistantChatGptModel = v);
        state.ChatGpt.Changed += OnConnectionChanged;

        Root.Loaded += (_, _) => (ChatGptSignInButton.Visibility == Visibility.Visible ? ChatGptSignInButton : CloseButton).Focus(FocusState.Programmatic);
        Closed += (_, _) =>
        {
            closed = true;
            state.ChatGpt.Changed -= OnConnectionChanged;
            refresh?.Cancel();
            refresh?.Dispose();
            refresh = null;
            lifetime.Cancel();
            lifetime.Dispose();
            if (modal.ReturnToOwner() is { } activated)
            {
                LogReturned(logger, activated);
            }
        };
        ShowConnection();
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>
    /// Opens the window over <paramref name="owner"/> (the main window when
    /// null or hidden, shown first) and looks at Codex and the connection.
    /// Null when there is no window to open it over.
    /// </summary>
    public static ChatGptIntegrationWindow? Show(AppState state, Window? owner)
    {
        ArgumentNullException.ThrowIfNull(state);
        if (owner is null || !owner.AppWindow.IsVisible)
        {
            state.ShowMainWindow();
            owner = state.MainWindow;
        }
        if (owner is null || state.IsStopping)
        {
            return null;
        }
        var w = new ChatGptIntegrationWindow(state);
        w.modal.Present(owner, ContentWidth, ContentHeight);
        w.Refresh();
        LogPresented(w.logger);
        return w;
    }

    private void OnConnectionChanged()
    {
        if (!DispatcherQueue.HasThreadAccess)
        {
            DispatcherQueue.TryEnqueue(OnConnectionChanged);
            return;
        }
        if (!closed)
        {
            ShowConnection();
            Refresh();
        }
    }

    private void ShowConnection()
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
        ChatGptSignInButton.IsEnabled = !changingConnection && !signingIn;
        ChatGptSignInButton.Visibility = connected ? Visibility.Collapsed : Visibility.Visible;
        ChatGptDisconnectButton.IsEnabled = !disconnecting;
        ChatGptDisconnectButton.Visibility = connected || signingIn || connection.Status == ChatGptConnectionStatus.ReconnectRequired
            ? Visibility.Visible : Visibility.Collapsed;
        ChatGptUsageButton.IsEnabled = connected;
        ChatGptModelBox.IsEnabled = connected;
    }

    // Codex (its path and version), then the catalogue while connected.
    private async void Refresh()
    {
        if (closed)
        {
            return;
        }
        refresh?.Cancel();
        refresh?.Dispose();
        refresh = CancellationTokenSource.CreateLinkedTokenSource(lifetime.Token);
        var cancellation = refresh.Token;
        var my = ++generation;
        ShowConnection();
        var path = CodexExecutable.Resolve(state.Settings.AssistantCodexPath);
        state.UpdateChatGptAvailability();
        CodexState.Text = path ?? ChatGptText.MissingCodex;
        CodexInstallButton.Visibility = path is null ? Visibility.Visible : Visibility.Collapsed;
        model.Show([]);
        ChatGptModelRow.ClearValue(CommunityToolkit.WinUI.Controls.SettingsCard.DescriptionProperty);
        if (path is null)
        {
            return;
        }
        try
        {
            var version = await CodexExecutable.VersionAsync(path, cancellation);
            if (closed || my != generation)
            {
                return;
            }
            CodexState.Text = version is null ? path : path + " · " + version;
            if (state.ChatGpt.Connection.Status != ChatGptConnectionStatus.Connected)
            {
                return;
            }
            var models = await state.Codex.GetModelsAsync(cancellation);
            if (!closed && my == generation)
            {
                model.Show(models);
            }
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception e) when (e is AssistantProviderException or ChatGptAuthException or IOException
            or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            if (!closed && my == generation)
            {
                ChatGptModelRow.Description = ChatGptText.ModelsUnavailable;
            }
        }
    }

    private async void OnChatGptSignInClick(object sender, RoutedEventArgs e)
    {
        if (changingConnection)
        {
            return;
        }
        changingConnection = true;
        ShowConnection();
        try
        {
            await state.ChatGpt.SignInAsync();
        }
        catch (OperationCanceledException)
        {
        }
        catch (ChatGptAuthException)
        {
            if (!closed)
            {
                ToastsHost.Show(ChatGptText.ConnectionFailed);
            }
        }
        finally
        {
            changingConnection = disconnecting;
            if (!closed)
            {
                Refresh();
            }
        }
    }

    private async void OnChatGptDisconnectClick(object sender, RoutedEventArgs e)
    {
        if (disconnecting)
        {
            return;
        }
        disconnecting = true;
        changingConnection = true;
        ShowConnection();
        try
        {
            var revoked = await state.ChatGpt.DisconnectAsync();
            if (!closed && !revoked)
            {
                ToastsHost.Show(ChatGptText.RevocationUnconfirmed);
            }
        }
        catch (OperationCanceledException)
        {
        }
        catch (ChatGptAuthException)
        {
            if (!closed)
            {
                ToastsHost.Show(ChatGptText.ConnectionFailed);
            }
        }
        finally
        {
            disconnecting = false;
            changingConnection = false;
            if (!closed)
            {
                Refresh();
            }
        }
    }

    private async void OnChooseCodexClick(object sender, RoutedEventArgs e)
    {
        var chosen = await ComposeFileDialog.OpenAsync(WindowPresenter.Handle(this), ChatGptText.Codex, multiple: false, (CodexFileName, "*.exe"));
        if (closed || chosen is not { Count: > 0 })
        {
            return;
        }
        if (chosen[0] is not { } path || !CodexExecutable.IsExecutableFile(path))
        {
            ToastsHost.Show(ChatGptText.MissingCodex);
            return;
        }
        state.Settings.AssistantCodexPath = string.Equals(path, CodexExecutable.AutomaticPath(), StringComparison.OrdinalIgnoreCase) ? "" : path;
        Refresh();
    }

    private async void OnInstallCodexClick(object sender, RoutedEventArgs e) => await state.OpenUrlAsync(CodexExecutable.InstallUrl);

    private async void OnChatGptUsageClick(object sender, RoutedEventArgs e) => await state.OpenUrlAsync(ChatGptOAuth.UsageUri.AbsoluteUri);

    private void OnCloseClick(object sender, RoutedEventArgs e) => Close();

    // The name of the dialog's filter: the file Codex is (a name, not translated).
    private const string CodexFileName = "codex.exe";

    [LoggerMessage(Level = LogLevel.Information, Message = "ChatGPT integration settings opened")]
    private static partial void LogPresented(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "ChatGPT integration settings closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
