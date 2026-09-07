package connector

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/kabili207/meshtastic-go/core"
	meshevent "github.com/kabili207/meshtastic-go/device/event"
	"github.com/kabili207/meshtastic-go/device/node"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	// The Meshtastic apps write mentions as "@!<node id>". The bare four-hex short
	// form is the bridge's older convention, kept so existing habits still resolve.
	mentionRegex = regexp.MustCompile(`@!([0-9a-fA-F]{8})\b|(?:\b|@\s?)([a-f0-9]{4})\b`)
)

// Handles events emitted by the mesh bridge.
func (c *MeshtasticConnector) handleGlobalMeshEvent(rawEvt any) {

	switch evt := rawEvt.(type) {
	case *meshevent.NodeInfoUpdated:
		c.handleMeshNodeInfo(evt)
	case *meshevent.MapReportReceived:
		c.handleMapReport(evt)
	case *meshevent.PositionUpdated:
		c.handleMeshLocation(evt)
	case *meshevent.WaypointReceived:
		c.handleMeshWaypoint(evt)
	case *meshevent.TracerouteReceived:
		// Traceroute requests are answered inside the library; only responses
		// reach here.
		if !evt.IsRequest {
			c.handleMeshTraceroute(evt)
		}
	case *meshevent.TextMessage:
		c.dispatchTextMessage(evt)
	case *meshevent.PacketReceived:
		c.handleUnknownPacket(&evt.Event)
	}
}

// dispatchTextMessage routes a text message or reaction to every logged-in user
// so each can decide whether it belongs to one of its portals.
func (c *MeshtasticConnector) dispatchTextMessage(evt *meshevent.TextMessage) {
	for _, login := range c.allLoginClients() {
		if evt.Emoji != 0 {
			login.handleMeshReaction(evt)
		} else {
			login.handleMeshMessage(evt)
		}
	}
}

// allLoginClients returns the MeshtasticClient for every active user login.
func (c *MeshtasticConnector) allLoginClients() []*MeshtasticClient {
	var clients []*MeshtasticClient
	for _, login := range c.bridge.GetAllCachedUserLogins() {
		if mc, ok := login.Client.(*MeshtasticClient); ok && mc != nil {
			clients = append(clients, mc)
		}
	}
	return clients
}

func (c *MeshtasticConnector) handleUnknownPacket(evt *meshevent.Event) {
	from := meshid.FromCore(evt.From)
	c.getRemoteGhost(context.Background(), meshid.MakeUserID(from), true)
	c.meshDB.MeshNodeInfo.SetLastSeen(context.Background(), from, evt.IsNeighbor)
}

func (c *MeshtasticConnector) handleMeshLocation(evt *meshevent.PositionUpdated) {
	from := meshid.FromCore(evt.From)
	lat := float32(0)
	lon := float32(0)
	if evt.Position != nil {
		if evt.Position.LatitudeI != nil {
			lat = float32(*evt.Position.LatitudeI) * 1e-7
		}
		if evt.Position.LongitudeI != nil {
			lon = float32(*evt.Position.LongitudeI) * 1e-7
		}
	}
	log := c.log.With().
		Str("action", "location_update").
		Stringer("node_id", from).
		Logger()
	log.Info().
		Float32("latitude", lat).
		Float32("longitude", lon).
		Msg("Location update received")

	c.getRemoteGhost(context.Background(), meshid.MakeUserID(from), true)
	c.meshDB.MeshNodeInfo.SetLastSeen(context.Background(), from, evt.IsNeighbor)
}

func (c *MeshtasticClient) joinChannel(channelName string, channelKey string) error {

	ctx := context.Background()
	login := c.UserLogin
	user := login.User
	log := c.log.With().Str("user_mxid", string(user.MXID)).Str("login_id", string(login.ID)).Logger()
	ctx = log.WithContext(ctx)

	portalKey := c.makePortalKey(channelName, &channelKey)

	portal, err := c.bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		log.Err(err).Str("portal_key", string(portalKey.ID)).Msg("Failed to get portal")
		return err
	}

	log.Info().Str("portal_id", string(portal.ID)).Msg("Successfully retrieved portal")

	chatInfo := c.wrapChatInfo(user, channelName, channelKey)
	// Create the room using portal
	return portal.CreateMatrixRoom(ctx, user.GetDefaultLogin(), chatInfo)

}

