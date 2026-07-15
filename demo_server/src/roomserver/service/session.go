package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"demo_server/pkg/glog"
	roomconfig "demo_server/src/roomserver/config"
	"demo_server/src/roomserver/protocol"

	"github.com/xtaci/kcp-go/v5"
)

// Session 客户端连接会话
type Session struct {
	id       string
	conn     *kcp.UDPSession
	cfg      roomconfig.Config
	handler  MessageHandler
	sendCh   chan protocol.Message
	closeCh  chan struct{}
	closeMu  sync.Once
	playerID uint64
	roomID   string
}

// MessageHandler 处理客户端消息
type MessageHandler interface {
	HandleMessage(context.Context, *Session, protocol.Message)
	HandleSessionClosed(context.Context, *Session)
}

// NewSession 创建客户端连接会话
func NewSession(id string, conn *kcp.UDPSession, cfg roomconfig.Config, handler MessageHandler) *Session {
	return &Session{
		id:      id,
		conn:    conn,
		cfg:     cfg,
		handler: handler,
		sendCh:  make(chan protocol.Message, cfg.WriteQueueSize),
		closeCh: make(chan struct{}),
	}
}

// ID 返回会话ID
func (s *Session) ID() string {
	return s.id
}

// PlayerID 返回玩家ID
func (s *Session) PlayerID() uint64 {
	return s.playerID
}

// SetPlayer 绑定玩家和房间信息
func (s *Session) SetPlayer(playerID uint64, roomID string) {
	s.playerID = playerID
	s.roomID = roomID
}

// Send 投递待发送消息
func (s *Session) Send(message protocol.Message) bool {
	select {
	case <-s.closeCh:
		return false
	case s.sendCh <- message:
		return true
	default:
		return false
	}
}

// Start 启动会话读写循环
func (s *Session) Start(ctx context.Context) {
	go s.readLoop(ctx)
	go s.writeLoop(ctx)
}

// Close 关闭会话
func (s *Session) Close() {
	s.closeMu.Do(func() {
		close(s.closeCh)
		_ = s.conn.Close()
	})
}

// readLoop 读取客户端消息
func (s *Session) readLoop(ctx context.Context) {
	defer func() {
		s.Close()
		if s.handler != nil {
			s.handler.HandleSessionClosed(ctx, s)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closeCh:
			return
		default:
		}

		// 设置读超时，用于发现长时间无消息的连接
		if err := s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout)); err != nil {
			glog.Warn(ctx, "set read deadline failed", glog.String("session_id", s.id), glog.Err(err))
		}
		message, err := protocol.ReadMessage(s.conn, s.cfg.MaxPayloadSize)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			glog.Warn(ctx, "read room message failed", glog.String("session_id", s.id), glog.Err(err))
			return
		}
		if s.handler != nil {
			s.handler.HandleMessage(ctx, s, message)
		}
	}
}

// writeLoop 写出服务端消息
func (s *Session) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			s.Close()
			return
		case <-s.closeCh:
			return
		case message := <-s.sendCh:
			if err := protocol.WriteMessage(s.conn, message, s.cfg.MaxPayloadSize); err != nil {
				glog.Warn(ctx, "write room message failed", glog.String("session_id", s.id), glog.Err(err))
				s.Close()
				return
			}
		}
	}
}

// newSessionID 创建连接会话ID
func newSessionID(remoteAddr string, sequence uint64) string {
	return fmt.Sprintf("%s-%d", remoteAddr, sequence)
}
