package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/wklog"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var PluginNo = "wk.plugin.redis-sensitive-words" // 插件编号
var Version = "0.0.1"                            // 插件版本
var Priority = int32(1)                          // 插件优先级

func main() {
	err := pdk.RunServer(New, PluginNo, pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Config struct {
	RedisAddr string        `json:"redis_addr" label:"Redis Address"`
	RedisPass pdk.SecretKey `json:"redis_pass" label:"Redis Password"`
	RedisDB   int           `json:"redis_db" label:"Redis DB Index"`
	//敏感词 redis key
	RedisKey string `json:"redis_key" label:"SensitiveWords Redis Key"`
}

type Robot struct {
	wklog.Log
	Config      Config // 插件的配置，名字必须为Config, 声明了以后，可以在WuKongIM后台配置
	redisClient *redis.Client
	ctx         context.Context
}

func New() interface{} {
	return &Robot{
		Log: wklog.NewWKLog("robot"),
		Config: Config{
			RedisAddr: "localhost:6379", // 默认Redis地址
			RedisDB:   0,                // 默认Redis数据库
		},
		ctx: context.Background(),
	}
}

// ConfigUpdate is called when the config is updated
func (r *Robot) ConfigUpdate() {
	r.Info("Config updated, reconnecting to Redis...")
	// 创建Redis客户端
	if r.redisClient != nil {
		r.redisClient.Close()
	}
	r.redisClient = redis.NewClient(&redis.Options{
		Addr:     r.Config.RedisAddr,
		Password: r.Config.RedisPass.String(),
		DB:       r.Config.RedisDB,
	})
	// 测试连接
	_, err := r.redisClient.Ping(r.ctx).Result()
	if err != nil {
		r.Error("Failed to connect to Redis", zap.Error(err))
	} else {
		r.Info("Connected to Redis successfully")
	}
}

// 消息发送前（适合敏感词过滤之类的插件）(同步调用)
func (r *Robot) Send(c *pdk.Context) {
	if r.redisClient == nil {
		r.Error("Redis client not initialized")
		return
	}

	// 解析消息内容
	var payload map[string]interface{}
	err := json.Unmarshal(c.SendPacket.Payload, &payload)
	if err != nil {
		r.Error("Failed to unmarshal payload", zap.Error(err))
		return
	}

	// 获取消息内容
	var content string
	if payload["content"] != nil {
		content = payload["content"].(string)
	}

	// 检查是否包含敏感词
	sensitiveWords, err := r.redisClient.SMembers(r.ctx, r.Config.RedisKey).Result()
	if err != nil {
		r.Error("Failed to get sensitive words from Redis", zap.Error(err))
		return
	}

	// 检查消息是否包含敏感词
	for _, word := range sensitiveWords {
		if strings.Contains(strings.ToLower(content), strings.ToLower(word)) {
			r.Info("Message contains sensitive word, replacing with asterisks", zap.String("word", word))
			// 替换敏感词为星号
			replacedContent := strings.ReplaceAll(
				strings.ToLower(content),
				strings.ToLower(word),
				strings.Repeat("*", len(word)),
			)

			// 更新消息内容
			payload["content"] = replacedContent
			newPayload, _ := json.Marshal(payload)
			c.SendPacket.Payload = newPayload

			break
		}
	}
}

// Receive 实现插件的回复消息方法
func (r *Robot) Receive(c *pdk.Context) {
	if r.redisClient == nil {
		r.Error("Redis client not initialized")
		return
	}

	// 解析消息内容
	var payload map[string]interface{}
	err := json.Unmarshal(c.RecvPacket.Payload, &payload)
	if err != nil {
		r.Error("unmarshal payload error", zap.Error(err))
		return
	}

	// 获取消息内容
	var content string
	if payload["content"] != nil {
		content = payload["content"].(string)
	}

	// 消息为空则忽略
	if content == "" {
		return
	}

	// 检查Redis中是否已存在该内容
	// 使用 "tg_messages:{channelId}" 作为集合名来存储不同群组的消息
	redisKey := r.Config.RedisKey

	exists, err := r.redisClient.SIsMember(r.ctx, redisKey, content).Result()
	if err != nil {
		r.Error("Failed to check message in Redis", zap.Error(err))
		return
	}

	// 打开流用于回复
	imstream, err := c.OpenStream()
	if err != nil {
		r.Error("open stream error", zap.Error(err))
		return
	}
	defer imstream.Close()

	if exists {
		// 如果消息已存在，删除它
		_, err := r.redisClient.SRem(r.ctx, redisKey, content).Result()
		if err != nil {
			r.Error("Failed to remove message from Redis", zap.Error(err))
			r.sendResponse(imstream, "删除失败，请稍后再试")
			return
		}
		r.sendResponse(imstream, fmt.Sprintf("删除成功: %s", content))
	} else {
		// 如果消息不存在，添加它
		_, err := r.redisClient.SAdd(r.ctx, redisKey, content).Result()
		if err != nil {
			r.Error("Failed to add message to Redis", zap.Error(err))
			r.sendResponse(imstream, "添加失败，请稍后再试")
			return
		}
		r.sendResponse(imstream, fmt.Sprintf("添加成功: %s", content))
	}
}

// 发送响应消息
func (r *Robot) sendResponse(imstream *pdk.Stream, content string) {
	data, _ := json.Marshal(map[string]interface{}{
		"type":    1,
		"content": content,
	})
	imstream.Write(data)
}
