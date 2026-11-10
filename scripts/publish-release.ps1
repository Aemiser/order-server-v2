<#
.SYNOPSIS
Publish build/bin/OnePoint.exe as a GitHub Release so the desktop app can auto-update.

.EXAMPLE
$env:GITHUB_TOKEN = 'ghp_xxxxxxxxxxxx'
powershell -ExecutionPolicy Bypass -File .\scripts\publish-release.ps1 -Version 10.0.1

.EXAMPLE
# exe already uploaded, only (re)upload the checksum file:
powershell -ExecutionPolicy Bypass -File .\scripts\publish-release.ps1 -Version 10.0.1 -ChecksumOnly

.NOTES
The token needs "Contents: write" permission on the target repository.
The exe must come from 'wails build': wails.json pins build:tags=wechat, which is
required for the desktop app to keep the WeChat export feature. A plain
'go build' produces a reduced build and must not be published.
Keep this file ASCII-only: Windows PowerShell 5.1 reads .ps1 as ANSI without a BOM.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$Owner = 'Aemiser',
    [string]$Repo = 'order_manager',
    [string]$ExePath = '',
    [string]$Title = '',
    [string]$Notes = '',
    [string]$Token = $env:GITHUB_TOKEN,
    [switch]$ChecksumOnly
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = Split-Path -Parent $PSScriptRoot

if ([string]::IsNullOrWhiteSpace($ExePath)) {
    $ExePath = Join-Path $root 'build\bin\OnePoint.exe'
}
if (-not (Test-Path -LiteralPath $ExePath)) {
    throw ('Executable not found: ' + $ExePath + '. Run "wails build" first.')
}
$exeFile = Get-Item -LiteralPath $ExePath

# Guard: the release tag must match the version compiled into the binary.
$globalFile = Join-Path $root 'go\global\enter.go'
if (Test-Path -LiteralPath $globalFile) {
    $code = Get-Content -LiteralPath $globalFile -Raw -Encoding UTF8
    if ($code -match 'Version\s*=\s*"([^"]+)"') {
        $codeVersion = $Matches[1]
        if ($codeVersion -ne $Version) {
            throw ('Version mismatch: go/global/enter.go says "' + $codeVersion + '" but you are publishing "' + $Version + '". Update the constant and rebuild with "wails build" first.')
        }
    }
}

if ([string]::IsNullOrWhiteSpace($Token)) {
    throw ('Missing GitHub token. Set $env:GITHUB_TOKEN or pass -Token. It needs Contents: write on ' + $Owner + '/' + $Repo + '.')
}

$headers = @{
    'Authorization'        = 'Bearer ' + $Token
    'Accept'               = 'application/vnd.github+json'
    'X-GitHub-Api-Version' = '2022-11-28'
    'User-Agent'           = 'order_server-release-script'
}
$api = 'https://api.github.com/repos/' + $Owner + '/' + $Repo
$escTag = [uri]::EscapeDataString($Version)

function Invoke-GitHubJson {
    param([string]$Method, [string]$Uri, [object]$Body = $null)
    $params = @{
        Method          = $Method
        Uri             = $Uri
        Headers         = $headers
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        # Windows PowerShell 5.1 sends a -Body string using the ANSI codepage,
        # which turns Chinese release notes into '?'. Encode JSON as explicit
        # UTF-8 bytes so the Release body keeps its original text.
        $json = ($Body | ConvertTo-Json -Compress)
        $params['Body'] = [System.Text.Encoding]::UTF8.GetBytes($json)
        $params['ContentType'] = 'application/json; charset=utf-8'
    }
    return Invoke-RestMethod @params
}

# Extract the section matching this version from CHANGELOG.md at repo root,
# and use it as the Release body (the changelog shown in the app UI).
# Match rule: find the heading line starting with "## v<Version>", then take all
# content until the next "## " heading.
function Get-ChangelogNotes {
    param([string]$Version)
    $cl = Join-Path $root 'CHANGELOG.md'
    if (-not (Test-Path -LiteralPath $cl)) { return '' }
    $lines = Get-Content -LiteralPath $cl -Encoding UTF8
    $pattern = '^##\s+v?' + [regex]::Escape($Version) + '\b'
    $start = -1
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match $pattern) { $start = $i; break }
    }
    if ($start -lt 0) { return '' }
    $out = @()
    for ($j = $start + 1; $j -lt $lines.Count; $j++) {
        if ($lines[$j] -match '^##\s+') { break }
        $out += $lines[$j]
    }
    return ($out -join [Environment]::NewLine).Trim()
}

# Resolve the changelog notes once: explicit -Notes wins, otherwise read CHANGELOG.md.
# This also lets a re-run repair notes on an already-published release.
if ([string]::IsNullOrWhiteSpace($Title)) { $Title = 'OnePoint ' + $Version }
if ([string]::IsNullOrWhiteSpace($Notes)) {
    $Notes = Get-ChangelogNotes -Version $Version
}
if ([string]::IsNullOrWhiteSpace($Notes)) { $Notes = 'OnePoint ' + $Version }

