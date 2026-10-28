// The website's Worker (web/wrangler.jsonc): one address, malachimail.app.
// The site's other names answer with a permanent redirect to it, the path
// and query kept. Every other request is served from the static assets of
// web/, with their _headers, _redirects and 404 page; that includes the
// local `wrangler dev`, which presents every request as the first route.
// Plain http on malachimail.app is the zone's "Always Use HTTPS".

const CANONICAL = "malachimail.app";
const ALIASES = new Set(["www.malachimail.app", "malachimail.org", "www.malachimail.org"]);

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (ALIASES.has(url.hostname)) {
      url.protocol = "https:";
      url.hostname = CANONICAL;
      url.port = "";
      return Response.redirect(url.toString(), 301);
    }
    return env.ASSETS.fetch(request);
  },
};
