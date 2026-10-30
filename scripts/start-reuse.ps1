[CmdletBinding()]
param(
    [string]$Address = '127.0.0.1:8081',
    [ValidateRange(1, 60)][int]$HealthTimeoutSeconds = 25
)

$ErrorActionPreference = 'Stop'
$workspacePath = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$binaryDirectory = Join-Path $workspacePath 'artifacts\bin'
$runtimeDirectory = Join-Path $workspacePath 'resource-reuse\reports\runtime'
$binaryPath = Join-Path $binaryDirectory 'educoder-reuse.exe'
$pidPath = Join-Path $runtimeDirectory 'reuse.pid'
$statePath = Join-Path $runtimeDirectory 'reuse-process.json'
$endpoint = [Uri]('http://' + $Address + '/healthz')
if ($endpoint.Port -lt 1 -or $endpoint.Port -gt 65535) {
    throw 'Address must include a valid TCP port.'
}

New-Item -ItemType Directory -Path $binaryDirectory -Force | Out-Null
New-Item -ItemType Directory -Path $runtimeDirectory -Force | Out-Null

function Get-OwnedProcess([int]$ProcessId) {
    $processInfo = Get-CimInstance -ClassName Win32_Process -Filter "ProcessId = $ProcessId"
    if ($null -eq $processInfo -or [string]::IsNullOrEmpty($processInfo.ExecutablePath)) {
        throw "Listener PID $ProcessId has no verifiable executable path; it was not stopped."
    }
    if (-not [string]::Equals([IO.Path]::GetFullPath($processInfo.ExecutablePath), $binaryPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Port $($endpoint.Port) belongs to another application (PID $ProcessId); it was not stopped."
    }
    return $processInfo
}

# Validate every listener before stopping any process. A stale PID file never
# grants authority to terminate an unrelated process that reused the number.
$listenerIds = @(Get-NetTCPConnection -State Listen -LocalPort $endpoint.Port -ErrorAction SilentlyContinue |
    Select-Object -ExpandProperty OwningProcess -Unique)
foreach ($listenerId in $listenerIds) {
    $null = Get-OwnedProcess -ProcessId $listenerId
}
foreach ($listenerId in $listenerIds) {
    $null = Get-OwnedProcess -ProcessId $listenerId
    Stop-Process -Id $listenerId -Force
    Wait-Process -Id $listenerId -Timeout 10 -ErrorAction SilentlyContinue
}

Push-Location -LiteralPath $workspacePath
try {
    & go build -o $binaryPath ./cmd/reuse
    if ($LASTEXITCODE -ne 0) {
        throw "Go build failed (exit $LASTEXITCODE)."
    }
} finally {
    Pop-Location
}

$logStamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$stdoutPath = Join-Path $runtimeDirectory "reuse-$logStamp.stdout.log"
$stderrPath = Join-Path $runtimeDirectory "reuse-$logStamp.stderr.log"
$previousAddress = $env:REUSE_ADDR
$previousStaticDirectory = $env:REUSE_STATIC_DIR
try {
    $env:REUSE_ADDR = $Address
    $env:REUSE_STATIC_DIR = Join-Path $workspacePath 'resource-reuse\site'
    $serviceProcess = Start-Process -FilePath $binaryPath -WorkingDirectory $workspacePath -WindowStyle Hidden `
        -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru
} finally {
    $env:REUSE_ADDR = $previousAddress
    $env:REUSE_STATIC_DIR = $previousStaticDirectory
}

Set-Content -LiteralPath $pidPath -Value $serviceProcess.Id -Encoding Ascii
[ordered]@{
    pid = $serviceProcess.Id
    executable = $binaryPath
    address = $Address
    startedAt = (Get-Date).ToString('o')
    stdout = $stdoutPath
    stderr = $stderrPath
} | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8

$deadline = [DateTime]::UtcNow.AddSeconds($HealthTimeoutSeconds)
$healthy = $false
while ([DateTime]::UtcNow -lt $deadline) {
    $serviceProcess.Refresh()
    if ($serviceProcess.HasExited) {
        throw "Reuse service exited (code $($serviceProcess.ExitCode)); see $stderrPath"
    }
    try {
        $result = Invoke-RestMethod -Uri $endpoint.AbsoluteUri -Method Get -TimeoutSec 2
        if ($result.status -eq 'ok') {
            $healthy = $true
            break
        }
    } catch {
        # Startup can take a moment while the TCP socket is being bound.
    }
    Start-Sleep -Milliseconds 250
}
if (-not $healthy) {
    throw "Service health check timed out; PID $($serviceProcess.Id), log $stderrPath"
}
Write-Output "SERVICE=RUNNING; PID=$($serviceProcess.Id); URL=http://$Address/"
Write-Output 'HEALTH=200; STATUS=ok'
Write-Output "STDOUT=$stdoutPath"
Write-Output "STDERR=$stderrPath"

