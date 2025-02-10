package main

import (
	"fmt"
	"net/http"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/WuKongIM/plugins/search/search"
)

var Version = "0.0.1"   // 插件版本
var Priority = int32(1) // 插件优先级

func main() {
	err := pdk.RunServer(New, "wk.plugin.search", pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Search struct {
	s *search.Search
}

func New() interface{} {

	return &Search{
		s: search.New(),
	}
}

func (s Search) Route(r *pdk.Route) {
	// http://127.0.0.1:5001/plugins/wk.plugin.search/search
	r.GET("/search", s.search)
}

func (s Search) PersistAfter(c *pdk.Context) {
	fmt.Println("search---PersistAfter--->", c.Packet, "-->", pdk.S)
	messageBatch := c.Packet.(*pluginproto.MessageBatch)
	for _, message := range messageBatch.Messages {
		s.s.MakeIndex(message.ChannelId, uint8(message.ChannelType))
	}
}

func (s Search) search(c *pdk.HttpContext) {
	from := c.GetQuery("from")

	result, err := s.s.Search(search.SearchReq{
		From: from,
	})
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s Search) Stop() {
	s.s.Stop()
}
