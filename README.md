# squadron-gateway-discord

A Squadron gateway that surfaces `builtins.human.ask` questions in a
Discord channel. When an agent asks a question, the gateway posts a
message with quick-reply buttons (or a multi-select dropdown); the
human's answer flows back to the agent via a button click or message
reply.

## Setup

### 1. Create the Discord application

- Go to <https://discord.com/developers/applications> and click **New
  Application**. Name it whatever you want (e.g. `squadron`).
- Open the app → **Bot** tab → **Add Bot** → **Yes, do it**.
- Under **Privileged Gateway Intents**, enable **Message Content
  Intent** (required so the gateway can read free-text replies).
- Under **Token**, click **Reset Token**, copy the value, and stash it
  in squadron:

  ```bash
  squadron vars set discord_bot_token <token>
  ```

### 2. Invite the bot to your server

- App page → **OAuth2** → **URL Generator**.
- **Scopes**: `bot`, `applications.commands`.
- **Bot Permissions**: `View Channels`, `Send Messages`, `Read Message
  History`.
- Copy the generated URL, open it in a browser, pick the server,
  **Authorize**.

### 3. Pick a channel

In Discord: **Settings → Advanced → Developer Mode = on**, then
right-click the target channel → **Copy Channel ID**.

```bash
squadron vars set discord_channel_id <id>
```

### 4. Add the gateway to your squadron config

```hcl
variable "discord_bot_token" {
  secret = true
}

variable "discord_channel_id" {}

gateway "discord" {
  source  = "github.com/mlund01/squadron-gateway-discord"
  version = "v0.1.0"

  settings = {
    bot_token       = vars.discord_bot_token
    channel_id      = vars.discord_channel_id
    checkpoint_path = "${path.cwd}/.squadron/discord-gateway.json"
  }
}
```

Restart squadron and the log should show `gateway "discord" started`.
The next `builtins.human.ask` call lands in your channel.

## Settings reference

| Setting           | Required | Notes                                                                 |
| ----------------- | -------- | --------------------------------------------------------------------- |
| `bot_token`       | yes      | Bot token from the Discord developer portal. Use a `secret` variable. |
| `channel_id`      | one of   | Numeric channel ID (right-click → Copy Channel ID).                   |
| `channel_name`    | one of   | Channel name (e.g. `general` or `#general`). Resolved at startup.     |
| `guild_name`      | optional | Only needed if the bot is in multiple servers with the same channel name. |
| `checkpoint_path` | optional | File the gateway uses to remember the last event it processed across restarts. Defaults to `./.squadron-discord-gateway.json`. |

Set exactly one of `channel_id` or `channel_name`. `channel_id` is the
most stable handle — renaming a channel won't break the gateway.
`channel_name` is friendlier but pays a startup REST round-trip and
will break if the channel is renamed.

## What the operator sees

For a question with choices:

```
**Should I proceed with prod or staging?**
The schemas are identical except for the past 24 hours of writes.

[ prod ] [ staging ] [ both ]

`databricks_explore › discover`
```

Click a button to answer. For multi-select questions (`multi_select:
true` on the agent's tool call) the channel shows a dropdown picker
with `min=1, max=N` instead of buttons. For free-text questions, reply
to the message in Discord and the body becomes the answer.

When a question is resolved (here, in Discord, in Command Center, or
anywhere else), the message is edited to strike through the question
and append the answer.

## Local development

If you want to hack on the gateway itself, point your squadron config
at a locally-built binary:

```hcl
gateway "discord" {
  version = "local"   # skips the GitHub download
  # source omitted intentionally
  settings = { ... }
}
```

Then build and install:

```bash
go build -o gateway .
mkdir -p ~/.squadron/gateways/darwin-arm64/discord/local
mv gateway ~/.squadron/gateways/darwin-arm64/discord/local/gateway
```

Adjust the platform path to match `runtime.GOOS-runtime.GOARCH`.

## See also

- [`squadron-gateway-sdk`](https://github.com/mlund01/squadron-gateway-sdk)
  — the Go SDK if you want to build a gateway for some other system.
