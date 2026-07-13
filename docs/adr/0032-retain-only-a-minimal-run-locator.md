---
status: accepted
---

# Retain only a minimal Run Locator

Provisioning will retain for 90 days a minimal GitHub artifact that proves a Run Identity was created by a trusted workflow and identifies its provider, region, cloud-account hash, source revision, scenario digest, creation and expiry times, and provisioning workflow run. It contains no observability or diagnostic data and is not an evidence archive. Analysis validates this locator before querying every relevant tagged cloud resource type. Only a valid locator combined with an empty provider inventory may produce `released`; a missing locator produces `unknown_run` and cannot claim automatic destruction.
