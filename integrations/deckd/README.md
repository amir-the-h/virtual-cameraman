# Deck plugin

`me.the-h.virtualcameraman.sdPlugin` controls Virtual Cameraman from a macro
deck through its [HTTP API](../../docs/API.md). It's an Elgato-format plugin
(Node.js ≥ 22, no dependencies) made for [deckd](https://github.com/amir-the-h/deckd).

- **Capture On/Off** (key): start or stop the virtual camera; the key shows
  whether it's running.
- **Switch Camera** (dial): turn to switch between camera sources.

Install it from deckd's UI: under **Plugins → Plugin sources**, add
`https://github.com/amir-the-h/virtual-cameraman`. Each release publishes the
plugin and an `index.json` (built by `pack.sh`), so new versions show up there
as updates.

Or, while working on it, link it into deckd and restart it:

```bash
ln -s "$PWD/integrations/deckd/me.the-h.virtualcameraman.sdPlugin" ~/.config/deckd/plugins/
systemctl --user restart deckd
```

If the API runs on a port other than 8090, set it in either button's settings.
Each button's colours can be changed there too.
