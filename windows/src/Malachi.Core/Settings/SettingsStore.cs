// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings); GTK:
// ui/internal/settings/store.go (Store). The class is not called Settings,
// as in Swift, because code in the Malachi.Core namespaces would find the
// namespace Malachi.Core.Settings under that name before the type; GTK calls
// it settings.Store.
//
// UI-only preferences with the keys, defaults and ranges of
// data/io.github.schotek.Malachi.gschema.xml, plus the Windows-only ctrl-r.
// Only presentation belongs here; anything that affects mail handling is the
// daemon's and goes through config.get/config.set (CLAUDE.md rule 1).
//
// As GSettings: numeric keys are clamped to their range when read and
// written, an enum value outside its nicks reads as the default, a write
// equal to the stored value (or to the default of an unset key) changes
// nothing and fires nothing, and lists are handed out as copies. Handlers
// fire for every change of their key, in registration order: a change made
// through this class before the setter returns, on the thread that made it;
// a change from outside (another process, reg add) on the
// SynchronizationContext captured when the store was made (the UI thread),
// or on the backend's thread when there was none.

using System;
using System.Collections.Frozen;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
using System.Threading;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;

namespace Malachi.Core.Settings;

/// <summary>The typed settings facade.</summary>
public sealed class SettingsStore : IDisposable
{
    /// <summary>The largest mark-read delay in seconds; the gschema's range.</summary>
    public const int MarkReadDelayMax = 60;

    /// <summary>The smallest text zoom in percent; the gschema's range.</summary>
    public const int TextZoomMin = 50;

    /// <summary>The largest text zoom in percent; the gschema's range.</summary>
    public const int TextZoomMax = 200;

    /// <summary>The step of the text zoom control.</summary>
    public const int TextZoomStep = 10;

    // In SettingsKey order.
    private static readonly SettingsKeyInfo[] Keys =
    [
        new(SettingsKey.WindowWidth, "window-width", "i", 1200),
        new(SettingsKey.WindowHeight, "window-height", "i", 760),
        new(SettingsKey.WindowMaximized, "window-maximized", "b", false),
        new(SettingsKey.FolderPaneWidth, "folder-pane-width", "i", 240),
        new(SettingsKey.MessageListWidth, "message-list-width", "i", 380),
        new(SettingsKey.CollapsedFolders, "collapsed-folders", "as", Array.Empty<string>()),
        new(SettingsKey.CollapsedAccounts, "collapsed-accounts", "as", Array.Empty<string>()),
        new(SettingsKey.FavouriteFolders, "favourite-folders", "as", Array.Empty<string>()),
        new(SettingsKey.LaunchAtLogin, "launch-at-login", "b", false),
        new(SettingsKey.RunInBackground, "run-in-background", "b", false),
        new(SettingsKey.MarkReadDelay, "mark-read-delay", "i", 2, minimum: 0, maximum: MarkReadDelayMax),
        new(SettingsKey.ConfirmDelete, "confirm-delete", "b", true),
        new(SettingsKey.DesktopNotifications, "desktop-notifications", "b", true),
        new(SettingsKey.NotificationSound, "notification-sound", "b", false),
        new(SettingsKey.ColorScheme, "color-scheme", "s", "system", choices: Nicks<ColorScheme>.All),
        new(SettingsKey.Density, "message-list-density", "s", "comfortable", choices: Nicks<Density>.All),
        new(SettingsKey.ShowPreviewLine, "show-preview-line", "b", true),
        new(SettingsKey.GroupByConversation, "group-by-conversation", "b", false),
        new(SettingsKey.SearchScope, "search-scope", "s", "folder", choices: Nicks<SearchScope>.All),
        new(SettingsKey.ShowAvatars, "show-avatars", "b", true),
        new(SettingsKey.MonochromeAvatars, "monochrome-avatars", "b", false),
        new(SettingsKey.MonospacePlainText, "monospace-plain-text", "b", false),
        new(SettingsKey.TextZoom, "text-zoom", "i", 100, minimum: TextZoomMin, maximum: TextZoomMax),
        new(SettingsKey.AssistantMenu, "assistant-menu", "b", true),
        new(SettingsKey.AssistantTarget, "assistant-target", "s", "desktop", choices: Nicks<AssistantTarget>.All),
        new(SettingsKey.AssistantModel, "assistant-model", "s", "sonnet", choices: Nicks<AssistantModel>.All),
        new(SettingsKey.AssistantClaudePath, "assistant-claude-path", "s", ""),
        new(SettingsKey.AssistantConsent, "assistant-consent", "b", false),
        new(SettingsKey.AssistantProvider, "assistant-provider", "s", "claude", choices: Nicks<AssistantProviderID>.All),
        new(SettingsKey.AssistantCodexPath, "assistant-codex-path", "s", ""),
        new(SettingsKey.AssistantChatGptModel, "assistant-chatgpt-model", "s", ""),
        new(SettingsKey.AssistantChatGptConsentVersion, "assistant-chatgpt-consent-version", "i", 0, minimum: 0, maximum: int.MaxValue),
        // Schema metadata already present in GTK/macOS; the Windows Board
        // widgets remain separate tracked work (docs/chatgpt-integration.md).
        new(SettingsKey.BoardDefaultStyle, "board-default-style", "s", "list", choices: Nicks<BoardStyle>.All),
        new(SettingsKey.BoardTriageConsent, "board-triage-consent", "b", false),
        new(SettingsKey.BoardTriageModel, "board-triage-model", "s", "sonnet", choices: Nicks<AssistantModel>.All),
        new(SettingsKey.BoardChatGptModel, "board-triage-chatgpt-model", "s", ""),
        new(SettingsKey.BoardChatGptConsentVersion, "board-triage-chatgpt-consent-version", "i", 0, minimum: 0, maximum: int.MaxValue),
        new(SettingsKey.CtrlR, "ctrl-r", "s", "reply", choices: Nicks<CtrlR>.All, windowsOnly: true),
    ];

