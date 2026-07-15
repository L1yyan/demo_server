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
	Token string `json:"token"` // 入房令牌
}

// JoinRoomAck 加入房间响应
type JoinRoomAck struct {
	OK      bool   `json:"ok"`      // 是否成功
	RoomID  string `json:"room_id"` // 房间ID
	Content string `json:"content"` // 响应信息
	Tick    int64  `json:"tick"`    // 当前房间帧号
}

// Heartbeat 心跳消息
type Heartbeat struct {
	ClientTime int64 `json:"client_time"` // 客户端时间戳
	ServerTime int64 `json:"server_time"` // 服务端时间戳
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

// PlayerState 玩家快照状态
type PlayerState struct {
	PlayerID uint64  `json:"player_id"` // 玩家ID
	X        float64 `json:"x"`         // X坐标
	Y        float64 `json:"y"`         // Y坐标
	Z        float64 `json:"z"`         // Z坐标
	Yaw      float64 `json:"yaw"`       // 水平视角
	Pitch    float64 `json:"pitch"`     // 垂直视角
	HP       int     `json:"hp"`        // 生命值
}

// Snapshot 状态快照
type Snapshot struct {
	ServerTick int64         `json:"server_tick"` // 服务端帧号
	Players    []PlayerState `json:"players"`     // 可见玩家状态
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
