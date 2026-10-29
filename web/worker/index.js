// The website's Worker (web/wrangler.jsonc): one address, malachimail.app.
// The site's other names answer with a permanent redirect to it, the path
// and query kept. /dl/<tag>/<file> serves a release package from the R2
// bucket DOWNLOADS (web/README.md, "Packages"). Every other request is
// served from the static assets of web/, with their _headers, _redirects
// and 404 page; that includes the local `wrangler dev`, which presents
// every request as the first route. Plain http on malachimail.app is the
// zone's "Always Use HTTPS".

const CANONICAL = "malachimail.app";
const ALIASES = new Set(["www.malachimail.app", "malachimail.org", "www.malachimail.org"]);

// /dl/v0.1.4/Malachi-Mail-0.1.4-universal.dmg: a release tag and a plain
// file name, nothing else (no further directories, no dot segments, no
// escapes). The object key in the bucket is the path after /dl/.
const PACKAGE_PATH = /^\/dl\/(v\d+\.\d+\.\d+)\/([A-Za-z0-9][A-Za-z0-9._+-]*)$/;

// A package under a tag never changes once uploaded (a fix is a new
// version), so browsers and Cloudflare may keep it for a year.
const PACKAGE_CACHE = "public, max-age=31536000, immutable";

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (ALIASES.has(url.hostname)) {
      url.protocol = "https:";
      url.hostname = CANONICAL;
      url.port = "";
      return Response.redirect(url.toString(), 301);
    }
    if (url.pathname.startsWith("/dl/")) {
      return servePackage(request, env, url);
    }
    return env.ASSETS.fetch(request);
  },
};

// GET and HEAD of one package, with ETag, If-None-Match and Range (a
// download that broke off can resume). Never a redirect: the daemon
// follows none off the host it asked.
async function servePackage(request, env, url) {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Method not allowed\n", {
      status: 405,
      headers: { Allow: "GET, HEAD", "Content-Type": "text/plain; charset=utf-8" },
    });
  }
  const match = PACKAGE_PATH.exec(url.pathname);
  if (!match || match[2].includes("..")) return notFound(request, env);
  const [, tag, file] = match;
  const key = `${tag}/${file}`;

  const meta = await env.DOWNLOADS.head(key);
  if (meta === null) return notFound(request, env);
  // One range of bytes, or the whole file: a Range the Worker does not
  // understand (several ranges, other units) is ignored, as HTTP allows.
  const range = request.method === "GET" ? parseRange(request.headers.get("Range"), meta.size) : null;
  if (range === "unsatisfiable") {
    return new Response(null, { status: 416, headers: { "Content-Range": `bytes */${meta.size}` } });
  }
  const object =
    request.method === "HEAD"
      ? meta
      : await env.DOWNLOADS.get(key, range ? { onlyIf: request.headers, range } : { onlyIf: request.headers });
  if (object === null) return notFound(request, env);

  const headers = new Headers({
    "Content-Type": "application/octet-stream",
    "Content-Disposition": `attachment; filename="${file}"`,
    "Cache-Control": PACKAGE_CACHE,
    ETag: object.httpEtag,
    "Accept-Ranges": "bytes",
    "X-Content-Type-Options": "nosniff",
  });
  if (request.method === "HEAD") {
    headers.set("Content-Length", String(object.size));
    return new Response(null, { headers });
  }
  // A failed precondition gives the object without its body.
  if (!("body" in object)) {
    return new Response(null, { status: request.headers.has("If-None-Match") ? 304 : 412, headers });
  }
  if (range) {
    headers.set("Content-Range", `bytes ${range.offset}-${range.offset + range.length - 1}/${object.size}`);
    return new Response(object.body, { status: 206, headers });
  }
  return new Response(object.body, { headers });
}

// "bytes=a-b", "bytes=a-" or "bytes=-n" → { offset, length } within size;
// null for no Range or one to ignore; "unsatisfiable" when it starts past
// the end.
function parseRange(header, size) {
  const m = header === null ? null : /^bytes=(\d*)-(\d*)$/.exec(header.trim());
  if (!m || (m[1] === "" && m[2] === "")) return null;
  if (m[1] === "") {
    const suffix = Number(m[2]);
    if (suffix === 0) return "unsatisfiable";
    const length = Math.min(suffix, size);
    return { offset: size - length, length };
  }
  const start = Number(m[1]);
  const end = m[2] === "" ? size - 1 : Math.min(Number(m[2]), size - 1);
  if (start >= size) return "unsatisfiable";
  if (end < start) return null;
  return { offset: start, length: end - start + 1 };
}

// The site's own 404 page, so a wrong download link looks like any other.
async function notFound(request, env) {
  const page = await env.ASSETS.fetch(new URL("/404.html", request.url));
  return new Response(request.method === "HEAD" ? null : page.body, { status: 404, headers: page.headers });
}