    // Registry value names are case-insensitive, so a hand edit may come
    // back as "Text-Zoom".
    private static readonly FrozenDictionary<string, SettingsKey> ByName =
        Keys.ToFrozenDictionary(k => k.Name, k => k.Key, StringComparer.OrdinalIgnoreCase);

    private readonly ISettingsBackend backend;
    private readonly SynchronizationContext? context;
    private readonly ChangeHub hub = new();
    private int disposed;

    /// <summary>
    /// A store over <paramref name="backend"/> that raises external changes
    /// on the current <see cref="SynchronizationContext"/>.
    /// </summary>
    public SettingsStore(ISettingsBackend backend)
        : this(backend, SynchronizationContext.Current)
    {
    }

    /// <summary>
    /// A store over <paramref name="backend"/> that raises external changes
    /// on <paramref name="context"/>, or on the backend's thread when it is
    /// null.
    /// </summary>
    public SettingsStore(ISettingsBackend backend, SynchronizationContext? context)
    {
        ArgumentNullException.ThrowIfNull(backend);
        this.backend = backend;
        this.context = context;
        backend.Changed += OnBackendChanged;
    }

    /// <summary>Every key with its type, default and range, in <see cref="SettingsKey"/> order.</summary>
    public static IReadOnlyList<SettingsKeyInfo> Schema { get; } = new ReadOnlyCollection<SettingsKeyInfo>(Keys);

    /// <summary>The backend the values are stored in.</summary>
    public ISettingsBackend Backend => backend;

    /// <summary>Whether values survive a restart.</summary>
    public bool Persistent => backend.IsPersistent;

    // Window geometry: the gschema declares no range; the window clamps.

    /// <summary>The main window's width in effective pixels.</summary>
    public int WindowWidth
    {
        get => GetInt32(SettingsKey.WindowWidth);
        set => SetInt32(SettingsKey.WindowWidth, value);
    }

    /// <summary>The main window's height in effective pixels.</summary>
    public int WindowHeight
    {
        get => GetInt32(SettingsKey.WindowHeight);
        set => SetInt32(SettingsKey.WindowHeight, value);
    }

    /// <summary>Whether the main window is maximized.</summary>
    public bool WindowMaximized
    {
        get => GetBoolean(SettingsKey.WindowMaximized);
        set => SetBoolean(SettingsKey.WindowMaximized, value);
    }

