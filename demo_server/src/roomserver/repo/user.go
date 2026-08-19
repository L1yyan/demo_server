package repo

import (
	_ "context"
	conf "demo_server/config"
	_ "demo_server/pkg/mongodb"
	_ "demo_server/pkg/redis"
	"demo_server/src/roomserver/consts"
	"errors"
	_ "fmt"
	"strings"
	_ "time"

	_ "go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	_ "go.mongodb.org/mongo-driver/mongo/options"
)

const usersCollectionName    = "users"    // 用户集合名

// User 用户数据
type User struct {
	ID       primitive.ObjectID `bson:"_id,omitempty"` // MongoDB文档ID
	UserInfo consts.UserInfo    `bson:",inline"`       // 用户信息
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

	database := client.Database(cfg.Database)
	repo := &UserRepo{
		collection: database.Collection(usersCollectionName),
	}
	return repo, nil
}
