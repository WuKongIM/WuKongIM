# Review Agent CLI Flow

`internal/access/reviewagentcli` is a strict JSON control boundary. Every
control command accepts exactly one bounded JSON document on stdin. The sole
advisory-input exception is `normalize-review-result`, which accepts one
bounded model-authored Review result and emits its validated, normalized JSON
object.

```text
GitHub Actions job
  -> exact command + strict JSON request
  -> internal/app Review Agent operation
  -> one bounded JSON response

bounded model output
  -> normalize-review-result
  -> one validated ReviewResult JSON response
```

The CLI does not parse shell plans, arbitrary commands, paths, URLs, secrets,
or model instructions. Model-result normalization accepts only the contract's
single unambiguous JSON object. Errors are generic and never echo input or
credential material.
