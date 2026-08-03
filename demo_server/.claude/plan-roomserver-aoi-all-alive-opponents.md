# Roomserver AOI 双人对局可见性修复方案

## 需求理解

当前 1v1 房间中，服务端 `SimpleAOIFilter` 会按距离和水平视野角过滤其他玩家。日志显示对手距离未超、仍存活，但因为相对角度超过 `ViewAngle / 2 = 60` 被过滤，导致 snapshot 中看不到对手。

目标是让双人对局稳定同步对手状态：只要对手存活，就应进入 snapshot，不再因为视野角被过滤。

## 影响范围

预计修改：

- `src/roomserver/logic/aoi.go`
  - 调整 `SimpleAOIFilter.FilterVisible` 的过滤规则。
  - 保留 nil、自身、死亡过滤。
  - 去掉或旁路水平视野角过滤。
  - 可选择保留距离过滤，作为基础保护。

可能新增或修改：

- `src/roomserver/logic/aoi_test.go`
  - 增加 AOI 测试，覆盖“角度超过原 60 度但仍应可见”的双人对局场景。

不修改：

- 协议结构和消息 ID。
- `broadcastSnapshots` 的 snapshot 组装逻辑。
- roomserver 配置结构。
- 客户端文档。

## 设计方案

采用更直接的服务端规则：`SimpleAOIFilter` 返回所有存活、非自己的候选玩家。

考虑到后续多人或大地图仍可能需要距离保护，最终规则建议为：

```text
for candidate in candidates:
  if candidate == nil: continue
  if candidate.ID == self.ID: continue
  if !candidate.Alive: continue
  if distance > visibleDistance: continue
  visible append candidate
```

核心变化是移除当前的角度过滤：

```go
if math.Abs(angle) > viewAngle/2 {
    logAOIFiltered("angle", ...)
    continue
}
```

同时 `ViewAngle` 字段可以保留，避免对结构体使用方造成不必要改动；但 `SimpleAOIFilter` 当前不再使用它做过滤。后续如果需要真实视野 AOI，可以新建更明确的过滤器，例如 `ViewConeAOIFilter`，不要把 1v1 默认同步和视野裁剪混在一起。

## 错误处理和边界

- `self == nil`：继续返回 nil。
- `candidate == nil`：跳过。
- `candidate.ID == self.ID`：跳过自己。
- `candidate.Alive == false`：跳过死亡玩家，并保留诊断日志。
- 超出 `VisibleDistance`：仍跳过，并保留诊断日志。
- 角度不再作为过滤原因，因此不会再出现 `reason = angle` 导致对手消失。

## 兼容性影响

- 不影响消息协议和客户端解析。
- snapshot 中可见玩家数量可能增加。当前房间默认 2 人，这正是目标行为。
- 如果未来多人房间依赖视野角减少流量，这个默认策略会更宽松；但当前配置 `max_players_per_room = 2`，优先保证 1v1 同步正确。

## 性能考虑

- 当前房间最多 2 人，遍历候选玩家成本可忽略。
- 移除角度过滤会减少少量三角函数计算。
- 保留距离计算，避免未来配置误扩房间人数时无限制同步所有远处对象。

## 验证方式

1. 运行单元测试：

```bash
go test ./src/roomserver/logic
```

2. 编译全部服务：

```bash
./scripts/build_all.sh
```

3. 重启服务后联调：

- 两个玩家入房。
- 即使双方 yaw 都为 0、相对角度超过原来的 60 度，对手仍应出现在 `MsgSnapshot.players` 中。
- roomserver 日志不应再出现该对手被 `reason = angle` 过滤。

## 自我审查

- 是否遗漏已有结构：已确认 AOI 只通过 `AOIFilter.FilterVisible` 接口接入 `Room.broadcastSnapshots`，改 `SimpleAOIFilter` 即可影响当前 roomserver。
- 是否过度设计：不新增配置项，不新增复杂过滤器，改动集中。
- 协议兼容风险：无协议变更。
- 错误处理风险：nil、自己、死亡、距离仍处理。
- 性能风险：无，反而少一次角度过滤。
- 扩展风险：保留 `ViewAngle` 字段可能让字段语义变弱，但比删除字段更稳。后续若要恢复视野锥，应新增专用 AOI 策略。

## 最终方案

修改 `SimpleAOIFilter.FilterVisible`：默认 AOI 不再按视野角过滤玩家，只按非空、非自己、存活和距离过滤。新增测试确保角度超过原视野半角时仍可见。验证通过后再按需要重启服务。

等待确认后实施。
