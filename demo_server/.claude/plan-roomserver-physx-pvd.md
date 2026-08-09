# roomserver PhysX PVD 接入方案

## 1. 需求理解

需要在当前 roomserver 已有 PhysX cgo 后端上接入 PhysX Visual Debugger（PVD），用于在开发调试时观察 PhysX scene、actor、静态地图碰撞体、玩家 capsule、raycast/sweep 查询等物理状态。

接入原则：PVD 属于调试能力，默认关闭；启用后才连接外部 PVD 工具，避免影响默认运行性能和部署稳定性。

## 2. 影响范围

预计修改这些文件：

- `src/roomserver/config/config.go`
  - 增加 PVD 配置字段、默认值和 Normalize 逻辑
- `config/config.yaml`
  - 增加 roomserver PVD 配置示例，默认关闭
- `config/config_test.go`、`src/roomserver/config/config_test.go`
  - 增加默认值/配置读取校验
- `src/roomserver/service/server.go`
  - 把 PVD 配置传入 PhysX factory，并在启动日志中输出是否启用
- `src/roomserver/physx/types.go`
  - 扩展 PhysX 后端配置结构
- `src/roomserver/physx/world.go`
  - 把 PVD 配置转换为 C ABI 配置传给 C++ 层
- `src/roomserver/physx/physx_bridge.h`
  - 新增 `px_pvd_config`，扩展 `px_world_create` 参数
- `src/roomserver/physx/physx_bridge.cc`
  - 创建 `PxPvd`、socket transport，连接 PVD，并为 scene 设置 PVD scene flags
- `src/roomserver/physx/world_stub.go`
  - 保持未启用 physx tag 时的类型兼容
- `src/roomserver/README.md`、`src/roomserver/PHYSX_FLOW.md`、`src/roomserver/learning/05-physics-and-map-collision.md`
  - 补充 PVD 开启方式、连接端口和注意事项

不会修改网络协议、proto、logic 层物理接口或玩法逻辑。

## 3. 设计方案

### 3.1 配置设计

在 roomserver 配置增加：

```go
PhysXPVDEnabled   bool   `yaml:"physx_pvd_enabled"`    // 是否启用 PhysX PVD
PhysXPVDHost      string `yaml:"physx_pvd_host"`       // PVD 监听地址
PhysXPVDPort      int    `yaml:"physx_pvd_port"`       // PVD 监听端口
PhysXPVDTimeoutMS int    `yaml:"physx_pvd_timeout_ms"` // PVD 连接超时毫秒
```

默认值：

```yaml
physx_pvd_enabled: false
physx_pvd_host: "127.0.0.1"
physx_pvd_port: 5425
physx_pvd_timeout_ms: 100
```

说明：

- `enabled=false` 时完全不创建 PVD 对象，不增加额外连接和调试开销。
- host/port/timeout 即使默认关闭也会 Normalize，方便用户只改 `physx_pvd_enabled: true`。
- 不把 instrumentation flags 暴露成配置，第一版启用时固定使用 PhysX PVD `eALL`，并在 scene 上开启 contacts、scene queries、constraints 传输。PVD 是显式调试开关，配置过细会增加使用成本。

### 3.2 Go 到 C ABI

`physx.Config` 增加同名 PVD 字段。`service.newPhysicsWorldFactory` 从 roomserver Config 传入这些字段。

`world.go` 在创建 world 时构造 C 结构：

```c
typedef struct px_pvd_config {
    int enabled;
    const char* host;
    int port;
    unsigned int timeout_ms;
} px_pvd_config;
```

然后调用：

```c
px_world_create(create_ground_plane, pvd_config, err, err_len)
```

host 使用 `C.CString` 临时传入，C++ 层只在创建 runtime 时读取，不保存 Go 指针。

### 3.3 C++ runtime 生命周期

当前 C++ 层已有进程级 `px_runtime`：

```cpp
PxFoundation* foundation;
PxPhysics* physics;
int ref_count;
```

扩展为：

```cpp
PxPvd* pvd;
PxPvdTransport* pvd_transport;
bool pvd_enabled;
```

创建顺序调整为：

```text
PxCreateFoundation
-> 如果 enabled，PxCreatePvd
-> 如果 enabled，PxDefaultPvdSocketTransportCreate(host, port, timeout)
-> 如果 enabled，pvd->connect(transport, PxPvdInstrumentationFlag::eALL)
-> PxCreatePhysics(..., pvd 或 nullptr)
```

释放顺序：

```text
释放所有 world scene / actor / material / dispatcher
-> runtime 引用计数归零
-> physics->release()
-> pvd->disconnect() / pvd->release()
-> pvd_transport->release()
-> foundation->release()
```

这样保持 PhysX 对象依赖关系正确，且 PVD 生命周期跟随进程级 PhysX runtime。

### 3.4 每房间 scene 的 PVD flags

每个房间创建 `PxScene` 后，如果 PVD 可用：

