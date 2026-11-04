// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import "github.com/diamondburned/gotk4-adwaita/pkg/adw"

// Avatar is the row's sender avatar: the window shows a contact's photo in
// it (Adw.Avatar custom-image) where the sender is a contact with one
// (docs/plans/address-book.md §9, F10); its initials stay the fallback.
func (r *MessageRow) Avatar() *adw.Avatar { return r.avatar }
