ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_no_overlap;

-- Расширение не удаляется: его мог поставить или использовать кто-то ещё
-- в той же базе.
