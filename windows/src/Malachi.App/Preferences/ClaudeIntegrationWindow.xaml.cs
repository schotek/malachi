// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/ClaudeIntegrationSheetController.swift
// (ClaudeIntegrationViewController: bindIfReady, setSwitch, registerChanged,
// bindClaudeDesktop, restartClaudeDesktop, showClaudeCode, claudeCodeState,
// chooseClaudeCode, claudeCodeOffer); GTK: ui/internal/window/
// claude_integration.go (newClaudeIntegration, bindMCP, bindClaudeCode,
// claudeCodeState). The settings of one Claude app on this computer
// (Preferences → Integrations → Claude Desktop or Claude Code, its row's
// Settings and its catalogue entry's Add…), one window for both by the kind
// (Integrations.LocalKind.ClaudeDesktop, ClaudeCode): moved from the AI page
// (docs/plans/ai-api.md §7.2), and one window with one switch for both apps
// before they were split.
//
// The switch shows what the bundled malachi-mcp.exe last confirmed about the
// window's app, through an McpRegistrationController for that app (status;
// install and uninstall with --client, --json and the Windows paths,
// docs/windows-port.md §10), as Integrations.RegisterSwitchFor says:
// insensitive, saying so, while the app is neither installed nor
// registered; the application's last status is
// shown at once (Adopt) and so is every newer one while no call of the
// window runs, and a failed status check is repeated after 1, 2 and 4 s. A
// missing bridge, or a status the bridge did not give, goes into the group's
// description, as in GTK (U2, U3); a failed install or uninstall flips the
// switch back with a toast. Every status goes to the application's
// AssistantController, so the menus and the pages know at once.
//
// Claude Desktop's window: a flip while Claude Desktop runs as a present
// client asks "Restart Claude Desktop?" first (the application's
// ClaudeDesktopController, macOS's offer): Restart quits it, writes the
// change and starts it again; Later writes now and leaves the change
// pending, with the row Claude Desktop under the switch and its Restart
// until Claude Desktop quits. Claude Code's window writes at once: Claude
// Code reads its file anew.
//
// Claude Code's window has the Claude Code row: the claude.exe the panel
// runs, its version and whether it is signed in (the application's locator,
// shared with the panel), Choose…, and the button of what the row offers:
// "Sign In…" while
// Claude Code says it is signed out (Claude Code's own sign-in in the
// browser; the row says "Waiting for the sign-in in your browser…" whoever
// started it) and "Get Claude Code…" while there is none.
//
// Windows (docs/windows-port.md §11.3): an owned modal window (ModalDialog)
// of the Adw.Dialog's 560×420 (Claude Desktop's 320 high: GTK's 260 and the
// row of a pending restart) with Close at the bottom; its close button,
// Escape and Ctrl+W close it; a change under way runs to its end unseen.
// Choose… offers .exe files only (the panel runs only a claude.exe).

using System;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Integrations = Malachi.Core.Model.Integrations;

namespace Malachi.App.Preferences;

/// <summary>The settings of Claude Desktop or of Claude Code on this computer.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The controller is closed with the window (Closed).")]
public sealed partial class ClaudeIntegrationWindow : Window
{
    /// <summary>claude_integration.blp content-width.</summary>
    public const int ContentWidth = 560;

    /// <summary>claude_integration.blp content-height (Claude Code's window).</summary>
    public const int ContentHeight = 420;

    /// <summary>
    /// The height of Claude Desktop's window: one group (GTK sets 260), with
    /// room for the row of a pending restart, which GTK has not.
    /// </summary>
    public const int DesktopContentHeight = 320;

    private readonly AppState state;
    private readonly Integrations.LocalKind kind;
    private readonly Integrations.ClaudeAppTexts texts;
    private readonly ILogger logger;
    private readonly ModalDialog modal;
    private readonly McpRegistrationController registration;
    private readonly SettingsChangeToken? pathToken;

    // The switch is being set from the controller, not by the user.
    private bool syncing;

    // "Restart Claude Desktop?" is up or its change runs: the switch shows
    // the user's choice until it is written, whatever the application
    // learns meanwhile.
    private bool asking;

    // Bumped by every look at Claude Code: a late answer is dropped.
    private int claudeCodeGen;

    // What the Claude Code row's second button does.
    private ErrorOffer claudeOffer;
    private bool closed;

