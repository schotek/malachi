// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The hostile document of the WebView2 spike (spikes/recommended/
// CanaryTests.cs, SPIKES.md §2c), with a canary per vector: every way a page
// can make a request or a connection that the spike measured (pictures, CSS,
// <link> hints, prerender and speculation rules, frames, plugins, media,
// SVG, forms, pings, refresh), host-name variants for the DNS check, and the
// active document whose links, form, new-window and download targets the
// host clicks and hovers. Plus what the previewer gets: an SVG that
// references the canaries and a PDF whose link and open action point at
// them.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;

namespace Malachi.App.Canary;

/// <summary>The documents of the canary, built over its listeners.</summary>
internal static class HostileDocuments
{
    /// <summary>The passive vectors: id and markup, <c>{U}</c> standing for the vector's canary origin.</summary>
    public static readonly (string Id, string Html)[] PassiveVectors =
    [
        ("img", "<img src='{U}/img.png' width=10 height=10>"),
        ("img-srcset", "<img srcset='{U}/srcset.png 1x' width=10 height=10>"),
        ("picture-source", "<picture><source srcset='{U}/picture.png'><img width=10 height=10></picture>"),
        ("css-bg-inline", "<div style='background:url({U}/bg-inline.png);width:10px;height:10px'></div>"),
        ("css-bg-escaped", "<div style='background:u\\72 l(\"{U}/bg-escaped.png\");width:10px;height:10px'></div>"),
        ("css-bg-styletag", "<style>.bg2{background-image:url({U}/bg-style.png)}</style><div class=bg2 style='width:10px;height:10px'></div>"),
        ("css-import", "<style>@import url({U}/import.css);</style>"),
        ("css-fontface", "<style>@font-face{font-family:spkf;src:url({U}/font.woff)}</style><span style=\"font-family:spkf\">font</span>"),
        ("css-image-set", "<div style='background-image:image-set(\"{U}/imageset.png\" 1x);width:10px;height:10px'></div>"),
        ("css-list-style", "<ul style='list-style-image:url({U}/list.png)'><li>li</li></ul>"),
        ("css-border-image", "<div style='border:5px solid;border-image:url({U}/border.png) 30 round;width:10px;height:10px'></div>"),
        ("css-mask", "<div style='-webkit-mask-image:url({U}/mask.png);mask-image:url({U}/mask.png);width:10px;height:10px;background:red'></div>"),
        ("css-content", "<style>.cc::before{content:url({U}/content.png)}</style><div class=cc></div>"),
        ("css-cursor", "<div style='cursor:url({U}/cursor.cur),auto;width:10px;height:10px'></div>"),
        ("link-stylesheet", "<link rel=stylesheet href='{U}/style.css'>"),
        ("link-preconnect", "<link rel=preconnect href='{U}'>"),
        ("link-prefetch", "<link rel=prefetch href='{U}/prefetch'>"),
        ("link-prerender", "<link rel=prerender href='{U}/prerender'>"),
        ("link-preload-img", "<link rel=preload as=image href='{U}/preload.png'>"),
        ("link-preload-style", "<link rel=preload as=style href='{U}/preload.css'>"),
        ("link-preload-font", "<link rel=preload as=font crossorigin href='{U}/preload.woff2'>"),
        ("link-modulepreload", "<link rel=modulepreload href='{U}/module.js'>"),
        ("link-icon", "<link rel=icon href='{U}/favicon.ico'>"),
        ("link-manifest", "<link rel=manifest href='{U}/manifest.json'>"),
        ("meta-http-equiv-link", "<meta http-equiv='Link' content='<{U}/metalink.css>; rel=preload; as=style'>"),
        ("speculationrules", "<script type=speculationrules>{\"prefetch\":[{\"source\":\"list\",\"urls\":[\"{U}/spec-prefetch\"]}],\"prerender\":[{\"source\":\"list\",\"urls\":[\"{U}/spec-prerender\"]}]}</script>"),
        ("script-src", "<script src='{U}/script.js'></script>"),
        ("script-inline-fetch", "<script>fetch('{U}/fetch');new Image().src='{U}/js-image.png';</script>"),
        ("onerror-handler", "<img src='x:' onerror=\"this.src='{U}/onerror.png'\" width=10 height=10>"),
        ("iframe", "<iframe src='{U}/frame' width=20 height=20></iframe>"),
        ("iframe-srcdoc-img", "<iframe srcdoc=\"<img src='{U}/srcdoc.png'>\" width=20 height=20></iframe>"),
        ("object-img", "<object data='{U}/object.png' type='image/png' width=10 height=10></object>"),
        ("object-html", "<object data='{U}/object.html' type='text/html' width=10 height=10></object>"),
        ("embed", "<embed src='{U}/embed.png' type='image/png' width=10 height=10>"),
        ("video-poster", "<video poster='{U}/poster.png' width=20 height=20></video>"),
        ("video-src", "<video src='{U}/video.mp4' preload=auto width=20 height=20></video>"),
        ("video-source", "<video preload=auto width=20 height=20><source src='{U}/source.mp4' type='video/mp4'></video>"),
        ("audio-src", "<audio src='{U}/audio.mp3' preload=auto></audio>"),
        ("track", "<video width=20 height=20 preload=auto><track default kind=subtitles srclang=en src='{U}/track.vtt'></video>"),
        ("svg-image", "<svg width=10 height=10><image href='{U}/svgimage.png' width=10 height=10/></svg>"),
        ("svg-use", "<svg width=10 height=10><use href='{U}/sprite.svg#a'/></svg>"),
        ("svg-feimage", "<svg width=10 height=10><filter id=f1><feImage href='{U}/feimage.png'/></filter><rect filter='url(#f1)' width=10 height=10/></svg>"),
        ("input-image", "<input type=image src='{U}/input.png' width=10 height=10>"),
        ("table-background", "<table background='{U}/tablebg.png'><tr><td background='{U}/tdbg.png'>t</td></tr></table>"),
        ("noscript-img", "<noscript><img src='{U}/noscript.png' width=10 height=10></noscript>"),
        ("anchor-plain", "<a href='{U}/anchor'>plain anchor</a>"),
        ("protocol-relative", "<img src='//127.0.0.1:{P}/protocol-relative.png' width=10 height=10>"),
        ("base-href", ""),
        ("body-background", ""),
    ];

