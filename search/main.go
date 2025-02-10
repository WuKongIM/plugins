package main

import (
	"fmt"

	"github.com/WuKongIM/GoPDK/pdk"
	"github.com/WuKongIM/GoPDK/pdk/pluginproto"
)

var Version = "0.0.1"   // 插件版本
var Priority = int32(1) // 插件优先级

func main() {
	err := pdk.RunServer(New, "com.githubim.plugin.search", pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Search struct {
}

func New() interface{} {

	return &Search{}
}

func (s Search) PersistAfter(c *pdk.Context) {
	fmt.Println("search---PersistAfter--->", c.Packet)
	messageBatch := c.Packet.(*pluginproto.MessageBatch)
}
