package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"golang.org/x/sync/errgroup"
)

// channelsNeedingRefresh verifies persisted index checkpoints against committed
// history. Current channels need no queue slot behind unrelated cold rebuilds.
// Only a complete authoritative empty page proves a channel current.
func (s *Search) channelsNeedingRefresh(ctx context.Context, channels map[Channel]struct{}) (map[Channel]struct{}, error) {
	const batchSize = 16
	requests := make([]*pluginproto.ChannelMessageReq, 0, len(channels))
	for channel := range channels {
		seq, err := s.db.getChannelMaxMessageSeq(channel.ChannelId, channel.ChannelType)
		if err != nil {
			return nil, err
		}
		if seq == math.MaxUint64 {
			return nil, errors.New("search index checkpoint overflow")
		}
		requests = append(requests, &pluginproto.ChannelMessageReq{ChannelId: channel.ChannelId, ChannelType: uint32(channel.ChannelType), StartMessageSeq: seq + 1, Limit: 1})
	}
	pending := make(map[Channel]struct{})
	var mu sync.Mutex
	group, readCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for start := 0; start < len(requests); start += batchSize {
		batch := requests[start:min(start+batchSize, len(requests))]
		group.Go(func() error {
			response, err := s.fetchRefreshCheck(readCtx, &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: batch})
			if err != nil {
				return err
			}
			if response == nil || len(response.ChannelMessageResps) != len(batch) {
				return errors.New("incomplete search freshness response")
			}
			for i, page := range response.ChannelMessageResps {
				expected := batch[i]
				if page == nil || page.ChannelId != expected.ChannelId || page.ChannelType != expected.ChannelType {
					return errors.New("misaligned search freshness response")
				}
				if len(page.Messages) > 0 {
					if page.Messages[0] == nil || page.Messages[0].MessageSeq < expected.StartMessageSeq {
						return fmt.Errorf("invalid search freshness sequence for channel %s", expected.ChannelId)
					}
					mu.Lock()
					pending[Channel{ChannelId: expected.ChannelId, ChannelType: uint8(expected.ChannelType)}] = struct{}{}
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return pending, nil
}

// fetchRefreshCheck keeps the PDK's non-context RPC bounded even when a caller
// stops waiting. Its slot is released only when the actual RPC returns.
func (s *Search) fetchRefreshCheck(ctx context.Context, req *pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.refreshOnce.Do(func() { s.refreshSlots = make(chan struct{}, 16) })
	select {
	case s.refreshSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	type result struct {
		response *pluginproto.ChannelMessageBatchResp
		err      error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-s.refreshSlots }()
		fetch := s.fetchMessages
		if fetch == nil {
			fetch = pdk.S.GetChannelMessages
		}
		response, err := fetch(req)
		done <- result{response, err}
	}()
	select {
	case r := <-done:
		return r.response, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
