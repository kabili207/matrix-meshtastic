-- v4: Bridge root identity key

CREATE TABLE bridge_identity (
    id          INTEGER NOT NULL CHECK (id = 1),
    private_key BYTEA NOT NULL,
    created     BIGINT NOT NULL,

    PRIMARY KEY (id)
);