    private ClaudeIntegrationWindow(AppState state, Integrations.LocalKind kind)
    {
        this.state = state;
        this.kind = kind;
        texts = Integrations.ClaudeAppText(kind) ?? throw new ArgumentException("not a Claude app", nameof(kind));
        logger = state.Logs.CreateLogger<ClaudeIntegrationWindow>();
        InitializeComponent();
        var title = texts.Title;
        Title = title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = title;
        Header.Loaded += (_, _) => Title = title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);

        RegisterRow.Header = texts.SwitchTitle;
        RegisterRow.Description = texts.SwitchSubtitle;
        registration = new McpRegistrationController(
            state.Paths.McpBridge, logger: state.Logs.CreateLogger<McpRegistrationController>(), client: Integrations.ClientId(kind));
        registration.EnabledChanged += (_, _) => UpdateRegisterRow();
        registration.RegisteredChanged += (_, on) =>
        {
            SetSwitch(on);
            UpdateRegisterRow();
            // The Assistant menus and the pages know at once.
            if (registration.Status is { } status)
            {
                state.Assistant.Apply(status);
            }
        };
        registration.ToastRequested += (_, text) => ToastsHost.Show(text);
        registration.DescriptionChanged += (_, text) => McpDescription.Text = text
            ?? L10n.T("Lets AI assistants read your mail and prepare drafts through the Model Context Protocol.");
        SetSwitch(registration.IsRegistered);
        // The application's last status, at once.
        if (state.Assistant.Status is { } known)
        {
            registration.Adopt(known);
        }

        state.Assistant.Changed += OnAssistantChanged;
        if (IsDesktop)
        {
            // Claude Desktop around a change (the application's controller).
            var restart = Assistant.RestartTexts();
            PendingRow.Header = Assistant.TargetName(AssistantTarget.Desktop);
            PendingRow.Description = restart.Pending;
            RestartButton.Content = restart.RestartNow;
            PendingRow.Visibility = state.ClaudeDesktop.Pending is null ? Visibility.Collapsed : Visibility.Visible;
            state.ClaudeDesktop.Changed += OnClaudeDesktopChanged;
            state.ClaudeDesktop.ToastRequested += OnClaudeDesktopToast;
            ClaudeCodeGroup.Visibility = Visibility.Collapsed;
        }
        else
        {
            PendingRow.Visibility = Visibility.Collapsed;
            ClaudeCodeRow.Header = Assistant.TargetName(AssistantTarget.Code);
            ClaudeChooseButton.Content = Assistant.PanelTexts().Choose;
            state.ClaudeCode.SigningInChanged += OnSigningInChanged;
            // Another claude.exe chosen: the row looks again.
            pathToken = state.Settings.OnChange(SettingsKey.AssistantClaudePath, () => ShowClaudeCode());
        }