    /// <summary>The width of the folder sidebar.</summary>
    public int FolderPaneWidth
    {
        get => GetInt32(SettingsKey.FolderPaneWidth);
        set => SetInt32(SettingsKey.FolderPaneWidth, value);
    }

    /// <summary>The width of the message list.</summary>
    public int MessageListWidth
    {
        get => GetInt32(SettingsKey.MessageListWidth);
        set => SetInt32(SettingsKey.MessageListWidth, value);
    }

    // General

    /// <summary>
    /// Mirrors the Run value and StartupApproved, which, not this key, are
    /// authoritative.
    /// </summary>
    public bool LaunchAtLogin
    {
        get => GetBoolean(SettingsKey.LaunchAtLogin);
        set => SetBoolean(SettingsKey.LaunchAtLogin, value);
    }

    /// <summary>Whether closing the main window keeps the app running.</summary>
    public bool RunInBackground
    {
        get => GetBoolean(SettingsKey.RunInBackground);
        set => SetBoolean(SettingsKey.RunInBackground, value);
    }

    /// <summary>Seconds a message must be shown before it is marked read; 0 = at once.</summary>
    public int MarkReadDelay
    {
        get => GetInt32(SettingsKey.MarkReadDelay);
        set => SetInt32(SettingsKey.MarkReadDelay, value);
    }

    /// <summary>Whether moving to Trash asks first.</summary>
    public bool ConfirmDelete
    {
        get => GetBoolean(SettingsKey.ConfirmDelete);
        set => SetBoolean(SettingsKey.ConfirmDelete, value);
    }

    /// <summary>Whether new mail shows a notification.</summary>
    public bool DesktopNotifications
    {
        get => GetBoolean(SettingsKey.DesktopNotifications);
        set => SetBoolean(SettingsKey.DesktopNotifications, value);
    }

    /// <summary>Whether new mail plays the system sound.</summary>
    public bool NotificationSound
    {
        get => GetBoolean(SettingsKey.NotificationSound);
        set => SetBoolean(SettingsKey.NotificationSound, value);
    }

    /// <summary>What Ctrl+R does (Windows only; macOS's command-r).</summary>
    public CtrlR CtrlR
    {
        get => GetEnum<CtrlR>(SettingsKey.CtrlR);
        set => SetEnum(SettingsKey.CtrlR, value);
    }

    // Appearance

    /// <summary>The colour scheme.</summary>
    public ColorScheme ColorScheme
    {
        get => GetEnum<ColorScheme>(SettingsKey.ColorScheme);
        set => SetEnum(SettingsKey.ColorScheme, value);
    }

    /// <summary>The message list's density.</summary>
    public Density Density
    {
        get => GetEnum<Density>(SettingsKey.Density);
        set => SetEnum(SettingsKey.Density, value);
    }

    /// <summary>Whether list rows show a preview line.</summary>
    public bool ShowPreviewLine
    {
        get => GetBoolean(SettingsKey.ShowPreviewLine);
        set => SetBoolean(SettingsKey.ShowPreviewLine, value);
    }

    /// <summary>Whether the list shows one row per conversation (thread.list).</summary>
    public bool GroupByConversation
    {
        get => GetBoolean(SettingsKey.GroupByConversation);
        set => SetBoolean(SettingsKey.GroupByConversation, value);
    }

    /// <summary>The scope last chosen for a search.</summary>
    public SearchScope SearchScope
    {
        get => GetEnum<SearchScope>(SettingsKey.SearchScope);
        set => SetEnum(SettingsKey.SearchScope, value);
    }

    /// <summary>Whether list rows show sender avatars.</summary>
    public bool ShowAvatars
    {
        get => GetBoolean(SettingsKey.ShowAvatars);
        set => SetBoolean(SettingsKey.ShowAvatars, value);
    }

    /// <summary>Whether avatars are grey instead of per-sender colours.</summary>
    public bool MonochromeAvatars
    {
        get => GetBoolean(SettingsKey.MonochromeAvatars);
        set => SetBoolean(SettingsKey.MonochromeAvatars, value);
    }

    /// <summary>Whether plain-text bodies use a monospace font.</summary>
    public bool MonospacePlainText
    {
        get => GetBoolean(SettingsKey.MonospacePlainText);
        set => SetBoolean(SettingsKey.MonospacePlainText, value);
    }

