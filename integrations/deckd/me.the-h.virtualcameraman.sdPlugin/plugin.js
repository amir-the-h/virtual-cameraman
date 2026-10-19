// Virtual Cameraman deck plugin (Node >= 22, no deps): a capture on/off key and a camera-switch dial,
// talking to Virtual Cameraman's local HTTP API (docs/API.md). Runs in deckd or any host that speaks
// the Stream Deck SDK WebSocket protocol.

const args = {};
for (let i = 2; i + 1 < process.argv.length; i += 2) args[process.argv[i].replace(/^-/, "")] = process.argv[i + 1];

const log = (...msg) => console.log(new Date().toISOString(), ...msg);

const ws = new WebSocket(`ws://127.0.0.1:${args.port}`);
const send = (msg) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify(msg));

ws.onopen = () => {
  send({ event: args.registerEvent, uuid: args.pluginUUID });
  send({ event: "getGlobalSettings", context: args.pluginUUID });
};
ws.onclose = () => process.exit(0);
ws.onmessage = (m) => {
  let msg;
  try { msg = JSON.parse(m.data); } catch { return; }
  handle(msg).catch((e) => log(`${msg.event} failed:`, e.stack || e));
};

const TOGGLE = `${args.pluginUUID}.toggle`;
const SWITCH = `${args.pluginUUID}.switch`;
const POLL_MS = 5000;

// Material Design Icons (Apache 2.0), 24x24.
const PATHS = {
  camOn: "M17 10.5V7c0-.55-.45-1-1-1H4c-.55 0-1 .45-1 1v10c0 .55.45 1 1 1h12c.55 0 1-.45 1-1v-3.5l4 4v-11l-4 4z",
  camOff: "M21 6.5l-4 4V7c0-.55-.45-1-1-1H9.82L21 17.18V6.5zM3.27 2L2 3.27 4.73 6H4c-.55 0-1 .45-1 1v10c0 .55.45 1 1 1h12c.21 0 .39-.08.54-.18L19.73 21 21 19.73 3.27 2z",
  cameraSwitch: "M16 7h-1l-1-1h-4L9 7H8c-1.1 0-2 .9-2 2v6c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V9c0-1.1-.9-2-2-2zm-4 7c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zM8.57.51l4.48 4.48V2.04c4.72.47 8.48 4.23 8.95 8.95h2C23.34 3.02 15.49-.59 8.57.51zm2.38 21.45c-4.72-.47-8.48-4.23-8.95-8.95H0c.66 7.97 8.51 11.58 15.43 10.48l-4.48-4.48v2.95z",
};

// Default colours; the settings panel (pi/index.html) uses the same keys.
const DEFAULTS = {
  [TOGGLE]: { onColor: "#2196f3", offColor: "#6a6a6a", onBackground: "", offBackground: "" },
  [SWITCH]: { iconColor: "#ffffff", titleColor: "#ffffff", valueColor: "#ffffff" },
};

const contexts = new Map(); // context -> { action, settings }
const sent = new Map(); // context:kind -> last message, to skip redundant updates
let globals = {}; // { port }
const camera = { running: false, name: "" };

