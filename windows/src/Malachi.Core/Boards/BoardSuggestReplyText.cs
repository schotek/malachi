// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSuggestReplyText.swift; Go:
// ui/internal/board/reply.go (SuggestReply, SuggestFollowUp,
// SuggestReplyTitle … SuggestReplyFailureText), which holds the msgids.
//
// The texts of the case detail's Suggest Reply. The reasons are the
// triage's where the meaning is the same; Stop and the sign-in hint are
// the assistant panel's. Swift's suggestReplyFailure(_:) is Go's
// SuggestReplyFailureText (a member named like the enum would hide it).

using Malachi.Core.I18n;

namespace Malachi.Core.Boards;

public static partial class Board
{
    public static partial class Text
    {
        /// <summary>The button that asks the assistant for a reply draft.</summary>
        public static string SuggestReply => L10n.T("✦ Suggest Reply");

        /// <summary>
        /// The button on a case waiting for someone else
        /// (<see cref="IsFollowUp"/>): the assistant writes a follow-up to the
        /// user's own last message.
        /// </summary>
        public static string SuggestFollowUp =>
            // TRANSLATORS: a button in a conversation's detail on the board, for a
            // conversation where the user waits for an answer: the assistant writes
            // a polite follow-up to the user's own last message.
            L10n.T("✦ Suggest Follow-up");

        /// <summary>The button: <see cref="SuggestFollowUp"/> for a follow-up, else <see cref="SuggestReply"/>.</summary>
        public static string SuggestReplyTitle(bool followUp) => followUp ? SuggestFollowUp : SuggestReply;

        /// <summary>The placeholder of the instruction field.</summary>
        public static string SuggestReplyPlaceholder => L10n.T("What should the reply say? (optional)");

        /// <summary>The progress while the assistant writes.</summary>
        public static string SuggestReplyRunning => L10n.T("Writing a suggested reply…");

        /// <summary>The control's line while the request runs for another case.</summary>
        public static string SuggestReplyElsewhere => L10n.T("The assistant is writing a reply for another conversation");

        /// <summary>How a request failed: "The suggested reply failed: it took too long."</summary>
        public static string SuggestReplyFailed(SuggestReplyFailure f) =>
            L10n.T("The suggested reply failed: %s.", SuggestReplyFailureText(f));

        /// <summary>Why a request failed, inside a sentence (Swift <c>suggestReplyFailure</c>).</summary>
        public static string SuggestReplyFailureText(SuggestReplyFailure f) => f switch
        {
            SuggestReplyFailure.NotFound => TriageFailureText(TriageFailure.NotFound),
            SuggestReplyFailure.NotSignedIn => TriageFailureText(TriageFailure.NotSignedIn),
            SuggestReplyFailure.ToolsMissing => TriageFailureText(TriageFailure.ToolsMissing),
            SuggestReplyFailure.Timeout => TriageFailureText(TriageFailure.Timeout),
            SuggestReplyFailure.Cancelled => TriageFailureText(TriageFailure.Cancelled),
            SuggestReplyFailure.Stopped => TriageFailureText(TriageFailure.Stopped),
            SuggestReplyFailure.Backend => TriageFailureText(TriageFailure.Backend),
            SuggestReplyFailure.NoDraft => L10n.T("the assistant wrote no reply"),
            SuggestReplyFailure.Limit => TriageFailureText(TriageFailure.Limit),
            _ => "",
        };
    }
}
