// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's Suggest Reply (ui/internal/assistant suggest_reply.go;
// docs/mcp.md "A suggested reply on the board", docs/security.md §10.2):
// the pure half of the one-shot request `BoardReplyController` sends the
// user's assistant for one case, on the user's click. It is the panel's
// command line (`Assistant.args`) with the bridge started as the daemon's
// prompt of purpose reply says (docs/api.md §4.18: `bridgeArgs` `--in-app
// --reply-only <replyMessageId>`, `tools` read_message, list_messages and
// a create_draft that accepts only a reply to exactly that message). The
// system prompt and the message of `ReplyInput` are the daemon's; here
// stay the draft tool's name, the timeout and the cleaning of the user's
// instruction.

import Foundation

extension Assistant {
    /// The tool whose result names the draft (`parseDraftResult`), without
    /// the bridge's prefix, as `Event.tool` names it.
    public static let suggestReplyDraftTool = "create_draft"

    /// How long a suggested reply may take before it ends as a timeout.
    public static let suggestReplyTimeout: Duration = .seconds(120)

    /// The longest instruction the message carries, in characters (Unicode
    /// scalars).
    public static let suggestReplyMaxInstruction = 500

    /// assistant.CleanSuggestReplyInstruction: the instruction field's text
    /// as it goes into `ReplyInput.instruction`: every run of white space
    /// one space, control and bidirectional formatting characters left
    /// out, trimmed, cut to `suggestReplyMaxInstruction` characters.
    public static func cleanSuggestReplyInstruction(_ text: String) -> String {
        var out = String.UnicodeScalarView()
        var count = 0
        var space = false
        for u in text.unicodeScalars {
            let r = u.value
            if isSpace(r) {
                space = !out.isEmpty
                continue
            }
            if isControl(r) || isBidiControl(r) {
                continue
            }
            if space {
                guard count < suggestReplyMaxInstruction - 1 else { break }
                out.append(" ")
                count += 1
                space = false
            }
            guard count < suggestReplyMaxInstruction else { break }
            out.append(u)
            count += 1
        }
        return String(out)
    }
}
