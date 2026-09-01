'use strict';

const {
  WKIM,
  WKIMChannelType,
  WKIMDeviceFlag,
  WKIMEvent,
} = require('easyjssdk');

const required = [
  'PEER_WS_URL',
  'ALICE_UID',
  'BOB_UID',
  'BOB_TOKEN',
  'ALICE_TO_BOB_TEXT',
  'BOB_TO_ALICE_TEXT',
];

for (const name of required) {
  if (!process.env[name]) {
    throw new Error(`Missing required environment variable: ${name}`);
  }
}

const timeout = setTimeout(() => {
  console.error('PEER_TIMEOUT');
  process.exit(1);
}, 15 * 60_000);

const peer = WKIM.init(
  process.env.PEER_WS_URL,
  {
    uid: process.env.BOB_UID,
    token: process.env.BOB_TOKEN,
    deviceFlag: WKIMDeviceFlag.Web,
  },
  { singleton: false },
);

let completed = false;

peer.on(WKIMEvent.Message, async message => {
  if (
    completed ||
    message.fromUid !== process.env.ALICE_UID ||
    message.payload?.content !== process.env.ALICE_TO_BOB_TEXT
  ) {
    return;
  }

  completed = true;
  try {
    // Let the SDK finish the automatic RECVACK emitted after Message handlers
    // return before issuing a new request on the same connection.
    await new Promise(resolve => setTimeout(resolve, 250));
    const acknowledgment = await peer.send(
      process.env.ALICE_UID,
      WKIMChannelType.Person,
      { type: 1, content: process.env.BOB_TO_ALICE_TEXT },
    );
    if (!acknowledgment.messageSeq) {
      throw new Error('Reply acknowledgment did not contain a message sequence');
    }
    console.log('PEER_PASS alice-to-bob=true bob-to-alice-ack=true');
    clearTimeout(timeout);
    setTimeout(() => {
      peer.disconnect();
      process.exit(0);
    }, 3_000);
  } catch (error) {
    console.error(`PEER_REPLY_FAILED ${error?.constructor?.name || 'Error'}`);
    process.exit(1);
  }
});

peer.on(WKIMEvent.Error, error => {
  console.error(`PEER_SDK_ERROR ${error?.constructor?.name || 'Error'}`);
});

peer.connect()
  .then(() => console.log('PEER_READY package=easyjssdk@2.0.4'))
  .catch(error => {
    console.error(`PEER_CONNECT_FAILED ${error?.constructor?.name || 'Error'}`);
    process.exit(1);
  });
