# First candidate CI generation — preserved failure

Head `d79bff5219f5a69872bb1720cbbaa224b1c7b3eb`, attempt 1. Both job checkout logs show synthetic merge `5eb8adb7af1a944684b83a481166aa266daaa746`; Git API confirms the same tree as the reviewed head.

Pages 39578472569: 446 Python tests and 4 contract tests passed; deploy skipped. Native 39578472435: metadata 7, local signing 10 and bootstrap preparation 6 cases actually passed; the new stable candidate failed before its first recorded command/case. Receipt reports only preflight/AssertionError and scratch removal. The later full repository drill was skipped, not passed.

The macOS reproduction gives gpg-agent's explicit socket-name-too-long error for the same nested home. With the same GPG arguments and only the temporary prefix shortened, TEST ONLY generation passed and the owned agent/scratch were cleaned. This proves a harness path defect on macOS; the original Linux receipt cannot by itself prove the exact exception cause. A new commit shortens only the disposable path and adds non-sensitive phase labels. The old workflow is not rerun.

Small artifact ZIPs were read completely, with member CRC and SHA recorded; binary ZIPs and transient redirect errors are excluded from this archive allowlist. All signing material in these receipts is public or TEST ONLY; no production private key was read. Stable remains disabled and nothing was published.
