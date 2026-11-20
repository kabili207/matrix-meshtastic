-- v6: Store each node's hardware model and status message for its Matrix profile

-- Added in place: mesh_waypoints references this table with ON DELETE CASCADE.
ALTER TABLE mesh_node_info ADD COLUMN hw_model VARCHAR(40) NOT NULL DEFAULT '';
ALTER TABLE mesh_node_info ADD COLUMN status TEXT NOT NULL DEFAULT '';
