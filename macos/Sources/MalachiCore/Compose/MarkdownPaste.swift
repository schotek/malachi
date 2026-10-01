// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A paste of plain text that looks like Markdown (the editor bridge's
// "paste" message, `bridgeJS`): the daemon renders it (draft.markdown) and
// the editor inserts the HTML, or the text as it is.

import Foundation

/// The HTML a paste of `text` puts into the editor: the daemon's rendering
/// when the text reads as Markdown (`DraftMarkdownResult.insertion`); nil,
/// the text pasted as it is, when it does not, when it is over the
/// daemon's limit, and on any error (no daemon, a daemon without the
/// method).
public func markdownPaste(_ text: String, client: RPCClient) async -> String? {
    guard !text.isEmpty, text.utf8.count <= API.Limits.maxDraftBodyBytes else {
        return nil
    }
    guard let result = try? await client.call(API.DraftMarkdown.self, DraftMarkdownParams(text: text)) else {
        return nil
    }
    return result.insertion
}
