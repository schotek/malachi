// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// UI-only preferences over `UserDefaults`, the counterpart of
/// ui/internal/settings (GSettings). The keys and defaults are those of
/// data/io.github.schotek.Malachi.gschema.xml; `command-r` and
/// `ui-text-size` are macOS-only.
/// Only presentation options belong here; anything that affects mail
/// handling is the daemon's and goes through config.get/config.set.
///
/// Change handlers fire for every change of a key, from this process or
/// another one (`defaults write`), on the main actor; a change made through
/// this class reaches its handlers before the setter returns.
@MainActor
public final class Settings {
    public enum Key: String, CaseIterable, Sendable {
        case launchAtLogin = "launch-at-login"
        case runInBackground = "run-in-background"
        case markReadDelay = "mark-read-delay"
        case confirmDelete = "confirm-delete"
        case desktopNotifications = "desktop-notifications"
        case notificationSound = "notification-sound"
        case colorScheme = "color-scheme"
        case density = "message-list-density"
        case showPreviewLine = "show-preview-line"
        case groupByConversation = "group-by-conversation"
        case searchScope = "search-scope"
        case showAvatars = "show-avatars"
        case monochromeAvatars = "monochrome-avatars"
        case monospacePlainText = "monospace-plain-text"
        case textZoom = "text-zoom"
        case collapsedFolders = "collapsed-folders"
        case collapsedAccounts = "collapsed-accounts"
        case favouriteFolders = "favourite-folders"
        /// The Assistant menu (ui/internal/assistant): shown at all, and
        /// the Claude app it opens (Settings → AI → Assistant).
        case assistantMenu = "assistant-menu"
        case assistantTarget = "assistant-target"
        /// The assistant panel (the In App target): the model, where
        /// Claude Code is (empty: the usual places), and whether the user
        /// allowed mail to go to Claude.
        case assistantModel = "assistant-model"
        case assistantClaudePath = "assistant-claude-path"
        case assistantConsent = "assistant-consent"
        // Provider selection and independent, versioned OpenAI disclosures.
        case assistantProvider = "assistant-provider"
        case assistantCodexPath = "assistant-codex-path"
        case assistantChatGPTModel = "assistant-chatgpt-model"
        case assistantChatGPTConsentVersion = "assistant-chatgpt-consent-version"
        /// macOS only for now (the board is Swift-first): whether the user
        /// allowed the board's triage to send mail of the board's cases to
        /// Claude, on top of `assistantConsent` (`BoardTriageController`).
        case boardTriageChatGPTModel = "board-triage-chatgpt-model"
        case boardTriageChatGPTConsentVersion = "board-triage-chatgpt-consent-version"
        case boardTriageConsent = "board-triage-consent"
        /// macOS only for now: the model of the board's triage runs, the
        /// nicks of `assistantModel` but set apart from it (Settings → AI →
        /// Board).
        case boardTriageModel = "board-triage-model"
        /// macOS only for now: the style the board opens in the first time
        /// it shows in a run, `Board.Style`'s nicks (Settings → General →
        /// Board; `BoardController.boardWillShow`).
        case boardDefaultStyle = "board-default-style"
        /// macOS only: what ⌘R does (Settings → General → Keyboard).
        case commandR = "command-r"
        /// macOS only: the size of the window text (`Typo`, Settings →
        /// Appearance → Theme), read once at launch.
        case uiTextSize = "ui-text-size"
    }

    public enum ColorScheme: String, Sendable, CaseIterable {
        case system, light, dark
    }

    public enum Density: String, Sendable, CaseIterable {
        case comfortable, compact
    }

    /// Where a search looks: the selected folder, its account, or every
    /// account (the gschema's SearchScope).
    public enum SearchScope: String, Sendable, CaseIterable {
        case folder, account, all
    }

