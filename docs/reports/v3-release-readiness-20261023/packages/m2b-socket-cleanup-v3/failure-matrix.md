# Socket cleanup follow-up

The v2 independent Spec/Standards reviews found no blocking findings but
observed ResourceWarning from deliberately blocked connect calls. CPython
create_connection closes only for OSError; this guard deliberately raises
RuntimeError. Before changing the guard or its probes, require all four existing
clean/refused/converted/primary-error scenarios to emit zero ResourceWarning
after collection, while retaining the original independent attempt counts,
failure/cleanup receipts, and primary-error assertions. Then explicitly own and
close each probe socket. Do not change the guard exception, refusal classifier,
provider behavior, acceptance counts, or network policy.
