<#
Restaura un backup generado por scripts/backup-database.ps1. PISA la base destino por completo
(dropea y recrea) -- pide confirmacion explicita salvo que se pase -Force.

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-database.ps1 -BackupFile database\backups\pokemon_online_2026-07-28_120000.sql
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-database.ps1 -BackupFile <archivo> -DatabaseName pokemon_online -Force
#>
param(
    [Parameter(Mandatory = $true)][string]$BackupFile,
    [string]$DatabaseName = "pokemon_online",
    [switch]$Force
)

$RepoRoot = Split-Path -Parent $PSScriptRoot
$PsqlExe = Join-Path $RepoRoot "postgresql-16.5\pgsql\bin\psql.exe"

if (-not (Test-Path $PsqlExe)) { Write-Error "No se encontro psql.exe en $PsqlExe"; exit 1 }
if (-not (Test-Path $BackupFile)) { Write-Error "No se encontro el archivo de backup: $BackupFile"; exit 1 }

if (-not $Force) {
    Write-Warning "Esto va a BORRAR '$DatabaseName' por completo y recrearla desde $BackupFile."
    $confirm = Read-Host "Escribi el nombre de la base ('$DatabaseName') para confirmar, o cualquier otra cosa para cancelar"
    if ($confirm -ne $DatabaseName) { Write-Output "Cancelado."; exit 0 }
}

$env:PGPASSWORD = "pokemon"
$PgUser = "pokemon"
$PgHost = "localhost"
$PgPort = "5432"

Write-Output "Cerrando conexiones activas a '$DatabaseName'..."
& $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d postgres -c @"
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$DatabaseName' AND pid <> pg_backend_pid();
"@ | Out-Null

Write-Output "Recreando la base '$DatabaseName'..."
& $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d postgres -c "DROP DATABASE IF EXISTS $DatabaseName;"
if ($LASTEXITCODE -ne 0) { Write-Error "No se pudo dropear '$DatabaseName'."; exit $LASTEXITCODE }
& $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d postgres -c "CREATE DATABASE $DatabaseName OWNER $PgUser;"
if ($LASTEXITCODE -ne 0) { Write-Error "No se pudo crear '$DatabaseName'."; exit $LASTEXITCODE }

Write-Output "Restaurando desde $BackupFile ..."
& $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $DatabaseName -v ON_ERROR_STOP=1 -f $BackupFile
if ($LASTEXITCODE -ne 0) { Write-Error "La restauracion fallo a mitad de camino -- la base puede haber quedado parcialmente cargada."; exit $LASTEXITCODE }

Write-Output "Listo: '$DatabaseName' restaurada desde $BackupFile. Reiniciar server.exe si estaba corriendo contra esta base."
