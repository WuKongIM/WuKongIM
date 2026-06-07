# internalv2/contracts/channelmembers Flow

## Responsibility

`internalv2/contracts/channelmembers` contains the stable member-list channel-id
namespace shared by channel management usecases and runtime adapters.

It must remain dependency-light and must not import access, app, gateway,
cluster, or storage packages. The generated IDs intentionally match the legacy
namespace so internalv2 can read and write compatible allowlist, denylist, and
temporary subscriber rows.
