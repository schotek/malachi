// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the local integrations of macos/Sources/MalachiMail/Preferences/
// IntegrationsPaneViewController.swift (bindLocalIfReady, followApplication,
// localStates, refreshLocal, applyLocal, openLocal, removeLocal) and its
// LocalIntegrationRowView; GTK: ui/internal/window/integrations_page.go
// (bindLocalRows, localStates, applyLocalRows, localShown,
// refreshIntegrationsEmpty, localRow, removeLocal).
//
// Claude Desktop, Claude Code and ChatGPT (Codex) are state of this client,
// not accounts of the daemon (docs/plans/ai-api.md D15): each is a row under
// the accounts while it is connected (the bridge registered in that Claude
// app; ChatGPT connected) and an entry of the catalogue otherwise
// (Integrations.LocalRowFor, Offered). Claude Desktop and Claude Code were
// one integration with one switch for both apps before they were split.
// A row has the integration's icon, its name with the "This
// computer" capsule, what is known of it ("MCP bridge registered"; for
// Claude Code also where it is, its version and whether it is signed in,
// asked of Claude Code in the background; the ChatGPT account and whether
// Codex is there), Settings (its window: ClaudeIntegrationWindow of that
// app, ChatGptIntegrationWindow) and Remove with its label, after the row's
// own confirmation (LocalRow.RemoveHeading, RemoveBody): Unregister takes
// the bridge out of that Claude app only (--client), Claude Desktop's
// through the same path as its window's switch (the application's
// ClaudeDesktopController with its "Restart Claude Desktop?"), Claude
// Code's at once; Disconnect signs out of ChatGPT. The empty row of
// Connected Services shows only while neither an account nor a local
// integration has a row.

using System;
using System.Collections.Generic;
using Malachi.App.Resources;
using Malachi.Core.Assistants;
using Malachi.Core.ChatGPT;
using Malachi.Core.Controllers;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Integrations = Malachi.Core.Model.Integrations;
using SettingsCard = CommunityToolkit.WinUI.Controls.SettingsCard;

namespace Malachi.App.Preferences;

public sealed partial class IntegrationsPage
{
    // The rows of the local integrations by kind, their Remove buttons, and
    // the kinds whose Unregister or Disconnect runs.
    private readonly Dictionary<Integrations.LocalKind, (SettingsCard Row, Button Remove)> localRows = [];
    private readonly HashSet<Integrations.LocalKind> removing = [];

    // The bridge's status for the Claude rows (no client: it only asks);
    // shares every status with the application's assistant.
    private McpRegistrationController? registration;

    // The Unregister of each Claude app's row (--client of that app).
    private McpRegistrationController? desktopRegistration;
    private McpRegistrationController? codeRegistration;
    private SettingsChangeToken? codexPathToken;

    // Bumped by every look at Claude Code: a late answer is dropped.
    private int localGen;

    private void InitializeLocal()
    {
        var tx = Integrations.Texts();
        foreach (var kind in Integrations.LocalKinds)
        {
            var card = LocalRowCard(kind, LocalAutomationName(kind), tx.Settings);
            localRows[kind] = card;
            LocalRows.Children.Add(card.Row);
        }

        registration = NewRegistration(null);
        desktopRegistration = NewRegistration(McpStatus.ClientDesktop);
        codeRegistration = NewRegistration(McpStatus.ClientCode);
        state.Assistant.Changed += OnAssistantChanged;
        state.ClaudeCode.SigningInChanged += OnSigningInChanged;
        state.ChatGpt.Changed += OnChatGptChanged;
        codexPathToken = state.Settings.OnChange(SettingsKey.AssistantCodexPath, RefreshLocal);
        RefreshLocal();
    }

    // A registration of the page (for client, or only asking without one):
    // every status goes to the application's assistant, a failure toasts.
    private McpRegistrationController NewRegistration(string? client)
    {
        var r = new McpRegistrationController(state.Paths.McpBridge, logger: state.Logs.CreateLogger<McpRegistrationController>(), client: client);
        r.RegisteredChanged += (_, _) =>
        {
            if (r.Status is { } status)
            {
                state.Assistant.Apply(status);
            }
            RefreshLocal();
        };
        r.ToastRequested += (_, text) => toasts.Show(text);
        if (state.Assistant.Status is { } known)
        {
            r.Adopt(known);
        }
        return r;
    }

    private void CloseLocal()
    {
        registration?.Close();
        desktopRegistration?.Close();
        codeRegistration?.Close();
        state.Assistant.Changed -= OnAssistantChanged;
        state.ClaudeCode.SigningInChanged -= OnSigningInChanged;
        state.ChatGpt.Changed -= OnChatGptChanged;
        codexPathToken?.Cancel();
        codexPathToken = null;
    }

    // The page came up: the bridge, Claude Code and the Claude apps' handlers
    // are asked again (installed, registered or signed in meanwhile).
    private void RefreshLocalAgain()
    {
        state.ClaudeCode.Refresh();
        state.Assistant.RefreshHandlers();
        registration?.Load();
        RefreshLocal();
    }

