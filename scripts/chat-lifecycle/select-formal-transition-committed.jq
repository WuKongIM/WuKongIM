select(
  .schema == "wukongim.chat_lifecycle.formal_transition/v1" and
  .from_stage == "rehearsal" and
  .outcome == "rehearsal_pass" and
  .zero_inventory == true and
  .request_id == $request and
  .source_sha == $source and
  .bundle_digest == $bundle and
  (.committed_micros | type == "number") and
  .committed_micros > 0 and
  .committed_micros < 1350000000
) |
.committed_micros
