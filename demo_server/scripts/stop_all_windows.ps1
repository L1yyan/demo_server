param(
    [ValidateRange(0, 300)]
    [int]$GracefulTimeoutSeconds = 5,
    [ValidateRange(1, 300)]
    [int]$PortCheckTimeoutSeconds = 5,
    [switch]$Force,
    [switch]$Quiet
)

$ErrorActionPreference = "Stop"

# 源码目录脚本位于 scripts 下，部署包脚本位于部署根目录
$ScriptDir = (Resolve-Path $PSScriptRoot).Path
$ParentDir = [IO.Path]::GetFullPath((Join-Path $ScriptDir ".."))
if ((Split-Path $ScriptDir -Leaf) -ieq "scripts" -and (Test-Path (Join-Path $ParentDir "go.mod"))) {
    $RootDir = $ParentDir
    $BinDir = Join-Path $RootDir "bin\windows"
} else {
    $RootDir = $ScriptDir
    $BinDir = Join-Path $RootDir "bin"
}

$TargetNames = @("logicserver.exe", "matchserver.exe", "roomserver.exe")
$TargetPaths = @{}
foreach ($TargetName in $TargetNames) {
    $TargetPaths[$TargetName.ToLowerInvariant()] = (Join-Path $BinDir $TargetName)
}

# 项目服务的网络端点：logic gRPC、logic HTTP、match gRPC、room KCP
$PortSpecs = @(
    [PSCustomObject]@{ Protocol = "TCP"; Port = 8080 },
    [PSCustomObject]@{ Protocol = "TCP"; Port = 8081 },
    [PSCustomObject]@{ Protocol = "TCP"; Port = 8090 },
    [PSCustomObject]@{ Protocol = "UDP"; Port = 9001 }
)

function Write-Log {
    param([string]$Message)
    if (-not $Quiet) {
        Write-Host $Message
    }
}

function Get-TargetProcesses {
    # 通过完整路径优先匹配，避免误杀其他目录中的同名程序
    $processes = @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object {
        $name = ([string]$_.Name).ToLowerInvariant()
        $TargetNames -contains $name
    })
    $result = @()
    $seen = @{}

    foreach ($process in $processes) {
        $name = ([string]$process.Name).ToLowerInvariant()
        $path = [string]$process.ExecutablePath
        $expectedPath = [string]$TargetPaths[$name]
        $matchBy = $null

        if (-not [string]::IsNullOrWhiteSpace($path)) {
            try {
                $path = [IO.Path]::GetFullPath($path)
            } catch {
                # 路径无法规范化时，不使用路径匹配
            }
            if ($path -ieq $expectedPath) {
                $matchBy = "path"
            }
        } elseif ($TargetNames -contains $name) {
            # 某些权限环境无法读取 ExecutablePath，只对固定服务名做兜底匹配
            $matchBy = "name-fallback"
        }

        if ($null -eq $matchBy) {
            continue
        }

        $processId = [int]$process.ProcessId
        if ($seen.ContainsKey($processId)) {
            continue
        }
        $seen[$processId] = $true
        $result += [PSCustomObject]@{
            Name    = $name
            PID     = $processId
            Path    = $path
            MatchBy = $matchBy
        }
    }

    return $result
}

function Wait-TargetProcessesGone {
    param([int]$TimeoutSeconds)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ($true) {
        $remaining = @(Get-TargetProcesses)
        if ($remaining.Count -eq 0) {
            return @()
        }
        if ($TimeoutSeconds -le 0 -or (Get-Date) -ge $deadline) {
            return $remaining
        }
        Start-Sleep -Milliseconds 200
    }
}

function Stop-TargetProcess {
    param(
        [Parameter(Mandatory = $true)]
        $TargetProcess,
        [switch]$ForceStop
    )

    $mode = if ($ForceStop) { "force" } else { "stop" }
    Write-Log "[stop] $($TargetProcess.Name) PID=$($TargetProcess.PID) mode=$mode match=$($TargetProcess.MatchBy)"

    try {
        if ($ForceStop) {
            # taskkill 的 /T 同时清理该服务可能创建的子进程
            & taskkill.exe /PID ([string]$TargetProcess.PID) /T /F 2>$null | Out-Null
            if ($LASTEXITCODE -ne 0) {
                Stop-Process -Id $TargetProcess.PID -Force -ErrorAction Stop
            }
        } else {
            Stop-Process -Id $TargetProcess.PID -ErrorAction Stop
        }
    } catch {
        # 进程可能在本次操作前已自行退出，最终状态由轮询再次确认
        Write-Warning "failed to stop $($TargetProcess.Name) PID=$($TargetProcess.PID): $($_.Exception.Message)"
    }
}

