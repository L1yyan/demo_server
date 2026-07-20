//go:build !physx

package physx

import (
	"errors"

	"demo_server/src/roomserver/logic"
)

var errPhysXBuildTagDisabled = errors.New("physx backend requires building with -tags physx")

// Factory 创建 PhysX 物理世界
//
// 未启用 physx build tag 时保留该类型，用于在配置错误时给出明确错误
type Factory struct {
	cfg Config // PhysX 后端配置
}

// NewFactory 创建 PhysX 物理世界工厂
func NewFactory(cfg Config) *Factory {
	return &Factory{cfg: cfg}
}

// NewWorld 返回未启用 PhysX 构建标签的错误
func (f *Factory) NewWorld(roomID string) (logic.PhysicsWorld, error) {
	return nil, errPhysXBuildTagDisabled
}
