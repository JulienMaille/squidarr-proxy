# Proxy API Capabilities and Findings

This document tracks Qobuz proxy implementations, endpoint schemas, and observed availability.

## Full-length Navidrums backend (verified 2026-10-23)

`https://qobuz.itzsantiax.qzz.io` — Navidrums route shape (the schema documented
below), backed by its own **premium** pool (`uid=468453`): streams are served
`fmt=6` (CD FLAC) or `fmt=7` (hi-res ≤96 kHz) with **no `range=` parameter**, i.e.
full-length, not 30 s previews.

| Route | Params | Response |
|-------|--------|----------|
| `GET /api/get-music` | `q`, `offset` | `{success, data:{albums:{items:[…]}, tracks:{…}}}` — items carry the **alphanumeric** catalog `id`, the numeric `qobuz_id`, `tracks_count`, `maximum_bit_depth`, `maximum_sampling_rate` (kHz) |
| `GET /api/get-album` | `album_id` (**catalog id only** — numeric `qobuz_id` → HTTP 400) | `{success, data:{…, tracks:{items:[{id,title,duration,track_number,media_number,isrc}]}}}` |
| `GET /api/download-music` | `track_id`, `quality` (`5`=MP3-320, `6`=CD FLAC, `7`/`27`=hi-res) | `{success, data:{url}}` — signed Akamai URL, `fmt=` tells the format actually served |
| `GET /api/get-artist` | `artist_id` | artist metadata; `get-track` is not implemented |

Verified end-to-end: Discovery (14/14 tracks, 16/44.1), OK Computer (12/12),
Nevermind (13/13, **24/96**). Measured audio duration matches the catalog duration
within 2 s on every sampled track. 24-bit files arrive as **MP4/FLAC** (`fLaC`
header + `dfLa` box) — duration/format sniffing must handle both containers.

The app treats it as `QOBUZ_NAV_LINK`: streams are resolved through it first, then
the official API when `QOBUZ_TOKEN` is configured. The dead premium and proxy
fallbacks were removed after live route checks on 2026-10-30.

### Lead: Yupify gateways (2026-10-25, NOT wired)

`https://api-one.yupify.qzz.io` and `https://api-two.yupify.qzz.io` (from
`Santiaxelpro/Yupify-Reworked`, commit `35fc005`, `workers/src/index.ts`) expose
`GET /api/track/{tidalId}` with no key:

```json
{"assetPresentation":"FULL","audioQuality":"HI_RES_LOSSLESS",
 "source":"qobuz-fallback","directUrl":"<Qobuz CDN url>",
 "isrc":"...","qobuzTrackId":1068442}
```

6/6 requests 200 on 3 Tidal ids, both gateways, ~2.5 s per call. Two properties matter
here: the lookup key is a **Tidal track id** (which the indexer already has), and the
payload is a ready **Qobuz CDN URL** — so this is an alternative source for the
`QOBUZ_NAV_LINK` slot, not a Navidrums adapter (`/api/get-music`, `/api/get-album`
and album enumeration do not exist there, so Lidarr album search/`f` naming would
still need Navidrums).

Same owner as `qobuz.itzsantiax.qzz.io` (its own `DEFAULT_HIFI_APIS` is that project's
`hifi-one`/`hifi-two`, both metadata-only today) → not independent redundancy, and it
disappears with the same pool. Before wiring: check real FLAC `STREAMINFO` durations
(no 30 s previews), coverage on a wider sample, and whether the URLs are byte-identical
to what `/api/download-music?quality=7` returns.

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

## Qobuz Endpoint Status (checked 2026-10-30)

| URL | Status | Notes |
|-----|--------|-------|
| `https://qobuz-api1.onrender.com` | ❌ 503 Service Unavailable | Confirmed again at root and `/search`; removed from squidarr-proxy config/runtime. |
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
| `https://qobuz-tidal-eclipse.cyrusna29.workers.dev/` | ❌ 404 on root, `/health`, and tested `/stream/{id}` | Premium stream route is gone; removed from squidarr-proxy config/runtime. |

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

## Premium-backed stream backend (FULL-LENGTH, no previews)

