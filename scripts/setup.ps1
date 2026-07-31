<#
Bootstrap de un `git clone` limpio: descarga Go y PostgreSQL portables (no viajan por git --
son binarios de terceros, ver .gitignore), crea la base de datos vacia (rol + esquema via
migraciones) y deja todo listo para arrancar el servidor.

Esto NO instala .NET (no se puede automatizar sin permisos de administrador -- instalalo a
mano) ni provee ninguna ROM (nunca se distribuye, ver Antion.md regla #2 -- el Launcher te la
va a pedir la primera vez que lo abras).

Es seguro correrlo mas de una vez: cada paso detecta si ya esta hecho y lo saltea.

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\setup.ps1
#>

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot

Write-Output "=== 1. .NET SDK ==="
$dotnetCmd = Get-Command dotnet -ErrorAction SilentlyContinue
$dotnetOk = $false
if ($dotnetCmd) {
    $dotnetVersion = & dotnet --version 2>$null
    if ($dotnetVersion -and $dotnetVersion.StartsWith("10.")) { $dotnetOk = $true }
}
if ($dotnetOk) {
    Write-Output "[OK] .NET SDK $dotnetVersion encontrado."
} else {
    Write-Warning ".NET 10 SDK no encontrado (o version distinta). Instalalo a mano desde https://dotnet.microsoft.com/download (elegi 'SDK', no el Runtime) -- no se puede automatizar sin privilegios de administrador. El resto de este script sigue igual: no hace falta para levantar el servidor Go."
}

Write-Output ""
Write-Output "=== 2. Go portable ==="
$goDir = Join-Path $RepoRoot "go1.26.5"
$goExe = Join-Path $goDir "go\bin\go.exe"
if (Test-Path $goExe) {
    Write-Output "[OK] Go ya esta en go1.26.5\go\bin\go.exe."
} else {
    $goZip = Join-Path $RepoRoot "go1.26.5.windows-amd64.zip"
    Write-Output "Descargando Go 1.26.5 desde go.dev (~75MB)..."
    Invoke-WebRequest -Uri "https://go.dev/dl/go1.26.5.windows-amd64.zip" -OutFile $goZip
    Write-Output "Extrayendo..."
    if (Test-Path $goDir) { Remove-Item $goDir -Recurse -Force }
    Expand-Archive -Path $goZip -DestinationPath $goDir -Force
    Remove-Item $goZip
    if (-not (Test-Path $goExe)) { throw "La extraccion de Go no dejo go.exe donde se esperaba: $goExe" }
    Write-Output "[OK] Go instalado en $goDir."
}

Write-Output ""
Write-Output "=== 3. PostgreSQL portable ==="
$pgDir = Join-Path $RepoRoot "postgresql-16.5"
$pgCtl = Join-Path $pgDir "pgsql\bin\pg_ctl.exe"
if (Test-Path $pgCtl) {
    Write-Output "[OK] Postgres ya esta en postgresql-16.5\pgsql\bin\."
} else {
    $pgZip = Join-Path $RepoRoot "postgresql-16.5-binaries.zip"
    Write-Output "Descargando PostgreSQL 16.5 portable desde enterprisedb.com (~300MB, puede tardar unos minutos)..."
    Invoke-WebRequest -Uri "https://get.enterprisedb.com/postgresql/postgresql-16.5-1-windows-x64-binaries.zip" -OutFile $pgZip
    Write-Output "Extrayendo (tarda 3-5 minutos por la cantidad de archivos)..."
    if (Test-Path $pgDir) { Remove-Item $pgDir -Recurse -Force }
    Expand-Archive -Path $pgZip -DestinationPath $pgDir -Force
    Remove-Item $pgZip
    if (-not (Test-Path $pgCtl)) { throw "La extraccion de Postgres no dejo pg_ctl.exe donde se esperaba: $pgCtl" }
    Write-Output "[OK] Postgres instalado en $pgDir."
}

Write-Output ""
Write-Output "=== 4. Base de datos ==="
& (Join-Path $RepoRoot "scripts\start-postgres.ps1")
if ($LASTEXITCODE -ne 0) { throw "No se pudo iniciar Postgres." }

$PsqlExe = Join-Path $pgDir "pgsql\bin\psql.exe"
Write-Output "Creando rol 'pokemon' y bases (si no existen)..."
$roleExists = & $PsqlExe -U postgres -h localhost -p 5432 -d postgres -t -A -c "SELECT 1 FROM pg_roles WHERE rolname='pokemon';"
if ($roleExists -notmatch "1") {
    & $PsqlExe -U postgres -h localhost -p 5432 -d postgres -c "CREATE ROLE pokemon WITH LOGIN PASSWORD 'pokemon' CREATEDB;" | Out-Null
    Write-Output "  [creado] rol pokemon"
} else {
    Write-Output "  [ya existia] rol pokemon"
}
foreach ($db in @("pokemon_online", "pokemon_online_test")) {
    $dbExists = & $PsqlExe -U postgres -h localhost -p 5432 -d postgres -t -A -c "SELECT 1 FROM pg_database WHERE datname='$db';"
    if ($dbExists -notmatch "1") {
        & $PsqlExe -U postgres -h localhost -p 5432 -d postgres -c "CREATE DATABASE $db OWNER pokemon;" | Out-Null
        Write-Output "  [creada] base $db"
    } else {
        Write-Output "  [ya existia] base $db"
    }
}

Write-Output ""
Write-Output "Aplicando migraciones..."
& (Join-Path $RepoRoot "scripts\apply-migrations.ps1")
if ($LASTEXITCODE -ne 0) { throw "Las migraciones fallaron -- revisar arriba antes de continuar." }

Write-Output ""
Write-Output "=== Listo ==="
Write-Output "Postgres esta corriendo con el esquema aplicado (base vacia, sin cuentas/personajes de otra instalacion)."
Write-Output ""
Write-Output "Proximos pasos:"
Write-Output "  1. Levantar el servidor:"
Write-Output "       cd server; ..\go1.26.5\go\bin\go.exe run ./cmd/server"
Write-Output "     (o: powershell .\scripts\start-everything.ps1 -- ya deja Postgres+servidor corriendo juntos)"
Write-Output "  2. Abrir el Launcher (en otra ventana):"
Write-Output "       cd client-engine\Launcher; dotnet run"
Write-Output "     Te va a pedir seleccionar tu ROM la primera vez (nunca se incluye/distribuye -- la tenes que tener vos)."
Write-Output ""
Write-Output "Ver RESTAURAR-PROYECTO.md para mas detalle o troubleshooting."
