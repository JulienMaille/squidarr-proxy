@echo off
setlocal

echo Squidarr Proxy - Windows Launcher
echo =================================

REM Default values
set PORT=8687
set CATEGORY=music
set TZ=Europe/Berlin

REM Check for config file
if exist "config.env" (
    echo Loading configuration from config.env...
    for /f "tokens=1* delims==" %%a in (config.env) do set %%a=%%b
) else (
    echo config.env not found. prompting for input...
)

REM Prompt for API_KEY if not set
if "%API_KEY%"=="" (
    set /p API_KEY="Enter your API Key (Password for Lidarr): "
)

REM Prompt for DOWNLOAD_PATH if not set
if "%DOWNLOAD_PATH%"=="" (
    set /p DOWNLOAD_PATH="Enter your Download Path (e.g., C:\Downloads\squidarr): "
)

REM Verify inputs
if "%API_KEY%"=="" (
    echo Error: API Key is required.
    pause
    exit /b 1
)

if "%DOWNLOAD_PATH%"=="" (
    echo Error: Download Path is required.
    pause
    exit /b 1
)

REM Display configuration
echo.
echo Configuration:
echo   PORT: %PORT%
echo   CATEGORY: %CATEGORY%
echo   API_KEY: [HIDDEN]
echo   DOWNLOAD_PATH: %DOWNLOAD_PATH%
echo   TZ: %TZ%
echo.

REM Run the proxy
echo Starting squidarr-proxy...
squidarr-proxy.exe

pause
