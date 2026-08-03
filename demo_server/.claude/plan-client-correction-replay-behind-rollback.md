# 客户端 correction replay 后持续落后修复方案

## 需求理解

当前现象：

- 刚进游戏不开弱网，不刷纠偏。
- 开弱网后出现 `late_input_reschedule` 纠偏，属于预期。
- 关闭弱网后仍持续刷：

```text
Prediction correction replayed. reason=late_input_reschedule rollback=937 server=937 posErr=0.000 angleErr=0.000
```

服务端日志显示此时刷的是：

```text
reason=late_input_reschedule
rollback_tick=server_tick
position_error=0
last_accepted_input_tick 比 server_tick 落后约 4 tick
```

这说明服务端不是在报真实位置误差，而是在提示“输入长期迟到后按服务端当前权威状态重同步”。问题出在客户端处理这种 correction 时：`ReplayFromCorrection` 即使本地 `latestTick < correction.rollback_tick`，也会返回成功，然后把 `clientTick` 强制回本地较旧的 `latestTick`。结果客户端 tick 继续落后服务端，后续输入继续迟到，服务端继续发 `late_input_reschedule`，形成反馈环。

## 影响范围

预计修改客户端：

- `/mnt/c/Users/liyan1/codes/democlient/demo_client/Assets/Scripts/Game/RoomSessionBehaviour.cs`
  - 修正 `ReplayFromCorrection` 对 `latestTick < rollback_tick` 的处理。
  - 这种情况下不应视为 replay 成功，应走 fallback resync，把本地 tick 对齐到 `server_tick + inputLeadTicks`。

可能修改客户端：

- `/mnt/c/Users/liyan1/codes/democlient/demo_client/Assets/Scripts/Game/Prediction/PredictionStats.cs`
  - 暂不需要，除非后续发现 stale correction 过滤也有问题。

暂不修改服务端：

- 服务端当前 `late_input_reschedule` 代表“服务端输入重排后的权威重同步”，它本身不是位置误差。
- 这次持续刷的直接原因是客户端把当前服务端 tick 的 correction replay 成功后，又把 `clientTick` 拉回旧 tick。

## 设计方案

### 1. 修正 ReplayFromCorrection 的前置条件

当前逻辑：

```csharp
var latestTick = Math.Max(clientTick, inputHistory.LatestTick);
for (var tick = correction.rollback_tick + 1; tick <= latestTick; tick++)
{
    ...
}
clientTick = latestTick;
tickSync.ForceLocalTick(clientTick);
return true;
```

当 `latestTick < correction.rollback_tick` 时，循环不会执行，但函数仍返回 true，并把 `clientTick` 设置成较旧的 latestTick。这会让客户端持续落后服务端。

修正为：

```csharp
var latestTick = Math.Max(clientTick, inputHistory.LatestTick);
if (latestTick < correction.rollback_tick)
{
    return false;
}
```

这样会进入现有 fallback 分支：

```csharp
simulationState = FromPlayerState(correction.state);
clientTick = correction.server_tick + tickSync.InputLeadTicks;
tickSync.ForceLocalTick(clientTick);
CleanupAfter(correction.rollback_tick + 1);
```

这正是 `late_input_reschedule` 这类当前权威重同步需要的行为：接受服务端当前状态，并把本地输入 tick 重新拉到服务端前方。

### 2. 保留正常 rollback replay 行为

如果 `latestTick >= rollback_tick`，说明客户端有 rollback tick 之后的输入历史，继续按原逻辑逐帧 replay。

如果中间缺输入历史，仍返回 false，走 fallback。

### 3. 日志语义

修复后，这类情况不应再打印：

```text
Prediction correction replayed. reason=late_input_reschedule ...
```

而应打印一次 fallback：

```text
Prediction resynced from correction without full replay. reason=late_input_reschedule ...
```

随后客户端 tick 应对齐到 `server_tick + inputLeadTicks`，服务端的 `late_input_reschedule` 应快速停止。

## 兼容性

- 不改协议。
- 不改服务端接口。
- 只改变客户端在“本地历史已经落后于 correction rollback tick”时的处理。
- 对正常 position_error 回滚重放没有影响。

## 健壮性

- rollback tick 早于历史窗口或本地缺历史时，继续走 fallback。
- fallback 会清理 rollback 之后的本地历史，避免旧输入继续污染后续预测。
- correction stale 过滤仍由 `PredictionStats.ShouldAcceptCorrection` 保持。

## 性能考虑

- 只是新增一个条件判断，无性能影响。

## 验证方式

1. 修改客户端 `RoomSessionBehaviour.cs`。
2. 如果 Unity 工程当前不能在命令行完整编译，说明原因；至少做源码级检查。
3. 重启/重新进入游戏测试：
   - 不开弱网：无 correction 刷屏。
   - 开弱网：允许出现少量 `late_input_reschedule`。
   - 关闭弱网：`late_input_reschedule` 应快速停止，不再持续刷。
4. 同时看 roomserver 日志：`late_input_reschedule` 数量应在弱网关闭后收敛。

## 自我审查

- 这次不应继续扩大服务端窗口，日志显示输入迟到反馈环已由客户端 correction replay 把 tick 拉回旧值导致。
- 不应让客户端忽略 `late_input_reschedule`，否则弱网时权威重同步会失效。
- 正确做法是：当无法从 correction.rollback_tick replay 到本地当前 tick 时，承认 replay 不成立，走现有 fallback resync。

## 最终方案

修客户端 `ReplayFromCorrection`：当 `latestTick < correction.rollback_tick` 时返回 false，触发现有 fallback resync，把 `clientTick` 对齐到 `server_tick + InputLeadTicks`。这样关闭弱网后客户端能重新领先服务端发送输入，服务端不再持续 late reschedule correction。

等待确认后实施。
