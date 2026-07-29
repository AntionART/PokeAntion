<#
Backup real de pokemon_online (pg_dump, formato plano) a database/backups/ -- no toca
pokemon_online_test (esa es descartable, se recrea con apply-migrations.ps1).

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\backup-database.ps1
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\backup-database.ps1 -DatabaseName pokemon_online -KeepLast 14

Restaurar un backup (CUIDADO: pisa la base destino por completo):
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-database.ps1 -BackupFile database\backups\pokemon_online_2026-07-28_120000.sql

Para correrlo solo, sin depender de que alguien se acuerde: Windows Task Scheduler, ej.
  schtasks /create /tn "PokemonOnline-DB-Backup" /tr "powershell -NoProfile -ExecutionPolicy Bypass -File C:\ruta\al\repo\scripts\backup-database.ps1" /sc daily /st 03:00
(no se crea automaticamente desde aca -- es una accion de sistema que le queda al que hostea el
servidor decidir, no algo para que un script corra solo la primera vez que se lo invoca).
#>
param(
    [string]$DatabaseName = "pokemon_online",
    [int]$KeepLast = 14
)

$RepoRoot = Split-Path -Parent $PSScriptRoot
$PgDumpExe = Join-Path $RepoRoot "postgresql-16.5\pgsql\bin\pg_dump.exe"
$BackupDir = Join-Path $RepoRoot "database\backups"

if (-not (Test-Path $PgDumpExe)) { Write-Error "No se encontro pg_dump.exe en $PgDumpExe"; exit 1 }
if (-not (Test-Path $BackupDir)) { New-Item -ItemType Directory -Path $BackupDir | Out-Null }

$env:PGPASSWORD = "pokemon"
$PgUser = "pokemon"
$PgHost = "localhost"
$PgPort = "5432"

$Stamp = Get-Date -Format "yyyy-MM-dd_HHmmss"
$OutFile = Join-Path $BackupDir "$DatabaseName`_$Stamp.sql"

Write-Output "Volcando '$DatabaseName' a $OutFile ..."
& $PgDumpExe -U $PgUser -h $PgHost -p $PgPort -d $DatabaseName -F p --no-owner --no-privileges -f $OutFile
if ($LASTEXITCODE -ne 0) {
    Write-Error "pg_dump fallo (codigo $LASTEXITCODE)."
    if (Test-Path $OutFile) { Remove-Item $OutFile -Force }
    exit $LASTEXITCODE
}

$SizeKb = [Math]::Round((Get-Item $OutFile).Length / 1KB, 1)
Write-Output "Listo: $OutFile ($SizeKb KB)"

# Retencion: mantener solo los ultimos $KeepLast backups de ESTA base (no borra backups de otras
# bases que compartan la carpeta) -- evita que database/backups/ crezca sin limite si esto corre
# como tarea diaria y nadie lo revisa nunca.
$existing = Get-ChildItem -Path $BackupDir -Filter "$DatabaseName`_*.sql" | Sort-Object Name -Descending
if ($existing.Count -gt $KeepLast) {
    $toDelete = $existing | Select-Object -Skip $KeepLast
    foreach ($old in $toDelete) {
        Write-Output "Borrando backup viejo (fuera de los ultimos $KeepLast): $($old.Name)"
        Remove-Item $old.FullName -Force
    }
}
