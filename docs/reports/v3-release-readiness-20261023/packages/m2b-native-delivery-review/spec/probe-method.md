# Bounded helper probe method

Both probes imported the frozen driver with Python `-B`; its top-level imports are standard library only. No SDK module, cloud client, actual socket connect, container, full suite, or source mutation was used. Results are in `bounded-helper-probes.json`.

1. Patch `socket.socket.connect` with a function that records the intercepted attempt then raises the same `RuntimeError` used by `main`. Call the real `refused(lambda: socket.create_connection(("127.0.0.1", 9), timeout=1))`. It returned normally after one intercepted attempt; no real connect was made. This demonstrates the negative-case exception classifier can absorb the guard.
2. In a review-owned temporary directory create `native-sdk-work/bundle.json`. Call real `main()` with SDK arguments, patching only `sdk_cases` to raise a fixed `ValueError` and `shutil.rmtree` to raise `PermissionError`. The outer exception was `PermissionError`; the output receipt did not exist. The probe subsequently left the mocks and removed its own temporary directory through the real cleanup.

These isolate failure semantics. They do not claim to rerun the full native/SDK acceptance or establish a production network escape. The guard prevented the socket; the defect is that this forbidden attempt can be reported as an expected refusal.
