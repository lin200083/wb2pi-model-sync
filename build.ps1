$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root
$env:GOPROXY = "https://goproxy.cn,direct"

$goCmd = Get-Command go -ErrorAction SilentlyContinue
if ($goCmd) {
    $go = $goCmd.Source
} else {
    $candidate = Join-Path $env:USERPROFILE ".local\go\bin\go.exe"
    if (-not (Test-Path $candidate)) {
        throw "Go not found. Install Go 1.27+ or add go.exe to PATH."
    }
    $go = $candidate
}

$gopath = (& $go env GOPATH).Trim()
$rsrc = Join-Path $gopath "bin\rsrc.exe"
if (-not (Test-Path $rsrc)) {
    Write-Host "Installing rsrc..."
    & $go install github.com/akavel/rsrc@latest
}

Write-Host "Generating rsrc.syso..."
& $rsrc -manifest app.manifest -ico app.ico -o rsrc.syso

Write-Host "Building GUI exe..."
& $go build -ldflags="-H windowsgui -s -w" -o wb2pi-model-sync.exe .

Write-Host "Building CLI exe..."
& $go build -ldflags="-s -w" -o wb2pi-model-sync-cli.exe .

Write-Host "Done:"
Get-Item wb2pi-model-sync.exe, wb2pi-model-sync-cli.exe | Select-Object Name, Length, LastWriteTime | Format-Table
