// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/accountservices"
	"github.com/schotek/malachi/ui/internal/contactsctl"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The Contacts window's offers to sync the contacts of an account that has
// them off (docs/plans/account-services.md §3.3; macos
// ContactsSyncOfferBanner), under the consent banners: the sentence with
// Not Now and Turn On. An Adw.Banner holds one button only, so the offer
// is a bar of its own in the banners' box.

// wireSyncOffers gives the window's controller the key
// account-services-dismissed. The samples' accounts are invented: their
// Not Now stays in the window.
func (cw *ContactsWindow) wireSyncOffers() {
	st := cw.main.settings
	if st == nil || contactsctl.SamplesOn() {
		return
	}
	cw.ctl.Dismissed = st.AccountServicesDismissed
	cw.ctl.SetDismissed = st.SetAccountServicesDismissed
}

// appendSyncOffers adds a bar per offer to the banners' box.
func (cw *ContactsWindow) appendSyncOffers(offers []contactsctl.SyncOffer) {
	tr := i18n.Tr
	for _, o := range offers {
		id := o.AccountID
		bar := gtk.NewBox(gtk.OrientationHorizontal, 6)
		bar.AddCSSClass("contacts-sync-offer")
		bar.AddCSSClass("toolbar")
		bar.SetMarginStart(6)
		bar.SetMarginEnd(6)
		icon := gtk.NewImageFromIconName("x-office-address-book-symbolic")
		icon.AddCSSClass("dim-label")
		bar.Append(icon)
		label := gtk.NewLabel(o.Text)
		label.SetUseMarkup(false)
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetWrap(true)
		label.SetWrapMode(pango.WrapWordChar)
		bar.Append(label)
		later := gtk.NewButtonWithLabel(accountservices.NotNow(tr))
		later.SetVAlign(gtk.AlignCenter)
		later.ConnectClicked(func() { cw.ctl.DismissSyncOffer(id) })
		bar.Append(later)
		on := gtk.NewButtonWithLabel(accountservices.TurnOn(tr))
		on.SetVAlign(gtk.AlignCenter)
		on.AddCSSClass("suggested-action")
		on.ConnectClicked(func() { cw.ctl.TurnOnSync(id) })
		bar.Append(on)
		cw.banners.Append(bar)
		cw.banners.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	}
}
