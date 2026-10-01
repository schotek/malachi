// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantController.swift
// (AssistantController: targets, status, handlers, claudeFound, closed,
// close, refresh, refreshHandlers, apply, registered, shown, availability,
// pick, panelShown, canRunInApp, problem, onChange) and of its extension
// canAsk(about:) in macos/Sources/MalachiMail/Assistant/AssistantActions.swift;
// GTK: ui/internal/window/assistant.go (the Assistant type: NewAssistant,
// Refresh, RefreshHandlers, Apply, registered, shown, availability, pick,
// panelShown, CanRunInApp, canAskFile, problem, OnChange, notify).
//
// Windows differences: the handler lookup is a Func<string, bool> the app
// answers with the default ProgID of the URL scheme, where macOS asks
// LaunchServices and GTK GIO. Swift's onChange with its tokens is the event
// Changed, raised as Swift notifies its observers (in order, each handler
// guarded, docs/windows-port.md §7.5); it also follows assistant-target, as
// GTK's NewAssistant does, and assistant-claude-path looks the handlers up
// again (GTK: whether the panel can run changes with the claude chosen).
// The bridge is run by an McpRegistrationController of its own, without
// its toasts, given the Microsoft Store's Claude Desktop as the AI page's
// is (--claude-desktop-config).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Daemon;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the Assistant menu knows about the two Claude apps, once for the
/// whole application: whether an app handles each target's links
/// (<see cref="Assistant.Scheme"/>, looked up through the injected handler
/// lookup) and whether the <c>malachi-mcp</c> bridge is registered in each
/// client (the last <c>malachi-mcp status --json</c>, <see cref="McpStatus"/>).
/// Out of the two comes the <see cref="AssistantAvailability"/> of each
/// target, and <see cref="Pick"/> says whether the target of the
/// <c>assistant-target</c> preference can be used (never the other one
/// instead).
/// </summary>
/// <remarks>
/// <para>
/// The menus never wait for it: they use the last known state.
/// <see cref="Refresh"/> looks the handlers up at once and asks the bridge
/// for its status in the background; the application calls it at launch and
/// every time an Assistant menu opens, and the AI page of the preferences
/// hands over the status its own switch got (<see cref="Apply"/>), so a
/// change of "Register with Claude" counts at once. A status that is not
/// known (not asked yet, no bridge beside the application, the bridge
/// failed) counts as not registered; a failed status keeps the last known
/// one. The bridge is run by an <see cref="McpRegistrationController"/> of
/// its own, without its toasts: a status asked while one runs is skipped, a
/// stale reply dropped.
/// </para>
/// <para>
/// Whether the Assistant appears at all is <see cref="Shown"/>
/// (<see cref="Assistant.Shown"/>): the <c>assistant-menu</c> preference and
/// the bridge registered in at least one client, so it is off while
/// "Register with Claude" is. <see cref="Changed"/> reports a change of the
/// handlers, of the status and of the <c>assistant-menu</c> and
/// <c>assistant-target</c> preferences, so the menus, the command bars, the
/// attachment chips and the preferences follow one source.
/// </para>
/// <para>
/// The third target, In App (the assistant panel), handles no link: its
/// "handler" is the user's Claude Code found by the
/// <see cref="ClaudeCodeLocator"/> with the bridge beside the application
/// (the panel hands the bridge to Claude Code itself), and it counts as
/// registered while the bridge is registered in any client, which is what
/// <see cref="Shown"/> asks anyway. The panel exists while
/// <see cref="PanelShown"/>: the Assistant is shown and In App chosen.
/// </para>
/// <para>
/// No texts here but <see cref="Problem"/>'s: the menus and the preferences
/// read <see cref="Shown"/>, <see cref="Availability"/>, <see cref="Pick"/>
/// and <see cref="Problem"/>. Create it, and call it, on the UI thread.
/// </para>
/// </remarks>
public sealed class AssistantController : IDisposable
{
    private readonly McpRegistrationController registration;
    private readonly Func<string, bool> lookup;

    // The bridge beside the application, which the panel passes to Claude
    // Code; null without one.
    private readonly string? bridge;
    private readonly ControllerScope scope;
    private readonly List<SettingsChangeToken> tokens = [];

    private Dictionary<AssistantTarget, bool> handlers = [];

