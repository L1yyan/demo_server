# roomserver 后续开发计划：AOI 升级

## 当前状态

当前 roomserver 已经具备基础入房、房间 tick、玩家输入、状态快照和 AOI 过滤链路。

当前 AOI 实现在 `src/roomserver/logic/aoi.go`，不是只做距离筛，已经包含两层过滤：

1. 距离过滤：超过 `VisibleDistance` 的玩家不下发
2. 水平视野角过滤：超过 `ViewAngle / 2` 的玩家不下发

当前还没有：

- 垂直视角过滤
- 地图遮挡检测
- 空间索引加速
- AOI 参数配置化
- AOI 单元测试和压测基准
- 可观测日志或统计指标

## roomserver 下一步总览

后续 roomserver 建议按以下顺序推进：

1. AOI 升级：让快照只下发真正可见或应同步的玩家
2. 权威移动：服务端限制速度、位移、输入频率，避免客户端直接改坐标
3. 碰撞接入：把移动和射击接到 PhysicsWorld，先做接口内模拟，再替换真实物理
4. 房间生命周期：空房回收、满房拒绝、异常玩家清理
5. token 一次性消费：room token 使用后失效，避免重复入房
6. matchserver 联动：roomserver 向 matchserver 回报房间人数和连接确认
7. 状态同步优化：快照差量、压缩、丢包容错、客户端插值支持
8. 玩法基础：开火命中、伤害、死亡、复活、结算

本计划先展开第 1 项：AOI 升级。

## AOI 升级目标

第一版升级目标不是做复杂地图系统，而是把 AOI 从“简单可见列表”提升为可扩展的多阶段过滤管线：

```text
候选玩家
  -> 距离粗筛
  -> 视锥筛选
  -> 遮挡检测
  -> 快照输出
```

同时保留当前简单实现的低成本优势：10 人小房间仍可直接遍历，避免过早引入复杂空间结构。

## 阶段一：整理 AOI 接口和参数

### 需求理解

当前 `AOIFilter.FilterVisible(self, candidates)` 只拿玩家列表，无法访问物理世界，也无法携带配置或统计信息。升级前需要让 AOI 接口承载更多上下文。

### 影响范围

预计修改：

- `src/roomserver/logic/aoi.go`
- `src/roomserver/logic/room.go`
- `src/roomserver/config/config.go`

### 设计方案

新增 AOI 配置结构：

```go
type AOIConfig struct {
    VisibleDistance float64
    HorizontalFOV   float64
    VerticalFOV     float64
    EnableViewCone  bool
    EnableOcclusion bool
}
```

调整过滤接口为：

```go
type AOIFilter interface {
    FilterVisible(ctx AOIContext) []*Player
}

type AOIContext struct {
    Self       *Player
    Candidates []*Player
    Physics    PhysicsWorld
}
```

这样 AOI 逻辑后续可以在遮挡检测时调用 `PhysicsWorld.Raycast`，不会让 `Room` 里堆大量可见性判断。

### 健壮性

- `Self == nil` 返回空列表
- `Candidates` 为空直接返回空列表
- 配置非法时使用默认值
- `Physics == nil` 或关闭遮挡检测时跳过射线检测

### 验证方式

- `go test ./src/roomserver/logic`
- `go test ./...`

## 阶段二：补完整视锥筛选

### 需求理解

当前只判断水平角 `Yaw`，没有判断 `Pitch` 和高度差。FPS 场景下目标在头顶或脚下时，需要能按垂直视野过滤。

### 影响范围

预计修改：

- `src/roomserver/logic/aoi.go`
- `src/roomserver/logic/aoi_test.go`

### 设计方案

将视角过滤拆成独立函数：

```go
func inHorizontalFOV(self *Player, target *Player, fov float64) bool
func inVerticalFOV(self *Player, target *Player, fov float64) bool
```

水平角继续使用 `atan2(dx, dz) - self.Yaw`。

垂直角基于高度差和水平距离：

```text
verticalAngle = atan2(dy, horizontalDistance) - self.Pitch
```

保留默认值：

- 可见距离：80
- 水平 FOV：120
- 垂直 FOV：90 或先默认关闭，避免当前移动系统没有完整 Y 轴语义时误伤

### 健壮性

- 目标和自己重合时不触发除零问题
- 角度统一归一化到 `[-180, 180]`
- FOV <= 0 时跳过对应角度过滤或使用默认值，需要明确选择一种行为

### 验证方式

新增表驱动测试：

- 正前方目标可见
- 背后目标不可见
- 距离外目标不可见
- 水平 FOV 边界目标行为稳定
- 垂直 FOV 上下边界行为稳定

## 阶段三：接入遮挡检测

### 需求理解

