package repo

import (
	"context"
	conf "demo_server/config"
	"demo_server/src/logicserver/consts"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const mallCollectionName = "mall" // 商店集合名

type mall struct {
	GunList []consts.Gun `bson:"gun_List"` //武器列表
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

// GetMallAllDetails 获取商店所有物品信息  商品多了改成分页 先这样写
func (m *MallRepo) GetMallAllDetails(ctx context.Context) ([]consts.Gun, error) {
	filter := bson.M{}
	cursor, err := m.collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var guns []consts.Gun
	if err := cursor.All(ctx, &guns); err != nil {
		return nil, err
	}
	if guns == nil {
		return []consts.Gun{}, nil
	}
	return guns, nil
}

// GetGunPriceById 通过武器id获取商城里武器的价格
func (m *MallRepo) GetGunPriceById(ctx context.Context, id int32) (int64, error) {
	var gun consts.Gun
	err := m.collection.FindOne(ctx, bson.M{"gun_id": id}).Decode(&gun)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, errors.New("gun not found")
	}
	if err != nil {
		return 0, fmt.Errorf("find gun price by id err:%w", err)
	}
	return gun.Price, nil
}
