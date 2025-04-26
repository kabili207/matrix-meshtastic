package connector

import (
	"context"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
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
var _ bridgev2.IdentifierResolvingNetworkAPI = (*MeshtasticClient)(nil)

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

func (s *MeshtasticClient) makeDMPortalKey(remote, synth meshid.NodeID) networkid.PortalKey {
	key := networkid.PortalKey{
		ID:       meshid.MakeDMPortalID(remote, synth),
		Receiver: s.UserLogin.ID,
	}
	return key
}

func (s *MeshtasticClient) makeEventSender(sender meshid.NodeID) bridgev2.EventSender {
	meta := s.UserLogin.Metadata.(*UserLoginMetadata)
	return bridgev2.EventSender{
		IsFromMe:    meta.NodeID == sender,
		Sender:      meshid.MakeUserID(sender),
		SenderLogin: meshid.MakeUserLoginID(sender),
	}
}

func (tc *MeshtasticClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	meta := tc.UserLogin.Metadata.(*UserLoginMetadata)
	return meshid.MakeUserID(meta.NodeID) == userID
}

func (tc *MeshtasticClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	remoteNode, synthNode, err := meshid.ParseDMPortalID(portal.ID)
	if err == nil {
		portal.RoomType = database.RoomTypeDM
		return tc.wrapDMInfo(synthNode, remoteNode), nil
	}
	// Other room types handle portal info on join
	return nil, nil
}

func (c *MeshtasticClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return &bridgev2.UserInfo{
		Name: ptr.Ptr(string(ghost.ID)),
	}, nil
}

func (c *MeshtasticClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	remoteNode, err := meshid.ParseNodeID(identifier)
	if err != nil {
		return nil, err
	}
	ghost, err := c.main.bridge.GetGhostByID(ctx, meshid.MakeUserID(remoteNode))
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}
	myNode := meshid.MXIDToNodeID(c.UserLogin.UserMXID)

	return &bridgev2.ResolveIdentifierResponse{
		Ghost:  ghost,
		UserID: meshid.MakeUserID(remoteNode),
		Chat: &bridgev2.CreateChatResponse{
			PortalKey:  c.makeDMPortalKey(remoteNode, myNode),
			PortalInfo: c.wrapDMInfo(myNode, myNode),
		},
	}, nil
}
