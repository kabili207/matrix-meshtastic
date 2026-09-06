package connector

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// ensureIdentity loads or creates the bridge root key, fixes the bridge's own node
// ID from it, and re-creates any login still on the pre-2.8 MXID-hash scheme. It
// runs before the mesh transport starts so nothing is sent under a stale ID.
func (c *MeshtasticConnector) ensureIdentity(ctx context.Context) error {
	key, err := c.meshDB.GetBridgeKey(ctx)
	if err != nil {
		c.log.Err(err).Msg("Failed to load bridge root key")
		return err
	}
	if key == nil {
		key = make([]byte, meshid.RootKeySize)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		if err := c.meshDB.SetBridgeKey(ctx, key); err != nil {
			c.log.Err(err).Msg("Failed to store bridge root key")
			return err
		}
		c.log.Warn().Msg("Generated a new bridge root key. Every node ID derives from it, so back up the database")
	}
	c.rootKey = key

	baseID, pub, _, err := meshid.DeriveBridgeIdentity(key)
	if err != nil {
		return err
	}
	c.baseNodeID = baseID
	c.log.Info().
		Stringer("node_id", baseID).
		Str("public_key", base64.StdEncoding.EncodeToString(pub)).
		Msg("Bridge mesh identity")

	if err := c.migrateStaleIdentities(ctx); err != nil {
		return err
	}
	// Rows created by the migration before this stamp existed have no last-seen
	// time, which keeps them out of the periodic NodeInfo broadcast.
	if err := c.meshDB.MeshNodeInfo.BackfillManagedLastSeen(ctx); err != nil {
		c.log.Err(err).Msg("Failed to backfill managed node activity")
	}
	return nil
}

// NodeIDForMXID returns the mesh node ID of a Matrix user. It is a pure function of
// the root key and the MXID, so it works for users who have never logged in.
func (c *MeshtasticConnector) NodeIDForMXID(mxid id.UserID) meshid.NodeID {
	c.identityMu.RLock()
	nodeID, ok := c.identityCache[mxid]
	c.identityMu.RUnlock()
	if ok {
		return nodeID
	}
	nodeID, _, _, err := meshid.DeriveUserIdentity(c.rootKey, mxid)
	if err != nil {
		c.log.Err(err).Stringer("mxid", mxid).Msg("Failed to derive node identity")
		return 0
	}
	c.identityMu.Lock()
	c.identityCache[mxid] = nodeID
	c.identityMu.Unlock()
	return nodeID
}

// bridgeKeys returns the bridge's own key pair.
func (c *MeshtasticConnector) bridgeKeys() (pub, priv []byte, err error) {
	_, pub, priv, err = meshid.DeriveBridgeIdentity(c.rootKey)
	return pub, priv, err
}

