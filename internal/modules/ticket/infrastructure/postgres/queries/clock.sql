-- name: TransactionTime :one
-- The single instant a write transaction is stamped with.
--
-- now() is the transaction timestamp, so every call inside one transaction
-- returns the same value. Reading it here, rather than taking time.Now() in Go,
-- is what keeps deadlines on the database's clock: the breach worker selects
-- WHERE sla_due_at < now(), and a deadline derived from an API instance's clock
-- would be shifted by whatever that instance's drift happens to be. On Cloud
-- Run there is more than one instance and no reason to expect them to agree.
SELECT now()::timestamptz AS transaction_time;
