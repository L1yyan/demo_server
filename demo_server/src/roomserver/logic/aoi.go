package logic

import "math"

const (
	defaultVisibleDistance = 80.0  // 默认可见距离
	defaultViewAngle       = 120.0 // 默认水平视野角度
)

// AOIFilter AOI 可见性过滤接口
type AOIFilter interface {
	FilterVisible(self *Player, candidates []*Player) []*Player
}

// SimpleAOIFilter 简化 AOI 过滤器
type SimpleAOIFilter struct {
	VisibleDistance float64 // 可见距离
	ViewAngle       float64 // 视野角度
}

// NewSimpleAOIFilter 创建简化 AOI 过滤器
func NewSimpleAOIFilter() *SimpleAOIFilter {
	return &SimpleAOIFilter{VisibleDistance: defaultVisibleDistance, ViewAngle: defaultViewAngle}
}

// FilterVisible 过滤当前玩家可见的其他玩家
func (f *SimpleAOIFilter) FilterVisible(self *Player, candidates []*Player) []*Player {
	if self == nil {
		return nil
	}
	distanceLimit := f.VisibleDistance
	if distanceLimit <= 0 {
		distanceLimit = defaultVisibleDistance
	}
	viewAngle := f.ViewAngle
	if viewAngle <= 0 {
		viewAngle = defaultViewAngle
	}

	visible := make([]*Player, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.ID == self.ID || !candidate.Alive {
			continue
		}

		// 先按距离粗筛，2人房间直接遍历即可
		dx := candidate.X - self.X
		dz := candidate.Z - self.Z
		distance := math.Sqrt(dx*dx + dz*dz)
		if distance > distanceLimit {
			continue
		}

		// 再按水平视野角度过滤，遮挡检测后续接 PhysicsWorld
		angle := normalizeAngle(math.Atan2(dx, dz)*180/math.Pi - self.Yaw)
		if math.Abs(angle) > viewAngle/2 {
			continue
		}
		visible = append(visible, candidate)
	}
	return visible
}

// normalizeAngle 将角度归一化到 -180 到 180
func normalizeAngle(angle float64) float64 {
	for angle > 180 {
		angle -= 360
	}
	for angle < -180 {
		angle += 360
	}
	return angle
}
