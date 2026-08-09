package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
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
	// 消息号 8、9 和 10 曾用于旧版批量输入和预测确认消息，已废弃且不可复用
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

// NewProtoMessage 创建 protobuf 业务消息
func NewProtoMessage(messageType uint16, value proto.Message) (Message, error) {
	payload, err := proto.Marshal(value)
	if err != nil {
		return Message{}, fmt.Errorf("marshal message error: %w", err)
	}
	return Message{Type: messageType, Payload: payload}, nil
}

// DecodeProto 解码 protobuf 消息负载
func DecodeProto(message Message, value proto.Message) error {
	if len(message.Payload) == 0 {
		return fmt.Errorf("message payload is nil")
	}
	if err := proto.Unmarshal(message.Payload, value); err != nil {
		return fmt.Errorf("message unmarshal err: %w", err)
	}
	return nil
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
