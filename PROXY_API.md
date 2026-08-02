# Proxy API Capabilities and Findings

This document tracks Qobuz proxy implementations, endpoint schemas, and observed availability.

## Known Proxy Endpoints

**Discovery endpoints:**
1. `https://eclipsemusic.app/addonstore/registry.json` — Eclipse Music addon registry; each addon has a `setupUrl` pointing to a config page. Known addon URLs:
   - `https://monochrome1.cyrusna29.workers.dev/` (Claudo — monochrome)
   - `https://all-in-one.cyrusna29.workers.dev/` (All in One)
   - `https://dawn-art-79bc.cyrusna29.workers.dev/` (Deezer — setup page only, no proxy)
   - `https://qobuz-tidal-eclipse.cyrusna29.workers.dev/` (Qobuz/Tidal — setup page JS reveals `QOBUZ_REAL` array with endpoints below)
   - `https://eclipse3.cyrusna29.workers.dev/` (SoundCloud — setup page only, no proxy)

**Qobuz endpoints** found in Qobuz addon JS (`QOBUZ_REAL` array):
- `https://qobuz-api1.onrender.com` — Qobuz-only (`{"service":"qobuz-api","status":"ok","token_set":true}`)
- `https://trypt-hifi-dl-456461932686.us-west1.run.app` — Qobuz-only (redirects to Google Cloud Run)
- `https://qobuz-api.stremio123.duckdns.org` — Qobuz-only (`{"service":"qobuz-api","status":"ok","token_set":true}`)

**FLACidal v0.9.0 Qobuz proxy endpoints** (7 total, from community proxy pool):
- dab: `https://dab.yeet.su`, `https://dabmusic.xyz`, `https://qobuz.squid.wtf`
- wjhe: `https://music.wjhe.top`
- gdstudio: `https://music.gdstudio.xyz`, `https://music.gdstudio.org`
- musicdl: `https://musicdl.me`

**MusicGrabber Qobuz proxy** (used by Navidrome plugin):
- `https://qdl-api.monochrome.tf` — Qobuz proxy for resolving direct audio streams

**Navidrums** (github.com/cesargomez89/navidrums) Qobuz proxy example:
- `https://qobuz.kennyy.com.br/api` — Qobuz proxy (downloads/streaming endpoint; currently times out)

**Wenqi qobuz-dl-go transparent API proxy:**
- `https://qobuz.wenqi.icu/api.json/0.2/` — Cloudflare Worker proxy for the official Qobuz API; requires the caller's Qobuz app ID, secret, and user token

**Self-hostable implementations (no usable public deployment found):**
- [BartolomeoRusso9/qobuz-rest-api](https://github.com/BartolomeoRusso9/qobuz-rest-api) — FastAPI REST facade with search, metadata, signed URL, stream, and download routes
- [KissAnotherDay/Qobuz-Api](https://github.com/KissAnotherDay/Qobuz-Api) — FastAPI Qobuz facade; runs locally on port 7979

## Qobuz Endpoint Status (checked 2026-07-24)

| URL | Status | Notes |
|-----|--------|-------|
| `https://qobuz-api1.onrender.com` | ❌ 503 Service Unavailable | — |
| `https://trypt-hifi-dl-456461932686.us-west1.run.app` | ❌ 503 Service Unavailable | — |
| `https://qobuz-api.stremio123.duckdns.org` | ❌ 404 Not Found | — |
| `https://dab.yeet.su` | ❌ Timeout | — |
| `https://dabmusic.xyz` | ❌ 503 Service Unavailable | — |
| `https://qobuz.squid.wtf` | ❌ Timeout (DNS dead) | — |
| `https://music.wjhe.top` | ❌ Timeout (DNS dead) | — |
| `https://musicdl.me` | ❌ 301 Redirect | Returns hex blob, not a Qobuz proxy |
| `https://qdl-api.monochrome.tf` | ❌ Timeout (DNS dead) | Former MusicGrabber/Navidrome proxy |
| `https://qobuz.kennyy.com.br/api` | ❌ Timeout | Navidrums Qobuz proxy |
| `https://qobuz.wenqi.icu/api.json/0.2/` | ⚠️ Alive (auth required) | Transparent CF Worker proxy to official Qobuz API; responds with Qobuz API errors (`401`/`400`) when credentials are provided. Requires Qobuz `app_id` + `user_auth_token`. |
| `https://qobuz-rest-hvfijzx06-bartolomeorusso9s-projects.vercel.app` | ❌ 410 Gone | Former `qobuz-rest-api` Vercel deployment; removed |
| `https://music.gdstudio.xyz` | ✅ 200 OK (music UI) | GD Studio music platform, not a Qobuz proxy API |
| `https://music.gdstudio.org` | ✅ 200 OK (music UI) | Same as above, mirror fallback |
| `https://qobuz-tidal-eclipse.cyrusna29.workers.dev/` | ✅ 200 OK (worker alive) | Eclipse addon worker; no public Qobuz proxy API exposed |

## Navidrums Qobuz Schema

Navidrums stores user-supplied Qobuz provider URLs in its database and does not contain a hidden or default fallback pool. Its only documented example is `https://qobuz.kennyy.com.br/api`.

| Route | Parameters | Purpose |
|-------|------------|---------|
| `GET {base}/get-music` | `q`, `offset` | Search all content types |
| `GET {base}/get-artist` | `artist_id` | Artist metadata and discography |
| `GET {base}/get-album` | `album_id` | Album metadata with tracks |
| `GET {base}/get-track` | `track_id` or `isrc` | Track metadata or ISRC lookup |
| `GET {base}/download-music` | `track_id`, `quality` | Time-limited signed stream URL |

Quality codes used by Navidrums are `27` (Hi-Res), `6` (lossless), `5` (high), and `1` (low).

## Official API Proxy Schema

The Eclipse addon and Wenqi proxy use the official Qobuz API shape under `/api.json/0.2`, including `/track/search`, `/album/search`, `/artist/search`, `/track/get`, `/album/get`, and signed `/track/getFileUrl`. These are not compatible with the Navidrums route names without an adapter.

The current Eclipse source tries `https://www.qobuz.com/api.json/0.2` directly first, then falls back to `qobuz-api1.onrender.com` and `trypt-hifi-dl-456461932686.us-west1.run.app`. Both fallback instances are offline. The Eclipse worker manifest is alive, but that does not establish that its Qobuz search or streaming path is working.

## Search Result

No additional raw Qobuz proxy URL was found on `r/QobuzDownloader` or broader Reddit searches. Posts predominantly reference tools such as qobuz-dl and FLACidal rather than publishing proxy endpoints. Searches of Russian and Chinese engines, GitHub/GitLab, Pastebin, GitHub Gists, Lidarr/Navidrome forums, and the Eclipse addon store likewise produced no currently usable unauthenticated endpoint.

The practical remaining option is self-hosting one of the REST facades with legitimate Qobuz credentials, optionally routing its upstream requests through a residential HTTP/SOCKS proxy. No verified public Qobuz proxy is currently usable.


