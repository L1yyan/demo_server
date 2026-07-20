//go:build physx

package physx

/*
#cgo CXXFLAGS: -std=c++17 -I${SRCDIR}/../../../third_party/physx-sdk/include -I${SRCDIR}/../../../third_party/physx-sdk/pxshared/include
#cgo LDFLAGS: -L${SRCDIR}/../../../third_party/physx-sdk/lib/linux.x86_64/release -lPhysX_static_64 -lPhysXExtensions_static_64 -lPhysXPvdSDK_static_64 -lPhysXCommon_static_64 -lPhysXFoundation_static_64 -ldl -lpthread -lstdc++
#include <stdlib.h>
#include "physx_bridge.h"

typedef struct px_vec3 CVec3;
typedef struct px_raycast_hit CRaycastHit;
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"demo_server/src/roomserver/logic"
)

const cErrorBufferSize = 512

// Factory 创建 PhysX 物理世界
type Factory struct {
	cfg Config // PhysX 后端配置
}

// World PhysX 物理世界
type World struct {
	ptr *C.px_world
	cfg Config
}

// NewFactory 创建 PhysX 物理世界工厂
func NewFactory(cfg Config) *Factory {
	if cfg.PlayerCapsuleRadius <= 0 {
		cfg.PlayerCapsuleRadius = 0.35
	}
	if cfg.PlayerCapsuleHeight <= 0 {
		cfg.PlayerCapsuleHeight = 1.8
	}
	return &Factory{cfg: cfg}
}

// NewWorld 创建房间级 PhysX 物理世界
func (f *Factory) NewWorld(roomID string) (logic.PhysicsWorld, error) {
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	createGroundPlane := C.int(0)
	if f.cfg.CreateGroundPlane {
		createGroundPlane = 1
	}
	ptr := C.px_world_create(createGroundPlane, errBuf, cErrorBufferSize)
	if ptr == nil {
		return nil, cError(errBuf, "create physx world")
	}
	return &World{ptr: ptr, cfg: f.cfg}, nil
}

// AddPlayer 添加玩家胶囊体
func (w *World) AddPlayer(playerID uint64, position logic.Vector3) error {
	if w.ptr == nil {
		return logic.ErrPhysicsWorldClosed
	}
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
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	var outPosition C.CVec3
	var outBlocked C.int
	code := C.px_world_move_player(w.ptr, C.uint64_t(req.PlayerID), toCVec3(req.Direction), C.double(req.Distance), &outPosition, &outBlocked, errBuf, cErrorBufferSize)
	if code != 0 {
		return logic.MovePlayerResult{}, cError(errBuf, "move physx player")
	}
	return logic.MovePlayerResult{Position: fromCVec3(outPosition), Blocked: outBlocked != 0}, nil
}

// Raycast 执行 PhysX 射线检测
func (w *World) Raycast(req logic.RaycastRequest) (logic.RaycastHit, error) {
	if w.ptr == nil {
		return logic.RaycastHit{}, logic.ErrPhysicsWorldClosed
	}
	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	var outHit C.CRaycastHit
	code := C.px_world_raycast(w.ptr, toCVec3(req.Origin), toCVec3(req.Direction), C.double(req.MaxDistance), C.uint32_t(req.Mask), &outHit, errBuf, cErrorBufferSize)
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

	origins := make([]C.CVec3, len(reqs))
	directions := make([]C.CVec3, len(reqs))
	maxDistances := make([]C.double, len(reqs))
	masks := make([]C.uint32_t, len(reqs))
	outHits := make([]C.CRaycastHit, len(reqs))
	for i, req := range reqs {
		origins[i] = toCVec3(req.Origin)
		directions[i] = toCVec3(req.Direction)
		maxDistances[i] = C.double(req.MaxDistance)
		masks[i] = C.uint32_t(req.Mask)
	}

	errBuf := newCErrorBuffer()
	defer C.free(unsafe.Pointer(errBuf))

	code := C.px_world_batch_raycast(w.ptr, &origins[0], &directions[0], &maxDistances[0], &masks[0], C.int(len(reqs)), &outHits[0], errBuf, cErrorBufferSize)
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
	C.px_world_release(w.ptr)
	w.ptr = nil
	return nil
}

// toCVec3 将 Go 向量转换为 C 向量
func toCVec3(value logic.Vector3) C.CVec3 {
	return C.CVec3{x: C.double(value.X), y: C.double(value.Y), z: C.double(value.Z)}
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
	return fmt.Errorf("%s: %s", action, message)
}
