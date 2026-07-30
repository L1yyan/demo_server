package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	messageHeaderSize = 6 // 消息头长度
)

const (
	// MsgJoinRoom 请求加入房间
	MsgJoinRoom uint16 = 1
	// MsgJoinRoomAck 加入房间响应
	MsgJoinRoomAck uint16 = 2
	// MsgHeartbeat 心跳请求
	MsgHeartbeat uint16 = 3
	// MsgHeartbeatAck 心跳响应
	MsgHeartbeatAck uint16 = 4
	// MsgPlayerInput 玩家输入
	MsgPlayerInput uint16 = 5
	// MsgSnapshot 状态快照
	MsgSnapshot uint16 = 6
	// MsgError 错误响应
	MsgError uint16 = 7
	// MsgPlayerInputBatch 批量玩家输入
	MsgPlayerInputBatch uint16 = 8
	// MsgInputAck 输入处理确认
	MsgInputAck uint16 = 9
	// MsgStateCorrection 权威状态纠偏
	MsgStateCorrection uint16 = 10
	// MsgGameStart 对局开始通知
	MsgGameStart uint16 = 11
	// MsgGameOver 对局结束通知
	MsgGameOver uint16 = 12
	// MsgPlayerStatsQuery 查询玩家战绩
	MsgPlayerStatsQuery uint16 = 13
	// MsgPlayerStatsResp 玩家战绩响应
	MsgPlayerStatsResp uint16 = 14
)

var (
	// ErrPayloadTooLarge 表示消息负载超过限制
	ErrPayloadTooLarge = errors.New("payload too large")
	// ErrInvalidMessage 表示消息格式非法
	ErrInvalidMessage = errors.New("invalid message")
)

// Message KCP 业务消息
type Message struct {
	Type    uint16 // 消息类型
	Payload []byte // 消息负载
}

// JoinRoomRequest 加入房间请求
type JoinRoomRequest struct {
	Token             string `json:"token"`              // 入房令牌
	SyncVersion       int    `json:"sync_version"`       // 同步协议版本
	PredictionEnabled bool   `json:"prediction_enabled"` // 是否请求预测模式
	PhysicsHash       string `json:"physics_hash"`       // 客户端物理数据hash
}

// JoinRoomAck 加入房间响应
type JoinRoomAck struct {
	OK                         bool    `json:"ok"`                           // 是否成功
	RoomID                     string  `json:"room_id"`                      // 房间ID
	Content                    string  `json:"content"`                      // 响应信息
	Tick                       int64   `json:"tick"`                         // 当前房间帧号
	SpawnID                    string  `json:"spawn_id"`                     // 出生点ID
	X                          float64 `json:"x"`                            // 初始X坐标
	Y                          float64 `json:"y"`                            // 初始Y坐标
	Z                          float64 `json:"z"`                            // 初始Z坐标
	Yaw                        float64 `json:"yaw"`                          // 初始水平视角
	Pitch                      float64 `json:"pitch"`                        // 初始垂直视角
	TickRate                   int     `json:"tick_rate"`                    // 房间逻辑帧率
	SnapshotRate               int     `json:"snapshot_rate"`                // 快照发送频率
	ServerTime                 int64   `json:"server_time"`                  // 服务端时间戳
	SyncMode                   string  `json:"sync_mode"`                    // 实际同步模式
	MapID                      string  `json:"map_id"`                       // 地图ID
	PhysicsHash                string  `json:"physics_hash"`                 // 服务端物理数据hash
	RollbackWindowTicks        int64   `json:"rollback_window_ticks"`        // 回滚历史窗口
	FutureInputWindowTicks     int64   `json:"future_input_window_ticks"`    // 允许未来输入窗口
	PredictionKeyframeInterval int64   `json:"prediction_keyframe_interval"` // 预测关键帧间隔
	PositionTolerance          float64 `json:"position_tolerance"`           // 位置误差阈值
	HardPositionTolerance      float64 `json:"hard_position_tolerance"`      // 硬纠偏位置阈值
	AngleTolerance             float64 `json:"angle_tolerance"`              // 角度误差阈值
	GameDurationSeconds        int64   `json:"game_duration_seconds"`        // 对局时长秒数
	GameStarted                bool    `json:"game_started"`                 // 是否已开始对局
	GameStartTick              int64   `json:"game_start_tick"`              // 对局开始帧号
	GameEndTick                int64   `json:"game_end_tick"`                // 对局结束帧号
}

