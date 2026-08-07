select(
  .schema == "wukongim.cloud_lease.receipt/v1" and
  .receipt.request_id == $request and
  .receipt.state == "active" and
  (.receipt.created_at | type == "string")
) |
.receipt.created_at
