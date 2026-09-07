-- v5: Store the key of a node's last-heard channel alongside its name

-- Added in place: mesh_waypoints references this table with ON DELETE CASCADE.
ALTER TABLE mesh_node_info ADD COLUMN channel_key VARCHAR(64) NOT NULL DEFAULT '';
