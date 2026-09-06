package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/kabili207/meshtastic-go/core"
	"github.com/kabili207/meshtastic-go/core/lora"
	"github.com/kabili207/meshtastic-go/device/node"
	"go.mau.fi/util/ptr"
	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var _ bridgev2.ReactionHandlingNetworkAPI = (*MeshtasticClient)(nil)
var _ bridgev2.TypingHandlingNetworkAPI = (*MeshtasticClient)(nil)
var _ bridgev2.ReadReceiptHandlingNetworkAPI = (*MeshtasticClient)(nil)
var _ bridgev2.MembershipHandlingNetworkAPI = (*MeshtasticClient)(nil)

// sendOpts builds the common send options for an outgoing message: the target
// channel and whether to use PKI encryption.
func (c *MeshtasticClient) sendOpts(channel *core.Channel, usePKI bool) []node.SendOption {
	opts := []node.SendOption{}
	if channel != nil {
		opts = append(opts, node.WithChannel(channel.GetName()))
	}
	if usePKI {
		opts = append(opts, node.WithPKI())
	}
	return opts
}

func (c *MeshtasticClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (message *bridgev2.MatrixMessageResponse, err error) {

	log := c.log.With().
		Str("user_id", string(c.UserLogin.ID)).
		Str("sender_mxid", string(msg.Event.Sender)).
		Str("user_id", string(msg.Event.ID)).
		Logger()
	ctx = log.WithContext(ctx)

	log.Info().Msg("HandleMatrixMessage called")

	// try to get the user
	_, err = c.bridge.GetExistingUserByMXID(ctx, msg.Event.Sender)
	if err != nil {
		log.Err(err).Str("user_mxid", string(msg.Event.Sender)).Msg("Failed to get user object, ignoring message")
		// ignoring this because we only reply to user messages
		return nil, nil
	}

	fromNode := c.main.NodeIDForMXID(msg.Event.Sender)
	// Left nil for DMs so the bridge picks the channel the peer was last heard on.
	var channel *core.Channel
	messIDSender := ""
	targetNode := meshid.BROADCAST_ID
	usePKI := false

	switch msg.Portal.Portal.RoomType {
	case database.RoomTypeDefault:
		channel, err = meshid.ChannelDefFromPortalID(msg.Portal.ID)
		if err == nil {
			messIDSender = channel.GetName()
		}
	case database.RoomTypeDM:
		targetNode, _, err = meshid.ParseDMPortalID(msg.Portal.ID)
		if err == nil {
			if nodeInfo, err2 := c.main.meshDB.MeshNodeInfo.GetByNodeID(ctx, targetNode); err2 == nil && nodeInfo != nil {
				if nodeInfo.IsUnmessagable {
					err = errors.New("node is marked as unmesseagable")
				}
			}
			messIDSender = targetNode.String()
			if pubKey, err := c.main.getGhostPublicKey(ctx, targetNode); err == nil && len(pubKey) > 0 {
				usePKI = true
			}
		}
	default:
		err = fmt.Errorf("unsupported room type: %s", msg.Portal.Portal.RoomType)
	}

	if err != nil {
		log.Err(err).
			Str("user_mxid", string(msg.Event.Sender)).
			Str("portal_id", string(msg.Portal.ID)).
			Msg("Failed to parse portal ID, ignoring message")
		return nil, nil
	}

	packetId, geouri, err := uint32(0), (*meshid.GeoURI)(nil), nil
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		content, _ := c.main.MsgConv.ToMeshtastic(ctx, msg.Event, msg.Content)
		opts := c.sendOpts(channel, usePKI)
		if msg.ReplyTo != nil {
			if _, replyID, perr := meshid.ParseMessageID(msg.ReplyTo.ID); perr == nil && replyID != 0 {
				opts = append(opts, node.WithReplyID(replyID))
			}
		}
		packetId, err = c.main.meshBridge.SendTextAs(ctx, fromNode.Core(), targetNode.Core(), content, opts...)
	case event.MsgLocation:
		geouri, err = meshid.ParseGeoURI(msg.Content.GeoURI)
		if err != nil {
			return nil, bridgev2.WrapErrorInStatus(err).WithErrorAsMessage().WithIsCertain(true).WithSendNotice(true)
		}
		ts := time.UnixMilli(msg.Event.Timestamp)
		latI := int32(geouri.Latitude * 1e7)
		lonI := int32(geouri.Longitude * 1e7)
		var alt *int32
		if geouri.Altitude != nil {
			alt = ptr.Ptr(int32(*geouri.Altitude))
		}
		// Zero tells receivers the precision is unknown rather than claiming a
		// ~22 m radius the coordinates were never truncated to.
		var precision uint32
		if geouri.Uncertainty != nil {
			precision = lora.MetersToPrecisionBits(*geouri.Uncertainty)
		}
		packetId, err = c.main.meshBridge.SendPositionAs(ctx, fromNode.Core(), targetNode.Core(), latI, lonI, alt, precision, ts, c.sendOpts(channel, usePKI)...)

	default:
		return nil, bridgev2.ErrUnsupportedMessageType
	}

	if err != nil {
		return nil, bridgev2.WrapErrorInStatus(err).WithErrorAsMessage().WithIsCertain(true).WithSendNotice(true)
	}

	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:       meshid.MakeMessageID(messIDSender, packetId),
			SenderID: meshid.MakeUserID(fromNode),
		},
		PostSave: c.postMessageSave(msg.Event.Sender, msg.Event.RoomID),
	}, nil
}