// managedIdentityKeys derives a managed node's key pair from the Matrix user it
// belongs to and caches it on the node row, which is where the send paths read it.
func (c *MeshtasticConnector) managedIdentityKeys(ctx context.Context, nodeInfo *meshdb.MeshNodeInfo) (pub, priv []byte, err error) {
	ghost, err := c.bridge.GetExistingGhostByID(ctx, meshid.MakeUserID(nodeInfo.NodeID))
	if err != nil {
		return nil, nil, err
	}
	meta, _ := ghost.Metadata.(*meshid.GhostMetadata)
	if ghost == nil || meta == nil || meta.UserMXID == "" {
		return nil, nil, fmt.Errorf("managed node %s has no Matrix user", nodeInfo.NodeID)
	}
	derived, pub, priv, err := meshid.DeriveUserIdentity(c.rootKey, meta.UserMXID)
	if err != nil {
		return nil, nil, err
	}
	if derived != nodeInfo.NodeID {
		return nil, nil, fmt.Errorf("node %s is not the derived identity of %s (%s)", nodeInfo.NodeID, meta.UserMXID, derived)
	}
	nodeInfo.PublicKey = pub
	nodeInfo.PrivateKey = priv
	if err := nodeInfo.SetAll(ctx); err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

// migrateStaleIdentities re-creates every login whose node ID is not the one derived
// for its user. Each login is handled independently, so a partial run resumes on
// the next start. The bridge's own pre-2.8 record is retired the same way.
func (c *MeshtasticConnector) migrateStaleIdentities(ctx context.Context) error {
	if old := meshid.LegacyMXIDToNodeID(c.bridge.Bot.GetMXID()); old != c.baseNodeID {
		if err := c.meshDB.MeshNodeInfo.RetireManagedNode(ctx, old); err != nil {
			c.log.Err(err).Stringer("node_id", old).Msg("Failed to retire old bridge identity")
		}
	}

	userIDs, err := c.bridge.DB.UserLogin.GetAllUserIDsWithLogins(ctx)
	if err != nil {
		return err
	}
	migrated := 0
	for _, mxid := range userIDs {
		logins, err := c.bridge.DB.UserLogin.GetAllForUser(ctx, mxid)
		if err != nil {
			c.log.Err(err).Stringer("mxid", mxid).Msg("Failed to list logins")
			continue
		}
		newID := c.NodeIDForMXID(mxid)
		for _, row := range logins {
			oldID := loginNodeID(row)
			if oldID == newID {
				continue
			}
			log := c.log.With().Stringer("mxid", mxid).Stringer("old_node_id", oldID).Stringer("new_node_id", newID).Logger()
			if err := c.migrateLogin(log.WithContext(ctx), mxid, row, oldID, newID); err != nil {
				log.Err(err).Msg("Failed to migrate login to derived identity; log in again to fix")
				continue
			}
			log.Info().Msg("Migrated login to derived identity")
			migrated++
		}
	}
	if migrated > 0 {
		c.managedNodeCache = map[meshid.NodeID]bool{}
		c.log.Info().Int("migrated", migrated).Msg("Renumbered logins for firmware 2.8 identities")
	}
	return nil
}

// loginNodeID reads the node ID a login was created under.
func loginNodeID(row *database.UserLogin) meshid.NodeID {
	if meta, ok := row.Metadata.(*meshid.UserLoginMetadata); ok && meta.NodeID != 0 {
		return meta.NodeID
	}
	nodeID, _ := meshid.ParseUserID(networkid.UserID(row.ID))
	return nodeID
}

// migrateLogin replaces one login with a fresh one under the derived identity. The
// names come from the old node record, falling back to the login's profile. DM
// portals are re-pointed at the new synth ID so rooms and history survive.
func (c *MeshtasticConnector) migrateLogin(ctx context.Context, mxid id.UserID, row *database.UserLogin, oldID, newID meshid.NodeID) error {
	longName, shortName := "", ""
	if info, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, oldID); err == nil && info != nil {
		longName, shortName = info.LongName, info.ShortName
	}
	if longName == "" {
		longName = row.RemoteProfile.Name
	}
	if shortName == "" {
		shortName = row.RemoteName
	}
	if longName == "" || shortName == "" {
		return errors.New("no names recorded for the old identity")
	}

	user, err := c.bridge.GetUserByMXID(ctx, mxid)
	if err != nil {
		return fmt.Errorf("loading user: %w", err)
	}
	oldLogin, err := c.bridge.GetExistingUserLoginByID(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("loading old login: %w", err)
	}

	newLogin, err := c.createLogin(ctx, user, longName, shortName)
	if err != nil {
		return fmt.Errorf("creating login: %w", err)
	}

	portals, err := c.bridge.GetAllPortalsWithMXID(ctx)
	if err != nil {
		return fmt.Errorf("listing portals: %w", err)
	}
	for _, p := range portals {
		remote, synth, err := meshid.ParseDMPortalID(p.ID)
		if err != nil || synth != oldID {
			continue
		}
		target := networkid.PortalKey{ID: meshid.MakeDMPortalID(remote, newID), Receiver: newLogin.ID}
		if _, _, err := c.bridge.ReIDPortal(ctx, p.PortalKey, target); err != nil {
			c.log.Err(err).Str("portal_id", string(p.ID)).Msg("Failed to re-ID DM portal")
		}
	}

	if oldLogin != nil {
		oldLogin.Delete(ctx, status.BridgeState{}, bridgev2.DeleteOpts{DontCleanupRooms: true})
	}
	c.retireIdentity(ctx, oldID)
	c.notifyRenumbered(ctx, user, oldID, newID)
	return nil
}

// retireIdentity strips the managed status and keys from an old identity so it is
// no longer broadcast or answered for, while keeping the node record itself.
func (c *MeshtasticConnector) retireIdentity(ctx context.Context, nodeID meshid.NodeID) {
	if err := c.meshDB.MeshNodeInfo.RetireManagedNode(ctx, nodeID); err != nil {
		c.log.Err(err).Stringer("node_id", nodeID).Msg("Failed to retire node record")
	}
	ghost, err := c.bridge.GetExistingGhostByID(ctx, meshid.MakeUserID(nodeID))
	if err == nil && ghost != nil {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{ExtraUpdates: c.updateGhostSenderID("")})
	}
	delete(c.managedNodeCache, nodeID)
}

func (c *MeshtasticConnector) notifyRenumbered(ctx context.Context, user *bridgev2.User, oldID, newID meshid.NodeID) {
	if user.ManagementRoom == "" {
		return
	}
	body := fmt.Sprintf(
		"Meshtastic firmware 2.8 derives node IDs from each node's public key, so your mesh identity was re-created. "+
			"Your node ID changed from %s to %s. Your rooms and messages were kept, but other nodes will see you as a new node.",
		oldID, newID)
	_, err := c.bridge.Bot.SendMessage(ctx, user.ManagementRoom, event.EventMessage, &event.Content{
		Parsed: &event.MessageEventContent{MsgType: event.MsgNotice, Body: body},
	}, nil)
	if err != nil {
		c.log.Err(err).Stringer("mxid", user.MXID).Msg("Failed to send renumbering notice")
	}
}
