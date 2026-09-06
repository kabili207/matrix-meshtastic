-- v3: Track the channel a node's NodeInfo was last heard on

-- Added in place: mesh_waypoints references this table with ON DELETE CASCADE,
-- so a table rebuild would drop every waypoint.
ALTER TABLE mesh_node_info ADD COLUMN channel VARCHAR(11) NOT NULL DEFAULT '';
