package physx

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"demo_server/src/roomserver/logic"
)

const (
	mapCollisionUnitsMeter   = "meter"     // 地图碰撞单位
	mapCollisionRotationXYZW = "quat_xyzw" // 地图旋转四元数格式
	mapColliderShapeBox      = "box"       // box 碰撞体
	projectRootMarkFile      = "go.mod"    // 项目根目录标记文件
)

var (
	// ErrMapCollisionPathEmpty 表示地图碰撞文件路径为空
	ErrMapCollisionPathEmpty = errors.New("map collision path is empty")
	// ErrUnsupportedMapColliderShape 表示地图碰撞体形状暂不支持
	ErrUnsupportedMapColliderShape = errors.New("unsupported map collider shape")
)

// Quaternion 四元数旋转
//
// JSON 中使用 Unity 导出的 [x,y,z,w] 顺序
type Quaternion struct {
	X float64 // X分量
	Y float64 // Y分量
	Z float64 // Z分量
	W float64 // W分量
}

// MapCollision 服务端地图碰撞配置
type MapCollision struct {
	MapID       string          `json:"map_id"`       // 地图ID
	MapVersion  int             `json:"map_version"`  // 地图版本
	PhysicsHash string          `json:"physics_hash"` // 物理数据hash
	Units       string          `json:"units"`        // 坐标单位
	Rotation    string          `json:"rotation"`     // 旋转格式
	Colliders   []MapCollider   `json:"colliders"`    // 碰撞体列表
	SpawnPoints []MapSpawnPoint `json:"spawn_points"` // 出生点列表
}

// MapCollider 地图碰撞体配置
type MapCollider struct {
	ID        string     `json:"id"`         // 碰撞体ID
	Shape     string     `json:"shape"`      // 碰撞形状
	Position  Vector3Raw `json:"position"`   // 世界坐标
	Rotation  QuatRaw    `json:"rotation"`   // 世界旋转
	Size      Vector3Raw `json:"size"`       // box完整尺寸
	Radius    float64    `json:"radius"`     // sphere或capsule半径
	Height    float64    `json:"height"`     // capsule高度
	Direction string     `json:"direction"`  // capsule轴向
	IsTrigger bool       `json:"is_trigger"` // 是否触发器
}

// MapSpawnPoint 地图出生点配置
type MapSpawnPoint struct {
	ID       string     `json:"id"`       // 出生点ID
	Position Vector3Raw `json:"position"` // 世界坐标
	Rotation QuatRaw    `json:"rotation"` // 世界旋转
}

// Vector3Raw JSON 三维数组
//
// 用数组是为了和 Unity 导出格式保持一致
type Vector3Raw [3]float64

// QuatRaw JSON 四元数数组
//
// 用数组是为了和 Unity 导出格式保持一致
type QuatRaw [4]float64

// loadMapCollision 加载并校验地图碰撞配置
func loadMapCollision(path string, expectedMapID string) (*MapCollision, error) {
	resolvedPath, err := resolveProjectPath(path)
	if err != nil {
		return nil, err
	}

	// 读取 Unity 导出的服务端碰撞配置
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read map collision: %w", err)
	}

	var collision MapCollision
	if err := json.Unmarshal(data, &collision); err != nil {
		return nil, fmt.Errorf("parse map collision: %w", err)
	}
	if err := validateMapCollision(&collision, strings.TrimSpace(expectedMapID)); err != nil {
		return nil, err
	}
	return &collision, nil
}

// resolveProjectPath 将相对路径解析到项目根目录
func resolveProjectPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ErrMapCollisionPathEmpty
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	root, err := findProjectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, path), nil
}

// validateMapCollision 校验地图碰撞配置
func validateMapCollision(collision *MapCollision, expectedMapID string) error {
	if collision == nil {
		return errors.New("map collision is nil")
	}
	if strings.TrimSpace(collision.MapID) == "" {
		return errors.New("map collision id is empty")
	}
	if expectedMapID != "" && collision.MapID != expectedMapID {
		return fmt.Errorf("map collision id mismatch: expected %s, got %s", expectedMapID, collision.MapID)
	}
	if collision.Units != "" && collision.Units != mapCollisionUnitsMeter {
		return fmt.Errorf("unsupported map collision units: %s", collision.Units)
	}
	if collision.Rotation != "" && collision.Rotation != mapCollisionRotationXYZW {
		return fmt.Errorf("unsupported map collision rotation: %s", collision.Rotation)
	}
	if len(collision.Colliders) == 0 {
		return errors.New("map collision colliders is empty")
	}
	for index := range collision.Colliders {
		if err := validateMapCollider(collision.Colliders[index]); err != nil {
			return fmt.Errorf("invalid collider %d: %w", index, err)
		}
	}
	for index := range collision.SpawnPoints {
		if err := validateMapSpawnPoint(collision.SpawnPoints[index]); err != nil {
			return fmt.Errorf("invalid spawn point %d: %w", index, err)
		}
	}
	return nil
}

