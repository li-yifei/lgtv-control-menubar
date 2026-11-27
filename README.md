# LG TV Control

Small macOS menu bar app and CLI for controlling an LG webOS TV.

The macOS menu bar app uses MyGo’s native UI. The app and CLI share a Go controller for the webOS WebSocket API, Wake-on-LAN, and LG IP control. Pairing credentials (client key, IP control keycode) live in macOS Keychain under the `com.lgtv-control` service; non-secret config (host, MAC addresses) stays in a JSON file at `~/.config/lgtv-pairing.json`.

## Install

The release supports Apple Silicon Macs running macOS 14 Sonoma or later.

```sh
brew install --cask li-yifei/tap/lgtv-control
```

This installs the menu bar app to `/Applications` and symlinks a `lgtv` CLI into Homebrew's bin directory. The app is self-signed, so the cask strips the quarantine attribute on install.

To uninstall:

```sh
brew uninstall --cask lgtv-control
```

## First run

1. Launch **LG TV Control** from Spotlight or Launchpad.
2. Click the menu bar icon, choose **Pair / Re-pair**, enter the TV’s IP address or hostname, and accept the on-screen prompt on the TV. The client key is saved to Keychain.
3. (Optional, only needed for service-menu PIN entry) On the TV, navigate to **Settings → All Settings → Network → LG Connect Apps** and copy the 8-character IP control keycode. In the menu bar app open **Settings → IP Control Keycode**, paste it, and save.

## webOS 26 compatibility

Use **v0.5.1 or later** with webOS 26. Some newer firmware rejects the legacy registration certificate with:

```text
403 Pairing rejected: blacklisted certificate detected
```

The app first sends the original signed registration handshake for older TVs. When the TV explicitly reports this certificate error, it reconnects and retries once with an unsigned manifest, preserving the requested permissions. Other errors are returned normally. This applies to both the menu bar app and CLI.

After upgrading the TV firmware:

1. Install [v0.5.1 or later](https://github.com/li-yifei/lgtv-control-menubar/releases/latest) and restart the app.
2. Try **Refresh** to reconnect with the saved pairing key.
3. If authorization is needed, select **Pair / Re-pair**, enter the TV's current IP address, and accept the prompt on the TV.

Validation covers live status retrieval on webOS 26 and automated regression checks for the legacy manifest, permission preservation, and bounded fallback. Older firmware compatibility has been checked at the code level; coverage across all TV models and firmware versions remains incomplete. Service-menu access and IP-control PIN entry require separate firmware-specific verification.

## Local connection

Some LG TVs in Always Ready or standby accept connections on port 3001 and
then close the WebSocket with `1008 Try Again Later (EWS)`. Choose
**Power On**, or turn on the TV with its remote, then choose **Refresh**.
The CLI equivalents are `lgtv power on` followed by `lgtv status` once the
TV is awake. This behavior was reproduced on an OLED42C3PJA.

If the app cannot reach an awake TV, verify its current IP and check
**System Settings → Privacy & Security → Local Network** for LG TV Control.

## Features

- Native menu bar menu with volume up / down, mute, and an inline volume slider
- Safety volume reminder with configurable threshold
- Power on (Wake-on-LAN with auto-discovered MACs) and power off
- HDMI input list and switching
- **InStart / EZ Adjust**: launch LG service menus and auto-type the 4-digit PIN over IP control
- Customizable single-letter menu shortcuts
- Structured CLI (`lgtv`) with `--json` for scripts and the **Run Shell Script** action in Shortcuts

## CLI

```sh
lgtv --help
lgtv status --json
lgtv volume set 12
lgtv volume up --steps 3
lgtv mute toggle
lgtv power on
lgtv input list
lgtv input switch HDMI_2
lgtv raw ssap://audio/getVolume --json
```

Reads `LG_TV_CONFIG` first, then `~/.config/lgtv-pairing.json`. Individual commands can override with `--config PATH`. Data commands write JSON to stdout with `--json`; failures go to stderr with non-zero exit.

## Build from source

Requires macOS 14 or newer and Go 1.27.3 or newer.

```sh
./build.sh
```

Produces `build/LG TV Control.app`, `build/LG-TV-Control.app.zip`, and the standalone CLI at `build/bin/lgtv` with `build/lgtv-LICENSES.txt`.

Use the same certificate to sign successive builds and share Keychain authorization between the app and the CLI. See [Development reload](#development-reload) for certificate setup.

Run the Go checks for signed and unsigned registration and the standby close response. They use a local WebSocket server:

```sh
sh scripts/test-registration.sh
```

## Development reload

```sh
./scripts/setup-codesign.sh  # once, if the signing identity is missing
./scripts/dev.sh
```

The setup script requires `openssl@3`. The reload command signs each
build with the same `LG TV Control Dev` certificate and
`io.github.li-yifei.lgtv-control.dev` bundle identifier. Set
`LGTV_CODESIGN_IDENTITY` to use a different installed certificate and keep
it the same across rebuilds. The dev bundle uses the existing configuration
and Keychain items. If macOS asks to authorize the dev app, choose
**Always Allow** for that item once. In the dev terminal, enter `r` to
rebuild or `q` to quit.

## License

[MIT](LICENSE). See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for bundled dependencies.
