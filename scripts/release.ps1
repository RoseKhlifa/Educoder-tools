[CmdletBinding()]
param([Parameter(Mandatory)][string]$Message)

$ErrorActionPreference = 'Stop'
$repositoryPath = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location -LiteralPath $repositoryPath
try {
    $remote = (& git remote get-url origin).Trim()
    if ($LASTEXITCODE -ne 0 -or $remote -ne 'git@github.com:RoseKhlifa/Educoder-tools.git') {
        throw 'origin must use the approved SSH repository.'
    }
    $branch = (& git branch --show-current).Trim()
    if ($branch -ne 'main') { throw 'Release from main after integrating your changes.' }
    if (-not (& git status --porcelain)) { throw 'No changes to release.' }
    $current = (Get-Content -LiteralPath 'VERSION' -Raw).Trim()
    if ($current -notmatch '^(\d+)\.(\d+)\.(\d+)$') { throw 'VERSION must be MAJOR.MINOR.PATCH.' }
    $next = '{0}.{1}.{2}' -f $Matches[1], $Matches[2], ([int]$Matches[3] + 1)
    & git rev-parse --verify "refs/tags/v$current" *> $null
    if ($LASTEXITCODE -ne 0) { throw 'Current version must already have a release tag.' }
    & git fetch origin main --tags
    if ($LASTEXITCODE -ne 0) { throw 'SSH fetch failed.' }
    $head = (& git rev-parse HEAD).Trim()
    $remoteHead = (& git rev-parse origin/main).Trim()
    if ($head -ne $remoteHead) { throw 'Sync main with origin/main before creating a release.' }

    [IO.File]::WriteAllText((Join-Path $repositoryPath 'VERSION'), "$next`n")
    foreach ($packageFile in @('package.json', 'package-lock.json')) {
        $fullPath = Join-Path $repositoryPath $packageFile
        $source = [IO.File]::ReadAllText($fullPath)
        # Only the root package version has the previous application version.
        $source = $source.Replace(('"version": "' + $current + '"'), ('"version": "' + $next + '"'))
        [IO.File]::WriteAllText($fullPath, $source)
    }
    $changelogPath = Join-Path $repositoryPath 'CHANGELOG.md'
    $changelog = [IO.File]::ReadAllText($changelogPath)
    $date = [TimeZoneInfo]::ConvertTimeBySystemTimeZoneId([DateTime]::UtcNow, 'China Standard Time').ToString('yyyy-MM-dd')
    $entry = "# Changelog`n`n## $next — $date`n`n- $Message`n"
    [IO.File]::WriteAllText($changelogPath, $changelog.Replace('# Changelog', $entry.TrimEnd()))

    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed; release not committed.' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed; release not committed.' }
    & npm test
    if ($LASTEXITCODE -ne 0) { throw 'TypeScript tests failed; release not committed.' }
    & npm run typecheck
    if ($LASTEXITCODE -ne 0) { throw 'TypeScript check failed; release not committed.' }
    & git add --all
    if ($LASTEXITCODE -ne 0) { throw 'Git staging failed.' }
    & python scripts/check-private-data.py
    if ($LASTEXITCODE -ne 0) { throw 'Privacy audit failed; release not committed or pushed.' }
    & git diff --cached --check
    if ($LASTEXITCODE -ne 0) { throw 'Staged whitespace check failed.' }
    & git commit -m "chore(release): v$next $Message"
    if ($LASTEXITCODE -ne 0) { throw 'Commit failed.' }
    & git tag -a "v$next" -m "Release v$next"
    if ($LASTEXITCODE -ne 0) { throw 'Tag creation failed.' }
    & git push --atomic origin main "v$next"
    if ($LASTEXITCODE -ne 0) { throw 'SSH push failed; local commit/tag preserved for retry.' }
    Write-Output "RELEASE=v$next; COMMIT_AND_SSH_PUSH=PASS"
} finally {
    Pop-Location
}
