package connector

import (
	"context"
	"errors"
	"fmt"

	// Added time for createWelcomeRoomAndSendIntro call

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
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
	LoginFieldLongName  = "long-name"
	LoginFieldShortName = "short-name"
)

// MeshtasticLogin represents an ongoing username/password login attempt.
type MeshtasticLogin struct {
	User *bridgev2.User
	Main *MeshtasticConnector // Needs access to the connector for LoadUserLogin
	Log  zerolog.Logger
}

// Ensure MeshtasticLogin implements the required interface
var _ bridgev2.LoginProcessUserInput = (*MeshtasticLogin)(nil)

// GetLoginFlows implements bridgev2.NetworkConnector
func (c *MeshtasticConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		ID:          LoginFlowIDUsernamePassword,
		Name:        "Mesh Device",
		Description: "Login as a mesh device.",
	}}
}

// CreateLogin implements bridgev2.NetworkConnector
func (c *MeshtasticConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != LoginFlowIDUsernamePassword {
		return nil, fmt.Errorf("unsupported login flow ID: %s", flowID)
	}
	// Now returns SimpleLogin defined in login.go
	return &MeshtasticLogin{
		User: user,
		Main: c, // Pass the connector instance
		Log:  user.Log.With().Str("action", "login").Str("flow", flowID).Logger(),
	}, nil
}

// Start implements bridgev2.LoginProcessUserInput
func (sl *MeshtasticLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	sl.Log.Debug().Msg("Starting username/password login flow")
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       LoginStepIDUsernamePassword,
		Instructions: "Enter the long and short names to use on Meshtastic",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type: bridgev2.LoginInputFieldTypeUsername,
					ID:   LoginFieldLongName,
					Name: "Long name",
					Validate: func(s string) (string, error) {
						if len([]byte(s)) > 39 {
							return s, errors.New("must be less than 40 bytes long")
						}
						return s, nil
					},
				},
				{
					Type: bridgev2.LoginInputFieldTypeUsername,
					ID:   LoginFieldShortName,
					Name: "Short name",
					Validate: func(s string) (string, error) {
						if len([]byte(s)) > 4 {
							return s, errors.New("must be less than 5 bytes long")
						}
						return s, nil
					},
				},
			},
		},
	}, nil
}

// SubmitUserInput implements bridgev2.LoginProcessUserInput
func (sl *MeshtasticLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	long_name := input[LoginFieldLongName]
	short_name := input[LoginFieldShortName]

	if long_name == "" || short_name == "" {
		return nil, fmt.Errorf("long and short names are required")
	}

	userNodeId := meshid.MXIDToNodeID(sl.User.MXID)

	// Correct type is networkid.UserLoginID
	var loginID networkid.UserLoginID = meshid.MakeUserLoginID(userNodeId)

	// Create the UserLogin entry in the bridge database
	ul, err := sl.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: short_name,
		RemoteProfile: status.RemoteProfile{
			Name:     long_name,
			Username: userNodeId.String(),
		},
		Metadata: &UserLoginMetadata{
			NodeID: userNodeId,
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
	}

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       LoginStepIDComplete,
		Instructions: fmt.Sprintf("Successfully logged in as %s. Please call the join-channel command to join a mesh channel", userNodeId),
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