    /// <summary>Message body zoom in percent, clamped to <see cref="TextZoomMin"/>…<see cref="TextZoomMax"/>.</summary>
    public int TextZoom
    {
        get => GetInt32(SettingsKey.TextZoom);
        set => SetInt32(SettingsKey.TextZoom, value);
    }

    // Assistant

    /// <summary>Whether the Assistant menu that hands mail to Claude is shown.</summary>
    public bool AssistantMenu
    {
        get => GetBoolean(SettingsKey.AssistantMenu);
        set => SetBoolean(SettingsKey.AssistantMenu, value);
    }

    /// <summary>Where the Assistant opens Claude, the last choice made in the menu or in the settings.</summary>
    public AssistantTarget AssistantTarget
    {
        get => GetEnum<AssistantTarget>(SettingsKey.AssistantTarget);
        set => SetEnum(SettingsKey.AssistantTarget, value);
    }

    /// <summary>The model the assistant panel passes to Claude Code.</summary>
    public AssistantModel AssistantModel
    {
        get => GetEnum<AssistantModel>(SettingsKey.AssistantModel);
        set => SetEnum(SettingsKey.AssistantModel, value);
    }

    /// <summary>
    /// The claude executable the assistant panel runs; empty looks in the
    /// usual places. A null write stores the empty string.
    /// </summary>
    public string AssistantClaudePath
    {
        get => GetString(SettingsKey.AssistantClaudePath);
        set => SetString(SettingsKey.AssistantClaudePath, value);
    }

    /// <summary>Whether the user allowed the assistant panel to send mail to Claude (asked before the first question).</summary>
    public bool AssistantConsent
    {
        get => GetBoolean(SettingsKey.AssistantConsent);
        set => SetBoolean(SettingsKey.AssistantConsent, value);
    }

    /// <summary>The in-app provider; external Claude hand-offs keep their own target.</summary>
    public AssistantProviderID AssistantProvider
    {
        get => GetEnum<AssistantProviderID>(SettingsKey.AssistantProvider);
        set => SetEnum(SettingsKey.AssistantProvider, value);
    }

    /// <summary>The native Codex executable; empty uses discovery.</summary>
    public string AssistantCodexPath
    {
        get => GetString(SettingsKey.AssistantCodexPath);
        set => SetString(SettingsKey.AssistantCodexPath, value);
    }

    /// <summary>ChatGPT's model ID; empty lets the verified provider choose its default.</summary>
    public string AssistantChatGptModel
    {
        get => GetString(SettingsKey.AssistantChatGptModel);
        set => SetString(SettingsKey.AssistantChatGptModel, value);
    }

    /// <summary>The accepted OpenAI disclosure version, separately from Anthropic consent.</summary>
    public int AssistantChatGptConsentVersion
    {
        get => GetInt32(SettingsKey.AssistantChatGptConsentVersion);
        set => SetInt32(SettingsKey.AssistantChatGptConsentVersion, value);
    }

    // Board

    /// <summary>Whether the user allowed the assistant to triage the board with Claude Code (asked once, with the panel's consent).</summary>
    public bool BoardTriageConsent
    {
        get => GetBoolean(SettingsKey.BoardTriageConsent);
        set => SetBoolean(SettingsKey.BoardTriageConsent, value);
    }

    /// <summary>The model the board's triage passes to Claude Code, independent of the panel's.</summary>
    public AssistantModel BoardTriageModel
    {
        get => GetEnum<AssistantModel>(SettingsKey.BoardTriageModel);
        set => SetEnum(SettingsKey.BoardTriageModel, value);
    }

    /// <summary>ChatGPT's model ID of the board's triage; empty lets the provider choose its default.</summary>
    public string BoardChatGptModel
    {
        get => GetString(SettingsKey.BoardChatGptModel);
        set => SetString(SettingsKey.BoardChatGptModel, value);
    }

    /// <summary>The accepted version of the board's OpenAI disclosure, separately from the panel's.</summary>
    public int BoardChatGptConsentVersion
    {
        get => GetInt32(SettingsKey.BoardChatGptConsentVersion);
        set => SetInt32(SettingsKey.BoardChatGptConsentVersion, value);
    }

