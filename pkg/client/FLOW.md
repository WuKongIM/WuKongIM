# pkg/client Flow

`pkg/client` is a tooling-grade WKProto TCP client for wkbench, e2e tests, and server-side Go tools.

It owns protocol connection behavior only: CONNECT/CONNACK, optional session encryption, SEND/SENDACK, RECV/RECVACK, PING/PONG, one writer pump, one reader loop, and optional pooling. It does not prepare users, channels, subscribers, or tokens.

SEND batching writes multiple normal WKProto SEND frames contiguously on one TCP stream. No SENDBATCH frame is introduced.
