// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

extension Integration {
    /// The update check's banner across the top of the main window, in
    /// every mode (Mail, Board, Calendar): it follows the application's
    /// `UpdateController`, and its buttons act through `AppState`.
    func wireUpdates() {
        guard let mainWindow else { return }
        let banner = UpdateBannerView()
        let content = mainWindow.content
        content.install(banner: banner)
        banner.onShownChange = { [weak content] shown in
            content?.showBanner(shown)
        }
        banner.onAction = { [weak self, weak mainWindow] action, b in
            self?.state.performUpdate(action, banner: b, from: mainWindow?.window)
        }
        banner.bind(state.updates)
    }
}
