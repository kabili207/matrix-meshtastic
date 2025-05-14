# Features & roadmap
* Matrix → Meshtastic
  * [x] Message content
    * [x] Plain text
    * [ ] ~~Formatted messages~~ (not supported by Meshtastic)
    * [ ] ~~Media/files~~ (not supported by Meshtastic)
    * [x] Location messages (sent as location update to primary channel)
  * [x] Reactions
  * [ ] Initial room metadata
* Meshtastic → Matrix
  * [x] Message content
    * [x] Plain text
    * [ ] ~~Formatted messages~~ (not supported by Meshtastic)
    * [ ] ~~Media/files~~ (not supported by Meshtastic)
    * [ ] Location messages (processed but ignored for now)
    * [ ] Waypoint messages
  * [x] Chat types
    * [x] Channels
    * [x] Direct Messages
  * [x] Reactions
  * [ ] ~~Group metadata changes~~ (not applicable)
  * [x] User metadata changes
    * [x] Long Name
    * [x] Short Name
* Misc
  * [x] Automatic portal creation
    * [x] When receiving message
  * [x] Private chat creation by inviting Matrix puppet of Meshtastic user to new room
  * [x] Shared group chat portals
