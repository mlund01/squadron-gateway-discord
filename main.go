// Squadron Discord gateway. Subscribes to ask_human events from a
// running squadron and bridges them to a Discord channel: new
// questions become messages with choice buttons; clicking a button
// (or replying in the message's thread) resolves the request.
//
// Required HCL settings:
//
//	gateway "discord" {
//	  source  = "github.com/mlund01/squadron-gateway-discord"
//	  version = "vX.Y.Z"
//	  settings = {
//	    bot_token  = vars.discord_bot_token
//	    channel_id = "1234567890"
//	  }
//	}
//
// Optional settings:
//
//	checkpoint_path = "/abs/path/to/checkpoint.json"   // default: ./.squadron-discord-gateway.json
//
// The checkpoint file holds the latest event timestamp the gateway has
// processed; on restart we ask squadron for everything since then so
// transient disconnects don't drop events.
package main

import "github.com/mlund01/squadron-gateway-sdk"

func main() {
	gateway.Serve(newDiscordGateway())
}