// validateMapCollider 校验单个碰撞体配置
func validateMapCollider(collider MapCollider) error {
	if strings.TrimSpace(collider.ID) == "" {
		return errors.New("collider id is empty")
	}
	if !validRawVector3(collider.Position) {
		return errors.New("collider position is invalid")
	}
	if !validRawQuaternion(collider.Rotation) {
		return errors.New("collider rotation is invalid")
	}

	// 当前 PhysX 接入第一阶段只加载实体 box，触发器留给后续区域逻辑处理
	if collider.IsTrigger {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(collider.Shape)) {
	case mapColliderShapeBox:
		if !validRawVector3(collider.Size) || collider.Size[0] <= 0 || collider.Size[1] <= 0 || collider.Size[2] <= 0 {
			return errors.New("box size is invalid")
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedMapColliderShape, collider.Shape)
	}
}

// validateMapSpawnPoint 校验出生点配置
func validateMapSpawnPoint(spawnPoint MapSpawnPoint) error {
	if strings.TrimSpace(spawnPoint.ID) == "" {
		return errors.New("spawn point id is empty")
	}
	if !validRawVector3(spawnPoint.Position) {
		return errors.New("spawn point position is invalid")
	}
	if !validRawQuaternion(spawnPoint.Rotation) {
		return errors.New("spawn point rotation is invalid")
	}
	return nil
}

// validRawVector3 判断 JSON 三维数组是否有效
func validRawVector3(value Vector3Raw) bool {
	return isFinite(value[0]) && isFinite(value[1]) && isFinite(value[2])
}

// validRawQuaternion 判断 JSON 四元数是否有效
func validRawQuaternion(value QuatRaw) bool {
	if !isFinite(value[0]) || !isFinite(value[1]) || !isFinite(value[2]) || !isFinite(value[3]) {
		return false
	}
	lengthSquared := value[0]*value[0] + value[1]*value[1] + value[2]*value[2] + value[3]*value[3]
	return lengthSquared > 0.000001
}

// toLogicVector3 将 JSON 三维数组转换为 logic 向量
func toLogicVector3(value Vector3Raw) logic.Vector3 {
	return logic.Vector3{X: value[0], Y: value[1], Z: value[2]}
}

// toQuaternion 将 JSON 四元数数组转换为结构体
func toQuaternion(value QuatRaw) Quaternion {
	return Quaternion{X: value[0], Y: value[1], Z: value[2], W: value[3]}
}

// toLogicSpawnPoints 将地图出生点转换为 logic 出生点
func toLogicSpawnPoints(spawnPoints []MapSpawnPoint) []logic.SpawnPoint {
	result := make([]logic.SpawnPoint, 0, len(spawnPoints))
	for _, spawnPoint := range spawnPoints {
		result = append(result, logic.SpawnPoint{
			ID:       spawnPoint.ID,
			Position: toLogicVector3(spawnPoint.Position),
			Yaw:      quaternionYaw(toQuaternion(spawnPoint.Rotation)),
		})
	}
	return result
}

// quaternionYaw 从 Unity Y 轴四元数中提取水平朝向
func quaternionYaw(value Quaternion) float64 {
	sinyCosp := 2 * (value.W*value.Y + value.X*value.Z)
	cosyCosp := 1 - 2*(value.Y*value.Y+value.Z*value.Z)
	return normalizeDegrees(math.Atan2(sinyCosp, cosyCosp) * 180 / math.Pi)
}

// normalizeDegrees 将角度归一化到 -180 到 180
func normalizeDegrees(angle float64) float64 {
	angle = math.Mod(angle, 360)
	if angle > 180 {
		angle -= 360
	}
	if angle < -180 {
		angle += 360
	}
	return angle
}

// isFinite 判断浮点数是否为有限值
func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// findProjectRoot 从当前目录向上查找项目根目录
func findProjectRoot() (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	currentDir := workDir
	for {
		markPath := filepath.Join(currentDir, projectRootMarkFile)
		if _, err := os.Stat(markPath); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", errors.New("project root not found")
		}
		currentDir = parentDir
	}
}
