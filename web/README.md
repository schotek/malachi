# The website

Static HTML for the project's website: a presentation of the app, a
download page, the release notes, in English with a Czech mirror under
`cs/`. No build step, no external fonts or trackers, and one small
script; the pages share `assets/style.css` and work from a plain file
server or `file://`.

`assets/platform.js`, loaded in the head of the presentation and download
pages, reads the browser's platform (macOS, Linux or Windows; phones get
nothing) and puts the visitor's own desktop first: `data-platform` on
`<html>` picks the hero picture through CSS before the first paint, and
once the document is parsed the gallery's platform is checked, the
download buttons get " · macOS" and a `#macos`-style fragment, and the
matching download card moves first with a badge (`.yours`,
`data-yours`). Nothing leaves the browser. Without the script the pages
show macOS first.

`assets/lightbox.js` (deferred, the presentation pages) opens a
screenshot in the page's `<dialog class="lightbox">`: the picture with
its caption, the previous and next one of the strip it came from, Escape,
the arrow keys and a click beside it to close. Every screenshot is a link
to the picture itself (`a.shot`), which is what a browser without the
script gets.

| Page | English | Czech |
|---|---|---|
| Presentation | `index.html` | `cs/index.html` |
| Features | `features.html` | `cs/features.html` |
| The Assistant | `assistant.html` | `cs/assistant.html` |
| Under the hood | `under-the-hood.html` | `cs/under-the-hood.html` |
| Download | `download.html` | `cs/download.html` |
| What's new | `news.html` | `cs/news.html` |
| Not found | `404.html` (for a server that serves it for a missing path) | |

All links are relative, so the site works at the root of a domain as well
as under a path. Where and how it is published is not decided yet; nothing
in the repository deploys it.

## Preview

```sh
python3 -m http.server -d web 8765
```

and open <http://localhost:8765/>.

## Keeping it current

- A new version: update the version, date and asset names on both
  download pages and add the release to both news pages from `NEWS`.
  The links point at `https://github.com/schotek/malachi/releases/download/<tag>/<asset>`,
  so the release has to be published, not a draft, for them to work.
- Screenshots: `assets/screenshots/<platform>/<language>/<scene>.webp`
  are the PNGs taken over the sample world as `docs/screenshots.md`
  describes (one set per platform and language), the main window scaled
  to 1600 px wide (`sips -Z 1600`, so 1600×1000) and the compose window
  left as it was taken (1520×1280 on macOS and Linux, 1494×1267 on
  Windows, its client area), encoded with `cwebp -q 82 -sharp_yuv`. The
  gallery on the presentation page is one strip per platform behind
  radio buttons (`.gallery`, `.platforms`, `.set`: macOS, Linux,
  Windows), each with the same eleven figures; the hero's `.frame` has
  one `<img>` per platform, shown by two lines of the stylesheet per
  platform. The hero shows the inbox of the visitor's platform, macOS
  by default.
- Icons: `assets/icon-*.png` and `icon-512.webp` are rasterised from
  `docs/malachi_icon.svg`; `favicon.ico` wraps the 32 and 64 px PNGs.
- The small illustrations (the three windows, the chat, the board, the
  MCP switchboard) are plain HTML and CSS, marked `aria-hidden`; the line
  icons are one inline SVG sprite at the top of each page that uses them.
- The English page is the reference; the Czech page mirrors it section
  for section.