func (c *MeshtasticClient) postMessageSave(mxid id.UserID, roomId id.RoomID) func(context.Context, *database.Message) {
	return func(ctx context.Context, m *database.Message) {

		log := c.log.With().
			Str("user_id", string(c.UserLogin.ID)).
			Str("sender_mxid", string(mxid)).
			Str("mesh_id", string(m.SenderID)).
			Logger()
		ctx = log.WithContext(ctx)

		log.Info().Msg("postMessageSave called")

		ghost, err := c.bridge.GetGhostByID(ctx, m.SenderID)
		if err != nil {
			log.Err(err).Msg("Failed to get ghost")
			return
		}

		u, err := c.bridge.Matrix.GetMemberInfo(ctx, roomId, mxid)
		if err != nil {
			log.Err(err).Msg("Failed to get user object, ignoring message")
			// ignoring this because we only reply to user messages
			return
		}

		if !ghost.NameSet {
			nodeID := c.main.NodeIDForMXID(mxid)
			longName, shortName := nodeID.GetDefaultNodeNames()
			if strings.TrimSpace(u.Displayname) != "" {
				longName = TruncateString(strings.TrimSpace(u.Displayname), core.MaxLongName)
			}
			if err = c.main.UpdateGhostMeshNames(ctx, m.SenderID, mxid, longName, shortName); err != nil {
				log.Err(err).Msg("Unable to set fallback ghost names")
			}
		}
	}
}

func (c *MeshtasticConnector) UpdateGhostMeshNames(ctx context.Context, userID networkid.UserID, mxid id.UserID, longName, shortName string) error {
	if len([]byte(longName)) > core.MaxLongName {
		return fmt.Errorf("long name must be at most %d bytes", core.MaxLongName)
	} else if len([]byte(shortName)) > core.MaxShortName {
		return fmt.Errorf("short name must be at most %d bytes", core.MaxShortName)
	}

	ghost, err := c.bridge.GetGhostByID(ctx, userID)
	if err != nil {
		return err
	}
	userInfo := &bridgev2.UserInfo{
		Name:         &longName,
		IsBot:        ptr.Ptr(false),
		Identifiers:  []string{},
		ExtraUpdates: bridgev2.MergeExtraUpdaters(c.updateGhostSenderID(mxid)),
	}
	ghost.UpdateInfo(ctx, userInfo)
	nodeID, err := meshid.ParseUserID(userID)
	if err != nil {
		return err
	}
	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
	if err != nil {
		return err
	}
	if nodeInfo == nil {
		nodeInfo = c.meshDB.MeshNodeInfo.New()
		nodeInfo.NodeID = nodeID
		nodeInfo.UserID = nodeID.String()
		nodeInfo.IsDirect = true
		nodeInfo.IsManaged = true
		// Counts as activity: the periodic broadcasts skip idle managed nodes, and a
		// node that has never broadcast a signed NodeInfo is never marked verified.
		nodeInfo.LastSeen = ptr.Ptr(time.Now().UTC())
	}

	if len(nodeInfo.PrivateKey) == 0 {
		derived, pub, priv, err := meshid.DeriveUserIdentity(c.rootKey, mxid)
		if err != nil {
			return err
		}
		if derived != nodeID {
			return fmt.Errorf("node %s is not the derived identity of %s (%s)", nodeID, mxid, derived)
		}
		nodeInfo.PublicKey = pub
		nodeInfo.PrivateKey = priv
	}

	nodeInfo.LongName = longName
	nodeInfo.ShortName = shortName
	if err := nodeInfo.SetAll(ctx); err != nil {
		return err
	}

	// Nil during the startup identity migration, which runs before the mesh starts.
	if c.meshBridge == nil {
		return nil
	}
	_, err = c.meshBridge.SendNodeInfoAs(ctx, nodeID.Core(), meshid.BROADCAST_ID.Core())
	return err
}

