package search

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/tidwall/gjson"
	gse "github.com/vcaesar/gse-bleve"
)

type Search struct {
	buckets  []*bucket
	db       *db
	msgIndex bleve.Index
}

func New() *Search {
	s := &Search{
		buckets: make([]*bucket, 10),
		db:      newDb(),
	}

	for i := 0; i < len(s.buckets); i++ {
		s.buckets[i] = newBucket(i, s)
		s.buckets[i].start()
	}

	return s
}

// 索引频道的消息
func (s *Search) MakeIndex(channelId string, channelType uint8) {
	bucketIndex := s.bucketIndex(channelId)
	bucket := s.buckets[bucketIndex]
	bucket.indexChan <- indexReq{
		channelId:   channelId,
		channelType: channelType,
	}
}

func (s *Search) buildMessageMapping(indexName string) *mapping.IndexMappingImpl {
	opt := gse.Option{
		Index: indexName,
		// Dicts: "embed, ja",
		Dicts: "embed, zh",
		Opt:   "search-hmm",
		Trim:  "trim",
		Stop:  "",
	}
	var err error
	indexMapping, err := gse.NewMapping(opt)
	if err != nil {
		panic(err)
	}

	// 创建一个文档映射
	docMapping := bleve.NewDocumentMapping()
	indexMapping.DefaultMapping = docMapping

	// 添加字段映射
	fromFieldMapping := bleve.NewTextFieldMapping()
	fromFieldMapping.Analyzer = "keyword" // keyword 表示不分词
	docMapping.AddFieldMappingsAt("from_uid", fromFieldMapping)

	// channelId
	channelIdFieldMapping := bleve.NewKeywordFieldMapping()
	channelIdFieldMapping.Analyzer = "keyword" // 关键配置：禁用分词
	docMapping.AddFieldMappingsAt("channel_id", channelIdFieldMapping)

	// channelType
	channelTypeFieldMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("channel_type", channelTypeFieldMapping)

	// messageSeq
	messageSeqFieldMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("message_seq", messageSeqFieldMapping)

	// timestamp
	timestampFieldMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("timestamp", timestampFieldMapping)

	// topic
	topicFieldMapping := bleve.NewTextFieldMapping()
	docMapping.AddFieldMappingsAt("topic", topicFieldMapping)

	// payload
	payloadFieldMapping := gse.NewDoc()

	// payload.content
	contentFieldMapping := gse.NewTextMap()
	contentFieldMapping.IncludeTermVectors = true
	payloadFieldMapping.AddFieldMappingsAt("content", contentFieldMapping)

	// payload.type
	typeFieldMapping := bleve.NewNumericFieldMapping()
	payloadFieldMapping.AddFieldMappingsAt("type", typeFieldMapping)

	docMapping.AddSubDocumentMapping("payload", payloadFieldMapping)

	return indexMapping

}

