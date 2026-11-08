// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantRequest.swift
// (AssistantRequest.Failure, text, reason); GTK:
// ui/internal/assistantpanel/oneshot.go (Failure, FailureKind, Text,
// ReasonText, providerText) and provider.go (FailureLimit). Swift's enum
// with an associated value is a closed record hierarchy, compared by value
// as the enum is; a provider's reason (a ChatGPT code, an ai account's
// "api_<class>") is said in that provider's words (ProviderText).

using Malachi.Core.Assistants;
using Malachi.Core.I18n;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantRequest
{
    /// <summary>Why a request brought no answer.</summary>
    public abstract record Failure
    {
        // Only the cases below derive from it.
        private Failure()
        {
        }

        /// <summary>
        /// The line where the panel's errors are shown (the compose window's
        /// popover): the panel's texts, and for a missing sign-in where to
        /// sign in (a request has no Sign In… of its own); a provider's
        /// reason in that provider's words (<see cref="ProviderText"/>).
        /// </summary>
        public string Text => this switch
        {
            NotFound => Assistant.PanelTexts().NotFound,
            NotSignedIn n => ProviderText(n.Code, signIn: true) ?? Assistant.SignInTexts().Hint,
            Stopped s => ProviderText(s.Detail) ?? Assistant.StoppedText(s.Detail),
            ToolsMissing t => ProviderText(t.Code) ?? Assistant.PanelTexts().ToolsMissing,
            Limit l => ProviderText(l.Code) ?? Assistant.StoppedText(Boards.Board.Text.TriageFailureText(Boards.Board.TriageFailure.Limit)),
            _ => Assistant.StoppedText(""),
        };

        /// <summary>The reason inside another sentence ("The search could not be converted: %s").</summary>
        public string Reason => this switch
        {
            NotFound => Assistant.PanelTexts().NotFound,
            NotSignedIn n => ProviderText(n.Code, signIn: true) ?? Assistant.SignInTexts().Hint,
            Stopped s => ProviderText(s.Detail) ?? s.Detail,
            ToolsMissing t => ProviderText(t.Code) ?? Assistant.PanelTexts().ToolsMissing,
            Limit l => ProviderText(l.Code) ?? Boards.Board.Text.TriageFailureText(Boards.Board.TriageFailure.Limit),
            _ => "",
        };

        /// <summary>
        /// The failure is the reason of a session the daemon runs over an
        /// ai account ("api_&lt;class&gt;").
        /// </summary>
        public bool IsApiFailure => this switch
        {
            NotSignedIn n => IsApi(n.Code),
            Stopped s => IsApi(s.Detail),
            ToolsMissing t => IsApi(t.Code),
            Limit l => IsApi(l.Code),
            _ => false,
        };

        /// <summary>
        /// assistantpanel.Failure.providerText: what a provider's reason says
        /// in that provider's words: a ChatGPT connection to make again (a
        /// missing sign-in with a "chatgpt_" reason), or the sentence of a
        /// session's failure class of the daemon's assistant
        /// (<see cref="Assistant.ApiFailureText"/>). Null for any other reason
        /// (and for Claude Code's, whose reasons are never these codes).
        /// </summary>
        public static string? ProviderText(string reason, bool signIn = false)
        {
            if (signIn && reason.StartsWith("chatgpt_", System.StringComparison.Ordinal))
            {
                return L10n.T("Reconnect to ChatGPT");
            }
            return Assistant.ApiFailureText(reason);
        }

        private static bool IsApi(string reason) => reason.StartsWith(Assistant.ApiFailurePrefix, System.StringComparison.Ordinal);

        /// <summary>Claude Code was not found on this computer.</summary>
        public sealed record NotFound : Failure;

        /// <summary>
        /// Claude Code says it is not signed in, or the API refused its
        /// sign-in; for a provider, its reason (a ChatGPT connection to make
        /// again, an ai account's missing consent or refused key:
        /// "api_consent_required", "api_auth"), "" for Claude Code's own.
        /// </summary>
        /// <param name="Code">The provider's reason; "" for Claude Code.</param>
        public sealed record NotSignedIn(string Code = "") : Failure;

        /// <summary>
        /// It ended badly; the reason is technical (the result's text or
        /// subtype, stderr's first line, the timeout, a launch failure).
        /// </summary>
        /// <param name="Detail">The technical reason.</param>
        public sealed record Stopped(string Detail) : Failure;

        /// <summary>
        /// A request with <see cref="Tools"/>: Claude Code did not report the
        /// bridge connected; for an ai account, the reason of a model without
        /// tools, a server without the model or a bridge the daemon could not
        /// start ("api_toolsUnsupported", "api_model", "api_bridge"), whose
        /// sentence it says (Go's <c>Failure.Reason</c>; Swift's case
        /// carries none and says the panel's words).
        /// </summary>
        /// <param name="Code">The provider's reason; "" for Claude Code.</param>
        public sealed record ToolsMissing(string Code = "") : Failure;

        /// <summary>
        /// The provider refused the request because the usage limit of the
        /// user's plan was reached (ChatGPT's HTTP 429, an API provider's rate
        /// limit or exhausted credit).
        /// </summary>
        /// <param name="Code">The provider's code.</param>
        public sealed record Limit(string Code) : Failure;
    }
}
