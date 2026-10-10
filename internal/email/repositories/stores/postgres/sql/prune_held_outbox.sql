-- Removes held messages that no review can release any more, once they are
-- older than the cutoff.
--
-- A held message waits here for a reviewer, and its row carries the whole
-- serialised request including attachments. The review queue is what decides
-- when it is finished with -- a reject or an expiry discards it -- and this is
-- the backstop for when that discard never happened: it failed, it predates
-- the expiry sweep discarding anything, or the review record was never
-- written at all. Nothing else ever deleted a HELD row.
--
-- The NOT EXISTS is what keeps it safe whatever the cutoff. A hold whose
-- review is still PENDING can be released, so its payload stays however old it
-- is -- including when the quarantine period was shortened after it was held,
-- since its own deadline was promised when it was held.
DELETE FROM outbox
WHERE status = 'HELD'
  AND created_at < $1
  AND NOT EXISTS (
        SELECT 1
        FROM filtered_messages f
        WHERE f.message_id = outbox.id
          AND f.status = 'PENDING'
  );
