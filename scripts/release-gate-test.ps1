[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$release = Join-Path $PSScriptRoot 'release.ps1'
$source = [IO.File]::ReadAllText($release)
foreach ($required in @(
        "Invoke-ReleaseCheck 'Go tests'",
        "Invoke-ReleaseCheck 'Go vet'",
        "Invoke-ReleaseCheck 'TypeScript tests'",
        "Invoke-ReleaseCheck 'TypeScript check'",
        '[switch]$VerifyOnly'
    )) {
    if (-not $source.Contains($required)) {
        throw "release.ps1 is missing the required gate: $required"
    }
}

# Exercise the actual failure path with a disposable command. VerifyOnly must
# stop at the failing Go gate and must not mutate VERSION or publish anything.
$temporary = Join-Path ([IO.Path]::GetTempPath()) ("educoder-release-gate-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    $failGo = Join-Path $temporary 'go-fails.ps1'
    $passNpm = Join-Path $temporary 'npm-passes.ps1'
    Set-Content -LiteralPath $failGo -Value "exit 42" -Encoding UTF8
    Set-Content -LiteralPath $passNpm -Value "exit 0" -Encoding UTF8

    $beforeVersion = [IO.File]::ReadAllText((Join-Path $root 'VERSION'))
    $pwshCommand = Get-Command pwsh -ErrorAction SilentlyContinue
    if ($null -eq $pwshCommand) { $pwshCommand = Get-Command powershell -ErrorAction Stop }
    $pwsh = $pwshCommand.Source
    & $pwsh -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $release `
        -VerifyOnly -RepositoryPath $root -GoExecutable $failGo -NpmExecutable $passNpm
    $exitCode = $LASTEXITCODE
    if ($exitCode -eq 0) {
        throw 'release.ps1 accepted a deliberately failing Go gate.'
    }
    $afterVersion = [IO.File]::ReadAllText((Join-Path $root 'VERSION'))
    if ($afterVersion -ne $beforeVersion) {
        throw 'A failed gate mutated VERSION.'
    }
    Write-Output "RELEASE_GATE_FAILURE=REJECTED; EXIT=$exitCode"
}
finally {
    Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
}
