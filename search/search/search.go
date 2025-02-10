package search

import (
	"fmt"

	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

type Search struct {
	buckets  []*bucket
	db       *db
	msgIndex bleve.Index
}

func New() *Search {
	fmt.Println("New---->")
	s := &Search{
		buckets: make([]*bucket, 10),
		db:      newDb(),
	}

	for i := 0; i < len(s.buckets); i++ {
		s.buckets[i] = newBucket(i, s)
		s.buckets[i].start()
	}
	var err error

	s.msgIndex, err = bleve.Open("message.bleve")
	if err != nil {
		if err == bleve.ErrorIndexPathDoesNotExist {

			s.msgIndex, err = bleve.New("message.bleve", s.buildMessageMapping())
			if err != nil {
				panic(err)
			}
		}
	}

	return s
}

func (s *Search) buildMessageMapping() *mapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()

	// 创建一个文档映射
	docMapping := bleve.NewDocumentMapping()
	indexMapping.AddDocumentMapping("message", docMapping)

	// 添加字段映射
	fromFieldMapping := bleve.NewTextFieldMapping()
	fromFieldMapping.Analyzer = "en" // 使用英文分词器
	docMapping.AddFieldMappingsAt("from", fromFieldMapping)

	return indexMapping

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

func (s *Search) Search(req SearchReq) (*SearchResp, error) {

	query := bleve.NewTermQuery(req.From)
	query.SetField("from")

	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Fields = []string{"from", "channelId", "channelType"}
	searchResult, err := s.msgIndex.Search(searchRequest)
	if err != nil {
		return nil, err
	}
	// 输出查询结果
	fmt.Printf("查询结果: %d 条匹配\n", searchResult.Total)
	for _, hit := range searchResult.Hits {
		fmt.Printf("ID: %s, 分数: %f\n fields:%s", hit.ID, hit.Score, hit.Fields)
	}
	return nil, nil
}

func (s *Search) Stop() {
	s.db.close()
}

func (s Search) bucketIndex(channelId string) int {
	return int(Hash(channelId) % uint32(len(s.buckets)))
}

type SearchReq struct {
	ChannelId   string
	ChannelType uint8
	From        string
}

type SearchResp struct {
	Messages []*pluginproto.Message
}
