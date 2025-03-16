package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/WuKongIM/plugins/search/search"
	"github.com/WuKongIM/wklog"
	"golang.org/x/sync/errgroup"
)

var pluginNo = "wk.plugin.search" // 插件编号
var Version = "0.0.1"             // 插件版本
var Priority = int32(1)           // 插件优先级

func main() {
	err := pdk.RunServer(New, pluginNo, pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Search struct {
	s *search.Search
	wklog.Log
}

func New() interface{} {

	return &Search{
		s:   search.New(),
		Log: wklog.NewWKLog("search"),
	}
}

// Setup 插件初始化
func (s Search) Setup() {
	s.s.Start()
}

func (s Search) Route(r *pdk.Route) {
	// http://127.0.0.1:5001/plugins/wk.plugin.search/search
	// 全局搜索消息
	r.POST("/search", s.search)

	// 搜索指定用户消息
	r.POST("/usersearch", s.usersearch)
}

// PersistAfter 持久化消息后，更新索引
func (s Search) PersistAfter(c *pdk.Context) {
	for _, message := range c.Messages {
		s.s.MakeIndex(message.ChannelId, uint8(message.ChannelType))
	}
}

func (s Search) search(c *pdk.HttpContext) {
	var req search.SearchReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}

	result, err := s.s.Search(req)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s Search) Stop() {
	s.s.Stop()
}

func (s Search) usersearch(c *pdk.HttpContext) {

	var req struct {
		Uid string `json:"uid"`
		search.SearchReq
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}

	reqBytes, _ := json.Marshal(req)
	fmt.Println("usersearch-req----->", string(reqBytes))

	if req.Uid == "" {
		c.ResponseError(fmt.Errorf("uid is empty"))
		return
	}

	if req.Limit <= 0 || req.Limit > 1000 {
		req.Limit = 20
	}

	if strings.TrimSpace(req.ChannelId) != "" && req.ChannelType == wkproto.ChannelTypePerson {
		req.ChannelId = pdk.GetFakeChannelIDWith(req.Uid, req.ChannelId)
	}

	// 获取用户的会话列表
	conversationChannelResp, err := pdk.S.ConversationChannels(req.Uid)
	if err != nil {
		c.ResponseError(err)
		return
	}
	if len(conversationChannelResp.Channels) == 0 {
		c.JSON(http.StatusOK, search.SearchResp{
			Messages: []*search.Message{},
		})
		return
	}

	channels := conversationChannelResp.Channels

	// 获取频道所属节点
	channelBelogNodeBatchResp, err := pdk.S.ClusterChannelBelongNode(&pluginproto.ClusterChannelBelongNodeReq{
		Channels: channels,
	})
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelBelongNodeResps := channelBelogNodeBatchResp.ClusterChannelBelongNodeResps

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	g, _ := errgroup.WithContext(timeoutCtx)

	messages := make([]*search.Message, 0)
	messageLock := sync.Mutex{}

	var maxCost uint64  // 最大耗时
	var maxTotal uint64 // 最大消息数量
	for _, channelBelongNodeResp := range channelBelongNodeResps {
		nodeId := channelBelongNodeResp.NodeId

		channels := channelBelongNodeResp.Channels

		if len(channels) == 0 {
			continue
		}

		cloneReq := req.SearchReq.Clone()
		cloneReq.Channels = channels

		bodyData, _ := json.Marshal(cloneReq)

		headerMap := c.Request.Headers

		g.Go(func() error {
			// 转发请求到频道所属节点
			resp, err := pdk.S.ForwardHttp(&pluginproto.ForwardHttpReq{
				PluginNo: pluginNo,
				ToNodeId: int64(nodeId),
				Request: &pluginproto.HttpRequest{
					Method:  "POST",
					Headers: headerMap,
					Path:    "/search",
					Body:    bodyData,
				},
			})
			if err != nil {
				return err
			}

			searchResp := &search.SearchResp{}
			err = json.Unmarshal(resp.Body, searchResp)
			if err != nil {
				return err
			}
			messageLock.Lock()

			// 个人频道替换成真实频道
			for _, msg := range searchResp.Messages {
				if msg.ChannelType == wkproto.ChannelTypePerson {
					msg.ChannelId = getRealChannelId(req.Uid, msg.ChannelId)
				}
			}
			messages = append(messages, searchResp.Messages...)
			if searchResp.Cost > maxCost {
				maxCost = searchResp.Cost
			}
			if searchResp.Total > maxTotal {
				maxTotal = searchResp.Total
			}
			messageLock.Unlock()

			return nil
		})
	}
	err = g.Wait()
	if err != nil {
		c.ResponseError(err)
		return
	}

	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Timestamp > messages[j].Timestamp
	})

	// 截取前limit条消息
	if len(messages) > req.Limit {
		messages = messages[:req.Limit]
	}

	c.JSON(http.StatusOK, &search.SearchResp{
		Cost:     maxCost,
		Total:    maxTotal,
		Page:     req.Page,
		Limit:    req.Limit,
		Messages: messages,
	})
}

func parseUint64(str string) uint64 {
	if str == "" {
		return 0
	}
	i, _ := strconv.ParseUint(str, 10, 64)
	return i
}

func getRealChannelId(uid, channelId string) string {
	uids := strings.Split(channelId, "@")
	if len(uids) < 2 {
		return channelId
	}
	if uids[0] == uid {
		return uids[1]
	}
	return uids[0]
}
