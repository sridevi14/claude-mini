@echo off
rem ============================================================
rem  claude-mini installer for Windows (CMD only)
rem  Downloads the latest claude-mini.exe from GitHub Releases
rem  into %USERPROFILE%\bin and adds that folder to your PATH.
rem ============================================================
setlocal enableextensions

set "REPO=sridevi14/claude-mini"
set "ASSET=claude-mini.exe"
set "INSTALL_DIR=%USERPROFILE%\bin"
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
    curl -L --fail -o "%INSTALL_DIR%\%ASSET%" "%URL%"
) else (
    powershell -NoProfile -Command "try { Invoke-WebRequest -Uri '%URL%' -OutFile '%INSTALL_DIR%\%ASSET%' -UseBasicParsing } catch { exit 1 }"
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
echo      %INSTALL_DIR%\%ASSET%
echo.
echo  Close this window and open a NEW Command Prompt, then run:
echo.
echo      claude-mini
echo.
echo  (run install.cmd again any time to update to the latest release)
echo ============================================================
echo.

endlocal
