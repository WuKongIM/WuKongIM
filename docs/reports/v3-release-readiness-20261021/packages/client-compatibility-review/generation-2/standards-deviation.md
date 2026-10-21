# Recorded test-order deviation

The first SHA512 alias regression failed before the planner changed and then passed in 04-alias-green.log (4 discovered tests at 12:29:50Z). Five guard tests were added later, yielding 9 tests in 05-guards.log at 12:30:49Z. This did not follow the user instruction to write unit tests before implementation. The prior failure inventory does not change that observed order. Original receipts remain unchanged; current passing guards are regression evidence, not proof of tests-first compliance.

The later independently reproduced response-delivery false positive has its complete failure cases written before the fix. No user approval or extra release condition is inferred from this recorded process deviation.
