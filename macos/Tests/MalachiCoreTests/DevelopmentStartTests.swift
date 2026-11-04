// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Testing
@testable import MalachiCore

// The pure half of the MALACHI_START steps: the schedule with `wait=`,
// the split of a step, and the values of the assistant's steps.
struct DevelopmentStartTests {
    @Test func scheduleIsTheIntervalUnlessAStepWaits() {
        let steps = ["mail", "row=DevConf", "triage", "wait=30", "shot=/a.png", "wait=x", "quit"]
        #expect(DevelopmentStart.schedule(steps, interval: 4) == [0, 4, 8, 12, 42, 46, 50])
        #expect(DevelopmentStart.schedule([], interval: 4) == [])
        #expect(DevelopmentStart.schedule(["quit"], interval: 2.5) == [0])
    }

    @Test func waitSeconds() {
        #expect(DevelopmentStart.waitSeconds("wait=30") == 30)
        #expect(DevelopmentStart.waitSeconds("wait=1.5") == 1.5)
        #expect(DevelopmentStart.waitSeconds("wait=0") == 0)
        #expect(DevelopmentStart.waitSeconds("wait=-1") == nil)
        #expect(DevelopmentStart.waitSeconds("wait") == nil)
        #expect(DevelopmentStart.waitSeconds("waiting=3") == nil)
        #expect(DevelopmentStart.waitSeconds("row=wait=3") == nil)
    }

    @Test func splitKeepsTheValueWhole() {
        #expect(DevelopmentStart.split("row=DevConf").name == "row")
        #expect(DevelopmentStart.split("row=DevConf").value == "DevConf")
        #expect(DevelopmentStart.split("quit").name == "quit")
        #expect(DevelopmentStart.split("quit").value == "")
        #expect(DevelopmentStart.split("assistant-say=a = b").value == "a = b")
        #expect(DevelopmentStart.split("board:list:select").name == "board:list:select")
    }

    @Test func assistantValues() {
        #expect(DevelopmentStart.assistantAction("tasks") == .tasks)
        #expect(DevelopmentStart.assistantAction("draft-reply") == .draftReply)
        #expect(DevelopmentStart.assistantAction("summarize") == .summarize)
        #expect(DevelopmentStart.assistantAction("ask") == .ask)
        #expect(DevelopmentStart.assistantAction("unread") == nil)
        #expect(DevelopmentStart.rewrite("politer") == .politer)
        #expect(DevelopmentStart.rewrite("english") == .toEnglish)
        #expect(DevelopmentStart.rewrite("custom") == nil)
    }

    @Test func composeFields() {
        let f = DevelopmentStart.composeFields("Eva Králová <eva.kralova@bluebird.test>|Site visit next week|Hi, I got your request.\\nJana")
        #expect(f?.to == Address(name: "Eva Králová", address: "eva.kralova@bluebird.test"))
        #expect(f?.subject == "Site visit next week")
        #expect(f?.paragraphs == ["Hi, I got your request.", "Jana"])
        let plain = DevelopmentStart.composeFields("eva@bluebird.test|Subject|One paragraph with a | in it")
        #expect(plain?.to == Address(address: "eva@bluebird.test"))
        #expect(plain?.paragraphs == ["One paragraph with a | in it"])
        #expect(DevelopmentStart.composeFields("|No recipient|Text")?.to == nil)
        #expect(DevelopmentStart.composeFields("only|two") == nil)
    }
}
