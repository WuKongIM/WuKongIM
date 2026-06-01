# internalv2/runtime/online Flow

## Responsibility

`internalv2/runtime/online` owns node-local real gateway sessions for internalv2 connection routing. It is not a distributed directory.

## Lifecycle

`RegisterPending` is used before CONNACK succeeds. `MarkActive` promotes a session after authority registration and local active re-check. `MarkClosingAndUnregister` removes local indexes before authority unregister tombstones are queued.

## Rehydrate

`VisitActiveByHashSlot` pages active routes for bounded authority-change rehydrate without copying or sorting an entire hash-slot bucket.
