# squidarr-proxy

Complete your Lidarr library by downloading from Qobuz via squid.wtf

## Setup

### Docker
Use the included [docker-compose](docker-compose.yml) as reference for creating your container.

### Configuration

The following environment variables can be used to configure squidarr-proxy. You can use the included [config.env.example](config.env.example) as a template for your own `config.env` file.

| Variable | Description | Default |
|----------|-------------|---------|
| `API_LINK` | The API endpoint for metadata and download links. | `https://qobuz.squid.wtf/api` |
| `API_KEY` | Your unique API key for squidarr-proxy. | (none) |
| `DOWNLOAD_PATH` | The path where music will be downloaded. | `/data/squidarr/` |
| `QUALITY` | Download quality (`mp3-320`, `flac-lossless`, `flac-hi-res`). | `flac-hi-res` |
| `PORT` | The port squidarr-proxy will listen on. | `8687` |
| `DEBUG` | Enable debug logging (`true`/`false`). | `false` |

**API_LINK Alternatives:**
- `https://qobuz.kennyy.com.br/api` (Kennyy API)
- `https://qobuz.squid.wtf/api` (Squid.wtf API)

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
