// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ComposeDraftController.editorLoaded (compose/pane.go `editorLoaded`): a
// window that started with the user's own text (`ComposeParams.ownHTML`, the
// chat's quick-reply bar) marks its draft dirty once the editor loaded it,
// behind one flush, so that the autosave and the question on close apply;
// once, not again after a crash's reload; the board's pane still learns how
// the editor writes its draft. No RPC is made (the autosave is long), so the
// client never connects.

/// A form whose editor reports its text only with a flush (`unreported`),
/// as the bridge does before its first `changed`.
@MainActor
private final class Form: ComposeForm {
    var account = testAccount("a", email: "me@example.invalid")
    var isComment = false
    var subject = "Re: Lunch"
    var attachments: [DraftAttachment] = []
    var html = ""
    var text = ""
    var unreported: String?
    var flushes = 0
    var closes = 0

    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool) {
        ([Address(name: "Jana", address: "jana@example.org")], [], [], true)
    }
    func editorHTML() -> String { html }
    func editorText() -> String { text }
    func flushEditor(_ done: @escaping @MainActor () -> Void) {
        flushes += 1
        if let s = unreported {
            unreported = nil
            html = "<p>\(s)</p>"
            text = s
        }
        done()
    }
    func setAttachments(_ list: [DraftAttachment]) { attachments = list }
    func setStatus(_ text: String) {}
    func toast(_ text: String) {}
    func setSendEnabled(_ enabled: Bool) {}
    func closeWindow() { closes += 1 }
}

@MainActor
private func controller(_ form: Form, owner: DraftOwner = .window) -> ComposeDraftController {
    let dc = ComposeDraftController(
        client: RPCClient(socketPath: "/nonexistent/malachi-test.sock"), settings: ScratchSettings().settings,
        placeholder: { false }, registry: CIDRegistry(), autosaveDelay: .seconds(600), owner: owner)
    dc.form = form
    return dc
}

@MainActor
struct ComposeDraftOwnTextTests {
    /// The own text is the user's edit: dirty behind one flush, the
    /// autosave armed, and closing asks.
    @Test func ownTextMarksTheDraftDirty() {
        let form = Form()
        form.unreported = "Thanks"
        let dc = controller(form)
        #expect(dc.canCloseWithoutAsking)
        dc.editorLoaded(ownText: true)
        #expect(form.flushes == 1)
        #expect(dc.draft.dirty)
        #expect(dc.autosaveArmed)
        #expect(!dc.canCloseWithoutAsking)
        dc.cleanup()
    }

    @Test func onceNotAgainAfterAReload() {
        let form = Form()
        let dc = controller(form)
        dc.editorLoaded(ownText: true)
        dc.editorLoaded(ownText: true)
        #expect(form.flushes == 1)
        dc.cleanup()
    }

    /// Without own text nothing is flushed or marked: the prefill is no
    /// edit and the window closes unasked.
    @Test func withoutOwnTextNothingChanges() {
        let form = Form()
        let dc = controller(form)
        dc.editorLoaded(ownText: false)
        #expect(form.flushes == 0)
        #expect(!dc.draft.dirty)
        #expect(dc.canCloseWithoutAsking)
    }

    /// A comment whose editor never reported its text counted as empty and
    /// would close unasked; the flush makes the own text known, so it asks.
    @Test func aCommentWithOwnTextAsks() {
        let form = Form()
        form.isComment = true
        form.unreported = "Fixed in 1.2"
        let dc = controller(form)
        #expect(dc.canCloseWithoutAsking)
        dc.editorLoaded(ownText: true)
        #expect(form.text == "Fixed in 1.2")
        #expect(!dc.canCloseWithoutAsking)
        dc.cleanup()
    }

    /// The board's pane learns how the editor writes its draft (one flush,
    /// `editorReady`), after every reload; it never has own text.
    @Test func theBoardStillLearnsTheEditor() {
        let form = Form()
        let dc = controller(form, owner: .board)
        dc.editorLoaded(ownText: false)
        dc.editorLoaded(ownText: false)
        #expect(form.flushes == 2)
        #expect(!dc.draft.dirty)
    }

    /// Closing before the editor reported `ready` asks: the form set
    /// `ownTextPending` before it loaded the editor, and the draft is not
    /// dirty yet (a comment's editor has reported no text either).
    /// `editorLoaded` clears it once the draft is marked dirty.
    @Test func closingBeforeReadyAsks() {
        for comment in [false, true] {
            let form = Form()
            form.isComment = comment
            form.unreported = "Thanks"
            let dc = controller(form)
            dc.ownTextPending = true
            #expect(!dc.draft.dirty)
            #expect(!dc.canCloseWithoutAsking)
            dc.editorLoaded(ownText: true)
            #expect(!dc.ownTextPending)
            #expect(dc.draft.dirty)
            #expect(!dc.canCloseWithoutAsking)
            dc.cleanup()
        }
    }

    /// A closed window marks nothing.
    @Test func aClosedWindowMarksNothing() {
        let form = Form()
        let dc = controller(form)
        dc.cleanup()
        dc.editorLoaded(ownText: true)
        #expect(form.flushes == 0)
        #expect(!dc.draft.dirty)
    }
}
