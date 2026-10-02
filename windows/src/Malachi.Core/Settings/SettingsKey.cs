// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.Key);
// GTK: ui/internal/settings/store.go (the Key constants). The keys of
// data/io.github.schotek.Malachi.gschema.xml in its order, the window
// geometry included (GTK declares it but never writes it; Windows keeps the
// window there, docs/windows-port.md §8), plus the Windows-only ctrl-r, the
// counterpart of macOS's command-r. SettingsStore.Schema has the names,
// types, defaults and ranges.

namespace Malachi.Core.Settings;

/// <summary>A key of the settings schema.</summary>
public enum SettingsKey
{
    /// <summary><c>window-width</c>.</summary>
    WindowWidth,

    /// <summary><c>window-height</c>.</summary>
    WindowHeight,

    /// <summary><c>window-maximized</c>.</summary>
    WindowMaximized,

    /// <summary><c>folder-pane-width</c>.</summary>
    FolderPaneWidth,

    /// <summary><c>message-list-width</c>.</summary>
    MessageListWidth,

    /// <summary><c>collapsed-folders</c>.</summary>
    CollapsedFolders,

    /// <summary><c>collapsed-accounts</c>.</summary>
    CollapsedAccounts,

    /// <summary><c>favourite-folders</c>.</summary>
    FavouriteFolders,

    /// <summary><c>launch-at-login</c>.</summary>
    LaunchAtLogin,

    /// <summary><c>run-in-background</c>.</summary>
    RunInBackground,

    /// <summary><c>mark-read-delay</c>.</summary>
    MarkReadDelay,

    /// <summary><c>confirm-delete</c>.</summary>
    ConfirmDelete,

    /// <summary><c>desktop-notifications</c>.</summary>
    DesktopNotifications,

    /// <summary><c>notification-sound</c>.</summary>
    NotificationSound,

    /// <summary><c>color-scheme</c>.</summary>
    ColorScheme,

    /// <summary><c>message-list-density</c>.</summary>
    Density,

    /// <summary><c>show-preview-line</c>.</summary>
    ShowPreviewLine,

    /// <summary><c>group-by-conversation</c>.</summary>
    GroupByConversation,

    /// <summary><c>search-scope</c>.</summary>
    SearchScope,

    /// <summary><c>show-avatars</c>.</summary>
    ShowAvatars,

    /// <summary><c>monochrome-avatars</c>.</summary>
    MonochromeAvatars,

    /// <summary><c>monospace-plain-text</c>.</summary>
    MonospacePlainText,

    /// <summary><c>text-zoom</c>.</summary>
    TextZoom,

    /// <summary><c>assistant-menu</c>.</summary>
    AssistantMenu,

    /// <summary><c>assistant-target</c>.</summary>
    AssistantTarget,

    /// <summary><c>assistant-model</c>.</summary>
    AssistantModel,

    /// <summary><c>assistant-claude-path</c>.</summary>
    AssistantClaudePath,

    /// <summary><c>assistant-consent</c>.</summary>
    AssistantConsent,

    /// <summary><c>assistant-provider</c>, independent of the external hand-off target.</summary>
    AssistantProvider,

    /// <summary><c>assistant-codex-path</c>.</summary>
    AssistantCodexPath,

    /// <summary><c>assistant-chatgpt-model</c>.</summary>
    AssistantChatGptModel,

    /// <summary><c>assistant-chatgpt-consent-version</c>.</summary>
    AssistantChatGptConsentVersion,

    /// <summary><c>board-default-style</c>; reserved for the Board UI port.</summary>
    BoardDefaultStyle,

    /// <summary><c>board-triage-consent</c>; reserved for the Board UI port.</summary>
    BoardTriageConsent,

    /// <summary><c>board-triage-model</c>; reserved for the Board UI port.</summary>
    BoardTriageModel,

    /// <summary><c>board-triage-chatgpt-model</c>; reserved for the Board UI port.</summary>
    BoardChatGptModel,

    /// <summary><c>board-triage-chatgpt-consent-version</c>; separate OpenAI Board disclosure.</summary>
    BoardChatGptConsentVersion,

    /// <summary><c>ctrl-r</c>, Windows only: what Ctrl+R does.</summary>
    CtrlR,
}
