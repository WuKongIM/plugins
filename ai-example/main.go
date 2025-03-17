package main

import (
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/wklog"
	"go.uber.org/zap"
)

var pluginNo = "wk.plugin.ai-example" // 插件编号
var Version = "0.0.1"                 // 插件版本
var Priority = int32(1)               // 插件优先级

func main() {
	err := pdk.RunServer(New, pluginNo, pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Config struct {
	Name string `json:"name" label:"AI名字"`
}

type AIExample struct {
	wklog.Log
	Config Config // 插件的配置，名字必须为Config, 声明了以后，可以在WuKongIM后台配置
}

func New() interface{} {

	return &AIExample{
		Log: wklog.NewWKLog("AIExample"),
		Config: Config{
			Name: "AIExample",
		},
	}
}

// 收到消息
func (a *AIExample) Receive(c *pdk.Context) {

	content := a.getContent(c)
	// 回复
	data, _ := json.Marshal(map[string]interface{}{
		"type":    1,
		"content": fmt.Sprintf("我是%s,收到您的消息：%s", a.Config.Name, content),
	})
	c.Reply(data)
}

func (a AIExample) getContent(c *pdk.Context) string {
	var payload map[string]interface{}
	err := json.Unmarshal(c.RecvPacket.Payload, &payload)
	if err != nil {
		a.Error("unmarshal payload error:", zap.Error(err))
		return ""
	}

	var content string
	if payload["content"] != nil {
		content = payload["content"].(string)
	}

	return content
}
