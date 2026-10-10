-- Removes messages that have reached a terminal state and are older than the
-- cutoff.
--
-- Only FAILED qualifies: a delivered message is deleted outright. HELD rows
-- accumulate too, but how long they live is the review queue's business, not
-- this retention's: see prune_held_outbox.sql. A failed one is kept for a while because it is the record
-- an operator consults when asking why something never arrived — but keeping it
-- forever means the table grows for the life of the deployment, and each row
-- carries the whole serialised request including the message body.
DELETE FROM outbox
WHERE status = 'FAILED'
  AND updated_at < $1;