    // A status the application learned elsewhere is this page's too.
    private void OnAssistantChanged(object? sender, EventArgs e)
    {
        if (closed)
        {
            return;
        }
        if (state.Assistant.Status is { } known)
        {
            registration?.Adopt(known);
            if (!removing.Contains(Integrations.LocalKind.ClaudeDesktop) && !state.ClaudeDesktop.IsBusy)
            {
                desktopRegistration?.Adopt(known);
            }
            if (!removing.Contains(Integrations.LocalKind.ClaudeCode))
            {
                codeRegistration?.Adopt(known);
            }
        }
        RefreshLocal();
    }

    private void OnSigningInChanged(object? sender, EventArgs e) => RefreshLocal();

    private void OnChatGptChanged()
    {
        if (!DispatcherQueue.HasThreadAccess)
        {
            DispatcherQueue.TryEnqueue(OnChatGptChanged);
            return;
        }
        RefreshLocal();
    }

    // The UI Automation name of a local integration's row and of its
    // catalogue entry: ClaudeDesktop, ClaudeCode, ChatGpt.
    internal static string LocalAutomationName(Integrations.LocalKind kind) => kind switch
    {
        Integrations.LocalKind.ClaudeDesktop => "ClaudeDesktop",
        Integrations.LocalKind.ClaudeCode => "ClaudeCode",
        Integrations.LocalKind.ChatGpt => "ChatGpt",
        _ => "Other",
    };

    // localRow: the icon, the name with its capsule, the subtitle, Settings
    // (a gear, its tooltip "Settings") and Remove with its label.
    private (SettingsCard Row, Button Remove) LocalRowCard(Integrations.LocalKind kind, string id, string settingsLabel)
    {
        var gear = new Button
        {
            Content = Icons.Create("emblem-system-symbolic"),
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        };
        ToolTipService.SetToolTip(gear, settingsLabel);
        AutomationProperties.SetName(gear, settingsLabel);
        AutomationProperties.SetAutomationId(gear, id + "IntegrationSettings");
        gear.Click += (_, _) => OpenLocal(kind);
        var remove = new Button { VerticalAlignment = VerticalAlignment.Center };
        AutomationProperties.SetAutomationId(remove, id + "IntegrationRemove");
        remove.Click += (_, _) => RemoveLocal(kind);
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        buttons.Children.Add(gear);
        buttons.Children.Add(remove);
        var row = new SettingsCard
        {
            HeaderIcon = Icons.Create(Integrations.AiIcon),
            Content = buttons,
            Visibility = Visibility.Collapsed,
        };
        AutomationProperties.SetAutomationId(row, id + "Integration");
        return (row, remove);
    }

    // localStates: what the application knows of the Claude apps and
    // ChatGPT now, without what Claude Code itself is asked for: the bridge's
    // registration in each Claude app (the application's last status, else
    // the page's).
    private (Integrations.ClaudeState Claude, Integrations.ChatGptState ChatGpt) LocalStates()
    {
        var path = state.ClaudeCode.Locate() ?? "";
        var status = state.Assistant.Status ?? registration?.Status;
        var claude = new Integrations.ClaudeState
        {
            DesktopRegistered = status?.RegisteredIn(McpStatus.ClientDesktop) ?? false,
            CodeRegistered = status?.RegisteredIn(McpStatus.ClientCode) ?? false,
            Present = path.Length > 0,
            Path = path,
        };
        var connection = state.ChatGpt.Connection;
        var chatGpt = new Integrations.ChatGptState
        {
            Connected = connection.Status == ChatGptConnectionStatus.Connected,
            Account = connection.Email ?? "",
            CodexFound = CodexExecutable.Resolve(state.Settings.AssistantCodexPath) is not null,
        };
        return (claude, chatGpt);
    }

    // refreshLocal: the rows from the states at once, then with the version
    // and the sign-in of Claude Code, which come from Claude Code itself and
    // only its row shows.
    private async void RefreshLocal()
    {
        if (closed)
        {
            return;
        }
        var gen = ++localGen;
        var (claude, chatGpt) = LocalStates();
        ApplyLocal(claude, chatGpt);
        if (!claude.Present || !claude.CodeRegistered)
        {
            return;
        }
        var locator = state.ClaudeCode;
        var version = await locator.VersionAsync();
        var signedIn = await locator.SignedInAsync();
        if (closed || gen != localGen)
        {
            return;
        }
        ApplyLocal(claude with { Version = version ?? "", SignInKnown = signedIn is not null, SignedIn = signedIn == true }, chatGpt);
    }

