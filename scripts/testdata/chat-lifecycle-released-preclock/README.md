# TEST ONLY public predecessor fixture

`original.json` preserves the first released rehearsal's public document shapes
and fixed resource/cost model. All request/source/bundle/account identities,
resource IDs, public keys and addresses are synthetic. The bootstrap digest and
canonical Plan digest are recomputed for those synthetic identities. It contains
no private key, token, decrypted access, payload, provider call or signed URL.

The Go integration fixture creates bounded ZIP bytes and exact GET responses
locally, including independent Acquire and Release artifacts. A passing test is
not proof of a live GitHub identity, a provider operation or an actual invoice.