    /// <summary>An Assistant state on the calling (UI) thread.</summary>
    /// <param name="bridge"><c>malachi-mcp.exe</c> beside the application (<see cref="Paths.McpBridge"/>), or null when there is none.</param>
    /// <param name="settings">Where the <c>assistant-*</c> preferences are read.</param>
    /// <param name="handler">Whether an application handles links of a URL scheme (the default ProgID of the scheme).</param>
    /// <param name="runner">How the bridge is run (tests run a stand-in).</param>
    /// <param name="timeout">How long one bridge call may take (<see cref="McpRegistrationController.DefaultTimeout"/>).</param>
    /// <param name="locator">Finds Claude Code for the In App target; null leaves it unavailable.</param>
    /// <param name="claudeDesktop">The Microsoft Store's Claude Desktop (<see cref="ClaudeDesktopPackage.ForCurrentUser"/>).</param>
    /// <param name="registrationLogger">The logger of the bridge's calls.</param>
    /// <param name="pending">Counts the background work (the bridge's status); one of its own when null.</param>
    public AssistantController(
        string? bridge,
        SettingsStore settings,
        Func<string, bool> handler,
        BridgeRunner? runner = null,
        TimeSpan? timeout = null,
        ClaudeCodeLocator? locator = null,
        ClaudeDesktopPackage? claudeDesktop = null,
        ILogger<McpRegistrationController>? registrationLogger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(handler);
        Settings = settings;
        this.bridge = bridge;
        Locator = locator;
        lookup = handler;
        scope = new ControllerScope(pending);
        registration = new McpRegistrationController(bridge, runner, timeout, claudeDesktop: claudeDesktop, logger: registrationLogger, pending: scope.Pending);
        registration.RegisteredChanged += (_, _) =>
        {
            if (registration.Status is { } s)
            {
                Apply(s);
            }
        };
        tokens.Add(settings.OnChange(SettingsKey.AssistantMenu, Notify));
        tokens.Add(settings.OnChange(SettingsKey.AssistantTarget, Notify));
        // Another claude chosen: whether the panel can run changes.
        tokens.Add(settings.OnChange(SettingsKey.AssistantClaudePath, RefreshHandlers));
    }

    /// <summary>
    /// Called after the handlers, the status, or the <c>assistant-menu</c> or
    /// <c>assistant-target</c> preference changed (Swift <c>onChange</c>).
    /// </summary>
    public event EventHandler? Changed;

    /// <summary>The targets, in the order of the menu and the preferences.</summary>
    public static IReadOnlyList<AssistantTarget> Targets => Assistant.Targets;

    /// <summary>Where the <c>assistant-*</c> preferences are read.</summary>
    public SettingsStore Settings { get; }

    /// <summary>Finds Claude Code for the panel; null: the panel is never available.</summary>
    public ClaudeCodeLocator? Locator { get; }

    /// <summary>The last status the bridge reported; null until one answered.</summary>
    public McpStatus? Status { get; private set; }

    /// <summary>
    /// Whether an app handles each target's links, as last looked up; a
    /// target not looked up yet has none.
    /// </summary>
    public IReadOnlyDictionary<AssistantTarget, bool> Handlers => handlers;

    /// <summary>
    /// Whether the <see cref="ClaudeCodeLocator"/> found Claude Code when the
    /// handlers were last looked up (<see cref="CanRunInApp"/>).
    /// </summary>
    public bool ClaudeFound { get; private set; }

    /// <summary>Nothing is emitted afterwards and replies are dropped.</summary>
    public bool Closed { get; private set; }

    /// <summary>
    /// Whether the bridge is registered in at least one client, as last
    /// reported; false while no status is known.
    /// </summary>
    public bool Registered => Status?.IsRegistered ?? false;

    /// <summary>
    /// Whether the Assistant appears at all (<see cref="Assistant.Shown"/>):
    /// the <c>assistant-menu</c> preference while the bridge is registered.
    /// </summary>
    public bool Shown => Assistant.Shown(Settings.AssistantMenu, Registered);

    /// <summary>
    /// Whether the assistant panel exists: the Assistant is shown and the In
    /// App target chosen. Whether it can run is <see cref="Pick"/>'s.
    /// </summary>
    public bool PanelShown => Shown && Settings.AssistantTarget == AssistantTarget.App;

    /// <summary>
    /// Whether the In App target's one-shot requests exist: the compose
    /// window's rewrite and the search in the user's own words, which read no
    /// mail and need no bridge (<see cref="AssistantRequest"/>). The same
    /// condition as the panel's (<see cref="PanelShown"/>), and Claude Code
    /// found (as last looked up); whether it is signed in is asked when a
    /// request runs. Its changes come through <see cref="Changed"/>.
    /// </summary>
    public bool CanRunInApp => PanelShown && ClaudeFound;