function Get-PortOccupants {
    $result = @()
    $hasTcpQuery = $null -ne (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue)
    $hasUdpQuery = $null -ne (Get-Command Get-NetUDPEndpoint -ErrorAction SilentlyContinue)
    if (-not $hasTcpQuery -or -not $hasUdpQuery) {
        throw "Windows networking cmdlets are unavailable: require Get-NetTCPConnection and Get-NetUDPEndpoint"
    }

    foreach ($spec in $PortSpecs) {
        if ($spec.Protocol -eq "TCP") {
            $connections = @(Get-NetTCPConnection -LocalPort $spec.Port -ErrorAction Stop)
            foreach ($connection in $connections) {
                $result += [PSCustomObject]@{
                    Protocol      = $spec.Protocol
                    Port          = $spec.Port
                    OwningProcess = [int]$connection.OwningProcess
                }
            }
        } else {
            $endpoints = @(Get-NetUDPEndpoint -LocalPort $spec.Port -ErrorAction Stop)
            foreach ($endpoint in $endpoints) {
                $result += [PSCustomObject]@{
                    Protocol      = $spec.Protocol
                    Port          = $spec.Port
                    OwningProcess = [int]$endpoint.OwningProcess
                }
            }
        }
    }

    return $result | Sort-Object Protocol, Port, OwningProcess -Unique
}

function Get-ProcessLabel {
    param([int]$ProcessId)
    try {
        return (Get-Process -Id $ProcessId -ErrorAction Stop).ProcessName
    } catch {
        return "exited"
    }
}

function Wait-PortsReleased {
    param([int]$TimeoutSeconds)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ($true) {
        $occupants = @(Get-PortOccupants)
        if ($occupants.Count -eq 0) {
            return @()
        }
        if ((Get-Date) -ge $deadline) {
            return $occupants
        }
        Start-Sleep -Milliseconds 200
    }
}

try {
    Write-Log "[stop] root: $RootDir"
    $targets = @(Get-TargetProcesses)

    if ($targets.Count -eq 0) {
        Write-Log "[stop] no project service process found"
    } else {
        if ($Force) {
            foreach ($target in $targets) {
                Stop-TargetProcess -TargetProcess $target -ForceStop
            }
        } else {
            foreach ($target in $targets) {
                Stop-TargetProcess -TargetProcess $target
            }

            # 给服务一个短暂的退出窗口；超时后必须强制清理
            $targets = @(Wait-TargetProcessesGone -TimeoutSeconds $GracefulTimeoutSeconds)
            if ($targets.Count -gt 0) {
                Write-Warning "graceful stop timed out; force stopping $($targets.Count) remaining process(es)"
                foreach ($target in $targets) {
                    Stop-TargetProcess -TargetProcess $target -ForceStop
                }
            }
        }

        $targets = @(Wait-TargetProcessesGone -TimeoutSeconds 3)
        if ($targets.Count -gt 0) {
            foreach ($target in $targets) {
                Write-Error "project service is still running: $($target.Name) PID=$($target.PID) path=$($target.Path)"
            }
            exit 1
        }
    }

    # 进程退出后再确认所有项目端点已经释放
    $occupants = @(Wait-PortsReleased -TimeoutSeconds $PortCheckTimeoutSeconds)
    if ($occupants.Count -gt 0) {
        foreach ($occupant in $occupants) {
            $label = Get-ProcessLabel -ProcessId $occupant.OwningProcess
            Write-Error "port is still occupied: $($occupant.Protocol) $($occupant.Port), PID=$($occupant.OwningProcess), process=$label"
        }
        exit 1
    }

    foreach ($spec in $PortSpecs) {
        Write-Log "[stop] released $($spec.Protocol) $($spec.Port)"
    }
    Write-Log "[stop] all project services stopped and ports released"
    exit 0
} catch {
    Write-Error "stop services failed: $($_.Exception.Message)"
    exit 1
}
