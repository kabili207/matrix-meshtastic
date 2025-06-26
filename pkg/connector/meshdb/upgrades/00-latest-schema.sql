-- v0 -> v1: Latest revision

CREATE TABLE mesh_node_info (
    -- 0 = unset, 1 = non-lora broadcast, 4294967295 = broadcast
    -- only: sqlite (line commented)
--	id               BIGINT NOT NULL CHECK (id >= 2 AND id < 4294967295),
    -- only: postgres
    id               BIGINT NOT NULL CHECK (id >= 2 AND id < '4294967295'::BIGINT),
    user_id          VARCHAR(20) NOT NULL DEFAULT '',
    long_name        VARCHAR(39) NOT NULL DEFAULT '',
    short_name       VARCHAR(4) NOT NULL DEFAULT '',
    node_role        VARCHAR(20) NOT NULL DEFAULT '',
    is_licensed      BOOLEAN NOT NULL DEFAULT false,
    is_unmessageable BOOLEAN NOT NULL DEFAULT false,
    is_managed       BOOLEAN NOT NULL DEFAULT false,
    is_direct        BOOLEAN NOT NULL DEFAULT false,
    public_key       BYTEA,
    private_key      BYTEA,
    last_seen        BIGINT,

    PRIMARY KEY (id),
    CONSTRAINT mesh_node_info_user_id UNIQUE (user_id)
);