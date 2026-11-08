// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's triage run (ui/internal/assistant triage.go; docs/mcp.md
// "Triage of the board", docs/security.md §10.2): the pure half of the
// one-shot request `BoardTriageController` sends the user's assistant. It
// is the panel's command line (`Assistant.args`: no built-in tool, nothing
// of the user's setup, no session on disk) with the bridge started for the
// run as the daemon's prompt of purpose triage says (docs/api.md §4.18:
// its `bridgeArgs` are `--in-app --allow-triage --triage-run <runId>
// --triage-max <limit>`, never --allow-modify or --allow-send, and its
// `tools` the bridge's read tools, create_draft for a manual run only
// (`triageAutomaticDrafts`) and the three triage tools). The procedure and
// the rules live in the bridge's tool descriptions and server
// instructions; the system prompt and the message are the daemon's. Here
// stay the rules of the run that the client keeps: whether a trigger's run
// may create drafts, the accepted-note parsing, the bridge's limit, the
// run's source, timeout and batch.

import Foundation

extension Assistant {
    /// The rule (kept when suggested replies became local drafts): an
    /// automatic run creates no drafts. Nobody
    /// watches it; a suggested reply now stays on this device as the case's
    /// local draft (never in the Drafts folder, docs/mcp.md "A suggested
    /// reply on the board"), but it is still written to the recipients the
    /// original names, maybe from a hostile Reply-To, on a Jira case it is a
    /// public comment draft, and the user edits and sends it from the board
    /// itself. So an automatic run gets no create_draft and is told to skip
    /// suggested replies; a manual run, which the user started and watches,
    /// keeps it. Flip this to give automatic runs drafts again.
    public static let triageAutomaticDrafts = false

    /// assistant.TriageDrafts: whether a run started by `trigger` may
    /// create drafts (`TriageInput.drafts`).
    public static func triageDrafts(for trigger: Board.TriageTrigger) -> Bool {
        trigger == .manual || triageAutomaticDrafts
    }

    /// The triage tool whose accepted calls are the run's progress
    /// (without the bridge's prefix, as `Event.tool` names it).
    public static let triageAnnotateTool = "annotate_case"

    /// The start of the text of every accepted annotate_case result:
    /// "annotated case <caseId>: …" (the bridge's annotateCase).
    static let triageAnnotatedPrefix = "annotated case "

    /// assistant.TriageAnnotatedCase: the case an accepted annotate_case
    /// result names (its text, `Event.resultText`), nil when the text does
    /// not start the bridge's way or the id is not one: 1 to 64 bytes of
    /// ASCII letters, digits, "_" and "-". The bridge accepts a second
    /// annotation of a case without charging another of the run's cases,
    /// so the run counts the distinct cases.
    public static func triageAnnotatedCase(_ result: String) -> String? {
        let bytes = Array(result.utf8)
        let prefix = Array(triageAnnotatedPrefix.utf8)
        guard bytes.starts(with: prefix) else { return nil }
        let rest = bytes[prefix.count...]
        guard let colon = rest.firstIndex(of: UInt8(ascii: ":")) else { return nil }
        let id = rest[rest.startIndex ..< colon]
        guard !id.isEmpty, id.count <= 64 else { return nil }
        for c in id {
            let ok = (c >= 0x61 && c <= 0x7A) || (c >= 0x41 && c <= 0x5A) || (c >= 0x30 && c <= 0x39)
                || c == UInt8(ascii: "_") || c == UInt8(ascii: "-")
            guard ok else { return nil }
        }
        return String(decoding: id, as: UTF8.self)
    }

    /// What the bridge's --triage-max accepts.
    public static let triageMaxRange: ClosedRange<Int> = 1 ... 200

    /// `n` within `triageMaxRange`.
    public static func clampTriageMax(_ n: Int) -> Int {
        min(max(n, triageMaxRange.lowerBound), triageMaxRange.upperBound)
    }

    /// The run's `source` for `board.runStart` when the user's Claude Code
    /// runs it: what the bridge reports as its client's name for the
    /// annotations of the run (ChatGPT's is "malachi-chatgpt", an `ai`
    /// account's "api:<provider>"; `BoardTriageController.source`).
    public static let triageSource = "claude-code"

    /// How long a triage run may take before it is ended as a timeout.
    public static let triageTimeout: Duration = .seconds(15 * 60)

    /// The most cases one run is asked to triage (a manual run, and the
    /// most an automatic run takes of the day's remaining cases).
    public static let triageBatch = 40
}
