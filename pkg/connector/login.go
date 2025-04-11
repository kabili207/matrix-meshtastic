package connector

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	// Added time for createWelcomeRoomAndSendIntro call

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/martinlindhe/crc24"
	"github.com/rs/zerolog" // Added ptr for createWelcomeRoomAndSendIntro call

	// Added mautrix for createWelcomeRoomAndSendIntro call
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	// Added event for createWelcomeRoomAndSendIntro call
	// Added id for createWelcomeRoomAndSendIntro call
)

// Login Flow/Step IDs - Copied from network_connector.go as they are used here
const (
	LoginFlowIDUsernamePassword = "user-pass"
	LoginStepIDUsernamePassword = "user-pass-input"
	LoginStepIDComplete         = "complete"

	LoginInputFieldTypeMqttTopic bridgev2.LoginInputFieldType = "mqtt-topic"

	LoginFieldRootTopic = "root-topic"
)

// SimpleLogin represents an ongoing username/password login attempt.
type MeshtasticLogin struct {
	User *bridgev2.User
	Main *MeshtasticConnector // Needs access to the connector for LoadUserLogin and createWelcomeRoom...
	Log  zerolog.Logger
}

// Ensure SimpleLogin implements the required interface
var _ bridgev2.LoginProcessUserInput = (*MeshtasticLogin)(nil)

// Start implements bridgev2.LoginProcessUserInput
func (sl *MeshtasticLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	sl.Log.Debug().Msg("Starting username/password login flow")
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       LoginStepIDUsernamePassword,
		Instructions: "Enter the root mqtt topic to connect to.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type: LoginInputFieldTypeMqttTopic, // Correct type based on login.go
					ID:   LoginFieldRootTopic,
					Name: "Root topic",
					// TODO: Add Validator function
				},
			},
		},
	}, nil
}

func mxidToNodeId(mxid string) uint32 {
	mxidBytes := []byte(mxid)
	checksum := crc24.ChecksumOpenPGP(mxidBytes)
	return checksum | (0x77 << 24)
}

func nodeIdToMacAddr(nodeId uint32) []byte {
	a := make([]byte, 4)
	binary.BigEndian.PutUint32(a, nodeId)
	return []byte{0xA, 0, a[0], a[1], a[2], a[3]}
}

// SubmitUserInput implements bridgev2.LoginProcessUserInput
func (sl *MeshtasticLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	root_topic := input[LoginFieldRootTopic]

	if root_topic == "" {
		return nil, fmt.Errorf("root topic cannot be empty")
	}

	sl.Log.Info().Str("topic", root_topic).Msg("Received login request for topic")

	// In a real bridge, you would authenticate with the remote network here.
	// Since this is simple, we just generate a unique ID based on the username.
	// We need a stable way to generate the LoginID for a given remote identifier (username).
	// Using a UUID based on a namespace and the username ensures this.
	// IMPORTANT: Do NOT just use the raw username, as it might contain invalid characters for a Matrix Localpart.
	// Also, avoid collisions if usernames are not unique across different contexts (not an issue here).

	mqttClient := sl.Main.MakeMqttClient(root_topic)
	err := mqttClient.Connect()

	if err != nil {
		sl.Log.Err(err).Msg("Failed to log in to MQTT server")
		return nil, fmt.Errorf("failed to log in to MQTT server: %w", err)
	}

	userNodeId := sl.Main.Config.BaseNodeId

	// Correct type is networkid.UserLoginID
	var loginID networkid.UserLoginID = meshid.MakeUserLoginID(root_topic)

	topicParts := strings.Split(root_topic, "/")
	topicFriendly := root_topic

	if len(topicParts) > 1 && topicParts[0] == "msh" {
		topicFriendly = strings.Join(topicParts[1:], " - ")
	}

	// Create the UserLogin entry in the bridge database
	ul, err := sl.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: topicFriendly, // Use the provided username as the display name
		RemoteProfile: status.RemoteProfile{
			Name: root_topic,
			// Add other profile fields if known (e.g., avatar URL)
		},
		Metadata: &UserLoginMetadata{
			ServerNodeId: userNodeId,
			RootTopic:    root_topic,
		},
	}, &bridgev2.NewLoginParams{
		DeleteOnConflict: false, // Or true if you want relogins to replace old ones
	})
	if err != nil {
		sl.Log.Err(err).Msg("Failed to create user login entry")
		return nil, fmt.Errorf("failed to create user login: %w", err)
	}

	sl.Log.Info().Str("login_id", string(ul.ID)).Msg("Successfully 'logged in' and created user login")

	// Load the user login into memory (important!)
	// In a real bridge, this would trigger connecting to the remote network for this user.
	err = sl.Main.LoadUserLogin(ctx, ul) // Calls method on SimpleNetworkConnector
	if err != nil {
		// Log the error, but maybe still return success to the user? Depends on desired UX.
		sl.Log.Err(err).Msg("Failed to load user login after creation (this might indicate an issue)")
		// Optionally delete the login record if loading failed critically:
		// sl.User.DeleteLogin(ctx, ul.ID)
		// return nil, fmt.Errorf("failed to activate user login: %w", err)
	}

	// Run welcome logic *after* the login is fully established and loaded
	// This needs access to the connector instance (sl.Main)
	go sl.Main.TryJoinChannels(ctx, ul)

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       LoginStepIDComplete,
		Instructions: fmt.Sprintf("Successfully connected to '%s'", topicFriendly),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul, // Pass the loaded UserLogin back
		},
	}, nil
}

// Cancel implements bridgev2.LoginProcessUserInput
func (sl *MeshtasticLogin) Cancel() {
	sl.Log.Debug().Msg("Login process cancelled")
	// Add any cleanup logic here if needed (e.g., aborting network connections)
}
