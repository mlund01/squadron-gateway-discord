# squadron-gateway-discord

Squadron gateway that bridges `builtins.human.ask` requests to a
Discord channel. New questions become messages with quick-reply
buttons; clicking a button (or replying to the message) records the
answer back in squadron.

## Status

Reference implementation of the Squadron gateway model. Demonstrates:

- Subscribing to live `human_input_requested` / `human_input_resolved`
  events pushed from squadron.
- Catching up after disconnects via the SDK's `ListHumanInputs(since:…)`
  call and a local checkpoint file.
- Submitting resolutions via `ResolveHumanInput`, with idempotent
  handling of "already answered by another surface".

## Configure

In your squadron config:

```hcl
variable "discord_bot_token" {
  secret = true
}

gateway "discord" {
  source  = "github.com/mlund01/squadron-gateway-discord"
  version = "local"   # or a release tag once published

  settings = {
    bot_token       = vars.discord_bot_token
    channel_id      = "1234567890123456789"
    checkpoint_path = "${path.cwd}/.squadron/discord-gateway.json"
  }
}
```

### Targeting a channel by name

`channel_id` is the most stable handle (renaming a channel won't break
the gateway), but you can target by name instead:

```hcl
settings = {
  bot_token    = vars.discord_bot_token
  channel_name = "agent-questions"   # leading "#" is optional
  # guild_name = "My Server"          # only needed if the bot is in
                                      # multiple guilds that all have
                                      # a channel by this name
}
```

Set exactly one of `channel_id` or `channel_name`. When `channel_name`
is used, the gateway resolves it to an ID at startup by listing the
bot's guilds — bumping the bot's `View Channels` permission is required
for the lookup to see the channel.

`version = "local"` skips the GitHub download and expects the binary
at `.squadron/gateways/<platform>/discord/local/gateway`.

To install locally:

```bash
go build -o gateway .
mkdir -p ~/.squadron/gateways/darwin-arm64/discord/local
mv gateway ~/.squadron/gateways/darwin-arm64/discord/local/gateway
```

(Adjust the platform path to match `runtime.GOOS-runtime.GOARCH`.)

## Releasing

Releases are cut by goreleaser on tag push. The pipeline produces the
exact archive layout squadron's gateway loader expects (`<repo>_<os>_<arch>.tar.gz`
containing a single `gateway` binary, plus `checksums.txt`).

To ship a new version:

```bash
# Drop the local replace directive in go.mod first — release builds
# must resolve squadron-gateway-sdk from its published module path,
# not a sibling worktree.
go mod edit -dropreplace github.com/mlund01/squadron-gateway-sdk
go mod tidy

git tag v0.1.0
git push origin v0.1.0
```

The `Release` workflow ([.github/workflows/release.yml](.github/workflows/release.yml))
runs goreleaser ([.goreleaser.yml](.goreleaser.yml)) and uploads the
archives + checksums to the matching GitHub release.

## Discord setup

One-time, ~5 minutes.

### 1. Create the application

- Go to https://discord.com/developers/applications and click **New Application**.
- Name it whatever you want (e.g. `squadron`). Save.

### 2. Add a bot

- Open the app → **Bot** tab in the sidebar → **Add Bot** → **Yes, do it**.
- Under **Privileged Gateway Intents**, enable **Message Content Intent**
  (required so the gateway can read free-text replies).
- Under **Token**, click **Reset Token**, copy the value, and stash it in
  squadron:

  ```bash
  squadron vars set discord_bot_token <token>
  ```

### 3. Invite the bot to your server

- App page → **OAuth2** → **URL Generator**.
- **Scopes**: `bot`, `applications.commands`.
- **Bot Permissions**: `View Channels`, `Send Messages`, `Read Message
  History`.
- Copy the generated URL, open it in a browser, pick the server, **Authorize**.

### 4. Get the channel id

- In Discord client: **Settings → Advanced → Developer Mode = on**.
- Right-click the target channel → **Copy Channel ID**.
- Either set it as a var…

  ```bash
  squadron vars set discord_channel_id <id>
  ```

  …or hard-code it in `channel_id` in your HCL.

### 5. Restart squadron

The gateway is loaded at startup. After setting the vars (or editing the
HCL) run `scripts/restart.sh` (or your equivalent) and check the squadron
log for `gateway "discord" started`.

## What the user sees

For a question with choices:

```
**Should I proceed with prod or staging?**
The schemas are identical except for the past 24 hours of writes.

[ prod ] [ staging ] [ both ]

`databricks_explore › discover`
```

For free-text or "Other": reply to the message in Discord and the
gateway forwards the body as the response.
