# Linux (GTK) screenshots — hand-over notes

Taken 2026-10-28 from branch `shots/linux` (based on `main` at
`49f0a376`). The code commits come first on the branch; this commit
holds only these pictures and notes. The sets follow docs/screenshots.md
§2 in English (`en/`) and Czech (`cs/`): 11 PNGs in each, lossless and
unscaled.

## System

- **Not the Fedora 42 container.** The machine was a Fedora Linux 44
  (Server Edition) aarch64 VM under Parallels, kernel
  7.2.11-200.fc44.aarch64. It had no container engine (no Apple
  `container`, podman or docker) and no sudo. The owner said to build on
  the host, since a newer Fedora is fine.
- Host packages: GTK 4.22.5, libadwaita 1.9.4, GLib 2.88.4,
  WebKitGTK 6.0 2.54.2, librsvg2 2.62.4, adwaita-icon-theme 50.0,
  adwaita-sans-fonts 50.0, Mesa 26.2.5, Go 1.26.10, blueprint-compiler
  0.20.4 and gettext 0.26. gotk4 and gotk4-adwaita are the pinned
  snapshots from `ui/go.mod`, unchanged.
- The client was built on the host with
  `CC="ccache gcc" make BUILD_DIR=build/linux build`. gsound was found,
  so the build had no `nosound` tag.
- Some X tools and packages were missing and nothing could be installed
  system-wide. They were downloaded with `dnf download` from the Fedora
  44 repositories and unpacked with `rpm2cpio` into `~/.local/opt/xtools`
  (on `PATH` and `LD_LIBRARY_PATH`):
  - `xorg-x11-server-Xvfb` 21.1.25 and `xwd` 1.0.9
  - `xwininfo` 1.1.6 and `xdotool` with `libxdo`
  - `dbus-daemon` 1.16.2
  - `glibc-langpack-cs` 2.43. The Czech locale came through `LOCPATH`,
    a folder holding `cs_CZ.utf8` and the host's `C.utf8` and
    `en_GB.utf8`.

## Display, scale, size

- Xvfb `:99`, `-screen 0 2880x1800x24`, with no window manager and no
  compositor. The client ran with `GDK_BACKEND=x11`, `GDK_SCALE=2` and
  `GSK_RENDERER=cairo` (the renderer `gtk.sh x11` uses).
- The main window is 1440×900 logical, which is **2880×1800 px**: it
  sits at 0,0 and fills the screen. GTK draws its own frame, with no
  shadow.
- The compose window is a toplevel of its own at its default size,
  760×640 logical (**1520×1280 px**), and was cropped to itself.
- The pointer was moved to the screen's last pixel (2879,1799) before
  the runs. A software cursor never shows in an X `GetImage`.
- Light appearance and the default accent. `gtk-4.0/settings.ini` was
  the block `gtk.sh x11` writes: Adwaita Sans 11, the Adwaita icon and
  cursor themes, and GNOME's font rendering.

## How the client was driven

`scripts/screenshots/gtk.sh en|cs OUTDIR` (this branch) ran over
`scripts/screenshots/stack.sh start en|cs`, which started a fresh world
for each language. It runs the client twice with `MALACHI_GTK_START`
(the steps added in `ui/internal/window/devstart.go` on this branch) and
`MALACHI_GTK_START_INTERVAL=4`:

```
group=on;chat=off;folder=inbox;row=DevConf;shot=…/inbox.png;row=Quotation;chat=on;shot=…/chat.png;
chat=off;row=Q4 planning;shot=…/invitation.png;row=Design Weekly;shot=…/newsletter.png;
folder=Web;row=WEB-1;shot=…/jira.png;board:list:select;shot=…/board-list.png;
board:columns:select;shot=…/board-columns.png;calendar:week;shot=…/calendar-week.png;
mail;folder=inbox;row=DevConf;compose-open;shot=…/compose.png;quit

group=off;chat=off;folder=sent;row=Quotation;shot=…/crm-saved.png;
folder=inbox;row=Request for a quote;crm-save;shot=…/crm-dialog.png;quit
```

- These are the two runs of §3, with two changes: `folder=inbox` at the
  start of the first run, and `board:columns:select` in place of
  `board:columns`. Without `:select`, the Columns style once came up with
  the first case selected instead of the quotation thread, a matter of
  timing; `:select` picks the same case again by the same rule. In Czech the three subjects were `Nabídka`,
  `Porada` and `Poptávka`.
- Each `shot=` prints `MALACHI_GTK_START shot <path>`. The script waits
  1 s, then `scripts/screenshots/xwd2png.py` takes `xwd -root`, crops it
  to the topmost viewable toplevel (from `xwininfo`) and writes the PNG.
  The windows have a 32-bit visual, which is why the shot comes from the
  root window.
- `board:list:select` asks for every case's conversation and waits until
  all 17 have loaded. It then selects the case with the most text (the
  quotation thread). The next step waits for this and then for one more
  full interval.

