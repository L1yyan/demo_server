param(
    [ValidateSet("debug", "checked", "profile", "release")]
    [string]$BuildType = $(if ($env:BUILD_TYPE) { $env:BUILD_TYPE } else { "checked" }),
    [string]$Generator = $(if ($env:CMAKE_GENERATOR) { $env:CMAKE_GENERATOR } else { "Visual Studio 17 2022" })
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$PhysXSDKDir = Join-Path $RootDir "third_party\physx-sdk"
$PhysXLibDir = Join-Path $PhysXSDKDir "lib\windows.x86_64\$BuildType"
$PhysXBinDir = Join-Path $PhysXSDKDir "bin\windows.x86_64\$BuildType"
$BuildDir = Join-Path $RootDir "build\windows\physx_bridge"
$BinDir = Join-Path $RootDir "bin\windows"
$ImportLibDir = Join-Path $BinDir "lib"

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

function Ensure-MinGWImportLib {
    param(
        [string]$DefPath,
        [string]$DllPath,
        [string]$OutputPath
    )
    if (Test-Path $OutputPath) {
        return
    }

    if (-not (Test-Path $DefPath)) {
        throw "missing bridge def file: $DefPath"
    }

    $DllTool = Find-CommandAny -Names @("dlltool", "llvm-dlltool") -Hint "missing dlltool. Install MSYS2 mingw-w64-binutils or LLVM binutils and rerun this script."
    & $DllTool.Path -d $DefPath -l $OutputPath -D (Split-Path $DllPath -Leaf)
    if ($LASTEXITCODE -ne 0) { throw "$($DllTool.Name) failed: $LASTEXITCODE" }
}

Require-Command "cmake" "Install CMake and make sure it is in PATH."

if (-not (Test-Path $PhysXLibDir)) {
    throw "missing Windows PhysX lib directory: $PhysXLibDir. Run scripts\setup_physx_windows.ps1 first."
}
if (-not (Test-Path $PhysXBinDir)) {
    throw "missing Windows PhysX bin directory: $PhysXBinDir. Run scripts\setup_physx_windows.ps1 first."
}

New-Item -ItemType Directory -Path $BuildDir -Force | Out-Null
New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
New-Item -ItemType Directory -Path $ImportLibDir -Force | Out-Null

Write-Host "[bridge] configuring physx_bridge.dll"
& cmake -S (Join-Path $RootDir "src\roomserver\physx") -B $BuildDir -G $Generator -A x64 `
    "-DDEMO_SERVER_ROOT=$RootDir" `
    "-DPHYSX_SDK_DIR=$PhysXSDKDir" `
    "-DPHYSX_CONFIG=$BuildType" `
    "-DPHYSX_LIB_DIR=$PhysXLibDir" `
    "-DPHYSX_BIN_DIR=$PhysXBinDir"
if ($LASTEXITCODE -ne 0) {
    throw "cmake configure failed: $LASTEXITCODE"
}

Write-Host "[bridge] building physx_bridge.dll"
& cmake --build $BuildDir --config $BuildType --parallel --target physx_bridge
if ($LASTEXITCODE -ne 0) {
    throw "physx_bridge build failed: $LASTEXITCODE"
}

$DllPath = Join-Path $BinDir "physx_bridge.dll"
$MSVCImportLib = Join-Path $ImportLibDir "physx_bridge.lib"
if (-not (Test-Path $DllPath)) {
    throw "missing bridge dll after build: $DllPath"
}
if (-not (Test-Path $MSVCImportLib)) {
    $FoundLib = @(Get-ChildItem -Path $BuildDir -Filter "physx_bridge.lib" -Recurse -ErrorAction SilentlyContinue | Sort-Object FullName | Select-Object -First 1)
    if ($FoundLib.Count -gt 0) {
        Copy-Item -Path $FoundLib[0].FullName -Destination $MSVCImportLib -Force
    }
}

$DefPath = Join-Path $RootDir "src\roomserver\physx\physx_bridge.def"
$MinGWImportLib = Join-Path $ImportLibDir "libphysx_bridge.dll.a"
Ensure-MinGWImportLib -DefPath $DefPath -DllPath $DllPath -OutputPath $MinGWImportLib
if (-not (Test-Path $MinGWImportLib)) {
    Write-Warning "missing MinGW import library: $MinGWImportLib"
}

Write-Host "[bridge] output dll: $DllPath"
Write-Host "[bridge] import libs: $ImportLibDir"
