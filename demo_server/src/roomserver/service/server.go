package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	roompb "demo_server/gen/room"
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
	if err := s.resolveMapCollisionMetadata(ctx); err != nil {
		return err
	}
	physicsFactory, err := s.newPhysicsWorldFactory()
	if err != nil {
		return err
	}
	syncConfig := logic.SyncConfig{
		MaxInputHoldTicks: s.cfg.MaxInputHoldTicks,
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
	glog.Info(ctx, "roomserver started", glog.String("addr", s.cfg.ListenAddr), glog.String("server_id", s.cfg.ServerID), glog.String("physics_backend", s.cfg.PhysicsBackend), glog.Bool("physx_pvd_enabled", s.cfg.PhysXPVDEnabled), glog.String("physx_pvd_addr", fmt.Sprintf("%s:%d", s.cfg.PhysXPVDHost, s.cfg.PhysXPVDPort)))

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

// resolveMapCollisionMetadata 校验实际地图碰撞文件并绑定运行时hash
func (s *Server) resolveMapCollisionMetadata(ctx context.Context) error {
	backend := strings.ToLower(strings.TrimSpace(s.cfg.PhysicsBackend))
	if backend == roomconfig.PhysicsBackendSimple {
		return nil
	}
	if backend != "" && backend != roomconfig.PhysicsBackendPhysX {
		return fmt.Errorf("unknown physics backend: %s", s.cfg.PhysicsBackend)
	}
	if !physx.BackendAvailable() {
		return fmt.Errorf("physx backend requires building with -tags physx")
	}

	metadata, err := physx.LoadMapCollisionMetadata(s.cfg.MapCollisionPath, s.cfg.DefaultMapID)
	if err != nil {
		return fmt.Errorf("load map collision metadata: %w", err)
	}
	if strings.TrimSpace(metadata.PhysicsHash) != "" && s.cfg.PhysicsHash != "" && metadata.PhysicsHash != s.cfg.PhysicsHash {
		glog.Warn(ctx, "roomserver physics hash config differs from map file", glog.String("config_hash", s.cfg.PhysicsHash), glog.String("map_hash", metadata.PhysicsHash))
	}
	if strings.TrimSpace(metadata.PhysicsHash) != "" {
		s.cfg.PhysicsHash = metadata.PhysicsHash
	}

	glog.Info(ctx, "roomserver map collision loaded", glog.String("map_id", metadata.MapID), glog.Int("map_version", metadata.MapVersion), glog.String("path", s.cfg.MapCollisionPath), glog.String("physics_hash", s.cfg.PhysicsHash), glog.Int("colliders", metadata.ColliderCount), glog.Int("spawn_points", metadata.SpawnPointCount))
	return nil
}

// HandleMessage 处理客户端业务消息
func (s *Server) HandleMessage(ctx context.Context, session *Session, message protocol.Message) {
	switch message.Type {
	case protocol.MsgJoinRoom:
		s.handleJoinRoom(ctx, session, message)
	case protocol.MsgHeartbeat:
		s.handleHeartbeat(session, message)
	case protocol.MsgLeaveRoom:
		s.handleLeaveRoom(ctx, session)
	case protocol.MsgPlayerInput:
		s.handlePlayerInput(ctx, session, message)
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
		s.manager.LeaveRoom(session.PlayerID(), session.RoomID())
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
			PVDEnabled:          s.cfg.PhysXPVDEnabled,
			PVDHost:             s.cfg.PhysXPVDHost,
			PVDPort:             s.cfg.PhysXPVDPort,
			PVDTimeoutMS:        s.cfg.PhysXPVDTimeoutMS,
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
	var request roompb.JoinRoomReq
	err := protocol.DecodeProto(message, &request)
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

	player := &logic.Player{
		ID:      claims.PlayerID,
		RoomID:  claims.RoomID,
		Session: session,
	}
	if err := s.manager.JoinRoom(claims.RoomID, player); err != nil {
		glog.Warn(ctx, "join room failed", glog.String("room_id", claims.RoomID), glog.Uint64("player_id", claims.PlayerID), glog.Err(err))
		s.sendError(session, "join_failed", err.Error())
		return
	}
	// 入房成功后再绑定会话，避免失败连接继续投递输入污染房间索引
	session.SetPlayer(claims.PlayerID, claims.RoomID)
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
	var request roompb.PlayerStatsReq
	if len(message.Payload) > 0 {
		err := protocol.DecodeProto(message, &request)
		if err != nil {
			s.sendError(session, "bad_request", "invalid player stats query")
			return
		}
	}
	statsSnapshot, err := s.manager.QueryPlayerStats(session.PlayerID(), request.PlayerId)
	if err != nil {
		glog.Warn(ctx, "query player stats failed", glog.String("session_id", session.ID()), glog.Uint64("player_id", session.PlayerID()), glog.Uint64("target_player_id", request.PlayerId), glog.Err(err))
		s.sendError(session, "stats_failed", err.Error())
		return
	}
	statsSnapshotpb := roompb.PlayerStats{
		PlayerId:   statsSnapshot.Stats.PlayerID,
		KillCount:  int32(statsSnapshot.Stats.KillCount),
		DeathCount: int32(statsSnapshot.Stats.DeathCount),
	}
	response, err := protocol.NewProtoMessage(protocol.MsgPlayerStatsResp, &roompb.PlayerStatsResp{Status: true, Content: "ok", RoomId: statsSnapshot.RoomID, ServerTick: statsSnapshot.ServerTick, Stats: &statsSnapshotpb})
	if err != nil {
		return
	}
	session.Send(response)
}

// handleHeartbeat 处理心跳
func (s *Server) handleHeartbeat(session *Session, requestMessage protocol.Message) {
	var request roompb.Heartbeat
	protocol.DecodeProto(requestMessage, &request)
	serverTick := int64(0)
	if s.manager != nil && session.PlayerID() != 0 {
		serverTick = s.manager.RoomTick(session.PlayerID())
	}
	message, err := protocol.NewProtoMessage(protocol.MsgHeartbeatAck, &roompb.Heartbeat{ClientTime: request.ClientTime, ServerTime: time.Now().UnixMilli(), ServerTick: serverTick})
	if err != nil {
		return
	}
	session.Send(message)
}

// handleLeaveRoom 处理主动离房
func (s *Server) handleLeaveRoom(ctx context.Context, session *Session) {
	if session.PlayerID() == 0 {
		s.sendError(session, "not_joined", "player not joined room")
		return
	}
	playerID := session.PlayerID()
	roomID := session.RoomID()
	if s.manager != nil {
		s.manager.LeaveRoom(playerID, roomID)
	}
	session.SetPlayer(0, "")
	glog.Info(ctx, "player leave room requested", glog.String("session_id", session.ID()), glog.Uint64("player_id", playerID), glog.String("room_id", roomID))
}

// handlePlayerInput 处理玩家输入
func (s *Server) handlePlayerInput(ctx context.Context, session *Session, message protocol.Message) {
	if session.PlayerID() == 0 {
		s.sendError(session, "not_joined", "player not joined room")
		return
	}
	var inputpb roompb.PlayerInput
	if err := protocol.DecodeProto(message, &inputpb); err != nil {
		s.sendError(session, "bad_request", "invalid player input")
		return
	}

	if err := s.manager.PushInput(session.PlayerID(), &inputpb); err != nil {
		glog.Warn(ctx, "push player input failed", glog.String("session_id", session.ID()), glog.Uint64("player_id", session.PlayerID()), glog.Err(err))
		s.sendError(session, "input_failed", err.Error())
	}
}

// sendError 发送错误响应
func (s *Server) sendError(session *Session, code string, content string) {
	message, err := protocol.NewProtoMessage(protocol.MsgError, &roompb.ErrorResp{Code: code, Content: content})
	if err != nil {
		return
	}
	session.Send(message)
}