The Eclipse **Qobuz/Tidal** addon worker (`com.eclipse.community.qobuztidal` in the
[eclipse registry](https://eclipsemusic.app/addonstore/registry.json)) serves streams
signed with its own **paid** account (`credLabel` e.g. `rickyrick1019`, Akamai
`uid=2759740` / `uid=14104545`) — unlike `qobuz-api1.onrender.com`, which is free-tier
(`range=20-30` previews, MP3 demote).

| Route | Params | Response |
|-------|--------|----------|
| `GET /stream/{track_id}?quality=320` | `320` → 320kbps MP3 (fmt 5); `flac` (default) → 16/44.1 FLAC (fmt 6) | `{url, format, quality, expiresAt, cacheHit, credId, credLabel}` — **no `range=` param = full-length** |
| `GET /search?q={query}` | — | `{tracks[], albums[], artists[], playlists[]}` (own shape, catalog ids alphanumeric) |
| `GET /album/{catalog_id}` | alphanumeric id only | `{id, title, artist, tracks[]}` (own shape) |
| `GET /health` | — | `{"status":"ok","qobuz":true,...}` |

Historical observation (2026-08-09): this worker previously served full-length FLAC
and MP3-320 streams. Its tested stream route now returns 404, so squidarr-proxy no
longer uses it.

**Dead since 2026-10-23**: `/health` and `/stream/{id}` return HTTP 404 from the
worker (the whole route surface is gone). Do not configure it; use `QOBUZ_NAV_LINK`
(see above) for full-length audio.

## Search Result

No additional raw Qobuz proxy URL was found on `r/QobuzDownloader` or broader Reddit searches. Posts predominantly reference tools such as qobuz-dl and FLACidal rather than publishing proxy endpoints. Searches of Russian and Chinese engines, GitHub/GitLab, Pastebin, GitHub Gists, Lidarr/Navidrome forums, and the Eclipse addon store surfaced no other *usable* endpoint — the Eclipse Qobuz/Tidal worker above is the one live premium-backed backend found; everything else is dead, free-tier, or requires user-supplied credentials. Self-hosting one of the REST facades with legitimate Qobuz credentials remains the reliable long-term option.



## Finding Fresh Qobuz Tokens (user_auth_token)

Qobuz user tokens rotate often and get revoked. When the official API starts returning 401
(`"User authentication is required"`) for every endpoint, fetch the newest token from
projects that embed it in client-side source:

1. **`jacobyz211/monochrome` / `jacobyz211/eclipse3`** — constants
   `QOBUZ_APP_ID` / `QOBUZ_USER_TOKEN` / `QOBUZ_SECRET` live in
   [`index.js`](https://github.com/jacobyz211/monochrome/blob/main/index.js) (values
   change in commit history — the raw file may lag the newest rotation). The 798273057
   pair was banned from many IPs (2026-08); the current working pair in `config.env` is
   `APP_ID=312369995` + `APP_SECRET=e79f8b9be485692b0e5f9dd895826368` (also seen in
   monochrome commit history and the Stash pool below).
2. **Live token pool (Stash, rawnaldclark/Stash)** — refreshed continuously:
   `GET https://citegptapi.f5.si/webhook/qbdlx/shared` (send `Origin:
   https://qbdlx.launchpd.cloud`). Returns a JSON array of
   `{token, country, createdAt, app_id, app_secret}` — pick a recent entry whose
   `app_id` matches `config.env`'s `APP_ID`. **All pool tokens are free-tier**
   (see previews below); they rotate/expire within days.
3. **Verify before use** — a token is live iff this returns HTTP 200 (no auth headers):
   `GET https://www.qobuz.com/api.json/0.2/catalog/search?app_id=312369995&user_auth_token=<token>&query=test&limit=1`
4. Write the fresh token to `config.env` (`QOBUZ_TOKEN=`) and restart `squidarr-proxy.exe`.
   `APP_ID` / `APP_SECRET` are also env-overridable (no rebuild needed).

Notes learned 2026-08-09:
- **Free-account tokens only serve 30-second previews.** `track/getFileUrl` on a free
  token returns `"sample": true, "duration": 30, restrictions: [UserUnauthenticated]` and
  a signed URL containing `range=20-30`; full-length streams require a paid
  (premium/studio) account — no token, code, or proxy choice bypasses this. The
  downloader now detects previews by MP3 frame count and renames the completed folder
  with a `-30S-SAMPLE` suffix instead of silently delivering samples as full tracks.
- Historical pipeline note (no longer active): a 401 on **all** endpoints (even unsigned `album/get`) with the 798273057 pair means
  that app_id is flagged/blocked from the caller's IP — a different app_id
  (312369995) works from the same IP. The old proxy fallback is retired; current
  Qobuz search/streaming relies on `QOBUZ_NAV_LINK` and optionally the official API.

