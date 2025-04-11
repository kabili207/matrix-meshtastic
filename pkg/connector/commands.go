package connector

import (
	"strings"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2/commands"
)

var (
	HelpSectionChannels = commands.HelpSection{Name: "Channel Management", Order: 25}
	HelpSectionNode     = commands.HelpSection{Name: "Node Management", Order: 26}
)

var cmdJoinChannel = &commands.FullHandler{
	Func: fnJoinChannel,
	Name: "join-channel",
	Help: commands.HelpMeta{
		Section:     HelpSectionChannels,
		Description: "Joins a Meshtastic channel with the supplied encryption key",
	},
	RequiresLogin:  true,
	RequiresPortal: false,
}

var cmdUpdateNames = &commands.FullHandler{
	Func: fnUpdateNames,
	Name: "update-names",
	Help: commands.HelpMeta{
		Section:     HelpSectionNode,
		Description: "Updates the long and short names shown to Meshtastic users",
	},
	RequiresLogin:  false,
	RequiresPortal: false,
}

func fnJoinChannel(ce *commands.Event) {

	if len(ce.Args) != 2 {
		ce.Reply("**Usage:** `join-channel <channel name> <channel key>`")
		return
	}

	name := ce.Args[0]
	key := ce.Args[1]

	_, _ = name, key

	if login := ce.User.GetDefaultLogin(); login == nil {
		ce.Reply("Login not found")
	} else if !login.Client.IsLoggedIn() {
		ce.Reply("Not logged in")
	} else if err := login.Client.(*MeshtasticClient).MeshClient.AddChannel(name, key); err != nil {
		ce.Log.Err(err).Msg("Failed to join channel")
		ce.Reply("Failed to join channel: %v", err)
	} else {
		ce.Reply("Successfully joined channel, the portal should be created momentarily")
	}
}

func fnUpdateNames(ce *commands.Event) {

	if len(ce.Args) < 2 {
		ce.Reply("**Usage:** `update-names <short name> <long name>`")
		return
	}

	shortName := ce.Args[0]
	longName := strings.Join(ce.Args[1:], " ")

	_, _ = longName, shortName

	userMXID := ce.User.MXID
	nodeID := ce.Bridge.Network.(*MeshtasticConnector).MXIDToNodeId(userMXID)

	if len([]byte(longName)) > 39 {
		ce.Reply("Long name must be less than 40 bytes")
	} else if len([]byte(shortName)) > 4 {
		ce.Reply("Short name must be less than 5 bytes")
	} else if logins, err := ce.Bridge.GetUserLoginsInPortal(ce.Ctx, ce.Portal.PortalKey); err != nil {
		ce.Log.Err(err).Msg("Failed to find login for this room")
		ce.Reply("Failed to update names: %v", err)
	} else if len(logins) == 0 {
		ce.Log.Error().Msg("Failed to find login for this room")
		ce.Reply("Failed to update names: no available logins")
	} else if err = logins[0].Client.(*MeshtasticClient).
		UpdateGhostMeshNames(ce.Ctx, meshid.MakeUserID(nodeID), longName, shortName); err != nil {
		ce.Log.Err(err).Msg("Update user names")
		ce.Reply("Failed to join channel: %v", err)

	} else {
		ce.Reply("Successfully updated Meshtastic names")
	}
}
