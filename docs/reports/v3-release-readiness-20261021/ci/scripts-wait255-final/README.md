# Observation head terminal CI

All four first-attempt workflows at 76a3ba23303e288345dbd37bc354b41d81b6f070 have completed. Candidate default and all migration/messaging E2E stages passed; only scripts failed with the fixture signaler wait(-1)/255 already archived in generation-1-observed-failure. Chat, four native distributions, and SDK matrix passed. This is not a passing overall gate.

E2E artifact 12332359754 archive SHA/size/CRC was validated by the downloader. Exact merge source 974545635cc9be14a6b5f00413eb4bdb56ae0b7b has the same tree as the candidate head. Every retained command passed with verified empty cleanup and untruncated output. The executable itself is excluded by the workflow, so binary bytes cannot be independently rehashed; the declared hash matches both self-chat cases. Original failed scripts evidence stays in the prior immutable generation.
