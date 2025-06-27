-- v2: Add waypoints

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