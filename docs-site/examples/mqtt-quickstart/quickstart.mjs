import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import mqtt from 'mqtt';

const clients = [];
let phase = 'configuration';
let succeeded = false;
let diagnostic = 'operation rejected';

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error('required input missing');
  return value;
}

function brokerURL(value) {
  const url = new URL(value);
  if (!['mqtt:', 'mqtts:'].includes(url.protocol) || url.username || url.password) {
    throw new Error('use a TCP URL without embedded credentials');
  }
  return value;
}

function inbox(uid) {
  return `wk/v1/users/${Buffer.from(uid, 'utf8').toString('base64url')}/messages`;
}

// Library QoS promises can stay pending after close; bound every control/ACK wait.
function bounded(client, operation, label) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const timer = setTimeout(() => finish(new Error(`${label} deadline`)), 10000);
    const onError = () => finish(new Error(`${label} connection failed`));
    const onClose = () => finish(new Error(`${label} connection closed`));
    function finish(error, result) {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      client.off('error', onError);
      client.off('close', onClose);
      if (error) {
        diagnostic = error.message;
        reject(error);
      } else resolve(result);
    }
    client.once('error', onError);
    client.once('close', onClose);
    operation.then((result) => finish(undefined, result), () => finish(new Error(`${label} rejected`)));
  });
}

// Install listeners before CONNECT; keep this first-message example ephemeral.
async function connect(url, uid, token, clientId) {
  const client = mqtt.connect(url, {
    manualConnect: true,
    protocolVersion: 5,
    clientId,
    username: uid,
    password: token,
    clean: true,
    reconnectPeriod: 0,
    resubscribe: false,
    queueQoSZero: false,
    connectTimeout: 10000,
    properties: {
      sessionExpiryInterval: 0,
      userProperties: { 'wk.device_flag': '1' },
    },
  });
  clients.push(client);
  client.on('error', () => {}); // The bounded operations below report failure without credentials.
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => finish(new Error('connect deadline')), 11000);
    const onError = () => finish(new Error('connection rejected'));
    const onClose = () => finish(new Error('connection closed'));
    const onConnect = (connack) => {
      try {
        assert.equal(connack.sessionPresent, false);
        assert.equal(connack.properties.maximumQoS, 1);
        assert.equal(connack.properties.retainAvailable, false);
        finish();
      } catch {
        finish(new Error('unexpected capabilities'));
      }
    };
    function finish(error) {
      clearTimeout(timer);
      client.off('connect', onConnect);
      client.off('error', onError);
      client.off('close', onClose);
      if (error) reject(error); else resolve();
    }
    client.once('connect', onConnect);
    client.once('error', onError);
    client.once('close', onClose);
    client.connect();
  });
  return client;
}

// Observe actual recipient delivery independently of the sender's PUBACK.
async function exchange(sender, receiver, fromUID, toUID, text, clientMsgNo) {
  phase = `exchange ${fromUID} to ${toUID}`;
  const payload = Buffer.from(JSON.stringify({ type: 1, content: text }), 'utf8');
  let cleanup;
  const received = new Promise((resolve, reject) => {
    const timer = setTimeout(() => finish(new Error('receive deadline after subscribe')), 20000);
    const onError = () => finish(new Error('receive connection failed'));
    const onClose = () => finish(new Error('receive connection closed'));
    const onMessage = (topic, body, packet) => {
      try {
        assert.equal(topic, inbox(toUID));
        assert.deepEqual(body, payload);
        const props = packet.properties.userProperties;
        assert.equal(props['wk.from_uid'], fromUID);
        assert.equal(props['wk.client_msg_no'], clientMsgNo);
        assert.equal(props['wk.channel_type'], '1');
        assert.match(props['wk.message_id'], /^[1-9][0-9]*$/);
        assert.match(props['wk.message_seq'], /^[1-9][0-9]*$/);
        finish(undefined, {
          from_uid: fromUID, message_id: props['wk.message_id'], message_seq: props['wk.message_seq'],
        });
      } catch {
        finish(new Error('recipient content or identity mismatch'));
      }
    };
    function finish(error, value) {
      if (error) diagnostic = error.message;
      cleanup();
      if (error) reject(error); else resolve(value);
    }
    cleanup = () => {
      clearTimeout(timer);
      receiver.off('message', onMessage);
      receiver.off('error', onError);
      receiver.off('close', onClose);
    };
    receiver.on('message', onMessage);
    receiver.once('error', onError);
    receiver.once('close', onClose);
  });
  // Attach a rejection observer before awaiting the independent publish operation.
  received.catch(() => {});
  try {
    const published = bounded(sender, sender.publishAsync(inbox(toUID), payload, {
      qos: 1, retain: false,
      properties: { userProperties: { 'wk.client_msg_no': clientMsgNo } },
    }), 'publish acknowledgement');
    const [message] = await Promise.all([received, published]);
    return message;
  } finally {
    cleanup();
  }
}

async function main() {
  const aliceToken = required('MQTT_ALICE_TOKEN');
  const bobToken = required('MQTT_BOB_TOKEN');
  const aliceURL = brokerURL(process.env.MQTT_URL ?? 'mqtt://127.0.0.1:1883');
  const bobURL = brokerURL(process.env.MQTT_BOB_URL ?? aliceURL);
  const run = randomUUID();
  phase = 'connect';
  const alice = await connect(aliceURL, 'alice', aliceToken, `docs-${run}-alice`);
  const bob = await connect(bobURL, 'bob', bobToken, `docs-${run}-bob`);
  phase = 'subscribe';
  for (const [client, uid] of [[alice, 'alice'], [bob, 'bob']]) {
    const grants = await bounded(client, client.subscribeAsync(inbox(uid), { qos: 1 }), 'subscription');
    assert.equal(grants[0].qos, 1);
  }
  phase = 'exchange';
  const exchanges = [
    await exchange(alice, bob, 'alice', 'bob', 'hello Bob', `${run}-alice-1`),
    await exchange(bob, alice, 'bob', 'alice', 'hello Alice', `${run}-bob-1`),
  ];
  succeeded = true;
  return { passed: true, client_version: '5.16.0', exchanges };
}

// Bound the complete demonstration, including missing ACKs and graceful cleanup.
const deadline = setTimeout(() => {
  for (const client of clients) client.end(true);
  process.stderr.write(`MQTT example failed during ${phase}: deadline\n`);
  process.exit(1);
}, 60000);

try {
  const result = await main();
  await Promise.all(clients.map((client) => client.endAsync(false)));
  console.log(JSON.stringify(result));
} catch {
  await Promise.allSettled(clients.map((client) => client.endAsync(true)));
  console.error(`MQTT example failed during ${phase}: ${diagnostic}; check credentials, listener and server logs`);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  if (!succeeded) process.exitCode = 1;
}
