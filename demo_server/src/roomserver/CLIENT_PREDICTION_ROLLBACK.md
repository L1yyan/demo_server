# 客户端预测、服务端权威纠偏接入说明

本文档只说明客户端需要完成的工作，便于客户端侧按当前 roomserver 协议接入预测、回滚和重放。

所有 roomserver 业务消息都跑在 KCP 连接之上。KCP 当前开启 stream mode，所以客户端必须按服务端业务帧格式拆包和组包，不能把一次 KCP read 当成一条完整业务消息。

```text
uint16 message_type      # 2 字节，大端序
uint32 payload_length    # 4 字节，大端序
bytes  payload           # JSON payload
```

当前 payload 使用 JSON 编码。

消息类型表：

| ID | 名称 | 方向 | 用途 |
| --- | --- | --- | --- |
| 1 | MsgJoinRoom | 客户端 -> 服务端 | 请求入房 |
| 2 | MsgJoinRoomAck | 服务端 -> 客户端 | 入房响应 |
| 3 | MsgHeartbeat | 客户端 -> 服务端 | 心跳和时间对齐 |
| 4 | MsgHeartbeatAck | 服务端 -> 客户端 | 心跳响应 |
| 5 | MsgPlayerInput | 客户端 -> 服务端 | 旧单帧输入，预测客户端不推荐使用 |
| 6 | MsgSnapshot | 服务端 -> 客户端 | 服务端状态快照 |
| 7 | MsgError | 服务端 -> 客户端 | 错误响应 |
| 8 | MsgPlayerInputBatch | 客户端 -> 服务端 | 批量输入和预测关键帧 |
| 9 | MsgInputAck | 服务端 -> 客户端 | 输入确认 |
| 10 | MsgStateCorrection | 服务端 -> 客户端 | 权威状态纠偏 |

实现时不要自行新增字段或改字段名。JSON 字段名以本文档为准，客户端可以忽略暂不使用的服务端字段，但不能依赖文档外的隐式字段。

## 1. 接入目标

客户端要做到：

```text
采集输入
  -> 本地立即模拟并记录预测状态
  -> 批量上报输入和关键帧预测状态
  -> 接收 InputAck 删除旧历史
  -> 接收 StateCorrection 回滚到服务端权威帧
  -> 用本地历史输入快速重放到当前本地帧
```

服务端仍然是最终权威。客户端上报的坐标、状态和 hash 只用于误差检测，不能当作服务端认可的最终状态。

AI 实现约束：

- 不要改 roomserver 协议字段名、消息 ID 或 JSON 结构。
- 不要预测其他玩家，只对本地玩家做预测、回滚和重放。
- 模拟层状态和渲染层表现要分离；纠偏时模拟层立即覆盖，渲染层只做视觉平滑。
- 输入历史和预测历史必须按 tick 索引，不能只保存队列尾部。
- 网络层基于 KCP 可靠有序字节流，但业务层仍要处理重复输入、延迟到达、过期 correction 和断线重连后的状态重同步。

## 2. 入房请求

客户端发送 `MsgJoinRoom = 1`，JSON payload 增加同步能力字段：

```json
{
  "token": "room token",
  "sync_version": 1,
  "prediction_enabled": true,
  "physics_hash": "sha256:e1328d5d97e68938b5c55f13a7c04553849cdefcdfed8a32ea288275464d9289"
}
```

字段说明：

- `sync_version`：当前填 `1`。
- `prediction_enabled`：客户端已实现预测和回滚时填 `true`。
- `physics_hash`：客户端加载的地图碰撞数据 hash，必须和服务端下发值一致。

如果 hash 不一致，或客户端未声明能力，服务端会返回 `sync_mode = snapshot_only`，客户端应关闭本地预测，只按 snapshot 同步。

如果 token 非法、过期、server_id 不匹配、入房或输入 payload 解析失败、尚未入房就发输入，服务端会发送 `MsgError = 7`，格式如下：

```json
{
  "code": "invalid_token",
  "content": "room token expired"
}
```

客户端收到 `MsgError` 后应按 `code` 做分支处理，并把 `content` 作为调试信息展示或记录。

## 3. 入房响应