// Heartbeat 心跳消息
type Heartbeat struct {
	ClientTime int64 `json:"client_time"` // 客户端时间戳
	ServerTime int64 `json:"server_time"` // 服务端时间戳
	ServerTick int64 `json:"server_tick"` // 服务端房间帧号
}

// PlayerInput 玩家输入消息
type PlayerInput struct {
	ClientTick int64   `json:"client_tick"` // 客户端帧号
	MoveX      float64 `json:"move_x"`      // 左右移动输入
	MoveZ      float64 `json:"move_z"`      // 前后移动输入
	Yaw        float64 `json:"yaw"`         // 水平视角
	Pitch      float64 `json:"pitch"`       // 垂直视角
	Fire       bool    `json:"fire"`        // 是否开火
}

// PlayerInputBatch 批量玩家输入消息
type PlayerInputBatch struct {
	BaseClientTick         int64              `json:"base_client_tick"`          // 批量起始客户端帧号
	Frames                 []PlayerInputFrame `json:"frames"`                    // 输入帧列表
	LastReceivedServerTick int64              `json:"last_received_server_tick"` // 客户端已收到的服务端帧号
}

// PlayerInputFrame 单帧玩家输入
type PlayerInputFrame struct {
	ClientTick     int64                 `json:"client_tick"`               // 客户端帧号
	MoveX          float64               `json:"move_x"`                    // 左右移动输入
	MoveZ          float64               `json:"move_z"`                    // 前后移动输入
	Yaw            float64               `json:"yaw"`                       // 水平视角
	Pitch          float64               `json:"pitch"`                     // 垂直视角
	Fire           bool                  `json:"fire"`                      // 是否开火
	PredictedState *PredictedPlayerState `json:"predicted_state,omitempty"` // 客户端预测状态
}

// PredictedPlayerState 客户端预测玩家状态
type PredictedPlayerState struct {
	X         float64 `json:"x"`                    // X坐标
	Y         float64 `json:"y"`                    // Y坐标
	Z         float64 `json:"z"`                    // Z坐标
	Yaw       float64 `json:"yaw"`                  // 水平视角
	Pitch     float64 `json:"pitch"`                // 垂直视角
	StateHash uint32  `json:"state_hash,omitempty"` // 预测状态hash
}

// PlayerState 玩家快照状态
type PlayerState struct {
	PlayerID   uint64  `json:"player_id"`   // 玩家ID
	SpawnID    string  `json:"spawn_id"`    // 出生点ID
	X          float64 `json:"x"`           // X坐标
	Y          float64 `json:"y"`           // Y坐标
	Z          float64 `json:"z"`           // Z坐标
	Yaw        float64 `json:"yaw"`         // 水平视角
	Pitch      float64 `json:"pitch"`       // 垂直视角
	HP         int     `json:"hp"`          // 生命值
	KillCount  int     `json:"kill_count"`  // 击杀数量
	DeathCount int     `json:"death_count"` // 死亡数量
}

// Snapshot 状态快照
type Snapshot struct {
	ServerTick int64         `json:"server_tick"` // 服务端帧号
	Players    []PlayerState `json:"players"`     // 可见玩家状态
}

// InputAck 输入处理确认
type InputAck struct {
	ServerTick            int64 `json:"server_tick"`              // 服务端当前帧号
	LastAcceptedInputTick int64 `json:"last_accepted_input_tick"` // 最后接受的输入帧号
	LastVerifiedInputTick int64 `json:"last_verified_input_tick"` // 最后校验的输入帧号
}

// StateCorrection 权威状态纠偏
type StateCorrection struct {
	PlayerID              uint64      `json:"player_id"`                // 玩家ID
	RollbackTick          int64       `json:"rollback_tick"`            // 客户端回滚帧号
	ServerTick            int64       `json:"server_tick"`              // 服务端当前帧号
	LastAcceptedInputTick int64       `json:"last_accepted_input_tick"` // 最后接受的输入帧号
	State                 PlayerState `json:"state"`                    // 权威玩家状态
	Reason                string      `json:"reason"`                   // 纠偏原因
	PositionError         float64     `json:"position_error"`           // 位置误差
	AngleError            float64     `json:"angle_error"`              // 角度误差
}

