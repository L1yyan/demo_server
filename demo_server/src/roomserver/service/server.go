package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"demo_server/pkg/glog"
	roomconfig "demo_server/src/roomserver/config"
	"demo_server/src/roomserver/logic"
	"demo_server/src/roomserver/physx"
	"demo_server/src/roomserver/protocol"

	"github.com/xtaci/kcp-go/v5"
)

// Server roomserver 服务
type Server struct {
	cfg      roomconfig.Config
	manager  *logic.RoomManager
	listener *kcp.Listener
	sessions sync.Map
	seq      atomic.Uint64
}

// NewServer 创建 roomserver 服务
func NewServer(cfg roomconfig.Config) *Server {
	cfg = cfg.Normalize()
	return &Server{cfg: cfg}
}

// Start 启动 roomserver
func (s *Server) Start(ctx context.Context) error {
	physicsFactory, err := s.newPhysicsWorldFactory()
	if err != nil {
		return err
	}
	syncConfig := logic.SyncConfig{
		PredictionEnabled:          s.cfg.PredictionEnabled,
		RollbackWindowTicks:        s.cfg.RollbackWindowTicks,
		FutureInputWindowTicks:     s.cfg.FutureInputWindowTicks,
		PredictionKeyframeInterval: s.cfg.PredictionKeyframeInterval,
		PositionTolerance:          s.cfg.PositionTolerance,
		HardPositionTolerance:      s.cfg.HardPositionTolerance,
		AngleTolerance:             s.cfg.AngleTolerance,
		MaxInputBatchFrames:        s.cfg.MaxInputBatchFrames,
		MaxInputHoldTicks:          s.cfg.MaxInputHoldTicks,
		CorrectionMinIntervalTicks: s.cfg.CorrectionMinIntervalTicks,
	}
	manager := logic.NewRoomManagerWithOptions(ctx, s.cfg.MaxRooms, s.cfg.MaxPlayersPerRoom, s.cfg.TickRate, s.cfg.SnapshotRate, syncConfig, s.cfg.DefaultMapID, s.cfg.PhysicsHash, s.cfg.GameDuration, logic.NewSimpleAOIFilter(), physicsFactory)
	listener, err := kcp.ListenWithOptions(s.cfg.ListenAddr, nil, 10, 3)
	if err != nil {
		return fmt.Errorf("listen kcp: %w", err)
	}
	listener.SetReadBuffer(4 * 1024 * 1024)
	listener.SetWriteBuffer(4 * 1024 * 1024)

	s.manager = manager
	s.listener = listener
	glog.Info(ctx, "roomserver started", glog.String("addr", s.cfg.ListenAddr), glog.String("server_id", s.cfg.ServerID), glog.String("physics_backend", s.cfg.PhysicsBackend))

	go s.acceptLoop(ctx)
	return nil
}

// Stop 停止 roomserver
func (s *Server) Stop(ctx context.Context) {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.manager != nil {
		s.manager.Stop()
	}
	s.sessions.Range(func(key, value any) bool {
		if session, ok := value.(*Session); ok {
			session.Close()
		}
		return true
	})
	glog.Info(ctx, "roomserver stopped")
}

// HandleMessage 处理客户端业务消息
func (s *Server) HandleMessage(ctx context.Context, session *Session, message protocol.Message) {
	switch message.Type {
	case protocol.MsgJoinRoom:
		s.handleJoinRoom(ctx, session, message)
	case protocol.MsgHeartbeat:
		s.handleHeartbeat(session, message)
	case protocol.MsgPlayerInput:
		s.handlePlayerInput(ctx, session, message)
	case protocol.MsgPlayerInputBatch:
		s.handlePlayerInputBatch(ctx, session, message)
	case protocol.MsgPlayerStatsQuery:
		s.handlePlayerStatsQuery(ctx, session, message)
	default:
		s.sendError(session, "unknown_message", "unknown message type")
	}
}

