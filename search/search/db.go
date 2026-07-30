package search

import (
	"encoding/binary"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/cockroachdb/pebble"
)

type db struct {
	pebbleDb *pebble.DB

	channelMsgMaxSeqPrefix string
}

func newDb() *db {
	d := &db{
		channelMsgMaxSeqPrefix: "channel_msg_max_seq:",
	}

	return d
}

func (d *db) open() error {
	opts := d.defaultPebbleOptions()
	db, err := pebble.Open(path.Join(pdk.S.SandboxDir(), "db"), opts)
	if err != nil {
		return err
	}
	d.pebbleDb = db
	return nil
}

func (d *db) close() {
	if d.pebbleDb == nil {
		return
	}
	d.pebbleDb.Close()
}

func (d *db) defaultPebbleOptions() *pebble.Options {
	blockSize := 32 * 1024
	sz := 16 * 1024 * 1024
	levelSizeMultiplier := 2

	lopts := make([]pebble.LevelOptions, 0)
	var numOfLevels int64 = 7
	for l := int64(0); l < numOfLevels; l++ {
		opt := pebble.LevelOptions{
			// Compression:    pebble.NoCompression,
			BlockSize:      blockSize,
			TargetFileSize: 16 * 1024 * 1024,
		}
		sz = sz * levelSizeMultiplier
		lopts = append(lopts, opt)
	}
	return &pebble.Options{
		Levels:             lopts,
		FormatMajorVersion: pebble.FormatNewest,
		// 控制写缓冲区的大小。较大的写缓冲区可以减少磁盘写入次数，但会占用更多内存。
		MemTableSize: 16 * 1024 * 1024,
		// 当队列中的MemTables的大小超过 MemTableStopWritesThreshold*MemTableSize 时，将停止写入，
		// 直到被刷到磁盘，这个值不能小于2
		MemTableStopWritesThreshold: 4,
		// MANIFEST 文件的大小
		MaxManifestFileSize:       128 * 1024 * 1024,
		LBaseMaxBytes:             4 * 1024 * 1024 * 1024,
		L0CompactionFileThreshold: 8,
		L0StopWritesThreshold:     24,
	}
}

// 设置频道已同步的最大消息序号
func (d *db) setChannelMaxMessageSeq(channelId string, channelType uint8, messageSeq uint64) error {

	key := d.channelMaxMessageSeqKey(channelId, channelType)

	var buf = make([]byte, 8)
	binary.BigEndian.PutUint64(buf, messageSeq)
	return d.pebbleDb.Set(key, buf, pebble.Sync)
}

// 获取频道已同步的最大消息序号
func (d *db) getChannelMaxMessageSeq(channelId string, channelType uint8) (uint64, error) {
	key := d.channelMaxMessageSeqKey(channelId, channelType)

	data, closer, err := d.pebbleDb.Get(key)
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	return binary.BigEndian.Uint64(data), nil
}

func (d *db) indexedChannels() ([]Channel, error) {
	prefix := []byte(d.channelMsgMaxSeqPrefix)
	iter, err := d.pebbleDb.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	channels := make([]Channel, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		key := strings.TrimPrefix(string(iter.Key()), d.channelMsgMaxSeqPrefix)
		separatorIndex := strings.LastIndexByte(key, ':')
		if separatorIndex <= 0 || separatorIndex == len(key)-1 {
			continue
		}

		channelType, err := strconv.ParseUint(key[separatorIndex+1:], 10, 8)
		if err != nil {
			return nil, fmt.Errorf("parse channel type from index state key %q: %w", string(iter.Key()), err)
		}
		channels = append(channels, Channel{
			ChannelId:   key[:separatorIndex],
			ChannelType: uint8(channelType),
		})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return channels, nil
}

func (d *db) resetChannelMaxMessageSeq(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}

	batch := d.pebbleDb.NewBatch()
	defer batch.Close()

	var zero [8]byte
	for _, channel := range channels {
		key := d.channelMaxMessageSeqKey(channel.ChannelId, channel.ChannelType)
		if err := batch.Set(key, zero[:], nil); err != nil {
			return err
		}
	}
	return d.pebbleDb.Apply(batch, pebble.Sync)
}

func (d *db) channelMaxMessageSeqKey(channelId string, channelType uint8) []byte {
	return []byte(fmt.Sprintf("%s%s:%d", d.channelMsgMaxSeqPrefix, channelId, channelType))
}

func prefixUpperBound(prefix []byte) []byte {
	upperBound := append([]byte(nil), prefix...)
	for i := len(upperBound) - 1; i >= 0; i-- {
		if upperBound[i] < 0xff {
			upperBound[i]++
			return upperBound[:i+1]
		}
	}
	return nil
}
