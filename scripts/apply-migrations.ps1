<#
Aplica todas las migraciones pendientes de database/migrations/*.sql contra AMBAS bases
(pokemon_online y pokemon_online_test) usando una tabla de control (schema_migrations) para
saber cuales ya se aplicaron. Reemplaza el proceso manual documentado en README.md seccion 6b,
que ya causo un bug real (olvidarse de aplicar 0007_pokemon_battle_fields.sql a una de las dos
bases hizo que los tests pasaran y el servidor real fallara en silencio).

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\apply-migrations.ps1
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\apply-migrations.ps1 -DatabaseName pokemon_online
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\apply-migrations.ps1 -WhatIf

Sin -DatabaseName, aplica a pokemon_online Y pokemon_online_test (las dos bases reales de este
entorno, ver README.md seccion 6b). -WhatIf lista que se aplicaria sin ejecutar nada.
#>
param(
    [string]$DatabaseName = "",
    [switch]$WhatIf
)

$RepoRoot = Split-Path -Parent $PSScriptRoot
$PsqlExe = Join-Path $RepoRoot "postgresql-16.5\pgsql\bin\psql.exe"
$MigrationsDir = Join-Path $RepoRoot "database\migrations"

if (-not (Test-Path $PsqlExe)) { Write-Error "No se encontro psql.exe en $PsqlExe"; exit 1 }
if (-not (Test-Path $MigrationsDir)) { Write-Error "No se encontro el directorio de migraciones en $MigrationsDir"; exit 1 }

$env:PGPASSWORD = "pokemon"
$PgUser = "pokemon"
$PgHost = "localhost"
$PgPort = "5432"

$databases = if ($DatabaseName) { @($DatabaseName) } else { @("pokemon_online", "pokemon_online_test") }
$migrationFiles = Get-ChildItem -Path $MigrationsDir -Filter "*.sql" | Sort-Object Name

$ensureTrackingTableSql = @"
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename    VARCHAR(255) PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
"@

$exitCode = 0

foreach ($db in $databases) {
    Write-Output ""
    Write-Output "=== Base: $db ==="

    $checkDb = & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d postgres -t -A -c "SELECT 1 FROM pg_database WHERE datname = '$db';" 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Error "No se pudo conectar a Postgres para chequear si '$db' existe: $checkDb"
        $exitCode = 1
        continue
    }
    if (-not ($checkDb -match "1")) {
        Write-Warning "La base '$db' no existe todavia. Creandola (OWNER $PgUser)..."
        if (-not $WhatIf) {
            & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d postgres -c "CREATE DATABASE $db OWNER $PgUser;" | Out-Null
        }
    }

    $trackingTableExists = $false
    if (-not $WhatIf) {
        # 2>$null: CREATE TABLE IF NOT EXISTS manda un NOTICE por stderr en cada corrida
        # posterior a la primera ("la relacion ya existe, omitiendo") -- inofensivo (el exit
        # code sigue siendo 0), pero PowerShell 5.1 lo muestra como si fuera un error real.
        & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -c $ensureTrackingTableSql 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Error "No se pudo crear/verificar schema_migrations en $db"; $exitCode = 1; continue }
        $trackingTableExists = $true
    } else {
        $check = & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -t -A -c "SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migrations';" 2>$null
        $trackingTableExists = ($check -match "1")
    }

    $applied = @{}
    if ($trackingTableExists) {
        $rows = & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -t -A -c "SELECT filename FROM schema_migrations;"
        foreach ($row in $rows) { if ($row.Trim().Length -gt 0) { $applied[$row.Trim()] = $true } }
    }

    # Bootstrap: las 2 bases de este entorno YA tenian las migraciones existentes aplicadas a
    # mano (proceso manual documentado en README.md 6b) desde ANTES de que existiera esta tabla
    # de control. Si schema_migrations esta vacia pero el esquema real ya existe (marcador: la
    # tabla 'accounts'), asumir que todo lo que YA esta en el directorio de migraciones en este
    # momento es una baseline ya aplicada, no volver a correrlo (correr "CREATE TABLE accounts"
    # de nuevo fallaria con "la relacion ya existe"). Cualquier archivo nuevo que se agregue
    # despues de este bootstrap si se aplica normalmente.
    if ($applied.Count -eq 0) {
        $accountsExists = & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -t -A -c "SELECT 1 FROM information_schema.tables WHERE table_name = 'accounts';"
        if ($accountsExists -match "1") {
            if ($WhatIf) {
                Write-Output "  [bootstrap] schema_migrations vacia (o inexistente) pero el esquema ya existe. Se marcarian $($migrationFiles.Count) migraciones existentes como ya aplicadas sin re-ejecutarlas."
                foreach ($file in $migrationFiles) { $applied[$file.Name] = $true }
            } else {
                Write-Output "  [bootstrap] schema_migrations vacia pero el esquema ya existe. Marcando $($migrationFiles.Count) migraciones existentes como ya aplicadas sin re-ejecutarlas."
                foreach ($file in $migrationFiles) {
                    & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -c "INSERT INTO schema_migrations (filename) VALUES ('$($file.Name)') ON CONFLICT DO NOTHING;" | Out-Null
                    $applied[$file.Name] = $true
                }
            }
        }
    }

    foreach ($file in $migrationFiles) {
        if ($applied.ContainsKey($file.Name)) {
            Write-Output "  [ya aplicada] $($file.Name)"
            continue
        }

        if ($WhatIf) {
            Write-Output "  [se aplicaria] $($file.Name)"
            continue
        }

        Write-Output "  [aplicando]    $($file.Name)"
        & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -v ON_ERROR_STOP=1 -f $file.FullName
        if ($LASTEXITCODE -ne 0) {
            Write-Error "  FALLO aplicando $($file.Name) contra $db. Deteniendo esta base (las migraciones posteriores dependen del esquema anterior)."
            $exitCode = 1
            break
        }

        & $PsqlExe -U $PgUser -h $PgHost -p $PgPort -d $db -c "INSERT INTO schema_migrations (filename) VALUES ('$($file.Name)');" | Out-Null
    }
}

Write-Output ""
if ($exitCode -eq 0) {
    Write-Output "Listo. Todas las migraciones pendientes se aplicaron (o ya estaban aplicadas) en: $($databases -join ', ')"
} else {
    Write-Output "Termino con errores. Revisar arriba antes de reiniciar server.exe."
}
exit $exitCode