func (c *MeshtasticClient) handleMeshMessage(evt *meshevent.TextMessage) {
	from := meshid.FromCore(evt.From)
	to := meshid.FromCore(evt.To)
	meta, ok := c.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != to) {
		return
	}

	ctx := context.Background()

	c.main.getRemoteGhost(ctx, meshid.MakeUserID(from), true)

	var portalKey networkid.PortalKey
	var messIDSender = ""

	roomType := database.RoomTypeDefault
	if evt.IsDM {
		portalKey = c.makeDMPortalKey(from, to)
		messIDSender = from.String()
		roomType = database.RoomTypeDM
		if evt.WantAck {
			c.main.meshBridge.SendAckAs(ctx, to.Core(), from.Core(), evt.PacketID)
		}
	} else {
		portalKey = c.makePortalKey(evt.ChannelName, evt.ChannelKey)
		messIDSender = evt.ChannelName
		if evt.WantAck {
			c.main.meshBridge.SendAckAs(ctx, c.main.GetBaseNodeID().Core(), from.Core(), evt.PacketID)
		}
	}

	if !evt.IsDM {
		logins, err := c.bridge.GetUserLoginsInPortal(ctx, portalKey)
		if err != nil {
			return
		}
		found := false
		for _, l := range logins {
			if l.ID == c.UserLogin.ID {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}

	mess := simplevent.Message[*meshevent.TextMessage]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", from)
				c = c.Time("message_ts", evt.Timestamp)
				return c
			},
			PortalKey:    portalKey,
			CreatePortal: true,
			Sender:       c.makeEventSender(from),
			Timestamp:    evt.Timestamp,
			PreHandleFunc: func(ctx context.Context, p *bridgev2.Portal) {
				p.RoomType = roomType
			},
		},
		Data:               evt,
		ID:                 meshid.MakeMessageID(messIDSender, evt.PacketID),
		ConvertMessageFunc: c.convertMessageEvent,
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
	c.main.meshDB.MeshNodeInfo.SetLastSeen(ctx, from, evt.IsNeighbor)
}

func (c *MeshtasticClient) convertMessageEvent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *meshevent.TextMessage) (*bridgev2.ConvertedMessage, error) {
	mess := data.Message
	formatted := ""
	mentions := &event.Mentions{}
	seenUsers := make(map[id.UserID]struct{})

	matches := mentionRegex.FindAllStringSubmatchIndex(mess, -1)
	if matches != nil {
		var bodyBuilder, formattedBuilder strings.Builder
		lastIndex := 0

		for _, match := range matches {
			start, end := match[0], match[1] // full match

			bodyBuilder.WriteString(mess[lastIndex:start])
			formattedBuilder.WriteString(data.Message[lastIndex:start])

			var ni *meshdb.MeshNodeInfo
			if match[2] >= 0 {
				if nodeID, err := meshid.ParseNodeID("!" + strings.ToLower(mess[match[2]:match[3]])); err == nil {
					ni, _ = c.main.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
				}
			} else {
				ni, _ = c.main.meshDB.MeshNodeInfo.GetByShortUserID(ctx, mess[match[4]:match[5]])
			}
			if ni != nil {
				nodeUserID := meshid.MakeUserID(ni.NodeID)
				ghost, err := c.main.getRemoteGhost(ctx, nodeUserID, true)
				if err != nil {
					c.log.Err(err).Msg("unable to fetch remote node info")
					bodyBuilder.WriteString(mess[start:end])
					formattedBuilder.WriteString(data.Message[start:end])
					lastIndex = end
					continue
				}

				ghostMXID := ghost.Intent.GetMXID()
				if gm, ok := ghost.Metadata.(*meshid.GhostMetadata); ok && gm.UserMXID != "" {
					ghostMXID = gm.UserMXID
				}
				userTag := ghostMXID.String()
				userDisplay := userTag
				if ni.LongName != "" {
					userDisplay = fmt.Sprintf("@%s", ni.LongName)
				}

				bodyBuilder.WriteString(userTag)
				formattedBuilder.WriteString(fmt.Sprintf(
					`<a href="%s">%s</a>`,
					ghostMXID.URI().MatrixToURL(),
					html.EscapeString(userDisplay),
				))

				if _, seen := seenUsers[ghostMXID]; !seen {
					seenUsers[ghostMXID] = struct{}{}
					mentions.UserIDs = append(mentions.UserIDs, ghostMXID)
				}
			} else {
				bodyBuilder.WriteString(mess[start:end])
				formattedBuilder.WriteString(data.Message[start:end])
			}

			lastIndex = end
		}

		// copy the remainder
		bodyBuilder.WriteString(mess[lastIndex:])
		formattedBuilder.WriteString(data.Message[lastIndex:])

		mess = bodyBuilder.String()
		formatted = formattedBuilder.String()
	}

	if strings.Contains(mess, meshid.BellCharacter) {
		if portal.RoomType == database.RoomTypeDM {
			user := c.bridge.GetCachedUserLoginByID(portal.Receiver)
			userTag := user.UserMXID.String()

			mess = strings.ReplaceAll(mess, meshid.BellCharacter, userTag)
			formatted = strings.ReplaceAll(data.Message, meshid.BellCharacter, fmt.Sprintf(`<a href="%s">%s</a>`, user.UserMXID.URI().MatrixToURL(), html.EscapeString(userTag)))
			mentions.UserIDs = append(mentions.UserIDs, user.UserMXID)
		} else {
			mess = strings.ReplaceAll(mess, meshid.BellCharacter, portal.MXID.String())
			formatted = strings.ReplaceAll(data.Message, meshid.BellCharacter, fmt.Sprintf(`<a href="%s">%s</a>`, portal.MXID.URI().MatrixToURL(), html.EscapeString("@room")))
			mentions.Room = true
		}
	}

	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          mess,
		FormattedBody: formatted,
		Mentions:      mentions,
	}
	if formatted != "" {
		content.Format = event.FormatHTML
	}
	m := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: content,
		}},
	}
	if data.ReplyID != 0 {
		var messIDSender string
		if data.IsDM {
			messIDSender = meshid.FromCore(data.From).String()
		} else {
			messIDSender = data.ChannelName
		}
		m.ReplyTo = &networkid.MessageOptionalPartID{
			MessageID: meshid.MakeMessageID(messIDSender, data.ReplyID),
		}
	}
	return m, nil
}

