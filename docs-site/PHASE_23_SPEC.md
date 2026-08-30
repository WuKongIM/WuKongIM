# Phase 23: UniApp retirement and JSSDK migration

## Goal

Publish a bilingual retirement path for the deprecated
`WuKongIMUniappSDK`. The path must stop new adoption of the old package, explain
why the registry artifact alone is misleading, and give existing UniApp teams
a bounded migration and runtime-validation checklist for the current
`wukongimjssdk` package. It must not resurrect the deprecated wrapper or project
the site's Chromium receipt onto any UniApp target.

Phase 23 publishes only:

- `/{lang}/sdk/uniapp`
- `/{lang}/sdk/uniapp/migrate-to-jssdk`

There is no new UniApp installation, quickstart, platform-capability, API
reference, or upgrade claim. The migration page is an evaluation guide, not a
runtime compatibility receipt.

## Deprecated source and artifact snapshot

The official `WuKongIMUniappSDK` default branch is frozen at revision
`582cacb5ed7a559b66ed4f66fe71dd1a3608e21d` (2023-10-20). It has no git tags.
Revision `88da7bff68046bd4f2299b511e0dcb91a705c8de` added the README's explicit
`Deprecated` notice on 2023-07-13 and directs users to
`WuKongIM/WuKongIMJSSDK`.

The repository still declares package version `1.0.3`, but its last published
npm artifact predates that retirement notice:

```text
wukongimuniappsdk@1.0.3
published 2023-06-26
tarball SHA-256 a2dfcb7a2317ea6f123ac4fbd8f92a2ecee6f48eaa10d6629e77abc1a1540db7
integrity sha512-3IYWWKqRAVloLn7MVkoPJO0diF16UIPWxnLgO8/SaqTE06dxUkynVD0kxRoOzsnBM4UX+su5IoHpxWYH5wEwWA==
```

The registry metadata has no `deprecated` field and the tarball README has no
retirement notice. The tarball also declares
`"wukongimuniappsdk": "^1.0.1"` as its own dependency. Therefore npm metadata,
version equality, a successful install, or the tarball README must never be
used to claim that the package remains maintained.

The repository's current example imports `wukongimjssdk` rather than its local
wrapper source. Its package range is `^1.0.5` and its lockfile resolves an old
`1.2.2`; that example is migration history, not the target version for new
documentation.

## JSSDK migration target

The migration guide pins:

```text
wukongimjssdk@1.3.5
tag/revision 3c507ea3ebc08eae9d74fc1f76b150c380752008
tarball SHA-256 b053c9623ac36b7ce78dfd874240ac48abaee48e20dd78d824f28881c5504cfc
integrity sha512-Y3RY4IdkLfCB2MCJFQlamSe5EQ6SU3PGphdoV9MJjJTSUAzZTTw5gBxmMi2jbwLRDqM+cSFaIb1vhQ+Rl0ftnQ==
```

The package export map exposes only the package root and `package.json`; the
guide uses root imports and rejects legacy `wukongimjssdk/lib/*` imports.

Exact `1.3.5` source detects transports in a fixed order: global `uni` uses
`uni.connectSocket`, otherwise global `wx` uses `wx.connectSocket`, otherwise it
falls back to native `WebSocket`. It also exposes `config.platform`, but passing
`uni` or `wx` there does not initialize or refresh the module-level
`wkconnectSocket`. On first use it can therefore remain undefined and create a
native `WebSocket` while later treating it as a platform socket; after an
earlier auto-detected connection it can retain a stale adapter. The migration
guide must not recommend either explicit override as a working fix. A target
that requires a platform socket but exposes neither supported global must patch
or fork the adapter and prove the result; an H5 target with native `WebSocket`
must validate that separate branch directly.

`config.debug` defaults to `false`, but exact `1.3.5` does not use it to guard
all console output. The receive path decrypts `RecvPacket.payload` and then logs
the packet, while reconnect queue flushing logs plaintext `SendPacket` values.
Therefore `config.debug = false` cannot close the Payload-logging gate. A
production build needs a reviewed redaction/removal patch or verified build-time
stripping, and the exact released artifact must prove that sensitive Payloads
cannot reach console collectors.

`config.deviceFlag` defaults to `1`. Before CONNECT, applications must choose
the protocol category deliberately and keep it aligned with backend token
metadata: `0 = APP`, `1 = WEB`, `2 = PC`. Native App and H5 builds must not
inherit one hardcoded value merely because both were built with UniApp. Any
target without an obvious category requires an explicit product decision and
its own acceptance evidence.

## Migration contract

The reader must be able to:

1. inventory the exact old package, lockfile, deep imports, connection setup,
   provider callbacks, custom message registration, and target matrix before
   changing dependencies;
2. use only the package manager owning the existing lockfile, remove
   `wukongimuniappsdk`, install exact `wukongimjssdk@1.3.5`, and commit the
   reviewed lockfile without creating a second lockfile; the npm example must
   explain that an empty `npm ls wukongimuniappsdk` normally has exit code `1`,
   and the guide must include Yarn and pnpm equivalents;
3. import only from `wukongimjssdk` and map APIs against the exact exported
   declarations rather than assuming the old wrapper is drop-in compatible;
4. obtain UID, short-lived token, and WSS route from a trusted backend;
5. set identity, route, provider callbacks, and the correct Device Flag, use
   `WKSDK.shared().register(contentType, factory)` for custom content, then add
   connection/message/SENDACK listeners through their manager APIs before
   connecting; an unregistered custom type decodes as `UnknownContent`;
6. let exact `1.3.5` auto-detect global `uni`, then global `wx`, or use its
   native `WebSocket` fallback; maintain and test a reviewed adapter fork only
   when the target needs a platform socket and exposes none of those paths;
7. run one isolated Alice/Bob acceptance for each App, H5, or mini-program
   target and keep SENDACK, online receipt, reconnect, and offline recovery
   results separate;
8. retain an exact HBuilderX/CLI, uni-app/Vue, package-lock, target, device/OS,
   server revision, route scheme, and redacted result tuple;
9. block production release until the unconditional plaintext Payload console
   paths are removed or redacted in a reviewed artifact; `config.debug = false`
   is not evidence that logging is disabled.

The guide must not tell readers to expose Product HTTP administrative
credentials in the UniApp client. The old example calls demonstration Product
HTTP endpoints directly; it is not a production trust architecture.

## Evidence boundary

The documentation host downloaded and inspected both npm tarballs and both
official repositories. It did not run HBuilderX, build an App/H5/mini-program
target, call `uni.connectSocket` or `wx.connectSocket`, install a package on a
device, or run a UniApp Alice/Bob scenario.

The existing `wukongimjssdk@1.3.5` executable receipt covers the repository's
browser/Chromium golden path only. It proves neither the UniApp adapter nor App,
H5, WeChat, another mini-program, mobile background, push, packaging, domain
allowlists, or device network policy. Each target remains unverified until its
exact tuple produces a separate receipt.

## Documentation integration

- Replace the planned UniApp navigation group with a published retirement
  group containing only the overview and migration route.
- Link the migration route from SDK overview, chooser, and compatibility pages
  in both languages.
- Keep the deprecated package out of install commands and runnable samples.
- Record the stable retirement, artifact mismatch, target package, adapter
  detection/override bug, unconditional Payload logging, Device Flag rule, and
  evidence boundary in
  `docs/development/PROJECT_KNOWLEDGE.md`.
- Add a contract test that pins every revision, package hash, integrity value,
  route, migration command, source caveat, and runtime-receipt boundary.
