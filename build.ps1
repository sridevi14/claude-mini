# Cross-compile claude-mini release binaries into .\dist for every supported
# platform. Pure-Go (CGO disabled) so a single Windows host builds all targets.
# Upload everything in .\dist as assets on the GitHub Release.
#
# Usage:  powershell -ExecutionPolicy Bypass -File build.ps1
$ErrorActionPreference = "Stop"

$out = "dist"
if (Test-Path $out) { Remove-Item -Recurse -Force $out }
New-Item -ItemType Directory -Path $out | Out-Null

# Asset names follow the convention the installers expect:
# claude-mini-<os>-<arch>[.exe]
$targets = @(
    @{os="linux";   arch="amd64"},
    @{os="linux";   arch="arm64"},
    @{os="darwin";  arch="amd64"},
    @{os="darwin";  arch="arm64"},
    @{os="windows"; arch="amd64"},
    @{os="windows"; arch="arm64"}
)

$env:CGO_ENABLED = "0"
foreach ($t in $targets) {
    $ext = ""
    if ($t.os -eq "windows") { $ext = ".exe" }
    $name = "claude-mini-$($t.os)-$($t.arch)$ext"
    Write-Host "  building $name"
    $env:GOOS = $t.os
    $env:GOARCH = $t.arch
    go build -trimpath -ldflags "-s -w" -o "$out\$name" .
    if ($LASTEXITCODE -ne 0) { throw "build failed for $name" }
}
Remove-Item env:GOOS, env:GOARCH, env:CGO_ENABLED

Write-Host ""
Write-Host "Done. Release assets are in .\$out :"
Get-ChildItem $out | Select-Object -ExpandProperty Name
