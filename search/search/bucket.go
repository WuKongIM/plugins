package search

import (
	"fmt"
	"time"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/WuKongIM/wklog"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type bucket struct {
	id        int
	s         *Search
	indexChan chan indexReq
	wklog.Log
}

func newBucket(id int, s *Search) *bucket {
	return &bucket{
		id:        id,
		indexChan: make(chan indexReq, 1000),
		s:         s,
		Log:       wklog.NewWKLog(fmt.Sprintf("bucket[%d]", id)),
	}
}

func (b *bucket) start() {
	go b.loopIndex()
}

func (b *bucket) loopIndex() {
	batchSize := 100
	reqs := make([]indexReq, 0, batchSize)
	done := false
	for req := range b.indexChan {
		reqs = append(reqs, req)
		for !done {
			select {
			case req := <-b.indexChan:
				if len(reqs) > batchSize {
					done = true
					break
				}
				reqs = append(reqs, req)
			default:
				done = true
			}
		}
		b.handleIndex(reqs)
		reqs = reqs[:0]
		done = false
	}
}

func (b *bucket) handleIndex(indexs []indexReq) {

	reqs := make([]*pluginproto.ChannelMessageReq, 0, len(indexs))
	for _, indexReq := range indexs {
		msgSeq, err := b.s.db.getChannelMaxMessageSeq(indexReq.channelId, indexReq.channelType)
		if err != nil {
			continue
		}
		reqs = append(reqs, &pluginproto.ChannelMessageReq{
			ChannelId:       indexReq.channelId,
			ChannelType:     uint32(indexReq.channelType),
			StartMessageSeq: msgSeq + 1,
			Limit:           500,
		})

	}

	req := &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: reqs,
	}
	messageResp, err := pdk.S.GetChannelMessages(req)
	if err != nil {
		b.Error("get channel message error", zap.Error(err), zap.Int("channelMessageReqs", len(reqs)))
		return
	}

	if len(messageResp.ChannelMessageResps) == 0 {
		b.Warn("channel message is empty", zap.Int("channelMessageReqs", len(reqs)))
		return
	}
	for _, resp := range messageResp.ChannelMessageResps {
		if len(resp.Messages) == 0 {
			continue
		}
		// 索引消息
		err = b.buildIndex(resp.ChannelId, uint8(resp.ChannelType), resp.Messages)
		if err != nil {
			b.Error("search index error", zap.Error(err))
			continue
		}

		lastMsg := resp.Messages[len(resp.Messages)-1]
		err = b.s.db.setChannelMaxMessageSeq(resp.ChannelId, uint8(resp.ChannelType), lastMsg.MessageSeq)
		if err != nil {
			b.Error("set channel max message seq error", zap.Error(err))
		}

		// 如果消息数量大于等于limit，继续请求
		if len(resp.Messages) >= int(resp.Limit) {

			time.Sleep(time.Millisecond * 500) // 防止请求过快

			select {
			case b.indexChan <- indexReq{
				channelId:   resp.ChannelId,
				channelType: uint8(resp.ChannelType),
			}:
			default:
			}
		}
	}

}

func (b *bucket) buildIndex(channelId string, channelType uint8, msgs []*pluginproto.Message) error {

	batch := b.s.msgIndex.NewBatch()
	for _, msg := range msgs {
		if gjson.ValidBytes(msg.Payload) {

			m := newMessageFrom(msg)
			err := batch.Index(fmt.Sprintf("%d", msg.MessageId), m)
			if err != nil {
				b.Error("index message error", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int64("messageId", msg.MessageId), zap.Uint64("messageSeq", msg.MessageSeq))
			}
		}
	}
	return b.s.msgIndex.Batch(batch)
}

type indexReq struct {
	channelId   string
	channelType uint8
}