    /// <summary>The vectors of the active document (clicked, hovered, submitted, refreshed).</summary>
    public static readonly string[] ActiveVectors = ["hover", "nav", "ping", "form", "blank", "middle", "download", "refresh"];

    /// <summary>The previewer's vectors (an SVG's references, a PDF's link and open action).</summary>
    public static readonly string[] PreviewVectors = ["svg-preview", "pdf-link", "pdf-open"];

    /// <summary>Every vector.</summary>
    public static IEnumerable<string> AllVectors =>
        PassiveVectors.Select(v => v.Id).Concat(ActiveVectors).Concat(PreviewVectors);

    /// <summary>The DNS-only host names of a run (they resolve nowhere; only a lookup would show).</summary>
    public static string[] DnsHosts(string run) =>
        ["img-" + run + ".dnscanary.invalid", "preconnect-" + run + ".dnscanary.invalid",
         "dnsprefetch-" + run + ".dnscanary.invalid", "anchor-" + run + ".dnscanary.invalid"];

    /// <summary>The passive document: every vector at once, loaded and left alone.</summary>
    public static string Passive(Func<string, CanaryListener> canary, string run)
    {
        var b = new StringBuilder("<!doctype html><html><head><meta charset=utf-8><title>passive</title>");
        b.Append("<base href='").Append(canary("base-href").Origin).Append("/base/'>");
        b.Append("</head><body background='").Append(canary("body-background").Origin).Append("/bodybg.png'>\n");
        b.Append("<p>cid image: <img id=cid src='malachi-cid:acc_1/msg_2/2'> <img src='cid:canary@x'></p>\n");
        foreach (var (id, html) in PassiveVectors)
        {
            if (html.Length > 0)
            {
                var c = canary(id);
                b.Append(html.Replace("{U}", c.Origin, StringComparison.Ordinal)
                    .Replace("{P}", c.Port.ToString(CultureInfo.InvariantCulture), StringComparison.Ordinal)).Append('\n');
            }
        }
        b.Append("<img src='rel-from-base.png' width=10 height=10>\n");
        var hosts = DnsHosts(run);
        b.Append("<img src='http://").Append(hosts[0]).Append("/x.png' width=10 height=10>\n");
        b.Append("<link rel=preconnect href='http://").Append(hosts[1]).Append("'>\n");
        b.Append("<link rel=dns-prefetch href='//").Append(hosts[2]).Append("'>\n");
        b.Append("<a href='http://").Append(hosts[3]).Append("/'>dns anchor</a>\n");
        b.Append("<img src='file://127.0.0.1/c$/windows/win.ini' width=10 height=10>\n");
        b.Append("</body></html>");
        return b.ToString();
    }

