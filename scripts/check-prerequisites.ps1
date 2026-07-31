<#
Chequea que la maquina donde corres esto tenga todo lo necesario para levantar el proyecto.
Go y Postgres NO se chequean como "instalados en el sistema" -- son portables y viven adentro
del propio proyecto (go1.26.5/, postgresql-16.5/). Si veniste de un `git clone` y todavia no
estan, no es un error de tu copia: no viajan por git (ver .gitignore) -- corre
`scripts\setup.ps1` para descargarlos y armar la base de datos antes de re-chequear esto.

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\check-prerequisites.ps1
#>
$RepoRoot = Split-Path -Parent $PSScriptRoot
$AllOk = $true

Write-Output "Revisando requisitos en: $RepoRoot"
Write-Output ""

# --- .NET SDK (unico requisito real que no viene incluido en el proyecto) ---
$dotnetCmd = Get-Command dotnet -ErrorAction SilentlyContinue
if ($dotnetCmd) {
    $dotnetVersion = & dotnet --version 2>$null
    if ($dotnetVersion -and $dotnetVersion.StartsWith("10.")) {
        Write-Output "[OK] .NET SDK $dotnetVersion encontrado."
    } else {
        Write-Output "[AVISO] .NET SDK encontrado ($dotnetVersion) pero no es la version 10.x -- el cliente/launcher puede no compilar."
        $AllOk = $false
    }
} else {
    Write-Output "[FALTA] .NET 10 SDK no esta instalado."
    Write-Output "         Descargalo de https://dotnet.microsoft.com/download (elegi '.NET 10 SDK', no el Runtime solo)."
    $AllOk = $false
}

# --- Go portable (deberia venir siempre adentro del proyecto) ---
$goExe = Join-Path $RepoRoot "go1.26.5\go\bin\go.exe"
if (Test-Path $goExe) {
    Write-Output "[OK] Go portable encontrado en go1.26.5\go\bin\go.exe."
} else {
    Write-Output "[FALTA] No esta go1.26.5\go\bin\go.exe -- si copiaste la carpeta completa, revisa que se haya copiado bien; si veniste de un git clone, corre scripts\setup.ps1 para descargarlo."
    $AllOk = $false
}

# --- Postgres portable (deberia venir siempre adentro del proyecto) ---
$pgCtlExe = Join-Path $RepoRoot "postgresql-16.5\pgsql\bin\pg_ctl.exe"
if (Test-Path $pgCtlExe) {
    Write-Output "[OK] Postgres portable encontrado en postgresql-16.5\pgsql\bin\."
} else {
    Write-Output "[FALTA] No esta postgresql-16.5\pgsql\bin\pg_ctl.exe -- si copiaste la carpeta completa, revisa que se haya copiado bien; si veniste de un git clone, corre scripts\setup.ps1 para descargarlo."
    $AllOk = $false
}

# --- Datos reales de la base (si falta, no es un error fatal: se puede restaurar del dump SQL) ---
$dataDir = Join-Path $RepoRoot "postgres_data"
if (Test-Path (Join-Path $dataDir "PG_VERSION")) {
    Write-Output "[OK] postgres_data\ tiene una base de datos real (tus personajes/cuentas)."
} else {
    Write-Output "[AVISO] postgres_data\ no tiene datos todavia -- esta bien si es la primera vez, o restaura desde database\backups\*.sql (ver RESTAURAR-PROYECTO.md)."
}

# --- ROM (nunca se incluye/distribuye -- el usuario la pone) ---
$romsEncontradas = Get-ChildItem -Path $RepoRoot -Filter "*.gba" -File -ErrorAction SilentlyContinue
if ($romsEncontradas.Count -gt 0) {
    Write-Output "[OK] $($romsEncontradas.Count) ROM(s) .gba encontrada(s) en la raiz del proyecto."
} else {
    Write-Output "[AVISO] No hay ninguna ROM .gba en la raiz -- el Launcher te va a pedir seleccionar una la primera vez que lo abras."
}

Write-Output ""
if ($AllOk) {
    Write-Output "Todo listo. Segui los pasos de RESTAURAR-PROYECTO.md para levantar el proyecto."
} else {
    Write-Output "Faltan cosas (ver [FALTA] arriba) -- resolvelas antes de continuar."
}
