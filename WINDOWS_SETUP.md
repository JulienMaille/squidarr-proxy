# Squidarr Proxy - Windows Setup Guide

This guide explains how to run `squidarr-proxy` on Windows without Docker.

## Prerequisites

1.  **Windows OS** (Windows 10, 11, or Server).
2.  A Windows ZIP from the project's [Releases](https://github.com/JulienMaille/squidarr-proxy/releases).

## Step 1: Download the Binary

1. Download the latest `squidarr-proxy-windows-*.zip` from Releases.
2. Extract it to a folder of your choice.

## Step 2: Setup

1. Copy `config.env.example` to `config.env` in the extracted folder.
2. Set a private `API_KEY` and your `DOWNLOAD_PATH` in `config.env`.
3. Add `DEEZER_API_KEY` only if you are authorized to use the Deezer backend.

## Step 3: Run

1. Double-click `start_squidarr.bat`.
2. If configuration is valid, the server listens on port `8687` by default.
3. Open `client\index.html` and configure the server URL and API key.

Keep `config.env` private. It is ignored by Git and must never be committed or included in a public ZIP.

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