    /// `reply` follows Mail.app (⌘R replies, ⇧⌘N checks for mail);
    /// `refresh` follows the GTK UI (⌘R checks for mail, ⌥⌘R replies).
    public enum CommandR: String, Sendable, CaseIterable {
        case reply, refresh
    }

    /// `standard` is the original port of libadwaita's sizes (13 pt body,
    /// 10 pt captions); `larger` raises the small text toward what GTK draws
    /// (its points are at 96 DPI) and what other Mac mail clients use.
    public enum TextSize: String, Sendable, CaseIterable {
        case standard, larger
    }

    /// Bounds of the numeric keys; must match the gschema's `<range>`.
    public static let markReadDelayMax = 60
    public static let textZoomMin = 50
    public static let textZoomMax = 200
    public static let textZoomStep = 10

    /// The gschema defaults, for `UserDefaults.register(defaults:)`.
    public static func registrationDefaults() -> [String: Any] {
        [
            Key.launchAtLogin.rawValue: false,
            Key.runInBackground.rawValue: false,
            Key.markReadDelay.rawValue: 2,
            Key.confirmDelete.rawValue: true,
            Key.desktopNotifications.rawValue: true,
            Key.notificationSound.rawValue: false,
            Key.colorScheme.rawValue: ColorScheme.system.rawValue,
            Key.density.rawValue: Density.comfortable.rawValue,
            Key.showPreviewLine.rawValue: true,
            Key.groupByConversation.rawValue: false,
            Key.searchScope.rawValue: SearchScope.folder.rawValue,
            Key.showAvatars.rawValue: true,
            Key.monochromeAvatars.rawValue: false,
            Key.monospacePlainText.rawValue: false,
            Key.textZoom.rawValue: 100,
            Key.collapsedFolders.rawValue: [String](),
            Key.collapsedAccounts.rawValue: [String](),
            Key.favouriteFolders.rawValue: [String](),
            Key.assistantMenu.rawValue: true,
            Key.assistantTarget.rawValue: Assistant.Target.desktop.rawValue,
            Key.assistantModel.rawValue: Assistant.Model.sonnet.rawValue,
            Key.assistantClaudePath.rawValue: "",
            Key.assistantConsent.rawValue: false,
            Key.assistantProvider.rawValue: "claude",
            Key.boardTriageChatGPTModel.rawValue: "",
            Key.boardTriageChatGPTConsentVersion.rawValue: 0,
            Key.assistantCodexPath.rawValue: "",
            Key.assistantChatGPTModel.rawValue: "",
            Key.assistantChatGPTConsentVersion.rawValue: 0,
            Key.boardTriageConsent.rawValue: false,
            Key.boardTriageModel.rawValue: Assistant.Model.sonnet.rawValue,
            Key.boardDefaultStyle.rawValue: Board.Style.list.nick,
            Key.commandR.rawValue: CommandR.reply.rawValue,
            Key.uiTextSize.rawValue: TextSize.larger.rawValue,
        ]
    }

    /// Registers the gschema defaults with `defaults`.
    public static func register(defaults: UserDefaults) {
        defaults.register(defaults: registrationDefaults())
    }

    /// Removes a handler installed by `onChange`. Dropping the token does
    /// not remove the handler; call `cancel()`.
    @MainActor
    public final class ChangeToken {
        private weak var hub: ChangeHub?
        private let key: Key
        private let id: Int

        fileprivate init(hub: ChangeHub, key: Key, id: Int) {
            self.hub = hub
            self.key = key
            self.id = id
        }

        public func cancel() {
            hub?.remove(key, id)
        }
    }

