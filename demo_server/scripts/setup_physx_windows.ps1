param(
    [string]$PhysXRepo = $(if ($env:PHYSX_REPO) { $env:PHYSX_REPO } else { "https://github.com/NVIDIA-Omniverse/PhysX.git" }),
    [string]$PhysXRef = $(if ($env:PHYSX_REF) { $env:PHYSX_REF } else { "main" }),
    [string]$Preset = $(if ($env:PHYSX_PRESET) { $env:PHYSX_PRESET } else { "vc17win64-cpu-only" }),
    [ValidateSet("debug", "checked", "profile", "release")]
    [string]$BuildType = $(if ($env:BUILD_TYPE) { $env:BUILD_TYPE } else { "checked" }),
    [switch]$Update
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$ThirdPartyDir = Join-Path $RootDir "third_party"
$PhysXSourceDir = Join-Path $ThirdPartyDir "PhysX"
$PhysXSDKDir = Join-Path $ThirdPartyDir "physx-sdk"

function Require-Command {
    param(
        [string]$Name,
        [string]$Hint
    )
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "missing command: $Name. $Hint"
    }
}

function Copy-DirectoryContents {
    param(
        [string]$Source,
        [string]$Destination
    )
    if (-not (Test-Path $Source)) {
        throw "missing source directory: $Source"
    }
    Remove-Item -Path $Destination -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $Destination | Out-Null
    Copy-Item -Path (Join-Path $Source "*") -Destination $Destination -Recurse -Force
}

function Get-PhysXPlatformName {
    param([string]$SourceRoot)
    $Pattern = Join-Path $SourceRoot "physx\bin\win.x86_64.*"
    $Dirs = @(Get-ChildItem -Path $Pattern -Directory -ErrorAction SilentlyContinue | Sort-Object Name)
    if ($Dirs.Count -eq 0) {
        throw "no Windows PhysX output directory found under: $(Join-Path $SourceRoot 'physx\bin')"
    }
    return $Dirs[0].Name
}

Require-Command "git" "Install Git for Windows."
Require-Command "cmake" "Install CMake and make sure it is in PATH."

New-Item -ItemType Directory -Path $ThirdPartyDir -Force | Out-Null

if (Test-Path (Join-Path $PhysXSourceDir ".git")) {
    if ($Update) {
        Write-Host "[physx] updating PhysX source: $PhysXSourceDir"
        & git -C $PhysXSourceDir fetch --depth 1 origin $PhysXRef
        if ($LASTEXITCODE -ne 0) { throw "git fetch failed: $LASTEXITCODE" }
        & git -C $PhysXSourceDir checkout FETCH_HEAD
        if ($LASTEXITCODE -ne 0) { throw "git checkout failed: $LASTEXITCODE" }
    } else {
        Write-Host "[physx] using existing PhysX source: $PhysXSourceDir"
    }
} else {
    Write-Host "[physx] cloning PhysX: $PhysXRepo ($PhysXRef)"
    Remove-Item -Path $PhysXSourceDir -Recurse -Force -ErrorAction SilentlyContinue
    & git clone --depth 1 --branch $PhysXRef $PhysXRepo $PhysXSourceDir
    if ($LASTEXITCODE -ne 0) { throw "git clone failed: $LASTEXITCODE" }
}

$GenerateProjects = Join-Path $PhysXSourceDir "physx\generate_projects.bat"
if (-not (Test-Path $GenerateProjects)) {
    throw "missing PhysX generate_projects.bat: $GenerateProjects"
}

Write-Host "[physx] generating Windows project: $Preset"
& $GenerateProjects $Preset
if ($LASTEXITCODE -ne 0) {
    throw "generate_projects failed: $LASTEXITCODE"
}

$CompilerDir = Join-Path $PhysXSourceDir "physx\compiler\$Preset"
$SolutionPath = Join-Path $CompilerDir "PhysXSDK.sln"
if (-not (Test-Path $SolutionPath)) {
    throw "missing PhysX solution: $SolutionPath"
}

Write-Host "[physx] building PhysX core libraries: $BuildType"
& cmake --build $CompilerDir --config $BuildType --parallel --target PhysX PhysXExtensions PhysXPvdSDK PhysXCommon PhysXCooking PhysXFoundation
if ($LASTEXITCODE -ne 0) {
    throw "PhysX build failed: $LASTEXITCODE"
}

$PlatformName = Get-PhysXPlatformName -SourceRoot $PhysXSourceDir
$SourceLibDir = Join-Path $PhysXSourceDir "physx\bin\$PlatformName\$BuildType"
if (-not (Test-Path $SourceLibDir)) {
    throw "missing PhysX library directory: $SourceLibDir"
}

$IncludeDir = Join-Path $PhysXSDKDir "include"
$PxSharedIncludeDir = Join-Path $PhysXSDKDir "pxshared\include"
$TargetLibDir = Join-Path $PhysXSDKDir "lib\windows.x86_64\$BuildType"
$TargetBinDir = Join-Path $PhysXSDKDir "bin\windows.x86_64\$BuildType"

Write-Host "[physx] copying headers"
Copy-DirectoryContents -Source (Join-Path $PhysXSourceDir "physx\include") -Destination $IncludeDir
$PxSharedSource = Join-Path $PhysXSourceDir "pxshared\include"
if (Test-Path $PxSharedSource) {
    Copy-DirectoryContents -Source $PxSharedSource -Destination $PxSharedIncludeDir
} else {
    New-Item -ItemType Directory -Path $PxSharedIncludeDir -Force | Out-Null
}

Write-Host "[physx] copying Windows libraries"
Remove-Item -Path $TargetLibDir -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path $TargetBinDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $TargetLibDir -Force | Out-Null
New-Item -ItemType Directory -Path $TargetBinDir -Force | Out-Null

$LibSearchPath = Join-Path $SourceLibDir "*"
$Libs = @(Get-ChildItem -Path $LibSearchPath -File -Include "PhysX*.lib", "PhysX*.pdb" -ErrorAction SilentlyContinue)
$Dlls = @(Get-ChildItem -Path $LibSearchPath -File -Include "PhysX*.dll", "PVDRuntime*.dll" -ErrorAction SilentlyContinue)
if ($Libs.Count -eq 0) {
    throw "no PhysX .lib files found in: $SourceLibDir"
}
foreach ($Item in $Libs) {
    Copy-Item -Path $Item.FullName -Destination $TargetLibDir -Force
}
foreach ($Item in $Dlls) {
    Copy-Item -Path $Item.FullName -Destination $TargetBinDir -Force
}

Write-Host "[physx] SDK prepared: $PhysXSDKDir"
Write-Host "[physx] platform output: $PlatformName"
Write-Host "[physx] config: $BuildType"
