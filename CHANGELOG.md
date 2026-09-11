## Unreleased

- Scoped search refreshes only its allowed query intersection; indexing rejects incomplete or misaligned history before advancing checkpoints. All history RPCs share a process bound. / 单频道搜索仅刷新授权查询交集；索引拒绝不完整或错配历史，所有历史查询共用进程级并发上限。

- Search verifies current channel checkpoints with bounded committed-history reads before enqueueing, so unrelated cold index rebuilds do not block already-current channels. / 搜索先通过有界的已提交历史查询核对索引位置，已同步频道不再等待其他频道的冷索引重建。

- Search refresh queues preserve every completion notification when draining full batches, preventing avoidable query timeouts under backlog. / 修复搜索刷新队列在批次边界丢失完成通知、造成查询超时的问题。

- Search 0.0.2 catches up explicitly queried channels before returning results, so a changed leader or a missed PersistAfter notification does not silently omit committed history. Refresh uses bounded queues and a request deadline; remote refresh failures are returned to user search. / 搜索插件 0.0.2 在查询前补齐指定频道的索引，避免 Leader 变化或通知遗漏后漏搜已提交消息；补齐采用有界队列与请求时限，远端补齐失败会传递给用户搜索。

首次发布
