module github.com/schotek/malachi/ui

go 1.25.0

// The UI depends on the backend module only for pkg/api (the contract).
// Local path replace keeps both modules in one repository without a go.work
// requirement for offline (Flatpak) builds.
replace github.com/schotek/malachi/backend => ../backend

require (
	github.com/diamondburned/gotk4-adwaita/pkg v0.0.0-20250703085337-e94555b846b6
	github.com/diamondburned/gotk4/pkg v0.3.2-0.20250703063411-16654385f59a
	github.com/schotek/malachi/backend v0.0.0
)

require (
	github.com/KarpelesLab/weak v0.1.1 // indirect
	github.com/diamondburned/gotk4-webkitgtk/pkg v0.0.0-20240108031600-dee1973cf440 // indirect
	go4.org/unsafe/assume-no-moving-gc v0.0.0-20231121144256-b99613f794b6 // indirect
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c // indirect
)