服务端返回 `MsgJoinRoomAck = 2`。客户端需要读取这些字段：

```json
{
  "ok": true,
  "room_id": "room-1001",
  "content": "ok",
  "tick": 12,
  "spawn_id": "spawn_a",
  "x": -4,
  "y": 0.1,
  "z": 0,
  "yaw": 0,
  "pitch": 0,
  "tick_rate": 20,
  "snapshot_rate": 10,
  "server_time": 1785139200000,
  "sync_mode": "prediction_authoritative",
  "map_id": "map_001",
  "physics_hash": "sha256:...",
  "rollback_window_ticks": 60,
  "future_input_window_ticks": 8,
  "prediction_keyframe_interval": 2,
  "position_tolerance": 0.15,
  "hard_position_tolerance": 0.5,
  "angle_tolerance": 2
}
```

客户端需要：

- 使用 `tick_rate` 驱动本地逻辑模拟，不要用渲染帧率当逻辑帧率。
- 使用 `tick` 和 `server_time` 初始化服务端帧估算，并让本地预测 tick 和服务端 room tick 处在同一条时间线。
- 校验 `map_id` 和 `physics_hash`。
- 只有 `sync_mode == "prediction_authoritative"` 时启用预测回滚。
- 用 `rollback_window_ticks` 决定本地输入和预测状态历史保留窗口。

注意：`client_tick` 不是从 1 开始的本地自增序号，而是客户端估算出的 room tick。入房后应从 `JoinRoomAck.tick` 附近开始推进；心跳只用于持续修正这个估算。否则服务端会把输入判断成过旧或过未来，预测状态也无法和服务端权威帧对齐校验。

## 4. 心跳对齐

客户端发送 `MsgHeartbeat = 3`，payload 可继续使用：

```json
{
  "client_time": 1785139200100
}
```

服务端返回 `MsgHeartbeatAck = 4`：

```json
{
  "client_time": 1785139200100,
  "server_time": 1785139200120,
  "server_tick": 25
}
```

客户端用多次心跳估算 `server_tick` 和本地 tick 偏移。建议：

- 小漂移通过轻微加快或放慢本地模拟节奏修正。
- 大漂移直接对齐服务端估算帧，并准备接受 correction。
- 不要频繁硬改本地 tick，否则会造成输入历史和预测状态错位。

建议实现一个 `TickSync`：

```text
estimatedServerTick = heartbeat.server_tick + elapsedSinceHeartbeat / tickDuration
localPredictionTick = estimatedServerTick + inputLeadTicks
```

`inputLeadTicks` 建议控制在 `0..future_input_window_ticks` 内，避免输入因为网络延迟到达服务端时已经过旧，也避免超过服务端未来窗口。第一版可取 1 到 2 帧，再根据 RTT 调整。

## 5. 本地历史数据

客户端至少维护：

```text
inputHistory[tick]
predictedStateHistory[tick]
lastAckedInputTick
latestLocalTick
estimatedServerTick
```

每个本地 tick：

```text
1. 采集输入
2. 写入 inputHistory[tick]
3. 用客户端角色控制和碰撞数据本地模拟一帧
4. 写入 predictedStateHistory[tick]
5. 将未 ack 的输入按 batch 发送给服务端
```

历史至少保留 `rollback_window_ticks` 范围。为了抗丢包，可以重复发送尚未 ack 的最近几帧，但单包帧数不能超过服务端配置的 `max_input_batch_frames`，当前默认是 8。

`predictedStateHistory[tick]` 应保存该 tick 输入模拟完成后的状态，也就是和上报的 `predicted_state` 同一帧语义一致。

本地预测移动规则必须尽量和服务端一致：

```text
move_x / move_z clamp 到 [-1, 1]
如果 sqrt(move_x^2 + move_z^2) > 1，则归一化
yaw 归一到 [-180, 180]
pitch clamp 到 [-89, 89]
移动方向：
  forward = (sin(yaw), 0, cos(yaw))
  right   = (cos(yaw), 0, -sin(yaw))
  direction = forward * move_z + right * move_x
移动距离：4.0 / tick_rate
```

