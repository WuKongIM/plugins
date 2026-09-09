package search

import (
	"context"
	"errors"
	"strings"

	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
)

// RefreshChannels catches up explicit query channels through the existing
// bounded indexing queues. Missing best-effort hooks or a different channel
// leader must not turn a stale local index into a successful incomplete search.
// Failure or a caller deadline returns an error; queued indexing can continue.
func (s *Search) RefreshChannels(ctx context.Context, channels []*pluginproto.Channel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}
	if len(channels) > 1000 {
		return errors.New("search refresh exceeds 1000 channels")
	}
	select {
	case <-s.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if len(s.buckets) == 0 {
		return errors.New("search indexing unavailable")
	}
	unique := make(map[Channel]struct{}, len(channels))
	for _, channel := range channels {
		if channel == nil || strings.TrimSpace(channel.ChannelId) == "" || channel.ChannelType == 0 || channel.ChannelType > 255 {
			return errors.New("invalid search refresh channel")
		}
		unique[Channel{ChannelId: channel.ChannelId, ChannelType: uint8(channel.ChannelType)}] = struct{}{}
	}
	done := make(chan error, len(unique))
	for channel := range unique {
		if err := ctx.Err(); err != nil {
			return err
		}
		b := s.buckets[s.bucketIndex(channel.ChannelId)]
		select {
		case b.indexChan <- indexReq{channelId: channel.ChannelId, channelType: channel.ChannelType, done: done}:
		default:
			return errors.New("search indexing queue is full; retry later")
		}
	}
	for range unique {
		select {
		case err := <-done:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