距离和视锥只能说明目标在视野范围内，不能说明中间没有墙。后续地图有障碍物后，AOI 需要调用物理射线检测判断是否被遮挡。

### 影响范围

预计修改：

- `src/roomserver/logic/aoi.go`
- `src/roomserver/logic/physics.go`
- `src/roomserver/logic/room.go`
- `src/roomserver/logic/aoi_test.go`

### 设计方案

AOI 在距离和视锥通过后再做遮挡检测：

```text
ray origin = self 眼睛位置
ray target = target 身体中心或眼睛位置
ray max distance = self 到 target 距离
```

`PhysicsWorld.Raycast` 需要能表达：

- 没打到任何障碍：可见
- 打到目标之前的障碍：不可见
- 打到玩家自身或目标：可见

当前 `PhysicsWorld` 还是简化接口，第一版遮挡可以先定义语义，不接真实地图时默认 `SimplePhysicsWorld` 返回不遮挡。

### 健壮性

- 没有物理世界时不做遮挡检测
- Raycast 错误时默认不下发还是默认可见，需要明确。建议第一版默认可见并打 warn，避免物理模块故障导致玩家全部消失
- 后续真实物理接入后再根据错误类型细分

### 验证方式

- 用 fake physics world 测试无遮挡时可见
- 用 fake physics world 测试有墙遮挡时不可见
- 验证遮挡检测只在距离和视锥通过后执行，减少不必要 raycast

## 阶段四：空间索引预留

### 需求理解

当前每个玩家快照都遍历全房间玩家，10 人房间没有问题。如果后续单房间人数提高，全量遍历会变成 `O(n^2)`。

### 影响范围

预计新增或修改：

- `src/roomserver/logic/aoi_grid.go`
- `src/roomserver/logic/room.go`

### 设计方案

先不急着引入复杂结构，预留接口：

```go
type AOIIndex interface {
    Rebuild(players map[uint64]*Player)
    Query(center Vector3, radius float64) []*Player
}
```

第一版可以实现 `LinearAOIIndex`，行为等同全量遍历。后续再替换为 uniform grid：

- 按 X/Z 坐标划分格子
- 每次快照前 rebuild 一次索引
- 查询中心周围半径覆盖的格子

### 性能考虑

- 10 人房间继续全量遍历，代码最简单
- 50 人以上再考虑 grid，否则索引维护成本可能大于收益
- 遮挡 raycast 比遍历更贵，优先保证 raycast 只对距离和视锥通过的候选执行

### 验证方式

- benchmark 对比全量遍历和 grid 查询
- 10、50、100 人模拟快照耗时

## 阶段五：配置化和观测

### 需求理解

AOI 参数不应写死在代码里。不同玩法模式可能需要不同视野距离、FOV、是否遮挡检测。

### 影响范围

预计修改：

- `src/roomserver/config/config.go`
- `config/config.go`
- `config/config.yaml`
- `src/roomserver/service/server.go`

### 设计方案

roomserver 配置增加：

```yaml
room_server_01:
  aoi:
    visible_distance: 80
    horizontal_fov: 120
    vertical_fov: 90
    enable_view_cone: true
    enable_occlusion: false
```

房间创建时由 `service.NewServer` 或 `RoomManager` 注入 AOI 配置。

可以增加少量统计：

- 候选人数
- 距离过滤后人数
- 视锥过滤后人数
- 遮挡过滤后人数
- 单次快照 AOI 耗时

统计先只在 debug 或定期采样输出，避免高频日志影响性能。

## 推荐实施顺序

1. 先补 AOI 单元测试，锁定当前“距离 + 水平视野角”行为
2. 抽出 AOI 配置和上下文结构，不改变现有行为
3. 增加垂直 FOV，但默认可关闭或设置较宽，避免影响当前玩法
4. 接入 fake physics 测试遮挡语义，真实物理暂时保持不遮挡
5. 增加配置项，让 `room_server_01` 可以控制 AOI 参数
6. 最后再考虑空间索引，等单房间人数或压测数据证明需要

## 本阶段不做的事

- 不直接接真实 PhysX
- 不做复杂地图资源加载
- 不做客户端预测和插值
- 不做多人团队/阵营可见性规则
- 不做 Redis 或跨 roomserver 的 AOI

## 验证清单

每次 AOI 升级后至少执行：

```bash
go test ./src/roomserver/logic
go test ./...
```

如果涉及启动链路，再执行：

```bash
go run ./src/roomserver/cmd
```

如果接入遮挡检测，需要额外用 fake physics world 覆盖：

- 无遮挡目标可见
- 有遮挡目标不可见
- 距离外目标不会触发 raycast
- 视锥外目标不会触发 raycast