func (c *MeshtasticConnector) updateGhostSenderID(mxid id.UserID) func(context.Context, *bridgev2.Ghost) bool {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		meta := &meshid.GhostMetadata{}
		switch ghost.Metadata.(type) {
		case *meshid.GhostMetadata:
			meta = ghost.Metadata.(*meshid.GhostMetadata)
		default:
			ghost.Metadata = meta
		}
		forceSave := mxid != meta.UserMXID
		meta.UserMXID = mxid
		return forceSave
	}
}

func (c *MeshtasticConnector) updateDMPortalInfo(ctx context.Context, ghost *bridgev2.Ghost) {
	portals, err := c.bridge.GetDMPortalsWith(ctx, ghost.ID)
	if err != nil {
		c.log.Err(err).Str("node", string(ghost.ID)).Msg("Unable to get DM portals")
	}
	for _, p := range portals {
		ci := &bridgev2.ChatInfo{}
		c.setDMNames(ci, ghost)
		loginsInPortal, err := p.Bridge.GetUserLoginsInPortal(ctx, p.PortalKey)
		if err != nil {
			c.log.Err(err).Str("node", string(ghost.ID)).Msg("Failed to get user logins in portal")
		}
		if len(loginsInPortal) == 0 {
			c.log.Error().Str("node", string(ghost.ID)).Msg("Failed to get user logins in portal")
		}
		loginsInPortal[0].QueueRemoteEvent(&simplevent.ChatInfoChange{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatInfoChange,
				LogContext:   nil,
				PortalKey:    p.PortalKey,
				CreatePortal: false,
				Timestamp:    time.Now(),
			},
			ChatInfoChange: &bridgev2.ChatInfoChange{
				ChatInfo: ci,
			},
		})

	}
}

func (c *MeshtasticConnector) getGhostPublicKey(ctx context.Context, nodeID meshid.NodeID) ([]byte, error) {
	if nodeID == c.GetBaseNodeID() {
		pub, _, err := c.bridgeKeys()
		return pub, err
	}
	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if nodeInfo != nil && len(nodeInfo.PublicKey) > 0 {
		return nodeInfo.PublicKey, nil
	}
	if nodeInfo != nil && nodeInfo.IsManaged {
		pub, _, err := c.managedIdentityKeys(ctx, nodeInfo)
		return pub, err
	}
	return nil, errors.New("no public key found")
}

func (c *MeshtasticConnector) getGhostPrivateKey(ctx context.Context, nodeID meshid.NodeID) ([]byte, error) {
	if nodeID == c.GetBaseNodeID() {
		_, priv, err := c.bridgeKeys()
		return priv, err
	}
	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if nodeInfo != nil && len(nodeInfo.PrivateKey) > 0 {
		return nodeInfo.PrivateKey, nil
	}
	if nodeInfo != nil && nodeInfo.IsManaged {
		_, priv, err := c.managedIdentityKeys(ctx, nodeInfo)
		return priv, err
	}
	return nil, errors.New("no private key found")
}

func (c *MeshtasticClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	fromNode := c.main.NodeIDForMXID(msg.Event.Sender)
	return bridgev2.MatrixReactionPreResponse{
		SenderID: meshid.MakeUserID(fromNode),
		EmojiID:  networkid.EmojiID(msg.Content.RelatesTo.Key),
		Emoji:    variationselector.FullyQualify(msg.Content.RelatesTo.Key),
	}, nil
}

