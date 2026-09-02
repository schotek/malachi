package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/settings"
)

// PreferencesDialog is the application preferences dialog, built from
// data/ui/preferences.blp. The Appearance page is bound to the settings
// store; the General page rows are still placeholders.
type PreferencesDialog struct {
	*adw.PreferencesDialog

	colorScheme *adw.ComboRow
	density     *adw.ComboRow
	showPreview *adw.SwitchRow
	showAvatars *adw.SwitchRow
	monochrome  *adw.SwitchRow
	monospace   *adw.SwitchRow
	textZoom    *adw.SpinRow
}

// Combo row entries, in the order of the StringLists in preferences.blp.
var (
	colorSchemeChoices = []settings.ColorScheme{
		settings.ColorSchemeSystem, settings.ColorSchemeLight, settings.ColorSchemeDark,
	}
	densityChoices = []settings.Density{settings.DensityComfortable, settings.DensityCompact}
)

// NewPreferences builds the dialog bound to s. Present it with Present(parent).
func NewPreferences(s *settings.Store) *PreferencesDialog {
	b := gtk.NewBuilderFromString(data.MustUI("preferences.ui"))

	d := &PreferencesDialog{
		PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog),
		colorScheme:       b.GetObject("color_scheme").Cast().(*adw.ComboRow),
		density:           b.GetObject("list_density").Cast().(*adw.ComboRow),
		showPreview:       b.GetObject("show_preview_line").Cast().(*adw.SwitchRow),
		showAvatars:       b.GetObject("show_avatars").Cast().(*adw.SwitchRow),
		monochrome:        b.GetObject("monochrome_avatars").Cast().(*adw.SwitchRow),
		monospace:         b.GetObject("monospace_plain_text").Cast().(*adw.SwitchRow),
		textZoom:          b.GetObject("text_zoom").Cast().(*adw.SpinRow),
	}

	// The dialog is rebuilt on every open while the store lives for the whole
	// process, so every binding is undone when the dialog closes.
	cleanup := []func(){
		s.Bind(settings.KeyShowPreviewLine, d.showPreview.Object, "active"),
		s.Bind(settings.KeyShowAvatars, d.showAvatars.Object, "active"),
		s.Bind(settings.KeyMonochromeAvatars, d.monochrome.Object, "active"),
		s.Bind(settings.KeyMonospacePlainText, d.monospace.Object, "active"),
		s.Bind(settings.KeyTextZoom, d.textZoom.Object, "value"),
		bindChoice(s, settings.KeyColorScheme, d.colorScheme, colorSchemeChoices, s.ColorScheme, s.SetColorScheme),
		bindChoice(s, settings.KeyDensity, d.density, densityChoices, s.Density, s.SetDensity),
	}
	d.ConnectClosed(func() {
		for _, f := range cleanup {
			f()
		}
	})
	return d
}

// bindChoice keeps a combo row and a string-enum setting in sync in both
// directions (GSettings cannot bind an enum key to a position property).
func bindChoice[T ~string](s *settings.Store, key string, row *adw.ComboRow, choices []T, get func() T, set func(T)) (unbind func()) {
	indexOf := func(v T) uint {
		for i, c := range choices {
			if c == v {
				return uint(i)
			}
		}
		return 0
	}
	row.SetSelected(indexOf(get()))
	handle := row.NotifyProperty("selected", func() {
		if i := row.Selected(); i < uint(len(choices)) {
			set(choices[i])
		}
	})
	remove := s.OnChanged(key, func() {
		if want := indexOf(get()); row.Selected() != want {
			row.SetSelected(want)
		}
	})
	return func() {
		remove()
		row.HandlerDisconnect(handle)
	}
}
