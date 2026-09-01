param(
    [switch]$BuildBridge,
    [switch]$Proto,
    [ValidateSet("debug", "checked", "profile", "release")]
    [string]$BuildType = $(if ($env:BUILD_TYPE) { $env:BUILD_TYPE } else { "checked" })
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$BinDir = Join-Path $RootDir "bin\windows"
$BridgeDll = Join-Path $BinDir "physx_bridge.dll"
$BridgeImportLib = Join-Path $BinDir "lib\libphysx_bridge.dll.a"

function Require-Command {
    param(
        [string]$Name,
        [string]$Hint
    )
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "missing command: $Name. $Hint"
    }
}

function Find-CommandAny {
    param(
        [string[]]$Names,
        [string]$Hint
    )
    foreach ($Name in $Names) {
        $Command = Get-Command $Name -ErrorAction SilentlyContinue
        if ($Command) {
            return $Command
        }
    }
    throw $Hint
}

Require-Command "go" "Install Go and make sure it is in PATH."
$CCompiler = Find-CommandAny -Names @("x86_64-w64-mingw32-gcc", "gcc", "clang") -Hint "missing C compiler for cgo. Install MSYS2 MinGW64 GCC or LLVM clang and make sure it is in PATH."

# 将 CC 转换为 8.3 短路径，避免路径中包含空格（例如 "C:\Program Files"）
# 被 cgo 按空格拆分成 "C:\Program" 导致编译器查找失败
$Fso = New-Object -ComObject Scripting.FileSystemObject
$env:CC = $Fso.GetFile($CCompiler.Path).ShortPath

Set-Location $RootDir
New-Item -ItemType Directory -Path $BinDir -Force | Out-Null

if ($Proto) {
    & (Join-Path $PSScriptRoot "proto_windows.ps1")
}

if ($BuildBridge) {
    & (Join-Path $PSScriptRoot "build_physx_bridge_windows.ps1") -BuildType $BuildType
}

if (-not (Test-Path $BridgeDll)) {
    throw "missing physx_bridge.dll: $BridgeDll. Run scripts\build_physx_bridge_windows.ps1 first or pass -BuildBridge."
}
if (-not (Test-Path $BridgeImportLib)) {
    throw "missing MinGW import lib: $BridgeImportLib. Rerun scripts\build_physx_bridge_windows.ps1 after installing MSYS2 dlltool/gendef."
}

$Services = @(
    @{ Name = "logicserver"; Package = "./src/logicserver/cmd"; Tags = @() },
    @{ Name = "matchserver"; Package = "./src/matchserver/cmd"; Tags = @() },
    @{ Name = "roomserver"; Package = "./src/roomserver/cmd"; Tags = @("-tags", "physx") }
)

$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

foreach ($Service in $Services) {
    $OutputPath = Join-Path $BinDir ($Service.Name + ".exe")
    $Args = @("build", "-trimpath") + $Service.Tags + @("-o", $OutputPath, $Service.Package)
    Write-Host "[build] building $($Service.Name): $($Service.Package)"
    & go @Args
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed for $($Service.Name): $LASTEXITCODE"
    }
    Write-Host "[build] done: $OutputPath"
}

Write-Host "[build] Windows services built: $BinDir"