// HandleSessionClosed 处理连接关闭
func (s *Server) HandleSessionClosed(ctx context.Context, session *Session) {
	if session == nil {
		return
	}
	s.sessions.Delete(session.ID())
	if session.PlayerID() != 0 && s.manager != nil {
		s.manager.LeaveRoom(session.PlayerID())
	}
	glog.Info(ctx, "session closed", glog.String("session_id", session.ID()), glog.Uint64("player_id", session.PlayerID()), glog.String("room_id", session.RoomID()))
}

// newPhysicsWorldFactory 根据配置创建物理世界工厂
func (s *Server) newPhysicsWorldFactory() (logic.PhysicsWorldFactory, error) {
	switch strings.ToLower(strings.TrimSpace(s.cfg.PhysicsBackend)) {
	case "", roomconfig.PhysicsBackendPhysX:
		return physx.NewFactory(physx.Config{
			PlayerCapsuleRadius: s.cfg.PlayerCapsuleRadius,
			PlayerCapsuleHeight: s.cfg.PlayerCapsuleHeight,
			CreateGroundPlane:   s.cfg.PhysicsGroundPlane,
			DefaultMapID:        s.cfg.DefaultMapID,
			MapCollisionPath:    s.cfg.MapCollisionPath,
		}), nil
	case roomconfig.PhysicsBackendSimple:
		return logic.NewSimplePhysicsWorldFactory(), nil
	default:
		return nil, fmt.Errorf("unknown physics backend: %s", s.cfg.PhysicsBackend)
	}
}

// acceptLoop 接收客户端 KCP 连接
func (s *Server) acceptLoop(ctx context.Context) {
	for {
		conn, err := s.listener.AcceptKCP()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				glog.Warn(ctx, "accept kcp session failed", glog.Err(err))
				continue
			}
		}

		// KCP 低延迟参数，后续按压测结果调整
		conn.SetNoDelay(1, 20, 2, 1)
		conn.SetStreamMode(true)
		conn.SetWriteDelay(false)

		sequence := s.seq.Add(1)
		sessionID := newSessionID(conn.RemoteAddr().String(), sequence)
		session := NewSession(sessionID, conn, s.cfg, s)
		s.sessions.Store(sessionID, session)
		session.Start(ctx)
		glog.Info(ctx, "session accepted", glog.String("session_id", sessionID), glog.Any("player_id", session.playerID), glog.String("remote_addr", conn.RemoteAddr().String()))
	}
}

// handleJoinRoom 处理入房请求
func (s *Server) handleJoinRoom(ctx context.Context, session *Session, message protocol.Message) {
	request, err := protocol.DecodeJSON[protocol.JoinRoomRequest](message)
	if err != nil {
		s.sendError(session, "bad_request", "invalid join room request")
		return
	}
	claims, err := protocol.ParseRoomToken(s.cfg.TokenSecret, request.Token)
	if err != nil {
		s.sendError(session, "invalid_token", err.Error())
		return
	}
	if claims.ServerID != s.cfg.ServerID {
		s.sendError(session, "server_mismatch", "room token server mismatch")
		return
	}
	if claims.RoomID == "" || claims.PlayerID == 0 {
		s.sendError(session, "invalid_token", "room token missing room or player")
		return
	}

	session.SetPlayer(claims.PlayerID, claims.RoomID)
	player := &logic.Player{
		ID:                claims.PlayerID,
		RoomID:            claims.RoomID,
		Session:           session,
		SyncVersion:       request.SyncVersion,
		PredictionEnabled: request.PredictionEnabled,
		PhysicsHash:       request.PhysicsHash,
	}
	if err := s.manager.JoinRoom(claims.RoomID, player); err != nil {
		s.sendError(session, "join_failed", err.Error())
		return
	}
}

