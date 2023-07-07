CREATE TABLE IF NOT EXISTS subscriptions (
    id BIGSERIAL PRIMARY KEY,
    feed_id BIGINT NOT NULL,
    server_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    last_pub_date TIMESTAMP WITH TIME ZONE,
    UNIQUE(feed_id, server_id, channel_id),
    CONSTRAINT fkey_feed FOREIGN KEY(feed_id) REFERENCES feeds(id)
);
