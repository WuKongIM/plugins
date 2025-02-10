package search

import (
	"encoding/binary"
	"fmt"

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
	opts := d.defaultPebbleOptions()

	db, err := pebble.Open("db", opts)
	if err != nil {
		panic(err)
	}
	d.pebbleDb = db
	return d
}

func (d *db) close() {
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

	key := fmt.Sprintf("%s%s:%d", d.channelMsgMaxSeqPrefix, channelId, channelType)

	var buf = make([]byte, 8)
	binary.BigEndian.PutUint64(buf, messageSeq)
	return d.pebbleDb.Set([]byte(key), buf, pebble.Sync)
}

// 获取频道已同步的最大消息序号
func (d *db) getChannelMaxMessageSeq(channelId string, channelType uint8) (uint64, error) {
	key := fmt.Sprintf("%s%s:%d", d.channelMsgMaxSeqPrefix, channelId, channelType)

	data, closer, err := d.pebbleDb.Get([]byte(key))
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
