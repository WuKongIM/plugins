# Search plugin

`/usersearch` resolves the user's conversation channels and queries their current
owners. `/search` refreshes an explicit `channels` list, or a single
`channel_id` plus `channel_type`, from committed history before reading Bleve.
This refresh is required after a leader change because `PersistAfter` is a
best-effort notification and the new owner can have an older local index.

At most 1,000 explicit channels are admitted per request. Before enqueueing,
refresh checks committed history after each persisted index checkpoint in
batches of 16 channels, with at most four concurrent reads per query and 16
in-flight reads per plugin process. Only complete, aligned empty pages prove a
channel current, allowing it to bypass a queue occupied by another channel’s
cold rebuild. Canceled callers retain their read slots until the underlying RPC
returns. Channels with new messages enter the existing bounded indexing queues;
a query waits up to three seconds. A full queue,
history-read failure, or timeout returns an error rather than a successful
partial result. Already queued indexing can finish after the caller times out,
so retrying can make progress through a large backlog. Startup rebuild and
queued indexing serialize checkpoint updates within each bucket.

An unscoped `/search` retains its previous index-only behavior; it cannot infer
unknown channels or promise a complete cluster-wide history search. Business
clients should use `/usersearch` or provide explicit channels.

For v2-to-v3 migration, retain the original executable and cold backup, finish
offline import verification, then record the version and SHA-256 of any upgraded
runtime executable before startup acceptance. Verify historical results, a write
through only one node followed by leader replacement, and full-cluster restarts.
Do not treat server readiness or writes rotating through every node as proof of
search-index freshness.
