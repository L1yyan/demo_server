param(
    [string]$ConfigPath = (Join-Path (Resolve-Path (Join-Path $PSScriptRoot "..")).Path "config\config.windows.yaml"),
    [switch]$NoBuildCheck
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$BinDir = Join-Path $RootDir "bin\windows"
$ResolvedConfigPath = (Resolve-Path $ConfigPath).Path

if (-not $NoBuildCheck) {
    $RequiredFiles = @(
        (Join-Path $BinDir "logicserver.exe"),
        (Join-Path $BinDir "matchserver.exe"),
        (Join-Path $BinDir "roomserver.exe"),
        (Join-Path $BinDir "physx_bridge.dll")
    )
    foreach ($Path in $RequiredFiles) {
        if (-not (Test-Path $Path)) {
            throw "missing runtime file: $Path. Run scripts\build_all_windows.ps1 first."
        }
    }
}

$env:DEMO_SERVER_CONFIG = $ResolvedConfigPath
Set-Location $RootDir

$Services = @(
    @{ Name = "matchserver"; File = Join-Path $BinDir "matchserver.exe" },
    @{ Name = "logicserver"; File = Join-Path $BinDir "logicserver.exe" },
    @{ Name = "roomserver"; File = Join-Path $BinDir "roomserver.exe" }
)

foreach ($Service in $Services) {
    Write-Host "[start] $($Service.Name): $($Service.File)"
    Start-Process -FilePath $Service.File -WorkingDirectory $RootDir -WindowStyle Normal
    Start-Sleep -Seconds 1
}

Write-Host "[start] config: $env:DEMO_SERVER_CONFIG"
Write-Host "[start] open firewall if external clients connect: TCP 8080, TCP 8090, UDP 9001"