    /// <summary>
    /// The style the board opens in the first time it shows after launch
    /// (<see cref="Board.StyleOnShow"/>); an unknown nick reads as the List.
    /// </summary>
    public BoardStyle BoardDefaultStyle
    {
        get => GetEnum<BoardStyle>(SettingsKey.BoardDefaultStyle);
        set => SetEnum(SettingsKey.BoardDefaultStyle, value);
    }

    // Sidebar state

    /// <summary>
    /// Folded-away nodes of the folder sidebar, one entry per node; the
    /// model owns the encoding and tolerates entries it cannot parse.
    /// </summary>
    public IReadOnlyList<string> CollapsedFolders
    {
        get => GetStringList(SettingsKey.CollapsedFolders);
        set => SetStringList(SettingsKey.CollapsedFolders, value);
    }

    /// <summary>Folded-away accounts of the folder sidebar.</summary>
    public IReadOnlyList<string> CollapsedAccounts
    {
        get => GetStringList(SettingsKey.CollapsedAccounts);
        set => SetStringList(SettingsKey.CollapsedAccounts, value);
    }

    /// <summary>Folders pinned to the Favourites section, encoded like <see cref="CollapsedFolders"/>.</summary>
    public IReadOnlyList<string> FavouriteFolders
    {
        get => GetStringList(SettingsKey.FavouriteFolders);
        set => SetStringList(SettingsKey.FavouriteFolders, value);
    }

    /// <summary>What the schema says about <paramref name="key"/>.</summary>
    public static SettingsKeyInfo Info(SettingsKey key) => Keys[(int)key];

    /// <summary>The gschema name of <paramref name="key"/>.</summary>
    public static string Name(SettingsKey key) => Info(key).Name;

    /// <summary>The key named <paramref name="name"/>, ignoring case as the registry does.</summary>
    public static bool TryParseKey(string name, out SettingsKey key) => ByName.TryGetValue(name, out key);

    /// <summary>The defaults by gschema name (macOS <c>registrationDefaults</c>).</summary>
    public static IReadOnlyDictionary<string, object> RegistrationDefaults() =>
        Keys.ToDictionary(k => k.Name, k => k.Default, StringComparer.Ordinal);

    /// <summary>
    /// Converts <paramref name="value"/> to the type of <paramref name="like"/>
    /// where a two-way binding of an integer key to a numeric control needs
    /// it (an int key and a double property, rounding half away from zero);
    /// anything else comes back unchanged. GTK's <c>coerce</c> behind
    /// <c>Store.Bind</c>.
    /// </summary>
    public static object? Coerce(object? value, object? like)
    {
        switch (like)
        {
            case double:
                switch (value)
                {
                    case int i:
                        return (double)i;
                    case uint u:
                        return (double)u;
                }
                break;
            case int:
                switch (value)
                {
                    case double d:
                        return (int)Math.Clamp(Math.Round(d, MidpointRounding.AwayFromZero), int.MinValue, int.MaxValue);
                    case uint u:
                        return unchecked((int)u);
                }
                break;
            case uint:
                switch (value)
                {
                    case int i:
                        return unchecked((uint)i);
                    case double d:
                        return (uint)Math.Clamp(Math.Round(d, MidpointRounding.AwayFromZero), uint.MinValue, uint.MaxValue);
                }
                break;
        }
        return value;
    }

    /// <summary>
    /// Calls <paramref name="handler"/> whenever <paramref name="key"/>
    /// changes, from any source. The handler may cancel its own token.
    /// </summary>
    public SettingsChangeToken OnChange(SettingsKey key, Action handler)
    {
        ArgumentNullException.ThrowIfNull(handler);
        return new SettingsChangeToken(hub, key, hub.Add(key, handler));
    }

