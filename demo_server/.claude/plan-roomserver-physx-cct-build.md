# PhysX CCT 构建配置升级方案

## 1. 需求理解

当前项目准备的 PhysX SDK 已包含 `characterkinematic` 头文件和源码工程 target，但 Windows SDK 准备脚本没有构建 `PhysXCharacterKinematic`，roomserver 的 bridge CMake 也没有链接对应静态库。用户希望先升级构建配置，为后续将玩家实现迁移到 CCT 做准备；本次不修改 C++ 运行时代码、Go 物理接口、房间逻辑或协议。

## 2. 影响范围

预计只修改：

- `scripts/setup_physx_windows.ps1`
  - 将 `PhysXCharacterKinematic` 加入 PhysX SDK 构建 target 列表
  - 现有通配复制逻辑会自动将生成的 `.lib`、`.pdb` 和运行时 DLL（如有）整理到 prepared SDK
- `src/roomserver/physx/CMakeLists.txt`
  - 将 `PhysXCharacterKinematic_static_64` 加入 bridge 静态库链接列表

不修改：

- `src/roomserver/physx/physx_bridge.cc/.h`
- `src/roomserver/physx/world.go`
- `src/roomserver/logic/*`
- proto、配置和网络协议
- `scripts/build_physx_bridge_windows.ps1`，因为它已经按 `PhysX*.lib` 通配复制 SDK 产物，并将 CMake 参数传入 bridge

## 3. 设计方案

### 3.1 PhysX SDK 准备脚本

在现有 `cmake --build ... --target` 列表中追加 `PhysXCharacterKinematic`。PhysX 源码的 Windows CMake 工程已经声明该 target，构建后会生成 `PhysXCharacterKinematic_static_64.lib`。当前脚本会复制所有匹配 `PhysX*.lib` 和 `PhysX*.pdb` 的文件，因此不需要新增单独复制分支。

### 3.2 bridge 链接配置

在 `target_link_libraries(physx_bridge ...)` 中追加 `PhysXCharacterKinematic_static_64`，继续沿用当前库目录、配置名和 MSVC delay-load 设置。CCT 是静态库，不新增 DLL 的 delay-load 项。

### 3.3 配置边界

本次只保证后续 C++ 代码能够包含 CCT 头文件并解析 CCT API 的链接符号；不在本次引入 `PxControllerManager` 或创建 controller。这样构建配置变更可以独立验证，后续运行时迁移失败时能区分“链接基础设施问题”和“CCT 业务实现问题”。

## 4. 兼容性影响

- 已有非 CCT bridge 源码在配置完成并重新准备 SDK 后仍可构建
- 不改变任何 Go API、protobuf 字段、房间创建流程或运行时行为
- 首次重新执行 setup 会额外构建 PhysX Character Kinematic 静态库，构建时间和 SDK 体积略有增加
- 现有 checked/debug/profile/release 配置沿用 PhysX 工程已有 target，不新增配置名
- 当前工作区已有上述构建文件的未提交修改，实施时只做精确增量编辑，不覆盖已有改动

## 5. 健壮性

- 依赖库缺失时由 CMake 链接阶段显式失败，不静默退回未启用 CCT 的构建
- SDK target 构建失败时 setup 脚本沿用现有 `$LASTEXITCODE` 检查并终止
- 继续沿用现有路径参数和通配复制逻辑，避免引入重复文件复制或路径分支
- 不改变运行时资源释放和房间生命周期，避免在尚未迁移 C++ 实现前引入不匹配的 ABI

## 6. 性能考虑

本次只有构建期变化，不影响运行时性能。后续使用 CCT 时，静态库链接不会增加每帧 Go/C++ 调用次数，也不会改变当前 scene 或房间 tick 行为。

## 7. 验证方式

1. 检查修改后的 PowerShell target 列表和 CMake 链接列表
2. 在当前环境执行 CMake 配置或运行 `scripts/build_physx_bridge_windows.ps1`，确认配置能识别 CCT 库路径；若本地 prepared SDK 尚未重新执行 setup，需如实报告缺少 `PhysXCharacterKinematic_static_64.lib`
3. 重新执行 `scripts/setup_physx_windows.ps1 -BuildType checked`（若工具链和源码构建条件满足），确认 SDK 输出目录生成 CCT 静态库
4. 重新执行 `scripts/build_physx_bridge_windows.ps1 -BuildType checked`，确认 bridge 链接通过
5. 如无法执行 Windows MSVC 构建，至少完成静态配置检查并报告具体阻塞原因，不声称构建成功

## 8. 自我审查

- 未把 CCT 运行时代码提前混入本次构建改动，符合“先升级构建配置”的范围
- 未修改 `build_physx_bridge_windows.ps1`，因为其 `PhysX*.lib` 通配复制已经覆盖新静态库；仅在 CMake 链接中声明依赖即可
- 未修改 Linux 脚本，因为当前 bridge CMake 明确只支持 Windows，本次目标是当前 Windows 构建链
- 未新增不必要的库检查或自定义 CMake 抽象，避免与已有 PhysX 目录变量和脚本约定重复
- 重新执行 setup 是必要的：仅修改 bridge 链接不能凭空生成当前 SDK 缺失的 CCT 库

## 9. 修正后的最终方案

只对 `scripts/setup_physx_windows.ps1` 和 `src/roomserver/physx/CMakeLists.txt` 做两处最小增量：先让 PhysX SDK 准备阶段构建并复制 `PhysXCharacterKinematic`，再让 bridge 链接 `PhysXCharacterKinematic_static_64`。不改任何运行时代码；完成后优先验证 checked 配置，随后再进入 CCT native 实现阶段。

等待用户确认后执行。
