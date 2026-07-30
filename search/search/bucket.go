package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/WuKongIM/wklog"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

var errChannelIndexIterationLimit = errors.New("channel indexing reached iteration limit")

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
	// panic 恢复，防止单次处理失败导致整个 goroutine 退出
	defer func() {
		if r := recover(); r != nil {
			b.Error("handleIndex panic recovered", zap.Any("panic", r), zap.Int("indexCount", len(indexs)))
		}
	}()

	// 去重
	uniqueReqs := make(map[string]indexReq)
	for _, req := range indexs {
		key := fmt.Sprintf("%s:%d", req.channelId, req.channelType)
		uniqueReqs[key] = req
	}

	// 对每个频道单独处理，避免一个频道的问题影响其他频道
	for _, indexReq := range uniqueReqs {
		_ = b.processChannelIndex(indexReq.channelId, indexReq.channelType)
	}
}

// processChannelIndex 处理单个频道的索引，内部循环直到索引完成
func (b *bucket) processChannelIndex(channelId string, channelType uint8) error {
	const maxIterations = 100 // 防止无限循环
	const limit = 500

	for i := 0; i < maxIterations; i++ {
		msgSeq, err := b.s.db.getChannelMaxMessageSeq(channelId, channelType)
		if err != nil {
			b.Error("getChannelMaxMessageSeq error", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return err
		}

		req := &pluginproto.ChannelMessageBatchReq{
			ChannelMessageReqs: []*pluginproto.ChannelMessageReq{
				{
					ChannelId:       channelId,
					ChannelType:     uint32(channelType),
					StartMessageSeq: msgSeq + 1,
					Limit:           limit,
				},
			},
		}

		// 使用带超时的 context 包装 RPC 调用
		messageResp, err := b.fetchMessagesWithTimeout(req, 30*time.Second)
		if err != nil {
			b.Error("get channel message error", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return err
		}

		if messageResp == nil || len(messageResp.ChannelMessageResps) == 0 {
			b.Info("channel message is empty, indexing complete", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return nil
		}

		resp := messageResp.ChannelMessageResps[0]
		if len(resp.Messages) == 0 {
			b.Info("no new messages, indexing complete", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return nil
		}

		lastMsg := resp.Messages[len(resp.Messages)-1]
		if lastMsg.MessageSeq <= msgSeq {
			return fmt.Errorf("channel message sequence did not advance: channel_id=%s channel_type=%d current_seq=%d last_seq=%d", channelId, channelType, msgSeq, lastMsg.MessageSeq)
		}

		// 索引消息
		err = b.buildIndex(resp.ChannelId, uint8(resp.ChannelType), resp.Messages)
		if err != nil {
			b.Error("search index error", zap.Error(err), zap.String("channelId", resp.ChannelId), zap.Uint32("channelType", resp.ChannelType))
			return err
		}

		err = b.s.db.setChannelMaxMessageSeq(resp.ChannelId, uint8(resp.ChannelType), lastMsg.MessageSeq)
		if err != nil {
			b.Error("set channel max message seq error", zap.Error(err), zap.String("channelId", resp.ChannelId), zap.Uint32("channelType", resp.ChannelType), zap.Uint64("messageSeq", lastMsg.MessageSeq))
			return err
		}

		// 如果消息数量小于 limit，说明已经索引完成
		if len(resp.Messages) < limit {
			b.Info("channel indexing complete", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("iteration", i+1))
			return nil
		}

		// 还有更多消息，短暂休眠后继续
		time.Sleep(time.Millisecond * 100) // 减少到 100ms，提高效率
	}

	b.Warn("channel indexing reached max iterations", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("maxIterations", maxIterations))
	return fmt.Errorf("%w: channel_id=%s channel_type=%d", errChannelIndexIterationLimit, channelId, channelType)
}

// fetchMessagesWithTimeout 带超时的消息获取
func (b *bucket) fetchMessagesWithTimeout(req *pluginproto.ChannelMessageBatchReq, timeout time.Duration) (*pluginproto.ChannelMessageBatchResp, error) {
	type rpcResult struct {
		resp *pluginproto.ChannelMessageBatchResp
		err  error
	}
	resultChan := make(chan rpcResult, 1)

	go func() {
		resp, err := pdk.S.GetChannelMessages(req)
		resultChan <- rpcResult{resp: resp, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case result := <-resultChan:
		return result.resp, result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("get channel message timeout after %v", timeout)
	}
}

func (b *bucket) buildIndex(channelId string, channelType uint8, msgs []*pluginproto.Message) error {
	if b.s.msgIndex == nil {
		b.Error("search: msg index is nil", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return nil
	}
	b.Info("buildIndex: indexing messages", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("messageCount", len(msgs)))
	batch := b.s.msgIndex.NewBatch()
	indexedCount := 0
	for _, msg := range msgs {
		if gjson.ValidBytes(msg.Payload) {
			m := newMessageFrom(msg)
			b.Debug("buildIndex: indexing message", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int64("messageId", msg.MessageId), zap.Uint64("messageSeq", msg.MessageSeq), zap.String("payload", string(msg.Payload)))
			err := batch.Index(fmt.Sprintf("%d", msg.MessageId), m)
			if err != nil {
				b.Error("index message error", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int64("messageId", msg.MessageId), zap.Uint64("messageSeq", msg.MessageSeq))
			} else {
				indexedCount++
			}
		} else {
			b.Warn("buildIndex: skip non-json message", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int64("messageId", msg.MessageId), zap.Uint64("messageSeq", msg.MessageSeq))
		}
	}

	if indexedCount == 0 {
		b.Warn("buildIndex: no valid messages to index", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("totalMsgs", len(msgs)))
		return nil
	}

	// 使用带超时的方式执行 Batch 操作
	type batchResult struct {
		err error
	}
	resultChan := make(chan batchResult, 1)

	go func() {
		err := b.s.msgIndex.Batch(batch)
		resultChan <- batchResult{err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	select {
	case result := <-resultChan:
		if result.err != nil {
			b.Error("buildIndex: batch index error", zap.Error(result.err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return result.err
		}
		b.Info("buildIndex: batch index success", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("indexedCount", indexedCount))
		return nil
	case <-ctx.Done():
		b.Error("buildIndex: batch index timeout", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Duration("timeout", 60*time.Second))
		return fmt.Errorf("batch index timeout")
	}
}

type indexReq struct {
	channelId   string
	channelType uint8
}