```cpp
PxPvdSceneClient* pvd_client = scene->getScenePvdClient();
pvd_client->setScenePvdFlag(PxPvdSceneFlag::eTRANSMIT_CONTACTS, true);
pvd_client->setScenePvdFlag(PxPvdSceneFlag::eTRANSMIT_SCENEQUERIES, true);
pvd_client->setScenePvdFlag(PxPvdSceneFlag::eTRANSMIT_CONSTRAINTS, true);
```

`eTRANSMIT_SCENEQUERIES` 对当前项目比较重要，因为玩家移动用 sweep，开火用 raycast，PVD 中可以观察这些查询。

### 3.5 连接失败策略

第一版采用“启用即要求可连接”的策略：

- `physx_pvd_enabled=false`：不连接 PVD，默认运行不受影响。
- `physx_pvd_enabled=true` 且连接失败：创建 PhysX runtime 失败，错误信息提示 PVD host/port 连接失败。

原因：用户显式打开 PVD 时，一般就是为了调试可视化；如果静默继续运行，很容易误判为“PVD 已接入但工具没显示”。明确失败更利于排障。后续如果需要“连接失败但继续运行”，再增加 `physx_pvd_required` 配置。

## 4. 兼容性影响

- 默认配置关闭 PVD，现有启动、测试和房间逻辑行为不变。
- 不改客户端协议，不改 room token，不改 KCP 消息。
- 不改 logic 层 `PhysicsWorld` 接口，PVD 只影响 PhysX 后端内部。
- 仍然要求 roomserver 使用 `-tags physx` 才能启用真实 PhysX/PVD。
- 已有 cgo LDFLAGS 已链接 `PhysXPvdSDK` 和 `PhysXExtensions`，预计不需要新增库依赖。
- PVD 是进程级 PhysX runtime 配置；同一进程内首个 world 创建时确定 PVD 连接。roomserver 当前全进程只使用一份配置，符合这个约束。

## 5. 健壮性

- 校验 PVD host 非空，port > 0，timeout > 0；非法值 Normalize 到默认值。
- C++ 层对空 world、空 scene、空 pvd client 做保护。
- PVD 创建、transport 创建、connect、physics 创建失败时释放已创建资源，避免泄漏。
- `enabled=false` 时不创建 PVD 对象，释放逻辑对空指针安全。
- 继续保持每个房间 goroutine 串行访问自己的 PhysX scene，不引入新的并发访问路径。
- 不保存 Go 指针到 C++，只保存 C++ 自己创建的 PVD/transport 对象。

## 6. 性能考虑

- 默认关闭，无运行时额外成本。
- 启用后 PVD `eALL` 和 scene query 传输会增加 CPU、内存和网络开销，只建议本地或测试环境开启。
- PVD socket 连接只在进程级 runtime 创建时执行一次，不在每 tick 重连。
- scene query 传输会覆盖 raycast/sweep 调试需求，但高频战斗压测时不应开启。
- 不增加每 tick Go/C 调用次数，不改变现有移动和 raycast 调用路径。

## 7. 验证方式

实现后计划执行：

```bash
gofmt -w src/roomserver/config src/roomserver/service src/roomserver/physx config
go test ./src/roomserver/config ./config
go test ./src/roomserver/logic ./src/roomserver/protocol
go test -tags physx ./src/roomserver/physx
go build -tags physx ./src/roomserver/cmd
```

如果本机可连接 PVD 工具，再做手动验证：

1. 打开 NVIDIA PhysX Visual Debugger，监听 `5425`。
2. 将 `config/config.yaml` 中 `physx_pvd_enabled` 改为 `true`。
3. 启动 roomserver，并创建/加入房间触发 PhysX world 创建。
4. 在 PVD 中确认能看到 scene、map static box、player capsule，以及移动/开火产生的 scene queries。

如果 PVD GUI 当前不可用，我会说明只完成编译和单测验证，不声称 GUI 联调通过。

## 8. 自我审查

原始方案风险：

1. 如果默认启用 PVD，会让没有安装或没有打开 PVD 工具的开发者启动失败，也会影响性能。
2. 如果 PVD 连接失败后静默继续运行，调试时很难判断是配置错、网络不通还是代码未接入。
3. 如果把 PVD 参数做得过细，例如暴露所有 instrumentation flags，会增加配置复杂度，当前需求没有必要。
4. 如果在每个 room/world 单独创建 PVD，会违背 PhysX 进程级 runtime 设计，也会造成多个连接和资源浪费。
5. 如果只改 C++ 不补文档，后续很难知道 WSL/Windows host、端口和启用方式。

修正后的最终方案：

- PVD 默认关闭，通过 roomserver YAML 显式开启。
- PVD 挂到进程级 `px_runtime`，每个房间 scene 只设置 PVD scene flags。
- 启用后连接失败直接返回明确错误，避免误以为调试链路已经工作。
- 第一版固定使用 PVD `eALL` 和 scene query/contact/constraint 传输，不引入额外复杂配置。
- 配套更新配置测试、PhysX 编译验证和文档说明。

等待用户确认后再修改业务代码。
