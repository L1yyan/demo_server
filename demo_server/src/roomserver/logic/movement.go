package logic

import (
	"fmt"
	"math"

	roompb "demo_server/gen/room"
)

const (
	defaultPlayerMoveSpeed = 4.0    // 玩家默认移动速度，单位/秒
	defaultWorldLimit      = 100.0  // 简单世界边界，后续替换为地图碰撞
	minPlayerPitch         = -89.0  // 最小俯仰角
	maxPlayerPitch         = 89.0   // 最大俯仰角
	accelerationlimit      = 20 / 1 // 摇杆方向移动最大加速度限制
	g                      = -38    //重力加速度
	jumpSpeed              = 8.7    //跳跃初速度
	maxJumpHeight
)

type authoritativeInput struct {
	ClientTick int64   // 客户端输入帧号
	MoveX      float64 // 归一化后的左右移动输入
	MoveZ      float64 // 归一化后的前后移动输入
	Yaw        float64 // 服务端归一化后的水平视角
	Pitch      float64 // 服务端限制后的垂直视角
	Fire       bool    // 是否请求开火
	Jump       bool    // 是否请求跳跃
	Squat      bool    // 是否请求下蹲
}

// sanitizePlayerInput 校验并归一化客户端输入
func sanitizePlayerInput(input *roompb.PlayerInput) (authoritativeInput, bool) {
	if input == nil || !isFinite(input.MoveX) || !isFinite(input.MoveZ) || !isFinite(input.Yaw) || !isFinite(input.Pitch) {
		return authoritativeInput{}, false
	}

	moveX := clampFloat(input.MoveX, -1, 1)
	moveZ := clampFloat(input.MoveZ, -1, 1)
	length := math.Sqrt(moveX*moveX + moveZ*moveZ)
	if length > 1 {
		moveX /= length
		moveZ /= length
	}

	return authoritativeInput{
		ClientTick: input.ClientTick,
		MoveX:      moveX,
		MoveZ:      moveZ,
		Yaw:        normalizeDegrees(input.Yaw),
		Pitch:      clampFloat(input.Pitch, minPlayerPitch, maxPlayerPitch),
		Fire:       input.Fire,
		Jump:       input.Jump,
		Squat:      input.Squat,
	}, true
}

// buildMovePlayerRequest 按服务端 tick 生成物理移动请求
func buildMovePlayerRequest(player *Player, input authoritativeInput, tickRate int) (MovePlayerRequest, bool) {
	if player == nil || player.ID == 0 || tickRate <= 0 {
		return MovePlayerRequest{}, false
	}

	// 根据服务端认可的 yaw 将本地输入转换为世界坐标移动 最新摇杆移动
	move := movementDirection(input.Yaw, input.MoveX, input.MoveZ)
	player.Move = move
	//去掉上一帧摇杆与当前帧摇杆的反方向分量
	if player.LastMove.Dot(&player.Move) < 0 {
		fmt.Println("1")
		moveNormal := player.Move.Normalize()
		lastMoveWeightMoveDot := player.Move.Dot(&player.LastMove)
		lastMoveWeightMove := moveNormal.Mul(lastMoveWeightMoveDot / move.Len())
		player.LastMove = player.LastMove.Sub(&lastMoveWeightMove)
	}
	//如果lastmove与当前move方向变化量过大，做限制
	da := accelerationlimit * (1 / float64(tickRate))
	dMove := move.Sub(&player.LastMove)
	if dMove.SqrMagnitude() > da*da {
		fmt.Println("2")
		newdMove := dMove.Normalize()
		newdMove = newdMove.Mul(da)
		move = player.LastMove.Add(&newdMove)
	}
	player.LastMove = move

	//合成移动向量
	distance := 0.0
	if vectorLength(move) > 0 {
		distance = defaultPlayerMoveSpeed * (1 / float64(tickRate))
	}
	deltaTime := 1 / float64(tickRate)
	var disp Vector3
	//x-z水平方向位移向量
	dispNormal := move.Normalize()
	disp = dispNormal.Mul(distance)
	//下蹲一半伊苏
	if player.Crouched && input.Squat {
		disp = disp.Mul(0.5)
	}
	//y方向位移向量 不做头顶的碰撞检测，cct自己会处理
	if player.Grounded && input.Jump {
		player.VerticalVelocity = jumpSpeed + g*deltaTime
		// 标记玩家不在地面
		player.Grounded = false
	} else if !player.Grounded {
		// 在空中时计算y方向位移
		player.VerticalVelocity += g * deltaTime
	}
	disp.Y = player.VerticalVelocity * deltaTime

	return MovePlayerRequest{
		PlayerID:         player.ID,
		DeltaTime:        deltaTime,
		Disp:             disp,
		Jump:             input.Jump,
		Squat:            input.Squat,
		Crouched:         player.Crouched,
		Grounded:         player.Grounded,
		VerticalVelocity: player.VerticalVelocity,
	}, true
}

// shouldMovePlayer 判断本帧是否需要交给物理后端推进
func shouldMovePlayer(req MovePlayerRequest) bool {
	return req.Disp.SqrMagnitude() > 0 || req.Jump || req.Squat != req.Crouched || !req.Grounded || req.VerticalVelocity != 0
}

// applyViewRotation 更新玩家服务端认可的视角
func applyViewRotation(player *Player, input authoritativeInput) {
	if player == nil {
		return
	}
	player.Yaw = input.Yaw
	player.Pitch = input.Pitch
}

// movementDirection 根据水平视角计算世界坐标移动方向
func movementDirection(yaw float64, moveX float64, moveZ float64) Vector3 {
	yawRad := yaw * math.Pi / 180
	forwardX := math.Sin(yawRad)
	forwardZ := math.Cos(yawRad)
	rightX := math.Cos(yawRad)
	rightZ := -math.Sin(yawRad)

	return Vector3{
		X: forwardX*moveZ + rightX*moveX,
		Z: forwardZ*moveZ + rightZ*moveX,
	}
}

// viewDirection 根据玩家视角计算服务端认可的朝向
func viewDirection(yaw float64, pitch float64) Vector3 {
	yawRad := yaw * math.Pi / 180
	pitchRad := pitch * math.Pi / 180
	cosPitch := math.Cos(pitchRad)
	return Vector3{
		X: math.Sin(yawRad) * cosPitch,
		Y: math.Sin(pitchRad),
		Z: math.Cos(yawRad) * cosPitch,
	}
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

// clampFloat 将数值限制在指定范围内
func clampFloat(value float64, minValue float64, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// isFinite 判断浮点数是否为有效有限值
func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
