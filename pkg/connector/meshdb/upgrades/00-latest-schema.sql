-- v0 -> v5: Latest revision

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
    -- Name of the PSK channel this node's NodeInfo was last heard on, so
    -- unicasts to it can follow that channel across restarts.
    channel          VARCHAR(11) NOT NULL DEFAULT '',
    -- Base64 PSK of that channel. A channel is identified by name and key
    -- together; the name alone can match more than one.
    channel_key      VARCHAR(64) NOT NULL DEFAULT '',

    PRIMARY KEY (id),
    CONSTRAINT mesh_node_info_user_id UNIQUE (user_id)
);

-- Single row holding the root secret every managed identity is derived from.
CREATE TABLE bridge_identity (
    id          INTEGER NOT NULL CHECK (id = 1),
    private_key BYTEA NOT NULL,
    created     BIGINT NOT NULL,

    PRIMARY KEY (id)
);

CREATE TABLE mesh_waypoints (
    -- only: sqlite (line commented)
--	id              BIGINT NOT NULL CHECK (id >= 0 AND id < 4294967296),
    -- only: postgres
    id              BIGINT NOT NULL CHECK (id >= 0 AND id < '4294967296'::BIGINT),
    name            VARCHAR(29) NOT NULL,
    icon            CHAR(1) NOT NULL,
    description     VARCHAR(99) NOT NULL,
    latitude        REAL NOT NULL,
    longitude       REAL NOT NULL,
    -- only: sqlite (line commented)
--	locked_to       BIGINT CHECK (locked_to IS NULL OR (locked_to > 1 AND locked_to < 4294967295)),
    -- only: postgres
    locked_to       BIGINT CHECK (locked_to IS NULL OR (locked_to > 1 AND locked_to < '4294967295'::BIGINT)),
    expires         BIGINT,
    -- only: sqlite (line commented)
--	updated_by      BIGINT NOT NULL CHECK (updated_by > 1 AND updated_by < 4294967295),
    -- only: postgres
    updated_by      BIGINT NOT NULL CHECK (updated_by > 1 AND updated_by < '4294967295'::BIGINT),
    updated_date    BIGINT NOT NULL,

    PRIMARY KEY (id),
    CONSTRAINT mesh_waypoints_locked_to_fkey FOREIGN KEY (locked_to)
        REFERENCES mesh_node_info (id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT mesh_waypoints_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES mesh_node_info (id) ON UPDATE CASCADE ON DELETE CASCADE
)