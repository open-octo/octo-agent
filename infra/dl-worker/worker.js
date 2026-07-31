// Cloudflare Worker behind dl.octo-agent.dev — a download mirror for this
// repository's GitHub release assets, reachable from networks where
// github.com is blocked (notably mainland China). Cloudflare's edge fetches
// from GitHub, so the client never needs to reach GitHub itself.
//
// The path layout mirrors GitHub exactly, so everything that builds
// github.com URLs today (internal/upgrade, install.sh, install.ps1) works
// against this host unchanged:
//
//   /releases/latest                  -> 302, tag in the Location path
//   /releases/download/<tag>/<asset>  -> asset bytes (edge-cached; immutable)
//   /releases/latest/download/<asset> -> asset bytes (uncached; tracks latest)
//   /install.sh, /install.ps1         -> installer scripts from main
//   /api/releases/latest              -> the GitHub API release JSON, for the
//                                        desktop updater (cached 5 min)
//
// Only this fixed repository is proxied — the worker is not an open proxy.
// Trust still anchors on checksum verification in the clients; the mirror
// serves bytes, it does not vouch for them.

const REPO = 'open-octo/octo-agent'
const GITHUB = `https://github.com/${REPO}`
const RAW = `https://raw.githubusercontent.com/${REPO}/main/landing`
const LANDING = 'https://octo-agent.dev/'

export default {
  async fetch(request, env, ctx) {
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      return new Response('method not allowed', { status: 405 })
    }
    const { pathname } = new URL(request.url)

    if (pathname === '/releases/latest') {
      // Pass GitHub's redirect through untouched: clients (upgrade.Check,
      // the install scripts) parse the tag out of the Location path, and
      // only look at the path — the github.com host in it is fine.
      const upstream = await fetch(`${GITHUB}/releases/latest`, { redirect: 'manual' })
      const loc = upstream.headers.get('location')
      if (upstream.status >= 300 && upstream.status < 400 && loc) {
        return new Response(null, { status: 302, headers: { location: loc } })
      }
      return new Response(`unexpected upstream status ${upstream.status}`, { status: 502 })
    }

    if (pathname === '/api/releases/latest') {
      // The desktop updater's Wails GitHub provider checks the API, not the
      // web redirect. The asset URLs inside the JSON still point at
      // github.com — the desktop's mirrorTransport rewrites those requests
      // when they fail, so the JSON passes through unmodified. Cached
      // briefly: the worker's egress IPs are shared, and GitHub's anonymous
      // API rate limit is per-IP.
      const cache = caches.default
      if (request.method === 'GET') {
        const hit = await cache.match(request)
        if (hit) return hit
      }
      // GitHub's anonymous API limit is per-IP and the worker's egress IPs
      // are shared, so anonymous calls 403 intermittently. An optional
      // GITHUB_TOKEN secret (public-repo read-only) lifts that to 5000/h;
      // without it the endpoint still works, just less reliably — the
      // desktop updater falls back to notify-and-open on failure.
      const headers = {
        accept: 'application/vnd.github+json',
        'x-github-api-version': '2022-11-28',
        'user-agent': 'octo-dl-worker',
      }
      if (env.GITHUB_TOKEN) {
        headers.authorization = `Bearer ${env.GITHUB_TOKEN}`
      }
      const upstream = await fetch(`https://api.github.com/repos/${REPO}/releases/latest`, { headers })
      if (!upstream.ok) {
        const detail = (await upstream.text()).slice(0, 200)
        return new Response(`upstream ${upstream.status}: ${detail}`, {
          status: upstream.status === 404 ? 404 : 502,
        })
      }
      const resp = new Response(upstream.body, {
        status: 200,
        headers: {
          'content-type': 'application/json; charset=utf-8',
          'cache-control': 'public, max-age=300',
        },
      })
      if (request.method === 'GET') {
        ctx.waitUntil(cache.put(request, resp.clone()))
      }
      return resp
    }

    if (pathname === '/install.sh' || pathname === '/install.ps1') {
      const upstream = await fetch(`${RAW}${pathname}`)
      if (!upstream.ok) {
        return new Response(`upstream ${upstream.status}`, { status: 502 })
      }
      return new Response(upstream.body, {
        status: 200,
        headers: {
          'content-type': 'text/plain; charset=utf-8',
          'cache-control': 'public, max-age=300',
        },
      })
    }

    if (pathname.startsWith('/releases/download/')) {
      // Tagged asset URLs never change content — cache at the edge so a
      // release-day upgrade wave hits GitHub once per asset per colo.
      return proxyAsset(request, ctx, pathname, true)
    }
    if (pathname.startsWith('/releases/latest/download/')) {
      // Moves with every release — never cache.
      return proxyAsset(request, ctx, pathname, false)
    }

    return Response.redirect(LANDING, 302)
  },
}

async function proxyAsset(request, ctx, pathname, cacheable) {
  const cache = caches.default
  if (cacheable && request.method === 'GET') {
    const hit = await cache.match(request)
    if (hit) return hit
  }

  const upstream = await fetch(`${GITHUB}${pathname}`, {
    method: request.method,
    redirect: 'follow', // GitHub 302s assets to objects.githubusercontent.com
  })
  if (!upstream.ok) {
    return new Response(`upstream ${upstream.status} for ${pathname}`, {
      status: upstream.status === 404 ? 404 : 502,
    })
  }

  const headers = new Headers()
  for (const h of ['content-type', 'content-length', 'content-disposition', 'etag']) {
    const v = upstream.headers.get(h)
    if (v) headers.set(h, v)
  }
  headers.set('cache-control', cacheable ? 'public, max-age=31536000, immutable' : 'no-store')

  const resp = new Response(upstream.body, { status: 200, headers })
  if (cacheable && request.method === 'GET') {
    ctx.waitUntil(cache.put(request, resp.clone()))
  }
  return resp
}
