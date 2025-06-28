package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
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

	fromNode := meshid.MXIDToNodeID(msg.Event.Sender)
	channel := c.main.meshClient.GetPrimaryChannel()
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
		replyID := uint32(0)
		if msg.ReplyTo != nil {
			_, replyID, _ = meshid.ParseMessageID(msg.ReplyTo.ID)
		}
		packetId, err = c.MeshClient.SendMessage(fromNode, targetNode, channel, content, replyID, usePKI)
	case event.MsgLocation:
		geouri, err = meshid.ParseGeoURI(msg.Content.GeoURI)
		if err != nil {
			return nil, bridgev2.WrapErrorInStatus(err).WithErrorAsMessage().WithIsCertain(true).WithSendNotice(true)
		}
		ts := time.UnixMilli(msg.Event.Timestamp)
		packetId, err = c.MeshClient.SendPosition(fromNode, targetNode, *geouri, &ts)

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
			log.Err(err).Str("user_mxid", string(mxid)).Msg("Failed to get user object, ignoring message")
			// ignoring this because we only reply to user messages
			return
		}

		if !ghost.NameSet && u.Displayname != "" {
			longName := TruncateString(u.Displayname, 39)
			senderStr := string(m.SenderID)
			shortName := senderStr[len(senderStr)-4:]
			c.main.UpdateGhostMeshNames(ctx, m.SenderID, mxid, longName, shortName)
		}
	}
}

func (c *MeshtasticConnector) UpdateGhostMeshNames(ctx context.Context, userID networkid.UserID, mxid id.UserID, longName, shortName string) error {
	if len([]byte(longName)) > 39 {
		return errors.New("long name must be less than 40 bytes")
	} else if len([]byte(shortName)) > 4 {
		return errors.New("short name must be less than 5 bytes")
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
		c.log.Debug().Msg("Generating new keypair")
		pub, priv, err := c.meshClient.GenerateKeyPair()
		if err != nil {
			return err
		}
		nodeInfo.PublicKey = pub
		nodeInfo.PrivateKey = priv
	}
	nodeInfo.LongName = longName
	nodeInfo.ShortName = longName
	nodeInfo.SetAll(ctx)

	return c.meshClient.SendNodeInfo(nodeID, meshid.BROADCAST_ID, longName, shortName, false, nodeInfo.PublicKey)
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
		newId := mxid.String()
		forceSave := newId != meta.UserMXID
		meta.UserMXID = newId
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
	if nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID); err != nil {
		return nil, err
	} else if len(nodeInfo.PublicKey) > 0 {
		return nodeInfo.PublicKey, nil
	} else if nodeInfo.IsManaged {
		c.log.Debug().Stringer("node_id", nodeID).Msg("Generating new keypair")
		pub, priv, err := c.meshClient.GenerateKeyPair()
		if err != nil {
			return nil, err
		}
		nodeInfo.PublicKey = pub
		nodeInfo.PrivateKey = priv
		err = nodeInfo.SetAll(ctx)
		if err != nil {
			return nil, err
		}
		return pub, nil
	}
	return nil, errors.New("no public key found")
}

func (c *MeshtasticConnector) getGhostPrivateKey(ctx context.Context, nodeID meshid.NodeID) ([]byte, error) {
	if nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID); err != nil {
		return nil, err
	} else if len(nodeInfo.PrivateKey) > 0 {
		return nodeInfo.PrivateKey, nil
	} else if nodeInfo.IsManaged {
		c.log.Debug().Stringer("node_id", nodeID).Msg("Generating new keypair")
		pub, priv, err := c.meshClient.GenerateKeyPair()
		if err != nil {
			return nil, err
		}
		nodeInfo.PublicKey = pub
		nodeInfo.PrivateKey = priv
		err = nodeInfo.SetAll(ctx)
		if err != nil {
			return nil, err
		}
		return pub, nil
	}
	return nil, errors.New("no private key found")
}

func (c *MeshtasticClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	fromNode := meshid.MXIDToNodeID(msg.Event.Sender)
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

	channel := c.main.meshClient.GetPrimaryChannel()
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
	_, err = c.MeshClient.SendReaction(fromNode, targetNode, channel, packetID, pre.Emoji, usePKI)
	return &database.Reaction{}, err
}

func (c *MeshtasticClient) UpdateLastSeenDate(ctx context.Context, sender id.UserID) {
	if c.bridge.IsGhostMXID(sender) {
		return
	}
	nodeID := meshid.MXIDToNodeID(sender)
	uid := meshid.MakeUserID(nodeID)
	_, err := c.bridge.GetGhostByID(ctx, uid)
	if err != nil {
		return
	}
	c.main.meshDB.MeshNodeInfo.SetLastSeen(ctx, nodeID, true)
}

func (mc *MeshtasticClient) HandleMatrixMembership(ctx context.Context, msg *bridgev2.MatrixMembershipChange) (bool, error) {
	log := mc.log.With().
		Str("action", "handle_membership_change").
		Str("portal", string(msg.Portal.ID)).
		Logger()
	log.Debug().Msg("Handling membership change")

	if msg.Type != bridgev2.Join {
		return false, nil
	}

	var err error
	var nodeID meshid.NodeID = 0

	switch target := msg.Target.(type) {
	case *bridgev2.Ghost:
		_ = target
		nodeID, err = meshid.ParseUserID(target.ID)
		if err != nil {
			return false, fmt.Errorf("failed to parse node ID for user: %w", err)
		}
	case *bridgev2.UserLogin:
		ghost, err := target.Bridge.GetGhostByID(ctx, networkid.UserID(target.ID))
		if err != nil {
			return false, fmt.Errorf("failed to get ghost for user: %w", err)
		}
		nodeID, err = meshid.ParseUserID(ghost.ID)
		if err != nil {
			return false, fmt.Errorf("failed to parse node ID for user: %w", err)
		}
	default:
		return false, fmt.Errorf("cannot get target intent: unknown type: %T", target)
	}

	if !mc.main.IsManagedNode(nodeID) {
		return false, nil
	}

	return true, nil
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
