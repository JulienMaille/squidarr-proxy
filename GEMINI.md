# Development Guidelines

## Go Compilation
- Go IS installed locally (go1.26.5 windows/amd64). Build from `src/`:
  `go build -o ../squidarr-proxy.exe .` — produces the root exe used by `start_squidarr.bat`.
- Builds are fast (~2-20 s); the exe is gitignored.

## Shell Commands (Windows/PowerShell)
- Use `curl.exe` instead of `curl` to ensure you are using the real curl binary rather than the PowerShell alias `Invoke-WebRequest`.
- Use `Select-String` instead of `grep` for searching through command output.
  - Example: `curl.exe -s "..." | Select-String -Pattern "pattern"`
- Use `;` instead of `&&` for chaining commands in PowerShell.
- Never kill the user's running instance (hub name `squidarr`, port 8687, persist). Test on a separate port, then swap by stop+start with the FULL env — `hub restart` retains the ORIGINAL launch spec (env changes are ignored).

## Qobuz Streaming & Download Entitlement (verified 2026-08-09)
- **Free-account tokens only serve 30-second previews** — `track/getFileUrl` returns `sample:true`, `duration:30`, and a URL with `range=20-30` (Akamai). This is account entitlement, NOT a code bug and NOT fixable with any token/secret/proxy. 30s preview at 320kbps ≈ 1.2 MB.
- The Eclipse Qobuz/Tidal worker used to expose premium stream routes, but the tested root, `/health`, and `/stream/{track_id}` routes returned HTTP 404 on 2026-10-30. It is no longer configured or called by the app.
- **Preview honesty**: downloader frame-walks every MP3 (MPEG1 Layer III header walk, integer frame length) — files < 60 s count as samples; if ALL tracks are samples the folder gets `-30S-SAMPLE` suffix and a loud log warning. VBR files need `floor(144*bitrate*1000/rate)` per frame (float frame length breaks the walk). FLAC files are measured from STREAMINFO instead (plain FLAC and the MP4/`dfLa` container Qobuz uses for 24-bit), and a file only counts as a sample when it is far shorter than its catalog duration, so a genuine 30 s interlude is not flagged.

## Full-length Navidrums backend (`QOBUZ_NAV_LINK`)
- `https://qobuz.itzsantiax.qzz.io` (Navidrums routes: `/api/get-music?q=`, `/api/get-album?album_id=`, `/api/download-music?track_id=&quality=`) streams FULL-LENGTH audio from a premium pool: `fmt=6` CD FLAC, `fmt=7` hi-res, no `range=` param.
- `/api/get-album` accepts the ALPHANUMERIC catalog id only (numeric `qobuz_id` → 400), so the app resolves the id Lidarr passes back with `navGetAlbumByHints` (search artist+title, prefer exact `qobuz_id`, then catalog id, then normalized title).
- Quality mapping: requested `5` → `5`, `7` (flac-lossless) → `6`, `27` (flac-hi-res) → `27`. The release is relabelled from the `fmt=` actually served, so folder names never overstate the quality.
- Resolution order per track: Navidrums → official `getFileUrl` (only when `QOBUZ_TOKEN` is configured). Dead premium/proxy fallbacks were removed on 2026-10-30.
- Verified 2026-10-23: Discovery 14/14, OK Computer 12/12, Nevermind 13/13 24-bit/96 kHz, durations matching the catalog. The pool is community-run and can disappear — keep `QOBUZ_NAV_LINK` configurable and rotate the host when it dies.
- Watched lead (not wired): `https://api-one.yupify.qzz.io` / `api-two.yupify.qzz.io`, `GET /api/track/{tidalId}` → `{assetPresentation:"FULL", audioQuality:"HI_RES_LOSSLESS", source:"qobuz-fallback", directUrl}` (6/6 200s). Keyed by Tidal id, returns a Qobuz CDN URL directly — no album enumeration, same owner/pool as `QOBUZ_NAV_LINK`, so it is a fallback for streaming only. Details in `PROXY_API.md`.

## Qobuz Credential Rotation
- App credentials are env-overridable without rebuild: `APP_ID` / `APP_SECRET` (read in `src/qobuzapi.go init()`) + `QOBUZ_TOKEN` (config.env).
- Working pair (2026-08): `APP_ID=312369995` + `APP_SECRET=e79f8b9be485692b0e5f9dd895826368`. The classic `798273057` / `abb21364…` pair is **IP-banned from the user's network** (401 on ALL endpoints, even anonymous login) — a 401 on everything = flagged app_id, not just a dead token; try a different app_id before blaming the network.
- Live free-token pool (rotates daily-ish): `GET https://citegptapi.f5.si/webhook/qbdlx/shared` with header `Origin: https://qbdlx.launchpd.cloud` → JSON array of `{token, country, createdAt, app_id, app_secret}`. ALL pool tokens are free-tier (previews only). Verify a token: `catalog/search` with it returns 200.
- Do NOT paste live tokens into docs — token lives in config.env; docs are procedure-only (see PROXY_API.md).

## API & Metadata
- The upstream API (`qdl-api.monochrome.tf`) sometimes returns items wrapped in a `{"type": "...", "content": "..."}` structure, and sometimes as direct objects.
- Ensure duration is parsed correctly from the correct level depending on the item type.
- Client (`client/index.html`): search query persists in the URL (`?q=`) via history.replaceState; server/key/quality/filters persist in localStorage.
