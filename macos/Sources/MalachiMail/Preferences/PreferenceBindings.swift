// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A two-way binding between one control of the settings window and one
/// `Settings` key, the counterpart of `settings.Store.Bind` and
/// `bindChoice` in ui/internal/window/preferences.go: the control starts
/// from the stored value, a change made by the user is written to the
/// settings, and a change of the setting from anywhere else (another
/// window, `defaults write`) is pushed back into the control. A push never
/// re-enters the write path.
///
/// The pane keeps the bindings it makes and cancels them when its window
/// closes, as the GTK dialog undoes its bindings on close.
@MainActor
final class PreferenceBinding: NSObject {
    private let settings: Settings
    private var token: Settings.ChangeToken?
    /// The control is being updated from the settings: its action, should
    /// the control fire one, must not write back.
    private var syncing = false
    /// Reads the setting into the control.
    private var pull: (@MainActor () -> Void)?
    /// Writes the control into the setting.
    private var push: (@MainActor () -> Void)?

    private init(settings: Settings) {
        self.settings = settings
        super.init()
    }

    /// Stops both directions. Dropping the binding does not.
    func cancel() {
        token?.cancel()
        token = nil
        pull = nil
        push = nil
    }

    // MARK: Factories

    /// An `NSSwitch` and a Bool key (`Store.Bind(key, row, "active")`).
    static func bind(_ control: NSSwitch, to settings: Settings, _ key: Settings.Key, _ path: ReferenceWritableKeyPath<Settings, Bool>) -> PreferenceBinding {
        let b = PreferenceBinding(settings: settings)
        b.pull = { [weak control] in
            guard let control else { return }
            let want: NSControl.StateValue = settings[keyPath: path] ? .on : .off
            if control.state != want {
                control.state = want
            }
        }
        b.push = { [weak control] in
            guard let control else { return }
            settings[keyPath: path] = control.state == .on
        }
        b.attach(control, key)
        return b
    }

    /// A spin control and an Int key (`Store.Bind(key, row, "value")`);
    /// the settings clamp the value, and the clamped value comes back.
    static func bind(_ control: PrefsSpinControl, to settings: Settings, _ key: Settings.Key, _ path: ReferenceWritableKeyPath<Settings, Int>) -> PreferenceBinding {
        let b = PreferenceBinding(settings: settings)
        b.pull = { [weak control] in
            guard let control else { return }
            let want = settings[keyPath: path]
            if control.value != want {
                control.value = want
            }
        }
        control.onChange = { [weak b] value in
            guard let b, !b.syncing else { return }
            settings[keyPath: path] = value
        }
        b.token = settings.onChange(key) { [weak b] in b?.sync() }
        b.sync()
        return b
    }

    /// An `NSPopUpButton` and an enum key, the pop-up's items in the order
    /// of `choices` (`bindChoice`): a stored value outside the table
    /// selects the first item; a position outside the table writes nothing.
    static func bind<T: Equatable>(
        _ control: NSPopUpButton, to settings: Settings, _ key: Settings.Key,
        choices: [T], _ path: ReferenceWritableKeyPath<Settings, T>
    ) -> PreferenceBinding {
        let b = PreferenceBinding(settings: settings)
        b.pull = { [weak control] in
            guard let control else { return }
            let want = choices.firstIndex(of: settings[keyPath: path]) ?? 0
            if control.indexOfSelectedItem != want {
                control.selectItem(at: want)
            }
        }
        b.push = { [weak control] in
            guard let control else { return }
            let i = control.indexOfSelectedItem
            guard choices.indices.contains(i) else { return }
            settings[keyPath: path] = choices[i]
        }
        b.attach(control, key)
        return b
    }

    // MARK: Internals

    /// Wires a target/action control: the settings drive the control now
    /// and on every change; the action writes back.
    private func attach(_ control: NSControl, _ key: Settings.Key) {
        control.target = self
        control.action = #selector(controlChanged(_:))
        token = settings.onChange(key) { [weak self] in self?.sync() }
        sync()
    }

    private func sync() {
        syncing = true
        pull?()
        syncing = false
    }

    @objc private func controlChanged(_ sender: Any?) {
        guard !syncing else { return }
        push?()
    }
}

/// The bindings of one settings pane and when they end: the pane's window
/// closing (the GTK dialog's `closed` signal undoes every binding). The
/// pane adds its bindings, points the set at its window once the view is
/// in one, and gets `onClose` when the window goes; `close()` does the
/// same on request. Late RPC replies check `closed`.
@MainActor
final class PreferenceBindingSet: NSObject {
    private var bindings: [PreferenceBinding] = []
    private weak var window: NSWindow?
    private(set) var closed = false
    /// Called once, after the bindings were cancelled.
    var onClose: (@MainActor () -> Void)?

    func add(_ binding: PreferenceBinding) {
        guard !closed else {
            binding.cancel()
            return
        }
        bindings.append(binding)
    }

    /// Ends the bindings when `window` closes. Calling it again with the
    /// same window does nothing.
    func watch(_ window: NSWindow?) {
        guard let window, window !== self.window, !closed else { return }
        if let old = self.window {
            NotificationCenter.default.removeObserver(self, name: NSWindow.willCloseNotification, object: old)
        }
        self.window = window
        NotificationCenter.default.addObserver(
            self, selector: #selector(windowWillClose(_:)), name: NSWindow.willCloseNotification, object: window)
    }

    /// Cancels every binding and reports the close, once.
    func close() {
        guard !closed else { return }
        closed = true
        if let window {
            NotificationCenter.default.removeObserver(self, name: NSWindow.willCloseNotification, object: window)
            self.window = nil
        }
        for b in bindings {
            b.cancel()
        }
        bindings = []
        onClose?()
        onClose = nil
    }

    @objc private func windowWillClose(_ notification: Foundation.Notification) {
        close()
    }
}
