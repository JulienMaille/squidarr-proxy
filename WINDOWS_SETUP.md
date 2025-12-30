# Squidarr Proxy - Windows Setup Guide

This guide explains how to run `squidarr-proxy` on Windows without Docker.

## Prerequisites

1.  **Windows OS** (Windows 10, 11, or Server).
2.  **A GitHub Account** (to download the build artifact).

## Step 1: Download the Binary

1.  Navigate to the [**Actions**](https://github.com/JulienMaille/squidarr-proxy/actions/workflows/build-windows.yml)
    *   If you don't see one, you may need to enable workflows or trigger it manually by pushing a  tab of this repository on GitHub.
2.  Click on the latest run of the **Build Windows Binary** workflow.change or clicking "Run workflow" if available.
3.  Scroll down to the **Artifacts** section.
4.  Click on `squidarr-proxy-windows` to download the zip file.
5.  Extract `squidarr-proxy.exe` from the zip file.

## Step 2: Setup

1.  Create a folder where you want to keep the application (e.g., `C:\Tools\squidarr-proxy`).
2.  Move `squidarr-proxy.exe` into this folder.
3.  Download or create the `start_squidarr.bat` file from this repository and place it in the same folder.

## Step 3: Run

1.  Double-click `start_squidarr.bat`.
2.  The script will ask you for:
    *   **API Key:** This acts as the password for your instance. You will use this in Lidarr.
    *   **Download Path:** The full path where downloads should be saved (e.g., `C:\Downloads\squidarr`).
3.  The application should start and listen on port `8687`.

### Optional: Configuration File

Instead of entering details every time, you can create a file named `config.env` in the same folder with the following content:

```env
API_KEY=your_secret_password
DOWNLOAD_PATH=C:\Downloads\squidarr
PORT=8687
CATEGORY=music
TZ=Europe/Berlin
```

## Step 4: Configure Lidarr

1.  **Indexer (Newznab):**
    *   **Enable RSS:** unchecked
    *   **URL:** `http://localhost:8687`
    *   **API Path:** `/indexer`
    *   **API Key:** The value you entered/configured above.

2.  **Downloader (SABnzbd):**
    *   **Host:** `localhost`
    *   **Port:** `8687`
    *   **URL Base:** `downloader`
    *   **API Key:** The same value as above.
    *   **Category:** `music` (default)
