package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/wklog"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"go.uber.org/zap"
)

var PluginNo = "wk.plugin.ai-volcengine" // 插件编号
var Version = "0.0.1"                    // 插件版本
var Priority = int32(1)                  // 插件优先级

func main() {
	err := pdk.RunServer(New, PluginNo, pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Config struct {
	ApiKey pdk.SecretKey `json:"api_key" label:"Volcengine API Key"`
}

type Robot struct {
	wklog.Log
	client *arkruntime.Client
	Config Config // 插件的配置，名字必须为Config, 声明了以后，可以在WuKongIM后台配置
}

func New() interface{} {
	return &Robot{
		Log: wklog.NewWKLog("robot"),
	}
}

// func (r *Robot) Setup() {
// 	r.client = arkruntime.NewClientWithApiKey(
// 		r.Config.ApiKey.String(),
// 	)

// }

func (r *Robot) ConfigUpdate() {
	fmt.Println("config update...", r.Config.ApiKey)
	r.client = arkruntime.NewClientWithApiKey(
		r.Config.ApiKey.String(),
	)
}

// 实现插件的回复消息方法
func (r *Robot) Receive(c *pdk.Context) {

	var payload map[string]interface{}
	err := json.Unmarshal(c.RecvPacket.Payload, &payload)
	if err != nil {
		r.Error("unmarshal payload error:", zap.Error(err))
		return
	}

	var content string
	if payload["content"] != nil {
		content = payload["content"].(string)
	}

	req := model.CreateChatCompletionRequest{
		User:  &c.RecvPacket.FromUid,
		Model: "deepseek-r1-250120",
		Messages: []*model.ChatCompletionMessage{
			{
				Role: model.ChatMessageRoleSystem,
				Content: &model.ChatCompletionMessageContent{
					StringValue: volcengine.String("你是人工智能助手."),
				},
			},
			{
				Role: model.ChatMessageRoleUser,
				Content: &model.ChatCompletionMessageContent{
					StringValue: volcengine.String(content),
				},
			},
		},
	}
	ctx := context.Background()
	stream, err := r.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		fmt.Printf("standard chat error: %v\n", err)
		return
	}

	defer stream.Close()

	//打开流

	imstream, err := c.OpenStream(pdk.StreamWithPayload(&pdk.PayloadText{
		Content: "正在思考中...",
		Type:    1,
	}))
	if err != nil {
		r.Error("open stream error:", zap.Error(err))
		return
	}
	defer imstream.Close()

	for {
		recv, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			fmt.Printf("Stream chat error: %v\n", err)
			return
		}

		if len(recv.Choices) > 0 {

			content := recv.Choices[0].Delta.Content
			if content == "" {
				continue
			}

			fmt.Print(content)

			data, _ := json.Marshal(map[string]interface{}{
				"type":    1,
				"content": content,
			})
			imstream.Write(data)
		}
	}

}
