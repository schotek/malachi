// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first provider UI from docs/chatgpt-integration.md §8;
// GTK msgid reference: ui/internal/assistant/chatgpt.go. No Swift UI yet.

using Malachi.Core.I18n;

namespace Malachi.App.Assistants;

internal static class ChatGptText
{
    public static string Provider => L10n.T("In-app provider");
    public static string Name => L10n.T("ChatGPT (Codex, experimental)");
    public static string Description => L10n.T("Connect ChatGPT to use your plan with the in-app assistant.");
    public static string Footer => L10n.T("Mail you ask about is sent to OpenAI using your ChatGPT plan");
    public static string Codex => L10n.T("Codex");
    public static string Install => L10n.T("Get Codex…");
    public static string SignIn => L10n.T("Continue with ChatGPT");
    public static string Disconnect => L10n.T("Disconnect");
    public static string Usage => L10n.T("Manage usage");
    public static string Disconnected => L10n.T("Not connected");
    public static string Connecting => L10n.T("Connecting…");
    public static string Connected(string email) => L10n.T("Connected as %s", email);
    public static string Reconnect => L10n.T("Reconnect to ChatGPT");
    public static string ConnectionFailed => L10n.T("Could not connect to ChatGPT.");
    public static string RevocationUnconfirmed => L10n.T("Disconnected locally; remote sign-out could not be confirmed.");
    public static string MissingCodex => L10n.T("Codex was not found. Choose a native codex.exe.");
    public static string DefaultModel => L10n.T("Use the provider’s default model");
    public static string ModelsUnavailable => L10n.T("Model catalog unavailable. You can keep the provider’s default model.");
    public static string ConsentHeading => L10n.T("Send Mail to OpenAI?");
    public static string ConsentBody => L10n.T("Malachi Mail will send the selected mail and text you provide to OpenAI through Codex, using your ChatGPT plan. The assistant can read mail and prepare drafts. It cannot send, delete or move messages. This experimental integration does not import your ChatGPT conversations or memory.");
}