Client environment (`env -i`, so nothing of the desktop session leaked
in):

- `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME` and
  `XDG_RUNTIME_DIR` all under the throwaway `/tmp/malachi-shots/gtk`.
- A session bus of its own (`dbus-daemon --session`, socket in that
  runtime directory).
- `GSETTINGS_SCHEMA_DIR=build/linux/glib-2.0/schemas` and
  `GSETTINGS_BACKEND=keyfile`, with a keyfile that sets
  `window-width=1440` and `window-height=900`.
- `MALACHI_LOCALE_DIR=build/linux/locale`
- `MALACHI_DAEMON=none` and `MALACHI_SOCKET=/tmp/malachi-shots/rpc.sock`
- `MALACHI_CALENDAR_SAMPLES=1` and `MALACHI_LOG_LEVEL=info`
- `TZ=Europe/Prague`, and `LANG`/`LC_ALL` set to `en_GB.UTF-8` or
  `cs_CZ.UTF-8`.
- Not `WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS`: bwrap works on this
  host.

The real store, the real preferences and the real XDG directories were
never touched.

## Checked

I opened all 22 pictures:

- All text is drawn: no tofu and no `image-missing` icons.
- Every reading pane shows its body, including the WebKit HTML bodies
  (the DevConf table, the Jira comments and the invitation).
- No pointer, toast, notification or other window, and no "backend
  unavailable" or sync banner. The footer reads "Up to date" /
  "Aktuální".
- The Inbox shows 17 messages in both languages ("… of 17" / "… z 17").
- In the sidebar: the mail account with its folders and the Jira
  account with its views and spaces. As §2 says, the CRM account is not
  in Mail; it shows in the CRM bar and dialog.
- The language is right in each set. Dates are 28/10/2026 in en and
  28. 10. 2026 in cs.

## Differences from §2, and what could not be reproduced

1. **The sidebar's header title reads "Fold…" in English.** This is how
   the client lays out its header at a 1440 px window: the mode switch,
   New Message and the menu leave too little room for "Folders". The
   Czech "Složky" fits. I did not change the client for the picture.
2. **`crm-dialog.png`**: the search field has the keyboard focus and
   shows a text caret (not the mouse pointer), as the dialog opens.
3. **`compose.png`**: the To field has the focus and shows a text caret.
4. **`calendar-week.png`**: the built-in calendar samples
   (`MALACHI_CALENDAR_SAMPLES`) have English event and calendar names in
   both languages, because the samples are not translated. Everything
   around them is Czech in the Czech set.
5. **The unread count in the Inbox title changes from scene to scene**
   (for example 5, 4, 3, 2 of 17). Each scene marks the message it
   selects as read, as a click would.
6. **The selected case in `board-list.png`** is in *Waiting for Them*,
   below the visible part of the list. The list is not scrolled to it,
   which is the client's own behaviour after a programmatic selection.
   The detail shows the case and its four messages.

## Found and fixed on the way

- The chat did not show its end when it opened.
  - The client scrolls to the end from the adjustment's "changed",
    which GtkViewport emits during its own allocation. The allocation
    that the new value queues is dropped there, so the rows stay drawn
    at the old position until something else lays them out.
  - A pointer or caret on a real display hides this; under Xvfb it
    stays. Tracing showed the adjustment at its end while the picture
    stayed at the top.
  - Fixed in `fix(ui): the chat shows its end when it opens`: both
    programmatic scrolls of the chat now lay the viewport out again from
    idle.
  - I could not reproduce it in `TestConversationChatGTKSmoke` (it
    passes without the fix), so the fix has no test of its own. It was
    verified on the pictures.
- The board's conversation block came up empty for a case selected before
  its conversation was in the cache. GtkStack leaves a page that is
  hidden during loading and does not return to it. This is fixed in
  `fix(ui): the board's conversation shows its cards after loading`,
  with a check in `TestBoardGTKSmoke`.

## Verification (§6 of the task)

- `make BUILD_DIR=build/linux build` on the host is green. `gtk.sh make
  build` could not run, because there is no container engine here.
- `go test ./internal/window/...` under Xvfb is green.
- The GTK smoke tests of the areas touched (`TestConversationChatGTKSmoke`,
  `TestQuickReplyMessageGTKSmoke`, `TestBodyFitGTKSmoke`,
  `TestBoardGTKSmoke` and `TestTodayCalendarStripGTKSmoke`) pass one by
  one, with and without `MALACHI_GTK_CARDS_SMOKE=1`.
- All `GTKSmoke` tests in one process crash with SIGSEGV in
  `gtk_window_present`, at a different test each time. This happens with
  and without the changes of this branch, on this host with GTK 4.22.
- `make lint` is green. golangci-lint is not installed, so only `go vet`
  ran.
- `make po` produced no change: only the `POT-Creation-Date` line
  differed, and it was reverted.
