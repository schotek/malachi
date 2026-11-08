// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The application's hooks into the settings window that the rest of the
/// application calls (`AppState.Hooks`): Settings → Integrations, and the
/// AI provider account dialogs over it (docs/plans/ai-api.md §7.2, §7.4):
/// the assistant panel's "Set Up in Integrations…", the status bar's and
/// the banner's Sign In of an `ai` account whose key the provider
/// refused, the board's Triage button, and the App Store build's "Add
/// Anthropic API…". The settings window opens (or comes to the front) on
/// Integrations first, and the dialog is a sheet on it.
@MainActor
enum PreferencesHooks {
    static func install(in state: AppState) {
        state.hooks.openIntegrationsSettings = { [weak state] in
            guard let state else { return }
            Integration.showPreferences(state).select(.integrations)
        }
        state.hooks.editAIAccount = { [weak state] account, replaceKey in
            guard let state else { return }
            let prefs = Integration.showPreferences(state)
            prefs.select(.integrations)
            prefs.integrationsPane.editAIAccount(account, replaceKey: replaceKey, over: prefs.window)
        }
        state.hooks.addAIAccount = { [weak state] provider in
            guard let state else { return }
            let prefs = Integration.showPreferences(state)
            prefs.select(.integrations)
            prefs.integrationsPane.addAIAccount(provider, over: prefs.window)
        }
    }
}
