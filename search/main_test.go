package main

import (
	"github.com/WuKongIM/go-pdk/pdk/pluginproto"
	"github.com/WuKongIM/plugins/search/search"
	"testing"
)

func TestQueryChannelsIntersectsExplicitFilter(t *testing.T) {
	channels := make([]*pluginproto.Channel, 1001)
	for i := range channels {
		channels[i] = &pluginproto.Channel{ChannelId: "unrelated", ChannelType: 2}
	}
	channels = append(channels, &pluginproto.Channel{ChannelId: "target", ChannelType: 2}, &pluginproto.Channel{ChannelId: "target", ChannelType: 1})
	for _, tc := range []struct {
		name string
		req  search.SearchReq
		want int
	}{
		{"one current channel amid cold conversations", search.SearchReq{Channels: channels, ChannelId: "target", ChannelType: 2}, 1},
		{"unauthorized filter stays empty", search.SearchReq{Channels: channels, ChannelId: "absent", ChannelType: 2}, 0},
		{"all types for matching ID", search.SearchReq{Channels: channels, ChannelId: "target"}, 2},
		{"global scope preserved", search.SearchReq{Channels: channels}, len(channels)},
		{"explicit direct search", search.SearchReq{ChannelId: "target", ChannelType: 2}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := queryChannels(tc.req)
			if len(got) != tc.want {
				t.Fatalf("refresh scope=%d want%d", len(got), tc.want)
			}
			for _, c := range got {
				if tc.req.ChannelId != "" && c.ChannelId != tc.req.ChannelId {
					t.Fatal("unrelated channel")
				}
			}
		})
	}
}
