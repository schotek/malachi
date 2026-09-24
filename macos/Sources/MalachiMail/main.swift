// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Malachi Mail for macOS. See macos/README.md.

import AppKit

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
// A no-op inside the bundle. Under `swift run` there is no Info.plist, the
// process counts as a background one, gets no Dock icon and could not show
// a window without this.
app.setActivationPolicy(.regular)
app.run()
