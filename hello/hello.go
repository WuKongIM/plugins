package main

import (
	"fmt"
	"net/http"

	"github.com/WuKongIM/go-pdk/pdk"
)

var PluginNo = "wk.plugin.hello" // 插件编号
var Version = "0.0.1"            // 插件版本
var Priority = int32(1)          // 插件优先级

func main() {

	err := pdk.RunServer(New, PluginNo, pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Config struct {
	Test string `json:"test" label:"测试"`
}

type Hello struct {
	Config Config // 插件的配置，名字必须为Config, 声明了以后，可以在WuKongIM后台配置，从而填充这个结构体内的字段
}

func New() interface{} {
	return &Hello{}
}

// 插件初始化
func (s *Hello) Setup() {
	fmt.Println("plugin setup...")
}

// 注册http路由
func (s *Hello) Route(c *pdk.Route) {
	// http://127.0.0.1:5001/plugins/wk.plugin.hello/hello
	c.GET("/hello", s.sayHello)
}

// 消息发送前（适合敏感词过滤之类的插件）(同步调用)
func (s *Hello) Send(c *pdk.Context) {

	sendPacket := c.SendPacket
	sendPacket.Payload = []byte("{\"content\":\"hello\",\"type\":1}")
}

// 消息持久化后（适合消息搜索类插件）（默认异步调用）
func (s *Hello) PersistAfter(c *pdk.Context) {
	fmt.Println("PersistAfter:", c.Messages)
}

// 收到消息（适合AI类插件）（默认异步调用）
func (s *Hello) Receive(c *pdk.Context) {

}

// 配置Config有变更的时候会调用这个方法
func (s *Hello) ConfigUpdate() {
	fmt.Println("config update...", s.Config.Test)
}

// 插件停止
func (s *Hello) Stop() {
	fmt.Println("plugin stop...")
}

func (s *Hello) sayHello(c *pdk.HttpContext) {
	name := c.GetQuery("name")

	c.JSON(http.StatusOK, map[string]interface{}{
		"say": fmt.Sprintf("hello %s", name),
	})
}