func (c *MeshtasticConnector) getRemoteGhost(ctx context.Context, ghostID networkid.UserID, requestInfoIfNew bool) (*bridgev2.Ghost, error) {
	if !requestInfoIfNew {
		return c.bridge.GetGhostByID(ctx, ghostID)
	}
	ghost, err := c.bridge.GetExistingGhostByID(ctx, ghostID)
	if ghost != nil || err != nil {
		return ghost, err
	}
	go c.requestGhostNodeInfo(ghostID)
	return c.bridge.GetGhostByID(ctx, ghostID)
}

func (c *MeshtasticConnector) requestGhostNodeInfo(ghostID networkid.UserID) {
	log := c.log.With().
		Str("action", "request_nodeinfo").
		Str("ghost_id", string(ghostID)).
		Logger()
	nodeId, err := meshid.ParseUserID(ghostID)
	if err != nil {
		log.Err(err).
			Msg("unable to request node info")
		return
	}

	_, err = c.meshBridge.SendNodeInfoAs(context.Background(), c.GetBaseNodeID().Core(), nodeId.Core(), node.WithWantResponse())
	if err != nil {
		log.Err(err).
			Msg("unable to request node info")
		return
	}
	log.Debug().Msg("Sent request for node info")
}

func (c *MeshtasticConnector) handleMeshNodeInfo(evt *meshevent.NodeInfoUpdated) {
	from := meshid.FromCore(evt.From)
	to := meshid.FromCore(evt.To)
	user := evt.User
	if user == nil {
		return
	}
	log := c.log.With().
		Str("action", "handle_mesh_nodeinfo").
		Stringer("from_node_id", from).
		Stringer("to_node_id", to).
		Logger()
	ctx := log.WithContext(context.Background())
	ghost, err := c.getRemoteGhost(ctx, meshid.MakeUserID(from), to != c.GetBaseNodeID())
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
		return
	}

	// NodeInfo requests are answered by the bridge node itself, throttled per identity.

	mn, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, from)
	if mn == nil || err != nil {
		mn = c.meshDB.MeshNodeInfo.New()
		mn.NodeID = from
	}
	needUpdate := false
	if mn.UserID != user.Id || mn.LongName != user.LongName || mn.ShortName != user.ShortName || !slices.Equal(mn.PublicKey, user.PublicKey) {
		needUpdate = true
		mn.UserID = user.Id
		mn.LongName = user.LongName
		mn.ShortName = user.ShortName
		mn.PublicKey = user.PublicKey
	}
	mn.Role = user.Role.String()
	mn.IsDirect = evt.IsNeighbor
	// Nil for PKI packets, which do not arrive on a channel.
	if evt.Channel != nil {
		mn.Channel = evt.Channel.GetName()
		mn.ChannelKey = evt.Channel.GetKeyString()
	}
	mn.IsLicensed = user.IsLicensed
	mn.IsUnmessagable = core.IsUnmessageable(user)
	mn.LastSeen = ptr.Ptr(evt.Timestamp)
	err = mn.SetAll(ctx)
	if err != nil {
		log.
			Err(err).
			Str("long_name", user.LongName).
			Str("short_name", user.ShortName).
			Msg("Failed to update node db")
	}

	if user.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:        &user.LongName,
		IsBot:       ptr.Ptr(false),
		Identifiers: []string{},
	}
	ghost.UpdateInfo(ctx, userInfo)
	if needUpdate {
		c.updateDMPortalInfo(ctx, ghost)
	}
	log.
		Debug().
		Str("long_name", user.LongName).
		Str("short_name", user.ShortName).
		Msg("Updated ghost info")
}

