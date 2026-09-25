// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/window/drafts_test.go.
@Suite struct DraftsTests {
    @Test func inDraftsTest() {
        let m = MailModel(
            accounts: [testAccount("a")],
            folders: ["a": [
                testFolder("in", path: "INBOX", role: .inbox),
                testFolder("dr", path: "Drafts", role: .drafts),
            ]]
        )
        var inbox = summary("1")
        inbox.accountId = "a"
        inbox.folderId = "in"
        #expect(!m.inDrafts(inbox), "inbox message reported as a draft")
        var draft = summary("2")
        draft.accountId = "a"
        draft.folderId = "dr"
        #expect(m.inDrafts(draft), "message of the Drafts folder not recognised")
        // The folder of another account with the same id is not this one.
        var other = summary("3")
        other.accountId = "b"
        other.folderId = "dr"
        #expect(!m.inDrafts(other), "draft of an unknown account")
    }

    @Test func draftOpenErrorsTest() {
        #expect(draftOpenUnsupported(RPCError(code: .methodNotFound, message: "x")))
        #expect(draftOpenUnsupported(RPCError(code: .notImplemented, message: "x")))
        #expect(!draftOpenUnsupported(RPCError(code: .unavailable, message: "x")))
        #expect(!draftOpenUnsupported(CancellationError()))
        #expect(draftOpenErrorText(RPCError(code: .unavailable, message: "not downloaded"))
            == "The draft has not been downloaded yet; try again in a moment")
        #expect(draftOpenErrorText(RPCError(code: .messageNotFound, message: "gone")) == "Opening the draft failed")
        #expect(draftSkippedText(2) == "2 attachments of the draft could not be opened")
    }
}
