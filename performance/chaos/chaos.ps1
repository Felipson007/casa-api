[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('start', 'slow-db', 'db-timeout', 'recover', 'cpu-memory', 'stop')]
    [string]$Action,
    [string]$ComposeFile = 'docker-compose.homelab.yml',
    [string]$Project = 'atlas-go'
)

$compose = @('-p', $Project, '-f', $ComposeFile, '-f', 'performance/chaos/docker-compose.chaos.yml')
$api = "http://localhost:8474"
$container = (& docker compose @compose ps -q atlas-api).Trim()

function Invoke-Toxi([string]$Path, [string]$Method = 'Post', $Body = $null) {
    if ($null -eq $Body) { return Invoke-RestMethod -Method $Method -Uri "$api$Path" }
    return Invoke-RestMethod -Method $Method -Uri "$api$Path" -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Compress)
}

switch ($Action) {
    'start' {
        & docker compose @compose up -d
        Invoke-Toxi '/proxies' 'Post' @{ name = 'atlas-db'; listen = '0.0.0.0:5432'; upstream = 'atlas-postgres:5432'; enabled = $true }
        Write-Output 'Chaos stack ready. Run slow-db, db-timeout or cpu-memory, then recover.'
    }
    'slow-db' {
        Invoke-Toxi '/proxies/atlas-db/toxics' 'Post' @{ name = 'latency'; type = 'latency'; stream = 'downstream'; attributes = @{ latency = 1500; jitter = 250 } }
        Write-Output 'Added 1.5s +/- 250ms database latency.'
    }
    'db-timeout' {
        Invoke-Toxi '/proxies/atlas-db/toxics' 'Post' @{ name = 'timeout'; type = 'timeout'; stream = 'downstream'; attributes = @{ timeout = 1000 } }
        Write-Output 'Added 1s database response timeout.'
    }
    'recover' {
        Invoke-Toxi '/proxies/atlas-db/toxics/latency' 'Delete'
        Invoke-Toxi '/proxies/atlas-db/toxics/timeout' 'Delete'
        if ($container) { & docker update --cpus 1.0 --memory 512m $container | Out-Null }
        Write-Output 'Removed database toxics and restored API resource limits.'
    }
    'cpu-memory' {
        if (-not $container) { throw 'atlas-api container is not running; run start first' }
        & docker update --cpus 0.25 --memory 256m $container
        Write-Output 'Constrained API to 0.25 CPU and 256MiB.'
    }
    'stop' { & docker compose @compose down }
}
