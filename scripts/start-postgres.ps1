<#
Start local PostgreSQL from the extracted binaries in the workspace.
Usage: From repo root: `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-postgres.ps1`
#>
$RepoRoot = Split-Path -Parent $PSScriptRoot
$PgRoot = Join-Path $RepoRoot "postgresql-16.5\pgsql"
$DataDir = Join-Path $RepoRoot "postgres_data"
$LogFile = Join-Path $RepoRoot "postgresql.log"

if (-not (Test-Path $PgRoot)) {
    Write-Error "Postgres runtime not found at $PgRoot"
    exit 1
}

if (-not (Test-Path $DataDir)) {
    Write-Output "Data dir not found — running initdb..."
    $initdb = Join-Path $PgRoot "bin\initdb.exe"
    & $initdb -D $DataDir -L (Join-Path $PgRoot 'share') --auth=trust --username=postgres --encoding=UTF8
    if ($LASTEXITCODE -ne 0) { Write-Error "initdb failed"; exit $LASTEXITCODE }
}

# Use pg_ctl to start
$pgctl = Join-Path $PgRoot "bin\pg_ctl.exe"
if (-not (Test-Path $pgctl)) { Write-Error "pg_ctl not found"; exit 1 }

Write-Output "Starting PostgreSQL (data: $DataDir)"
& $pgctl start -D $DataDir -l $LogFile -w
if ($LASTEXITCODE -ne 0) {
    Write-Error "pg_ctl failed to start PostgreSQL; check $LogFile"
    exit $LASTEXITCODE
}

Write-Output "PostgreSQL started; logs: $LogFile"
