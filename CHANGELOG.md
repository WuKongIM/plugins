## Unreleased

- Search 0.0.2 catches up explicitly queried channels before returning results, so a changed leader or a missed PersistAfter notification does not silently omit committed history. Refresh uses bounded queues and a request deadline; remote refresh failures are returned to user search. / 搜索插件 0.0.2 在查询前补齐指定频道的索引，避免 Leader 变化或通知遗漏后漏搜已提交消息；补齐采用有界队列与请求时限，远端补齐失败会传递给用户搜索。

首次发布