    /// <summary>Stops listening: late replies are dropped, nothing is emitted.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        Closed = true;
        registration.Close();
        foreach (var token in tokens)
        {
            token.Cancel();
        }
        tokens.Clear();
        Changed = null;
        scope.Close();
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    /// <summary>
    /// Looks the handlers up now and asks the bridge for its status in the
    /// background (skipped while a status call runs). The caller goes on
    /// with the last known state; <see cref="Changed"/> reports what changed.
    /// </summary>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (Closed)
        {
            return;
        }
        RefreshHandlers();
        registration.Load();
    }

    /// <summary>Looks up whether an app handles each target's links, and whether the panel finds Claude Code and the bridge.</summary>
    public void RefreshHandlers()
    {
        scope.VerifyAccess();
        if (Closed)
        {
            return;
        }
        var claude = Locator?.Locate() is not null;
        var found = new Dictionary<AssistantTarget, bool>();
        foreach (var t in Targets)
        {
            found[t] = t == AssistantTarget.App ? bridge is not null && claude : lookup(t.Scheme());
        }
        if (SameHandlers(found, handlers) && claude == ClaudeFound)
        {
            return;
        }
        handlers = found;
        ClaudeFound = claude;
        Notify();
    }

    /// <summary>
    /// Takes a status the bridge reported elsewhere (the AI page's
    /// "Register with Claude" after <c>status</c>, <c>install</c> or
    /// <c>uninstall</c>).
    /// </summary>
    public void Apply(McpStatus s)
    {
        ArgumentNullException.ThrowIfNull(s);
        scope.VerifyAccess();
        if (Closed || s == Status)
        {
            return;
        }
        Status = s;
        Notify();
    }

    /// <summary>
    /// What is known about target <paramref name="t"/>: an app handles its
    /// links, the bridge is registered in its client
    /// (<see cref="Assistant.ClientId"/>). For the panel: Claude Code and the
    /// bridge were found, the bridge is registered in any client. A target
    /// outside the enum is Claude Desktop.
    /// </summary>
    public AssistantAvailability Availability(AssistantTarget t)
    {
        t = Enum.IsDefined(t) ? t : AssistantTarget.Desktop;
        var handler = handlers.TryGetValue(t, out var h) && h;
        if (t == AssistantTarget.App)
        {
            return new AssistantAvailability(handler, Registered);
        }
        var client = Status?.Clients.FirstOrDefault(c => c.Id == t.ClientId());
        return new AssistantAvailability(handler, client?.Registered ?? false);
    }

    /// <summary>
    /// The target of the <c>assistant-target</c> preference and whether it
    /// can run the action (<see cref="Assistant.Pick(AssistantTarget, AssistantAvailability, AssistantAvailability, AssistantAvailability, bool)"/>,
    /// no fallback to another target): the message actions and Summarize
    /// Unread need the bridge, the file hand-off does not (the panel's reads
    /// the attachment through it, so it asks with
    /// <paramref name="needsBridge"/>).
    /// </summary>
    public (AssistantTarget Target, bool Ok) Pick(bool needsBridge) =>
        Assistant.Pick(
            Settings.AssistantTarget,
            Availability(AssistantTarget.Desktop),
            Availability(AssistantTarget.Code),
            Availability(AssistantTarget.App),
            needsBridge);

    /// <summary>Why target <paramref name="t"/> cannot run the message actions; "" when it can (<see cref="Assistant.Problem"/>).</summary>
    public string Problem(AssistantTarget t) => Assistant.Problem(t, Availability(t));

    /// <summary>
    /// Whether an attachment's "Ask the Assistant…" can run for a part of
    /// <paramref name="contentType"/> (Swift <c>canAsk(about:)</c>, GTK
    /// <c>canAskFile</c>): the chosen Claude app is installed (the file goes
    /// without the bridge); for In App, the panel can run (it reads the file
    /// through the bridge's <c>get_attachment</c>) and the panel offers the
    /// type (<see cref="Assistant.AttachmentReadable"/>: text and images,
    /// never documents).
    /// </summary>
    public bool CanAskFile(string contentType)
    {
        ArgumentNullException.ThrowIfNull(contentType);
        if (Settings.AssistantTarget == AssistantTarget.App)
        {
            return Pick(needsBridge: true).Ok && Assistant.AttachmentReadable(contentType);
        }
        return Pick(needsBridge: false).Ok;
    }

    private static bool SameHandlers(Dictionary<AssistantTarget, bool> a, Dictionary<AssistantTarget, bool> b) =>
        a.Count == b.Count && a.All(kv => b.TryGetValue(kv.Key, out var v) && v == kv.Value);

    // Calls the handlers of Changed in the order they came; one that throws
    // is reported and the next is called all the same.
    private void Notify()
    {
        if (Closed || Changed is not { } changed)
        {
            return;
        }
        foreach (var handler in changed.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }
}