function colorOf(ctx, key) {
  const v = contexts.get(ctx)?.settings?.[key];
  if (typeof v === "string" && /^#[0-9a-f]{3,8}$/i.test(v)) return v;
  return DEFAULTS[contexts.get(ctx)?.action]?.[key] ?? "#ffffff";
}

const svgUri = (svg) => `data:image/svg+xml;base64,${Buffer.from(svg).toString("base64")}`;

function keyImage(path, color, background) {
  const bg = background ? `<rect width="144" height="144" fill="${background}"/>` : "";
  return svgUri(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 144 144">${bg}<path transform="translate(30 30) scale(3.5)" fill="${color}" d="${path}"/></svg>`);
}

function sendOnce(ctx, kind, msg) {
  const key = `${ctx}:${kind}`;
  const body = JSON.stringify(msg);
  if (sent.get(key) === body) return;
  sent.set(key, body);
  send(msg);
}

const contextsOf = (action) => [...contexts].filter(([, c]) => c.action === action).map(([ctx]) => ctx);

function draw(ctx) {
  const c = contexts.get(ctx);
  if (c?.action === TOGGLE) {
    for (const [st, path, prefix] of [[0, PATHS.camOff, "off"], [1, PATHS.camOn, "on"]]) {
      const image = keyImage(path, colorOf(ctx, `${prefix}Color`), colorOf(ctx, `${prefix}Background`));
      sendOnce(ctx, `image${st}`, { event: "setImage", context: ctx, payload: { image, state: st, target: 0 } });
    }
    sendOnce(ctx, "state", { event: "setState", context: ctx, payload: { state: camera.running ? 1 : 0 } });
  } else if (c?.action === SWITCH) {
    const icon = svgUri(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="${colorOf(ctx, "iconColor")}" d="${PATHS.cameraSwitch}"/></svg>`);
    sendOnce(ctx, "feedback", {
      event: "setFeedback",
      context: ctx,
      payload: { icon, title: { value: "Camera", color: colorOf(ctx, "titleColor") }, value: { value: camera.name || "Turn to switch", color: colorOf(ctx, "valueColor") } },
    });
  }
}

const drawAll = (action) => contextsOf(action).forEach(draw);

async function api(method, path, timeout = 2000) {
  const port = Number(globals.port) || 8090;
  const res = await fetch(`http://127.0.0.1:${port}${path}`, { method, signal: AbortSignal.timeout(timeout) });
  const text = await res.text();
  try { return JSON.parse(text); } catch { throw new Error(`${path}: ${text.trim() || res.status}`); }
}

async function refreshCamera() {
  try {
    camera.running = (await api("GET", "/state")).running === true;
  } catch (e) {
    log("camera state:", e.message);
  }
  drawAll(TOGGLE);
}

async function toggleCamera() {
  // Show the new state straight away; the reply (or a re-query) corrects it.
  camera.running = !camera.running;
  drawAll(TOGGLE);
  try {
    camera.running = (await api("POST", "/toggle", 10000)).running === true;
    drawAll(TOGGLE);
  } catch (e) {
    await refreshCamera();
    throw e;
  }
}

async function switchCamera(ticks) {
  for (let i = 0; i < Math.abs(ticks); i++) {
    const info = await api("POST", ticks > 0 ? "/cameras/next" : "/cameras/previous");
    camera.name = info.name || `Camera ${info.index}`;
  }
  drawAll(SWITCH);
}

function run(ctx, promise) {
  promise.catch((e) => {
    log("action failed:", e.message || e);
    send({ event: "showAlert", context: ctx });
  });
}

async function handle({ event, action, context: ctx, payload = {} }) {
  switch (event) {
    case "willAppear":
      contexts.set(ctx, { action, settings: payload.settings || {} });
      for (const k of [...sent.keys()]) if (k.startsWith(`${ctx}:`)) sent.delete(k);
      draw(ctx);
      if (action === TOGGLE) refreshCamera();
      break;
    case "willDisappear":
      contexts.delete(ctx);
      break;
    case "didReceiveSettings":
      if (contexts.has(ctx)) {
        contexts.get(ctx).settings = payload.settings || {};
        draw(ctx);
      }
      break;
    case "didReceiveGlobalSettings":
      globals = payload.settings || {};
      refreshCamera();
      break;
    case "keyUp": // act on release, so tap/hold triggers configured in deckd behave naturally
      if (action === TOGGLE) run(ctx, toggleCamera());
      break;
    case "dialRotate":
      if (action === SWITCH && payload.ticks) run(ctx, switchCamera(payload.ticks));
      break;
  }
}

// Pick up changes made elsewhere (the API, another button) while visible.
setInterval(() => {
  if (contextsOf(TOGGLE).length) refreshCamera();
}, POLL_MS);
