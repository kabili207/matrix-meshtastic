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
		IsFromMe:    meta.NodeID == sender,
		SenderLogin: s.UserLogin.ID,
		Sender:      meshid.MakeUserID(sender),
	}
}

func (tc *MeshtasticClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	meta := tc.UserLogin.Metadata.(*UserLoginMetadata)
	return meshid.MakeUserID(meta.NodeID) == userID
}

func (tc *MeshtasticClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return nil, nil
}

func (c *MeshtasticClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return &bridgev2.UserInfo{
		Name: ptr.Ptr(string(ghost.ID)),
	}, nil
}

func (c *MeshtasticClient) TryJoinChannels(ctx context.Context) {
	portals, _ := c.bridge.GetAllPortalsWithMXID(ctx)

	for _, p := range portals {
		if channelID, channelKey, err := meshid.ParsePortalID(p.ID); err == nil {
			c.MeshClient.AddChannel(channelID, channelKey)
		}
	}
}
