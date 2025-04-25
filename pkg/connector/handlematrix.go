package connector

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"go.mau.fi/util/ptr"
	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	geoRegex = regexp.MustCompile(`^geo:(-?\d{1,3}\.\d+),(-?\d{1,3}\.\d+).*(?:;u=(\d+(?:\.\d+)?)).*`)
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

	fromNode := c.main.MXIDToNodeId(msg.Event.Sender)
	channelId := c.main.Config.PrimaryChannel.Name
	messIDSender := ""
	targetNode := meshid.BROADCAST_ID

	switch msg.Portal.Portal.RoomType {
	case database.RoomTypeDefault:
		channelId, _, err = meshid.ParsePortalID(msg.Portal.ID)
		messIDSender = channelId
	case database.RoomTypeDM:
		targetNode, _, err = meshid.ParseDMPortalID(msg.Portal.ID)
		messIDSender = targetNode.String()
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

	packetId, err := uint32(0), nil
	switch msg.Content.MsgType {
	case event.MsgText:
		packetId, err = c.MeshClient.SendMessage(fromNode, targetNode, channelId, msg.Content.Body)
	case event.MsgLocation:
		matches := geoRegex.FindStringSubmatch(msg.Content.GeoURI)
		if len(matches) > 0 {
			lat, _ := strconv.ParseFloat(matches[1], 32)
			lon, _ := strconv.ParseFloat(matches[2], 32)
			acc, _ := strconv.ParseFloat(matches[3], 32)
			ts := time.UnixMilli(msg.Event.Timestamp)
			packetId, err = c.MeshClient.SendPosition(fromNode, targetNode, float32(lat), float32(lon), ptr.Ptr(float32(acc)), &ts)
		}
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

			userInfo := &bridgev2.UserInfo{
				Name:         &longName,
				IsBot:        ptr.Ptr(false),
				Identifiers:  []string{},
				ExtraUpdates: bridgev2.MergeExtraUpdaters(c.updateGhostSenderID(mxid), c.updateGhostNames(longName, string(shortName))),
			}
			ghost.UpdateInfo(ctx, userInfo)
			c.MeshClient.SendNodeInfo(c.main.MXIDToNodeId(mxid), meshid.BROADCAST_ID, longName, shortName, false)
		}
	}
}

func (c *MeshtasticClient) UpdateGhostMeshNames(ctx context.Context, userID networkid.UserID, longName, shortName string) error {
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
		ExtraUpdates: bridgev2.MergeExtraUpdaters(c.updateGhostNames(longName, string(shortName))),
	}
	ghost.UpdateInfo(ctx, userInfo)
	nodeID, err := meshid.ParseUserID(userID)
	if err != nil {
		return err
	}
	return c.MeshClient.SendNodeInfo(nodeID, meshid.BROADCAST_ID, longName, shortName, false)
}

func (c *MeshtasticClient) updateGhostSenderID(mxid id.UserID) func(context.Context, *bridgev2.Ghost) bool {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		meta := &GhostMetadata{}
		switch ghost.Metadata.(type) {
		case *GhostMetadata:
			meta = ghost.Metadata.(*GhostMetadata)
		default:
			ghost.Metadata = meta
		}
		newId := mxid.String()
		forceSave := newId != meta.UserMXID
		meta.UserMXID = newId
		meta.IsManaged = true
		return forceSave
	}
}

func (c *MeshtasticClient) updateGhostNames(longName, shortName string) func(context.Context, *bridgev2.Ghost) bool {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		meta, ok := ghost.Metadata.(*GhostMetadata)
		if !ok {
			meta = &GhostMetadata{}
		}
		forceSave := meta.LongName != longName || meta.ShortName != shortName
		meta.LongName = longName
		meta.ShortName = shortName
		return forceSave
	}
}

func (c *MeshtasticClient) updateGhostPublicKey(publicKey []byte) func(context.Context, *bridgev2.Ghost) bool {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		meta, ok := ghost.Metadata.(*GhostMetadata)
		if !ok {
			meta = &GhostMetadata{}
		}
		forceSave := slices.Equal(meta.PublicKey, publicKey)
		meta.PublicKey = publicKey
		return forceSave
	}
}

func (c *MeshtasticClient) updateGhostPrivateKey(publicKey, privateKey []byte) func(context.Context, *bridgev2.Ghost) bool {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		meta, ok := ghost.Metadata.(*GhostMetadata)
		if !ok {
			meta = &GhostMetadata{}
		}
		forceSave := slices.Equal(meta.PrivateKey, privateKey) && slices.Equal(meta.PublicKey, publicKey)
		meta.PrivateKey = privateKey
		meta.PublicKey = publicKey
		return forceSave
	}
}

func (c *MeshtasticClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	fromNode := c.main.MXIDToNodeId(msg.Event.Sender)
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

	channelID := c.main.Config.PrimaryChannel.Name
	targetNode := meshid.BROADCAST_ID

	switch msg.Portal.Portal.RoomType {
	case database.RoomTypeDefault:
		channelID, _, err = meshid.ParsePortalID(msg.Portal.ID)
	case database.RoomTypeDM:
		targetNode, _, err = meshid.ParseDMPortalID(msg.Portal.ID)
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
	_, err = c.MeshClient.SendReaction(fromNode, targetNode, channelID, packetID, pre.Emoji)
	return &database.Reaction{}, err
}

func (c *MeshtasticClient) UpdateLastSeenDate(ctx context.Context, sender id.UserID) {
	// TODO: Cache last seen date in bridge
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
	var ghost *bridgev2.Ghost = nil

	switch target := msg.Target.(type) {
	case *bridgev2.Ghost:
		ghost = target
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

	meta := &GhostMetadata{}
	switch ghost.Metadata.(type) {
	case *GhostMetadata:
		meta = ghost.Metadata.(*GhostMetadata)
	default:
		ghost.Metadata = meta
	}

	if meta.LongName == "" {

		//longName := TruncateString(u.Displayname, 39)
		//senderStr := string(m.SenderID)
		//shortName := senderStr[len(senderStr)-4:]
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