    public let defaults: UserDefaults
    private let hub: ChangeHub
    private let observer: DefaultsObserver

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        Settings.register(defaults: defaults)
        let hub = ChangeHub()
        self.hub = hub
        observer = DefaultsObserver(defaults: defaults, keys: Key.allCases.map(\.rawValue)) { keyPath in
            guard let key = Key(rawValue: keyPath) else { return }
            if Thread.isMainThread {
                MainActor.assumeIsolated { hub.fire(key) }
            } else {
                Task { @MainActor in hub.fire(key) }
            }
        }
    }

    // MARK: General

    /// Mirrors the login item's state; `SMAppService`, not this key, is
    /// authoritative.
    public var launchAtLogin: Bool {
        get { bool(.launchAtLogin) }
        set { set(.launchAtLogin, newValue) }
    }

    public var runInBackground: Bool {
        get { bool(.runInBackground) }
        set { set(.runInBackground, newValue) }
    }

    /// Seconds a message must be shown before it is marked read; 0 = at once.
    public var markReadDelay: Int {
        get { min(max(integer(.markReadDelay), 0), Settings.markReadDelayMax) }
        set { set(.markReadDelay, min(max(newValue, 0), Settings.markReadDelayMax)) }
    }

    public var confirmDelete: Bool {
        get { bool(.confirmDelete) }
        set { set(.confirmDelete, newValue) }
    }

    public var desktopNotifications: Bool {
        get { bool(.desktopNotifications) }
        set { set(.desktopNotifications, newValue) }
    }

    public var notificationSound: Bool {
        get { bool(.notificationSound) }
        set { set(.notificationSound, newValue) }
    }

    public var commandR: CommandR {
        get { CommandR(rawValue: string(.commandR)) ?? .reply }
        set { set(.commandR, newValue.rawValue) }
    }

    // MARK: Appearance

    /// The window text size; the app reads it at launch only.
    public var uiTextSize: TextSize {
        get { TextSize(rawValue: string(.uiTextSize)) ?? .larger }
        set { set(.uiTextSize, newValue.rawValue) }
    }

    public var colorScheme: ColorScheme {
        get { ColorScheme(rawValue: string(.colorScheme)) ?? .system }
        set { set(.colorScheme, newValue.rawValue) }
    }

    public var density: Density {
        get { Density(rawValue: string(.density)) ?? .comfortable }
        set { set(.density, newValue.rawValue) }
    }

    public var showPreviewLine: Bool {
        get { bool(.showPreviewLine) }
        set { set(.showPreviewLine, newValue) }
    }

    public var groupByConversation: Bool {
        get { bool(.groupByConversation) }
        set { set(.groupByConversation, newValue) }
    }

    /// The last scope chosen for a search.
    public var searchScope: SearchScope {
        get { SearchScope(rawValue: string(.searchScope)) ?? .folder }
        set { set(.searchScope, newValue.rawValue) }
    }

    public var showAvatars: Bool {
        get { bool(.showAvatars) }
        set { set(.showAvatars, newValue) }
    }

    public var monochromeAvatars: Bool {
        get { bool(.monochromeAvatars) }
        set { set(.monochromeAvatars, newValue) }
    }

    public var monospacePlainText: Bool {
        get { bool(.monospacePlainText) }
        set { set(.monospacePlainText, newValue) }
    }

    /// Message body zoom in percent, clamped to `textZoomMin…textZoomMax`.
    public var textZoom: Int {
        get { min(max(integer(.textZoom), Settings.textZoomMin), Settings.textZoomMax) }
        set { set(.textZoom, min(max(newValue, Settings.textZoomMin), Settings.textZoomMax)) }
    }

    // MARK: Sidebar state

    /// Folded-away nodes of the folder sidebar, one entry per node; the
    /// model owns the encoding and tolerates entries it cannot parse.
    public var collapsedFolders: [String] {
        get { stringList(.collapsedFolders) }
        set { set(.collapsedFolders, newValue) }
    }

    public var collapsedAccounts: [String] {
        get { stringList(.collapsedAccounts) }
        set { set(.collapsedAccounts, newValue) }
    }

    /// Folders pinned to the Favourites section, encoded like `collapsedFolders`.
    public var favouriteFolders: [String] {
        get { stringList(.favouriteFolders) }
        set { set(.favouriteFolders, newValue) }
    }

    // MARK: Assistant

    /// Whether the Assistant menu is shown (the toolbar button, the Message
    /// menu's submenu, the attachment menu's item).
    public var assistantMenu: Bool {
        get { bool(.assistantMenu) }
        set { set(.assistantMenu, newValue) }
    }

    /// Where the Assistant menu opens Claude: the gschema enum
    /// AssistantTarget's nicks, read with `Assistant.parseTarget` (an
    /// unknown nick is Claude Desktop).
    public var assistantTarget: Assistant.Target {
        get { Assistant.parseTarget(string(.assistantTarget)) }
        set { set(.assistantTarget, Assistant.parseTarget(newValue.rawValue).rawValue) }
    }

    /// The model of the assistant panel: the gschema enum AssistantModel's
    /// nicks, read with `Assistant.parseModel` (an unknown nick is Sonnet).
    public var assistantModel: Assistant.Model {
        get { Assistant.parseModel(string(.assistantModel)) }
        set { set(.assistantModel, Assistant.parseModel(newValue.rawValue).rawValue) }
    }

    /// The claude executable the panel runs; "" looks in the usual places
    /// (`ClaudeCodeLocator`).
    public var assistantClaudePath: String {
        get { string(.assistantClaudePath) }
        set { set(.assistantClaudePath, newValue) }
    }

    /// Whether the user allowed the panel to send mail to Claude (asked
    /// before the first question).
    public var assistantConsent: Bool {
        get { bool(.assistantConsent) }
        set { set(.assistantConsent, newValue) }
    }

    /// Whether the user allowed the board's triage to send the board's
    /// mail to Claude (asked before the first triage; withdrawn in
    /// Settings). The board's `assistant` preference follows it.
    public var boardTriageConsent: Bool {
        get { bool(.boardTriageConsent) }
        set { set(.boardTriageConsent, newValue) }
    }

    /// The model of the board's triage runs, apart from the panel's
    /// (`assistantModel`): the same nicks, read with `Assistant.parseModel`
    /// (an unknown nick is Sonnet).
    public var boardTriageModel: Assistant.Model {
        get { Assistant.parseModel(string(.boardTriageModel)) }
        set { set(.boardTriageModel, Assistant.parseModel(newValue.rawValue).rawValue) }
    }

    /// The style the board opens in the first time it shows in a run:
    /// `Board.Style`'s nicks, read with `Board.parseStyle` (an unknown or
    /// empty nick is the List).
    public var boardDefaultStyle: Board.Style {
        get { Board.parseStyle(string(.boardDefaultStyle)) }
        set { set(.boardDefaultStyle, newValue.nick) }
    }

    public var assistantProvider: AssistantProviderID {
        get { AssistantProviderID(rawValue: string(.assistantProvider)) ?? .claude }
        set { set(.assistantProvider, newValue.rawValue) }
    }
    public var assistantCodexPath: String {
        get { string(.assistantCodexPath) }
        set { set(.assistantCodexPath, newValue) }
    }
    public var assistantChatGPTModel: String {
        get { string(.assistantChatGPTModel) }
        set { set(.assistantChatGPTModel, newValue) }
    }
    public var assistantChatGPTConsentVersion: Int {
        get { integer(.assistantChatGPTConsentVersion) }
        set { set(.assistantChatGPTConsentVersion, newValue) }
    }
    public var boardTriageChatGPTModel: String {
        get { string(.boardTriageChatGPTModel) }
        set { set(.boardTriageChatGPTModel, newValue) }
    }
    public var boardTriageChatGPTConsentVersion: Int {
        get { integer(.boardTriageChatGPTConsentVersion) }
        set { set(.boardTriageChatGPTConsentVersion, newValue) }
    }
    public var selectedAssistantConsent: Bool {
        get { assistantProvider == .chatgpt ? assistantChatGPTConsentVersion == 1 : assistantConsent }
        set { if assistantProvider == .chatgpt { assistantChatGPTConsentVersion = newValue ? 1 : 0 } else { assistantConsent = newValue } }
    }
    public var selectedBoardConsent: Bool {
        get { assistantProvider == .chatgpt ? boardTriageChatGPTConsentVersion == 1 : boardTriageConsent }
        set { if assistantProvider == .chatgpt { boardTriageChatGPTConsentVersion = newValue ? 1 : 0 } else { boardTriageConsent = newValue } }
    }

    // MARK: Change notification

    /// Calls `f` whenever `key` changes, from any source. The handler may
    /// cancel its own token.
    public func onChange(_ key: Key, _ f: @escaping @MainActor () -> Void) -> ChangeToken {
        ChangeToken(hub: hub, key: key, id: hub.add(key, f))
    }

    // MARK: Storage

    private func bool(_ key: Key) -> Bool {
        defaults.bool(forKey: key.rawValue)
    }

    private func integer(_ key: Key) -> Int {
        defaults.integer(forKey: key.rawValue)
    }

    private func string(_ key: Key) -> String {
        defaults.string(forKey: key.rawValue) ?? ""
    }

    /// Always a fresh array (a value type), so a caller may keep and
    /// mutate it without touching the store.
    private func stringList(_ key: Key) -> [String] {
        defaults.stringArray(forKey: key.rawValue) ?? []
    }

    /// Stores a value that differs from the current one; an unchanged
    /// write is not a change and fires no handler (as GSettings does).
    private func set(_ key: Key, _ value: Bool) {
        guard value != bool(key) else { return }
        defaults.set(value, forKey: key.rawValue)
    }

    private func set(_ key: Key, _ value: Int) {
        guard value != integer(key) else { return }
        defaults.set(value, forKey: key.rawValue)
    }

    private func set(_ key: Key, _ value: String) {
        guard value != string(key) else { return }
        defaults.set(value, forKey: key.rawValue)
    }

    private func set(_ key: Key, _ value: [String]) {
        guard value != stringList(key) else { return }
        defaults.set(value, forKey: key.rawValue)
    }
}

