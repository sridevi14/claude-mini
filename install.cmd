@echo off
rem ============================================================
rem  claude-mini installer for Windows (CMD only)
rem  Downloads the latest claude-mini.exe from GitHub Releases
rem  into %USERPROFILE%\bin and adds that folder to your PATH.
rem ============================================================
setlocal enableextensions

set "REPO=sridevi14/claude-mini"
set "INSTALL_DIR=%USERPROFILE%\bin"
rem The binary is saved under this fixed name so the command is simply "claude-mini".
set "BIN=%INSTALL_DIR%\claude-mini.exe"

rem Pick the right release asset for this machine's CPU architecture.
set "ARCH=amd64"
if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "ARCH=arm64"
set "ASSET=claude-mini-windows-%ARCH%.exe"
set "URL=https://github.com/%REPO%/releases/latest/download/%ASSET%"

echo.
echo Installing claude-mini...
echo.

rem --- 1. Make sure %USERPROFILE%\bin exists -----------------
if not exist "%INSTALL_DIR%" (
    mkdir "%INSTALL_DIR%"
    if errorlevel 1 (
        echo ERROR: could not create "%INSTALL_DIR%".
        exit /b 1
    )
)

rem --- 2. Download the latest release binary -----------------
echo Downloading %ASSET% from the latest release...
where curl >nul 2>nul
if %errorlevel%==0 (
    curl -L --fail -o "%BIN%" "%URL%"
) else (
    powershell -NoProfile -Command "try { Invoke-WebRequest -Uri '%URL%' -OutFile '%BIN%' -UseBasicParsing } catch { exit 1 }"
)
if errorlevel 1 (
    echo.
    echo ERROR: download failed. Check your internet connection, or confirm a
    echo release named "%ASSET%" exists at:
    echo     https://github.com/%REPO%/releases/latest
    exit /b 1
)

rem --- 3. Add %USERPROFILE%\bin to the user PATH (permanent) --
rem Uses the User-scope PATH so it survives reboots and never touches the
rem system PATH. Idempotent: skips if the folder is already present.
powershell -NoProfile -Command "$d='%INSTALL_DIR%'; $p=[Environment]::GetEnvironmentVariable('Path','User'); if (-not $p) { $p='' }; if (($p -split ';') -notcontains $d) { $n = if ($p) { $p.TrimEnd(';') + ';' + $d } else { $d }; [Environment]::SetEnvironmentVariable('Path', $n, 'User'); Write-Host ('Added ' + $d + ' to your PATH.') } else { Write-Host ($d + ' is already on your PATH.') }"

echo.
echo ============================================================
echo  claude-mini was installed to:
echo      %BIN%
echo.
echo  Close this window and open a NEW Command Prompt, then run:
echo.
echo      claude-mini
echo.
echo  (run install.cmd again any time to update to the latest release)
echo ============================================================
echo.

endlocal
