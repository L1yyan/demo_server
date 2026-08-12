# roomserver 重进房间后 playerRooms 索引误清理修复方案

## 需求理解
玩家从 roomserver 对局结束或退回匹配界面后，快速重新匹配并入房，重进后可以短暂移动，但玩一会后 roomserver 开始打印 `push player input failed ... player room not found`，玩家不能动。现象说明新 session 已成功入房并发送输入，但后续某个异步清理把 `playerRooms[playerID]` 删除了。

## 影响范围
预计修改：
- `src/roomserver/logic/player.go`：给 `Player` 增加房间实例标识字段，用于区分同一个 `roomID` 的不同生命周期
- `src/roomserver/logic/room.go`：给 `Room` 增加实例标识，并在玩家加入时写入 Player
- `src/roomserver/logic/room_manager.go`：把 `playerRooms` 从 `playerID -> roomID` 升级为 `playerID -> roomSession`，清理时同时校验 roomID 和实例标识
- `src/roomserver/service/server.go`、`src/roomserver/service/session.go`：会话保存并传递房间实例标识，避免旧 session 关闭误删新索引
- `src/roomserver/logic/*test.go`、`src/roomserver/service/*test.go`：补充重进同 roomID 后旧房间结束不影响新房间的测试

## 设计方案
核心问题是 `roomID` 会被复用：matchserver 目前按 `room-01-1` 这类固定 ID 分配，旧房间结束和新房间创建可能使用同一个 roomID。仅用 `roomID` 判断清理归属不够。

修正方案：
1. 引入内部房间实例标识 `instanceID`，每次 `NewRoomWithOptions` 创建房间时生成一次
2. `RoomManager.playerRooms` 改为记录 `{roomID, instanceID}`，不再只保存字符串 roomID
3. `JoinRoom` 成功写入当前 room 的 `instanceID`
4. `finishRoom` 删除玩家索引时，只有 `roomID` 和 `instanceID` 都匹配才删除，避免旧房间结束清理删掉同 roomID 的新房间玩家索引
5. `Session` 记录 `roomInstanceID`，`HandleSessionClosed` 调 `LeaveRoom(playerID, roomID, roomInstanceID)`
6. `LeaveRoom` 同样同时校验 `roomID` 和 `instanceID`，旧 session 关闭不会误删新 session 索引
7. `handleJoinRoom` 保持入房成功后再绑定 session，并把 `roomInstanceID` 一起绑定

## 兼容性
不修改 proto、不修改客户端协议、不改变外部 roomID、matchID、token 格式。新增的 instanceID 只在 roomserver 内部使用，对客户端无感知。

## 健壮性
- 清理逻辑从“按 playerID/roomID 删除”变为“按 playerID + roomID + instanceID 删除”
- 旧房间 `finishRoom`、旧 session `HandleSessionClosed`、新房间复用同 roomID 三者并发时，只有当前实例能清理自己的索引
- 如果旧测试或手动构造没有 instanceID，提供兼容路径，但生产路径必须带 instanceID

## 性能考虑
`playerRooms` value 从 string 变成小结构体，查询仍是 O(1)。`finishRoom` 仍按当前 map 扫描，和现有实现一致。instanceID 生成只发生在房间创建时，不在每帧路径。

## 验证方式
- 运行 `go test ./src/roomserver/logic ./src/roomserver/service`
- 新增测试覆盖：同一个 roomID 的旧 room 执行 `finishRoom` 时，不会删除新 room 已写入的 `playerRooms` 索引
- 保留已有对局结束清理测试，确认当前房间结束仍能清理自己的索引

## 自我审查
上一版只用 roomID 校验 `LeaveRoom`，遗漏了 matchserver 会复用 roomID 的事实，也遗漏了 `finishRoom` 才是房间结束时的主清理路径。只靠 roomID 无法区分旧房间和新房间生命周期，因此问题仍可能复现。

本方案不引入跨服务通知和协议变更，避免扩大改动面；只在 roomserver 内部为索引增加实例维度，能直接解决“同 roomID 新旧房间清理串扰”。风险是测试里直接访问 `playerRooms` 的断言需要同步调整，但这是内部结构变化，影响范围可控。

## 最终方案
按上述 instanceID 方案修改 roomserver 内部索引，重点修复 `finishRoom` 和 `LeaveRoom` 两个清理入口。等待确认后实施。
