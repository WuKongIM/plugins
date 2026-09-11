package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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
	if calls != 2 {
		t.Fatalf("channel fetched %d times, want one freshness check and one index fetch", calls)
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
	disk, err := pebble.Open(t.TempDir(), &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	s := &Search{ready: make(chan struct{}), db: newDb(), fetchMessages: func(req *pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		c := req.ChannelMessageReqs[0]
		return &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: c.ChannelId, ChannelType: c.ChannelType, Messages: []*pluginproto.Message{{MessageSeq: 1}}}}}, nil
	}}
	s.db.pebbleDb = disk
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

// A queued cold channel must not delay an already-current channel in the same bucket.
func TestRefreshCurrentChannelBypassesBusyIndexWorker(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := refreshFixture(t, func(req *pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		c := req.ChannelMessageReqs[0]
		if c.ChannelId == "cold" {
			close(started)
			<-release
		}
		return &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: c.ChannelId, ChannelType: c.ChannelType}}}, nil
	})
	defer close(release)
	s.buckets[0].indexChan <- indexReq{channelId: "cold", channelType: 2}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.RefreshChannels(ctx, []*pluginproto.Channel{{ChannelId: "current", ChannelType: 2}}); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshCheckRejectsIncompleteAuthorityEvidence(t *testing.T) {
	for _, response := range []*pluginproto.ChannelMessageBatchResp{
		nil, {},
		{ChannelMessageResps: []*pluginproto.ChannelMessageResp{nil}},
		{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "wrong", ChannelType: 2}}},
		{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "group", ChannelType: 2, Messages: []*pluginproto.Message{{MessageSeq: 0}}}}},
	} {
		s := refreshFixture(t, func(*pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
			return response, nil
		})
		if err := s.RefreshChannels(context.Background(), []*pluginproto.Channel{{ChannelId: "group", ChannelType: 2}}); err == nil {
			t.Fatal("incomplete evidence accepted")
		}
	}
}

func TestRefreshChecksUseBoundedBatchesAndCurrentCheckpoints(t *testing.T) {
	var mu sync.Mutex
	calls, checked := 0, 0
	s := refreshFixture(t, func(req *pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		checked += len(req.ChannelMessageReqs)
		if len(req.ChannelMessageReqs) > 16 {
			t.Errorf("unbounded request: %d", len(req.ChannelMessageReqs))
		}
		out := &pluginproto.ChannelMessageBatchResp{}
		for _, c := range req.ChannelMessageReqs {
			if c.StartMessageSeq != 8 || c.Limit != 1 {
				t.Errorf("wrong freshness boundary: %v", c)
			}
			out.ChannelMessageResps = append(out.ChannelMessageResps, &pluginproto.ChannelMessageResp{ChannelId: c.ChannelId, ChannelType: c.ChannelType})
		}
		return out, nil
	})
	channels := make([]*pluginproto.Channel, 0, 65)
	for i := range 65 {
		id := fmt.Sprintf("channel-%d", i)
		if err := s.db.setChannelMaxMessageSeq(id, 2, 7); err != nil {
			t.Fatal(err)
		}
		channels = append(channels, &pluginproto.Channel{ChannelId: id, ChannelType: 2})
	}
	if err := s.RefreshChannels(context.Background(), channels); err != nil {
		t.Fatal(err)
	}
	if checked != 65 || calls != 5 {
		t.Fatalf("checked=%d calls=%d", checked, calls)
	}
}

func TestRefreshCanceledReadsKeepTheirConcurrencySlotsUntilFinished(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 16)
	finished := make(chan struct{}, 16)
	s := &Search{fetchMessages: func(*pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
		entered <- struct{}{}
		<-release
		finished <- struct{}{}
		return &pluginproto.ChannelMessageBatchResp{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	var callers sync.WaitGroup
	for range 20 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if _, err := s.fetchRefreshCheck(ctx, &pluginproto.ChannelMessageBatchReq{}); !errors.Is(err, context.Canceled) {
				t.Errorf("got %v", err)
			}
		}()
	}
	for range 16 {
		<-entered
	}
	cancel()
	callers.Wait()
	if len(s.refreshSlots) != 16 {
		t.Fatalf("in-flight RPC slots=%d, want 16", len(s.refreshSlots))
	}
	close(release)
	for range 16 {
		<-finished
	}
}

func TestRefreshRejectsIncompleteIndexResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *pluginproto.ChannelMessageBatchResp
	}{
		{"nil", nil},
		{"missing", &pluginproto.ChannelMessageBatchResp{}},
		{"nil page", &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{nil}}},
		{"wrong channel", &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "other", ChannelType: 2}}}},
		{"wrong type", &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "group", ChannelType: 1}}}},
		{"nil message", &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "group", ChannelType: 2, Messages: []*pluginproto.Message{nil}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := refreshFixture(t, func(req *pluginproto.ChannelMessageBatchReq) (*pluginproto.ChannelMessageBatchResp, error) {
				calls++
				if calls == 1 {
					return &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{ChannelId: "group", ChannelType: 2, Messages: []*pluginproto.Message{{MessageSeq: 1}}}}}, nil
				}
				return tc.response, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.RefreshChannels(ctx, []*pluginproto.Channel{{ChannelId: "group", ChannelType: 2}}); err == nil {
				t.Fatal("pending history was declared indexed without aligned evidence")
			} else if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("malformed response stranded its completion waiter")
			}
			seq, err := s.db.getChannelMaxMessageSeq("group", 2)
			if err != nil || seq != 0 {
				t.Fatalf("checkpoint advanced:%d %v", seq, err)
			}
		})
	}
}
