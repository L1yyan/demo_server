package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	conf "demo_server/config"
	"demo_server/pkg/mongodb"

	"demo_server/src/logicserver/consts"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	usersCollectionName    = "users"    // 用户集合名
	countersCollectionName = "counters" // 自增序列集合名
	userIDCounterName      = "users"    // 用户ID序列名
	userIndexTimeout       = 10 * time.Second
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
)

// User 用户登录数据
type User struct {
	ID       primitive.ObjectID `bson:"_id,omitempty"` // MongoDB文档ID
	UserInfo consts.UserInfo    `bson:",inline"`       // 用户信息
}

// UserRepo 用户持久化仓库
type UserRepo struct {
	collection *mongo.Collection // 用户集合
	counters   *mongo.Collection // 自增序列集合
}

type userIDCounter struct {
	ID  string `bson:"_id"` // 序列名
	Seq int64  `bson:"seq"` // 当前序列值
}

// NewUserRepo 创建用户仓库
func NewUserRepo(client *mongo.Client, cfg *conf.MongoDBConfig) (*UserRepo, error) {
	if client == nil {
		return nil, errors.New("mongo client is nil")
	}
	if cfg == nil || strings.TrimSpace(cfg.Database) == "" {
		return nil, errors.New("mongo database is empty")
	}

	database := client.Database(cfg.Database)
	repo := &UserRepo{
		collection: database.Collection(usersCollectionName),
		counters:   database.Collection(countersCollectionName),
	}
	if err := repo.ensureIndexes(context.Background()); err != nil {
		return nil, err
	}
	return repo, nil
}

// CreateUser 创建邮箱密码用户并写入 MongoDB
func (r *UserRepo) CreateUser(ctx context.Context, email string, passwordHash string) (*User, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	email = normalizeEmail(email)
	passwordHash = strings.TrimSpace(passwordHash)
	if email == "" {
		return nil, errors.New("email is empty")
	}
	if passwordHash == "" {
		return nil, errors.New("password hash is empty")
	}

	// 先通过 Mongo 原子计数器获取业务用户ID，保证多实例注册不会冲突
	userID, err := r.nextUserID(ctx)
	if err != nil {
		return nil, err
	}

	user := &User{
		ID: primitive.NewObjectID(),
		UserInfo: consts.UserInfo{
			UserID:         userID,
			Email:          email,
			Nickname:       fmt.Sprintf("Player%d", userID),
			Level:          "1",
			Experience:     0,
			Coins:          10000,
			ProfilePhotoID: 0,
			PasswordHash:   passwordHash,
		},
	}
	if _, err := r.collection.InsertOne(ctx, user); err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrUserAlreadyExists
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

// FindByEmail 根据邮箱查询登录用户
func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*User, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	email = normalizeEmail(email)
	if email == "" {
		return nil, errors.New("email is empty")
	}

	var user User
	// 邮箱是登录唯一标识，查询时统一使用小写邮箱
	err := r.collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return &user, nil
}

// FindByUserID 根据业务用户ID查询登录用户
func (r *UserRepo) FindByUserID(ctx context.Context, userID uint64) (consts.UserInfo, error) {
	if err := r.validate(); err != nil {
		return consts.UserInfo{}, err
	}
	if userID == 0 {
		return consts.UserInfo{}, errors.New("user id is empty")
	}

	var user User
	err := r.collection.FindOne(ctx, bson.M{"user_id": userID}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return consts.UserInfo{}, ErrUserNotFound
	}
	if err != nil {
		return consts.UserInfo{}, fmt.Errorf("find user by user id: %w", err)
	}
	userInfo := user.UserInfo
	return userInfo, nil
}

// MongoClient 返回已初始化的 MongoDB 客户端
func MongoClient() *mongo.Client {
	return mongodb.Instance()
}

// ensureIndexes 创建用户登录需要的唯一索引
func (r *UserRepo) ensureIndexes(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	indexCtx, cancel := context.WithTimeout(ctx, userIndexTimeout)
	defer cancel()

	// 邮箱唯一索引用于兜底并发注册，user_id 唯一索引用于保护业务ID
	_, err := r.collection.Indexes().CreateMany(indexCtx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetName("uniq_email").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}},
			Options: options.Index().SetName("uniq_user_id").SetUnique(true),
		},
	})
	if err != nil {
		return fmt.Errorf("create user indexes: %w", err)
	}
	return nil
}

// nextUserID 获取下一个业务用户ID
func (r *UserRepo) nextUserID(ctx context.Context) (uint64, error) {
	var counter userIDCounter
	// Mongo 的单文档更新具备原子性，用 $inc 生成跨进程唯一ID
	err := r.counters.FindOneAndUpdate(
		ctx,
		bson.M{"_id": userIDCounterName},
		bson.M{"$inc": bson.M{"seq": int64(1)}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return 0, fmt.Errorf("next user id: %w", err)
	}
	if counter.Seq <= 0 {
		return 0, errors.New("invalid user id sequence")
	}
	return uint64(counter.Seq), nil
}

// validate 校验仓库状态
func (r *UserRepo) validate() error {
	if r == nil || r.collection == nil || r.counters == nil {
		return errors.New("user repo is nil")
	}
	return nil
}

// normalizeEmail 统一登录邮箱格式
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// isDuplicateKeyError 判断 Mongo 写入是否违反唯一索引
func isDuplicateKeyError(err error) bool {
	var writeException mongo.WriteException
	if errors.As(err, &writeException) {
		for _, writeErr := range writeException.WriteErrors {
			if writeErr.Code == 11000 || writeErr.Code == 11001 {
				return true
			}
		}
	}

	var bulkWriteException mongo.BulkWriteException
	if errors.As(err, &bulkWriteException) {
		for _, writeErr := range bulkWriteException.WriteErrors {
			if writeErr.Code == 11000 || writeErr.Code == 11001 {
				return true
			}
		}
	}

	var commandError mongo.CommandError
	if errors.As(err, &commandError) {
		return commandError.Code == 11000 || commandError.Code == 11001
	}
	return strings.Contains(err.Error(), "E11000")
}

// ModifyNickname 修改用户昵称
func (r *UserRepo) ModifyNickname(ctx context.Context, userID uint64, nickname string) error {
	if err := r.validate(); err != nil {
		return err
	}
	// 按业务用户ID更新昵称，用户不存在时通过匹配数量返回明确错误
	result, err := r.collection.UpdateOne(
		ctx,
		bson.M{"user_id": userID},
		bson.M{"$set": bson.M{"nickname": nickname}},
	)
	if err != nil {
		return fmt.Errorf("modify nickname: %w", err)
	}
	if result.MatchedCount == 0 {
		return ErrUserNotFound
	}
	return nil
}
