# Cold and hot SEND ordering E2E

Use real WKProto sessions and public HTTP in 256-Hash-Slot single-node and
three-node clusters. Warm one group, then send twice to a new group and once to the
warm group on the same connection. Match decoded ACK timestamps by request ID;
verify the 400 ms budget, same-group sequence order, and exact histories.

Run `GOWORK=off go test -tags=e2e ./test/e2e/message/cold_hot_send -count=1 -timeout=4m -p=1 -v`.
`WK_E2E_COLD_HOT_REPORT` selects the JSON receipt path. This small regression
proves removal of the fixed cold collection delay; the unchanged hosted
500 SEND/s qualification remains the performance gate.
