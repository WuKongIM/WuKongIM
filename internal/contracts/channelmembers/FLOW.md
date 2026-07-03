# internal/contracts/channelmembers Flow

## Responsibility

`internal/contracts/channelmembers` contains the stable member-list channel-id
namespace shared by channel management usecases and runtime adapters.

It must remain dependency-light and must not import access, app, gateway,
cluster, or storage packages. The generated IDs intentionally match the legacy
namespace so internal can read and write compatible allowlist, denylist, and
temporary subscriber rows.
