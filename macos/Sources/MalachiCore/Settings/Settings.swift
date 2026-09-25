// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// UI-only preferences over `UserDefaults`, the counterpart of
/// ui/internal/settings (GSettings). The keys and defaults are those of
/// data/io.github.schotek.Malachi.gschema.xml; `command-r` is macOS-only.
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
        case showAvatars = "show-avatars"
        case monochromeAvatars = "monochrome-avatars"
        case monospacePlainText = "monospace-plain-text"
        case textZoom = "text-zoom"
        case collapsedFolders = "collapsed-folders"
        case collapsedAccounts = "collapsed-accounts"
        case favouriteFolders = "favourite-folders"
        /// macOS only: what ⌘R does (Settings → General → Keyboard).
        case commandR = "command-r"
    }

    public enum ColorScheme: String, Sendable, CaseIterable {
        case system, light, dark
    }

    public enum Density: String, Sendable, CaseIterable {
        case comfortable, compact
    }

    /// `reply` follows Mail.app (⌘R replies, ⇧⌘N checks for mail);
    /// `refresh` follows the GTK UI (⌘R checks for mail, ⌥⌘R replies).
    public enum CommandR: String, Sendable, CaseIterable {
        case reply, refresh
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
            Key.showAvatars.rawValue: true,
            Key.monochromeAvatars.rawValue: false,
            Key.monospacePlainText.rawValue: false,
            Key.textZoom.rawValue: 100,
            Key.collapsedFolders.rawValue: [String](),
            Key.collapsedAccounts.rawValue: [String](),
            Key.favouriteFolders.rawValue: [String](),
            Key.commandR.rawValue: CommandR.reply.rawValue,
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