// GameStart 对局开始通知
type GameStart struct {
	RoomID          string `json:"room_id"`          // 房间ID
	ServerTick      int64  `json:"server_tick"`      // 服务端当前帧号
	StartTick       int64  `json:"start_tick"`       // 对局开始帧号
	EndTick         int64  `json:"end_tick"`         // 对局结束帧号
	DurationSeconds int64  `json:"duration_seconds"` // 对局时长秒数
	ServerTime      int64  `json:"server_time"`      // 服务端时间戳
}

// GameOver 对局结束通知
type GameOver struct {
	RoomID     string        `json:"room_id"`     // 房间ID
	ServerTick int64         `json:"server_tick"` // 服务端当前帧号
	StartTick  int64         `json:"start_tick"`  // 对局开始帧号
	EndTick    int64         `json:"end_tick"`    // 对局结束帧号
	Reason     string        `json:"reason"`      // 结束原因
	ServerTime int64         `json:"server_time"` // 服务端时间戳
	Players    []PlayerState `json:"players"`     // 结束时玩家状态
}

// PlayerStatsQuery 玩家战绩查询请求
type PlayerStatsQuery struct {
	PlayerID uint64 `json:"player_id,omitempty"` // 目标玩家ID，为0时查询自己
}

// PlayerStats 玩家战绩数据
type PlayerStats struct {
	PlayerID   uint64 `json:"player_id"`   // 玩家ID
	KillCount  int    `json:"kill_count"`  // 击杀数量
	DeathCount int    `json:"death_count"` // 死亡数量
}

// PlayerStatsResp 玩家战绩查询响应
type PlayerStatsResp struct {
	OK         bool        `json:"ok"`          // 是否成功
	Content    string      `json:"content"`     // 响应信息
	RoomID     string      `json:"room_id"`     // 房间ID
	ServerTick int64       `json:"server_tick"` // 服务端当前帧号
	Stats      PlayerStats `json:"stats"`       // 玩家战绩
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Code    string `json:"code"`    // 错误码
	Content string `json:"content"` // 错误信息
}

// NewJSONMessage 创建 JSON 业务消息
func NewJSONMessage(messageType uint16, value any) (Message, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return Message{}, fmt.Errorf("marshal message: %w", err)
	}
	return Message{Type: messageType, Payload: payload}, nil
}

// DecodeJSON 解码 JSON 消息负载
func DecodeJSON[T any](message Message) (T, error) {
	var value T
	if len(message.Payload) == 0 {
		return value, ErrInvalidMessage
	}
	if err := json.Unmarshal(message.Payload, &value); err != nil {
		return value, fmt.Errorf("decode message payload: %w", err)
	}
	return value, nil
}

// ReadMessage 从连接中读取一条业务消息
func ReadMessage(reader io.Reader, maxPayloadSize uint32) (Message, error) {
	header := make([]byte, messageHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Message{}, err
	}

	messageType := binary.BigEndian.Uint16(header[:2])
	payloadLength := binary.BigEndian.Uint32(header[2:])
	if payloadLength > maxPayloadSize {
		return Message{}, ErrPayloadTooLarge
	}

	payload := make([]byte, payloadLength)
	if payloadLength > 0 {
		if _, err := io.ReadFull(reader, payload); err != nil {
			return Message{}, err
		}
	}
	return Message{Type: messageType, Payload: payload}, nil
}

// WriteMessage 写出一条业务消息
func WriteMessage(writer io.Writer, message Message, maxPayloadSize uint32) error {
	if uint32(len(message.Payload)) > maxPayloadSize {
		return ErrPayloadTooLarge
	}

	frame := make([]byte, messageHeaderSize+len(message.Payload))
	binary.BigEndian.PutUint16(frame[:2], message.Type)
	binary.BigEndian.PutUint32(frame[2:messageHeaderSize], uint32(len(message.Payload)))
	copy(frame[messageHeaderSize:], message.Payload)

	written, err := writer.Write(frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return io.ErrShortWrite
	}
	return nil
}
