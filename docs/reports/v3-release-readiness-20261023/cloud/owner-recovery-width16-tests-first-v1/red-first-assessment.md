# First bounded RED assessment

Four approved commands each executed once. All compiled successfully. The 128MiB/32MiB auxiliary budget control passed. Public 1-UID and full512-UID cases each failed at the fifth owner entry; the adapted proof-recheck fixture failed at its fifth owner entry. The cluster adapter observed exactly four admitted owner reads rather than sixteen. The byte fixture failed before its fifth 2048-byte fill entered; this is the expected C4 limit and does not yet independently test the 128MiB versus accidental512MiB distinction.

No outer timeout/output cap fired; stderr was empty. The runner verified all four frozen test files and all frozen source/rule hashes before and after every command. Production remains C4/K2048/128MiB and adapter4. All four recorded PGIDs were absent after completion. No retry, race, product workload, network, cloud action, or production edit was performed.

These are intentional test-first failures for the proposed wider admission contract. They do not establish that width causes or fixes message_loss. Implementation remains blocked pending root authorization.