func (s *Search) Search(req SearchReq) (*SearchResp, error) {

	query := bleve.NewConjunctionQuery()
	if strings.TrimSpace(req.FromUid) != "" {
		termQuery := bleve.NewTermQuery(req.FromUid)
		termQuery.SetField("from_uid")
		query.AddQuery(termQuery)
	}

	if len(req.Channels) > 0 {

		orQuery := bleve.NewDisjunctionQuery()
		for _, channel := range req.Channels {

			channelQuery := bleve.NewConjunctionQuery()

			termQuery := bleve.NewTermQuery(channel.ChannelId)
			termQuery.SetField("channel_id")
			channelQuery.AddQuery(termQuery)

			if channel.ChannelType != 0 {
				ftype := float64(channel.ChannelType)
				start := ftype
				end := ftype + 1
				termQuery := bleve.NewNumericRangeQuery(&start, &end)
				termQuery.SetField("channel_type")
				channelQuery.AddQuery(termQuery)
			}
			orQuery.AddQuery(channelQuery)
		}
		query.AddQuery(orQuery)

	}

	if strings.TrimSpace(req.ChannelId) != "" {
		termQuery := bleve.NewTermQuery(req.ChannelId)
		termQuery.SetField("channel_id")
		query.AddQuery(termQuery)

		if req.ChannelType != 0 {
			ftype := float64(req.ChannelType)
			start := ftype
			end := ftype + 1
			termQuery := bleve.NewNumericRangeQuery(&start, &end)
			termQuery.SetField("channel_type")
			query.AddQuery(termQuery)
		}
	}

	if len(req.Payload) > 0 {
		// or条件查询
		payloadQuery := bleve.NewDisjunctionQuery()

		exist := false
		for k, v := range req.Payload {
			if strings.TrimSpace(v) == "" {
				continue
			}
			exist = true
			bleveQuery := bleve.NewMatchQuery(v)
			bleveQuery.SetField(fmt.Sprintf("payload.%s", k))
			payloadQuery.AddQuery(bleveQuery)
		}
		if exist {
			query.AddQuery(payloadQuery)
		}
	}

	if strings.TrimSpace(req.Topic) != "" {
		termQuery := bleve.NewTermQuery(req.Topic)
		termQuery.SetField("topic")
		query.AddQuery(termQuery)
	}

	// 消息类型
	if len(req.PayloadTypes) > 0 {
		orQuery := bleve.NewDisjunctionQuery()
		for _, t := range req.PayloadTypes {
			ftype := float64(t)
			start := ftype
			end := ftype + 1
			termQuery := bleve.NewNumericRangeQuery(&start, &end)
			termQuery.SetField("payload.type")
			orQuery.AddQuery(termQuery)
		}
		query.AddQuery(orQuery)
	}

	// 时间范围
	if req.StartTime > 0 || req.EndTime > 0 {
		var start *float64
		var end *float64

		if req.StartTime > 0 {
			startTime := float64(req.StartTime)
			start = &startTime
		}

		if req.EndTime > 0 {
			endTime := float64(req.EndTime + 1) // TODO: 这里+1 是为了包含结束时间
			end = &endTime
		}

		termQuery := bleve.NewNumericRangeQuery(start, end)
		termQuery.SetField("timestamp")
		query.AddQuery(termQuery)
	}

	// 构建请求
	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Fields = []string{"*"}

	if len(req.Highlights) > 0 {
		searchRequest.Highlight = bleve.NewHighlight()
		for _, field := range req.Highlights {
			searchRequest.Highlight.AddField(field)
		}
	}

	from := 0

	if req.Page > 0 {
		from = (req.Page - 1) * req.Limit
	}
	searchRequest.From = from
	searchRequest.Size = req.Limit
	searchRequest.SortBy([]string{"-_score", "-timestamp"}) // 先按照相关度排序，再按照时间排序

	searchResult, err := s.msgIndex.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	resultMsgs := make([]*Message, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {

		msgId, _ := strconv.ParseInt(hit.ID, 10, 64)
		var (
			messageSeq  uint64
			clientMsgNo string
			fromUid     string
			channelId   string
			channelType uint8
			streamNo    string
			streamId    uint64
			payload     interface{}
			topic       string
			timestamp   uint32
		)

		// messageSeq
		messageSeqObj := hit.Fields["message_seq"]
		if messageSeqObj != nil {
			messageSeq = uint64(messageSeqObj.(float64))
		}

		// clientMsgNo
		clientMsgNoObj := hit.Fields["client_msg_no"]
		if clientMsgNoObj != nil {
			clientMsgNo = clientMsgNoObj.(string)
		}

		// fromUid
		fromUidObj := hit.Fields["from_uid"]
		if fromUidObj != nil {
			fromUid = fromUidObj.(string)
		}

		// channelId
		channelIdObj := hit.Fields["channel_id"]
		if channelIdObj != nil {
			channelId = channelIdObj.(string)
		}

		// channelType
		channelTypeObj := hit.Fields["channel_type"]
		if channelTypeObj != nil {
			channelType = uint8(channelTypeObj.(float64))
		}

		// streamNo
		streamNoObj := hit.Fields["stream_no"]
		if streamNoObj != nil {
			streamNo = streamNoObj.(string)
		}

		// streamId
		streamSeqObj := hit.Fields["stream_id"]
		if streamSeqObj != nil {
			streamId = uint64(streamSeqObj.(float64))
		}

		// payload
		var payloadMap map[string]interface{}
		for fieldKey, fieldValue := range hit.Fields {
			if fieldValue == nil {
				continue
			}
			if strings.HasPrefix(fieldKey, "payload.") {
				if payloadMap == nil {
					payloadMap = make(map[string]interface{})
				}
				key := strings.TrimPrefix(fieldKey, "payload.")
				payloadMap[key] = fieldValue
			}

		}

		if len(payloadMap) > 0 {
			payload = payloadMap

			// 如果有高亮内容，替换payload原来的值
			if len(hit.Fragments) > 0 {
				for k, v := range hit.Fragments {
					if strings.HasPrefix(k, "payload.") {
						key := strings.TrimPrefix(k, "payload.")
						payloadMap[key] = strings.Join(v, "")
					}
				}
			}
		}

		// topic
		topicObj := hit.Fields["topic"]
		if topicObj != nil {
			topic = topicObj.(string)
		}

		// timestamp
		timestampObj := hit.Fields["timestamp"]
		if timestampObj != nil {
			timestamp = uint32(timestampObj.(float64))
		}

		var payloadBytes []byte

		if payload != nil {
			payloadBytes, _ = json.Marshal(payload)
		}

		msg := &Message{
			MessageId:    msgId,
			MessageIdStr: hit.ID,
			MessageSeq:   messageSeq,
			ClientMsgNo:  clientMsgNo,
			FromUid:      fromUid,
			ChannelId:    channelId,
			ChannelType:  channelType,
			StreamNo:     streamNo,
			StreamId:     streamId,
			Payload:      payloadBytes,
			PayloadJson:  payload,
			Topic:        topic,
			Timestamp:    timestamp,
		}

		resultMsgs = append(resultMsgs, msg)
	}
	return &SearchResp{
		Cost:     searchResult.Cost,
		Total:    searchResult.Total,
		Limit:    req.Limit,
		Page:     req.Page,
		Messages: resultMsgs,
	}, nil
}

func (s *Search) Start() {
	var err error

	err = s.db.open()
	if err != nil {
		panic(err)
	}

	bleveDir := path.Join(pdk.S.SandboxDir(), "message.bleve")
	s.msgIndex, err = bleve.Open(bleveDir)
	if err != nil {
		if err == bleve.ErrorIndexPathDoesNotExist {

			s.msgIndex, err = bleve.New(bleveDir, s.buildMessageMapping("message.bleve"))
			if err != nil {
				panic(err)
			}
		}
	}
}

func (s *Search) Stop() {
	s.db.close()
}

func (s Search) bucketIndex(channelId string) int {
	return int(Hash(channelId) % uint32(len(s.buckets)))
}

type SearchReq struct {
	Channels     []*pluginproto.Channel `json:"channels"`      // 频道 (查询内容限制在这些频道内)
	ChannelId    string                 `json:"channel_id"`    // 频道ID，如果指定了频道ID，则查询此频道内的消息
	ChannelType  uint8                  `json:"channel_type"`  // 频道类型
	FromUid      string                 `json:"from_uid"`      // 发送者
	Payload      map[string]string      `json:"payload"`       // 消息内容
	PayloadTypes []int                  `json:"payload_types"` // 消息类型集合
	Page         int                    `json:"page"`          // 页码，默认为1
	Limit        int                    `json:"limit"`         // 消息数量限制
	Topic        string                 `json:"topic"`         // 消息主题
	StartTime    uint64                 `json:"start_time"`    // 开始时间
	EndTime      uint64                 `json:"end_time"`      // 结束时间(结果包含此时间)
	Highlights   []string               `json:"highlights"`    // 高亮字段
}

func (s SearchReq) Clone() SearchReq {
	req := s
	req.Channels = make([]*pluginproto.Channel, len(s.Channels))
	copy(req.Channels, s.Channels)
	return req
}

type SearchResp struct {
	Cost     uint64     `json:"cost"`     // 耗时
	Total    uint64     `json:"total"`    // 总数
	Limit    int        `json:"limit"`    // 消息数量限制
	Page     int        `json:"page"`     // 页码
	Messages []*Message `json:"messages"` // 消息列表
}

type Channel struct {
	ChannelId   string
	ChannelType uint8
}

type Message struct {
	MessageId    int64   `json:"message_id,omitempty"`    // 消息ID
	MessageIdStr string  `json:"message_idstr,omitempty"` // 消息ID字符串
	MessageSeq   uint64  `json:"message_seq,omitempty"`   // 消息序号
	ClientMsgNo  string  `json:"client_msg_no,omitempty"` // 客户端消息编号
	FromUid      string  `json:"from_uid,omitempty"`      // 发送者
	ChannelId    string  `json:"channel_id,omitempty"`    // 频道ID
	ChannelType  uint8   `json:"channel_type,omitempty"`  // 频道类型
	Payload      Payload `json:"payload,omitempty"`       // 消息内容
	PayloadJson  Payload `json:"payload_json,omitempty"`  // 消息内容 json形式
	StreamNo     string  `json:"stream_no,omitempty"`     // 流编号
	StreamId     uint64  `json:"stream_id,omitempty"`     // 流id
	Topic        string  `json:"topic,omitempty"`         // 消息主题
	Timestamp    uint32  `json:"timestamp,omitempty"`     // 时间戳
}

func newMessageFrom(m *pluginproto.Message) *Message {

	jsonObj := gjson.ParseBytes(m.Payload).Value()

	return &Message{
		MessageId:    int64(m.MessageId),
		MessageIdStr: fmt.Sprintf("%d", m.MessageId),
		MessageSeq:   m.MessageSeq,
		ClientMsgNo:  m.ClientMsgNo,
		FromUid:      m.From,
		ChannelId:    m.ChannelId,
		ChannelType:  uint8(m.ChannelType),
		Payload:      jsonObj,
		StreamNo:     m.StreamNo,
		StreamId:     m.StreamId,
		Topic:        m.Topic,
		Timestamp:    m.Timestamp,
	}
}

type Payload interface{}
