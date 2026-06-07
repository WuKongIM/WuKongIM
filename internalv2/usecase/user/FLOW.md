# internalv2/usecase/user Flow

## Responsibility

`internalv2/usecase/user` coordinates legacy-compatible user token, device quit,
online-status, and system UID operations without depending on HTTP, gateway
frames, or concrete cluster types.

## Flow

```text
/user/token
  -> validate UID/token/device fields
  -> create UID metadata when missing
  -> upsert per-device token metadata
  -> for master-device updates, schedule owner-local same-device session close

/user/device_quit
  -> read selected device metadata
  -> clear stored token
  -> schedule owner-local matching-device session close

/user/onlinestatus
  -> prefer authoritative presence route lookup when configured
  -> return one legacy online item for each active route

/user/systemuids*
  -> persist reserved system UIDs through the configured system UID store
  -> maintain the process-local cache used by callers that need fast checks
```

The usecase treats a single node as a single-node cluster. Durable metadata
access happens through injected ports supplied by `internalv2/app`.
