package service

import (
	"context"
	"encoding/json"
	"testing"

	roomconfig "demo_server/src/roomserver/config"
	"demo_server/src/roomserver/logic"
	"demo_server/src/roomserver/protocol"
)

// TestHandlePlayerStatsQueryRequiresJoinedSession 验证未入房不能查询战绩
func TestHandlePlayerStatsQueryRequiresJoinedSession(t *testing.T) {
	server := NewServer(roomconfig.DefaultConfig())
	session := NewSession("session-test", nil, roomconfig.DefaultConfig(), nil)

	server.HandleMessage(context.Background(), session, protocol.Message{Type: protocol.MsgPlayerStatsQuery})

	message := receiveControlMessage(t, session)
	if message.Type != protocol.MsgError {
		t.Fatalf("expected error message, got %d", message.Type)
	}
	var response protocol.ErrorResponse
	if err := json.Unmarshal(message.Payload, &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != "not_joined" {
		t.Fatalf("expected not_joined error, got %+v", response)
	}
}

// TestHandlePlayerStatsQueryReturnsStats 验证战绩查询返回玩家击杀死亡数量
func TestHandlePlayerStatsQueryReturnsStats(t *testing.T) {
	cfg := roomconfig.DefaultConfig()
	server := NewServer(cfg)
	server.manager = logic.NewRoomManagerWithOptions(context.Background(), 10, 2, 20, 10, logic.SyncConfig{}, "", "", cfg.GameDuration, nil, logic.NewSimplePhysicsWorldFactory())
	session := NewSession("session-test", nil, cfg, nil)
	session.SetPlayer(1, "room-test")
	player := &logic.Player{ID: 1, RoomID: "room-test", Session: session}
	if err := server.manager.JoinRoom("room-test", player); err != nil {
		t.Fatalf("join room: %v", err)
	}

	server.HandleMessage(context.Background(), session, protocol.Message{Type: protocol.MsgPlayerStatsQuery})

	message := receiveControlMessageOfType(t, session, protocol.MsgPlayerStatsResp)
	var response protocol.PlayerStatsResp
	if err := json.Unmarshal(message.Payload, &response); err != nil {
		t.Fatalf("decode stats response: %v", err)
	}
	if !response.OK || response.RoomID != "room-test" || response.Stats.PlayerID != 1 {
		t.Fatalf("unexpected stats response: %+v", response)
	}
}

// receiveControlMessage 读取测试会话控制消息
func receiveControlMessage(t *testing.T, session *Session) protocol.Message {
	t.Helper()
	select {
	case message := <-session.sendCh:
		return message
	default:
		t.Fatal("expected control message")
		return protocol.Message{}
	}
}

// receiveControlMessageOfType 读取指定类型的测试会话控制消息
func receiveControlMessageOfType(t *testing.T, session *Session, messageType uint16) protocol.Message {
	t.Helper()
	for {
		select {
		case message := <-session.sendCh:
			if message.Type == messageType {
				return message
			}
		default:
			t.Fatalf("expected control message type %d", messageType)
			return protocol.Message{}
		}
	}
}
