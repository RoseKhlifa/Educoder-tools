[CmdletBinding()]
param([ValidateRange(1, 65535)][int]$Port = 8081)

$ErrorActionPreference = 'Stop'
$workspacePath = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$binaryPath = Join-Path $workspacePath 'artifacts\bin\educoder-reuse.exe'
$runtimeDirectory = Join-Path $workspacePath 'resource-reuse\reports\runtime'
$listenerIds = @(Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue |
    Select-Object -ExpandProperty OwningProcess -Unique)

foreach ($listenerId in $listenerIds) {
    $processInfo = Get-CimInstance -ClassName Win32_Process -Filter "ProcessId = $listenerId"
    if ($null -eq $processInfo -or [string]::IsNullOrEmpty($processInfo.ExecutablePath) -or
        -not [string]::Equals([IO.Path]::GetFullPath($processInfo.ExecutablePath), $binaryPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Port $Port belongs to an unverified application (PID $listenerId); it was not stopped."
    }
}
foreach ($listenerId in $listenerIds) {
    # Recheck identity immediately before termination to avoid PID reuse.
    $processInfo = Get-CimInstance -ClassName Win32_Process -Filter "ProcessId = $listenerId"
    if ($null -ne $processInfo -and [string]::Equals($processInfo.ExecutablePath, $binaryPath, [StringComparison]::OrdinalIgnoreCase)) {
        Stop-Process -Id $listenerId -Force
        Wait-Process -Id $listenerId -Timeout 10 -ErrorAction SilentlyContinue
    }
}
foreach ($stateFile in @('reuse.pid', 'reuse-process.json')) {
    $target = Join-Path $runtimeDirectory $stateFile
    if (Test-Path -LiteralPath $target) {
        Remove-Item -LiteralPath $target -Force
    }
}
Write-Output "SERVICE=STOPPED; PORT=$Port"
