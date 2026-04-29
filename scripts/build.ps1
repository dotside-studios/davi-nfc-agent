#Requires -Version 5.1
<#
.SYNOPSIS
    Build the davi-nfc-agent binary. PowerShell equivalent of build.sh.
.DESCRIPTION
    Compiles the agent for the given target OS / arch (defaults to current).
    Reads BUILD_VERSION, BUILD_COMMIT, BUILD_TIME from env to stamp ldflags;
    falls back to git describe + UTC time. Mirrors build.sh so CI on Windows
    runners and local Windows developers get identical artifacts.

    Cross-compilation from Windows to Linux/macOS is out of scope — that path
    needs additional toolchains (mingw/clang) and is not covered here. Use
    WSL + build.sh if you need it.
.PARAMETER TargetOS
    Target GOOS. Default: current go env GOOS.
.PARAMETER TargetArch
    Target GOARCH. Default: current go env GOARCH.
.EXAMPLE
    ./scripts/build.ps1
    ./scripts/build.ps1 windows amd64
    ./scripts/build.ps1 -TargetOS windows -TargetArch arm64
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$TargetOS,

    [Parameter(Position = 1)]
    [string]$TargetArch
)

$ErrorActionPreference = 'Stop'

if (-not $TargetOS)   { $TargetOS = (go env GOOS) }
if (-not $TargetArch) { $TargetArch = (go env GOARCH) }

# Build info, with env overrides for CI parity with build.sh.
$BuildVersion = if ($env:BUILD_VERSION) { $env:BUILD_VERSION } else { 'dev' }
$BuildCommit  = if ($env:BUILD_COMMIT) {
    $env:BUILD_COMMIT
} else {
    try {
        $sha = (git rev-parse --short HEAD 2>$null)
        if ($LASTEXITCODE -eq 0 -and $sha) { $sha.Trim() } else { 'unknown' }
    } catch {
        'unknown'
    }
}
$BuildTime = if ($env:BUILD_TIME) { $env:BUILD_TIME } else { [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ') }

$Pkg = 'github.com/dotside-studios/davi-nfc-agent/buildinfo'
$LdFlags = "-X $Pkg.Version=$BuildVersion -X $Pkg.Commit=$BuildCommit -X $Pkg.BuildTime=$BuildTime"

if ($TargetOS -eq 'windows') {
    $BinaryName = "davi-nfc-agent-$TargetOS-$TargetArch.exe"
} else {
    $BinaryName = "davi-nfc-agent-$TargetOS-$TargetArch"
}

Write-Host "=== Building $BinaryName ==="
Write-Host "  Version: $BuildVersion"
Write-Host "  Commit: $BuildCommit"
Write-Host "  Build Time: $BuildTime"

$CurrentOS   = (go env GOOS)
$CurrentArch = (go env GOARCH)

# Save env so we don't leak GOOS/GOARCH back to the caller's session.
$prevGOOS         = $env:GOOS
$prevGOARCH       = $env:GOARCH
$prevCGO          = $env:CGO_ENABLED
$prevCC           = $env:CC
$prevCXX          = $env:CXX
$prevPKGCONFIG    = $env:PKG_CONFIG_PATH
$prevCGOLDFLAGS   = $env:CGO_LDFLAGS

try {
    if ($TargetOS -ne $CurrentOS -or $TargetArch -ne $CurrentArch) {
        Write-Host "  Cross-compiling from $CurrentOS/$CurrentArch to $TargetOS/$TargetArch"
        $env:CGO_ENABLED = '1'

        if ($CurrentOS -ne 'windows' -and ($TargetOS -eq 'linux' -or $TargetOS -eq 'darwin')) {
            # Honor the build.sh cross-compile knobs when this script is invoked
            # from a non-Windows host (e.g. CI calls build.sh which now also has
            # a PowerShell sibling for Windows devs).
            if ($TargetOS -eq 'linux' -and $TargetArch -eq 'arm64') {
                $libPath = '/usr/lib/aarch64-linux-gnu'
                $env:PKG_CONFIG_PATH = "$libPath/pkgconfig"
                $env:CGO_LDFLAGS     = "-L$libPath"
                Write-Host "  Library path: $libPath"
            }
            if ($TargetOS -eq 'darwin' -and $CurrentOS -eq 'darwin') {
                if ($TargetArch -eq 'arm64')  { $env:CC = 'clang -arch arm64';  $env:CXX = 'clang++ -arch arm64' }
                if ($TargetArch -eq 'amd64')  { $env:CC = 'clang -arch x86_64'; $env:CXX = 'clang++ -arch x86_64' }
                Write-Host "  Using native clang for macOS cross-compilation"
            }
        } elseif ($CurrentOS -eq 'windows' -and $TargetOS -ne 'windows') {
            Write-Warning "Cross-compiling from Windows to $TargetOS is not supported by build.ps1. Use WSL + scripts/build.sh."
        }

        if ($env:CC) { Write-Host "  Using CC: $($env:CC)" }
    }

    $env:GOOS   = $TargetOS
    $env:GOARCH = $TargetArch

    & go build "-ldflags=$LdFlags" -o $BinaryName .
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed (exit $LASTEXITCODE)"
    }
}
finally {
    $env:GOOS            = $prevGOOS
    $env:GOARCH          = $prevGOARCH
    $env:CGO_ENABLED     = $prevCGO
    $env:CC              = $prevCC
    $env:CXX             = $prevCXX
    $env:PKG_CONFIG_PATH = $prevPKGCONFIG
    $env:CGO_LDFLAGS     = $prevCGOLDFLAGS
}

Write-Host "Built: $BinaryName"
Get-Item $BinaryName | Format-List Name, Length, LastWriteTime