    /// <summary>
    /// The active document: links, a form and a download target at fixed
    /// places, each with an id the host points at.
    /// </summary>
    public static string Active(Func<string, CanaryListener> canary)
    {
        static string Box(int top) =>
            "position:absolute;left:20px;top:" + top.ToString(CultureInfo.InvariantCulture) + "px;display:block;width:260px;height:26px";
        return "<!doctype html><html><head><meta charset=utf-8><title>active</title></head><body>"
            + "<a id=pinglink href='" + canary("nav").Origin + "/nav' ping='" + canary("ping").Origin + "/ping' style='" + Box(20) + "'>ping link</a>"
            + "<form id=f action='" + canary("form").Origin + "/submit' method=get><input type=hidden name=q value=secret></form>"
            + "<button form=f id=sub style='" + Box(70) + "'>send</button>"
            + "<a id=blank target=_blank href='" + canary("blank").Origin + "/blank' style='" + Box(120) + "'>blank</a>"
            + "<a id=mailto href='mailto:someone@example.org?subject=hi' style='" + Box(170) + "'>mailto</a>"
            + "<a id=hover href='" + canary("hover").Origin + "/hover' style='" + Box(220) + "'>hover link</a>"
            + "<a id=middle href='" + canary("middle").Origin + "/middle' style='" + Box(270) + "'>middle link</a>"
            + "<a id=dl download href='" + canary("download").Origin + "/file.bin' style='" + Box(320) + "'>download</a>"
            + "</body></html>";
    }

    /// <summary>A document that refreshes itself to the refresh canary at once.</summary>
    public static string Refresh(Func<string, CanaryListener> canary) =>
        "<!doctype html><html><head><meta charset=utf-8><meta http-equiv=refresh content=\"0;url="
        + canary("refresh").Origin + "/refresh\"></head><body>refresh</body></html>";

    /// <summary>An SVG file that references the preview canary (the previewer shows its source).</summary>
    public static byte[] Svg(Func<string, CanaryListener> canary)
    {
        var u = canary("svg-preview").Origin;
        return Encoding.UTF8.GetBytes(
            "<?xml version=\"1.0\"?><?xml-stylesheet href=\"" + u + "/xsl.css\"?>"
            + "<svg xmlns=\"http://www.w3.org/2000/svg\" xmlns:xlink=\"http://www.w3.org/1999/xlink\" width=\"100\" height=\"100\">"
            + "<style>@import url(" + u + "/svg.css);</style><image href=\"" + u + "/svg.png\" width=\"10\" height=\"10\"/>"
            + "<use xlink:href=\"" + u + "/sprite.svg#a\"/><script href=\"" + u + "/svg.js\"/></svg>");
    }

    /// <summary>The title in the canary PDF's metadata, which must never name a window.</summary>
    public const string PdfTitle = "canary-pdf-title";

    /// <summary>
    /// A one-page PDF whose page is a link to the pdf-link canary, whose open
    /// action is a URI action to the pdf-open canary, and whose metadata
    /// carries <see cref="PdfTitle"/>.
    /// </summary>
    public static byte[] Pdf(Func<string, CanaryListener> canary)
    {
        var link = canary("pdf-link").Origin + "/pdf-link";
        var open = canary("pdf-open").Origin + "/pdf-open";
        var objects = new[]
        {
            "<</Type/Catalog/Pages 2 0 R/OpenAction<</S/URI/URI(" + open + ")>>>>",
            "<</Type/Pages/Kids[3 0 R]/Count 1>>",
            "<</Type/Page/Parent 2 0 R/MediaBox[0 0 400 300]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>"
                + "/Annots[<</Type/Annot/Subtype/Link/Rect[0 0 400 300]/Border[0 0 0]/A<</S/URI/URI(" + link + ")>>>>]>>",
            ContentStream("BT /F1 24 Tf 40 200 Td (Malachi canary) Tj ET"),
            "<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
            "<</Title(" + PdfTitle + ")>>",
        };
        return Document(objects);
    }

    private static string ContentStream(string content) =>
        "<</Length " + content.Length.ToString(CultureInfo.InvariantCulture) + ">>stream\n" + content + "\nendstream";

    // A PDF with a correct cross-reference table over the objects, the first
    // the catalog, the last the document information.
    private static byte[] Document(string[] objects)
    {
        var b = new StringBuilder("%PDF-1.4\n");
        var offsets = new List<int>();
        for (var i = 0; i < objects.Length; i++)
        {
            offsets.Add(Encoding.ASCII.GetByteCount(b.ToString()));
            b.Append(i + 1).Append(" 0 obj\n").Append(objects[i]).Append("\nendobj\n");
        }
        var xref = Encoding.ASCII.GetByteCount(b.ToString());
        b.Append("xref\n0 ").Append(objects.Length + 1).Append("\n0000000000 65535 f \n");
        foreach (var offset in offsets)
        {
            b.Append(offset.ToString("D10", CultureInfo.InvariantCulture)).Append(" 00000 n \n");
        }
        b.Append("trailer\n<</Size ").Append(objects.Length + 1).Append("/Root 1 0 R/Info ").Append(objects.Length)
            .Append(" 0 R>>\nstartxref\n").Append(xref).Append("\n%%EOF\n");
        return Encoding.ASCII.GetBytes(b.ToString());
    }
}
