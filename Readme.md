# squidarr-proxy

Complete your Lidarr library by downloading from Qobuz via squid.wtf

## Setup

### Docker
Use the included [docker-compose](docker-compose.yml) as reference for creating your container.

### Configuration

Searches return Qobuz and Deezer results together; each result identifies its source and downloads through that provider. Set `DEEZER_API_KEY` in `config.env` to enable Deezer downloads (the community key is not bundled). Deezer downloads use validated FLAC directly and require no external transcoder. `QUALITY=mp3-320` selects MP3 for Qobuz results; Deezer remains FLAC.

### Sharing with a friend
Send your friend the GitHub **Code → Download ZIP** archive and email them their own `config.env` based on `config.env.example`. Do not upload `config.env` or paste its secrets into GitHub. Include the server address, a unique `API_KEY`, and any required backend credentials. Treat the emailed file and API key as a password: anyone who obtains them can use your server. Prefer giving your friend a separate instance/key with restricted access rather than sharing credentials for a server containing private data. They should extract the ZIP, put `config.env` beside `start_squidarr.bat` (Windows) or follow the Docker setup, then start the app. The client can be opened from the extracted `client/index.html` file and configured with the server URL and API key.

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
| `APP_ID` / `APP_SECRET` | Qobuz app credential pair used to sign official-API requests. Override when the default pair is IP-banned/revoked. | `798273057` / `abb21364…` |

**Finding fresh credentials** (tokens rotate often):
1. `QOBUZ_USER_TOKEN` / `QOBUZ_APP_ID` / `QOBUZ_SECRET` are published in [monochrome `index.js`](https://github.com/jacobyz211/monochrome/blob/main/index.js) — take the newest values from the commit history.
2. Live pool of currently-valid tokens: `GET https://citegptapi.f5.si/webhook/qbdlx/shared` (send header `Origin: https://qbdlx.launchpd.cloud`).
3. Verify a token before use: `GET https://www.qobuz.com/api.json/0.2/catalog/search?app_id=312369995&user_auth_token=<token>&query=test&limit=1` → HTTP 200 = live.
Full procedure and caveats (free accounts get 30-second previews only): [PROXY_API.md](PROXY_API.md).

The retired `API_LINK` and premium Eclipse stream fallback have been removed after endpoint checks returned HTTP 503 and 404 respectively. Qobuz uses Navidrums, then the optional official API credentials.

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
