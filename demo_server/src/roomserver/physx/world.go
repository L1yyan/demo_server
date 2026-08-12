//go:build physx

package physx

/*
#cgo CXXFLAGS: -std=c++17 -I${SRCDIR}/../../../third_party/physx-sdk/include -I${SRCDIR}/../../../third_party/physx-sdk/pxshared/include
#cgo LDFLAGS: -L${SRCDIR}/../../../third_party/physx-sdk/lib/linux.x86_64/release -lPhysXExtensions_static_64 -lPhysX_static_64 -lPhysXPvdSDK_static_64 -lPhysXCooking_static_64 -lPhysXCommon_static_64 -lPhysXCooking_static_64 -lPhysXCommon_static_64 -lPhysXFoundation_static_64 -ldl -lpthread -lstdc++
#include <stdlib.h>
#include "physx_bridge.h"

typedef struct px_vec3 CVec3;
typedef struct px_quat CQuat;
typedef struct px_raycast_hit CRaycastHit;
typedef struct px_pvd_config CPVDConfig;
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	roomconfig "demo_server/src/roomserver/config"
	"demo_server/src/roomserver/logic"
)

const cErrorBufferSize = 512

// BackendAvailable 返回当前构建是否包含 PhysX 后端
func BackendAvailable() bool {
	return true
}

// Factory 创建 PhysX 物理世界
type Factory struct {
	cfg Config // PhysX 后端配置，创建每个房间 world 时使用
}

// World PhysX 物理世界
type World struct {
	ptr         *C.px_world        // C++ 侧 px_world 不透明指针，所有物理操作最终都走它
	cfg         Config             // 当前 world 使用的 PhysX 配置
	spawnPoints []logic.SpawnPoint // 从地图碰撞文件加载出的出生点列表
}

// NewFactory 创建 PhysX 物理世界工厂
func NewFactory(cfg Config) *Factory {
	if cfg.PlayerCapsuleRadius <= 0 {
		cfg.PlayerCapsuleRadius = 0.35
	}
	if cfg.PlayerCapsuleHeight <= 0 {
		cfg.PlayerCapsuleHeight = 1.8
	}
	if cfg.DefaultMapID == "" {
		cfg.DefaultMapID = "mfps_arena"
	}
	if cfg.MapCollisionPath == "" {
		cfg.MapCollisionPath = "config/maps/mfps_arena/collision.json"
	}
	if strings.TrimSpace(cfg.PVDHost) == "" {
		cfg.PVDHost = roomconfig.DefaultPhysXPVDHost
	} else {
		cfg.PVDHost = strings.TrimSpace(cfg.PVDHost)
	}
	if cfg.PVDPort <= 0 {
		cfg.PVDPort = roomconfig.DefaultPhysXPVDPort
	}
	if cfg.PVDTimeoutMS <= 0 {
		cfg.PVDTimeoutMS = roomconfig.DefaultPhysXPVDTimeoutMS
	}
	return &Factory{cfg: cfg}
}

// NewWorld 创建房间级 PhysX 物理世界
func (f *Factory) NewWorld(roomID string) (logic.PhysicsWorld, error) {
	// 创建 C 层错误缓冲区，用于承接 PhysX 初始化失败时的错误字符串
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	// 将 Go 配置转换为 C ABI 参数，交给 C++ 侧创建真正的 PhysX world
	createGroundPlane := C.int(0)
	if f.cfg.CreateGroundPlane {
		createGroundPlane = 1
	}
	pvdConfig := C.CPVDConfig{port: C.int(f.cfg.PVDPort), timeout_ms: C.uint(f.cfg.PVDTimeoutMS)}
	if f.cfg.PVDEnabled {
		pvdConfig.enabled = 1
		pvdConfig.host = C.CString(f.cfg.PVDHost)
		defer C.free(unsafe.Pointer(pvdConfig.host))
	}
	ptr := C.px_world_create(createGroundPlane, pvdConfig, errBuf, cErrorBufferSize)
	if ptr == nil {
		return nil, cError(errBuf, "create physx world")
	}

	// 保存 C++ world 指针后，再加载地图静态碰撞和出生点
	world := &World{ptr: ptr, cfg: f.cfg}
	if err := world.loadMapCollision(); err != nil {
		_ = world.Close()
		return nil, err
	}
	return world, nil
}

// loadMapCollision 加载地图静态碰撞体
func (w *World) loadMapCollision() error {
	if w.ptr == nil {
		return logic.ErrPhysicsWorldClosed
	}

	// 读取并校验 Unity 导出的地图碰撞文件，确保地图ID、单位和碰撞数据可用
	collision, err := loadMapCollision(w.cfg.MapCollisionPath, w.cfg.DefaultMapID)
	if err != nil {
		return fmt.Errorf("load physx map collision: %w", err)
	}
	w.spawnPoints = toLogicSpawnPoints(collision.SpawnPoints)
	for _, collider := range collision.Colliders {
		// 触发器不参与实体阻挡，后续区域逻辑单独处理
		if collider.IsTrigger {
			continue
		}
		// 根据碰撞体形状分发到 C++，由 PhysX scene 持有真正的静态 actor
		switch strings.ToLower(strings.TrimSpace(collider.Shape)) {
		case mapColliderShapeBox:
			if err := w.addStaticBox(collider); err != nil {
				return fmt.Errorf("add static collider %s: %w", collider.ID, err)
			}
		case mapColliderShapeMesh:
			if err := w.addStaticMesh(collider); err != nil {
				return fmt.Errorf("add static collider %s: %w", collider.ID, err)
			}
		default:
			return fmt.Errorf("%w: %s", ErrUnsupportedMapColliderShape, collider.Shape)
		}
	}
	return nil
}

// addStaticBox 添加地图静态 box 碰撞体
func (w *World) addStaticBox(collider MapCollider) error {
	// Box 碰撞体只需要把中心点、旋转和完整尺寸传给 C++，由 PhysX 创建静态刚体
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	code := C.px_world_add_static_box(w.ptr, toCVec3(toLogicVector3(collider.Position)), toCQuat(toQuaternion(collider.Rotation)), toCVec3(toLogicVector3(collider.Size)), errBuf, cErrorBufferSize)
	if code != 0 {
		return cError(errBuf, "add physx static box")
	}
	return nil
}

// addStaticMesh 添加地图静态 mesh 碰撞体
func (w *World) addStaticMesh(collider MapCollider) error {
	// mesh 碰撞体需要先把扁平数组整理成 C 侧期望的顶点和三角形索引数组
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	if len(collider.Vertices) == 0 || len(collider.Triangles) == 0 {
		return errors.New("mesh data is empty")
	}

	vertices := make([]C.double, len(collider.Vertices))
	for i, value := range collider.Vertices {
		vertices[i] = C.double(value)
	}
	triangles := make([]C.int, len(collider.Triangles))
	for i, value := range collider.Triangles {
		triangles[i] = C.int(value)
	}

	code := C.px_world_add_static_mesh(w.ptr, &vertices[0], C.int(len(vertices)/mapMeshVertexElementSize), &triangles[0], C.int(len(triangles)/mapMeshTriangleElementSize), errBuf, cErrorBufferSize)
	if code != 0 {
		return cError(errBuf, "add physx static mesh")
	}
	return nil
}

// SpawnPoints 返回地图出生点
func (w *World) SpawnPoints() []logic.SpawnPoint {
	spawnPoints := make([]logic.SpawnPoint, len(w.spawnPoints))
	copy(spawnPoints, w.spawnPoints)
	return spawnPoints
}

// AddPlayer 添加玩家胶囊体
func (w *World) AddPlayer(playerID uint64, position logic.Vector3) error {
	if w.ptr == nil {
		return logic.ErrPhysicsWorldClosed
	}
	// Go 侧只传业务坐标和玩家ID，具体胶囊体创建由 C++ 侧完成
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	code := C.px_world_add_player_capsule(w.ptr, C.uint64_t(playerID), toCVec3(position), C.double(w.cfg.PlayerCapsuleRadius), C.double(w.cfg.PlayerCapsuleHeight), errBuf, cErrorBufferSize)
	if code != 0 {
		return cError(errBuf, "add physx player")
	}
	return nil
}

// RemovePlayer 移除玩家胶囊体
func (w *World) RemovePlayer(playerID uint64) error {
	if w.ptr == nil {
		return logic.ErrPhysicsWorldClosed
	}
	// 玩家离开房间时同步移除 C++ 侧 actor，避免 scene 中残留胶囊体
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	code := C.px_world_remove_player(w.ptr, C.uint64_t(playerID), errBuf, cErrorBufferSize)
	if code != 0 {
		return cError(errBuf, "remove physx player")
	}
	return nil
}

// MovePlayer 通过 PhysX sweep 推进玩家位置
func (w *World) MovePlayer(req logic.MovePlayerRequest) (logic.MovePlayerResult, error) {
	if w.ptr == nil {
		return logic.MovePlayerResult{}, logic.ErrPhysicsWorldClosed
	}
	// 把 Go 的移动请求转为 C ABI 参数，由 C++ 侧做 sweep、重力和落地检测
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	var outPosition C.CVec3
	var outBlocked C.int
	var outGrounded C.int
	var outVerticalVelocity C.double
	deltaTime := req.DeltaTime
	if deltaTime <= 0 {
		deltaTime = 1.0 / 60.0
	}
	jump := C.int(0)
	if req.Jump {
		jump = 1
	}
	grounded := C.int(0)
	if req.Grounded {
		grounded = 1
	}
	code := C.px_world_move_player(w.ptr, C.uint64_t(req.PlayerID), toCVec3(req.Direction), C.double(req.Distance), C.double(deltaTime), jump, grounded, C.double(req.VerticalVelocity), &outPosition, &outBlocked, &outGrounded, &outVerticalVelocity, errBuf, cErrorBufferSize)
	if code != 0 {
		return logic.MovePlayerResult{}, cError(errBuf, "move physx player")
	}
	return logic.MovePlayerResult{Position: fromCVec3(outPosition), Blocked: outBlocked != 0, Grounded: outGrounded != 0, VerticalVelocity: float64(outVerticalVelocity)}, nil
}

// GetPlayerPosition 读取玩家当前 PhysX 位置
func (w *World) GetPlayerPosition(playerID uint64) (logic.Vector3, error) {
	if w.ptr == nil {
		return logic.Vector3{}, logic.ErrPhysicsWorldClosed
	}
	// 从 C++ 侧读取玩家当前位置，再转换回 Go 业务坐标
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	var outPosition C.CVec3
	code := C.px_world_get_player_position(w.ptr, C.uint64_t(playerID), &outPosition, errBuf, cErrorBufferSize)
	if code != 0 {
		return logic.Vector3{}, cError(errBuf, "get physx player position")
	}
	return fromCVec3(outPosition), nil
}

// SetPlayerPosition 设置玩家当前 PhysX 位置
func (w *World) SetPlayerPosition(playerID uint64, position logic.Vector3) error {
	if w.ptr == nil {
		return logic.ErrPhysicsWorldClosed
	}
	// 强制修正玩家位置时，先把 Go 坐标转成 C 侧期望的业务坐标格式
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	code := C.px_world_set_player_position(w.ptr, C.uint64_t(playerID), toCVec3(position), errBuf, cErrorBufferSize)
	if code != 0 {
		return cError(errBuf, "set physx player position")
	}
	return nil
}

// Raycast 执行 PhysX 射线检测
func (w *World) Raycast(req logic.RaycastRequest) (logic.RaycastHit, error) {
	if w.ptr == nil {
		return logic.RaycastHit{}, logic.ErrPhysicsWorldClosed
	}
	// 单条射线直接交给 C++ 侧的 PhysX scene query 处理
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	var outHit C.CRaycastHit
	code := C.px_world_raycast(w.ptr, toCVec3(req.Origin), toCVec3(req.Direction), C.double(req.MaxDistance), C.uint32_t(req.Mask), C.uint64_t(req.IgnorePlayerID), &outHit, errBuf, cErrorBufferSize)
	if code != 0 {
		return logic.RaycastHit{}, cError(errBuf, "physx raycast")
	}
	return fromCRaycastHit(outHit), nil
}

// BatchRaycast 批量执行 PhysX 射线检测
func (w *World) BatchRaycast(reqs []logic.RaycastRequest) ([]logic.RaycastHit, error) {
	if w.ptr == nil {
		return nil, logic.ErrPhysicsWorldClosed
	}
	if len(reqs) == 0 {
		return nil, nil
	}

	// 批量请求先整理成 C 数组，减少 Go/C++ 之间的多次跨语言调用
	origins := make([]C.CVec3, len(reqs))
	directions := make([]C.CVec3, len(reqs))
	maxDistances := make([]C.double, len(reqs))
	masks := make([]C.uint32_t, len(reqs))
	ignoredPlayerIDs := make([]C.uint64_t, len(reqs))
	outHits := make([]C.CRaycastHit, len(reqs))
	for i, req := range reqs {
		origins[i] = toCVec3(req.Origin)
		directions[i] = toCVec3(req.Direction)
		maxDistances[i] = C.double(req.MaxDistance)
		masks[i] = C.uint32_t(req.Mask)
		ignoredPlayerIDs[i] = C.uint64_t(req.IgnorePlayerID)
	}

	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	code := C.px_world_batch_raycast(w.ptr, &origins[0], &directions[0], &maxDistances[0], &masks[0], &ignoredPlayerIDs[0], C.int(len(reqs)), &outHits[0], errBuf, cErrorBufferSize)
	if code != 0 {
		return nil, cError(errBuf, "physx batch raycast")
	}

	hits := make([]logic.RaycastHit, len(outHits))
	for i, outHit := range outHits {
		hits[i] = fromCRaycastHit(outHit)
	}
	return hits, nil
}

// Close 释放 PhysX 物理世界
func (w *World) Close() error {
	if w.ptr == nil {
		return nil
	}
	// 先释放 C++ 侧 scene、actor 和共享 runtime 引用，再把 Go 侧指针置空
	C.px_world_release(w.ptr)
	w.ptr = nil
	return nil
}

// toCVec3 将 Go 向量转换为 C 向量
func toCVec3(value logic.Vector3) C.CVec3 {
	return C.CVec3{x: C.double(value.X), y: C.double(value.Y), z: C.double(value.Z)}
}

// toCQuat 将 Go 四元数转换为 C 四元数
func toCQuat(value Quaternion) C.CQuat {
	return C.CQuat{x: C.double(value.X), y: C.double(value.Y), z: C.double(value.Z), w: C.double(value.W)}
}

// fromCVec3 将 C 向量转换为 Go 向量
func fromCVec3(value C.CVec3) logic.Vector3 {
	return logic.Vector3{X: float64(value.x), Y: float64(value.y), Z: float64(value.z)}
}

// fromCRaycastHit 将 C 射线结果转换为 Go 结果
func fromCRaycastHit(value C.CRaycastHit) logic.RaycastHit {
	return logic.RaycastHit{
		Hit:      value.hit != 0,
		TargetID: uint64(value.target_id),
		Point:    fromCVec3(value.point),
		Normal:   fromCVec3(value.normal),
		Distance: float64(value.distance),
	}
}

// newCErrorBuffer 创建 C 错误缓冲区
func newCErrorBuffer() *C.char {
	return (*C.char)(C.calloc(cErrorBufferSize, C.size_t(1)))
}

// cError 转换 C 层错误信息
func cError(errBuf *C.char, action string) error {
	if errBuf == nil {
		return errors.New(action)
	}
	message := C.GoString(errBuf)
	if message == "" {
		return errors.New(action)
	}
	if strings.Contains(message, "player not found") {
		return logic.ErrPhysicsPlayerNotFound
	}
	return fmt.Errorf("%s: %s", action, message)
}
