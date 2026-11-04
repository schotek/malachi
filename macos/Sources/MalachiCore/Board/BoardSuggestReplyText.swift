// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The texts of the case detail's Suggest Reply. One to one with the Go
// reference ui/internal/board (reply.go), which holds the msgids; every
// text goes through L10n with the msgid as the key. The reasons are the
// triage's where the meaning is the same; Stop and the sign-in hint are
// the panel's.

import Foundation

extension Board.Text {
    public static var suggestReply: String { L10n.T("✦ Suggest Reply") }
    /// The button on a case waiting for someone else (`Board.isFollowUp`):
    /// the assistant writes a follow-up to the user's own last message.
    public static var suggestFollowUp: String { L10n.T("✦ Suggest Follow-up") }

    /// The button: `suggestFollowUp` for a follow-up, else `suggestReply`.
    public static func suggestReplyTitle(followUp: Bool) -> String {
        followUp ? suggestFollowUp : suggestReply
    }
    public static var suggestReplyPlaceholder: String { L10n.T("What should the reply say? (optional)") }
    public static var suggestReplyRunning: String { L10n.T("Writing a suggested reply…") }
    /// The control's line while the request runs for another case.
    public static var suggestReplyElsewhere: String {
        L10n.T("The assistant is writing a reply for another conversation")
    }

    /// How a request failed: "The suggested reply failed: it took too long."
    public static func suggestReplyFailed(_ f: Board.SuggestReplyFailure) -> String {
        L10n.T("The suggested reply failed: %s.", suggestReplyFailure(f))
    }

    /// Why a request failed, inside a sentence.
    public static func suggestReplyFailure(_ f: Board.SuggestReplyFailure) -> String {
        switch f {
        case .notFound: return triageFailure(.notFound)
        case .notSignedIn: return triageFailure(.notSignedIn)
        case .toolsMissing: return triageFailure(.toolsMissing)
        case .timeout: return triageFailure(.timeout)
        case .cancelled: return triageFailure(.cancelled)
        case .stopped: return triageFailure(.stopped)
        case .backend: return triageFailure(.backend)
        case .noDraft: return L10n.T("the assistant wrote no reply")
        case .limit: return triageFailure(.limit)
        }
    }
}