// handlePlayerStatsQuery 处理玩家战绩查询
func (s *Server) handlePlayerStatsQuery(ctx context.Context, session *Session, message protocol.Message) {
	if session.PlayerID() == 0 {
		s.sendError(session, "not_joined", "player not joined room")
		return
	}
	if s.manager == nil {
		s.sendError(session, "stats_failed", "room manager not started")
		return
	}

	request := protocol.PlayerStatsQuery{}
	if len(message.Payload) > 0 {
		decoded, err := protocol.DecodeJSON[protocol.PlayerStatsQuery](message)
		if err != nil {
			s.sendError(session, "bad_request", "invalid player stats query")
			return
		}
		request = decoded
	}
	statsSnapshot, err := s.manager.QueryPlayerStats(session.PlayerID(), request.PlayerID)
	if err != nil {
		glog.Warn(ctx, "query player stats failed", glog.String("session_id", session.ID()), glog.Uint64("player_id", session.PlayerID()), glog.Uint64("target_player_id", request.PlayerID), glog.Err(err))
		s.sendError(session, "stats_failed", err.Error())
		return
	}
	response, err := protocol.NewJSONMessage(protocol.MsgPlayerStatsResp, protocol.PlayerStatsResp{OK: true, Content: "ok", RoomID: statsSnapshot.RoomID, ServerTick: statsSnapshot.ServerTick, Stats: statsSnapshot.Stats})
	if err != nil {
		return
	}
	session.Send(response)
}

// handleHeartbeat 处理心跳
func (s *Server) handleHeartbeat(session *Session, requestMessage protocol.Message) {
	request, _ := protocol.DecodeJSON[protocol.Heartbeat](requestMessage)
	serverTick := int64(0)
	if s.manager != nil && session.PlayerID() != 0 {
		serverTick = s.manager.RoomTick(session.PlayerID())
	}
	message, err := protocol.NewJSONMessage(protocol.MsgHeartbeatAck, protocol.Heartbeat{ClientTime: request.ClientTime, ServerTime: time.Now().UnixMilli(), ServerTick: serverTick})
	if err != nil {
		return
	}
	session.Send(message)
}

// handlePlayerInput 处理玩家输入
func (s *Server) handlePlayerInput(ctx context.Context, session *Session, message protocol.Message) {
	if session.PlayerID() == 0 {
		s.sendError(session, "not_joined", "player not joined room")
		return
	}
	input, err := protocol.DecodeJSON[protocol.PlayerInput](message)
	if err != nil {
		s.sendError(session, "bad_request", "invalid player input")
		return
	}
	if err := s.manager.PushInput(session.PlayerID(), input); err != nil {
		glog.Warn(ctx, "push player input failed", glog.String("session_id", session.ID()), glog.Uint64("player_id", session.PlayerID()), glog.Err(err))
		s.sendError(session, "input_failed", err.Error())
	}
}

// handlePlayerInputBatch 处理批量玩家输入
func (s *Server) handlePlayerInputBatch(ctx context.Context, session *Session, message protocol.Message) {
	if session.PlayerID() == 0 {
		s.sendError(session, "not_joined", "player not joined room")
		return
	}
	batch, err := protocol.DecodeJSON[protocol.PlayerInputBatch](message)
	if err != nil {
		s.sendError(session, "bad_request", "invalid player input batch")
		return
	}
	if len(batch.Frames) == 0 || len(batch.Frames) > s.cfg.MaxInputBatchFrames {
		s.sendError(session, "bad_request", "invalid input batch frame count")
		return
	}
	if err := s.manager.PushInputBatch(session.PlayerID(), batch); err != nil {
		glog.Warn(ctx, "push player input batch failed", glog.String("session_id", session.ID()), glog.Uint64("player_id", session.PlayerID()), glog.Err(err))
		s.sendError(session, "input_failed", err.Error())
	}
}

// sendError 发送错误响应
func (s *Server) sendError(session *Session, code string, content string) {
	message, err := protocol.NewJSONMessage(protocol.MsgError, protocol.ErrorResponse{Code: code, Content: content})
	if err != nil {
		return
	}
	session.Send(message)
}
