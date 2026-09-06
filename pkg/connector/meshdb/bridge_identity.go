package meshdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	getBridgeKeyQuery = "SELECT private_key FROM bridge_identity WHERE id=1"
	setBridgeKeyQuery = "INSERT INTO bridge_identity (id, private_key, created) VALUES (1, $1, $2)"
)

// GetBridgeKey returns the bridge root secret, or nil if none has been created.
func (c *Database) GetBridgeKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := c.QueryRow(ctx, getBridgeKeyQuery).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return key, err
}

// SetBridgeKey stores the root secret. It is written once; a second insert fails.
func (c *Database) SetBridgeKey(ctx context.Context, key []byte) error {
	_, err := c.Exec(ctx, setBridgeKeyQuery, key, time.Now().Unix())
	return err
}
