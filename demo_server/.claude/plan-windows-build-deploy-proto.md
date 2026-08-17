# Windows 原生编译部署链路方案

## 需求理解

目标是把当前项目补成一条 Windows 原生可编译、可部署、可启动的链路。现有 Linux 链路继续保留；Windows 侧重点是新增 PowerShell 脚本和 PhysX/cgo 的平台适配。用户已经有部分改动，后续实现会避开无关改动，不回滚现有工作区内容。

## 影响范围

预计新增或修改以下文件：

1. `src/roomserver/physx/world.go`
   - 拆分 `#cgo` 平台参数。
   - Linux 继续链接 `third_party/physx-sdk/lib/linux.x86_64/release` 静态库。
   - Windows 链接 `physx_bridge` 导入库，运行时使用 `physx_bridge.dll`。

2. `src/roomserver/physx/physx_bridge.h`
   - 增加跨平台导出宏，例如 Windows 下 `__declspec(dllexport)`。
   - Linux 下宏为空，不影响现有 cgo 静态链接。

3. `src/roomserver/physx/physx_bridge.cc`
   - 调整 build tag 为 `physx && !windows`，避免 Windows 下 Go/cgo 直接编译 C++ 文件。
   - C++ 函数声明使用导出宏，供 Windows DLL 暴露 C ABI。

4. `src/roomserver/physx/CMakeLists.txt`
   - 新增 Windows 专用 `physx_bridge.dll` 构建定义。
   - 使用 MSVC/CMake 链接 Windows 版 PhysX SDK。

5. `config/config.go`
   - 新增 `DEMO_SERVER_CONFIG` 环境变量作为可选配置路径。
   - 未设置环境变量时保持读取 `config/config.yaml` 的旧行为。

6. `config/config.windows.yaml`
   - 新增 Windows 部署样例配置。
   - 使用 `0.0.0.0` 监听，`physics_backend: "physx"`。
   - 房间地址保留为可改的 Windows 主机地址样例。

7. `scripts/proto_windows.ps1`
   - 新增 Windows 版 proto 生成脚本。

8. `scripts/setup_physx_windows.ps1`
   - 新增 Windows 版 PhysX SDK 准备脚本。
   - 拉取 PhysX、调用 Windows preset、整理 include/lib 到 `third_party/physx-sdk`。

9. `scripts/build_physx_bridge_windows.ps1`
   - 新增 Windows 版 bridge DLL 构建脚本。
   - 输出并复制 `physx_bridge.dll` 和导入库到 Windows bin/lib 目录。

10. `scripts/build_all_windows.ps1`
    - 新增 Windows 版三个服务构建脚本。
    - `roomserver.exe` 使用 `-tags physx`，其余服务普通构建。

11. `scripts/deploy_windows.ps1`
    - 新增 Windows 部署打包脚本。
    - 汇总 exe、DLL、配置、地图碰撞文件到部署目录。

12. `scripts/start_all_windows.ps1`
    - 新增 Windows 本地启动脚本。
    - 设置 `DEMO_SERVER_CONFIG`，按 matchserver、logicserver、roomserver 启动。

13. `README.md` 或新增 `docs/windows-deploy.md`
    - 补充 Windows 构建、部署、启动命令和依赖说明。

## 设计方案

### 1. 平台构建分流

Linux 保持当前成熟路径：

```text
go build -tags physx ./src/roomserver/cmd
  -> cgo 编译 physx_bridge.cc
  -> 链接 Linux PhysX 静态库
```

Windows 使用 DLL 桥接：

```text
setup_physx_windows.ps1
  -> 准备 Windows 版 PhysX SDK
build_physx_bridge_windows.ps1
  -> MSVC/CMake 编译 physx_bridge.dll
build_all_windows.ps1
  -> go build -tags physx 生成 roomserver.exe
  -> cgo 链接 physx_bridge 导入库
```

这样 Go 侧仍然只调用 `physx_bridge.h` 暴露的 C ABI，不让 MinGW/cgo 直接混链 MSVC 编译出来的 PhysX C++ 静态库，降低 Windows ABI 和运行库风险。

### 2. Windows 输出目录

构建输出统一放到：

```text
bin/windows/
├── logicserver.exe
├── matchserver.exe
├── roomserver.exe
├── physx_bridge.dll
└── config/
```

部署脚本再复制到：

```text
dist/windows/demo_server/
├── bin/
├── config/
└── maps 或 config/maps/
```

具体目录会按现有配置读取方式选择，优先保证 `map_collision_path` 在部署包内可直接找到。

### 3. 配置策略

新增 `DEMO_SERVER_CONFIG`，配置读取顺序为：

1. `Load(path)` 显式传入路径时使用传入值。
2. `path` 为空且 `DEMO_SERVER_CONFIG` 非空时使用环境变量。
3. 否则继续自动查找项目根目录下的 `config/config.yaml`。

