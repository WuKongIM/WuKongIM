---
status: superseded
superseded_by: 0038-allow-accesskey-onboarding-with-oidc-fallback
---

# Use GitHub OIDC with separate cloud roles

Cloud account onboarding will establish GitHub as an OIDC identity provider and create separate least-privilege roles for provisioning and analysis. GitHub configuration will retain only non-secret identifiers such as role references and regions; workflows will exchange their GitHub identities for short-lived cloud credentials and will never accept a root-account password or long-lived access key. The provisioning role may manage tagged Simulation Run infrastructure, while the analysis role is limited to run inventory and the temporary network rule required for an Analysis Access Window.