服务端最终位置由物理后端按地图碰撞修正。客户端必须使用同一份地图碰撞数据；如果客户端物理和服务端物理无法完全一致，应依赖 `StateCorrection` 收敛，不要为了贴合 snapshot 私自改模拟规则。

## 6. 批量输入上报

新客户端使用 `MsgPlayerInputBatch = 8`：

```json
{
  "base_client_tick": 101,
  "last_received_server_tick": 95,
  "frames": [
    {
      "client_tick": 101,
      "move_x": 0,
      "move_z": 1,
      "yaw": 0,
      "pitch": 0,
      "fire": false,
      "predicted_state": {
        "x": -4,
        "y": 0.1,
        "z": 0.2,
        "yaw": 0,
        "pitch": 0,
        "state_hash": 0
      }
    }
  ]
}
```

发送规则：

- `client_tick` 是与服务端 room tick 对齐的本地预测帧号，每一帧都应显式填写。
- `base_client_tick` 当前只作为单帧缺省 `client_tick` 的兜底值，服务端不会按数组下标自动展开连续 tick。
- `last_received_server_tick` 当前服务端暂不参与逻辑处理，可填写客户端最近收到的 snapshot 或 ack 的 `server_tick`，便于后续扩展和联调观测。
- `move_x` 和 `move_z` 范围保持在 `[-1, 1]`，斜向移动客户端也应归一化。
- `yaw` 建议归一到 `[-180, 180]`。
- `pitch` 限制在 `[-89, 89]`。
- `fire` 只表示当前输入帧是否开火，不要把一次点击扩展成多帧 true。
- `predicted_state` 至少按 `prediction_keyframe_interval` 上报一次。当前服务端只在 `client_tick % prediction_keyframe_interval == 0` 的帧做预测误差校验；例如间隔为 2 时，应保证偶数 tick 带预测状态。联调初期可以每帧带上，方便观察误差。
- 同一 `client_tick` 的重复输入服务端只保留先到的一帧，后到包不会覆盖已缓存或已处理输入。

服务端只接受窗口内输入：

```text
server_tick - rollback_window_ticks <= client_tick <= server_tick + future_input_window_ticks
```

太旧输入不会改写服务端历史，预测模式下可能触发 `reason = "stale_input"` 的纠偏；太未来输入会被丢弃。单包帧数为 0 或超过 `max_input_batch_frames` 时，service 层会返回 `MsgError`。

如果某个服务端 tick 没收到精确输入，服务端会短暂沿用上一帧移动输入，当前默认最多沿用 3 tick，但沿用帧会强制 `fire = false`。客户端不能依赖沿用作为正常移动机制，仍应持续发送每一帧输入。

## 7. InputAck 处理

服务端按快照频率向预测模式客户端发送 `MsgInputAck = 9`：

```json
{
  "server_tick": 120,
  "last_accepted_input_tick": 118,
  "last_verified_input_tick": 116
}
```

客户端处理：

- 更新 `lastAckedInputTick`。
- 删除不再可能用于回滚的历史输入和预测状态。
- 保留 rollback 窗口内必要历史，避免 correction 到来时没有可重放输入。
- `last_accepted_input_tick` 表示服务端已经接收入队的最大输入 tick，不等于这些输入都已经被模拟执行。
- `last_verified_input_tick` 表示服务端已经校验过预测状态的最大 tick，只会在收到对应预测状态并完成校验后推进。

建议删除条件：

```text
tick <= last_accepted_input_tick - rollback_window_ticks
```

不要收到 ack 就立即删除所有 `<= last_accepted_input_tick` 的预测状态，否则晚到 correction 可能无法回滚。

## 8. StateCorrection 处理

服务端发现预测误差超阈值时发送 `MsgStateCorrection = 10`：

```json
{
  "player_id": 1001,
  "rollback_tick": 116,
  "server_tick": 120,
  "last_accepted_input_tick": 118,
  "state": {
    "player_id": 1001,
    "spawn_id": "spawn_a",
    "x": -3.8,
    "y": 0.1,
    "z": 1.2,
    "yaw": 0,
    "pitch": 0,
    "hp": 100
  },
  "reason": "position_error",
  "position_error": 0.7,
  "angle_error": 0
}
```

客户端处理流程：

