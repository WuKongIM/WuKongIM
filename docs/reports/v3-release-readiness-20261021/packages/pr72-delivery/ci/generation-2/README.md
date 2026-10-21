# Second candidate CI generation — complete and ready for review

Head `c8aae37d13787a0aceceac751d6d35bbc1d89c35`, tree `5fac246f4234d1652b86f26a72927a4b003bbbe7`, base `0cd625cbee214927a81b0a1015d825a6f53fbf28`. Both actual checkout logs and the candidate receipt identify synthetic merge `6526671784e1c0e0252fcec0709832302ee0df29`; its tree equals the reviewed head.

Pages 39579343465: 449 Python tests, zero skips, and 4 contract tests passed. Deployment skipped. Native 39579343148 attempt 1 completed successfully: source metadata 7, local signing 10, bootstrap preparation 6 and all 31 candidate cases (10 positive / 21 exact rejection checks). The later full signed repository build/install validation also actually ran and passed. Candidate test secrets were removed before independent verification, scratch was removed, and checked-in controls stayed unchanged.

The four small public receipt ZIPs were fully downloaded; recomputed ZIP SHA-256 values match GitHub artifact digests, and member CRC/SHA values are retained. Product binaries and private signing material were not downloaded. The candidate receipt records a TEST ONLY source fixture, not real GitHub release provenance or public stable installation.

PR #72 body was updated to these facts and read back byte-for-byte. It is ready for review, unmerged. Thirty protected control/signing/archive files still match the base. Stable remains disabled; no production key, channel activation, cloud write or publication occurred. The first failed generation and socket repair red/green evidence remain separate and unmodified.
