# ─────────────────────────────────────────────────────────────────────────────
# install.ps1 — Install Cortex on Windows
#
# Usage:
#   irm https://raw.githubusercontent.com/lleontor705/cortex/main/scripts/install.ps1 | iex
#   # or with specific version:
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/lleontor705/cortex/main/scripts/install.ps1))) -Version "v0.3.0"
# ─────────────────────────────────────────────────────────────────────────────

[CmdletBinding()]
param (
    [string]$Version = "latest",
    [string]$InstallDir = "$env:LOCALAPPDATA\cortex\bin",
    [string]$Repo = "lleontor705/cortex"
)

$ErrorActionPreference = "Stop"

function Write-CortexBanner {
    Write-Host ""
    Write-Host "  ╔═══════════════════════════════════════════════════════════╗" -ForegroundColor Cyan
    Write-Host "  ║              CORTEX INSTALLER (WINDOWS)                   ║" -ForegroundColor Cyan
    Write-Host "  ║  Persistent memory for AI coding agents                   ║" -ForegroundColor DarkCyan
    Write-Host "  ╚═══════════════════════════════════════════════════════════╝" -ForegroundColor Cyan
    Write-Host ""
}

function Write-Info($msg)    { Write-Host "  [INFO]  " -ForegroundColor Cyan -NoNewline; Write-Host $msg }
function Write-Success($msg) { Write-Host "  [OK]    " -ForegroundColor Green -NoNewline; Write-Host $msg }
function Write-Warn($msg)    { Write-Host "  [WARN]  " -ForegroundColor Yellow -NoNewline; Write-Host $msg }
function Write-Failure($msg) { Write-Host "  [ERROR] " -ForegroundColor Red -NoNewline; Write-Host $msg; exit 1 }

Write-CortexBanner

# 1. Detect Architecture
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
    "AMD64" { $cortexArch = "amd64" }
    "ARM64" { $cortexArch = "arm64" }
    default {
        Write-Failure "Unsupported processor architecture: $arch. Please build from source via 'go install'."
    }
}
Write-Info "Detected platform: windows/$cortexArch"

# 2. Resolve Version
if ($Version -eq "latest") {
    Write-Info "Querying latest release from GitHub..."
    try {
        $releaseUri = "https://api.github.com/repos/$Repo/releases/latest"
        $release = Invoke-RestMethod -Uri $releaseUri -Headers @{ "User-Agent" = "cortex-installer" }
        $resolvedTag = $release.tag_name
        if (-not $resolvedTag) {
            throw "Unable to extract release tag."
        }
        $targetVersion = $resolvedTag
    } catch {
        Write-Warn "Could not auto-detect latest release from GitHub API: $_"
        Write-Warn "Falling back to default 'v0.3.0'. You can specify -Version vX.Y.Z manually."
        $targetVersion = "v0.3.0"
    }
} else {
    $targetVersion = $Version
}

$rawVersion = $targetVersion.TrimStart("v")
Write-Info "Installing Cortex $targetVersion..."

# 3. Prepare Paths & Download
$archiveName = "cortex_${rawVersion}_windows_${cortexArch}.zip"
$downloadUrl = "https://github.com/$Repo/releases/download/$targetVersion/$archiveName"

$tempDir = Join-Path $env:TEMP ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

try {
    $tempArchive = Join-Path $tempDir $archiveName
    Write-Info "Downloading $downloadUrl..."
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls13
        Invoke-WebRequest -Uri $downloadUrl -OutFile $tempArchive -UseBasicParsing
    } catch {
        Write-Failure "Failed to download $downloadUrl. Verify releases at https://github.com/$Repo/releases"
    }

    Write-Info "Extracting binary..."
    Expand-Archive -Path $tempArchive -DestinationPath $tempDir -Force

    $binarySource = Join-Path $tempDir "cortex.exe"
    if (-not (Test-Path $binarySource)) {
        # Search recursively in case it extracted into subfolder
        $found = Get-ChildItem -Path $tempDir -Filter "cortex.exe" -Recurse | Select-Object -First 1
        if ($found) {
            $binarySource = $found.FullName
        } else {
            Write-Failure "Executable 'cortex.exe' not found in downloaded package."
        }
    }

    # 4. Install Destination
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }
    $targetBinary = Join-Path $InstallDir "cortex.exe"

    # If running, stop or replace
    Copy-Item -Path $binarySource -Destination $targetBinary -Force
    Write-Success "Installed binary to: $targetBinary"

    # 5. Configure User PATH
    $userPath = [Environment]::GetEnvironmentVariable("PATH", [EnvironmentVariableTarget]::User)
    $pathParts = $userPath -split ";" | Where-Object { $_ -ne "" }
    if ($pathParts -notcontains $InstallDir) {
        Write-Info "Adding $InstallDir to User PATH..."
        $newPath = ($pathParts + $InstallDir) -join ";"
        [Environment]::SetEnvironmentVariable("PATH", $newPath, [EnvironmentVariableTarget]::User)
        $env:PATH = "$env:PATH;$InstallDir"
        Write-Success "User PATH updated successfully."
    } else {
        Write-Info "$InstallDir is already in User PATH."
    }

    Write-Host ""
    Write-Host "  ✨ Cortex installed successfully!" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Quick start:" -ForegroundColor Yellow
    Write-Host "    cortex setup            " -ForegroundColor Cyan -NoNewline; Write-Host "# Interactive AI agent setup"
    Write-Host "    cortex tui              " -ForegroundColor Cyan -NoNewline; Write-Host "# Open interactive terminal dashboard"
    Write-Host "    cortex --help           " -ForegroundColor Cyan -NoNewline; Write-Host "# View all commands"
    Write-Host ""

} finally {
    if (Test-Path $tempDir) {
        Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
