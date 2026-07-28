# 客户端预测、服务端权威纠偏接入说明

本文档只说明客户端需要完成的工作，便于客户端侧按当前 roomserver 协议接入预测、回滚和重放。

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

## 3. 入房响应

服务端返回 `MsgJoinRoomAck = 2`。客户端需要读取这些字段：

```json
{
  "ok": true,
  "room_id": "room-1001",
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
- 使用 `tick` 和 `server_time` 初始化服务端帧估算。
- 校验 `map_id` 和 `physics_hash`。
- 只有 `sync_mode == "prediction_authoritative"` 时启用预测回滚。
- 用 `rollback_window_ticks` 决定本地输入和预测状态历史保留窗口。

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

- `client_tick` 是本地逻辑帧号。
- `move_x` 和 `move_z` 范围保持在 `[-1, 1]`，斜向移动客户端也应归一化。
- `yaw` 建议归一到 `[-180, 180]`。
- `pitch` 限制在 `[-89, 89]`。
- `fire` 只表示当前输入帧是否开火，不要把一次点击扩展成多帧 true。
- `predicted_state` 至少按 `prediction_keyframe_interval` 上报一次。联调初期可以每帧带上，方便观察误差。

服务端只接受窗口内输入：

```text
server_tick - rollback_window_ticks <= client_tick <= server_tick + future_input_window_ticks
```

太旧输入不会改写服务端历史，太未来输入会被丢弃。

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
1. 找到 rollback_tick
2. 用 state 覆盖本地模拟层状态
3. 从 rollback_tick + 1 到 latestLocalTick 逐帧读取 inputHistory 并重放
4. 更新 predictedStateHistory
5. 渲染层从旧显示位置平滑到新模拟位置
```

注意：

- 模拟层必须立即服从服务端权威状态。
- 渲染层可以做短时间平滑，减少画面瞬移感。
- 如果缺少 rollback_tick 之后的输入历史，应以服务端 state 为准，并重新对齐本地 tick。
- 如果连续收到 correction，优先处理最新的 `server_tick`，避免旧 correction 覆盖新状态。

## 9. 其他玩家同步

第一版只预测本地玩家。其他玩家建议使用服务端 `MsgSnapshot = 6` 做插值：

```text
自己：输入预测 + correction 回滚重放
其他玩家：服务端快照 + 插值，必要时短外推
```

不要预测其他玩家移动，因为客户端没有其他玩家的真实输入，只能基于快照插值。

## 10. 客户端实现清单

客户端侧建议拆成这些模块：

1. `NetworkRoomClient`：KCP 消息收发，处理 MsgJoinRoomAck、MsgHeartbeatAck、MsgSnapshot、MsgInputAck、MsgStateCorrection。
2. `TickSync`：根据 JoinRoomAck 和 HeartbeatAck 估算服务端 tick，微调本地 tick。
3. `InputHistory`：保存每 tick 输入，支持按 ack 清理和按 rollback tick 读取。
4. `PredictionHistory`：保存每 tick 预测状态，支持纠偏后重写。
5. `LocalPlayerPredictor`：按 `tick_rate`、移动速度、碰撞数据模拟本地玩家。
6. `RollbackReplayer`：收到 correction 后覆盖权威状态并重放历史输入。
7. `RemotePlayerInterpolator`：对其他玩家使用 snapshot 插值。
8. `PredictionDebugPanel`：联调期显示 position_error、angle_error、correction 次数和 physics_hash。

## 11. 联调验证场景

建议至少验证：

1. 正常直线移动时，本地预测和服务端 snapshot 基本一致，correction 次数很少。
2. 客户端故意把预测位置偏移 1 米，服务端会发送 StateCorrection。
3. 客户端 physics_hash 错误时，JoinRoomAck 返回 `snapshot_only`。
4. 丢包或延迟抖动时，移动输入可短暂延续，但 `fire` 不会被重复触发。
5. 批量重复发送未 ack 输入时，同一 tick 不会被后到包覆盖。
6. 其他玩家只通过 snapshot 插值展示，不走预测重放。