/// The handler table, separate from `Settings` so the KVO observer can
/// reach it without a reference to the partly initialised settings object.
@MainActor
final class ChangeHub {
    private var handlers: [Settings.Key: [Int: @MainActor () -> Void]] = [:]
    private var nextID = 0

    func add(_ key: Settings.Key, _ f: @escaping @MainActor () -> Void) -> Int {
        let id = nextID
        nextID += 1
        handlers[key, default: [:]][id] = f
        return id
    }

    func remove(_ key: Settings.Key, _ id: Int) {
        handlers[key]?[id] = nil
    }

    func fire(_ key: Settings.Key) {
        // Copy first: a handler may remove itself.
        let fs = (handlers[key] ?? [:]).sorted { $0.key < $1.key }.map(\.value)
        for f in fs {
            f()
        }
    }
}

/// Observes the keys on `UserDefaults` through KVO (hyphenated key names
/// are ordinary KVC keys there) and forwards each change; it removes itself
/// when it goes away with its `Settings`.
private final class DefaultsObserver: NSObject {
    private let defaults: UserDefaults
    private let keys: [String]
    private let fire: @Sendable (String) -> Void

    init(defaults: UserDefaults, keys: [String], fire: @escaping @Sendable (String) -> Void) {
        self.defaults = defaults
        self.keys = keys
        self.fire = fire
        super.init()
        for key in keys {
            defaults.addObserver(self, forKeyPath: key, options: [.new], context: nil)
        }
    }

    deinit {
        for key in keys {
            defaults.removeObserver(self, forKeyPath: key)
        }
    }

    override func observeValue(forKeyPath keyPath: String?, of object: Any?, change: [NSKeyValueChangeKey: Any]?, context: UnsafeMutableRawPointer?) {
        guard let keyPath else { return }
        fire(keyPath)
    }
}