func (c *MeshtasticClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {

	pre := msg.PreHandleResp
	_, packetID, err := meshid.ParseMessageID(msg.TargetMessage.ID)
	if err != nil {
		return nil, err
	}

	var channel *core.Channel
	targetNode := meshid.BROADCAST_ID
	usePKI := false

	switch msg.Portal.Portal.RoomType {
	case database.RoomTypeDefault:
		channel, err = meshid.ChannelDefFromPortalID(msg.Portal.ID)
	case database.RoomTypeDM:
		targetNode, _, err = meshid.ParseDMPortalID(msg.Portal.ID)
		if pubKey, err := c.main.getGhostPublicKey(ctx, targetNode); err == nil && len(pubKey) > 0 {
			usePKI = true
		}
	default:
		err = fmt.Errorf("unsupported room type: %s", msg.Portal.Portal.RoomType)
	}

	if err != nil {
		return nil, err
	}

	fromNode, err := meshid.ParseUserID(pre.SenderID)
	if err != nil {
		return nil, err
	}
	_, err = c.main.meshBridge.SendReactionAs(ctx, fromNode.Core(), targetNode.Core(), packetID, pre.Emoji, c.sendOpts(channel, usePKI)...)
	return &database.Reaction{}, err
}

func (c *MeshtasticClient) UpdateLastSeenDate(ctx context.Context, sender id.UserID) {
	if c.bridge.IsGhostMXID(sender) {
		return
	}
	nodeID := c.main.NodeIDForMXID(sender)
	uid := meshid.MakeUserID(nodeID)
	_, err := c.bridge.GetGhostByID(ctx, uid)
	if err != nil {
		return
	}
	c.main.meshDB.MeshNodeInfo.SetLastSeen(ctx, nodeID, true)
}

func (mc *MeshtasticClient) HandleMatrixMembership(ctx context.Context, msg *bridgev2.MatrixMembershipChange) (*bridgev2.MatrixMembershipResult, error) {
	log := mc.log.With().
		Str("action", "handle_membership_change").
		Str("portal", string(msg.Portal.ID)).
		Logger()
	log.Debug().Msg("Handling membership change")

	if msg.Type != bridgev2.Join {
		return nil, nil
	}

	var err error
	var nodeID meshid.NodeID = 0

	switch target := msg.Target.(type) {
	case *bridgev2.Ghost:
		_ = target
		nodeID, err = meshid.ParseUserID(target.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse node ID for user: %w", err)
		}
	case *bridgev2.UserLogin:
		ghost, err := target.Bridge.GetGhostByID(ctx, networkid.UserID(target.ID))
		if err != nil {
			return nil, fmt.Errorf("failed to get ghost for user: %w", err)
		}
		nodeID, err = meshid.ParseUserID(ghost.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse node ID for user: %w", err)
		}
	default:
		return nil, fmt.Errorf("cannot get target intent: unknown type: %T", target)
	}

	if !mc.main.IsManagedNode(nodeID) {

		return nil, nil
	}

	return &bridgev2.MatrixMembershipResult{RedirectTo: meshid.MakeUserID(nodeID)}, nil
}

func (c *MeshtasticClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	c.UpdateLastSeenDate(ctx, msg.Event.Sender)
	return fmt.Errorf("reaction removal is not supported on Meshtastic network")
}

func (c *MeshtasticClient) HandleMatrixTyping(ctx context.Context, msg *bridgev2.MatrixTyping) error {
	if !msg.IsTyping {
		return nil
	}
	channelID, _, _ := meshid.ParsePortalID(msg.Portal.ID)
	log := c.log.With().
		Str("action", "handle_typing").
		Str("portal", channelID).
		Logger()
	log.Debug().Msg("Handling typing change")
	c.UpdateLastSeenDate(ctx, c.UserLogin.UserMXID)
	return nil
}

func (c *MeshtasticClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	channelID, _, _ := meshid.ParsePortalID(msg.Portal.ID)
	log := c.log.With().
		Str("action", "handle_read_receipts").
		Str("portal", channelID).
		Logger()
	log.Debug().Msg("Handling read receipts")
	c.UpdateLastSeenDate(ctx, c.UserLogin.UserMXID)
	return nil
}