这样 Windows 脚本可以启动指定配置，不影响 Linux 默认行为。

### 4. 脚本行为

PowerShell 脚本统一使用：

```powershell
$ErrorActionPreference = "Stop"
```

并从 `$PSScriptRoot` 定位项目根目录，避免用户在不同目录执行时找不到文件。

脚本会检查必要依赖：

- Go
- Git
- CMake
- protoc / protoc-gen-go / protoc-gen-go-grpc
- MSVC 构建环境
- Windows 版 PhysX SDK
- `physx_bridge.dll`

### 5. 部署脚本

`deploy_windows.ps1` 做可复制部署包：

1. 可选触发 proto 生成。
2. 可选触发 PhysX bridge 构建。
3. 构建三个服务 exe。
4. 复制 `physx_bridge.dll`。
5. 复制 Windows 配置和地图碰撞文件。
6. 输出部署目录和启动命令。

## 兼容性

1. 不修改 proto 字段、字段编号、RPC 定义。
2. 不修改客户端消息协议。
3. 不改变 service、logic、repo 分层。
4. Linux 仍使用 `make proto`、`scripts/setup_physx.sh`、`scripts/build_all.sh`。
5. Windows 新增脚本，不替换 Linux 脚本。
6. 默认 `config/config.yaml` 行为不变；Windows 配置通过环境变量显式启用。
7. 现有用户未提交改动不回滚、不覆盖。

## 健壮性

1. 脚本失败时直接中断并输出缺失依赖或缺失产物路径。
2. Windows 构建前检查 bridge DLL 和导入库是否存在。
3. 部署包缺少配置或地图碰撞文件时直接报错。
4. 配置路径支持相对路径和绝对路径。
5. roomserver 仍保留 `world_stub.go` 的错误提示，未带 `-tags physx` 时不会静默伪装成 PhysX 后端。
6. 启动脚本提示 Windows 防火墙需要放行 TCP 8080、TCP 8090、UDP 9001。

## 性能考虑

1. Linux 性能不变，仍是 cgo + PhysX 静态库。
2. Windows 下 Go 调用 DLL C ABI，PhysX 运算仍在 C++ 层。
3. 不改变房间 tick、移动、raycast、snapshot 的调用频率。
4. 不新增高频业务接口或无界 goroutine。
5. 如果后续 Windows DLL 调用开销需要优化，再做批量移动接口；本次先只完成可编译部署链路。

## 验证方式

当前 Linux/WSL 环境可执行：

```bash
go test ./...
go test -run TestLoadRoomServerConfig ./config
go build ./src/logicserver/cmd
go build ./src/matchserver/cmd
go build ./src/roomserver/cmd
```

如果本机已有 Linux PhysX SDK，可额外执行：

```bash
go test -tags physx ./src/roomserver/physx
scripts/build_all.sh
```

Windows 侧预期验证命令：

```powershell
.\scripts\proto_windows.ps1
.\scripts\setup_physx_windows.ps1
.\scripts\build_physx_bridge_windows.ps1
.\scripts\build_all_windows.ps1
.\scripts\deploy_windows.ps1
$env:DEMO_SERVER_CONFIG="config\config.windows.yaml"
.\scripts\start_all_windows.ps1
```

重点确认：

1. `gen` 目录生成成功。
2. `physx_bridge.dll` 生成并被复制到 Windows 输出目录。
3. `roomserver.exe` 使用 `-tags physx` 构建成功。
4. roomserver 启动时加载 `physics_backend: physx`。
5. roomserver 能读取地图碰撞文件。
6. 客户端能连到 Windows 主机 UDP 9001。

## 自我审查

1. 不能把 Windows 支持做成替换 Linux，方案已改成双平台并存。
2. Windows 直接混链 PhysX C++ 静态库风险较高，方案改用 DLL C ABI 隔离。
3. 仅加脚本不够，`world.go` 的 cgo flags 和 `physx_bridge.cc` build tag 必须同步处理。
4. 配置不应覆盖默认 Linux 配置，方案使用单独 `config.windows.yaml` 和环境变量。
5. 部署包必须包含地图碰撞文件，否则 roomserver 即使编译成功也会启动失败。
6. 不能承诺在当前 Linux 环境直接验证 Windows MSVC 编译，只能提供脚本并在 Windows 上运行验证。

## 修正后的最终方案

最终实现为：保留 Linux 原链路，新增 Windows PowerShell 链路；Windows 下先用 MSVC/CMake 生成 `physx_bridge.dll`，再用 Go/cgo 构建 `roomserver.exe` 并部署 DLL、配置和地图文件。业务代码和协议不变，只做构建、配置加载和部署脚本相关适配。

等待确认后开始修改代码和脚本。
