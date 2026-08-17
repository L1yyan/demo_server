param(
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $RootDir

function Require-Command {
    param(
        [string]$Name,
        [string]$Hint
    )
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "missing command: $Name. $Hint"
    }
}

Require-Command "protoc" "Install protoc and make sure it is in PATH."
Require-Command "protoc-gen-go" "Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"
Require-Command "protoc-gen-go-grpc" "Run: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"

$ProtoRoot = Join-Path $RootDir "pb"
$ProtoFiles = @(Get-ChildItem -Path $ProtoRoot -Filter "*.proto" -Recurse | Sort-Object FullName)
if ($ProtoFiles.Count -eq 0) {
    throw "no proto files found: $ProtoRoot"
}

if ($Clean) {
    Remove-Item -Path (Join-Path $RootDir "gen") -Recurse -Force -ErrorAction SilentlyContinue
}

$RelativeProtoFiles = @($ProtoFiles | ForEach-Object {
    $_.FullName.Substring($RootDir.Length + 1).Replace("\", "/")
})

$ProtoArgs = @(
    "--proto_path=.",
    "--go_out=.",
    "--go_opt=module=demo_server",
    "--go-grpc_out=.",
    "--go-grpc_opt=module=demo_server"
)

Write-Host "[proto] generating protobuf and grpc code"
& protoc @ProtoArgs @RelativeProtoFiles
if ($LASTEXITCODE -ne 0) {
    throw "protoc failed: $LASTEXITCODE"
}
Write-Host "[proto] generated successfully"