    /// <summary>
    /// Stops listening to the backend; changes from outside no longer reach
    /// the handlers. The backend belongs to the caller and stays open.
    /// </summary>
    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) == 0)
        {
            backend.Changed -= OnBackendChanged;
        }
    }

    private void OnBackendChanged(object? sender, SettingsChangedEventArgs e)
    {
        if (Volatile.Read(ref disposed) != 0 || !TryParseKey(e.Key, out var key))
        {
            return;
        }
        if (context is null || SynchronizationContext.Current == context)
        {
            hub.Fire(key);
            return;
        }
        context.Post(static state =>
        {
            var (store, changed) = ((SettingsStore, SettingsKey))state!;
            if (Volatile.Read(ref store.disposed) == 0)
            {
                store.hub.Fire(changed);
            }
        }, (this, key));
    }

    // Storage

    private bool GetBoolean(SettingsKey key)
    {
        var info = Info(key);
        return backend.TryGetBoolean(info.Name, out var value) ? value : (bool)info.Default;
    }

    // The stored value or the default, before clamping.
    private int RawInt32(SettingsKeyInfo info) => backend.TryGetInt32(info.Name, out var value) ? value : (int)info.Default;

    private int GetInt32(SettingsKey key)
    {
        var info = Info(key);
        return Clamp(info, RawInt32(info));
    }

    private string RawString(SettingsKeyInfo info) => backend.TryGetString(info.Name, out var value) ? value : (string)info.Default;

    private string GetString(SettingsKey key) => RawString(Info(key));

    private T GetEnum<T>(SettingsKey key)
        where T : struct, Enum
    {
        var info = Info(key);
        return Nicks<T>.Parse(RawString(info)) ?? Nicks<T>.Parse((string)info.Default) ?? default;
    }

    // Always a fresh array, so a caller may keep it without touching the store.
    private string[] GetStringList(SettingsKey key)
    {
        var info = Info(key);
        return backend.TryGetStringList(info.Name, out var value) ? [.. value] : [];
    }

    // A write equal to the stored value is not a change and fires no
    // handler (as GSettings does).
    private void SetBoolean(SettingsKey key, bool value)
    {
        if (value == GetBoolean(key))
        {
            return;
        }
        backend.SetBoolean(Name(key), value);
        hub.Fire(key);
    }

    private void SetInt32(SettingsKey key, int value)
    {
        var info = Info(key);
        value = Clamp(info, value);
        if (value == RawInt32(info))
        {
            return;
        }
        backend.SetInt32(info.Name, value);
        hub.Fire(key);
    }

    private void SetString(SettingsKey key, string? value)
    {
        value ??= "";
        var info = Info(key);
        if (string.Equals(value, RawString(info), StringComparison.Ordinal))
        {
            return;
        }
        backend.SetString(info.Name, value);
        hub.Fire(key);
    }

    // A value outside the enum is ignored (GTK's SetColorScheme and friends).
    private void SetEnum<T>(SettingsKey key, T value)
        where T : struct, Enum
    {
        if (!Enum.IsDefined(value))
        {
            return;
        }
        var info = Info(key);
        var nick = Nicks<T>.Nick(value);
        if (nick == RawString(info))
        {
            return;
        }
        backend.SetString(info.Name, nick);
        hub.Fire(key);
    }

    private void SetStringList(SettingsKey key, IReadOnlyList<string>? value)
    {
        string[] list = value is null ? [] : [.. value];
        if (list.Any(entry => entry is null))
        {
            throw new ArgumentException("a settings list cannot hold null", nameof(value));
        }
        if (list.SequenceEqual(GetStringList(key), StringComparer.Ordinal))
        {
            return;
        }
        backend.SetStringList(Name(key), list);
        hub.Fire(key);
    }

    private static int Clamp(SettingsKeyInfo info, int value)
    {
        if (info.Minimum is int min && value < min)
        {
            return min;
        }
        if (info.Maximum is int max && value > max)
        {
            return max;
        }
        return value;
    }

    // The gschema nicks of an enum: its member names in lower case, in
    // declaration order (the gschema's value order).
    private static class Nicks<T>
        where T : struct, Enum
    {
        private static readonly T[] Values = Enum.GetValues<T>();

        public static ReadOnlyCollection<string> All { get; } =
            new ReadOnlyCollection<string>([.. Enum.GetNames<T>().Select(name => name.ToLowerInvariant())]);

        public static string Nick(T value) => All[Array.IndexOf(Values, value)];

        public static T? Parse(string nick)
        {
            for (var i = 0; i < All.Count; i++)
            {
                if (string.Equals(All[i], nick, StringComparison.Ordinal))
                {
                    return Values[i];
                }
            }
            return null;
        }
    }
}
