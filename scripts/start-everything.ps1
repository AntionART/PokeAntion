<#
Levanta Postgres (si no esta corriendo) y el servidor Go, en un solo paso -- para no tener que
acordarse de los comandos sueltos cada vez que abris el proyecto en una maquina nueva (o
distinta). El Launcher/cliente se abren aparte (doble click a client-engine\Launcher, o
`dotnet run` -- ver RESTAURAR-PROYECTO.md).

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-everything.ps1
#>
$RepoRoot = Split-Path -Parent $PSScriptRoot
$DataDir = Join-Path $RepoRoot "postgres_data"
$PgCtl = Join-Path $RepoRoot "postgresql-16.5\pgsql\bin\pg_ctl.exe"
$PgLog = Join-Path $RepoRoot "postgresql.log"
$GoExe = Join-Path $RepoRoot "go1.26.5\go\bin\go.exe"
$ServerDir = Join-Path $RepoRoot "server"

if (-not (Test-Path $DataDir)) {
    Write-Output "postgres_data\ no existe todavia -- corriendo initdb (base nueva, vacia)..."
    & (Join-Path $RepoRoot "scripts\start-postgres.ps1")
} else {
    $running = Get-Process -Name postgres -ErrorAction SilentlyContinue
    if ($running) {
        Write-Output "Postgres ya esta corriendo."
    } else {
        Write-Output "Iniciando Postgres..."
        & $PgCtl start -D $DataDir -l $PgLog -w
        if ($LASTEXITCODE -ne 0) { Write-Error "Postgres no arranco -- revisa $PgLog"; exit 1 }
    }
}

Write-Output "Iniciando servidor Go (Ctrl+C para cortarlo)..."
Push-Location $ServerDir
try {
    & $GoExe run ./cmd/server
} finally {
    Pop-Location
}
