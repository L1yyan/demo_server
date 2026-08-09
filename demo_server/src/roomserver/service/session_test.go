package service

import (
	"testing"

	roomconfig "demo_server/src/roomserver/config"
	"demo_server/src/roomserver/protocol"
)

// TestSendSnapshotKeepsLatest 验证快照发送槽只保留最新快照
func TestSendSnapshotKeepsLatest(t *testing.T) {
	session := NewSession("session-test", nil, roomconfig.DefaultConfig(), nil)
	first := protocol.Message{Type: protocol.MsgSnapshot, Payload: []byte("first")}
	latest := protocol.Message{Type: protocol.MsgSnapshot, Payload: []byte("latest")}

	if !session.SendSnapshot(first) {
		t.Fatal("expected first snapshot accepted")
	}
	if !session.SendSnapshot(latest) {
		t.Fatal("expected latest snapshot accepted")
	}

	select {
	case message := <-session.snapshotCh:
		if string(message.Payload) != "latest" {
			t.Fatalf("expected latest snapshot retained, got %s", string(message.Payload))
		}
	default:
		t.Fatal("expected snapshot retained")
	}
}

// TestSendSnapshotDoesNotUseControlQueue 验证快照不会挤占关键消息队列
func TestSendSnapshotDoesNotUseControlQueue(t *testing.T) {
	cfg := roomconfig.DefaultConfig()
	cfg.WriteQueueSize = 1
	session := NewSession("session-test", nil, cfg, nil)
	control := protocol.Message{Type: protocol.MsgHeartbeatAck, Payload: []byte("ack")}
	snapshot := protocol.Message{Type: protocol.MsgSnapshot, Payload: []byte("snapshot")}

	if !session.Send(control) {
		t.Fatal("expected control message accepted")
	}
	if !session.SendSnapshot(snapshot) {
		t.Fatal("expected snapshot accepted outside control queue")
	}
	if len(session.sendCh) != 1 {
		t.Fatalf("expected control queue length 1, got %d", len(session.sendCh))
	}
	if len(session.snapshotCh) != 1 {
		t.Fatalf("expected snapshot queue length 1, got %d", len(session.snapshotCh))
	}
}
