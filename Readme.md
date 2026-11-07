# squidarr-proxy

Complete your Lidarr library with Qobuz, Deezer and Tidal search and downloads.

## Setup

### Docker
Use the included [docker-compose](docker-compose.yml) as reference for creating your container.

### Configuration

Searches return Qobuz and Deezer results together; each result identifies its source and downloads through that provider. Set an authorized `DEEZER_API_KEY` in `config.env` to enable Deezer downloads. Deezer downloads use FLAC directly: the mirror's `/api/track` route resolves the CDN URL and decryption key, the audio is fetched straight from Deezer's CDN and decrypted locally, and transient mirror failures are retried, so downloads do not depend on the relay route that intermittently answers 502. `QUALITY=mp3-320` selects MP3 for Qobuz results.

Tidal is added by pointing `TIDAL_API_LINK` at one or more hifi-api instances. It joins the combined search only when that variable is set. An instance hands back a signed DASH manifest, and both tiers are usable without a transcoder because the audio is already the final codec: LOSSLESS is FLAC carried in fMP4, remuxed into a native `.flac` (the `fLaC` header from the manifest's STREAMINFO, then the concatenated segment payloads), and HIGH is AAC-LC 320 kbit/s in fMP4, saved as `.m4a`. `TIDAL_QUALITY=HIGH` asks for the AAC tier, which is about a third of the size; leave it unset for FLAC, and an instance that can only do AAC is still written instead of refused. Public instances share one Tidal account and stop streaming whenever that account is banned, which is why the variable takes a list. A single track can be refused while its neighbours play, so an album is written with the tracks that stream and reported incomplete rather than failing whole.

### Sharing with a friend
For Windows, send the ZIP attached to a GitHub Release (not **Code → Download ZIP**). Your friend should copy `config.env.example` to `config.env`, set a private `API_KEY` and `DOWNLOAD_PATH`, then run `start_squidarr.bat`. Do not upload `config.env` or paste its secrets into GitHub. Treat credentials as passwords; preferably give your friend a separate instance/key rather than access to a server containing private data. The client is `client/index.html`.

To create a downloadable Windows release, push a version tag such as `v1.2.3`. GitHub Actions tests and builds the optimized executable, then attaches a minimal ZIP to the GitHub Release. The ZIP contains only the executable, launcher, client, config template, quick-start instructions, and license; it never includes `config.env`.

The following environment variables can be used to configure squidarr-proxy. You can use the included [config.env.example](config.env.example) as a template for your own `config.env` file.

| Variable | Description | Default |
|----------|-------------|---------|
| `API_KEY` | Your unique API key for squidarr-proxy. | (none) |
| `DEEZER_API_KEY` | API key required to stream Deezer downloads. | (none) |
| `DOWNLOAD_PATH` | The path where music will be downloaded. | `/data/squidarr/` |
| `QUALITY` | Download quality (`mp3-320`, `flac-lossless`, `flac-hi-res`). | `flac-hi-res` |
| `PORT` | The port squidarr-proxy will listen on. | `8687` (normally omit from `config.env`) |
| `DEBUG` | Enable debug logging (`true`/`false`). | `false` |
| `QOBUZ_TOKEN` | Optional Qobuz `user_auth_token` for direct official-API access. Tokens rotate and may be limited to 30-second previews. | (none) |
| `QOBUZ_NAV_LINK` | Qobuz search, album metadata, and full-length streaming backend. | `https://qobuz.itzsantiax.qzz.io` (configure in `config.env`) |
| `TIDAL_API_LINK` | Tidal hifi-api base URL(s), comma separated. Enables Tidal in search and downloads; omit to leave Tidal out entirely. | (none) |
| `TIDAL_QUALITY` | `LOSSLESS` (FLAC, default) or `HIGH` (AAC-LC 320, written as `.m4a`). | `LOSSLESS` |
| `APP_ID` / `APP_SECRET` | Optional Qobuz app credentials used to sign official-API requests. | Built-in defaults |

Use only credentials issued for your own account or application, and follow the provider's terms. Do not use shared token pools or credentials copied from third-party projects.

The retired `API_LINK` and premium Eclipse stream fallback have been removed after endpoint checks returned HTTP 503 and 404 respectively. Qobuz uses Navidrums, then the optional official API credentials.

Public Qobuz backends come and go. `python tools/qobuz_backend_probe.py` probes every host the project knows about with all four route shapes, fetches real audio for a searched track, and prints a verdict per host (`STREAMS`, `PREVIEW`, `SESSION`, `META_ONLY`, `DEAD`). Run it before blaming the proxy when Qobuz downloads stop resolving: a backend that answers search but refuses every stream is a blocked pool, not a broken installation.

### Windows (No Docker)
See [WINDOWS_SETUP.md](WINDOWS_SETUP.md) for instructions on how to run this on Windows without Docker.

Within Lidarr, set up a new Newznab indexer with the following settings:
1. Disable RSS
2. Set the URL to the IP/Hostname of your squidarr-proxy container, but make sure it begins with http:// and ends with your configured port (8687 by default)
3. Set the API path to /indexer
4. Set the API token you set in your docker-compose.yml

For the downloader, add a new SABnzbd downloader and configure the following:
1. Set the IP and port of the squidarr-proxy container
2. Set the Url base to "downloader"
3. Configure the API token you set in your docker-compose.yml
4. Set this downloader as the default for the squidarr-proxy indexer