    // applyLocal: a connected integration's row, its catalogue entry
    // otherwise; the rows' group only with a row in it, and the empty row of
    // Connected Services only without any row.
    private void ApplyLocal(Integrations.ClaudeState claude, Integrations.ChatGptState chatGpt)
    {
        var any = false;
        foreach (var kind in Integrations.LocalKinds)
        {
            if (Integrations.LocalRowFor(kind, claude, chatGpt) is not { } found || !localRows.TryGetValue(kind, out var card))
            {
                continue;
            }
            var (spec, shown) = found;
            var (row, remove) = card;
            row.Header = TitleWithBadge(spec.Title, spec.Badge);
            AutomationProperties.SetName(row, spec.Title);
            if (spec.Subtitle.Length == 0)
            {
                row.ClearValue(SettingsCard.DescriptionProperty);
            }
            else
            {
                row.Description = spec.Subtitle;
            }
            remove.Content = spec.RemoveLabel;
            remove.Visibility = spec.CanRemove ? Visibility.Visible : Visibility.Collapsed;
            remove.IsEnabled = !removing.Contains(kind);
            row.Visibility = shown ? Visibility.Visible : Visibility.Collapsed;
            foreach (var service in Integrations.Catalog())
            {
                if (service.Local == kind && cardsByKey.TryGetValue(service.Key, out var entry))
                {
                    entry.Visibility = Integrations.Offered(service, claude, chatGpt) ? Visibility.Visible : Visibility.Collapsed;
                }
            }
            any |= shown;
        }
        LocalRows.Visibility = any ? Visibility.Visible : Visibility.Collapsed;
        list.OtherRowsShown = any;
    }

    // openLocal: a local row's Settings and the catalogue's Add… of a local
    // integration open its window over the Preferences; the rows look again
    // once it closed.
    private void OpenLocal(Integrations.LocalKind kind)
    {
        Window? opened = kind switch
        {
            Integrations.LocalKind.ClaudeDesktop or Integrations.LocalKind.ClaudeCode => ClaudeIntegrationWindow.Show(state, window, kind),
            Integrations.LocalKind.ChatGpt => ChatGptIntegrationWindow.Show(state, window),
            _ => null,
        };
        if (opened is not null)
        {
            opened.Closed += (_, _) => RefreshLocal();
        }
    }

    // removeLocal: after the row's confirmation, the bridge unregistered
    // from that Claude app only (Claude Desktop's with the restart
    // question), or ChatGPT disconnected.
    private async void RemoveLocal(Integrations.LocalKind kind)
    {
        if (removing.Contains(kind) || !localRows.TryGetValue(kind, out var card))
        {
            return;
        }
        var (claude, chatGpt) = LocalStates();
        if (Integrations.LocalRowFor(kind, claude, chatGpt) is not { } found)
        {
            return;
        }
        var spec = found.Row;
        var button = card.Remove;
        var r = kind switch
        {
            Integrations.LocalKind.ClaudeDesktop => desktopRegistration,
            Integrations.LocalKind.ClaudeCode => codeRegistration,
            _ => null,
        };
        if (kind != Integrations.LocalKind.ChatGpt && r is null)
        {
            return;
        }
        if (!await state.Alerts.ConfirmDestructiveAsync(window, spec.RemoveHeading, spec.RemoveBody, spec.RemoveLabel) || closed)
        {
            return;
        }
        removing.Add(kind);
        button.IsEnabled = false;
        switch (kind)
        {
            case Integrations.LocalKind.ClaudeDesktop:
                try
                {
                    await state.ClaudeDesktop.ChangeAsync(false, state.Assistant.Status ?? r!.Status, AskRestartAsync, r!.ChangeAsync);
                }
                finally
                {
                    removing.Remove(kind);
                }
                RefreshLocal();
                break;
            case Integrations.LocalKind.ClaudeCode:
                try
                {
                    await r!.ChangeAsync(false);
                }
                finally
                {
                    removing.Remove(kind);
                }
                RefreshLocal();
                break;
            case Integrations.LocalKind.ChatGpt:
                {
                    var confirmed = false;
                    try
                    {
                        confirmed = await state.ChatGpt.DisconnectAsync();
                    }
                    catch (OperationCanceledException)
                    {
                    }
                    catch (ChatGptAuthException)
                    {
                    }
                    finally
                    {
                        removing.Remove(kind);
                    }
                    if (closed)
                    {
                        return;
                    }
                    if (!confirmed)
                    {
                        toasts.Show(Assistants.ChatGptText.RevocationUnconfirmed);
                    }
                    state.UpdateChatGptAvailability();
                    RefreshLocal();
                    break;
                }
            case Integrations.LocalKind.None:
            default:
                removing.Remove(kind);
                break;
        }
    }

    // "Restart Claude Desktop?": Restart Claude Desktop (the default) or Later.
    private async System.Threading.Tasks.Task<ClaudeDesktopController.Answer> AskRestartAsync()
    {
        var t = Assistant.RestartTexts();
        var restart = await state.Alerts.ConfirmAsync(window, t.Heading, t.Body, t.Restart, t.Later);
        return restart ? ClaudeDesktopController.Answer.Restart : ClaudeDesktopController.Answer.Later;
    }
}
