package connector

import (
	"context"
	"fmt"

	// Added time for createWelcomeRoomAndSendIntro call

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/kabili207/meshtastic-go/core"
	"github.com/rs/zerolog" // Added ptr for createWelcomeRoomAndSendIntro call

	// Added mautrix for createWelcomeRoomAndSendIntro call
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
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
						if len([]byte(s)) > core.MaxLongName {
							return s, fmt.Errorf("must be at most %d bytes long", core.MaxLongName)
						}
						return s, nil
					},
				},
				{
					Type: bridgev2.LoginInputFieldTypeUsername,
					ID:   LoginFieldShortName,
					Name: "Short name",
					Validate: func(s string) (string, error) {
						if len([]byte(s)) > core.MaxShortName {
							return s, fmt.Errorf("must be at most %d bytes long", core.MaxShortName)
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

	ul, err := sl.Main.createLogin(ctx, sl.User, long_name, short_name)
	if err != nil {
		sl.Log.Err(err).Msg("Failed to create user login entry")
		return nil, fmt.Errorf("failed to create user login: %w", err)
	}
	userNodeId := ul.Metadata.(*meshid.UserLoginMetadata).NodeID

	sl.Log.Info().Str("login_id", string(ul.ID)).Msg("Successfully 'logged in' and created user login")

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

// createLogin creates the mesh identity and the bridge login for a Matrix user.
// Shared by the login flow and the identity migration.
func (c *MeshtasticConnector) createLogin(ctx context.Context, user *bridgev2.User, longName, shortName string) (*bridgev2.UserLogin, error) {
	nodeID := c.NodeIDForMXID(user.MXID)
	if nodeID == 0 {
		return nil, fmt.Errorf("no identity could be derived for %s", user.MXID)
	}
	if err := c.UpdateGhostMeshNames(ctx, meshid.MakeUserID(nodeID), user.MXID, longName, shortName); err != nil {
		return nil, err
	}
	ul, err := user.NewLogin(ctx, &database.UserLogin{
		ID:         meshid.MakeUserLoginID(nodeID),
		RemoteName: shortName,
		RemoteProfile: status.RemoteProfile{
			Name:     longName,
			Username: nodeID.String(),
		},
		Metadata: &meshid.UserLoginMetadata{NodeID: nodeID},
	}, &bridgev2.NewLoginParams{DeleteOnConflict: false})
	if err != nil {
		return nil, err
	}
	// NewLogin loads the login through LoadUserLogin; keep the explicit call so a
	// reused login also gets a fresh client.
	if err := c.LoadUserLogin(ctx, ul); err != nil {
		return nil, err
	}
	return ul, nil
}

// Cancel implements bridgev2.LoginProcessUserInput
func (sl *MeshtasticLogin) Cancel() {
	sl.Log.Debug().Msg("Login process cancelled")
	// Add any cleanup logic here if needed (e.g., aborting network connections)
}
