# The website

Static HTML for the project's website: a presentation of the app, a
download page, the release notes, in English with a Czech mirror under
`cs/`. No build step, no JavaScript, no external fonts or trackers; the
pages share `assets/style.css` and work from a plain file server or
`file://`.

| Page | English | Czech |
|---|---|---|
| Presentation | `index.html` | `cs/index.html` |
| Features | `features.html` | `cs/features.html` |
| Download | `download.html` | `cs/download.html` |
| What's new | `news.html` | `cs/news.html` |
| Not found | `404.html` (GitHub Pages serves it for a missing path) | |

All links are relative, so the site works at the root of a domain as well
as under a path such as `https://schotek.github.io/malachi/`.

## Preview

```sh
python3 -m http.server -d web 8765
```

and open <http://localhost:8765/>.

## Publishing

`.github/workflows/pages.yml` uploads this directory to GitHub Pages on
every push to `main` that touches it. Pages has to be switched on once in
the repository settings (*Settings → Pages → Source: GitHub Actions*); a
custom domain goes in the same place and needs a `CNAME` file here.

## Keeping it current

- A new version: update the version, date and asset names on both
  download pages and add the release to both news pages from `NEWS`.
  The links point at `https://github.com/schotek/malachi/releases/download/<tag>/<asset>`,
  so the release has to be published, not a draft, for them to work.
- Screenshots: `assets/screenshots/*.webp` are the PNGs taken over the
  sample world as `docs/screenshots.md` describes (one set per platform
  and language), scaled to 1600 px wide (`sips -Z 1600`) and encoded
  with `cwebp -q 82 -sharp_yuv`.
- Icons: `assets/icon-*.png` and `icon-512.webp` are rasterised from
  `docs/malachi_icon.svg`; `favicon.ico` wraps the 32 and 64 px PNGs.
- The small illustrations (the three windows, the chat, the board, the
  MCP switchboard) are plain HTML and CSS, marked `aria-hidden`; the line
  icons are one inline SVG sprite at the top of each page that uses them.
- The English page is the reference; the Czech page mirrors it section
  for section.