func (c *MeshtasticConnector) handleMapReport(evt *meshevent.MapReportReceived) {
	from := meshid.FromCore(evt.From)
	to := meshid.FromCore(evt.To)
	mr := evt.MapReport
	if mr == nil {
		return
	}
	roleStr := mr.Role.String()
	log := c.log.With().
		Str("action", "handle_mesh_map_report").
		Stringer("from_node_id", from).
		Stringer("to_node_id", to).
		Logger()
	ctx := log.WithContext(context.Background())
	ghost, err := c.getRemoteGhost(ctx, meshid.MakeUserID(from), to != c.GetBaseNodeID())
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
		return
	}

	if !evt.To.IsBroadcast() && evt.WantResponse {
		c.sendNodeInfo(to, from, false)
	}

	mn, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, from)
	if mn == nil || err != nil {
		mn = c.meshDB.MeshNodeInfo.New()
		mn.NodeID = from
	}

	needUpdate := false
	if mn.UserID == "" {
		mn.UserID = from.String()
	}

	if mn.LongName != mr.LongName || mn.ShortName != mr.ShortName {
		mn.LongName = mr.LongName
		mn.ShortName = mr.ShortName
		needUpdate = true
	}
	if mn.Role != roleStr {
		// MapReports don't actually contain the IsMessageable flag, however the firmware
		// forcibly updates this flag when changing to a different role, so it is relatively
		// safe for us to do the same if we haven't received a NodeInfo packet yet
		mn.IsUnmessagable = core.IsUnmessageableRole(mr.Role)
	}
	mn.Role = roleStr
	mn.IsDirect = evt.IsNeighbor

	mn.LastSeen = ptr.Ptr(evt.Timestamp)
	err = mn.SetAll(ctx)
	if err != nil {
		log.
			Err(err).
			Str("long_name", mr.LongName).
			Str("short_name", mr.ShortName).
			Msg("Failed to update node db")
	}

	if mr.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:        &mr.LongName,
		IsBot:       ptr.Ptr(false),
		Identifiers: []string{},
	}
	ghost.UpdateInfo(ctx, userInfo)
	if needUpdate {
		c.updateDMPortalInfo(ctx, ghost)
	}
	log.
		Debug().
		Str("long_name", mr.LongName).
		Str("short_name", mr.ShortName).
		Msg("Updated ghost info")
}

func (c *MeshtasticConnector) sendNodeInfo(fromNode, toNode meshid.NodeID, wantResponse bool) {
	log := c.log.With().
		Str("action", "send_nodeinfo").
		Stringer("from_node_id", fromNode).
		Stringer("to_node_id", toNode).
		Logger()

	if !c.IsManagedNode(fromNode) {
		// We have no authority over this node
		return
	}

	ctx := log.WithContext(context.Background())

	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, fromNode)
	if err != nil {
		log.Err(err).Msg("Failed to query node info")
		return
	}

	notifyUser := nodeInfo == nil && fromNode != c.GetBaseNodeID()

	var opts []node.SendOption
	if wantResponse {
		opts = append(opts, node.WithWantResponse())
	}
	if _, err := c.meshBridge.SendNodeInfoAs(ctx, fromNode.Core(), toNode.Core(), opts...); err != nil {
		log.Err(err).Msg("Failed to send node info")
		return
	}

	if notifyUser {
		log.Warn().Msg("User does not have their node names configured")
		// TODO: Send a notice in the portal informing the user how to set their mesh names
	}
}

