// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// A throwaway defaults domain, wiped when the test is done.
private final class Scratch {
    let suite: String
    let defaults: UserDefaults
    let settings: Settings

    @MainActor init() {
        suite = "io.github.schotek.Malachi.test-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
        settings = Settings(defaults: defaults)
    }

    deinit {
        defaults.removePersistentDomain(forName: suite)
    }
}

/// The counterpart of ui/internal/settings/store_test.go.
@MainActor
@Suite(.serialized) struct SettingsTests {
    @Test func defaults() {
        let s = Scratch().settings
        #expect(s.colorScheme == .system)
        #expect(s.density == .comfortable)
        #expect(s.showPreviewLine)
        #expect(s.showAvatars)
        #expect(!s.monochromeAvatars)
        #expect(!s.monospacePlainText)
        #expect(!s.groupByConversation)
        #expect(s.textZoom == 100)
        #expect(!s.launchAtLogin)
        #expect(!s.runInBackground)
        #expect(s.confirmDelete)
        #expect(s.desktopNotifications)
        #expect(!s.notificationSound)
        #expect(s.markReadDelay == 2)
        #expect(s.commandR == .reply)
        #expect(s.collapsedFolders.isEmpty)
        #expect(s.collapsedAccounts.isEmpty)
        #expect(s.favouriteFolders.isEmpty)
        s.markReadDelay = 999
        #expect(s.markReadDelay == Settings.markReadDelayMax)
        #expect(s.searchScope == .folder)
        #expect(Settings.Key.allCases.count == 19)
        #expect(Set(Settings.registrationDefaults().keys) == Set(Settings.Key.allCases.map(\.rawValue)))
    }

    @Test func setAndNotify() {
        let scratch = Scratch()
        let s = scratch.settings
        var calls = 0
        let token = s.onChange(.textZoom) { calls += 1 }

        s.textZoom = 120
        #expect(s.textZoom == 120)
        #expect(calls == 1)
        #expect(scratch.defaults.integer(forKey: "text-zoom") == 120)
        s.textZoom = 120 // unchanged: no notification
        #expect(calls == 1)
        token.cancel()
        s.textZoom = 130
        #expect(calls == 1)
    }

    @Test func validation() {
        let scratch = Scratch()
        let s = scratch.settings

        s.textZoom = 10
        #expect(s.textZoom == Settings.textZoomMin)
        s.textZoom = 1000
        #expect(s.textZoom == Settings.textZoomMax)
        // An out-of-range value written from outside reads clamped too.
        scratch.defaults.set(999, forKey: "text-zoom")
        #expect(s.textZoom == Settings.textZoomMax)
        scratch.defaults.set(-5, forKey: "mark-read-delay")
        #expect(s.markReadDelay == 0)

        scratch.defaults.set("neon", forKey: "color-scheme")
        #expect(s.colorScheme == .system)
        s.colorScheme = .dark
        #expect(s.colorScheme == .dark)

        scratch.defaults.set("sardine", forKey: "message-list-density")
        #expect(s.density == .comfortable)
        s.density = .compact
        #expect(s.density == .compact)
        scratch.defaults.set("everywhere", forKey: "search-scope")
        #expect(s.searchScope == .folder)
        s.searchScope = .all
        #expect(s.searchScope == .all)

        scratch.defaults.set("dance", forKey: "command-r")
        #expect(s.commandR == .reply)
        s.commandR = .refresh
        #expect(s.commandR == .refresh)
    }

    @Test func handlerMayRemoveItself() {
        let s = Scratch().settings
        var calls = 0
        var token: Settings.ChangeToken?
        token = s.onChange(.showAvatars) {
            calls += 1
            token?.cancel()
        }
        s.showAvatars = false
        s.showAvatars = true
        #expect(calls == 1)
    }

    @Test func handlersFireInRegistrationOrderAndPerKey() {
        let s = Scratch().settings
        var order: [String] = []
        _ = s.onChange(.confirmDelete) { order.append("a") }
        _ = s.onChange(.confirmDelete) { order.append("b") }
        _ = s.onChange(.notificationSound) { order.append("other") }
        s.confirmDelete = false
        #expect(order == ["a", "b"])
    }

    @Test func favouriteFoldersRoundTrip() {
        let s = Scratch().settings
        #expect(s.favouriteFolders.isEmpty)
        var changed = 0
        _ = s.onChange(.favouriteFolders) { changed += 1 }
        s.favouriteFolders = ["acc_1/f_1"]
        #expect(s.favouriteFolders == ["acc_1/f_1"])
        #expect(changed == 1)
    }

    @Test func stringListRoundTrip() {
        let s = Scratch().settings
        #expect(s.collapsedFolders.isEmpty)
        var changed = 0
        _ = s.onChange(.collapsedFolders) { changed += 1 }

        s.collapsedFolders = ["acc_1/f_1", "acc_1/f_2"]
        var got = s.collapsedFolders
        #expect(got == ["acc_1/f_1", "acc_1/f_2"])
        #expect(changed == 1)

        // Writing the same contents is not a change.
        s.collapsedFolders = ["acc_1/f_1", "acc_1/f_2"]
        #expect(changed == 1)

        // The store hands out copies.
        got[0] = "tampered"
        #expect(s.collapsedFolders[0] == "acc_1/f_1")

        s.collapsedFolders = []
        #expect(s.collapsedFolders.isEmpty)
        #expect(changed == 2)

        s.collapsedAccounts = ["acc_2"]
        #expect(s.collapsedAccounts == ["acc_2"])
        #expect(changed == 2)
    }

    @Test func externalWriteThroughAnotherInstanceNotifies() async throws {
        let scratch = Scratch()
        let s = scratch.settings
        var seen = 0
        _ = s.onChange(.markReadDelay) { seen += 1 }
        // Another UserDefaults object on the same domain, as another window
        // or process would use.
        let other = UserDefaults(suiteName: scratch.suite)!
        other.set(7, forKey: "mark-read-delay")
        let deadline = ContinuousClock.now + .seconds(3)
        while seen == 0, ContinuousClock.now < deadline {
            try await Task.sleep(for: .milliseconds(20))
        }
        #expect(seen == 1)
        #expect(s.markReadDelay == 7)
    }

    @Test func valuesPersistAcrossInstances() {
        let scratch = Scratch()
        scratch.settings.textZoom = 140
        scratch.settings.favouriteFolders = ["a/b"]
        let again = Settings(defaults: UserDefaults(suiteName: scratch.suite)!)
        #expect(again.textZoom == 140)
        #expect(again.favouriteFolders == ["a/b"])
    }
}
