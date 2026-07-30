package search

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/blevesearch/bleve/v2"
)

func TestSearchRelevance(t *testing.T) {
	// 1. 创建临时目录
	tmpDir, err := os.MkdirTemp("", "search_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &Search{
		buckets: make([]*bucket, 0),
	}

	// 2. 创建索引
	indexDir := path.Join(tmpDir, "test.bleve")
	indexMapping := s.buildMessageMapping("test.bleve")
	index, err := bleve.New(indexDir, indexMapping)
	if err != nil {
		t.Fatal(err)
	}
	s.msgIndex = index
	defer index.Close()

	// 3. 准备测试数据
	// 消息1: 包含一个 "apple"，但时间戳更高
	msg1 := &pluginproto.Message{
		MessageId:   1,
		MessageSeq:  1,
		ChannelId:   "ch1",
		ChannelType: 1,
		Payload:     []byte(`{"content": "apple pie", "type": 1}`),
		Timestamp:   200, // 更晚的时间
	}
	// 消息2: 包含两个 "apple"，相关性更高，但时间戳更低
	msg2 := &pluginproto.Message{
		MessageId:   2,
		MessageSeq:  2,
		ChannelId:   "ch1",
		ChannelType: 1,
		Payload:     []byte(`{"content": "apple apple pie", "type": 1}`),
		Timestamp:   100, // 更早的时间
	}
	// 消息3: 不包含 "apple"
	msg3 := &pluginproto.Message{
		MessageId:   3,
		MessageSeq:  3,
		ChannelId:   "ch1",
		ChannelType: 1,
		Payload:     []byte(`{"content": "banana bread", "type": 1}`),
		Timestamp:   120,
	}

	// 4. 建立索引
	msgs := []*pluginproto.Message{msg1, msg2, msg3}
	batch := index.NewBatch()
	for _, m := range msgs {
		doc := newMessageFrom(m)
		batch.Index(fmt.Sprintf("%d", m.MessageId), doc)
	}
	err = index.Batch(batch)
	if err != nil {
		t.Fatal(err)
	}

	// 5. 执行搜索
	req := SearchReq{
		Payload: map[string]string{
			"content": "apple",
		},
		Limit: 10,
	}
	resp, err := s.Search(req)
	if err != nil {
		t.Fatal(err)
	}

	// 6. 验证结果
	if len(resp.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(resp.Messages))
	}

	// 打印结果分数
	for i, m := range resp.Messages {
		t.Logf("Result %d: ID=%d, Score=%f, Timestamp=%d", i, m.MessageId, m.Score, m.Timestamp)
	}

	// 验证排序：msg2 应该在 msg1 前面，因为 msg2 的相关性（Score）更高
	if resp.Messages[0].MessageId != 2 {
		t.Errorf("expected first message to be ID 2 (higher relevance), got ID %d", resp.Messages[0].MessageId)
	}
	if resp.Messages[1].MessageId != 1 {
		t.Errorf("expected second message to be ID 1, got ID %d", resp.Messages[1].MessageId)
	}
}

func TestSearchLimitAndRelevance(t *testing.T) {
	// 1. 创建临时目录
	tmpDir, err := os.MkdirTemp("", "search_limit_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &Search{
		buckets: make([]*bucket, 0),
	}

	// 2. 创建索引
	indexDir := path.Join(tmpDir, "test_limit.bleve")
	indexMapping := s.buildMessageMapping("test_limit.bleve")
	s.msgIndex, err = bleve.New(indexDir, indexMapping)
	if err != nil {
		t.Fatal(err)
	}
	defer s.msgIndex.Close()

	// 3. 准备测试数据 (共 6 条匹配消息，limit 为 3)
	// 我们保持文档长度一致，但包含关键词的频率不同，以确保得分高低符合预期
	msgs := []*pluginproto.Message{
		{MessageId: 1, Payload: []byte(`{"content": "apple apple apple", "type": 1}`), Timestamp: 100},   // 3次关键词
		{MessageId: 2, Payload: []byte(`{"content": "apple apple orange", "type": 1}`), Timestamp: 101},  // 2次关键词
		{MessageId: 3, Payload: []byte(`{"content": "apple orange orange", "type": 1}`), Timestamp: 102}, // 1次关键词
		{MessageId: 4, Payload: []byte(`{"content": "apple banana banana", "type": 1}`), Timestamp: 103}, // 1次关键词
		{MessageId: 5, Payload: []byte(`{"content": "apple cherry cherry", "type": 1}`), Timestamp: 104}, // 1次关键词
		{MessageId: 6, Payload: []byte(`{"content": "apple grape grape", "type": 1}`), Timestamp: 105},   // 1次关键词
	}

	// 4. 建立索引
	batch := s.msgIndex.NewBatch()
	for _, m := range msgs {
		doc := newMessageFrom(m)
		batch.Index(fmt.Sprintf("%d", m.MessageId), doc)
	}
	err = s.msgIndex.Batch(batch)
	if err != nil {
		t.Fatal(err)
	}

	// 5. 执行搜索，limit 为 3
	req := SearchReq{
		Payload: map[string]string{
			"content": "apple",
		},
		Limit: 3,
	}
	resp, err := s.Search(req)
	if err != nil {
		t.Fatal(err)
	}

	// 6. 验证结果
	if len(resp.Messages) != 3 {
		t.Fatalf("expected 3 messages due to limit, got %d", len(resp.Messages))
	}

	if resp.Total != 6 {
		t.Errorf("expected total matches to be 6, got %d", resp.Total)
	}

	// 打印结果
	for i, m := range resp.Messages {
		t.Logf("Result %d: ID=%d, Score=%f, Timestamp=%d", i, m.MessageId, m.Score, m.Timestamp)
	}

	// 验证前两个必须是 msg1 和 msg2 (相关性最高的)
	if resp.Messages[0].MessageId != 1 {
		t.Errorf("expected first message to be ID 1 (highest relevance), got ID %d", resp.Messages[0].MessageId)
	}
	if resp.Messages[1].MessageId != 2 {
		t.Errorf("expected second message to be ID 2 (second highest), got ID %d", resp.Messages[1].MessageId)
	}

	// 第三个消息的相关性得分应该比前两个低，且在得分相同的情况下，时间戳最大的 msg6 应该排在前面
	if resp.Messages[2].MessageId != 6 {
		t.Errorf("expected third message to be ID 6 (same score as 3-5 but newer), got ID %d", resp.Messages[2].MessageId)
	}
}
