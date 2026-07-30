package search

import (
	"hash/crc32"

	"github.com/WuKongIM/wklog"
	"go.uber.org/zap"
)

// HashCrc32 通过字符串获取32位数字
func HashCrc32(str string) uint32 {

	return crc32.ChecksumIEEE([]byte(str))
}

// GetFakeChannelIDWith GetFakeChannelIDWith
func GetFakeChannelIDWith(fromUID, toUID string) string {
	// TODO：这里可能会出现相等的情况 ，如果相等可以截取一部分再做hash直到不相等，后续完善
	fromUIDHash := HashCrc32(fromUID)
	toUIDHash := HashCrc32(toUID)
	if fromUIDHash > toUIDHash {
		return fromUID + "@" + toUID
	}
	if fromUID != toUID && fromUIDHash == toUIDHash {
		wklog.Warn("生成的fromUID的Hash和toUID的Hash是相同的！！", zap.Uint32("fromUIDHash", fromUIDHash), zap.Uint32("toUIDHash", toUIDHash), zap.String("fromUID", fromUID), zap.String("toUID", toUID))
	}
	return toUID + "@" + fromUID
}
