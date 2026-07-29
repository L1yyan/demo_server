package logic

import (
	"context"
	"math"
	"sync"
	"time"

	"demo_server/pkg/glog"
)

const (
	defaultVisibleDistance   = 80.0        // 默认可见距离
	defaultViewAngle         = 120.0       // 默认水平视野角度
	aoiDiagnosticLogInterval = time.Second // AOI 诊断日志节流间隔
)

type aoiDiagnosticKey struct {
	selfID      uint64
	candidateID uint64
	reason      string
}

var aoiDiagnosticState = struct {
	sync.Mutex
	lastLogged map[aoiDiagnosticKey]time.Time
}{lastLogged: make(map[aoiDiagnosticKey]time.Time)}

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
		if candidate == nil || candidate.ID == self.ID {
			continue
		}

		// 先按距离粗筛，2人房间直接遍历即可
		dx := candidate.X - self.X
		dz := candidate.Z - self.Z
		distance := math.Sqrt(dx*dx + dz*dz)
		angle := normalizeAngle(math.Atan2(dx, dz)*180/math.Pi - self.Yaw)
		if !candidate.Alive {
			logAOIFiltered("dead", self, candidate, distance, angle, distanceLimit, viewAngle)
			continue
		}
		if distance > distanceLimit {
			logAOIFiltered("distance", self, candidate, distance, angle, distanceLimit, viewAngle)
			continue
		}

		// 双人对局需要稳定同步存活对手，不再按水平视野角裁剪
		visible = append(visible, candidate)
	}
	return visible
}

// logAOIFiltered 输出 AOI 过滤原因诊断日志
func logAOIFiltered(reason string, self *Player, candidate *Player, distance float64, angle float64, distanceLimit float64, viewAngle float64) {
	if self == nil || candidate == nil {
		return
	}
	key := aoiDiagnosticKey{selfID: self.ID, candidateID: candidate.ID, reason: reason}
	now := time.Now()
	aoiDiagnosticState.Lock()
	lastLogged := aoiDiagnosticState.lastLogged[key]
	if !lastLogged.IsZero() && now.Sub(lastLogged) < aoiDiagnosticLogInterval {
		aoiDiagnosticState.Unlock()
		return
	}
	aoiDiagnosticState.lastLogged[key] = now
	aoiDiagnosticState.Unlock()

	glog.Info(context.Background(), "aoi filtered player",
		glog.String("reason", reason),
		glog.Uint64("self_player_id", self.ID),
		glog.Uint64("candidate_player_id", candidate.ID),
		glog.Float64("self_x", self.X),
		glog.Float64("self_z", self.Z),
		glog.Float64("self_yaw", self.Yaw),
		glog.Float64("candidate_x", candidate.X),
		glog.Float64("candidate_z", candidate.Z),
		glog.Float64("candidate_yaw", candidate.Yaw),
		glog.Bool("candidate_alive", candidate.Alive),
		glog.Float64("distance", distance),
		glog.Float64("angle", angle),
		glog.Float64("distance_limit", distanceLimit),
		glog.Float64("view_angle", viewAngle),
	)
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
