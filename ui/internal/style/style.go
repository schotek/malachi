// Package style applies application-wide appearance settings: the libadwaita
// color scheme and a CSS provider for everything that cannot be expressed
// per widget (message body zoom and font, list decorations).
//
// Because the provider is installed on the display, reloading it restyles
// every open window at once; windows need no subscriptions of their own.
package style

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/settings"
)

// Apply installs the color scheme and CSS from s and keeps them in sync with
// later changes. Call it once from the application's startup handler, after
// GTK and libadwaita are initialised.
func Apply(s *settings.Store) {
	sm := adw.StyleManagerGetDefault()
	applyScheme := func() { sm.SetColorScheme(adwColorScheme(s.ColorScheme())) }
	applyScheme()
	s.OnChanged(settings.KeyColorScheme, applyScheme)

	provider := gtk.NewCSSProvider()
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), provider,
		uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
	reload := func() {
		provider.LoadFromString(CSS(s.TextZoom(), s.MonospacePlainText(), s.MonochromeAvatars()))
	}
	reload()
	for _, key := range []string{settings.KeyTextZoom, settings.KeyMonospacePlainText, settings.KeyMonochromeAvatars} {
		s.OnChanged(key, reload)
	}
}

func adwColorScheme(c settings.ColorScheme) adw.ColorScheme {
	switch c {
	case settings.ColorSchemeLight:
		return adw.ColorSchemeForceLight
	case settings.ColorSchemeDark:
		return adw.ColorSchemeForceDark
	default:
		return adw.ColorSchemeDefault
	}
}

// CSS renders the application stylesheet for the given settings. Percent
// font sizes are relative to the parent, so zoom composes with the user's
// system font. The provider has application priority, so its rules win over
// the libadwaita stylesheet (e.g. the per-sender avatar colour classes).
func CSS(textZoomPercent int, monospace, monochromeAvatars bool) string {
	var b strings.Builder
	b.WriteString(".unread-dot { min-width: 8px; min-height: 8px; border-radius: 4px; background-color: @accent_bg_color; }\n")
	fmt.Fprintf(&b, ".message-body { font-size: %d%%; }\n", textZoomPercent)
	if monospace {
		b.WriteString(".message-body { font-family: monospace; }\n")
	}
	if monochromeAvatars {
		// Neutral tint of the foreground colour: readable in light and dark.
		b.WriteString("avatar { background-image: none; background-color: alpha(@window_fg_color, 0.12); color: alpha(@window_fg_color, 0.8); }\n")
	}
	return b.String()
}
