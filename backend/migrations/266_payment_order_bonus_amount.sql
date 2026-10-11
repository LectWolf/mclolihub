-- Imported upstream payment bonus migration, numbered after the fork's history.
-- The credited order amount already includes the bonus; this column keeps the
-- bonus visible and excludes it from affiliate rebate calculations.
ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS bonus_amount DECIMAL(20,2) NOT NULL DEFAULT 0;
