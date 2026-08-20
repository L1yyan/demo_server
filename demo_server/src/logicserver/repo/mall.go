package repo

import (
	conf "demo_server/config"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/mongo"
)

const mallCollectionName = "mall" // 商店集合名

type Gun struct {
	Id    int32  //武器id
	Price int64  //武器价格
	Name  string //武器名字
}

type mall struct {
	GunList []Gun `bson:"gun_List"` //武器列表
}

// MallRepo 商店持久化仓库
type MallRepo struct {
	collection *mongo.Collection // 商店集合
}

// NewMallRepo 创建商城仓库
func NewMallRepo(client *mongo.Client, cfg *conf.MongoDBConfig) (*MallRepo, error) {
	if client == nil {
		return nil, errors.New("mongoclient is nil")
	}
	if cfg == nil || strings.TrimSpace(cfg.Database) == "" {
		return nil, errors.New("mongodatabase is empty")
	}
	database := client.Database(cfg.Database)
	return &MallRepo{
		collection: database.Collection(mallCollectionName),
	}, nil
}
