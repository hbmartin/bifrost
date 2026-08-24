## ✨ Features

- **Virtual Key Rotation Cooldown** - New `client.vk_rotation_cooldown` setting (duration string, e.g. "5m"): after a rotation the previous key value keeps authenticating until the grace window expires. config.json VK sync now treats a changed value as an explicit rotation (with console warning) and recognizes the previously rotated-out value as "no change".
- **Realtime Ephemeral Credential Compatibility** - Reuses token cryptography across requests, dual-reads previous structured mappings, and returns opaque unauthorized errors for stale mapped keys
- **One-Click Render and Railway Deployments** - Added validated platform templates, release gates, inline runtime configuration, and safe arbitrary-UID startup for persistent volumes (thanks [@hbmartin](https://github.com/hbmartin)!)
