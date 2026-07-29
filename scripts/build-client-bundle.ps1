<#
Arma dos cosas para distribuir el juego a amigos, sin nunca incluir la ROM (restriccion legal
del proyecto, ver README.md seccion 8):

1. client-bundle.zip (en la raiz del repo, donde CLIENT_BUNDLE_PATH del servidor lo espera por
   defecto): lo que el Launcher (client-engine/Launcher) descarga y auto-actualiza. Adentro:
   ClientApp.exe self-contained (win-x64) + memory-maps/ + data/pokemon/ + los sprites de
   batalla ya extraidos de pokeemerald-master -- exactamente lo que ClientApp.exe necesita al
   lado suyo para que FindRepoRoot() (ver Program.cs) lo encuentre, sin el resto del repo
   (server, Postgres local, el checkout completo de pokeemerald-master con su codigo fuente,
   etc.) que un jugador final no necesita para nada.

2. bin\publish-launcher-win-x64\ (dentro de client-engine\Launcher): el Launcher.exe en si,
   self-contained. Esto NO se auto-actualiza a si mismo -- se reparte una sola vez a mano (o se
   vuelve a correr este script si el Launcher mismo cambia), y es el que despues se encarga de
   bajar client-bundle.zip cada vez que corre.

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-client-bundle.ps1
#>
$RepoRoot = Split-Path -Parent $PSScriptRoot
$Version = (Get-Content (Join-Path $RepoRoot "VERSION") -Raw).Trim()
Write-Output "Version del bundle: $Version"

# --- 1. Publicar ClientApp (mismo comando que publish-client.ps1) ---
$ClientAppDir = Join-Path $RepoRoot "client-engine\ClientApp"
$ClientOutDir = Join-Path $ClientAppDir "bin\publish-win-x64"
Write-Output "Publicando ClientApp (Release, win-x64, self-contained)..."
Push-Location $ClientAppDir
try {
    dotnet publish -c Release -r win-x64 --self-contained true -o "bin\publish-win-x64"
    if ($LASTEXITCODE -ne 0) { Write-Error "dotnet publish de ClientApp fallo"; exit $LASTEXITCODE }
} finally {
    Pop-Location
}

# --- 2. Armar la carpeta de staging con la MISMA estructura relativa que el repo (para que
#        FindRepoRoot en Program.cs encuentre memory-maps/ y data/pokemon/ como hermanos) ---
$Staging = Join-Path $RepoRoot "bin\client-bundle-staging"
if (Test-Path $Staging) { Remove-Item $Staging -Recurse -Force }
New-Item -ItemType Directory -Path $Staging | Out-Null

Write-Output "Copiando ClientApp publicado..."
$StagingClientAppOut = Join-Path $Staging "client-engine\ClientApp\bin\publish-win-x64"
New-Item -ItemType Directory -Path $StagingClientAppOut -Force | Out-Null
Copy-Item (Join-Path $ClientOutDir "*") $StagingClientAppOut -Recurse -Force

Write-Output "Copiando memory-maps/..."
Copy-Item (Join-Path $RepoRoot "memory-maps") (Join-Path $Staging "memory-maps") -Recurse -Force

Write-Output "Copiando data/pokemon/..."
New-Item -ItemType Directory -Path (Join-Path $Staging "data") -Force | Out-Null
Copy-Item (Join-Path $RepoRoot "data\pokemon") (Join-Path $Staging "data\pokemon") -Recurse -Force

Write-Output "Copiando sprites de batalla..."
$SpritesSrc = Join-Path $RepoRoot "Pokemon Esmeralda\pokeemerald-master\pokeemerald-master\graphics\pokemon"
if (Test-Path $SpritesSrc) {
    $SpritesDst = Join-Path $Staging "Pokemon Esmeralda\pokeemerald-master\pokeemerald-master\graphics\pokemon"
    New-Item -ItemType Directory -Path (Split-Path -Parent $SpritesDst) -Force | Out-Null
    Copy-Item $SpritesSrc $SpritesDst -Recurse -Force
} else {
    Write-Output "AVISO: no se encontro $SpritesSrc -- el bundle va sin sprites de batalla (BattleScreen no va a mostrar imagenes)."
}

# El .exe necesita poder escribir su propio VERSION (usa el mismo mecanismo del server para
# leerlo si algun dia el cliente reporta version) -- no hace falta acá, el Launcher es quien
# trackea la versión instalada, no ClientApp.

# --- 3. Zippear ---
$ZipPath = Join-Path $RepoRoot "client-bundle.zip"
if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }
Write-Output "Comprimiendo a $ZipPath ..."
Compress-Archive -Path (Join-Path $Staging "*") -DestinationPath $ZipPath
Remove-Item $Staging -Recurse -Force

# --- 4. Publicar el Launcher (self-contained, distribucion manual/unica) ---
$LauncherDir = Join-Path $RepoRoot "client-engine\Launcher"
$LauncherOutDir = Join-Path $LauncherDir "bin\publish-win-x64"
Write-Output ""
Write-Output "Publicando Launcher (Release, win-x64, self-contained)..."
Push-Location $LauncherDir
try {
    dotnet publish -c Release -r win-x64 --self-contained true -o "bin\publish-win-x64"
    if ($LASTEXITCODE -ne 0) { Write-Error "dotnet publish del Launcher fallo"; exit $LASTEXITCODE }
} finally {
    Pop-Location
}

Write-Output ""
Write-Output "Listo:"
Write-Output "  - $ZipPath  (version $Version) -- dejalo donde CLIENT_BUNDLE_PATH del servidor apunte"
Write-Output "    (por defecto ../client-bundle.zip relativo a donde corre server.exe, o sea la raiz"
Write-Output "    del repo si server.exe corre con cwd=server/, que es como corre hoy)."
Write-Output "  - $LauncherOutDir -- repartir esta carpeta UNA VEZ a cada amigo (Launcher.exe +"
Write-Output "    launcher-config.json editable con la IP/dominio real del servidor). El Launcher"
Write-Output "    se encarga de bajar/actualizar client-bundle.zip solo de ahi en adelante."
