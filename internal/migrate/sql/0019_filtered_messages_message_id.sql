-- Looks up a review record by the message it is about.
--
-- The held-outbox prune asks, for every old HELD row, whether a review of it is
-- still PENDING, and releasing or discarding a hold finds its review the same
-- way. Without this the question is a scan of filtered_messages per row, and
-- that table is the review history of every tenant.
CREATE INDEX IF NOT EXISTS idx_filtered_messages_message_id
    ON filtered_messages (message_id);