func (c *MeshtasticClient) handleMeshReaction(evt *meshevent.TextMessage) {
	from := meshid.FromCore(evt.From)
	to := meshid.FromCore(evt.To)
	meta, ok := c.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != to) {
		return
	}

	c.main.getRemoteGhost(context.Background(), meshid.MakeUserID(from), true)

	var portalKey networkid.PortalKey
	var messIDSender = ""

	if evt.IsDM {
		portalKey = c.makeDMPortalKey(from, to)
		messIDSender = from.String()
		if evt.WantAck {
			c.main.meshBridge.SendAckAs(context.Background(), to.Core(), from.Core(), evt.PacketID)
		}
	} else {
		portalKey = c.makePortalKey(evt.ChannelName, evt.ChannelKey)
		messIDSender = evt.ChannelName
		if evt.WantAck {
			c.main.meshBridge.SendAckAs(context.Background(), c.main.GetBaseNodeID().Core(), from.Core(), evt.PacketID)
		}
	}

	if !evt.IsDM {
		logins, err := c.bridge.GetUserLoginsInPortal(context.Background(), portalKey)
		if err != nil {
			return
		}
		found := false
		for _, l := range logins {
			if l.ID == c.UserLogin.ID {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}

	emoji := evt.Message
	mess := simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventReaction,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", from)
				c = c.Time("message_ts", evt.Timestamp)
				return c
			},
			PortalKey:    portalKey,
			CreatePortal: false,
			Sender:       c.makeEventSender(from),
			Timestamp:    evt.Timestamp,
		},
		EmojiID:       networkid.EmojiID(emoji),
		Emoji:         emoji,
		TargetMessage: meshid.MakeMessageID(messIDSender, evt.ReplyID),
	}

	c.main.meshDB.MeshNodeInfo.SetLastSeen(context.Background(), from, evt.IsNeighbor)

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
}

func (c *MeshtasticConnector) handleMeshWaypoint(evt *meshevent.WaypointReceived) {
	from := meshid.FromCore(evt.From)
	wp := evt.Waypoint
	if wp == nil {
		return
	}

	lat := float32(0)
	lon := float32(0)
	if wp.LatitudeI != nil {
		lat = float32(*wp.LatitudeI) * 1e-7
	}
	if wp.LongitudeI != nil {
		lon = float32(*wp.LongitudeI) * 1e-7
	}
	icon := string(rune(wp.Icon))
	expires := time.Unix(int64(wp.Expire), 0)
	var lockedTo *meshid.NodeID
	if wp.LockedTo != 0 && wp.LockedTo != uint32(meshid.BROADCAST_ID) && wp.LockedTo != uint32(meshid.BROADCAST_ID_NO_LORA) {
		lockedTo = ptr.Ptr(meshid.NodeID(wp.LockedTo))
	}

	log := c.log.With().
		Str("action", "waypoint_update").
		Stringer("node_id", from).
		Logger()
	log.Info().
		Float32("latitude", lat).
		Float32("longitude", lon).
		Str("name", wp.Name).
		Str("description", wp.Description).
		Str("icon", icon).
		Msg("Waypoint received")

	ctx := context.Background()
	c.meshDB.MeshNodeInfo.SetLastSeen(ctx, from, evt.IsNeighbor)
	if evt.IsDelete {
		if err := c.meshDB.Waypoint.DeleteByID(ctx, wp.Id); err != nil {
			log.Err(err).Msg("Error deleting waypoint")
		}
	} else if waypoint, err := c.meshDB.Waypoint.GetByWaypointID(ctx, wp.Id); err != nil {
		log.Err(err).Msg("Error checking for existing waypoint")
	} else if waypoint != nil && waypoint.LockedTo != nil && *waypoint.LockedTo != from {
		log.Err(err).Msgf("This waypoint is locked to another node")
	} else {
		if waypoint == nil {
			waypoint = c.meshDB.Waypoint.New()
			waypoint.WaypointID = wp.Id
		}
		waypoint.Name = wp.Name
		waypoint.Description = wp.Description
		waypoint.Icon = icon
		waypoint.Expires = &expires
		waypoint.Latitude = lat
		waypoint.Longitude = lon
		waypoint.UpdatedBy = from
		waypoint.UpdatedDate = ptr.Ptr(evt.Timestamp)
		waypoint.LockedTo = lockedTo
		if err := waypoint.SetAll(ctx); err != nil {
			log.Err(err).Msg("Error saving waypoint")
		}
	}
	// TODO: Possibly drop an m.location event in associated channel?
}
