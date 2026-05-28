# wukongimv2

`cmd/wukongimv2` is the standalone verification entry for the `internalv2/app`
composition root.

This entry is for the migration period. In the first stage it starts the new
skeleton and verifies the single-node cluster SEND -> SENDACK path through the
gateway, message usecase, and `pkg/clusterv2`. It is not a full production
replacement for `cmd/wukongim` yet.

Run it with the normal `wukongim.conf` discovery rules:

```bash
go run ./cmd/wukongimv2 -config ./wukongim.conf
```