```text
1. 如果 `server_tick` 小于或等于已处理的最新 correction，直接丢弃
2. 用 `state` 覆盖 `rollback_tick` 对应的本地模拟层状态
3. 从 `rollback_tick + 1` 到 `latestLocalTick` 逐帧读取 inputHistory 并重放
4. 更新 replay 过程中每一帧的 `predictedStateHistory`
5. 渲染层从旧显示位置平滑到新模拟位置
```

注意：

- 模拟层必须立即服从服务端权威状态。
- 渲染层可以做短时间平滑，减少画面瞬移感。
- 如果 `rollback_tick` 已经早于本地保留历史，或缺少 `rollback_tick` 之后的输入历史，应以服务端 state 为准，并重新对齐本地 tick。
- 如果连续收到 correction，只处理 `server_tick` 更新的 correction，避免旧 correction 覆盖新状态。

## 9. 其他玩家同步

第一版只预测本地玩家。其他玩家建议使用服务端 `MsgSnapshot = 6` 做插值：

```text
自己：输入预测 + correction 回滚重放
其他玩家：服务端快照 + 插值，必要时短外推
```

不要预测其他玩家移动，因为客户端没有其他玩家的真实输入，只能基于快照插值。

服务端 snapshot 当前至少包含玩家自己，也会包含 AOI 可见的其他玩家。预测模式下，本地玩家不要每次收到 snapshot 就直接覆盖模拟层；本地玩家应主要通过 `StateCorrection` 纠偏，snapshot 中自己的状态可用于调试、误差展示，或在本地历史缺失时做兜底重同步。

`MsgSnapshot = 6` payload 格式：

```json
{
  "server_tick": 120,
  "players": [
    {
      "player_id": 1001,
      "spawn_id": "spawn_a",
      "x": -3.8,
      "y": 0.1,
      "z": 1.2,
      "yaw": 0,
      "pitch": 0,
      "hp": 100
    }
  ]
}
```

## 10. 客户端实现清单

客户端侧建议拆成这些模块：

1. `NetworkRoomClient`：KCP 业务帧拆包组包和消息收发，处理 MsgJoinRoomAck、MsgHeartbeatAck、MsgSnapshot、MsgInputAck、MsgStateCorrection、MsgError。
2. `TickSync`：根据 JoinRoomAck 和 HeartbeatAck 估算服务端 tick，微调本地 tick。
3. `InputHistory`：保存每 tick 输入，支持按 ack 清理和按 rollback tick 读取。
4. `PredictionHistory`：保存每 tick 预测状态，支持纠偏后重写。
5. `LocalPlayerPredictor`：按 `tick_rate`、移动速度、碰撞数据模拟本地玩家。
6. `RollbackReplayer`：收到 correction 后覆盖权威状态并重放历史输入。
7. `RemotePlayerInterpolator`：对其他玩家使用 snapshot 插值。
8. `PredictionDebugPanel`：联调期显示 position_error、angle_error、correction 次数和 physics_hash。

AI 实现时建议按这个顺序落地：

```text
1. 先实现 KCP 业务帧拆包组包和 JSON 消息分发
2. 跑通 JoinRoom、Heartbeat、Snapshot、MsgError
3. 实现 TickSync，让 client_tick 与服务端 room tick 对齐
4. 实现输入采集、InputHistory、PlayerInputBatch 发送和 InputAck 清理
5. 实现本地预测移动和 PredictionHistory
6. 实现 StateCorrection 回滚重放
7. 最后实现其他玩家 snapshot 插值和调试面板
```

不要一开始就把插值、预测、回滚和渲染平滑混在一个类里，否则后续很难定位误差来源。

## 11. 联调验证场景

建议至少验证：

1. 正常直线移动时，本地预测和服务端 snapshot 基本一致，correction 次数很少。
2. 客户端故意把预测位置偏移 1 米，服务端会发送 StateCorrection。
3. 客户端 physics_hash 错误时，JoinRoomAck 返回 `snapshot_only`。
4. 丢包或延迟抖动时，移动输入可短暂延续，但 `fire` 不会被重复触发。
5. 批量重复发送未 ack 输入时，同一 tick 不会被后到包覆盖。
6. 其他玩家只通过 snapshot 插值展示，不走预测重放。
