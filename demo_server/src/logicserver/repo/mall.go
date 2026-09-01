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
	"go.mongodb.org/mongo-driver/mongo/options"
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
	// 定义一个结构体来接收 mall 文档
	var mallDoc struct {
		GunList []consts.Gun `bson:"gun_List"`
	}
	err := m.collection.FindOne(ctx, bson.M{}).Decode(&mallDoc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return []consts.Gun{}, nil // 没有商店数据，返回空切片
		}
		return nil, err
	}

	return mallDoc.GunList, nil
}

// GetGunById 通过武器id从商城中获取武器信息。
// mall 集合为单文档，武器嵌在 gun_List 数组中，使用 $elemMatch 精确匹配数组元素
func (m *MallRepo) GetGunById(ctx context.Context, id int32) (consts.Gun, error) {
	var nilGun consts.Gun
	// 只投影出命中的那一个数组元素，避免把整个武器列表拉回来
	var doc struct {
		GunList []consts.Gun `bson:"gun_List"`
	}
	err := m.collection.FindOne(
		ctx,
		bson.M{"gun_List": bson.M{"$elemMatch": bson.M{"id": id}}},
		options.FindOne().SetProjection(bson.M{
			"gun_List": bson.M{"$elemMatch": bson.M{"id": id}},
		}),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nilGun, errors.New("gun not found")
	}
	if err != nil {
		return nilGun, fmt.Errorf("find gun by id err:%w", err)
	}
	// 投影后 gun_List 只会包含命中的那一个元素，防御性判空
	if len(doc.GunList) == 0 {
		return nilGun, errors.New("gun not found")
	}
	return doc.GunList[0], nil
}
