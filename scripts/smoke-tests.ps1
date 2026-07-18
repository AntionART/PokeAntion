param(
    [string]$HttpHost = 'http://localhost:8080',
    [string]$WsUrl = 'ws://localhost:8080/ws'
)

Write-Output "Running smoke tests against $HttpHost and $WsUrl"

# Health check
try {
    $h = Invoke-RestMethod -Uri "$HttpHost/health" -Method Get -TimeoutSec 5
    Write-Output "Health: $h"
} catch {
    Write-Warning "Health check failed: $_"
}

# Register two test users
function Register-User($username, $email, $password, $nickname) {
    $body = @{ username=$username; email=$email; password=$password; rom_id='emerald_us'; nickname=$nickname } | ConvertTo-Json
    try {
        $res = Invoke-RestMethod -Uri "$HttpHost/register" -Method Post -Body $body -ContentType 'application/json'
        Write-Output "Registered $username -> $($res.character_id)"
        return $res
    } catch {
        Write-Warning "Register $username failed: $_"
        return $null
    }
}

Register-User -username 'ash' -email 'ash@example.com' -password 'pikachu123' -nickname 'Ash'
Register-User -username 'misty' -email 'misty@example.com' -password 'squirtle123' -nickname 'Misty'

# Node-based websocket smoke script (requires node + npm install in scripts/)
if (Get-Command node -ErrorAction SilentlyContinue) {
    Push-Location -Path $PSScriptRoot
    if (-not (Test-Path node_modules)) {
        Write-Output "Installing node deps for WS smoke test (run once)"
        npm install
    }
    Write-Output "Running WS smoke script..."
    node ws-smoke.js
    Pop-Location
} else {
    Write-Warning "Node.js not found in PATH - skipping WS script. Install Node 18+ and run scripts/ws-smoke.js manually."
}
