package search

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/WuKongIM/wklog"
	"github.com/blevesearch/bleve/v2"
	"github.com/cockroachdb/pebble"
)

func refreshFixture(t *testing.T, fetch func(*pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error)) *Search {
	t.Helper()
	disk, err := pebble.Open(t.TempDir(), &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		t.Fatal(err)
	}
	s := &Search{db: newDb(), msgIndex: idx, Log: wklog.NewWKLog("refresh-test"), fetchMessages: fetch, ready: make(chan struct{})}
	s.db.pebbleDb = disk
	close(s.ready)
	s.buckets = []*bucket{newBucket(0, s)}
	done := make(chan struct{})
	go func() { s.buckets[0].loopIndex(); close(done) }()
	t.Cleanup(func() { close(s.buckets[0].indexChan); <-done; idx.Close(); disk.Close() })
	return s
}

// The replica has an old index but no PersistAfter notification for the new
// committed message. A query must catch up before returning a complete result.
func TestRefreshChannelsRecoversMissedCommit(t *testing.T) {
	calls := 0
	s := refreshFixture(t, func(req *pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		calls++
		if req.ChannelMessageReqs[0].StartMessageSeq != 4 {
			t.Errorf("start=%d, want 4", req.ChannelMessageReqs[0].StartMessageSeq)
		}
		return &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "group", ChannelType: 2, Messages: []*pluginproto.Message{{MessageId: 4, MessageSeq: 4, ChannelId: "group", ChannelType: 2, Payload: []byte(`{"type":1,"content":"needle"}`)}}}}}, nil
	})
	var old []*pluginproto.Message
	for i := int64(1); i <= 3; i++ {
		old = append(old, &pluginproto.Message{MessageId: i, MessageSeq: uint64(i), ChannelId: "group", ChannelType: 2, Payload: []byte(`{"type":1,"content":"needle"}`)})
	}
	if err := s.buckets[0].buildIndex("group", 2, old); err != nil {
		t.Fatal(err)
	}
	if err := s.db.setChannelMaxMessageSeq("group", 2, 3); err != nil {
		t.Fatal(err)
	}
	channels := []*pluginproto.Channel{{ChannelId: "group", ChannelType: 2}, {ChannelId: "group", ChannelType: 2}}
	if err := s.RefreshChannels(context.Background(), channels); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("duplicate channel fetched %d times", calls)
	}
	resp, err := s.Search(SearchReq{Channels: channels, Payload: map[string]string{"content": "needle"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 4 {
		t.Fatalf("search returned %d messages, want 4", len(resp.Messages))
	}
	seq, err := s.db.getChannelMaxMessageSeq("group", 2)
	if err != nil || seq != 4 {
		t.Fatalf("checkpoint=%d err=%v", seq, err)
	}
}

func TestRefreshChannelsPropagatesFailureAndCancellation(t *testing.T) {
	failure := errors.New("history unavailable")
	s := refreshFixture(t, func(*pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		return nil, failure
	})
	channels := []*pluginproto.Channel{{ChannelId: "group", ChannelType: 2}}
	if err := s.RefreshChannels(context.Background(), channels); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
	seq, err := s.db.getChannelMaxMessageSeq("group", 2)
	if err != nil || seq != 0 {
		t.Fatalf("checkpoint advanced after failure: %d %v", seq, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.RefreshChannels(ctx, channels); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestIndexBatchSignalsEveryCoalescedWaiter(t *testing.T) {
	failure := errors.New("history unavailable")
	calls := 0
	s := refreshFixture(t, func(*pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		calls++
		return nil, failure
	})
	done := make(chan error, 2)
	s.buckets[0].handleIndex([]indexReq{
		{channelId: "group", channelType: 2, done: done},
		{channelId: "group", channelType: 2, done: done},
	})
	if calls != 1 || len(done) != 2 {
		t.Fatalf("fetches=%d notifications=%d, want one fetch and two notifications", calls, len(done))
	}
	for range 2 {
		if err := <-done; !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
}

func TestRefreshChannelsRejectsFullQueueAndOversizedScope(t *testing.T) {
	s := &Search{ready: make(chan struct{})}
	close(s.ready)
	s.buckets = []*bucket{newBucket(0, s)}
	for range cap(s.buckets[0].indexChan) {
		s.buckets[0].indexChan <- indexReq{channelId: "existing", channelType: 2}
	}
	channels := []*pluginproto.Channel{{ChannelId: "group", ChannelType: 2}}
	if err := s.RefreshChannels(context.Background(), channels); err == nil {
		t.Fatal("full indexing queue accepted a query")
	}
	if err := s.RefreshChannels(context.Background(), make([]*pluginproto.Channel, 1001)); err == nil {
		t.Fatal("oversized scope accepted")
	}
}

// A full batch must leave the next queued request for the next iteration.
func TestIndexQueueDoesNotDropWaitersAtBatchBoundary(t *testing.T) {
	disk, err := pebble.Open(t.TempDir(), &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	failure := errors.New("history unavailable")
	s := &Search{db: newDb(), fetchMessages: func(*pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		return nil, failure
	}}
	s.db.pebbleDb = disk
	b := newBucket(0, s)
	const count = 205
	done := make(chan error, count)
	for i := range count {
		b.indexChan <- indexReq{channelId: fmt.Sprintf("group-%d", i), channelType: 2, done: done}
	}
	close(b.indexChan)
	b.loopIndex()
	if len(done) != count {
		t.Fatalf("notifications=%d, want %d", len(done), count)
	}
	for range count {
		if err := <-done; !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
}
