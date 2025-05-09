package main

import (
	// Import the main bridge helper package
	"github.com/kabili207/matrix-meshtastic/pkg/connector"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
	// mxmain handles logging setup internally.
)

// Build time variables (optional but good practice)
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

var m = mxmain.BridgeMain{
	Name:        "matrix-meshtastic",
	Description: "A Matrix-Meshtastic bridge.",
	Version:     "0.1.0",
	URL:         "https://github.com/kabili207/matrix-meshtastic", // Optional: Add your repo URL
	Connector:   &connector.MeshtasticConnector{},
}

func main() {

	// Initialize version info and run the bridge.
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
