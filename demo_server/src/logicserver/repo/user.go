package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	conf "demo_server/config"
	"demo_server/pkg/mongodb"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

const usersCollectionName = "users" // 用户集合名

var ErrUserNotFound = errors.New("user not found")

// User 用户登录数据
type User struct {
	ID           primitive.ObjectID `bson:"_id,omitempty"` // MongoDB文档ID
	UserID       uint64             `bson:"user_id"`       // 业务用户ID
	Email        string             `bson:"email"`         // 邮箱
	PasswordHash string             `bson:"password_hash"` // 密码哈希
}

// UserRepo 用户持久化仓库
type UserRepo struct {
	collection *mongo.Collection // 用户集合
}

// NewUserRepo 创建用户仓库
func NewUserRepo(client *mongo.Client, cfg *conf.MongoDBConfig) (*UserRepo, error) {
	if client == nil {
		return nil, errors.New("mongo client is nil")
	}
	if cfg == nil || strings.TrimSpace(cfg.Database) == "" {
		return nil, errors.New("mongo database is empty")
	}
	return &UserRepo{collection: client.Database(cfg.Database).Collection(usersCollectionName)}, nil
}

// FindByEmail 根据邮箱查询登录用户
func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*User, error) {
	if r == nil || r.collection == nil {
		return nil, errors.New("user repo is nil")
	}
	if strings.TrimSpace(email) == "" {
		return nil, errors.New("email is empty")
	}

	var user User
	// 邮箱是登录唯一标识，查询时统一使用小写邮箱
	err := r.collection.FindOne(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email))}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return &user, nil
}

// MongoClient 返回已初始化的 MongoDB 客户端
func MongoClient() *mongo.Client {
	return mongodb.Instance()
}