        Root.Loaded += (_, _) => RegisterSwitch.Focus(FocusState.Programmatic);
        Closed += (_, _) =>
        {
            closed = true;
            registration.Close();
            pathToken?.Cancel();
            // The controllers are the application's: they go on without the window.
            state.ClaudeDesktop.Changed -= OnClaudeDesktopChanged;
            state.ClaudeDesktop.ToastRequested -= OnClaudeDesktopToast;
            state.Assistant.Changed -= OnAssistantChanged;
            // A sign-in under way goes on: the user is in the browser.
            state.ClaudeCode.SigningInChanged -= OnSigningInChanged;
            if (modal.ReturnToOwner() is { } activated)
            {
                LogReturned(logger, activated);
            }
        };
        UpdateRegisterRow();
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>
    /// Opens the window of Claude app <paramref name="kind"/>
    /// (<see cref="Integrations.LocalKind.ClaudeDesktop"/>,
    /// <see cref="Integrations.LocalKind.ClaudeCode"/>) over
    /// <paramref name="owner"/> (the main window when null or hidden, shown
    /// first), asks the bridge for its status and, for Claude Code, looks at
    /// Claude Code. Null when there is no window to open it over.
    /// </summary>
    public static ClaudeIntegrationWindow? Show(AppState state, Window? owner, Integrations.LocalKind kind)
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
        var w = new ClaudeIntegrationWindow(state, kind);
        w.modal.Present(owner, ContentWidth, w.IsDesktop ? DesktopContentHeight : ContentHeight);
        w.registration.Load();
        state.Assistant.RefreshHandlers();
        if (!w.IsDesktop)
        {
            // Claude Code may have been installed, or signed in, meanwhile.
            state.ClaudeCode.Refresh();
            w.ShowClaudeCode();
        }
        var client = Integrations.ClientId(kind);
        LogPresented(w.logger, client);
        return w;
    }

    // Claude Desktop's window (else Claude Code's).
    private bool IsDesktop => kind == Integrations.LocalKind.ClaudeDesktop;

    // The MCP switch

    private void SetSwitch(bool on)
    {
        if (asking)
        {
            return;
        }
        syncing = true;
        RegisterSwitch.IsOn = on;
        syncing = false;
    }

    // The row waits while the state is not known, a change runs or Claude
    // Desktop's restart waits, and stays insensitive, saying so, while the
    // app is neither installed nor registered (RegisterSwitchFor).
    private void UpdateRegisterRow()
    {
        var app = registration.ClientStatus;
        var sw = Integrations.RegisterSwitchFor(kind, registration.Status is not null, app?.Present == true, app?.Registered == true);
        RegisterRow.Description = sw.Subtitle;
        var busy = IsDesktop && state.ClaudeDesktop.IsBusy;
        RegisterRow.IsEnabled = registration.IsEnabled && sw.Sensitive && !busy && !asking;
        RestartButton.IsEnabled = !busy && !asking;
    }

    private async void OnRegisterToggled(object sender, RoutedEventArgs e)
    {
        if (syncing || asking)
        {
            return;
        }
        var want = RegisterSwitch.IsOn;
        asking = true;
        UpdateRegisterRow();
        try
        {
            if (IsDesktop)
            {
                await state.ClaudeDesktop.ChangeAsync(want, registration.Status, AskRestartAsync, registration.ChangeAsync);
            }
            else
            {
                await registration.ChangeAsync(want);
            }
        }
        finally
        {
            asking = false;
        }
        if (closed)
        {
            return;
        }
        SetSwitch(registration.IsRegistered);
        UpdateRegisterRow();
    }

    // "Restart Claude Desktop?": Restart Claude Desktop (the default) or Later.
    private async System.Threading.Tasks.Task<ClaudeDesktopController.Answer> AskRestartAsync()
    {
        var t = Assistant.RestartTexts();
        var restart = await state.Alerts.ConfirmAsync(this, t.Heading, t.Body, t.Restart, t.Later);
        return restart ? ClaudeDesktopController.Answer.Restart : ClaudeDesktopController.Answer.Later;
    }

    // The pending row's Restart.
    private async void OnRestartClick(object sender, RoutedEventArgs e)
    {
        if (state.ClaudeDesktop.IsBusy || asking)
        {
            return;
        }
        await state.ClaudeDesktop.RestartPendingAsync(registration.ChangeAsync);
    }

    private void OnClaudeDesktopChanged(object? sender, EventArgs e)
    {
        PendingRow.Visibility = state.ClaudeDesktop.Pending is null ? Visibility.Collapsed : Visibility.Visible;
        UpdateRegisterRow();
    }

    private void OnClaudeDesktopToast(object? sender, string text) => ToastsHost.Show(text);

    // A status the application learned elsewhere is shown by the switch
    // too; not while the question is up or a change runs.
    private void OnAssistantChanged(object? sender, EventArgs e)
    {
        if (!closed && !asking && !(IsDesktop && state.ClaudeDesktop.IsBusy) && state.Assistant.Status is { } known)
        {
            registration.Adopt(known);
        }
    }

    // Claude Code

    // The row's text: the claude.exe the panel runs, its version and whether
    // it is signed in (asked once, then kept by the locator until the window
    // opens again, the path changes or a sign-in starts or ends), or that
    // none was found; and what its second button offers.
    private async void ShowClaudeCode()
    {
        if (closed)
        {
            return;
        }
        var my = ++claudeCodeGen;
        var locator = state.ClaudeCode;
        if (locator.Locate() is not { } path)
        {
            ClaudeCodeState.Text = Assistant.Problem(AssistantTarget.App, new AssistantAvailability());
            SetClaudeOffer(ErrorOffer.Install);
            return;
        }
        if (!ClaudeCodeState.Text.StartsWith(path, StringComparison.Ordinal))
        {
            ClaudeCodeState.Text = path;
            SetClaudeOffer(ErrorOffer.None);
        }
        var version = await locator.VersionAsync();
        var signedIn = await locator.SignedInAsync();
        if (closed || my != claudeCodeGen)
        {
            return;
        }
        ClaudeCodeState.Text = ClaudeCodeStateText(path, version, signedIn, locator.SigningIn);
        SetClaudeOffer(signedIn == false ? ErrorOffer.SignIn : ErrorOffer.None);
    }

    /// <summary>
    /// "path · version · Signed in"; what is not known is left out, and
    /// while a sign-in is under way the row says that it waits for the
    /// browser instead of "Not signed in" (claude_integration.go claudeCodeState).
    /// </summary>
    internal static string ClaudeCodeStateText(string path, string? version, bool? signedIn, bool signingIn)
    {
        var t = Assistant.PanelTexts();
        var parts = new System.Collections.Generic.List<string> { path };
        if (version is { Length: > 0 })
        {
            parts.Add(version);
        }
        if (signedIn == true)
        {
            parts.Add(t.SignedIn);
        }
        else if (signingIn)
        {
            parts.Add(Assistant.SignInTexts().Waiting);
        }
        else if (signedIn == false)
        {
            parts.Add(t.NotSignedInShort);
        }
        return string.Join(" · ", parts);
    }

    private void SetClaudeOffer(ErrorOffer offer)
    {
        claudeOffer = offer;
        ClaudeOfferButton.Content = Assistants.AssistantPanel.OfferLabel(offer);
        ClaudeOfferButton.Visibility = offer == ErrorOffer.None ? Visibility.Collapsed : Visibility.Visible;
    }

    // A sign-in started or ended, here or in the panel: the row looks again.
    private void OnSigningInChanged(object? sender, EventArgs e)
    {
        if (!closed)
        {
            ShowClaudeCode();
        }
    }

    // The row's second button: Sign In… runs Claude Code's own sign-in in
    // the browser, Get Claude Code… opens the page with its installers.
    private async void OnClaudeOfferClick(object sender, RoutedEventArgs e)
    {
        switch (claudeOffer)
        {
            case ErrorOffer.Install:
                await state.OpenUrlAsync(Assistant.InstallUrl);
                break;
            case ErrorOffer.SignIn:
                var result = await state.ClaudeCode.SignInAsync();
                if (closed)
                {
                    return;
                }
                switch (result)
                {
                    case ClaudeCodeSignIn.Failed failed:
                        ToastsHost.Show(Assistant.SignInFailedText(failed.Reason));
                        break;
                    case ClaudeCodeSignIn.TimedOut:
                        ToastsHost.Show(Assistant.SignInTexts().TimedOut);
                        break;
                    default:
                        break;
                }
                break;
            default:
                break;
        }
    }

    // Choose…: the claude.exe the panel should run. The one found
    // automatically stores nothing, so choosing it goes back to looking in
    // the usual places.
    private async void OnChooseClaudeClick(object sender, RoutedEventArgs e)
    {
        var chosen = await ComposeFileDialog.OpenAsync(
            WindowPresenter.Handle(this), Assistant.TargetName(AssistantTarget.Code), multiple: false, (ClaudeFileName, "*.exe"));
        if (closed || chosen is not { Count: > 0 } || chosen[0] is not { } path || !ClaudeCodeLocator.IsExecutableFile(path))
        {
            return;
        }
        var value = string.Equals(path, state.ClaudeCode.AutomaticPath(), StringComparison.OrdinalIgnoreCase) ? "" : path;
        state.ClaudeCode.Refresh();
        if (state.Settings.AssistantClaudePath == value)
        {
            state.Assistant.RefreshHandlers();
            ShowClaudeCode();
            return;
        }
        // The change handlers look again (AssistantController follows the key).
        state.Settings.AssistantClaudePath = value;
    }

    private void OnCloseClick(object sender, RoutedEventArgs e) => Close();

    // The name of the dialog's filter: the file the panel runs (a name, not
    // translated).
    private const string ClaudeFileName = "claude.exe";

    [LoggerMessage(Level = LogLevel.Information, Message = "Claude integration settings opened for {Client}")]
    private static partial void LogPresented(ILogger logger, string client);

    [LoggerMessage(Level = LogLevel.Debug, Message = "Claude integration settings closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