# Reuse the release when the tag already exists.
$release = $null
try {
    $release = Invoke-GitHubJson -Method 'Get' -Uri ($api + '/releases/tags/' + $escTag)
    Write-Host ('Existing release found: ' + $release.html_url)
}
catch {
    $statusCode = 0
    if ($_.Exception.Response) { $statusCode = [int]$_.Exception.Response.StatusCode }
    if ($statusCode -ne 404) { throw }
}

if ($null -eq $release) {
    $body = @{
        tag_name         = $Version
        name             = $Title
        body             = $Notes
        draft            = $false
        prerelease       = $false
        target_commitish = 'master'
    }
    $release = Invoke-GitHubJson -Method 'Post' -Uri ($api + '/releases') -Body $body
    Write-Host ('Release created: ' + $release.html_url)
}
else {
    # Keep the stored notes in sync with CHANGELOG.md, so editing the file and
    # re-running the script repairs the release body without re-uploading assets.
    $currentBody = (([string]$release.body) -replace "`r`n", "`n").Trim()
    $wantedBody  = ($Notes -replace "`r`n", "`n").Trim()
    if ($currentBody -ne $wantedBody) {
        Invoke-GitHubJson -Method 'Patch' -Uri ($api + '/releases/' + $release.id) -Body @{ body = $Notes } | Out-Null
        Write-Host 'Release notes updated from CHANGELOG.md.'
    } else {
        Write-Host 'Release notes already up to date.'
    }
}

# GitHub rejects duplicate asset names, so remove the previous one first.
$assetName = $exeFile.Name
$checksumName = $assetName + '.sha256'
$assets = Invoke-GitHubJson -Method 'Get' -Uri ($api + '/releases/' + $release.id + '/assets')
foreach ($a in $assets) {
    $obsolete = ($a.name -eq $checksumName) -or ((-not $ChecksumOnly) -and ($a.name -eq $assetName))
    if ($obsolete) {
        Invoke-RestMethod -Method 'Delete' -Uri ($api + '/releases/assets/' + $a.id) -Headers $headers -UseBasicParsing | Out-Null
        Write-Host ('Removed old asset: ' + $a.name)
    }
}

if (-not $ChecksumOnly) {
    $uploadUri = 'https://uploads.github.com/repos/' + $Owner + '/' + $Repo + '/releases/' + $release.id + '/assets?name=' + [uri]::EscapeDataString($assetName)
    Write-Host ('Uploading ' + $exeFile.FullName + ' (' + [math]::Round($exeFile.Length / 1MB, 2) + ' MB) ...')
    try {
        $uploaded = Invoke-RestMethod -Method 'Post' -Uri $uploadUri -Headers $headers -InFile $exeFile.FullName -ContentType 'application/octet-stream' -UseBasicParsing
    }
    catch {
        throw ('Upload failed: ' + $_.Exception.Message + ' ' + $_.ErrorDetails.Message)
    }
    Write-Host ('Uploaded: ' + $uploaded.browser_download_url)
} else {
    Write-Host 'ChecksumOnly: skipping exe upload.'
}

# Generate and upload a SHA256 checksum so clients can verify downloads.
# certutil (built into Windows) is more reliable than Get-FileHash here.
$raw = & certutil.exe -hashfile $exeFile.FullName SHA256 2>&1
$hash = ''
foreach ($line in $raw) {
    # Loose capture: certutil output may contain CR/blank/ANSI noise around the hash line.
    if ([string]$line -match '([0-9a-fA-F]{64})') {
        $hash = $Matches[1].ToLower()
        break
    }
}
if ($hash -notmatch '^[0-9a-f]{64}$') {
    try {
        $hash = (Get-FileHash -LiteralPath $exeFile.FullName -Algorithm SHA256).Hash.ToLower()
    } catch {
        $hash = ''
    }
}
if ($hash -notmatch '^[0-9a-f]{64}$') {
    throw ('Failed to compute SHA256 for ' + $exeFile.FullName)
}
Write-Host ('SHA256: ' + $hash)
$checksumFile = Join-Path $env:TEMP ($assetName + '.sha256')
Set-Content -LiteralPath $checksumFile -Value ($hash + '  ' + $assetName) -Encoding ASCII -NoNewline
$checksumUri = 'https://uploads.github.com/repos/' + $Owner + '/' + $Repo + '/releases/' + $release.id + '/assets?name=' + [uri]::EscapeDataString($checksumName)
Write-Host ('Uploading checksum ' + $checksumFile + ' (' + $hash + ') ...')
try {
    Invoke-RestMethod -Method 'Post' -Uri $checksumUri -Headers $headers -InFile $checksumFile -ContentType 'text/plain' -UseBasicParsing | Out-Null
    Write-Host ('Uploaded checksum: ' + $checksumName)
}
catch {
    Write-Warning ('Checksum upload failed (non-fatal): ' + $_.Exception.Message)
}
Remove-Item -LiteralPath $checksumFile -Force -ErrorAction SilentlyContinue

$latest = Invoke-GitHubJson -Method 'Get' -Uri ($api + '/releases/latest')
Write-Host ''
Write-Host ('Latest tag : ' + $latest.tag_name)
Write-Host ('Assets     : ' + (($latest.assets | ForEach-Object { $_.name }) -join ', '))
Write-Host ('URL        : ' + $latest.html_url)
if ($latest.tag_name -ne $Version) {
    Write-Warning 'The latest release is not the one just published. Check draft / prerelease flags.'
}
