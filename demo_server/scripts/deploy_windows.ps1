param(
    [string]$OutputDir = (Join-Path (Resolve-Path (Join-Path $PSScriptRoot "..")).Path "dist\windows\demo_server"),
    [switch]$Build,
    [switch]$BuildBridge,
    [switch]$Proto,
    [ValidateSet("debug", "checked", "profile", "release")]
    [string]$BuildType = $(if ($env:BUILD_TYPE) { $env:BUILD_TYPE } else { "checked" })
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$BinDir = Join-Path $RootDir "bin\windows"
$DeployRoot = $OutputDir
$DeployBinDir = Join-Path $DeployRoot "bin"
$DeployConfigDir = Join-Path $DeployRoot "config"
$DeployMapDir = Join-Path $DeployConfigDir "maps\mfps_arena"

if ($Build -or $BuildBridge -or $Proto) {
    $Args = @()
    if ($Build -or $BuildBridge) { $Args += "-BuildBridge" }
    if ($Proto) { $Args += "-Proto" }
    $Args += @("-BuildType", $BuildType)
    & (Join-Path $PSScriptRoot "build_all_windows.ps1") @Args
}

$RequiredFiles = @(
    (Join-Path $BinDir "logicserver.exe"),
    (Join-Path $BinDir "matchserver.exe"),
    (Join-Path $BinDir "roomserver.exe"),
    (Join-Path $BinDir "physx_bridge.dll"),
    (Join-Path $RootDir "config\config.windows.yaml"),
    (Join-Path $RootDir "config\maps\mfps_arena\collision.json")
)

foreach ($Path in $RequiredFiles) {
    if (-not (Test-Path $Path)) {
        throw "missing deploy input: $Path"
    }
}

Remove-Item -Path $DeployRoot -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $DeployBinDir -Force | Out-Null
New-Item -ItemType Directory -Path $DeployMapDir -Force | Out-Null

Copy-Item -Path (Join-Path $BinDir "*.exe") -Destination $DeployBinDir -Force
Copy-Item -Path (Join-Path $BinDir "*.dll") -Destination $DeployBinDir -Force
Copy-Item -Path (Join-Path $RootDir "config\config.windows.yaml") -Destination (Join-Path $DeployConfigDir "config.windows.yaml") -Force
Copy-Item -Path (Join-Path $RootDir "config\maps\mfps_arena\collision.json") -Destination (Join-Path $DeployMapDir "collision.json") -Force

$StartScript = @'
$ErrorActionPreference = "Stop"
$RootDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$env:DEMO_SERVER_CONFIG = Join-Path $RootDir "config\config.windows.yaml"
Set-Location $RootDir
Start-Process -FilePath (Join-Path $RootDir "bin\matchserver.exe") -WorkingDirectory $RootDir
Start-Sleep -Seconds 1
Start-Process -FilePath (Join-Path $RootDir "bin\logicserver.exe") -WorkingDirectory $RootDir
Start-Sleep -Seconds 1
Start-Process -FilePath (Join-Path $RootDir "bin\roomserver.exe") -WorkingDirectory $RootDir
Write-Host "started demo_server with config: $env:DEMO_SERVER_CONFIG"
'@
Set-Content -Path (Join-Path $DeployRoot "start_windows.ps1") -Value $StartScript -Encoding UTF8

Write-Host "[deploy] output: $DeployRoot"
Write-Host "[deploy] start: powershell -ExecutionPolicy Bypass -File .\start_windows.ps1"
Write-Host "[deploy] open firewall if external clients connect: TCP 8080, TCP 8090, UDP 9001"
