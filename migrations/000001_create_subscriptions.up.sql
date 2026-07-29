CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS subscriptions (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    service_name VARCHAR(255) NOT NULL,
    price        INTEGER      NOT NULL CHECK (price >= 0),
    user_id      UUID         NOT NULL,
    -- Учитываются только месяц и год, поэтому день всегда первый.
    start_date   DATE         NOT NULL,
    end_date     DATE,

    CHECK (end_date IS NULL OR end_date >= start_date)
);

-- Оба фильтра из ТЗ (по пользователю и по названию сервиса) должны попадать
-- в индекс, иначе выборка и подсчёт суммы идут последовательным сканом.
CREATE INDEX IF NOT EXISTS subscriptions_user_id_idx ON subscriptions (user_id);

-- Название сервиса сравнивается без учёта регистра, поэтому индекс по выражению:
-- обычный индекс по service_name для lower(service_name) = ... не применится.
CREATE INDEX IF NOT EXISTS subscriptions_service_name_idx ON subscriptions (lower(service_name));
