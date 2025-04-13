package connector

import (
	"context"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
)

type MeshtasticClient struct {
	log        zerolog.Logger
	bridge     *bridgev2.Bridge
	main       *MeshtasticConnector
	UserLogin  *bridgev2.UserLogin
	MeshClient *mesh.MeshtasticClient
}

var _ bridgev2.NetworkAPI = (*MeshtasticClient)(nil)

func (mc *MeshtasticClient) Connect(ctx context.Context) {
	err := mc.MeshClient.Connect()
	if err != nil {
		mc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      "meshtastic-api-error",
			Message:    "Failed to connect to server",
			Info: map[string]any{
				"go_error": err.Error(),
			},
		})
		return
	}
	mc.MeshClient.AddEventHandler(mc.handleMeshEvent)
	mc.TryJoinChannels(ctx)
}

func (mc *MeshtasticClient) Disconnect() {
	mc.MeshClient.Disconnect()
}

func (tc *MeshtasticClient) IsLoggedIn() bool {
	return tc.MeshClient.IsConnected()
}

// LogoutRemote is a no-op for this simple connector.
func (nc *MeshtasticClient) LogoutRemote(ctx context.Context) {
	nc.log.Info().Msg("MeshtasticClient LogoutRemote called (no-op)")
}

func (tc *MeshtasticClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {

	return &event.RoomFeatures{
		MaxTextLength:        int(pb.Constants_DATA_PAYLOAD_LEN),
		Edit:                 event.CapLevelRejected,
		Reaction:             event.CapLevelPartialSupport,
		CustomEmojiReactions: false,
		Poll:                 event.CapLevelUnsupported,
		Reply:                event.CapLevelPartialSupport,
		Thread:               event.CapLevelUnsupported,
		LocationMessage:      event.CapLevelPartialSupport,
		Formatting: event.FormattingFeatureMap{
			event.FmtBold:               event.CapLevelUnsupported,
			event.FmtItalic:             event.CapLevelUnsupported,
			event.FmtStrikethrough:      event.CapLevelUnsupported,
			event.FmtInlineCode:         event.CapLevelUnsupported,
			event.FmtCodeBlock:          event.CapLevelUnsupported,
			event.FmtSyntaxHighlighting: event.CapLevelUnsupported,
			event.FmtBlockquote:         event.CapLevelUnsupported,
			event.FmtInlineLink:         event.CapLevelUnsupported,
			event.FmtUserLink:           event.CapLevelUnsupported,
			event.FmtRoomLink:           event.CapLevelUnsupported,
			event.FmtEventLink:          event.CapLevelUnsupported,
			event.FmtAtRoomMention:      event.CapLevelUnsupported,
			event.FmtUnorderedList:      event.CapLevelUnsupported,
			event.FmtOrderedList:        event.CapLevelUnsupported,
			event.FmtListStart:          event.CapLevelUnsupported,
			event.FmtListJumpValue:      event.CapLevelUnsupported,
			event.FmtCustomEmoji:        event.CapLevelUnsupported,
		},
	}
}

func (s *MeshtasticClient) makePortalKey(channelID string, channelKey *string) networkid.PortalKey {
	key := networkid.PortalKey{
		ID: meshid.MakePortalID(channelID, channelKey),
	}
	// For non-group chats, add receiver
	if s.bridge.Config.SplitPortals {
		key.Receiver = s.UserLogin.ID
	}
	return key
}

func (s *MeshtasticClient) makeEventSender(sender meshid.NodeID) bridgev2.EventSender {
	meta := s.UserLogin.Metadata.(*UserLoginMetadata)
	return bridgev2.EventSender{
		IsFromMe:    meta.ServerNodeId == sender,
		SenderLogin: s.UserLogin.ID,
		Sender:      meshid.MakeUserID(sender),
	}
}

func (tc *MeshtasticClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	meta := tc.UserLogin.Metadata.(*UserLoginMetadata)
	return meshid.MakeUserID(meta.ServerNodeId) == userID
}

func (tc *MeshtasticClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {

	return &bridgev2.ChatInfo{
		Members: &bridgev2.ChatMemberList{
			IsFull: true,
			Members: []bridgev2.ChatMember{
				{
					EventSender: bridgev2.EventSender{
						IsFromMe: true,
						Sender:   meshid.MakeUserID(tc.UserLogin.Metadata.(*UserLoginMetadata).ServerNodeId),
					},
					// This could be omitted, but leave it in to be explicit.
					Membership: event.MembershipJoin,
					// Make the user moderator, so they can adjust the room metadata if they want to.
					PowerLevel: ptr.Ptr(50),
				},
				{
					EventSender: bridgev2.EventSender{
						Sender: networkid.UserID(portal.ID),
					},
					Membership: event.MembershipJoin,
					PowerLevel: ptr.Ptr(50),
				},
			},
		},
	}, nil
}

func (c *MeshtasticClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return &bridgev2.UserInfo{
		Name: ptr.Ptr(string(ghost.ID)),
	}, nil
}

func (c *MeshtasticClient) TryJoinChannels(ctx context.Context) {
	portals, _ := c.bridge.GetAllPortalsWithMXID(ctx)

	for _, p := range portals {
		if channelID, channelKey, err := meshid.ParsePortalID(p.ID); err != nil {
			c.MeshClient.AddChannel(channelID, channelKey)
		}
	}
}
