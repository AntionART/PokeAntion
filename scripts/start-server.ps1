<#
Build and run the Go server using a local extracted Go SDK.
Usage: From repo root: `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-server.ps1`
#>
$RepoRoot = Split-Path -Parent $PSScriptRoot
$ServerDir = Join-Path $RepoRoot "server"
$GoRoot = Join-Path $RepoRoot "go1.26.5\go"
$Env:GOROOT = $GoRoot
$Env:PATH = (Join-Path $GoRoot 'bin') + ";" + $Env:PATH

if (-not (Test-Path (Join-Path $GoRoot 'bin\go.exe'))) {
    Write-Error "go.exe not found under $GoRoot; ensure the Go SDK is extracted there."
    exit 1
}

Push-Location $ServerDir
try {
    Write-Output "Building server..."
    & "$GoRoot\bin\go.exe" build -v -o "server.exe" ./cmd/server
    if ($LASTEXITCODE -ne 0) { Write-Error "go build failed"; exit $LASTEXITCODE }

    Write-Output "Starting server.exe"
    Start-Process -FilePath (Join-Path $ServerDir 'server.exe') -NoNewWindow
} finally {
    Pop-Location
}
