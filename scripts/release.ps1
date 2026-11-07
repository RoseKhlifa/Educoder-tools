[CmdletBinding()]
param(
    [string]$Message,
    # The default is the repository containing this script.  A path override
    # keeps the gate testable in a disposable clone without touching a user's
    # working tree or publishing anything.
    [string]$RepositoryPath,
    [string]$GoExecutable = 'go',
    [string]$NpmExecutable = 'npm',
    [string[]]$Paths = @(),
    [switch]$VerifyOnly
)

$ErrorActionPreference = 'Stop'
$repositoryPath = if ([string]::IsNullOrWhiteSpace($RepositoryPath)) {
    [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
} else {
    [IO.Path]::GetFullPath($RepositoryPath)
}
if (-not $VerifyOnly -and [string]::IsNullOrWhiteSpace($Message)) {
    throw 'Message is required for a release.'
}

function Invoke-ReleaseCheck {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$Executable,
        [Parameter(Mandatory)][string[]]$Arguments
    )
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed (exit code $LASTEXITCODE); release not committed."
    }
}

Push-Location -LiteralPath $repositoryPath
try {
    if (-not $VerifyOnly) {
        $remote = (& git remote get-url origin).Trim()
        if ($LASTEXITCODE -ne 0 -or $remote -ne 'git@github.com:RoseKhlifa/Educoder-tools.git') {
            throw 'origin must use the approved SSH repository.'
        }
        $branch = (& git branch --show-current).Trim()
        if ([string]::IsNullOrEmpty($branch)) { throw 'Release from a named branch or an isolated worktree branch.' }
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
    }

    # Run every expensive/semantic check before mutating VERSION, package
    # metadata, or CHANGELOG.  A failing check therefore leaves the checkout
    # untouched and, most importantly, cannot reach commit/tag/push below.
    Invoke-ReleaseCheck 'Go tests' $GoExecutable @('test', './...')
    Invoke-ReleaseCheck 'Go vet' $GoExecutable @('vet', './...')
    Invoke-ReleaseCheck 'TypeScript tests' $NpmExecutable @('test')
    Invoke-ReleaseCheck 'TypeScript check' $NpmExecutable @('run', 'typecheck')

    if ($VerifyOnly) {
        Write-Output 'RELEASE_GATES=PASS; PUBLISH=SKIPPED'
        return
    }

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
    $entry = "# Changelog`n`n## $next - $date`n`n- $Message`n"
    [IO.File]::WriteAllText($changelogPath, $changelog.Replace('# Changelog', $entry.TrimEnd()))

    # Stage only the release metadata, explicitly requested new files, and
    # already-tracked modifications. Never sweep unrelated user artifacts into
    # a published commit.
    $stage = @('VERSION', 'CHANGELOG.md', 'package.json', 'package-lock.json') + $Paths
    $stage += (& git diff --name-only)
    $stage = @($stage | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Sort-Object -Unique)
    & git add -- $stage
    if ($LASTEXITCODE -ne 0) { throw 'Git staging failed.' }
    & python scripts/check-private-data.py
    if ($LASTEXITCODE -ne 0) { throw 'Privacy audit failed; release not committed or pushed.' }
    & git diff --cached --check
    if ($LASTEXITCODE -ne 0) { throw 'Staged whitespace check failed.' }
    & git commit -m "chore(release): v$next $Message"
    if ($LASTEXITCODE -ne 0) { throw 'Commit failed.' }
    & git tag -a "v$next" -m "Release v$next"
    if ($LASTEXITCODE -ne 0) { throw 'Tag creation failed.' }
    & git push --atomic origin 'HEAD:refs/heads/main' "v$next"
    if ($LASTEXITCODE -ne 0) { throw 'SSH push failed; local commit/tag preserved for retry.' }
    Write-Output "RELEASE=v$next; COMMIT_AND_SSH_PUSH=PASS"
} finally {
    Pop-Location
}
